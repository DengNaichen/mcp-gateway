package gateway

import (
	"gatewaylab/internal/registry"
	"gatewaylab/internal/scope"
)

type DiscoverRequest struct {
	Intent      string   `json:"intent" jsonschema:"Describe the capability needed for the current task, in natural language"`
	Context     string   `json:"context,omitempty" jsonschema:"Relevant known information and actions already attempted; avoid secrets"`
	Services    []string `json:"services,omitempty" jsonschema:"Optional service IDs to search within when the task names a service; leave empty to search every service"`
	Actor       string   `json:"actor,omitempty" jsonschema:"Local simulation only: developer or observer; ignored when authenticated; this is not authentication"`
	IncludeDemo bool     `json:"include_demo,omitempty"`
	SummaryOnly bool     `json:"summary_only,omitempty"`
}

type Selection struct {
	registry.Tool
	Reason string `json:"reason"`
}

type DiscoverResult struct {
	ID           string            `json:"id"`
	Status       string            `json:"status"`
	Model        string            `json:"model,omitempty"`
	Summary      string            `json:"summary"`
	Question     string            `json:"question,omitempty"`
	Tools        []Selection       `json:"tools"`
	Servers      []string          `json:"servers,omitempty"` // services chosen by hierarchical routing
	Eligible     int               `json:"eligible"`
	DurationMS   int64             `json:"duration_ms"`
	PromptTokens int               `json:"prompt_tokens"`
	OutputTokens int               `json:"output_tokens"`
	Scope        *scope.EventScope `json:"scope,omitempty"`
}

type InvokeRequest struct {
	ToolID    string         `json:"tool_id" jsonschema:"Registered tool ID returned by discover_tools"`
	Arguments map[string]any `json:"arguments" jsonschema:"Arguments conforming to the selected tool input_schema"`
	Fields    []string       `json:"fields,omitempty" jsonschema:"Optional dot-separated response fields to retain; arrays are traversed automatically"`
	Actor     string         `json:"actor,omitempty" jsonschema:"Local simulation only: developer or observer; ignored when authenticated"`
}

type InvokeResult struct {
	ID         string `json:"id"`
	ToolID     string `json:"tool_id"`
	Protocol   string `json:"protocol"`
	Data       any    `json:"data"`
	DurationMS int64  `json:"duration_ms"`
}
