package apitypes

// DiscoverRequest is sent to POST /api/discover.
type DiscoverRequest struct {
	Intent      string   `json:"intent"`
	Context     string   `json:"context,omitempty"`
	Services    []string `json:"services,omitempty"`
	Actor       string   `json:"actor,omitempty"`
	IncludeDemo bool     `json:"include_demo,omitempty"`
	SummaryOnly bool     `json:"summary_only,omitempty"`
	// SimScope simulates a repository scope from the control plane.
	SimScope *SimScope `json:"sim_scope,omitempty"`
}

// Selection is a tool chosen by discovery together with the routing reason.
type Selection struct {
	Tool
	Reason string `json:"reason"`
}

// DiscoverResult is returned by POST /api/discover and POST /api/setup/model.
type DiscoverResult struct {
	ID           string      `json:"id"`
	Status       string      `json:"status"`
	Model        string      `json:"model,omitempty"`
	Summary      string      `json:"summary"`
	Question     string      `json:"question,omitempty"`
	Tools        []Selection `json:"tools"`
	Servers      []string    `json:"servers,omitempty"`
	Eligible     int         `json:"eligible"`
	DurationMS   int64       `json:"duration_ms"`
	PromptTokens int         `json:"prompt_tokens"`
	OutputTokens int         `json:"output_tokens"`
	Scope        *EventScope `json:"scope,omitempty"`
}

// DiscoverSummaryResult is returned by POST /api/discover when summary_only is
// set: the same envelope, with tools reduced to capability summaries.
type DiscoverSummaryResult struct {
	ID           string       `json:"id"`
	Status       string       `json:"status"`
	Model        string       `json:"model,omitempty"`
	Summary      string       `json:"summary"`
	Question     string       `json:"question,omitempty"`
	Tools        []Capability `json:"tools"`
	Servers      []string     `json:"servers,omitempty"`
	Eligible     int          `json:"eligible"`
	DurationMS   int64        `json:"duration_ms"`
	PromptTokens int          `json:"prompt_tokens"`
	OutputTokens int          `json:"output_tokens"`
	Scope        *EventScope  `json:"scope,omitempty"`
}

// InvokeRequest is sent to POST /api/invoke.
type InvokeRequest struct {
	ToolID    string         `json:"tool_id"`
	Arguments map[string]any `json:"arguments"`
	Fields    []string       `json:"fields,omitempty"`
	Actor     string         `json:"actor,omitempty"`
	SimScope  *SimScope      `json:"sim_scope,omitempty"`
}

// InvokeResult is returned by POST /api/invoke.
type InvokeResult struct {
	ID         string `json:"id"`
	ToolID     string `json:"tool_id"`
	Protocol   string `json:"protocol"`
	Data       any    `json:"data"`
	DurationMS int64  `json:"duration_ms"`
}
