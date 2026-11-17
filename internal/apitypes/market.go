package apitypes

// MarketInput is a header, environment variable, or argument a user supplies
// when installing a market server.
type MarketInput struct {
	Key         string `json:"key"`
	Where       string `json:"where"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Format      string `json:"format,omitempty"`
}

// MarketServer is an installable MCP server listed in the market.
type MarketServer struct {
	ID          string        `json:"id"`
	ServerID    string        `json:"server_id"`
	Title       string        `json:"title"`
	Publisher   string        `json:"publisher"`
	Icon        string        `json:"icon,omitempty"`
	Homepage    string        `json:"homepage,omitempty"`
	Description string        `json:"description"`
	Transport   string        `json:"transport"`
	Runs        string        `json:"runs"`
	Endpoint    string        `json:"endpoint,omitempty"`
	Command     string        `json:"command,omitempty"`
	Args        []string      `json:"args,omitempty"`
	SignIn      bool          `json:"sign_in,omitempty"`
	Featured    bool          `json:"featured"`
	Installed   bool          `json:"installed"`
	Inputs      []MarketInput `json:"inputs"`
}

// MarketSearchResult is returned by GET /api/market.
type MarketSearchResult struct {
	Items         []MarketServer `json:"items"`
	RegistryError string         `json:"registry_error,omitempty"`
}

// InstallMarketRequest is sent to POST /api/market/install.
type InstallMarketRequest struct {
	ID     string            `json:"id"`
	Server *MarketServer     `json:"server,omitempty"`
	Inputs map[string]string `json:"inputs,omitempty"`
}

// InstallMarketResult is returned by POST /api/market/install.
type InstallMarketResult struct {
	Server    Server `json:"server"`
	ToolCount int    `json:"tool_count"`
	SyncError string `json:"sync_error,omitempty"`
}
