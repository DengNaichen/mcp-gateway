package apitypes

import (
	"encoding/json"
	"time"
)

// Health is returned by GET /api/health.
type Health struct {
	Status       string `json:"status"`
	Version      string `json:"version"`
	Channel      string `json:"channel"`
	Application  string `json:"application"`
	InstanceID   string `json:"instance_id"`
	DataIdentity string `json:"data_identity"`
}

// Event is one entry of the activity log.
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

// ConnectionCheck is the latest connectivity check of a source or the model
// (POST /api/setup/source).
type ConnectionCheck struct {
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	Detail    string    `json:"detail,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	ToolCount int       `json:"tool_count,omitempty"`
	Stale     bool      `json:"stale"`
}

// SetupStatus is returned by GET /api/setup/status.
type SetupStatus struct {
	Sources          int                        `json:"sources"`
	EnabledTools     int                        `json:"enabled_tools"`
	ToolsReady       bool                       `json:"tools_ready"`
	ModelConfigured  bool                       `json:"model_configured"`
	ModelReady       bool                       `json:"model_ready"`
	ModelCheck       *ConnectionCheck           `json:"model_check"`
	SourceChecks     map[string]ConnectionCheck `json:"source_checks"`
	AgentLastRequest *Event                     `json:"agent_last_request"`
}

// AccessInfo describes how the caller reached the control plane.
type AccessInfo struct {
	Shared bool   `json:"shared"`
	Actor  string `json:"actor"`
}

// PulseBucket aggregates gateway requests over a 15-minute window.
type PulseBucket struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Total int       `json:"total"`
	Error int       `json:"error"`
}

// Bootstrap is returned by GET /api/bootstrap: everything the control plane
// needs to render its first screen.
type Bootstrap struct {
	Registry              Registry       `json:"registry"`
	Settings              Settings       `json:"settings"`
	KeyConfigured         bool           `json:"key_configured"`
	CredentialsPersistent bool           `json:"credentials_persistent"`
	Profiles              []ModelProfile `json:"profiles"`
	Events                []Event        `json:"events"`
	Pulse                 []PulseBucket  `json:"pulse,omitempty"`
	MCPURL                string         `json:"mcp_url"`
	Setup                 SetupStatus    `json:"setup"`
	Scopes                ScopeOverview  `json:"scopes"`
	Access                AccessInfo     `json:"access"`
}

// Principal is an authenticated identity in shared mode.
type Principal struct {
	ID         string   `json:"id"`
	Groups     []string `json:"groups"`
	Servers    []string `json:"servers"`
	Tools      []string `json:"tools,omitempty"`
	AllowWrite bool     `json:"allow_write"`
	Admin      bool     `json:"admin"`
}

// AuthMe is returned by GET /api/auth/me.
type AuthMe struct {
	Authenticated bool      `json:"authenticated"`
	Shared        bool      `json:"shared"`
	Principal     Principal `json:"principal"`
}

// LoginRequest is sent to POST /api/auth/login.
type LoginRequest struct {
	Token string `json:"token"`
}

// LoginResult is returned by POST /api/auth/login.
type LoginResult struct {
	Principal Principal `json:"principal"`
}

// Error is the body of every non-2xx /api response.
type Error struct {
	Error             string `json:"error"`
	Detail            string `json:"detail,omitempty"`
	Code              string `json:"code,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}

// Acknowledgement bodies of mutation endpoints that return no resource.
type (
	Saved struct {
		Saved bool `json:"saved"`
	}
	Deleted struct {
		Deleted bool `json:"deleted"`
	}
	Cancelled struct {
		Cancelled bool `json:"cancelled"`
	}
	Published struct {
		Published bool `json:"published"`
	}
	RolledBack struct {
		RolledBack bool `json:"rolled_back"`
	}
	SignedOut struct {
		SignedOut bool `json:"signed_out"`
	}
)

// WebhookResult is returned by POST /api/maintenance/sources/{id}/webhook/{provider}.
type WebhookResult struct {
	Ignored   bool   `json:"ignored,omitempty"`
	Accepted  bool   `json:"accepted,omitempty"`
	Duplicate bool   `json:"duplicate,omitempty"`
	Pending   bool   `json:"pending,omitempty"`
	RunID     string `json:"run_id,omitempty"`
}
