package registry

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

type Registry struct {
	Servers []Server `json:"servers"`
	Tools   []Tool   `json:"tools"`
}
