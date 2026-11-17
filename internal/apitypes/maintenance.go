package apitypes

import (
	"encoding/json"
	"time"
)

// MaintenanceSource tells the maintainer how to keep one server's catalog current
// (PUT /api/maintenance/sources).
type MaintenanceSource struct {
	ServerID        string            `json:"server_id"`
	Files           []string          `json:"files"`
	URLs            []string          `json:"urls"`
	Instructions    string            `json:"instructions"`
	Enabled         bool              `json:"enabled"`
	AutoPublish     bool              `json:"auto_publish"`
	IntervalMinutes int               `json:"interval_minutes"`
	TimeoutSeconds  int               `json:"timeout_seconds"`
	Provider        string            `json:"provider,omitempty"`
	Model           string            `json:"model,omitempty"`
	Format          string            `json:"format,omitempty"`
	OperationIDs    []string          `json:"operation_ids,omitempty"`
	Repository      *RepositorySource `json:"repository,omitempty"`
	NextRunAt       time.Time         `json:"next_run_at"`
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

// MaintenanceRun is one maintenance attempt (POST /api/maintenance/sources/{id}/run).
type MaintenanceRun struct {
	ID              string             `json:"id"`
	ServerID        string             `json:"server_id"`
	Status          string             `json:"status"`
	Stage           string             `json:"stage"`
	CreatedAt       time.Time          `json:"created_at"`
	FinishedAt      *time.Time         `json:"finished_at,omitempty"`
	Fingerprint     string             `json:"fingerprint,omitempty"`
	Baseline        string             `json:"baseline"`
	Source          MaintenanceSource  `json:"source"`
	Server          Server             `json:"server"`
	Before          []Tool             `json:"before"`
	Tools           []Tool             `json:"tools"`
	Summary         string             `json:"summary,omitempty"`
	Error           string             `json:"error,omitempty"`
	Events          []string           `json:"events"`
	Artifacts       map[string]string  `json:"artifacts,omitempty"`
	Force           bool               `json:"force"`
	AcceptanceInput *AcceptanceSuite   `json:"acceptance_input,omitempty"`
	Acceptance      AcceptanceReport   `json:"acceptance"`
	Review          PublicationReview  `json:"review"`
	Trigger         MaintenanceTrigger `json:"trigger"`
	Provenance      SourceProvenance   `json:"provenance"`
	PublishedBy     string             `json:"published_by,omitempty"`
	PublishedAt     *time.Time         `json:"published_at,omitempty"`
	RolledBackBy    string             `json:"rolled_back_by,omitempty"`
	RolledBackAt    *time.Time         `json:"rolled_back_at,omitempty"`
}

// RunMaintenanceRequest is sent to POST /api/maintenance/sources/{id}/run.
type RunMaintenanceRequest struct {
	Force bool `json:"force"`
}

// AcceptanceSuite is the owner-authored acceptance suite of one source.
type AcceptanceSuite struct {
	ServerID  string         `json:"server_id"`
	Contracts []ContractCase `json:"contracts"`
	Routing   []RoutingCase  `json:"routing"`
}

type ContractCase struct {
	Name        string          `json:"name"`
	ToolID      string          `json:"tool_id"`
	Arguments   map[string]any  `json:"arguments"`
	Exchanges   []HTTPExchange  `json:"exchanges,omitempty"`
	Expected    json.RawMessage `json:"expected,omitempty"`
	ExpectError bool            `json:"expect_error,omitempty"`
	MCPResult   json.RawMessage `json:"mcp_result,omitempty"`
	MCPError    bool            `json:"mcp_error,omitempty"`
}

type HTTPExchange struct {
	Method          string              `json:"method"`
	Path            string              `json:"path"`
	Query           map[string][]string `json:"query,omitempty"`
	Headers         map[string]string   `json:"headers,omitempty"`
	Body            json.RawMessage     `json:"body,omitempty"`
	Status          int                 `json:"status"`
	ResponseHeaders map[string]string   `json:"response_headers,omitempty"`
	Response        json.RawMessage     `json:"response,omitempty"`
	Text            *string             `json:"text,omitempty"`
}

type RoutingCase struct {
	Name      string   `json:"name"`
	Intent    string   `json:"intent"`
	Context   string   `json:"context,omitempty"`
	Actor     string   `json:"actor,omitempty"`
	Expected  []string `json:"expected"`
	Forbidden []string `json:"forbidden,omitempty"`
	NoMatch   bool     `json:"no_match,omitempty"`
}

type AcceptanceResult struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Passed     bool   `json:"passed"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type AcceptanceReport struct {
	Configured         bool               `json:"configured"`
	Passed             bool               `json:"passed"`
	Fingerprint        string             `json:"fingerprint,omitempty"`
	RoutingFingerprint string             `json:"routing_fingerprint,omitempty"`
	Results            []AcceptanceResult `json:"results"`
	MissingCoverage    []string           `json:"missing_coverage"`
}

type ReviewReason struct {
	ToolID  string `json:"tool_id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type PublicationReview struct {
	Required bool           `json:"required"`
	Reasons  []ReviewReason `json:"reasons"`
}

// MaintenanceStatus is returned by GET /api/maintenance.
type MaintenanceStatus struct {
	Runtime map[string]any      `json:"runtime"`
	Sources []MaintenanceSource `json:"sources"`
	Runs    []MaintenanceRun    `json:"runs"`
}

// ToolChange is one tool difference between a run's baseline and its result.
type ToolChange struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Before *Tool  `json:"before,omitempty"`
	After  *Tool  `json:"after,omitempty"`
}

// MaintenanceRunDetails is returned by GET /api/maintenance/runs/{id}.
type MaintenanceRunDetails struct {
	Run     MaintenanceRun `json:"run"`
	Changes []ToolChange   `json:"changes"`
}

// MaintenanceArtifact is returned by GET /api/maintenance/runs/{id}/artifacts/{name}.
type MaintenanceArtifact struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}
