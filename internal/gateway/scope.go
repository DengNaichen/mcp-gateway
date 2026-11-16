package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/store"
	"gatewaylab/internal/workspace"
)

const (
	maxScopeObservations = 100
	maxJTICacheEntries   = 65536
)

type ScopeOverview struct {
	Enforce      bool           `json:"enforce"`
	HookBinPath  string         `json:"hook_bin_path"`
	Repositories []Repository   `json:"repositories"`
	Rules        []ScopeRule    `json:"rules"`
	Observed     []ObservedRepo `json:"observed"`
}

type Repository struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	GitCommonDirs []string `json:"git_common_dirs"`
	RootPaths     []string `json:"root_paths"`
	RemoteHint    string   `json:"remote_hint"`
	CreatedAt     string   `json:"created_at"`
}

type ServiceGrant struct {
	ServerID string   `json:"server_id"`
	Access   string   `json:"access"` // "rw" | "ro"
	ToolIDs  []string `json:"tool_ids"`
}

type ScopeRule struct {
	RepoKey   string         `json:"repo_key"`
	AgentKey  string         `json:"agent_key"`
	Grants    []ServiceGrant `json:"grants"`
	UpdatedAt string         `json:"updated_at"`
}

type ObservedRepo struct {
	Fingerprint  string `json:"fingerprint"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Root         string `json:"root"`
	GitCommonDir string `json:"git_common_dir"`
	RemoteHint   string `json:"remote_hint"`
	LastAgent    string `json:"last_agent"`
	LastSeen     string `json:"last_seen"`
	RequestCount int    `json:"request_count"`
}

type ScopeServerSummary struct {
	ServerID  string `json:"server_id"`
	Access    string `json:"access"`
	ToolCount int    `json:"tool_count"`
}

type ScopeAgentPreview struct {
	Agent           string               `json:"agent"`
	MatchedRepoKey  string               `json:"matched_repo_key"`
	MatchedAgentKey string               `json:"matched_agent_key"`
	SourceLabel     string               `json:"source_label"`
	ServerCount     int                  `json:"server_count"`
	ToolCount       int                  `json:"tool_count"`
	HasWrite        bool                 `json:"has_write"`
	Servers         []ScopeServerSummary `json:"servers"`
}

type ScopePreviewResponse struct {
	RepoKey string              `json:"repo_key"`
	Agents  []ScopeAgentPreview `json:"agents"`
}

type ScopeSnippetResponse struct {
	Agent        string `json:"agent"`
	ConfigPath   string `json:"config_path"`
	MCPURL       string `json:"mcp_url"`
	HookBinPath  string `json:"hook_bin_path"`
	Instructions string `json:"instructions"`
	Snippet      string `json:"snippet"`
}

type SimScope struct {
	RepoKey  string `json:"repo_key"`
	AgentKey string `json:"agent_key"`
}

type EventScope struct {
	Agent        string `json:"agent"`
	RepoID       string `json:"repo_id"`
	RepoName     string `json:"repo_name"`
	Kind         string `json:"kind,omitempty"`
	Root         string `json:"root"`
	GitCommonDir string `json:"git_common_dir,omitempty"`
	RemoteHint   string `json:"remote_hint,omitempty"`
	MatchedRule  string `json:"matched_rule"`
	Status       string `json:"status"`
}

type SaveRepositoryRequest struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	GitCommonDirs []string `json:"git_common_dirs"`
	RootPaths     []string `json:"root_paths"`
	RemoteHint    string   `json:"remote_hint"`
}

type scopePolicyState struct {
	Enforce      bool
	Repositories []Repository
	Rules        []ScopeRule
	Observed     []ObservedRepo
}

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

type scopeEvalMode uint8

const (
	scopeModeCompatUnscoped scopeEvalMode = iota
	scopeModeBypass
	scopeModeEnforced
)

type resolvedRequestScope struct {
	Mode  scopeEvalMode
	Rule  ScopeRule
	Event EventScope
}

type requestScopeKey struct{}
type internalScopeBypassKey struct{}

func withRequestScope(ctx context.Context, sc resolvedRequestScope) context.Context {
	return context.WithValue(ctx, requestScopeKey{}, sc)
}

func requestScopeFrom(ctx context.Context) (resolvedRequestScope, bool) {
	sc, ok := ctx.Value(requestScopeKey{}).(resolvedRequestScope)
	return sc, ok
}

func withInternalScopeBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, internalScopeBypassKey{}, true)
}

func hasInternalScopeBypass(ctx context.Context) bool {
	v, _ := ctx.Value(internalScopeBypassKey{}).(bool)
	return v
}

func scopeProblem(status int, code, message string) error {
	if code != "" && !strings.Contains(message, code) {
		message = code + ": " + message
	}
	return &appError{status: status, code: code, message: message}
}

func (g *Gateway) now() time.Time {
	if g.clock != nil {
		return g.clock()
	}
	return time.Now()
}

func (g *Gateway) isScopeEnforced() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.scope.Enforce
}

func (g *Gateway) isAdminCaller(ctx context.Context) bool {
	if g.access == nil {
		return true
	}
	p, ok := principalFrom(ctx)
	return ok && p.Admin
}

func (g *Gateway) HookBinPath() string {
	g.mu.RLock()
	p := g.hookBinPath
	g.mu.RUnlock()
	if p != "" {
		return p
	}
	return workspace.ResolveHookBinary("", g.dir)
}

func (g *Gateway) loadScopes() error {
	state := scopePolicyState{
		Repositories: []Repository{},
		Rules:        []ScopeRule{},
		Observed:     []ObservedRepo{},
	}

	parsed, err := store.ScopeState[json.RawMessage](g.db).Load()
	if err == nil {
		var mode struct {
			Enforce bool `json:"enforce"`
		}
		if err := json.Unmarshal(parsed, &mode); err != nil {
			return fmt.Errorf("decode scope_state: %w", err)
		}
		state.Enforce = mode.Enforce
	}
	repos, err := store.Repositories[Repository](g.db).List()
	if err != nil {
		return err
	}
	for _, repo := range repos {
		state.Repositories = append(state.Repositories, normalizeRepositorySlices(repo))
	}
	rules, err := store.ScopeRules[ScopeRule](g.db).List()
	if err != nil {
		return err
	}
	hasWildcardDefault := false
	for _, rule := range rules {
		rule = normalizeScopeRuleSlices(rule)
		if rule.RepoKey == "*" && rule.AgentKey == "*" {
			hasWildcardDefault = true
		}
		state.Rules = append(state.Rules, rule)
	}
	if !hasWildcardDefault {
		def := ScopeRule{
			RepoKey:   "*",
			AgentKey:  "*",
			Grants:    []ServiceGrant{},
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		state.Rules = append([]ScopeRule{def}, state.Rules...)
	}

	observed, err := store.ScopeObservations[ObservedRepo](g.db).List(maxScopeObservations)
	if err != nil {
		return err
	}
	state.Observed = observed

	sortRepositories(state.Repositories)
	sortScopeRules(state.Rules)
	g.scope = state
	return nil
}

func normalizeRepositorySlices(r Repository) Repository {
	if r.GitCommonDirs == nil {
		r.GitCommonDirs = []string{}
	}
	if r.RootPaths == nil {
		r.RootPaths = []string{}
	}
	return r
}

func normalizeScopeRuleSlices(r ScopeRule) ScopeRule {
	if r.Grants == nil {
		r.Grants = []ServiceGrant{}
	}
	for i := range r.Grants {
		if r.Grants[i].ToolIDs == nil {
			r.Grants[i].ToolIDs = []string{}
		}
	}
	return r
}

func sortRepositories(repos []Repository) {
	sort.Slice(repos, func(i, j int) bool {
		if !strings.EqualFold(repos[i].Name, repos[j].Name) {
			return strings.ToLower(repos[i].Name) < strings.ToLower(repos[j].Name)
		}
		return repos[i].ID < repos[j].ID
	})
}

func sortScopeRules(rules []ScopeRule) {
	rank := func(r ScopeRule) int {
		switch {
		case r.RepoKey == "*" && r.AgentKey == "*":
			return 0
		case r.RepoKey == "*":
			return 1
		case r.AgentKey == "*":
			return 2
		default:
			return 3
		}
	}
	sort.Slice(rules, func(i, j int) bool {
		ri, rj := rank(rules[i]), rank(rules[j])
		if ri != rj {
			return ri < rj
		}
		if rules[i].RepoKey != rules[j].RepoKey {
			return rules[i].RepoKey < rules[j].RepoKey
		}
		return rules[i].AgentKey < rules[j].AgentKey
	})
}

func (g *Gateway) ScopeOverview() ScopeOverview {
	g.mu.RLock()
	defer g.mu.RUnlock()

	repos := make([]Repository, len(g.scope.Repositories))
	for i, r := range g.scope.Repositories {
		repos[i] = Repository{
			ID:            r.ID,
			Name:          r.Name,
			GitCommonDirs: slices.Clone(r.GitCommonDirs),
			RootPaths:     slices.Clone(r.RootPaths),
			RemoteHint:    r.RemoteHint,
			CreatedAt:     r.CreatedAt,
		}
	}

	rules := make([]ScopeRule, len(g.scope.Rules))
	for i, rl := range g.scope.Rules {
		grants := make([]ServiceGrant, len(rl.Grants))
		for j, gr := range rl.Grants {
			grants[j] = ServiceGrant{
				ServerID: gr.ServerID,
				Access:   gr.Access,
				ToolIDs:  slices.Clone(gr.ToolIDs),
			}
		}
		rules[i] = ScopeRule{
			RepoKey:   rl.RepoKey,
			AgentKey:  rl.AgentKey,
			Grants:    grants,
			UpdatedAt: rl.UpdatedAt,
		}
	}

	observed := make([]ObservedRepo, 0, len(g.scope.Observed))
	for _, obs := range g.scope.Observed {
		if matchConfiguredRepoByAnchors(g.scope.Repositories, obs.Kind, obs.GitCommonDir, obs.Root) != nil {
			continue
		}
		observed = append(observed, obs)
	}

	hookBin := g.hookBinPath
	if hookBin == "" {
		hookBin = workspace.ResolveHookBinary("", g.dir)
	}

	return ScopeOverview{
		Enforce:      g.scope.Enforce,
		HookBinPath:  hookBin,
		Repositories: repos,
		Rules:        rules,
		Observed:     observed,
	}
}

func (g *Gateway) SetScopeMode(enforce bool) error {
	raw, err := json.Marshal(map[string]bool{"enforce": enforce})
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := store.ScopeState[json.RawMessage](g.db).Save(raw); err != nil {
		return fmt.Errorf("persist scope mode: %w", err)
	}
	g.scope.Enforce = enforce
	return nil
}

func (g *Gateway) SaveRepository(ctx context.Context, in SaveRepositoryRequest) (Repository, error) {
	trimmedPath := strings.TrimSpace(in.Path)
	if trimmedPath != "" {
		if g.access != nil {
			return Repository{}, problem(http.StatusForbidden, "共享模式不支持服务端探测本地路径，请使用已观测仓库或显式提交锚点路径", nil)
		}
		info, err := workspace.Identify(ctx, trimmedPath)
		if err != nil {
			return Repository{}, problem(http.StatusBadRequest, "无法识别该本地目录", err)
		}
		if info.Kind == workspace.KindGit {
			in.GitCommonDirs = append(in.GitCommonDirs, info.CommonDir)
			if strings.TrimSpace(in.RemoteHint) == "" && info.RemoteHint != "" {
				in.RemoteHint = info.RemoteHint
			}
		} else {
			if err := workspace.ValidateDirAnchor(info.Root); err != nil {
				return Repository{}, invalid(err.Error())
			}
			in.RootPaths = append(in.RootPaths, info.Root)
		}
		if strings.TrimSpace(in.Name) == "" {
			in.Name = info.Name
		}
	}

	commonDirs, err := cleanAndValidateAnchors(in.GitCommonDirs, g.access == nil, true)
	if err != nil {
		return Repository{}, invalid(err.Error())
	}
	rootPaths, err := cleanAndValidateAnchors(in.RootPaths, g.access == nil, false)
	if err != nil {
		return Repository{}, invalid(err.Error())
	}
	if len(commonDirs) == 0 && len(rootPaths) == 0 {
		return Repository{}, invalid("仓库绑定至少需要一个 Git CommonDir 或目录根路径")
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		if len(commonDirs) > 0 {
			if filepath.Base(commonDirs[0]) == ".git" {
				name = filepath.Base(filepath.Dir(commonDirs[0]))
			} else {
				name = strings.TrimSuffix(filepath.Base(commonDirs[0]), ".git")
			}
		} else if len(rootPaths) > 0 {
			name = filepath.Base(rootPaths[0])
		}
	}
	if name == "" || len(name) > 128 {
		return Repository{}, invalid("请提供 1–128 字符的仓库名称")
	}

	remoteHint := strings.TrimSpace(in.RemoteHint)
	if remoteHint != "" {
		if norm := workspace.NormalizeRemote(remoteHint); norm != "" {
			remoteHint = norm
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	id := strings.TrimSpace(in.ID)
	if id != "" {
		if id == "*" || !validID(id) {
			return Repository{}, invalid("仓库 ID 不合法")
		}
	} else {
		// If updating by exact anchor match on an existing repo when ID was omitted, check collision below.
		id = generateRepositoryID(name, commonDirs, rootPaths, g.scope.Repositories)
	}

	for _, existing := range g.scope.Repositories {
		if existing.ID == id {
			continue
		}
		for _, cd := range commonDirs {
			if slices.Contains(existing.GitCommonDirs, cd) {
				return Repository{}, problem(http.StatusConflict, fmt.Sprintf("该 Git 仓库已绑定为 %q (%s)", existing.Name, existing.ID), nil)
			}
		}
		for _, rp := range rootPaths {
			if slices.Contains(existing.RootPaths, rp) {
				return Repository{}, problem(http.StatusConflict, fmt.Sprintf("该目录已绑定为 %q (%s)", existing.Name, existing.ID), nil)
			}
		}
	}

	createdAt := g.now().UTC().Format(time.RFC3339)
	existingIdx := slices.IndexFunc(g.scope.Repositories, func(r Repository) bool { return r.ID == id })
	if existingIdx >= 0 && g.scope.Repositories[existingIdx].CreatedAt != "" {
		createdAt = g.scope.Repositories[existingIdx].CreatedAt
	}

	repo := Repository{
		ID:            id,
		Name:          name,
		GitCommonDirs: commonDirs,
		RootPaths:     rootPaths,
		RemoteHint:    remoteHint,
		CreatedAt:     createdAt,
	}
	raw, err := json.Marshal(repo)
	if err != nil {
		return Repository{}, err
	}
	if err := store.Repositories[json.RawMessage](g.db).Put(repo.ID, raw); err != nil {
		return Repository{}, fmt.Errorf("persist repository: %w", err)
	}

	nextRepos := slices.Clone(g.scope.Repositories)
	if existingIdx >= 0 {
		nextRepos[existingIdx] = repo
	} else {
		nextRepos = append(nextRepos, repo)
	}
	sortRepositories(nextRepos)
	g.scope.Repositories = nextRepos

	// Prune any observed entries that now match this bound repository.
	g.pruneBoundObservationsLocked()

	return repo, nil
}

func cleanAndValidateAnchors(paths []string, resolveLocalSymlinks bool, isGitCommonDir bool) ([]string, error) {
	out := []string{}
	for _, raw := range paths {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) || strings.ContainsRune(p, '\x00') {
			return nil, errors.New("仓库锚点必须是绝对路径")
		}
		cleaned := filepath.Clean(p)
		if resolveLocalSymlinks {
			if eval, err := filepath.EvalSymlinks(cleaned); err == nil {
				cleaned = filepath.Clean(eval)
			}
		}
		if err := workspace.ValidateDirAnchor(cleaned); err != nil {
			return nil, err
		}
		if isGitCommonDir && filepath.Base(cleaned) == ".git" {
			if err := workspace.ValidateDirAnchor(filepath.Dir(cleaned)); err != nil {
				return nil, err
			}
		}
		if !slices.Contains(out, cleaned) {
			out = append(out, cleaned)
		}
	}
	return out, nil
}

func generateRepositoryID(name string, commonDirs, rootPaths []string, existing []Repository) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" || len(base) > 64 {
		seed := name + "|" + strings.Join(commonDirs, ",") + "|" + strings.Join(rootPaths, ",")
		sum := sha256.Sum256([]byte(seed))
		base = "repo-" + hex.EncodeToString(sum[:4])
	}
	candidate := base
	for suffix := 2; slices.ContainsFunc(existing, func(r Repository) bool { return r.ID == candidate }); suffix++ {
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
	return candidate
}

func (g *Gateway) DeleteRepository(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || id == "*" || !validID(id) {
		return invalid("仓库 ID 无效")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	tx, err := g.db.Begin()
	if err != nil {
		return fmt.Errorf("begin delete repository tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := store.Repositories[Repository](tx).Delete(id); err != nil {
		return fmt.Errorf("delete repository: %w", err)
	}
	if err := store.ScopeRules[ScopeRule](tx).DeleteRepository(id); err != nil {
		return fmt.Errorf("delete repository rules: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete repository: %w", err)
	}
	committed = true

	g.scope.Repositories = slices.DeleteFunc(slices.Clone(g.scope.Repositories), func(r Repository) bool {
		return r.ID == id
	})
	g.scope.Rules = slices.DeleteFunc(slices.Clone(g.scope.Rules), func(rl ScopeRule) bool {
		return rl.RepoKey == id
	})
	return nil
}

func (g *Gateway) SaveScopeRule(in ScopeRule) (ScopeRule, error) {
	repoKey := strings.TrimSpace(in.RepoKey)
	agentKey := strings.ToLower(strings.TrimSpace(in.AgentKey))
	if repoKey == "" {
		return ScopeRule{}, invalid("repo_key 不能为空")
	}
	if agentKey != "*" && !workspace.IsKnownAgent(agentKey) {
		return ScopeRule{}, invalid("agent_key 必须是 * 或有效客户端标识 (codex, claude, cursor, opencode, pi)")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if repoKey != "*" && !slices.ContainsFunc(g.scope.Repositories, func(r Repository) bool { return r.ID == repoKey }) {
		return ScopeRule{}, invalid("指定的仓库不存在")
	}

	reg := g.Snapshot()
	normalizedGrants := make([]ServiceGrant, 0, len(in.Grants))
	seenServers := map[string]bool{}
	for _, gr := range in.Grants {
		sid := strings.TrimSpace(gr.ServerID)
		if sid == "" || seenServers[sid] {
			if seenServers[sid] {
				return ScopeRule{}, invalid("同一规则中不能重复配置相同服务: " + sid)
			}
			return ScopeRule{}, invalid("server_id 不能为空")
		}
		if !slices.ContainsFunc(reg.Servers, func(s Server) bool { return s.ID == sid }) {
			return ScopeRule{}, invalid("服务不存在: " + sid)
		}
		seenServers[sid] = true

		access := strings.ToLower(strings.TrimSpace(gr.Access))
		if access != "rw" && access != "ro" {
			return ScopeRule{}, invalid("access 必须是 rw 或 ro")
		}

		toolIDs := []string{}
		for _, tid := range gr.ToolIDs {
			tid = strings.TrimSpace(tid)
			if tid == "" || slices.Contains(toolIDs, tid) {
				continue
			}
			if !slices.ContainsFunc(reg.Tools, func(t Tool) bool { return t.ID == tid && t.ServerID == sid }) {
				return ScopeRule{}, invalid(fmt.Sprintf("工具 %s 不属于服务 %s", tid, sid))
			}
			toolIDs = append(toolIDs, tid)
		}
		normalizedGrants = append(normalizedGrants, ServiceGrant{
			ServerID: sid,
			Access:   access,
			ToolIDs:  toolIDs,
		})
	}

	rule := ScopeRule{
		RepoKey:   repoKey,
		AgentKey:  agentKey,
		Grants:    normalizedGrants,
		UpdatedAt: g.now().UTC().Format(time.RFC3339),
	}
	raw, err := json.Marshal(rule)
	if err != nil {
		return ScopeRule{}, err
	}
	if err := store.ScopeRules[json.RawMessage](g.db).Put(rule.RepoKey, rule.AgentKey, raw); err != nil {
		return ScopeRule{}, fmt.Errorf("persist scope rule: %w", err)
	}

	nextRules := slices.Clone(g.scope.Rules)
	idx := slices.IndexFunc(nextRules, func(r ScopeRule) bool {
		return r.RepoKey == rule.RepoKey && r.AgentKey == rule.AgentKey
	})
	if idx >= 0 {
		nextRules[idx] = rule
	} else {
		nextRules = append(nextRules, rule)
	}
	sortScopeRules(nextRules)
	g.scope.Rules = nextRules
	return rule, nil
}

func (g *Gateway) DeleteScopeRule(repoKey, agentKey string) error {
	repoKey = strings.TrimSpace(repoKey)
	agentKey = strings.ToLower(strings.TrimSpace(agentKey))
	if repoKey == "" || agentKey == "" {
		return invalid("需要提供 repo_key 与 agent_key")
	}
	if agentKey != "*" && !workspace.IsKnownAgent(agentKey) {
		return invalid("agent_key 无效")
	}

	if repoKey == "*" && agentKey == "*" {
		_, err := g.SaveScopeRule(ScopeRule{
			RepoKey:  "*",
			AgentKey: "*",
			Grants:   []ServiceGrant{},
		})
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if err := store.ScopeRules[ScopeRule](g.db).Delete(repoKey, agentKey); err != nil {
		return fmt.Errorf("delete scope rule: %w", err)
	}
	g.scope.Rules = slices.DeleteFunc(slices.Clone(g.scope.Rules), func(r ScopeRule) bool {
		return r.RepoKey == repoKey && r.AgentKey == agentKey
	})
	return nil
}

func (g *Gateway) PreviewScope(ctx context.Context, repoKey, actor string) (ScopePreviewResponse, error) {
	repoKey = strings.TrimSpace(repoKey)
	if repoKey == "" {
		repoKey = "*"
	}
	actor = requestActor(ctx, actor)

	g.mu.RLock()
	if repoKey != "*" && !slices.ContainsFunc(g.scope.Repositories, func(r Repository) bool { return r.ID == repoKey }) {
		g.mu.RUnlock()
		return ScopePreviewResponse{}, invalid("指定的仓库不存在")
	}
	rules := slices.Clone(g.scope.Rules)
	reg := g.Snapshot()
	g.mu.RUnlock()

	enabledServers := map[string]Server{}
	for _, s := range reg.Servers {
		if s.Enabled && !s.Demo {
			enabledServers[s.ID] = s
		}
	}

	previews := make([]ScopeAgentPreview, 0, len(workspace.KnownAgents))
	for _, agent := range workspace.KnownAgents {
		matched := resolvePrecedenceRule(rules, repoKey != "*", repoKey, agent)
		srvSummaries := make([]ScopeServerSummary, 0, len(matched.Grants))
		totalTools := 0
		hasWrite := false

		for _, gr := range matched.Grants {
			if _, ok := enabledServers[gr.ServerID]; !ok {
				continue
			}
			tCount := 0
			for _, tool := range reg.Tools {
				if tool.ServerID != gr.ServerID {
					continue
				}
				if !g.identityAllowsTool(ctx, tool, actor) {
					continue
				}
				if !grantAllowsTool(matched, tool) {
					continue
				}
				tCount++
				if !tool.ReadOnly {
					hasWrite = true
				}
			}
			if tCount > 0 {
				srvSummaries = append(srvSummaries, ScopeServerSummary{
					ServerID:  gr.ServerID,
					Access:    gr.Access,
					ToolCount: tCount,
				})
				totalTools += tCount
			}
		}

		previews = append(previews, ScopeAgentPreview{
			Agent:           agent,
			MatchedRepoKey:  matched.RepoKey,
			MatchedAgentKey: matched.AgentKey,
			SourceLabel:     formatRuleSourceLabel(matched),
			ServerCount:     len(srvSummaries),
			ToolCount:       totalTools,
			HasWrite:        hasWrite,
			Servers:         srvSummaries,
		})
	}

	return ScopePreviewResponse{
		RepoKey: repoKey,
		Agents:  previews,
	}, nil
}

func formatRuleSourceLabel(rule ScopeRule) string {
	if len(rule.Grants) == 0 {
		if rule.AgentKey != "*" {
			return "客户端例外"
		}
		if rule.RepoKey != "*" {
			return "仓库默认"
		}
		return "无权限"
	}
	if rule.AgentKey != "*" {
		return "客户端例外"
	}
	if rule.RepoKey != "*" {
		return "仓库默认"
	}
	return "全局默认"
}

func (g *Gateway) ScopeSnippet(agent string) (ScopeSnippetResponse, error) {
	spec, err := workspace.BuildSnippet(workspace.SnippetOptions{
		Agent:    agent,
		Endpoint: g.baseURL + "/mcp",
		BinPath:  g.HookBinPath(),
		DataDir:  g.dir,
	})
	if err != nil {
		return ScopeSnippetResponse{}, invalid(err.Error())
	}
	return ScopeSnippetResponse{
		Agent:        spec.Agent,
		ConfigPath:   spec.ConfigPath,
		MCPURL:       spec.MCPURL,
		HookBinPath:  spec.HookBinPath,
		Instructions: spec.Instructions,
		Snippet:      spec.Snippet,
	}, nil
}

// resolvePrecedenceRule evaluates whole-rule replacement precedence:
// - Matched repo R: (R, agent) > (R, *) > (*, agent) > (*, *)
// - Unconfigured repo: (*, agent) > (*, *)
func resolvePrecedenceRule(rules []ScopeRule, matchedRepo bool, repoID, agent string) ScopeRule {
	if agent == "" {
		agent = "*"
	}
	type coord struct{ repo, agent string }
	var candidates []coord
	if matchedRepo && repoID != "" && repoID != "*" {
		if agent != "*" {
			candidates = append(candidates, coord{repoID, agent})
		}
		candidates = append(candidates, coord{repoID, "*"})
	}
	if agent != "*" {
		candidates = append(candidates, coord{"*", agent})
	}
	candidates = append(candidates, coord{"*", "*"})

	for _, c := range candidates {
		for _, r := range rules {
			if r.RepoKey == c.repo && r.AgentKey == c.agent {
				return r
			}
		}
	}
	return ScopeRule{RepoKey: "*", AgentKey: "*", Grants: []ServiceGrant{}}
}

func grantAllowsTool(rule ScopeRule, t Tool) bool {
	for _, gr := range rule.Grants {
		if gr.ServerID != t.ServerID {
			continue
		}
		if gr.Access == "ro" && !t.ReadOnly {
			return false
		}
		if len(gr.ToolIDs) > 0 && !slices.Contains(gr.ToolIDs, t.ID) {
			return false
		}
		return true
	}
	return false
}

// matchConfiguredRepoByAnchors matches a workspace strictly by local CommonDir
// for Git repositories, or longest segment-boundary RootPath for non-Git
// directories. An unbound Git repository never falls through to inherit a
// parent non-Git RootPath anchor, and RemoteHint never authorizes access.
func matchConfiguredRepoByAnchors(repos []Repository, kind, commonDir, root string) *Repository {
	if kind == string(workspace.KindGit) && commonDir != "" {
		for i := range repos {
			if slices.Contains(repos[i].GitCommonDirs, commonDir) {
				return &repos[i]
			}
		}
		return nil
	}
	if root != "" {
		var best *Repository
		bestLen := -1
		for i := range repos {
			for _, rp := range repos[i].RootPaths {
				if workspace.HasPathPrefix(root, rp) && len(rp) > bestLen {
					best = &repos[i]
					bestLen = len(rp)
				}
			}
		}
		if best != nil {
			return best
		}
	}
	return nil
}

func (g *Gateway) pruneBoundObservationsLocked() {
	kept := make([]ObservedRepo, 0, len(g.scope.Observed))
	for _, obs := range g.scope.Observed {
		if matchConfiguredRepoByAnchors(g.scope.Repositories, obs.Kind, obs.GitCommonDir, obs.Root) != nil {
			if err := store.ScopeObservations[ObservedRepo](g.db).Delete(obs.Fingerprint); err != nil {
				slog.Debug("prune bound scope observation", "error", err)
			}
			continue
		}
		kept = append(kept, obs)
	}
	g.scope.Observed = kept
}

func (g *Gateway) recordObservation(info workspace.Info, agent string, now time.Time) {
	if info.Fingerprint == "" {
		return
	}
	stamp := now.UTC().Format(time.RFC3339)

	g.mu.Lock()
	defer g.mu.Unlock()

	// Double-check still unconfigured under write lock.
	if matchConfiguredRepoByAnchors(g.scope.Repositories, string(info.Kind), info.CommonDir, info.Root) != nil {
		return
	}

	idx := slices.IndexFunc(g.scope.Observed, func(o ObservedRepo) bool { return o.Fingerprint == info.Fingerprint })
	count := 1
	if idx >= 0 {
		count = g.scope.Observed[idx].RequestCount + 1
	}
	obs := ObservedRepo{
		Fingerprint:  info.Fingerprint,
		Name:         info.Name,
		Kind:         string(info.Kind),
		Root:         info.Root,
		GitCommonDir: info.CommonDir,
		RemoteHint:   info.RemoteHint,
		LastAgent:    agent,
		LastSeen:     stamp,
		RequestCount: count,
	}

	next := slices.DeleteFunc(slices.Clone(g.scope.Observed), func(o ObservedRepo) bool { return o.Fingerprint == info.Fingerprint })
	next = append([]ObservedRepo{obs}, next...)

	raw, err := json.Marshal(obs)
	if err == nil {
		if dbErr := store.ScopeObservations[json.RawMessage](g.db).Put(obs.Fingerprint, obs.LastSeen, raw); dbErr != nil {
			slog.Warn("persist scope observation", "error", dbErr)
		}
	}

	if len(next) > maxScopeObservations {
		evicted := next[maxScopeObservations:]
		next = next[:maxScopeObservations]
		for _, ev := range evicted {
			_ = store.ScopeObservations[ObservedRepo](g.db).Delete(ev.Fingerprint)
		}
	}
	g.scope.Observed = next
}

func (g *Gateway) resolveMCPToolCallScope(ctx context.Context, urlAgent, toolName string, rawArgs []byte) (context.Context, []byte, error) {
	urlAgent = strings.ToLower(strings.TrimSpace(urlAgent))
	if urlAgent != "" && !workspace.IsKnownAgent(urlAgent) {
		return ctx, rawArgs, scopeProblem(http.StatusForbidden, "scope_invalid", fmt.Sprintf("未知客户端标识 %q", urlAgent))
	}

	ctxField := workspace.CurrentContextField()
	argsHash, tokenVal, hasField, err := workspace.CanonicalArgsHashJSON(rawArgs, ctxField)
	if err != nil {
		if hasField || errors.Is(err, workspace.ErrTokenInvalid) {
			return ctx, rawArgs, scopeProblem(http.StatusForbidden, "scope_invalid", "上下文凭证格式无效")
		}
		return ctx, rawArgs, invalid("工具调用参数不是有效的 JSON 对象")
	}

	cleanArgs, stripErr := workspace.StripContextFieldsJSON(rawArgs)
	if stripErr != nil {
		cleanArgs = rawArgs
	}

	if !hasField {
		if g.isScopeEnforced() {
			return ctx, cleanArgs, scopeProblem(http.StatusForbidden, "scope_required", "当前网关已开启仓库隔离拦截，缺少客户端 Hook 签发的上下文凭证")
		}
		return withRequestScope(ctx, resolvedRequestScope{
			Mode: scopeModeCompatUnscoped,
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
		return ctx, cleanArgs, scopeProblem(http.StatusForbidden, "scope_invalid", err.Error())
	}
	if !g.jtiReplay.consume(claims.JTI, claims.EXP, now) {
		return ctx, cleanArgs, scopeProblem(http.StatusForbidden, "scope_invalid", "上下文凭证已被重放消费")
	}

	resolved, evalErr := g.evaluateVerifiedClaims(ctx, claims, now)
	if evalErr != nil {
		return ctx, cleanArgs, evalErr
	}
	return withRequestScope(ctx, resolved), cleanArgs, nil
}

func (g *Gateway) resolveRESTScope(ctx context.Context, r *http.Request, op string, rawBody []byte, sim *SimScope) (context.Context, error) {
	isAdmin := g.isAdminCaller(ctx)
	if sim != nil && !isAdmin {
		return ctx, scopeProblem(http.StatusForbidden, "scope_invalid", "非管理员身份禁止传入 sim_scope")
	}

	scopeToken := strings.TrimSpace(r.Header.Get("X-Gateway-Scope"))
	if scopeToken != "" {
		ctxField := workspace.CurrentContextField()
		var allowedHashes []string
		if len(strings.TrimSpace(string(rawBody))) > 0 {
			h, _, _, err := workspace.CanonicalArgsHashJSON(rawBody, ctxField)
			if err != nil {
				return ctx, scopeProblem(http.StatusForbidden, "scope_invalid", "无法校验请求体摘要")
			}
			allowedHashes = append(allowedHashes, h)
		} else {
			emptyHash, _ := workspace.CanonicalArgsHash(nil, ctxField)
			allowedHashes = append(allowedHashes, emptyHash)
			if op == "get_tool_schema" {
				if id := r.PathValue("id"); id != "" {
					if idHash, err := workspace.CanonicalArgsHash(map[string]any{"tool_id": id}, ctxField); err == nil {
						allowedHashes = append(allowedHashes, idHash)
					}
				}
			}
		}

		allowedOps := restAllowedOps(op)
		urlAgent := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("agent")))
		if urlAgent != "" && !workspace.IsKnownAgent(urlAgent) {
			return ctx, scopeProblem(http.StatusForbidden, "scope_invalid", "未知 agent 参数")
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
			return ctx, scopeProblem(http.StatusForbidden, "scope_invalid", verifyErr.Error())
		}
		if !g.jtiReplay.consume(claims.JTI, claims.EXP, now) {
			return ctx, scopeProblem(http.StatusForbidden, "scope_invalid", "X-Gateway-Scope 凭证已被重放消费")
		}
		resolved, err := g.evaluateVerifiedClaims(ctx, claims, now)
		if err != nil {
			return ctx, err
		}
		return withRequestScope(ctx, resolved), nil
	}

	if !isAdmin {
		if g.isScopeEnforced() {
			return ctx, scopeProblem(http.StatusForbidden, "scope_required", "已启用仓库隔离拦截，非管理员请求必须提供有效的 X-Gateway-Scope 凭证")
		}
		return withRequestScope(ctx, resolvedRequestScope{
			Mode: scopeModeCompatUnscoped,
			Event: EventScope{
				Status: "unscoped",
			},
		}), nil
	}

	if sim != nil {
		resolved, err := g.evaluateSimulatedScope(*sim)
		if err != nil {
			return ctx, err
		}
		return withRequestScope(ctx, resolved), nil
	}

	return withRequestScope(ctx, resolvedRequestScope{
		Mode: scopeModeBypass,
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

func (g *Gateway) evaluateVerifiedClaims(ctx context.Context, claims workspace.Claims, now time.Time) (resolvedRequestScope, error) {
	enforce := g.isScopeEnforced()
	if claims.Ambiguous {
		if enforce {
			return resolvedRequestScope{}, scopeProblem(http.StatusForbidden, "ambiguous_workspace", "当前多根工作区包含多个不同仓库，无法唯一定位作用域")
		}
		return resolvedRequestScope{
			Mode: scopeModeCompatUnscoped,
			Event: EventScope{
				Agent:  claims.Agent,
				Status: "ambiguous",
			},
		}, nil
	}

	info, err := workspace.Identify(ctx, claims.Cwd)
	if err != nil {
		return resolvedRequestScope{}, scopeProblem(http.StatusForbidden, "scope_invalid", fmt.Sprintf("工作目录不存在或无法访问: %v", err))
	}

	g.mu.RLock()
	repos := slices.Clone(g.scope.Repositories)
	rules := slices.Clone(g.scope.Rules)
	enforce = g.scope.Enforce
	g.mu.RUnlock()

	matchedRepo := matchConfiguredRepoByAnchors(repos, string(info.Kind), info.CommonDir, info.Root)
	if matchedRepo != nil {
		rule := resolvePrecedenceRule(rules, true, matchedRepo.ID, claims.Agent)
		mode := scopeModeCompatUnscoped
		status := "observed"
		if enforce {
			mode = scopeModeEnforced
			status = "verified"
		}
		return resolvedRequestScope{
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
	mode := scopeModeCompatUnscoped
	if enforce {
		mode = scopeModeEnforced
	}
	return resolvedRequestScope{
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

func (g *Gateway) evaluateSimulatedScope(sim SimScope) (resolvedRequestScope, error) {
	repoKey := strings.TrimSpace(sim.RepoKey)
	agentKey := strings.ToLower(strings.TrimSpace(sim.AgentKey))
	if agentKey != "" && agentKey != "*" && !workspace.IsKnownAgent(agentKey) {
		return resolvedRequestScope{}, invalid("sim_scope.agent_key 无效")
	}
	if repoKey == "" {
		// Admin full view in Playground.
		return resolvedRequestScope{
			Mode: scopeModeBypass,
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
		return resolvedRequestScope{
			Mode: scopeModeEnforced,
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
		return resolvedRequestScope{}, invalid("模拟的仓库不存在: " + repoKey)
	}
	repo := repos[idx]
	rule := resolvePrecedenceRule(rules, true, repo.ID, evalAgent)
	root := ""
	if len(repo.RootPaths) > 0 {
		root = repo.RootPaths[0]
	} else if len(repo.GitCommonDirs) > 0 {
		root = repo.GitCommonDirs[0]
	}
	return resolvedRequestScope{
		Mode: scopeModeEnforced,
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

func (g *Gateway) checkExecutionScope(ctx context.Context) error {
	if hasInternalScopeBypass(ctx) {
		return nil
	}
	if _, ok := requestScopeFrom(ctx); ok {
		return nil
	}
	if g.isScopeEnforced() {
		return scopeProblem(http.StatusForbidden, "scope_required", "当前网关已开启仓库隔离拦截，缺少有效的仓库作用域凭证")
	}
	return nil
}

func (g *Gateway) scopeAllowsTool(ctx context.Context, t Tool) bool {
	if hasInternalScopeBypass(ctx) {
		return true
	}
	if sc, ok := requestScopeFrom(ctx); ok {
		switch sc.Mode {
		case scopeModeBypass, scopeModeCompatUnscoped:
			return true
		case scopeModeEnforced:
			return grantAllowsTool(sc.Rule, t)
		default:
			return false
		}
	}
	return !g.isScopeEnforced()
}

func scopeEventForContext(ctx context.Context) EventScope {
	if hasInternalScopeBypass(ctx) {
		return EventScope{Status: "internal"}
	}
	if sc, ok := requestScopeFrom(ctx); ok {
		return sc.Event
	}
	return EventScope{Status: "unscoped"}
}

func (g *Gateway) registerScopes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/scopes", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, g.ScopeOverview())
	})
	mux.HandleFunc("PUT /api/scopes/mode", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Enforce bool `json:"enforce"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.SetScopeMode(in.Enforce); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]bool{"enforce": in.Enforce})
	})
	mux.HandleFunc("PUT /api/scopes/repositories", func(w http.ResponseWriter, r *http.Request) {
		var in SaveRepositoryRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		repo, err := g.SaveRepository(r.Context(), in)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, repo)
	})
	mux.HandleFunc("DELETE /api/scopes/repositories/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := g.DeleteRepository(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]bool{"deleted": true})
	})
	mux.HandleFunc("PUT /api/scopes/rules", func(w http.ResponseWriter, r *http.Request) {
		var in ScopeRule
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		rule, err := g.SaveScopeRule(in)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, rule)
	})
	mux.HandleFunc("DELETE /api/scopes/rules", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if err := g.DeleteScopeRule(q.Get("repo_key"), q.Get("agent_key")); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]bool{"deleted": true})
	})
	mux.HandleFunc("POST /api/scopes/preview", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RepoKey string `json:"repo_key"`
			Actor   string `json:"actor"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		out, err := g.PreviewScope(r.Context(), in.RepoKey, in.Actor)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /api/scopes/snippets", func(w http.ResponseWriter, r *http.Request) {
		out, err := g.ScopeSnippet(r.URL.Query().Get("agent"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	})
}
