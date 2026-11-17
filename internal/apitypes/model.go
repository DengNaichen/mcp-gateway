package apitypes

// Settings is the active routing model configuration.
type Settings struct {
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	MaxTools       int    `json:"max_tools"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// ModelProfile is a saved routing model configuration. API keys are write-only.
type ModelProfile struct {
	ID             string `json:"id"`
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	MaxTools       int    `json:"max_tools"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Active         bool   `json:"active"`
	KeyConfigured  bool   `json:"key_configured"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

// KeyUpdate carries a write-only API key change alongside model settings.
type KeyUpdate struct {
	APIKey   string `json:"api_key,omitempty"`
	ClearKey bool   `json:"clear_key,omitempty"`
}

// SaveSettingsRequest is sent to PUT /api/settings.
type SaveSettingsRequest struct {
	Settings
	KeyUpdate
}

// SaveModelProfileRequest is sent to PUT /api/model-profiles.
type SaveModelProfileRequest struct {
	ModelProfile
	KeyUpdate
}

// PingModelRequest is sent to POST /api/model/ping.
type PingModelRequest struct {
	Settings
	ProfileID string `json:"profile_id,omitempty"`
	KeyUpdate
}

// SetupModelRequest is sent to POST /api/setup/model.
type SetupModelRequest struct {
	Settings
	KeyUpdate
	Intent string `json:"intent,omitempty"`
}

// PingModelResult is returned by POST /api/model/ping.
type PingModelResult struct {
	OK         bool   `json:"ok"`
	Model      string `json:"model"`
	DurationMS int64  `json:"duration_ms"`
}
