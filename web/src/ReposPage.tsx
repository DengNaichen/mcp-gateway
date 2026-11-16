import { useEffect, useMemo, useState } from "react";
import { ArrowUpRight, Loader2, Plus, Terminal, Trash2 } from "lucide-react";
import {
  SCOPE_AGENTS,
  cleanDescriptionSummary,
  deleteScopeRepository,
  deleteScopeRule,
  fetchHookSnippet,
  fetchScopes,
  getScopeOverview,
  previewScope,
  saveScopeRepository,
  saveScopeRule,
  scopeAgentLabel,
  setScopeMode,
  type Bootstrap,
  type ObservedRepo,
  type ScopeAgentPreview,
  type ScopeRepository,
  type ScopeServiceGrant,
  type ScopeSnippetResponse,
  type Server,
  type SimScope,
  type Tool,
} from "./types";
import { AlertBox, Badge, CopyButton, Modal, Switch } from "./ui";

function shortRemote(remote?: string): string {
  if (!remote) return "";
  return remote
    .replace(/^https?:\/\//i, "")
    .replace(/^ssh:\/\//i, "")
    .replace(/^git@/i, "")
    .replace(/\.git$/i, "");
}

function cloneGrants(grants?: ScopeServiceGrant[]): ScopeServiceGrant[] {
  return (grants || []).map((g) => ({
    server_id: g.server_id,
    access: g.access === "rw" ? "rw" : "ro",
    tool_ids: [...(g.tool_ids || [])],
  }));
}

function grantsEqual(a: ScopeServiceGrant[], b: ScopeServiceGrant[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i += 1) {
    if (a[i].server_id !== b[i].server_id || a[i].access !== b[i].access) {
      return false;
    }
    const ta = [...(a[i].tool_ids || [])].sort();
    const tb = [...(b[i].tool_ids || [])].sort();
    if (ta.length !== tb.length) return false;
    for (let j = 0; j < ta.length; j += 1) {
      if (ta[j] !== tb[j]) return false;
    }
  }
  return true;
}

export function HookSnippetModal({
  initialAgent = "claude",
  onClose,
  notify,
}: {
  initialAgent?: string;
  onClose: () => void;
  notify: (msg: string) => void;
}) {
  const [agent, setAgent] = useState<string>(initialAgent);
  const [cache, setCache] = useState<Record<string, ScopeSnippetResponse>>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (cache[agent]) {
      setError("");
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError("");
    fetchHookSnippet(agent)
      .then((res) => {
        if (cancelled) return;
        setCache((prev) => ({ ...prev, [agent]: res }));
      })
      .catch((e: any) => {
        if (cancelled) return;
        setError(e.message || "加载 Hook 配置片段失败");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [agent]);

  const current = cache[agent];

  return (
    <Modal
      title="手动配置客户端 Hook"
      onClose={onClose}
      maxWidth={680}
      footer={
        <button type="button" className="btn btn-primary" onClick={onClose}>
          完成
        </button>
      }
    >
      <div className="form-stack">
        <div className="segmented" style={{ alignSelf: "flex-start", flexWrap: "wrap" }}>
          {SCOPE_AGENTS.map((a) => (
            <button
              key={a.id}
              type="button"
              className={agent === a.id ? "active" : ""}
              onClick={() => setAgent(a.id)}
            >
              {a.label}
            </button>
          ))}
        </div>

        {error && <AlertBox tone="error" title={error} />}

        {loading && !current ? (
          <div className="empty-box" style={{ padding: "28px 16px" }}>
            <Loader2 size={18} className="spin" />
            <p>正在生成 {scopeAgentLabel(agent)} 配置片段…</p>
          </div>
        ) : current ? (
          <>
            <dl className="kv-grid">
              <div className="kv-item">
                <dt>目标配置文件</dt>
                <dd className="mono">{current.config_path || "—"}</dd>
              </div>
              <div className="kv-item">
                <dt>Hook 辅助程序</dt>
                <dd className="mono">{current.hook_bin_path || "—"}</dd>
              </div>
              <div className="kv-item" style={{ gridColumn: "1 / -1" }}>
                <dt>客户端专属 MCP 地址</dt>
                <dd
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    gap: 8,
                  }}
                >
                  <span className="mono">{current.mcp_url}</span>
                  <CopyButton
                    value={current.mcp_url}
                    label="复制 URL"
                    onCopied={() => notify("已复制客户端 MCP 地址")}
                  />
                </dd>
              </div>
            </dl>

            {current.instructions && (
              <p className="field-hint">{current.instructions}</p>
            )}

            <section className="navy-card">
              <div className="navy-card-header">
                <span className="navy-card-label">
                  {current.config_path || `${agent} hook snippet`}
                </span>
                <CopyButton
                  value={current.snippet}
                  label="复制片段"
                  variant="navy"
                  onCopied={() =>
                    notify(`已复制 ${scopeAgentLabel(agent)} Hook 配置`)
                  }
                />
              </div>
              <pre className="navy-pre">{current.snippet}</pre>
            </section>
          </>
        ) : null}
      </div>
    </Modal>
  );
}

function RepositoryFormModal({
  editing,
  observed,
  allowLocalPath,
  onClose,
  onSaved,
  notify,
}: {
  editing?: ScopeRepository | null;
  observed: ObservedRepo[];
  allowLocalPath: boolean;
  onClose: () => void;
  onSaved: (repo: ScopeRepository) => Promise<void>;
  notify: (msg: string) => void;
}) {
  const [name, setName] = useState(editing?.name || "");
  const [commonDirsText, setCommonDirsText] = useState(
    (editing?.git_common_dirs || []).join("\n"),
  );
  const [rootPathsText, setRootPathsText] = useState(
    (editing?.root_paths || []).join("\n"),
  );
  const [localPath, setLocalPath] = useState("");
  const [remoteHint, setRemoteHint] = useState(editing?.remote_hint || "");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  function fillFromObserved(obs: ObservedRepo) {
    setName(obs.name || "");
    if (obs.git_common_dir) {
      setCommonDirsText(obs.git_common_dir);
      setRootPathsText("");
    } else {
      setCommonDirsText("");
      setRootPathsText(obs.root || "");
    }
    setRemoteHint(obs.remote_hint || "");
    setError("");
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    const gitCommonDirs = commonDirsText
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean);
    const rootPaths = rootPathsText
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean);
    const trimmedLocalPath = localPath.trim();
    const trimmedName =
      name.trim() ||
      trimmedLocalPath.split("/").filter(Boolean).pop() ||
      gitCommonDirs[0]
        ?.replace(/\/\.git\/?$/, "")
        .split("/")
        .filter(Boolean)
        .pop() ||
      rootPaths[0]?.split("/").filter(Boolean).pop() ||
      "";

    if (!trimmedName) {
      setError("请填写仓库名称");
      return;
    }
    if (
      gitCommonDirs.length === 0 &&
      rootPaths.length === 0 &&
      (!allowLocalPath || !trimmedLocalPath)
    ) {
      setError("请至少填写一个 Git Common Dir 或目录根路径");
      return;
    }

    setSaving(true);
    setError("");
    try {
      const saved = await saveScopeRepository({
        id: editing?.id || "",
        name: trimmedName,
        path: allowLocalPath && trimmedLocalPath ? trimmedLocalPath : undefined,
        git_common_dirs: gitCommonDirs.length ? gitCommonDirs : undefined,
        root_paths: rootPaths.length ? rootPaths : undefined,
        remote_hint: remoteHint.trim() || undefined,
      });
      await onSaved(saved);
      notify(editing ? `已更新仓库 ${saved.name}` : `已绑定仓库 ${saved.name}`);
      onClose();
    } catch (err: any) {
      setError(err.message || "保存仓库失败");
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal
      title={editing ? "编辑仓库锚点" : "添加绑定仓库"}
      onClose={onClose}
      maxWidth={540}
      footer={
        <>
          <button type="button" className="btn" onClick={onClose}>
            取消
          </button>
          <button
            type="submit"
            form="repo-bind-form"
            className="btn btn-primary"
            disabled={saving}
          >
            {saving && <Loader2 size={14} className="spin" />}
            保存
          </button>
        </>
      }
    >
      <form id="repo-bind-form" className="form-stack" onSubmit={handleSubmit}>
        {error && <AlertBox tone="error" title={error} />}

        {!editing && observed.length > 0 && (
          <div className="field">
            <span className="field-label">
              从最近验证请求填入 <em>点击自动填入锚点</em>
            </span>
            <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
              {observed.slice(0, 6).map((obs) => (
                <button
                  key={obs.fingerprint}
                  type="button"
                  className="btn btn-sm"
                  onClick={() => fillFromObserved(obs)}
                >
                  {obs.name}
                </button>
              ))}
            </div>
          </div>
        )}

        <label className="field">
          <span className="field-label">仓库名称</span>
          <input
            className="input"
            placeholder="例如：mcp-gateway"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </label>

        {allowLocalPath && !editing && (
          <label className="field">
            <span className="field-label">
              本机仓库路径自动识别 <em>可选，自动解析 CommonDir</em>
            </span>
            <input
              className="input mono"
              placeholder="例如：/Users/me/Developer/mcp-gateway"
              value={localPath}
              onChange={(e) => setLocalPath(e.target.value)}
            />
          </label>
        )}

        <label className="field">
          <span className="field-label">
            Git Common Dir <em>每行一个，自动归并全部 git worktree</em>
          </span>
          <textarea
            className="textarea mono"
            rows={2}
            placeholder="/Users/me/Developer/mcp-gateway/.git"
            value={commonDirsText}
            onChange={(e) => setCommonDirsText(e.target.value)}
          />
        </label>

        <label className="field">
          <span className="field-label">
            非 Git 目录根路径 <em>可选，每行一个（禁止 /、$HOME、/tmp）</em>
          </span>
          <textarea
            className="textarea mono"
            rows={2}
            placeholder="/Users/me/Workspace/plain-folder"
            value={rootPathsText}
            onChange={(e) => setRootPathsText(e.target.value)}
          />
        </label>

        <label className="field">
          <span className="field-label">
            远端提示（Remote Hint） <em>仅展示提示，不自动跨路径授权</em>
          </span>
          <input
            className="input mono"
            placeholder="例如：github.com/DengNaichen/mcp-gateway"
            value={remoteHint}
            onChange={(e) => setRemoteHint(e.target.value)}
          />
        </label>
      </form>
    </Modal>
  );
}

function GrantRowsEditor({
  grants,
  servers,
  tools,
  emptyText,
  onChange,
}: {
  grants: ScopeServiceGrant[];
  servers: Server[];
  tools: Tool[];
  emptyText: string;
  onChange: (next: ScopeServiceGrant[]) => void;
}) {
  const [expandedServerId, setExpandedServerId] = useState<string | null>(null);

  if (grants.length === 0) {
    return (
      <div
        style={{
          padding: "16px 18px",
          fontSize: 12.5,
          color: "var(--ink-muted)",
        }}
      >
        {emptyText}
      </div>
    );
  }

  function updateGrant(
    serverId: string,
    updater: (g: ScopeServiceGrant) => ScopeServiceGrant,
  ) {
    onChange(
      grants.map((g) => (g.server_id === serverId ? updater(g) : g)),
    );
  }

  function removeServer(serverId: string) {
    if (expandedServerId === serverId) setExpandedServerId(null);
    onChange(grants.filter((g) => g.server_id !== serverId));
  }

  function toggleToolId(
    grant: ScopeServiceGrant,
    toolId: string,
    allIds: string[],
  ) {
    const current =
      grant.tool_ids && grant.tool_ids.length > 0
        ? [...grant.tool_ids]
        : [...allIds];
    const next = current.includes(toolId)
      ? current.filter((id) => id !== toolId)
      : [...current, toolId];
    const normalized = next.length >= allIds.length ? [] : next;
    updateGrant(grant.server_id, (g) => ({ ...g, tool_ids: normalized }));
  }

  return (
    <div>
      {grants.map((grant) => {
        const srv = servers.find((s) => s.id === grant.server_id);
        const srvTools = tools.filter(
          (t) => t.server_id === grant.server_id && t.enabled,
        );
        const allToolIds = srvTools.map((t) => t.id);
        const hasSubset = Boolean(grant.tool_ids && grant.tool_ids.length > 0);
        const subsetLabel = hasSubset
          ? `子集 ${grant.tool_ids!.length}/${srvTools.length}`
          : `全部 (${srvTools.length})`;
        const isExpanded = expandedServerId === grant.server_id;

        return (
          <div key={grant.server_id}>
            <div className="hairline-row-item">
              <div style={{ minWidth: 0, flex: 1 }}>
                <div className="cell-primary">
                  <span>{srv?.name || grant.server_id}</span>
                  {grant.access === "rw" ? (
                    <Badge tone="warning">RW</Badge>
                  ) : (
                    <span
                      style={{
                        fontSize: 12,
                        fontWeight: 400,
                        color: "var(--ink-muted)",
                      }}
                    >
                      RO
                    </span>
                  )}
                </div>
                <div className="cell-sub" style={{ WebkitLineClamp: 1 }}>
                  {cleanDescriptionSummary(srv?.description, grant.server_id)}
                </div>
              </div>

              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 8,
                  flexShrink: 0,
                }}
              >
                {srvTools.length > 0 && (
                  <button
                    type="button"
                    className="btn btn-sm"
                    onClick={() =>
                      setExpandedServerId(isExpanded ? null : grant.server_id)
                    }
                  >
                    {subsetLabel}
                  </button>
                )}

                <div className="segmented" role="group" aria-label="读写权限">
                  <button
                    type="button"
                    className={grant.access === "ro" ? "active" : ""}
                    onClick={() =>
                      updateGrant(grant.server_id, (g) => ({
                        ...g,
                        access: "ro",
                      }))
                    }
                  >
                    只读
                  </button>
                  <button
                    type="button"
                    className={grant.access === "rw" ? "active" : ""}
                    onClick={() =>
                      updateGrant(grant.server_id, (g) => ({
                        ...g,
                        access: "rw",
                      }))
                    }
                  >
                    读写
                  </button>
                </div>

                <button
                  type="button"
                  className="btn btn-ghost btn-sm"
                  aria-label={`移除服务 ${srv?.name || grant.server_id}`}
                  onClick={() => removeServer(grant.server_id)}
                >
                  <Trash2 size={13} />
                </button>
              </div>
            </div>

            {isExpanded && srvTools.length > 0 && (
              <div className="subset-drawer-panel">
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    gap: 8,
                    marginBottom: 2,
                  }}
                >
                  <span className="field-hint">
                    勾选允许暴露的工具子集（全选即不限子集）
                  </span>
                  {hasSubset && (
                    <button
                      type="button"
                      className="btn-link"
                      style={{ fontSize: 12 }}
                      onClick={() =>
                        updateGrant(grant.server_id, (g) => ({
                          ...g,
                          tool_ids: [],
                        }))
                      }
                    >
                      恢复全部工具
                    </button>
                  )}
                </div>

                {srvTools.map((tool) => {
                  const checked =
                    !hasSubset || grant.tool_ids!.includes(tool.id);
                  const blockedByRo =
                    grant.access === "ro" && !tool.read_only;
                  return (
                    <label
                      key={tool.id}
                      className="checkbox-field"
                      style={{
                        justifyContent: "space-between",
                        padding: "2px 0",
                        fontSize: 12.5,
                      }}
                    >
                      <span
                        style={{
                          display: "inline-flex",
                          alignItems: "center",
                          gap: 8,
                          minWidth: 0,
                        }}
                      >
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={() =>
                            toggleToolId(grant, tool.id, allToolIds)
                          }
                        />
                        <span
                          className="mono"
                          style={{
                            color: blockedByRo
                              ? "var(--ink-faint)"
                              : "var(--ink-strong)",
                            textDecoration: blockedByRo
                              ? "line-through"
                              : undefined,
                          }}
                        >
                          {tool.name}
                        </span>
                      </span>
                      <span
                        style={{
                          display: "inline-flex",
                          alignItems: "center",
                          gap: 6,
                          flexShrink: 0,
                        }}
                      >
                        {!tool.read_only ? (
                          <>
                            <Badge tone="warning">RW</Badge>
                            {blockedByRo && (
                              <span className="field-hint">只读下自动屏蔽</span>
                            )}
                          </>
                        ) : (
                          <span className="field-hint">RO</span>
                        )}
                      </span>
                    </label>
                  );
                })}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

export function ReposPage({
  state,
  initialRepoId,
  refresh,
  onSelectRepoRoute,
  onOpenPlaygroundScope,
  notify,
}: {
  state: Bootstrap;
  initialRepoId?: string | null;
  refresh: () => Promise<Bootstrap>;
  onSelectRepoRoute: (repoId: string | null) => void;
  onOpenPlaygroundScope: (payload: {
    actor?: string;
    simScope: SimScope;
  }) => void;
  notify: (msg: string) => void;
}) {
  const scopes = getScopeOverview(state);
  const [selectedKey, setSelectedKey] = useState<string>(() => {
    if (initialRepoId && scopes.repositories.some((r) => r.id === initialRepoId)) {
      return initialRepoId;
    }
    return scopes.repositories[0]?.id || "*";
  });

  const [togglingMode, setTogglingMode] = useState(false);
  const [hookModalOpen, setHookModalOpen] = useState(false);
  const [repoModalMode, setRepoModalMode] = useState<"add" | "edit" | null>(
    null,
  );
  const [bindingObsFp, setBindingObsFp] = useState<string | null>(null);

  // Draft grants for (selectedKey, "*")
  const [baseDraft, setBaseDraft] = useState<ScopeServiceGrant[]>([]);
  const [savingBase, setSavingBase] = useState(false);

  // Draft grants for (selectedKey, agent) overrides
  const [overrideDrafts, setOverrideDrafts] = useState<
    Record<string, ScopeServiceGrant[]>
  >({});
  const [savingAgent, setSavingAgent] = useState<string | null>(null);

  // 5-client effective preview
  const [previewActor, setPreviewActor] = useState<string>(
    state.access?.shared ? state.access.actor : "developer",
  );
  const [previewRows, setPreviewRows] = useState<ScopeAgentPreview[] | null>(
    null,
  );
  const [previewSeq, setPreviewSeq] = useState(0);
  const [liveScopes, setLiveScopes] = useState(scopes);

  useEffect(() => {
    setLiveScopes(getScopeOverview(state));
  }, [state.scopes]);

  useEffect(() => {
    let cancelled = false;
    fetchScopes()
      .then((fresh) => {
        if (!cancelled) setLiveScopes(fresh);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (
      initialRepoId &&
      liveScopes.repositories.some((r) => r.id === initialRepoId)
    ) {
      setSelectedKey(initialRepoId);
    } else if (
      selectedKey !== "*" &&
      !liveScopes.repositories.some((r) => r.id === selectedKey)
    ) {
      setSelectedKey(liveScopes.repositories[0]?.id || "*");
    }
  }, [initialRepoId, liveScopes.repositories]);

  const selectedRepo =
    selectedKey === "*"
      ? null
      : liveScopes.repositories.find((r) => r.id === selectedKey) || null;
  const effectiveRepoKey = selectedRepo ? selectedRepo.id : "*";

  const savedBaseRule = useMemo(
    () =>
      liveScopes.rules.find(
        (r) => r.repo_key === effectiveRepoKey && r.agent_key === "*",
      ) || null,
    [liveScopes.rules, effectiveRepoKey],
  );

  const savedAgentRules = useMemo(
    () =>
      liveScopes.rules.filter(
        (r) => r.repo_key === effectiveRepoKey && r.agent_key !== "*",
      ),
    [liveScopes.rules, effectiveRepoKey],
  );

  // Sync drafts when switching repo or when backend rules reload
  useEffect(() => {
    setBaseDraft(cloneGrants(savedBaseRule?.grants || []));
    const nextOverrides: Record<string, ScopeServiceGrant[]> = {};
    for (const rule of savedAgentRules) {
      nextOverrides[rule.agent_key] = cloneGrants(rule.grants);
    }
    setOverrideDrafts(nextOverrides);
  }, [effectiveRepoKey, JSON.stringify(liveScopes.rules)]);

  // Fetch authoritative 5-client preview from POST /api/scopes/preview
  useEffect(() => {
    let cancelled = false;
    previewScope(effectiveRepoKey, previewActor)
      .then((res) => {
        if (!cancelled) setPreviewRows(res.agents);
      })
      .catch(() => {
        if (!cancelled) setPreviewRows(null);
      });
    return () => {
      cancelled = true;
    };
  }, [
    effectiveRepoKey,
    previewActor,
    previewSeq,
    JSON.stringify(liveScopes.rules),
    state.registry.tools.length,
  ]);

  const realServers = useMemo(
    () => state.registry.servers.filter((s) => !s.demo && s.enabled),
    [state.registry.servers],
  );

  const globalDefaultRule = liveScopes.rules.find(
    (r) => r.repo_key === "*" && r.agent_key === "*",
  );
  const globalDefaultCount = globalDefaultRule?.grants.length || 0;

  const baseDirty = !grantsEqual(baseDraft, savedBaseRule?.grants || []);
  const unboundBaseServers = realServers.filter(
    (s) => !baseDraft.some((g) => g.server_id === s.id),
  );

  const overrideAgentKeys = Object.keys(overrideDrafts);
  const remainingOverrideAgents = SCOPE_AGENTS.filter(
    (a) => !overrideAgentKeys.includes(a.id),
  );

  function selectRepoKey(nextKey: string) {
    setSelectedKey(nextKey);
    onSelectRepoRoute(nextKey === "*" ? null : nextKey);
  }

  async function handleToggleEnforce() {
    if (togglingMode) return;
    setTogglingMode(true);
    try {
      const next = !liveScopes.enforce;
      await setScopeMode(next);
      await refresh();
      setPreviewSeq((s) => s + 1);
      notify(
        next
          ? "已启用仓库隔离拦截（缺失有效凭证返回 403 scope_required）"
          : "已关闭强制仓库隔离拦截",
      );
    } catch (e: any) {
      notify(e.message || "更新模式失败");
    } finally {
      setTogglingMode(false);
    }
  }

  async function handleQuickBindObserved(obs: ObservedRepo) {
    if (bindingObsFp) return;
    setBindingObsFp(obs.fingerprint);
    try {
      const saved = await saveScopeRepository({
        name: obs.name || obs.root.split("/").filter(Boolean).pop() || "repo",
        git_common_dirs: obs.git_common_dir ? [obs.git_common_dir] : undefined,
        root_paths:
          !obs.git_common_dir && obs.root ? [obs.root] : undefined,
        remote_hint: obs.remote_hint || undefined,
      });
      await refresh();
      selectRepoKey(saved.id);
      setPreviewSeq((s) => s + 1);
      notify(`已绑定仓库 ${saved.name}`);
    } catch (e: any) {
      notify(e.message || "绑定仓库失败");
    } finally {
      setBindingObsFp(null);
    }
  }

  async function handleDeleteRepo(repo: ScopeRepository) {
    if (!window.confirm(`确定移除仓库「${repo.name}」及其专属绑定规则吗？`)) {
      return;
    }
    try {
      await deleteScopeRepository(repo.id);
      await refresh();
      selectRepoKey("*");
      setPreviewSeq((s) => s + 1);
      notify(`已移除仓库 ${repo.name}`);
    } catch (e: any) {
      notify(e.message || "移除仓库失败");
    }
  }

  async function handleSaveBaseRule() {
    setSavingBase(true);
    try {
      await saveScopeRule({
        repo_key: effectiveRepoKey,
        agent_key: "*",
        grants: baseDraft,
      });
      await refresh();
      setPreviewSeq((s) => s + 1);
      notify("已保存默认服务规则");
    } catch (e: any) {
      notify(e.message || "保存规则失败");
    } finally {
      setSavingBase(false);
    }
  }

  async function handleResetBaseRule() {
    setSavingBase(true);
    try {
      await deleteScopeRule(effectiveRepoKey, "*");
      await refresh();
      setPreviewSeq((s) => s + 1);
      notify(
        effectiveRepoKey === "*"
          ? "全局默认已重置为 0 服务"
          : "已恢复继承全局默认规则",
      );
    } catch (e: any) {
      notify(e.message || "重置规则失败");
    } finally {
      setSavingBase(false);
    }
  }

  function handleAddAgentOverride(agentId: string) {
    if (!agentId || overrideDrafts[agentId]) return;
    setOverrideDrafts((prev) => ({
      ...prev,
      [agentId]: cloneGrants(baseDraft),
    }));
  }

  async function handleSaveAgentOverride(agentId: string) {
    setSavingAgent(agentId);
    try {
      await saveScopeRule({
        repo_key: effectiveRepoKey,
        agent_key: agentId,
        grants: overrideDrafts[agentId] || [],
      });
      await refresh();
      setPreviewSeq((s) => s + 1);
      notify(`已保存 ${scopeAgentLabel(agentId)} 例外规则`);
    } catch (e: any) {
      notify(e.message || "保存例外失败");
    } finally {
      setSavingAgent(null);
    }
  }

  async function handleDeleteAgentOverride(agentId: string) {
    const persisted = savedAgentRules.some((r) => r.agent_key === agentId);
    if (!persisted) {
      setOverrideDrafts((prev) => {
        const next = { ...prev };
        delete next[agentId];
        return next;
      });
      return;
    }
    setSavingAgent(agentId);
    try {
      await deleteScopeRule(effectiveRepoKey, agentId);
      await refresh();
      setPreviewSeq((s) => s + 1);
      notify(`${scopeAgentLabel(agentId)} 已恢复继承默认规则`);
    } catch (e: any) {
      notify(e.message || "删除例外失败");
    } finally {
      setSavingAgent(null);
    }
  }

  // Fallback local preview when POST /api/scopes/preview is in flight
  const fallbackPreviewRows: ScopeAgentPreview[] = useMemo(() => {
    return SCOPE_AGENTS.map((ag) => {
      const candidates =
        effectiveRepoKey === "*"
          ? [
              { rk: "*", ak: ag.id, label: "全局客户端默认" },
              { rk: "*", ak: "*", label: "全局默认" },
            ]
          : [
              { rk: effectiveRepoKey, ak: ag.id, label: "客户端例外" },
              { rk: effectiveRepoKey, ak: "*", label: "仓库默认" },
              { rk: "*", ak: ag.id, label: "全局客户端默认" },
              { rk: "*", ak: "*", label: "全局默认" },
            ];
      let matched = { rk: "*", ak: "*", label: "全局默认", grants: [] as ScopeServiceGrant[] };
      for (const c of candidates) {
        const found = liveScopes.rules.find(
          (r) => r.repo_key === c.rk && r.agent_key === c.ak,
        );
        if (found) {
          matched = { ...c, grants: found.grants };
          break;
        }
      }
      let toolCount = 0;
      let hasWrite = false;
      const serverSummaries = matched.grants.map((g) => {
        const srvTools = state.registry.tools.filter((t) => {
          if (t.server_id !== g.server_id || !t.enabled) return false;
          if (
            t.actors?.length &&
            !t.actors.includes("*") &&
            !t.actors.includes(previewActor)
          ) {
            return false;
          }
          if (g.tool_ids?.length && !g.tool_ids.includes(t.id)) return false;
          if (g.access === "ro" && !t.read_only) return false;
          return true;
        });
        toolCount += srvTools.length;
        if (g.access === "rw" && srvTools.some((t) => !t.read_only)) {
          hasWrite = true;
        }
        return {
          server_id: g.server_id,
          access: g.access,
          tool_count: srvTools.length,
        };
      });

      return {
        agent: ag.id,
        matched_repo_key: matched.rk,
        matched_agent_key: matched.ak,
        source_label: matched.grants.length === 0 ? "无权限" : matched.label,
        server_count: matched.grants.length,
        tool_count: toolCount,
        has_write: hasWrite,
        servers: serverSummaries,
      };
    });
  }, [effectiveRepoKey, liveScopes.rules, state.registry.tools, previewActor]);

  const displayPreview = previewRows || fallbackPreviewRows;

  return (
    <div>
      <div className="page-header">
        <div className="page-header-main">
          <h1>仓库绑定</h1>
        </div>

        <div className="page-header-actions">
          <div
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 8,
              paddingRight: 6,
              fontSize: 12.5,
              color: "var(--ink-body)",
            }}
          >
            <span
              className={`status-dot ${liveScopes.enforce ? "ok" : ""}`}
              aria-hidden="true"
            />
            <span>启用仓库隔离拦截</span>
            <Switch
              checked={liveScopes.enforce}
              label="启用仓库隔离拦截"
              disabled={togglingMode}
              onChange={() => void handleToggleEnforce()}
            />
          </div>

          <button
            type="button"
            className="btn"
            onClick={() => setHookModalOpen(true)}
          >
            <Terminal size={14} />
            手动配置 Hook
          </button>

          <button
            type="button"
            className="btn btn-primary"
            onClick={() => setRepoModalMode("add")}
          >
            <Plus size={14} />
            添加仓库
          </button>
        </div>
      </div>

      <div className="repos-split">
        {/* Left Column: Master Scope List (~280px Flush Card) */}
        <aside className="card" style={{ overflow: "hidden" }}>
          <button
            type="button"
            className={`repo-nav-row ${effectiveRepoKey === "*" ? "selected" : ""}`}
            onClick={() => selectRepoKey("*")}
          >
            <div style={{ minWidth: 0, flex: 1 }}>
              <div className="cell-primary" style={{ fontSize: 13 }}>
                全局默认
              </div>
              <div className="field-hint">未绑定仓库默认策略</div>
            </div>
            <span
              className="tnum"
              style={{ fontSize: 12, color: "var(--ink-muted)", flexShrink: 0 }}
            >
              {globalDefaultCount === 0
                ? "默认无服务"
                : `${globalDefaultCount} 服务`}
            </span>
          </button>

          <div className="repo-group-hdr">
            <span>已绑定仓库</span>
            <span className="mono tnum">{liveScopes.repositories.length}</span>
          </div>

          {liveScopes.repositories.length === 0 ? (
            <div
              style={{
                padding: "14px",
                fontSize: 12,
                color: "var(--ink-muted)",
              }}
            >
              暂无已绑定仓库
            </div>
          ) : (
            liveScopes.repositories.map((repo) => {
              const repoRule = liveScopes.rules.find(
                (r) => r.repo_key === repo.id && r.agent_key === "*",
              );
              const countText = repoRule
                ? `${repoRule.grants.length} 服务`
                : "继承全局";
              const sub =
                shortRemote(repo.remote_hint) ||
                (repo.git_common_dirs?.length ? "本地仓库" : "本地目录");

              return (
                <button
                  key={repo.id}
                  type="button"
                  className={`repo-nav-row ${effectiveRepoKey === repo.id ? "selected" : ""}`}
                  onClick={() => selectRepoKey(repo.id)}
                >
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <div
                      className="cell-primary"
                      style={{
                        fontSize: 13,
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                      }}
                    >
                      {repo.name}
                    </div>
                    <div
                      className="field-hint mono"
                      style={{
                        fontSize: 11.5,
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                      }}
                    >
                      {sub}
                    </div>
                  </div>
                  <span
                    className="tnum"
                    style={{
                      fontSize: 12,
                      color: "var(--ink-muted)",
                      flexShrink: 0,
                    }}
                  >
                    {countText}
                  </span>
                </button>
              );
            })
          )}

          {liveScopes.observed.length > 0 && (
            <>
              <div className="repo-group-hdr">
                <span>最近请求 · 未绑定</span>
                <span className="mono tnum">{liveScopes.observed.length}</span>
              </div>
              {liveScopes.observed.map((obs) => (
                <div
                  key={obs.fingerprint}
                  className="repo-nav-row"
                  style={{ cursor: "default" }}
                >
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <div
                      className="cell-primary"
                      style={{ fontSize: 12.5, gap: 6 }}
                    >
                      <span className="status-dot warn" />
                      <span
                        style={{
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                        }}
                        title={obs.root}
                      >
                        {obs.name || obs.root}
                      </span>
                    </div>
                    <div
                      className="field-hint mono"
                      style={{
                        fontSize: 11,
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                      }}
                    >
                      {obs.last_agent || "agent"}
                      {obs.request_count ? ` · ${obs.request_count} 次` : ""}
                    </div>
                  </div>
                  <button
                    type="button"
                    className="btn btn-sm"
                    disabled={bindingObsFp === obs.fingerprint}
                    onClick={() => void handleQuickBindObserved(obs)}
                  >
                    {bindingObsFp === obs.fingerprint ? "…" : "绑定"}
                  </button>
                </div>
              ))}
            </>
          )}
        </aside>

        {/* Right Column: Selected Scope Detail */}
        <div className="form-stack" style={{ minWidth: 0 }}>
          {/* 1. Anchor Header Card */}
          <section className="card">
            <div className="card-header" style={{ borderBottom: 0 }}>
              <div style={{ minWidth: 0, flex: 1 }}>
                <div
                  style={{
                    display: "flex",
                    alignItems: "baseline",
                    gap: 10,
                    flexWrap: "wrap",
                  }}
                >
                  <h2 style={{ fontSize: 16 }}>
                    {selectedRepo ? selectedRepo.name : "全局默认 (*)"}
                  </h2>
                  {selectedRepo?.remote_hint && (
                    <span className="field-hint mono">
                      {shortRemote(selectedRepo.remote_hint)}
                    </span>
                  )}
                </div>

                {selectedRepo ? (
                  <div
                    style={{
                      marginTop: 6,
                      display: "flex",
                      flexDirection: "column",
                      gap: 3,
                    }}
                  >
                    {(selectedRepo.git_common_dirs || []).map((dir) => (
                      <div
                        key={dir}
                        style={{
                          display: "flex",
                          alignItems: "center",
                          gap: 8,
                          fontSize: 12,
                          color: "var(--ink-muted)",
                        }}
                      >
                        <code style={{ wordBreak: "break-all" }}>{dir}</code>
                        <CopyButton
                          value={dir}
                          label="复制"
                          variant="ghost"
                          onCopied={() => notify("已复制 CommonDir 路径")}
                        />
                      </div>
                    ))}
                    {(selectedRepo.root_paths || []).map((p) => (
                      <div
                        key={p}
                        style={{
                          display: "flex",
                          alignItems: "center",
                          gap: 8,
                          fontSize: 12,
                          color: "var(--ink-muted)",
                        }}
                      >
                        <code style={{ wordBreak: "break-all" }}>{p}</code>
                        <CopyButton
                          value={p}
                          label="复制"
                          variant="ghost"
                          onCopied={() => notify("已复制目录路径")}
                        />
                      </div>
                    ))}
                  </div>
                ) : (
                  <p style={{ marginTop: 4 }}>
                    已通过有效 Hook 验证但尚未绑定的新仓库命中此策略（默认 0 服务）。
                  </p>
                )}
              </div>

              {selectedRepo && (
                <div style={{ display: "flex", gap: 6, flexShrink: 0 }}>
                  <button
                    type="button"
                    className="btn btn-sm"
                    onClick={() => setRepoModalMode("edit")}
                  >
                    编辑
                  </button>
                  <button
                    type="button"
                    className="btn btn-sm"
                    style={{ color: "var(--danger-ink)" }}
                    onClick={() => void handleDeleteRepo(selectedRepo)}
                  >
                    移除绑定
                  </button>
                </div>
              )}
            </div>
          </section>

          {/* 2. Default Services for (repoKey, "*") */}
          <section className="card" style={{ overflow: "hidden" }}>
            <div className="card-header">
              <div className="card-header-title">
                <h2>{selectedRepo ? "仓库默认服务" : "全局默认服务"}</h2>
              </div>

              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 8,
                  flexWrap: "wrap",
                }}
              >
                {unboundBaseServers.length > 0 && (
                  <select
                    aria-label="添加默认服务"
                    className="select"
                    style={{ height: 28, width: 156, fontSize: 12 }}
                    value=""
                    onChange={(e) => {
                      const srvId = e.target.value;
                      if (!srvId) return;
                      setBaseDraft((prev) => [
                        ...prev,
                        { server_id: srvId, access: "ro", tool_ids: [] },
                      ]);
                    }}
                  >
                    <option value="">+ 添加服务…</option>
                    {unboundBaseServers.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name}
                      </option>
                    ))}
                  </select>
                )}

                {selectedRepo && savedBaseRule && !baseDirty && (
                  <button
                    type="button"
                    className="btn btn-sm"
                    disabled={savingBase}
                    onClick={() => void handleResetBaseRule()}
                  >
                    恢复继承全局
                  </button>
                )}

                {!selectedRepo && globalDefaultCount > 0 && !baseDirty && (
                  <button
                    type="button"
                    className="btn btn-sm"
                    disabled={savingBase}
                    onClick={() => void handleResetBaseRule()}
                  >
                    清空为 0 服务
                  </button>
                )}

                {baseDirty && (
                  <>
                    <button
                      type="button"
                      className="btn btn-sm"
                      disabled={savingBase}
                      onClick={() =>
                        setBaseDraft(cloneGrants(savedBaseRule?.grants || []))
                      }
                    >
                      取消
                    </button>
                    <button
                      type="button"
                      className="btn btn-primary btn-sm"
                      disabled={savingBase}
                      onClick={() => void handleSaveBaseRule()}
                    >
                      {savingBase && <Loader2 size={12} className="spin" />}
                      保存规则
                    </button>
                  </>
                )}
              </div>
            </div>

            {selectedRepo && !savedBaseRule && !baseDirty && baseDraft.length === 0 ? (
              <div
                style={{
                  padding: "16px 18px",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: 12,
                  fontSize: 12.5,
                  color: "var(--ink-muted)",
                }}
              >
                <span>尚未单独设置仓库默认规则，当前继承全局默认（{globalDefaultCount} 服务）。</span>
                <button
                  type="button"
                  className="btn btn-sm"
                  onClick={() => void handleSaveBaseRule()}
                >
                  显式设为 0 服务
                </button>
              </div>
            ) : (
              <GrantRowsEditor
                grants={baseDraft}
                servers={state.registry.servers}
                tools={state.registry.tools}
                emptyText={
                  selectedRepo
                    ? "当前仓库默认开放 0 个上游服务。"
                    : "默认拒绝全部上游服务（新仓库默认 0 工具）。"
                }
                onChange={setBaseDraft}
              />
            )}
          </section>

          {/* 3. On-Demand Client Overrides (repoKey, agent) */}
          <section className="card" style={{ overflow: "hidden" }}>
            <div className="card-header">
              <div className="card-header-title">
                <h2>客户端例外</h2>
              </div>

              {remainingOverrideAgents.length > 0 && (
                <select
                  aria-label="添加客户端例外"
                  className="select"
                  style={{ height: 28, width: 170, fontSize: 12 }}
                  value=""
                  onChange={(e) => {
                    if (e.target.value) handleAddAgentOverride(e.target.value);
                  }}
                >
                  <option value="">+ 添加客户端例外…</option>
                  {remainingOverrideAgents.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.label} ({a.id})
                    </option>
                  ))}
                </select>
              )}
            </div>

            {overrideAgentKeys.length === 0 ? (
              <div
                style={{
                  padding: "16px 18px",
                  fontSize: 12.5,
                  color: "var(--ink-muted)",
                }}
              >
                无客户端例外，5 个客户端统一继承上方默认服务。
              </div>
            ) : (
              overrideAgentKeys.map((agentId) => {
                const draftGrants = overrideDrafts[agentId] || [];
                const persistedRule = savedAgentRules.find(
                  (r) => r.agent_key === agentId,
                );
                const isDirty =
                  !persistedRule ||
                  !grantsEqual(draftGrants, persistedRule.grants);
                const unboundForAgent = realServers.filter(
                  (s) => !draftGrants.some((g) => g.server_id === s.id),
                );
                const isSavingThis = savingAgent === agentId;

                return (
                  <div
                    key={agentId}
                    style={{ borderBottom: "1px solid var(--border)" }}
                  >
                    <div
                      style={{
                        padding: "10px 18px",
                        background: "var(--bg-subtle)",
                        borderBottom: "1px solid var(--border)",
                        display: "flex",
                        alignItems: "center",
                        justifyContent: "space-between",
                        gap: 10,
                        flexWrap: "wrap",
                      }}
                    >
                      <div
                        style={{
                          display: "inline-flex",
                          alignItems: "center",
                          gap: 8,
                        }}
                      >
                        <strong
                          style={{ fontSize: 13, color: "var(--ink-strong)" }}
                        >
                          {scopeAgentLabel(agentId)}
                        </strong>
                        <code
                          style={{ fontSize: 11.5, color: "var(--ink-muted)" }}
                        >
                          {agentId}
                        </code>
                      </div>

                      <div
                        style={{
                          display: "flex",
                          alignItems: "center",
                          gap: 8,
                          flexWrap: "wrap",
                        }}
                      >
                        {unboundForAgent.length > 0 && (
                          <select
                            aria-label={`为 ${agentId} 添加服务`}
                            className="select"
                            style={{ height: 26, width: 136, fontSize: 12 }}
                            value=""
                            onChange={(e) => {
                              const srvId = e.target.value;
                              if (!srvId) return;
                              setOverrideDrafts((prev) => ({
                                ...prev,
                                [agentId]: [
                                  ...(prev[agentId] || []),
                                  {
                                    server_id: srvId,
                                    access: "ro",
                                    tool_ids: [],
                                  },
                                ],
                              }));
                            }}
                          >
                            <option value="">+ 服务…</option>
                            {unboundForAgent.map((s) => (
                              <option key={s.id} value={s.id}>
                                {s.name}
                              </option>
                            ))}
                          </select>
                        )}

                        {isDirty && (
                          <button
                            type="button"
                            className="btn btn-primary btn-sm"
                            disabled={isSavingThis}
                            onClick={() => void handleSaveAgentOverride(agentId)}
                          >
                            {isSavingThis && (
                              <Loader2 size={12} className="spin" />
                            )}
                            保存例外
                          </button>
                        )}

                        <button
                          type="button"
                          className="btn btn-sm"
                          disabled={isSavingThis}
                          onClick={() =>
                            void handleDeleteAgentOverride(agentId)
                          }
                        >
                          恢复继承
                        </button>
                      </div>
                    </div>

                    <GrantRowsEditor
                      grants={draftGrants}
                      servers={state.registry.servers}
                      tools={state.registry.tools}
                      emptyText="此客户端在当前作用域禁止访问全部上游服务（0 工具）。"
                      onChange={(next) =>
                        setOverrideDrafts((prev) => ({
                          ...prev,
                          [agentId]: next,
                        }))
                      }
                    />
                  </div>
                );
              })
            )}
          </section>

          {/* 4. Five-Client Read-Only Effective Preview */}
          <section className="card" style={{ overflow: "hidden" }}>
            <div className="card-header">
              <div className="card-header-title">
                <h2>五端生效预览</h2>
              </div>

              <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                {!state.access?.shared && (
                  <select
                    aria-label="预览身份"
                    className="select"
                    style={{ height: 27, width: 120, fontSize: 12 }}
                    value={previewActor}
                    onChange={(e) => setPreviewActor(e.target.value)}
                  >
                    <option value="developer">developer</option>
                    <option value="observer">observer</option>
                  </select>
                )}
                <span className="field-hint">点击 ↗ 带入沙盒</span>
              </div>
            </div>

            <div>
              {displayPreview.map((row) => (
                <div key={row.agent} className="hairline-row-item">
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <div className="cell-primary">
                      <span>{scopeAgentLabel(row.agent)}</span>
                      <code
                        style={{
                          fontSize: 11.5,
                          fontWeight: 400,
                          color: "var(--ink-muted)",
                        }}
                      >
                        {row.agent}
                      </code>
                    </div>
                    <div className="field-hint">
                      规则来源：{row.source_label || "默认"}（
                      <span className="mono">
                        {row.matched_repo_key} · {row.matched_agent_key}
                      </span>
                      ）
                    </div>
                  </div>

                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 10,
                      flexShrink: 0,
                    }}
                  >
                    <span
                      className="tnum"
                      style={{ fontSize: 12.5, color: "var(--ink-muted)" }}
                    >
                      {row.server_count} 服务 · {row.tool_count} 工具
                    </span>

                    {row.has_write ? (
                      <Badge tone="warning">RW</Badge>
                    ) : (
                      <span
                        style={{
                          fontSize: 12,
                          color: "var(--ink-muted)",
                          minWidth: 22,
                          textAlign: "center",
                        }}
                      >
                        {row.tool_count > 0 ? "RO" : "—"}
                      </span>
                    )}

                    <button
                      type="button"
                      className="btn btn-sm"
                      title={`在路由沙盒中测试 ${scopeAgentLabel(row.agent)}`}
                      onClick={() =>
                        onOpenPlaygroundScope({
                          actor: previewActor,
                          simScope: {
                            repo_key: effectiveRepoKey,
                            agent_key: row.agent,
                          },
                        })
                      }
                    >
                      <ArrowUpRight size={13} />
                    </button>
                  </div>
                </div>
              ))}
            </div>
          </section>
        </div>
      </div>

      {hookModalOpen && (
        <HookSnippetModal
          onClose={() => setHookModalOpen(false)}
          notify={notify}
        />
      )}

      {repoModalMode && (
        <RepositoryFormModal
          editing={repoModalMode === "edit" ? selectedRepo : null}
          observed={liveScopes.observed}
          allowLocalPath={!state.access?.shared}
          onClose={() => setRepoModalMode(null)}
          onSaved={async (saved) => {
            await refresh();
            selectRepoKey(saved.id);
            setPreviewSeq((s) => s + 1);
          }}
          notify={notify}
        />
      )}
    </div>
  );
}
