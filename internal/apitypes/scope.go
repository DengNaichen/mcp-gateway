package apitypes

// ScopeOverview is returned by GET /api/scopes and embedded in the bootstrap.
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

// ServiceGrant grants one service with access "rw" or "ro" and an optional tool subset.
type ServiceGrant struct {
	ServerID string   `json:"server_id"`
	Access   string   `json:"access"`
	ToolIDs  []string `json:"tool_ids"`
}

// ScopeRule binds (repo_key, agent_key) to service grants (PUT /api/scopes/rules).
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

// ScopePreviewRequest is sent to POST /api/scopes/preview.
type ScopePreviewRequest struct {
	RepoKey string `json:"repo_key"`
	Actor   string `json:"actor"`
}

// ScopePreviewResponse is returned by POST /api/scopes/preview.
type ScopePreviewResponse struct {
	RepoKey string              `json:"repo_key"`
	Agents  []ScopeAgentPreview `json:"agents"`
}

// ScopeSnippetResponse is returned by GET /api/scopes/snippets.
type ScopeSnippetResponse struct {
	Agent        string `json:"agent"`
	ConfigPath   string `json:"config_path"`
	MCPURL       string `json:"mcp_url"`
	HookBinPath  string `json:"hook_bin_path"`
	Instructions string `json:"instructions"`
	Snippet      string `json:"snippet"`
}

// SimScope simulates a repository/agent scope for control-plane testing.
type SimScope struct {
	RepoKey  string `json:"repo_key"`
	AgentKey string `json:"agent_key"`
}

// EventScope is the scope decision recorded on a discovery or invocation.
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

// ScopeModeRequest is sent to PUT /api/scopes/mode and echoed back.
type ScopeModeRequest struct {
	Enforce bool `json:"enforce"`
}

// SaveRepositoryRequest is sent to PUT /api/scopes/repositories.
type SaveRepositoryRequest struct {
	ID            string   `json:"id,omitempty"`
	Name          string   `json:"name,omitempty"`
	Path          string   `json:"path,omitempty"`
	GitCommonDirs []string `json:"git_common_dirs,omitempty"`
	RootPaths     []string `json:"root_paths,omitempty"`
	RemoteHint    string   `json:"remote_hint,omitempty"`
}
