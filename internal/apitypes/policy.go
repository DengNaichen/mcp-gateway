package apitypes

type PolicyRule struct {
	ID        string   `json:"id,omitempty"`
	Effect    string   `json:"effect"`
	Enabled   bool     `json:"enabled"`
	RepoKey   string   `json:"repo_key"`
	ServiceID string   `json:"service_id"`
	ToolIDs   []string `json:"tool_ids,omitempty"`
	Access    string   `json:"access"`
	Actor     string   `json:"actor,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

type PolicyOverview struct {
	Enforce      bool         `json:"enforce"`
	BaselineFrom string       `json:"baseline_from"`
	Rules        []PolicyRule `json:"rules"`
}

type PolicyEvaluateRequest struct {
	ToolID string `json:"tool_id"`
	// RepoID simulates a configured repository. Use "*" for an unconfigured workspace.
	RepoID string `json:"repo_id,omitempty"`
	// Actor declares a simulated identity. An omitted actor uses the authenticated principal in shared mode.
	Actor string `json:"actor,omitempty"`
}

type PolicyTraceStep struct {
	Stage   string `json:"stage"`
	Matched bool   `json:"matched"`
	Effect  string `json:"effect,omitempty"`
	RuleID  string `json:"rule_id,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type PolicyEvaluateResult struct {
	Effect string            `json:"effect"`
	RuleID string            `json:"rule_id"`
	Reason string            `json:"reason,omitempty"`
	Trace  []PolicyTraceStep `json:"trace"`
	Trust  string            `json:"trust"`
}
