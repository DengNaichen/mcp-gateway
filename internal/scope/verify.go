package scope

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/workspace"
)

type jtiCache struct {
	mu      sync.Mutex
	entries map[string]int64
	ops     uint64
}

func newJTICache() *jtiCache {
	return &jtiCache{entries: make(map[string]int64)}
}

func (c *jtiCache) consume(jti string, exp int64, now time.Time) bool {
	if jti == "" {
		return false
	}
	nowUnix := now.Unix()
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ops++
	if c.ops%256 == 0 || len(c.entries) >= maxJTICacheEntries {
		for k, e := range c.entries {
			if nowUnix > e {
				delete(c.entries, k)
			}
		}
	}
	if existingExp, exists := c.entries[jti]; exists && nowUnix <= existingExp {
		return false
	}
	if len(c.entries) >= maxJTICacheEntries {
		// Fail closed rather than evicting unexpired JTIs within their TTL window.
		return false
	}
	c.entries[jti] = exp
	return true
}

type EvalMode uint8

const (
	ModeCompatUnscoped EvalMode = iota
	ModeBypass
	ModeEnforced
)

type ResolvedRequestScope struct {
	Mode  EvalMode
	Rule  ScopeRule
	Event EventScope
}

type requestScopeKey struct{}
type internalScopeBypassKey struct{}

func WithRequestScope(ctx context.Context, sc ResolvedRequestScope) context.Context {
	return context.WithValue(ctx, requestScopeKey{}, sc)
}

func RequestScopeFrom(ctx context.Context) (ResolvedRequestScope, bool) {
	sc, ok := ctx.Value(requestScopeKey{}).(ResolvedRequestScope)
	return sc, ok
}

func WithInternalBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, internalScopeBypassKey{}, true)
}

func HasInternalBypass(ctx context.Context) bool {
	v, _ := ctx.Value(internalScopeBypassKey{}).(bool)
	return v
}

func scopeProblem(kind error, code, message string) error {
	if code != "" && !strings.Contains(message, code) {
		message = code + ": " + message
	}
	return &Error{Kind: kind, Code: code, Message: message}
}

func (g *Service) ResolveMCPToolCallScope(ctx context.Context, urlAgent, toolName string, rawArgs []byte) (context.Context, []byte, error) {
	urlAgent = strings.ToLower(strings.TrimSpace(urlAgent))
	if urlAgent != "" && !workspace.IsKnownAgent(urlAgent) {
		return ctx, rawArgs, scopeProblem(ErrForbidden, "scope_invalid", fmt.Sprintf("未知客户端标识 %q", urlAgent))
	}

	ctxField := workspace.CurrentContextField()
	argsHash, tokenVal, hasField, err := workspace.CanonicalArgsHashJSON(rawArgs, ctxField)
	if err != nil {
		if hasField || errors.Is(err, workspace.ErrTokenInvalid) {
			return ctx, rawArgs, scopeProblem(ErrForbidden, "scope_invalid", "上下文凭证格式无效")
		}
		return ctx, rawArgs, invalid("工具调用参数不是有效的 JSON 对象")
	}

	cleanArgs, stripErr := workspace.StripContextFieldsJSON(rawArgs)
	if stripErr != nil {
		cleanArgs = rawArgs
	}

	if !hasField {
		if g.IsEnforced() {
			return ctx, cleanArgs, scopeProblem(ErrForbidden, "scope_required", "当前网关已开启仓库隔离拦截，缺少客户端 Hook 签发的上下文凭证")
		}
		return WithRequestScope(ctx, ResolvedRequestScope{
			Mode: ModeCompatUnscoped,
			Event: EventScope{
				Agent:  urlAgent,
				Status: "unscoped",
			},
		}), cleanArgs, nil
	}

	now := g.now()
	claims, err := workspace.VerifyToken(g.scopeKey, tokenVal, workspace.VerifyParams{
		Channel:    buildinfo.Current().Channel,
		URLAgent:   urlAgent,
		AllowedOps: []string{toolName},
		ArgsHash:   argsHash,
	}, now)
	if err != nil {
		return ctx, cleanArgs, scopeProblem(ErrForbidden, "scope_invalid", err.Error())
	}
	if !g.jtiReplay.consume(claims.JTI, claims.EXP, now) {
		return ctx, cleanArgs, scopeProblem(ErrForbidden, "scope_invalid", "上下文凭证已被重放消费")
	}

	resolved, evalErr := g.evaluateVerifiedClaims(ctx, claims, now)
	if evalErr != nil {
		return ctx, cleanArgs, evalErr
	}
	return WithRequestScope(ctx, resolved), cleanArgs, nil
}

// RESTRequest carries transport evidence already extracted by the HTTP adapter.
type RESTRequest struct {
	Token  string
	Agent  string
	ToolID string
}

func (g *Service) ResolveRESTScope(ctx context.Context, r RESTRequest, op string, rawBody []byte, sim *SimScope) (context.Context, error) {
	isAdmin := g.adminCaller(ctx)
	if sim != nil && !isAdmin {
		return ctx, scopeProblem(ErrForbidden, "scope_invalid", "非管理员身份禁止传入 sim_scope")
	}

	scopeToken := strings.TrimSpace(r.Token)
	if scopeToken != "" {
		ctxField := workspace.CurrentContextField()
		var allowedHashes []string
		if len(strings.TrimSpace(string(rawBody))) > 0 {
			h, _, _, err := workspace.CanonicalArgsHashJSON(rawBody, ctxField)
			if err != nil {
				return ctx, scopeProblem(ErrForbidden, "scope_invalid", "无法校验请求体摘要")
			}
			allowedHashes = append(allowedHashes, h)
		} else {
			emptyHash, _ := workspace.CanonicalArgsHash(nil, ctxField)
			allowedHashes = append(allowedHashes, emptyHash)
			if op == "get_tool_schema" {
				if id := r.ToolID; id != "" {
					if idHash, err := workspace.CanonicalArgsHash(map[string]any{"tool_id": id}, ctxField); err == nil {
						allowedHashes = append(allowedHashes, idHash)
					}
				}
			}
		}

		allowedOps := restAllowedOps(op)
		urlAgent := strings.ToLower(strings.TrimSpace(r.Agent))
		if urlAgent != "" && !workspace.IsKnownAgent(urlAgent) {
			return ctx, scopeProblem(ErrForbidden, "scope_invalid", "未知 agent 参数")
		}

		now := g.now()
		var (
			claims    workspace.Claims
			verifyErr error
		)
		for _, wantHash := range allowedHashes {
			claims, verifyErr = workspace.VerifyToken(g.scopeKey, scopeToken, workspace.VerifyParams{
				Channel:    buildinfo.Current().Channel,
				URLAgent:   urlAgent,
				AllowedOps: allowedOps,
				ArgsHash:   wantHash,
			}, now)
			if verifyErr == nil {
				break
			}
		}
		if verifyErr != nil {
			return ctx, scopeProblem(ErrForbidden, "scope_invalid", verifyErr.Error())
		}
		if !g.jtiReplay.consume(claims.JTI, claims.EXP, now) {
			return ctx, scopeProblem(ErrForbidden, "scope_invalid", "X-Gateway-Scope 凭证已被重放消费")
		}
		resolved, err := g.evaluateVerifiedClaims(ctx, claims, now)
		if err != nil {
			return ctx, err
		}
		return WithRequestScope(ctx, resolved), nil
	}

	if !isAdmin {
		if g.IsEnforced() {
			return ctx, scopeProblem(ErrForbidden, "scope_required", "已启用仓库隔离拦截，非管理员请求必须提供有效的 X-Gateway-Scope 凭证")
		}
		return WithRequestScope(ctx, ResolvedRequestScope{
			Mode: ModeCompatUnscoped,
			Event: EventScope{
				Status: "unscoped",
			},
		}), nil
	}

	if sim != nil {
		resolved, err := g.EvaluateSimulatedScope(*sim)
		if err != nil {
			return ctx, err
		}
		return WithRequestScope(ctx, resolved), nil
	}

	return WithRequestScope(ctx, ResolvedRequestScope{
		Mode: ModeBypass,
		Event: EventScope{
			Status: "bypass",
		},
	}), nil
}

func restAllowedOps(op string) []string {
	switch op {
	case "discover_tools", "discover":
		return []string{"discover_tools", "discover"}
	case "invoke_tool", "invoke":
		return []string{"invoke_tool", "invoke"}
	case "get_tool_schema", "schema":
		return []string{"get_tool_schema", "schema"}
	case "catalog":
		return []string{"catalog", "discover_tools"}
	default:
		return []string{op}
	}
}

func (g *Service) evaluateVerifiedClaims(ctx context.Context, claims workspace.Claims, now time.Time) (ResolvedRequestScope, error) {
	enforce := g.IsEnforced()
	if claims.Ambiguous {
		if enforce {
			return ResolvedRequestScope{}, scopeProblem(ErrForbidden, "ambiguous_workspace", "当前多根工作区包含多个不同仓库，无法唯一定位作用域")
		}
		return ResolvedRequestScope{
			Mode: ModeCompatUnscoped,
			Event: EventScope{
				Agent:  claims.Agent,
				Status: "ambiguous",
			},
		}, nil
	}

	info, err := workspace.Identify(ctx, claims.Cwd)
	if err != nil {
		return ResolvedRequestScope{}, scopeProblem(ErrForbidden, "scope_invalid", fmt.Sprintf("工作目录不存在或无法访问: %v", err))
	}

	g.mu.RLock()
	repos := slices.Clone(g.scope.Repositories)
	rules := slices.Clone(g.scope.Rules)
	enforce = g.scope.Enforce
	g.mu.RUnlock()

	matchedRepo := matchConfiguredRepoByAnchors(repos, string(info.Kind), info.CommonDir, info.Root)
	if matchedRepo != nil {
		rule := resolvePrecedenceRule(rules, true, matchedRepo.ID, claims.Agent)
		mode := ModeCompatUnscoped
		status := "observed"
		if enforce {
			mode = ModeEnforced
			status = "verified"
		}
		return ResolvedRequestScope{
			Mode: mode,
			Rule: rule,
			Event: EventScope{
				Agent:        claims.Agent,
				RepoID:       matchedRepo.ID,
				RepoName:     matchedRepo.Name,
				Kind:         string(info.Kind),
				Root:         info.Root,
				GitCommonDir: info.CommonDir,
				RemoteHint:   info.RemoteHint,
				MatchedRule:  rule.RepoKey + ":" + rule.AgentKey,
				Status:       status,
			},
		}, nil
	}

	// Verified hook evidence for an unconfigured workspace: record observation (<=100).
	g.recordObservation(info, claims.Agent, now)

	rule := resolvePrecedenceRule(rules, false, "*", claims.Agent)
	mode := ModeCompatUnscoped
	if enforce {
		mode = ModeEnforced
	}
	return ResolvedRequestScope{
		Mode: mode,
		Rule: rule,
		Event: EventScope{
			Agent:        claims.Agent,
			RepoID:       "",
			RepoName:     info.Name,
			Kind:         string(info.Kind),
			Root:         info.Root,
			GitCommonDir: info.CommonDir,
			RemoteHint:   info.RemoteHint,
			MatchedRule:  rule.RepoKey + ":" + rule.AgentKey,
			Status:       "unconfigured",
		},
	}, nil
}

func (g *Service) EvaluateSimulatedScope(sim SimScope) (ResolvedRequestScope, error) {
	repoKey := strings.TrimSpace(sim.RepoKey)
	agentKey := strings.ToLower(strings.TrimSpace(sim.AgentKey))
	if agentKey != "" && agentKey != "*" && !workspace.IsKnownAgent(agentKey) {
		return ResolvedRequestScope{}, invalid("sim_scope.agent_key 无效")
	}
	if repoKey == "" {
		// Admin full view in Playground.
		return ResolvedRequestScope{
			Mode: ModeBypass,
			Event: EventScope{
				Agent:  agentKey,
				Status: "bypass",
			},
		}, nil
	}

	g.mu.RLock()
	repos := slices.Clone(g.scope.Repositories)
	rules := slices.Clone(g.scope.Rules)
	g.mu.RUnlock()

	evalAgent := agentKey
	if evalAgent == "" {
		evalAgent = "*"
	}

	if repoKey == "*" {
		rule := resolvePrecedenceRule(rules, false, "*", evalAgent)
		return ResolvedRequestScope{
			Mode: ModeEnforced,
			Rule: rule,
			Event: EventScope{
				Agent:       agentKey,
				RepoID:      "*",
				RepoName:    "未绑定新仓库",
				MatchedRule: rule.RepoKey + ":" + rule.AgentKey,
				Status:      "simulated",
			},
		}, nil
	}

	idx := slices.IndexFunc(repos, func(r Repository) bool { return r.ID == repoKey })
	if idx < 0 {
		return ResolvedRequestScope{}, invalid("模拟的仓库不存在: " + repoKey)
	}
	repo := repos[idx]
	rule := resolvePrecedenceRule(rules, true, repo.ID, evalAgent)
	root := ""
	if len(repo.RootPaths) > 0 {
		root = repo.RootPaths[0]
	} else if len(repo.GitCommonDirs) > 0 {
		root = repo.GitCommonDirs[0]
	}
	return ResolvedRequestScope{
		Mode: ModeEnforced,
		Rule: rule,
		Event: EventScope{
			Agent:       agentKey,
			RepoID:      repo.ID,
			RepoName:    repo.Name,
			Root:        root,
			MatchedRule: rule.RepoKey + ":" + rule.AgentKey,
			Status:      "simulated",
		},
	}, nil
}

func (g *Service) CheckExecutionScope(ctx context.Context) error {
	if HasInternalBypass(ctx) {
		return nil
	}
	if _, ok := RequestScopeFrom(ctx); ok {
		return nil
	}
	if g.IsEnforced() {
		return scopeProblem(ErrForbidden, "scope_required", "当前网关已开启仓库隔离拦截，缺少有效的仓库作用域凭证")
	}
	return nil
}

func (g *Service) AllowsTool(ctx context.Context, t Tool) bool {
	if HasInternalBypass(ctx) {
		return true
	}
	if sc, ok := RequestScopeFrom(ctx); ok {
		switch sc.Mode {
		case ModeBypass, ModeCompatUnscoped:
			return true
		case ModeEnforced:
			return grantAllowsTool(sc.Rule, t)
		default:
			return false
		}
	}
	return !g.IsEnforced()
}

func EventForContext(ctx context.Context) EventScope {
	if HasInternalBypass(ctx) {
		return EventScope{Status: "internal"}
	}
	if sc, ok := RequestScopeFrom(ctx); ok {
		return sc.Event
	}
	return EventScope{Status: "unscoped"}
}
