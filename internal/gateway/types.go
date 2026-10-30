package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Server struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Kind         string            `json:"kind"`
	Endpoint     string            `json:"endpoint"`
	Headers      map[string]string `json:"headers,omitempty"`
	Enabled      bool              `json:"enabled"`
	Demo         bool              `json:"demo,omitempty"`
	Transport    string            `json:"transport,omitempty"`
	Command      string            `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	WorkDir      string            `json:"work_dir,omitempty"`
	RedactFields []string          `json:"redact_fields,omitempty"`
	RedactEmails bool              `json:"redact_emails,omitempty"`
}

type Tool struct {
	ID           string         `json:"id"`
	ServerID     string         `json:"server_id"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Tags         []string       `json:"tags"`
	InputSchema  map[string]any `json:"input_schema"`
	Enabled      bool           `json:"enabled"`
	ReadOnly     bool           `json:"read_only"`
	Actors       []string       `json:"actors"`
	Method       string         `json:"method,omitempty"`
	Path         string         `json:"path,omitempty"`
	Example      map[string]any `json:"example,omitempty"`
	Adapter      string         `json:"adapter,omitempty"`
	Revision     string         `json:"revision,omitempty"`
	HTTP         *HTTPMapping   `json:"http,omitempty"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
}

// HTTPMapping is the deterministic wire contract imported from OpenAPI.
// Argument groups keep identically named path/query/body parameters distinct.
type HTTPMapping struct {
	Parameters []HTTPParameter         `json:"parameters"`
	Body       bool                    `json:"body,omitempty"`
	Responses  map[string]HTTPResponse `json:"responses,omitempty"`
}
type HTTPParameter struct {
	Name    string `json:"name"`
	In      string `json:"in"`
	Style   string `json:"style"`
	Explode bool   `json:"explode"`
}
type HTTPResponse struct {
	MediaType string         `json:"media_type"`
	Schema    map[string]any `json:"schema,omitempty"`
}

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

type Settings struct {
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	Mode           string `json:"mode"`
	CandidateLimit int    `json:"candidate_limit"`
	MaxTools       int    `json:"max_tools"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type Registry struct {
	Servers []Server `json:"servers"`
	Tools   []Tool   `json:"tools"`
}

type DiscoverRequest struct {
	Intent      string `json:"intent" jsonschema:"Describe the capability needed for the current task, in natural language"`
	Context     string `json:"context,omitempty" jsonschema:"Relevant known information and actions already attempted; avoid secrets"`
	Actor       string `json:"actor,omitempty" jsonschema:"Local simulation only: developer or observer; ignored when authenticated; this is not authentication"`
	Mode        string `json:"mode,omitempty" jsonschema:"model for LLM selection, retrieval for keyword preview; defaults to gateway settings"`
	IncludeDemo bool   `json:"include_demo,omitempty"`
	SummaryOnly bool   `json:"summary_only,omitempty"`
}

type Candidate struct {
	Tool
	Score float64 `json:"score"`
}

type Selection struct {
	Tool
	Reason string `json:"reason"`
}

type DiscoverResult struct {
	ID           string      `json:"id"`
	Status       string      `json:"status"`
	Mode         string      `json:"mode"`
	Model        string      `json:"model,omitempty"`
	Summary      string      `json:"summary"`
	Question     string      `json:"question,omitempty"`
	Tools        []Selection `json:"tools"`
	Candidates   []Candidate `json:"candidates,omitempty"`
	Eligible     int         `json:"eligible"`
	DurationMS   int64       `json:"duration_ms"`
	PromptTokens int         `json:"prompt_tokens"`
	OutputTokens int         `json:"output_tokens"`
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

type Event struct {
	ID         string          `json:"id"`
	Time       time.Time       `json:"time"`
	Kind       string          `json:"kind"`
	Actor      string          `json:"actor"`
	Status     string          `json:"status"`
	Title      string          `json:"title"`
	DurationMS int64           `json:"duration_ms"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

type appError struct {
	status  int
	message string
	cause   error
}

func (e *appError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.message, e.cause)
	}
	return e.message
}
func (e *appError) Unwrap() error                           { return e.cause }
func problem(status int, message string, cause error) error { return &appError{status, message, cause} }
func invalid(message string) error                          { return problem(http.StatusBadRequest, message, nil) }
