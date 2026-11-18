package scope

import (
	"context"
	"database/sql"
	"slices"
	"sync"
	"time"

	"gatewaylab/internal/registry"
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

type ScopeSnippetResponse struct {
	Agent        string `json:"agent"`
	ConfigPath   string `json:"config_path"`
	MCPURL       string `json:"mcp_url"`
	HookBinPath  string `json:"hook_bin_path"`
	Instructions string `json:"instructions"`
	Snippet      string `json:"snippet"`
}

type SimScope struct {
	RepoKey string `json:"repo_key"`
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
	ID            string   `json:"id,omitempty"`
	Name          string   `json:"name,omitempty"`
	Path          string   `json:"path,omitempty"`
	GitCommonDirs []string `json:"git_common_dirs,omitempty"`
	RootPaths     []string `json:"root_paths,omitempty"`
	RemoteHint    string   `json:"remote_hint,omitempty"`
}

type scopePolicyState struct {
	Enforce      bool
	Repositories []Repository
	Rules        []ScopeRule
	Observed     []ObservedRepo
}

type Service struct {
	mu                        sync.RWMutex
	scope                     scopePolicyState
	db                        *sql.DB
	dir, baseURL, hookBinPath string
	scopeKey                  []byte
	jtiReplay                 *jtiCache
	clock                     func() time.Time
	shared                    bool
	snapshot                  func() registry.Registry
	adminCaller               func(context.Context) bool
}
type Options struct {
	DB           *sql.DB
	Dir, BaseURL string
	Key          []byte
	Clock        func() time.Time
	Shared       bool
	Snapshot     func() registry.Registry
	AdminCaller  func(context.Context) bool
}

func New(o Options) (*Service, error) {
	g := &Service{
		db:          o.DB,
		dir:         o.Dir,
		baseURL:     o.BaseURL,
		scopeKey:    slices.Clone(o.Key),
		clock:       o.Clock,
		shared:      o.Shared,
		snapshot:    o.Snapshot,
		adminCaller: o.AdminCaller,
		hookBinPath: workspace.ResolveHookBinary("", o.Dir),
		jtiReplay:   newJTICache(),
	}
	if err := g.loadScopes(); err != nil {
		return nil, err
	}
	return g, nil
}

type Tool = registry.Tool
type Server = registry.Server
