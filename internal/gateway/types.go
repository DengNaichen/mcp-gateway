package gateway

import (
	"fmt"
	"net/http"

	"gatewaylab/internal/activity"
	"gatewaylab/internal/model"
	"gatewaylab/internal/registry"
)

type Server = registry.Server
type Tool = registry.Tool
type HTTPMapping = registry.HTTPMapping
type HTTPParameter = registry.HTTPParameter
type HTTPResponse = registry.HTTPResponse
type Registry = registry.Registry

type RepositorySource struct {
	URL              string   `json:"url"`
	Ref              string   `json:"ref"`
	Files            []string `json:"files"`
	Project          string   `json:"project,omitempty"`
	EventsEnabled    bool     `json:"events_enabled"`
	WebhookSecretEnv string   `json:"webhook_secret_env,omitempty"`
}
type MaintenanceTrigger struct {
	Kind       string `json:"kind"`
	Provider   string `json:"provider,omitempty"`
	DeliveryID string `json:"delivery_id,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Ref        string `json:"ref,omitempty"`
}
type SourceProvenance struct {
	Repository string            `json:"repository,omitempty"`
	Ref        string            `json:"ref,omitempty"`
	Commit     string            `json:"commit,omitempty"`
	Documents  map[string]string `json:"documents"`
}

type Settings = model.Settings

// ModelProfile represents a saved Decisions model configuration.
// API keys remain in process memory only and are never persisted to SQLite.
type ModelProfile = model.ModelProfile

// PingModelResult is returned by POST /api/model/ping for direct model connectivity checks.
type PingModelResult = model.PingModelResult

type DiscoverRequest struct {
	Intent      string   `json:"intent" jsonschema:"Describe the capability needed for the current task, in natural language"`
	Context     string   `json:"context,omitempty" jsonschema:"Relevant known information and actions already attempted; avoid secrets"`
	Services    []string `json:"services,omitempty" jsonschema:"Optional service IDs to search within when the task names a service; leave empty to search every service"`
	Actor       string   `json:"actor,omitempty" jsonschema:"Local simulation only: developer or observer; ignored when authenticated; this is not authentication"`
	IncludeDemo bool     `json:"include_demo,omitempty"`
	SummaryOnly bool     `json:"summary_only,omitempty"`
}

type Selection struct {
	Tool
	Reason string `json:"reason"`
}

type DiscoverResult struct {
	ID           string      `json:"id"`
	Status       string      `json:"status"`
	Model        string      `json:"model,omitempty"`
	Summary      string      `json:"summary"`
	Question     string      `json:"question,omitempty"`
	Tools        []Selection `json:"tools"`
	Servers      []string    `json:"servers,omitempty"` // services chosen by hierarchical routing
	Eligible     int         `json:"eligible"`
	DurationMS   int64       `json:"duration_ms"`
	PromptTokens int         `json:"prompt_tokens"`
	OutputTokens int         `json:"output_tokens"`
	Scope        *EventScope `json:"scope,omitempty"`
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

type Event = activity.Event

type appError struct {
	status  int
	code    string
	message string
	cause   error
}

func (e *appError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.message, e.cause)
	}
	return e.message
}
func (e *appError) Unwrap() error { return e.cause }
func problem(status int, message string, cause error) error {
	return &appError{status: status, message: message, cause: cause}
}
func invalid(message string) error { return problem(http.StatusBadRequest, message, nil) }
