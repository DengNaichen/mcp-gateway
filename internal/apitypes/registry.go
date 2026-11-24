package apitypes

// Server is a registered tool source (PUT /api/servers, POST /api/setup/source).
type Server struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Kind          string            `json:"kind"`
	Endpoint      string            `json:"endpoint"`
	Headers       map[string]string `json:"headers,omitempty"`
	Enabled       bool              `json:"enabled"`
	Demo          bool              `json:"demo,omitempty"`
	Transport     string            `json:"transport,omitempty"`
	OAuth         bool              `json:"oauth,omitempty"`
	Command       string            `json:"command,omitempty"`
	CommandSHA256 string            `json:"command_sha256,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	WorkDir       string            `json:"work_dir,omitempty"`
	RedactFields  []string          `json:"redact_fields,omitempty"`
	RedactEmails  bool              `json:"redact_emails,omitempty"`
}

// Tool is a registered tool (PUT /api/tools).
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

// ToolSchema is the authorized tool returned by GET /api/tools/{id}/schema.
type ToolSchema struct {
	Tool
	NeedsApproval bool `json:"needs_approval,omitempty"`
}

// HTTPMapping is the deterministic HTTP call contract of a tool imported from OpenAPI.
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

// Registry is the full server and tool snapshot.
type Registry struct {
	Servers []Server `json:"servers"`
	Tools   []Tool   `json:"tools"`
}

// SyncServerResult is returned by POST /api/servers/{id}/sync.
type SyncServerResult struct {
	Imported int `json:"imported"`
}

// EnableToolsRequest is sent to POST /api/servers/{id}/tools.
type EnableToolsRequest struct {
	IDs []string `json:"ids"`
}

// Capability is the compact tool summary used by the catalog and summary discovery.
type Capability struct {
	ID            string   `json:"id"`
	ServerID      string   `json:"server_id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Tags          []string `json:"tags"`
	ReadOnly      bool     `json:"read_only"`
	Reason        string   `json:"reason,omitempty"`
	NeedsApproval bool     `json:"needs_approval,omitempty"`
}

type CatalogServer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ToolCount   int    `json:"tool_count"`
}

// Catalog is returned by GET /api/catalog.
type Catalog struct {
	Servers []CatalogServer `json:"servers"`
	Tools   []Capability    `json:"tools"`
}

// ImportOpenAPIRequest is sent to POST /api/servers/openapi. The server parses
// Spec (OpenAPI 3.0/3.1 JSON or YAML) or, when Spec is empty, downloads SpecURL.
type ImportOpenAPIRequest struct {
	Server       Server   `json:"server"`
	Spec         string   `json:"spec,omitempty"`
	SpecURL      string   `json:"spec_url,omitempty"`
	OperationIDs []string `json:"operation_ids,omitempty"`
}

// ImportOpenAPIResult is returned by POST /api/servers/openapi.
type ImportOpenAPIResult struct {
	Imported int `json:"imported"`
}

// SaveCredentialsRequest updates secrets for existing environment references.
// An empty value explicitly removes that reference's saved credential.
type SaveCredentialsRequest struct {
	Values map[string]string `json:"values"`
}
type CredentialStatus struct {
	Name        string `json:"name"`
	Configured  bool   `json:"configured"`
	Saved       bool   `json:"saved"`
	Environment bool   `json:"environment"`
}
type OAuthStatus struct {
	Enabled    bool `json:"enabled,omitempty"`
	Authorized bool `json:"authorized,omitempty"`
}
type OAuthUpgradeInfo struct {
	Title    string `json:"title"`
	Endpoint string `json:"endpoint"`
	Homepage string `json:"homepage,omitempty"`
}
type ServerCredentials struct {
	Persistent   bool               `json:"persistent"`
	Credentials  []CredentialStatus `json:"credentials"`
	OAuth        *OAuthStatus       `json:"oauth,omitempty"`
	OAuthUpgrade *OAuthUpgradeInfo  `json:"oauth_upgrade,omitempty"`
}
