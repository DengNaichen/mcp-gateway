package scope

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"gatewaylab/internal/registry"
	"gatewaylab/internal/store"
	"gatewaylab/internal/workspace"
)

func (g *Service) now() time.Time {
	if g.clock != nil {
		return g.clock()
	}
	return time.Now()
}

func (g *Service) IsEnforced() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.scope.Enforce
}

func (g *Service) HookBinPath() string {
	g.mu.RLock()
	p := g.hookBinPath
	g.mu.RUnlock()
	if p != "" {
		return p
	}
	return workspace.ResolveHookBinary("", g.dir)
}

func (g *Service) loadScopes() error {
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

func (g *Service) ScopeOverview() ScopeOverview {
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

func (g *Service) SetScopeMode(enforce bool) error {
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

func (g *Service) SaveRepository(ctx context.Context, in SaveRepositoryRequest) (Repository, error) {
	trimmedPath := strings.TrimSpace(in.Path)
	if trimmedPath != "" {
		if g.shared {
			return Repository{}, domainError(ErrForbidden, "共享模式不支持服务端探测本地路径，请使用已观测仓库或显式提交锚点路径", nil)
		}
		info, err := workspace.Identify(ctx, trimmedPath)
		if err != nil {
			return Repository{}, domainError(ErrInvalid, "无法识别该本地目录", err)
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

	commonDirs, err := cleanAndValidateAnchors(in.GitCommonDirs, !g.shared, true)
	if err != nil {
		return Repository{}, invalid(err.Error())
	}
	rootPaths, err := cleanAndValidateAnchors(in.RootPaths, !g.shared, false)
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
		if id == "*" || !registry.ValidID(id) {
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
				return Repository{}, domainError(ErrConflict, fmt.Sprintf("该 Git 仓库已绑定为 %q (%s)", existing.Name, existing.ID), nil)
			}
		}
		for _, rp := range rootPaths {
			if slices.Contains(existing.RootPaths, rp) {
				return Repository{}, domainError(ErrConflict, fmt.Sprintf("该目录已绑定为 %q (%s)", existing.Name, existing.ID), nil)
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

func (g *Service) DeleteRepository(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || id == "*" || !registry.ValidID(id) {
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

func (g *Service) SaveScopeRule(in ScopeRule) (ScopeRule, error) {
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

	reg := g.snapshot()
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

func (g *Service) DeleteScopeRule(repoKey, agentKey string) error {
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

func (g *Service) pruneBoundObservationsLocked() {
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

func (g *Service) recordObservation(info workspace.Info, agent string, now time.Time) {
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
