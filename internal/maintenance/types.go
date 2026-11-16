package maintenance

import "gatewaylab/internal/registry"

type Server = registry.Server
type Tool = registry.Tool
type Registry = registry.Registry
type HTTPMapping = registry.HTTPMapping
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
