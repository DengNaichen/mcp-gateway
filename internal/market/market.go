package market

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/fault"
	"gatewaylab/internal/i18n"
	"gatewaylab/internal/input"
	"gatewaylab/internal/registry"
)

// OAuthRemoteTarget describes a curated remote MCP server with browser OAuth
// that can replace a local stdio configuration for the same service.
type OAuthRemoteTarget struct {
	ID       string
	Title    string
	Endpoint string
	Homepage string
}

var stdioOAuthPackageAliases = map[string][]string{
	"notion":    {"@notionhq/notion-mcp-server", "notion-mcp-server"},
	"linear":    {"@linear/mcp-server", "mcp-linear", "linear-mcp"},
	"figma":     {"figma-developer-mcp", "@figma/mcp"},
	"sentry":    {"@sentry/mcp-server", "mcp-server-sentry"},
	"atlassian": {"mcp-atlassian"},
	"supabase":  {"@supabase/mcp-server-supabase"},
	"stripe":    {"@stripe/mcp"},
	"vercel":    {"@vercel/mcp"},
	"neon":      {"@neondatabase/mcp-server-neon"},
}

// MatchOAuthRemote returns the curated OAuth remote target if s is a stdio MCP
// service that corresponds to an official OAuth-capable market service.
func MatchOAuthRemote(s registry.Server) *OAuthRemoteTarget {
	if s.Demo || s.Kind != "mcp" || s.Transport != "stdio" {
		return nil
	}
	cmdAndArgs := strings.ToLower(strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " ")))
	for _, item := range curatedMarketServers() {
		if item.Transport != "http" || !item.SignIn || item.Endpoint == "" {
			continue
		}
		matched := strings.EqualFold(s.ID, item.ServerID)
		if !matched {
			for _, pkg := range stdioOAuthPackageAliases[item.ID] {
				if strings.Contains(cmdAndArgs, pkg) {
					matched = true
					break
				}
			}
		}
		if matched {
			return &OAuthRemoteTarget{
				ID:       item.ID,
				Title:    item.Title,
				Endpoint: item.Endpoint,
				Homepage: item.Homepage,
			}
		}
	}
	return nil
}

// MarketInput describes a single header, environment variable, or command argument
// a user supplies when installing a server from the MCP market.
type MarketInput struct {
	Key         string `json:"key"`
	Where       string `json:"where"` // "header", "env", "arg"
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Format      string `json:"format,omitempty"` // e.g. "Bearer {}"
}

// MarketServer describes an installable MCP server shown in the control plane market.
type MarketServer struct {
	ID          string        `json:"id"`
	ServerID    string        `json:"server_id"`
	Title       string        `json:"title"`
	Publisher   string        `json:"publisher"`
	Icon        string        `json:"icon,omitempty"`
	Homepage    string        `json:"homepage,omitempty"`
	Description string        `json:"description"`
	Transport   string        `json:"transport"` // "http" | "stdio"
	Runs        string        `json:"runs"`      // "远程" | "npx" | "uvx"
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
	Server    registry.Server `json:"server"`
	ToolCount int             `json:"tool_count"`
	SyncError string          `json:"sync_error,omitempty"`
}

var (
	marketRegistryURL   = "https://registry.modelcontextprotocol.io/v0/servers"
	marketHTTPClient    = &http.Client{Timeout: 5 * time.Second}
	marketPlaceholderRe = regexp.MustCompile(`\{[^{}]*\}`)
	marketNonIDRe       = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
	marketAllowedIcons  = struct {
		mu sync.RWMutex
		m  map[string]string
	}{m: map[string]string{}}
)

func ghAvatar(owner string) string {
	return "https://github.com/" + owner + ".png?size=96"
}

// curatedServerSpec captures the inputs for one entry in curatedMarketServers.
// Endpoint marks a remote SaaS server; Command marks a local stdio tool.
type curatedServerSpec struct {
	ID        string
	Title     string
	Publisher string
	Icon      string
	Homepage  string
	Desc      string
	Endpoint  string
	SignIn    bool
	Command   string
	Args      []string
	Inputs    []MarketInput
}

func curatedServer(s curatedServerSpec) MarketServer {
	m := MarketServer{
		ID:          s.ID,
		ServerID:    s.ID,
		Title:       s.Title,
		Publisher:   s.Publisher,
		Icon:        s.Icon,
		Homepage:    s.Homepage,
		Description: s.Desc,
		Featured:    true,
		Inputs:      s.Inputs,
	}
	if s.Endpoint != "" {
		m.Transport = "http"
		m.Runs = "远程"
		m.Endpoint = s.Endpoint
		m.SignIn = s.SignIn
	} else {
		m.Transport = "stdio"
		m.Runs = s.Command
		m.Command = s.Command
		m.Args = s.Args
	}
	if m.Inputs == nil {
		m.Inputs = []MarketInput{}
	}
	return m
}

// curatedMarketServers returns the curated catalog: 17 remote SaaS services
// plus 3 essential local tools (Playwright, Chrome DevTools, Filesystem).
// The catalog is rebuilt per call so descriptions and input labels follow the
// process-wide UI language instead of freezing the language at init time.
func curatedMarketServers() []MarketServer {
	return []MarketServer{
		curatedServer(curatedServerSpec{ID: "notion", Title: "Notion", Publisher: "Notion", Icon: ghAvatar("makenotion"), Homepage: "https://developers.notion.com/docs/mcp", Endpoint: "https://mcp.notion.com/mcp", Desc: i18n.T("market.catalog.notion.description"), SignIn: true}),
		curatedServer(curatedServerSpec{ID: "linear", Title: "Linear", Publisher: "Linear", Icon: ghAvatar("linear"), Homepage: "https://linear.app/docs/mcp", Endpoint: "https://mcp.linear.app/mcp", Desc: i18n.T("market.catalog.linear.description"), SignIn: true}),
		curatedServer(curatedServerSpec{ID: "figma", Title: "Figma", Publisher: "Figma", Icon: ghAvatar("figma"), Homepage: "https://help.figma.com/hc/en-us/articles/32132100833559", Endpoint: "https://mcp.figma.com/mcp", Desc: i18n.T("market.catalog.figma.description"), SignIn: true}),
		curatedServer(curatedServerSpec{ID: "github", Title: "GitHub", Publisher: "GitHub", Icon: ghAvatar("github"), Homepage: "https://github.com/github/github-mcp-server", Endpoint: "https://api.githubcopilot.com/mcp/", Desc: i18n.T("market.catalog.github.description"), SignIn: false, Inputs: []MarketInput{{Key: "Authorization", Where: "header", Label: "Personal Access Token", Description: i18n.T("market.catalog.github.token_description"), Placeholder: "github_pat_...", Secret: true, Required: true, Format: "Bearer {}"}}}),
		curatedServer(curatedServerSpec{ID: "sentry", Title: "Sentry", Publisher: "Sentry", Icon: ghAvatar("getsentry"), Homepage: "https://docs.sentry.io/product/sentry-mcp/", Endpoint: "https://mcp.sentry.dev/mcp", Desc: i18n.T("market.catalog.sentry.description"), SignIn: true}),
		curatedServer(curatedServerSpec{ID: "atlassian", Title: "Atlassian", Publisher: "Atlassian", Icon: ghAvatar("atlassian"), Homepage: "https://www.atlassian.com/platform/remote-mcp-server", Endpoint: "https://mcp.atlassian.com/v1/mcp", Desc: i18n.T("market.catalog.atlassian.description"), SignIn: true}),
		curatedServer(curatedServerSpec{ID: "supabase", Title: "Supabase", Publisher: "Supabase", Icon: ghAvatar("supabase"), Homepage: "https://supabase.com/docs/guides/getting-started/mcp", Endpoint: "https://mcp.supabase.com/mcp", Desc: i18n.T("market.catalog.supabase.description"), SignIn: true}),
		curatedServer(curatedServerSpec{ID: "stripe", Title: "Stripe", Publisher: "Stripe", Icon: ghAvatar("stripe"), Homepage: "https://docs.stripe.com/mcp", Endpoint: "https://mcp.stripe.com", Desc: i18n.T("market.catalog.stripe.description"), SignIn: true}),
		curatedServer(curatedServerSpec{ID: "vercel", Title: "Vercel", Publisher: "Vercel", Icon: ghAvatar("vercel"), Homepage: "https://vercel.com/docs/mcp/vercel-mcp", Endpoint: "https://mcp.vercel.com", Desc: i18n.T("market.catalog.vercel.description"), SignIn: true}),
		curatedServer(curatedServerSpec{ID: "neon", Title: "Neon", Publisher: "Neon", Icon: ghAvatar("neondatabase"), Homepage: "https://neon.com/docs/ai/neon-mcp-server", Endpoint: "https://mcp.neon.tech/mcp", Desc: i18n.T("market.catalog.neon.description"), SignIn: true}),
		curatedServer(curatedServerSpec{ID: "context7", Title: "Context7", Publisher: "Upstash", Icon: ghAvatar("upstash"), Homepage: "https://context7.com", Endpoint: "https://mcp.context7.com/mcp", Desc: i18n.T("market.catalog.context7.description"), SignIn: false, Inputs: []MarketInput{{Key: "CONTEXT7_API_KEY", Where: "header", Label: i18n.T("market.catalog.common.api_key_optional"), Description: i18n.T("market.catalog.context7.api_key_description"), Placeholder: "ctx7_...", Secret: true, Format: "{}"}}}),
		curatedServer(curatedServerSpec{ID: "deepwiki", Title: "DeepWiki", Publisher: "Cognition", Icon: ghAvatar("CognitionAI"), Homepage: "https://deepwiki.com", Endpoint: "https://mcp.deepwiki.com/mcp", Desc: i18n.T("market.catalog.deepwiki.description"), SignIn: false}),
		curatedServer(curatedServerSpec{ID: "exa", Title: "Exa", Publisher: "Exa", Icon: ghAvatar("exa-labs"), Homepage: "https://docs.exa.ai/reference/exa-mcp", Endpoint: "https://mcp.exa.ai/mcp", Desc: i18n.T("market.catalog.exa.description"), SignIn: false}),
		curatedServer(curatedServerSpec{ID: "anysearch", Title: "AnySearch", Publisher: "AnySearch", Icon: ghAvatar("anysearch-ai"), Homepage: "https://github.com/anysearch-ai/anysearch-mcp-server", Endpoint: "https://api.anysearch.com/mcp", Desc: i18n.T("market.catalog.brave.description"), SignIn: false, Inputs: []MarketInput{{Key: "Authorization", Where: "header", Label: i18n.T("market.catalog.common.api_key_optional"), Description: i18n.T("market.catalog.brave.api_key_description"), Placeholder: "as_sk_...", Secret: true, Format: "Bearer {}"}}}),
		curatedServer(curatedServerSpec{ID: "cloudflare-docs", Title: "Cloudflare Docs", Publisher: "Cloudflare", Icon: ghAvatar("cloudflare"), Homepage: "https://github.com/cloudflare/mcp-server-cloudflare", Endpoint: "https://docs.mcp.cloudflare.com/mcp", Desc: i18n.T("market.catalog.cloudflare.description"), SignIn: false}),
		curatedServer(curatedServerSpec{ID: "microsoft-learn", Title: "Microsoft Learn", Publisher: "Microsoft", Icon: ghAvatar("microsoft"), Homepage: "https://github.com/MicrosoftDocs/mcp", Endpoint: "https://learn.microsoft.com/api/mcp", Desc: i18n.T("market.catalog.microsoft.description"), SignIn: false}),
		curatedServer(curatedServerSpec{ID: "huggingface", Title: "Hugging Face", Publisher: "Hugging Face", Icon: ghAvatar("huggingface"), Homepage: "https://huggingface.co/settings/mcp", Endpoint: "https://huggingface.co/mcp", Desc: i18n.T("market.catalog.huggingface.description"), SignIn: false}),
		curatedServer(curatedServerSpec{ID: "playwright", Title: "Playwright", Publisher: "Microsoft", Icon: "https://playwright.dev/img/playwright-logo.svg", Homepage: "https://github.com/microsoft/playwright-mcp", Command: "npx", Args: []string{"-y", "@playwright/mcp@latest"}, Desc: i18n.T("market.catalog.playwright.description")}),
		curatedServer(curatedServerSpec{ID: "chrome-devtools", Title: "Chrome DevTools", Publisher: "Google", Icon: ghAvatar("ChromeDevTools"), Homepage: "https://github.com/ChromeDevTools/chrome-devtools-mcp", Command: "npx", Args: []string{"-y", "chrome-devtools-mcp@latest"}, Desc: i18n.T("market.catalog.chrome_devtools.description")}),
		curatedServer(curatedServerSpec{ID: "filesystem", Title: "Filesystem", Publisher: "Model Context Protocol", Icon: ghAvatar("modelcontextprotocol"), Homepage: "https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem"}, Desc: i18n.T("market.catalog.filesystem.description"), Inputs: []MarketInput{{Key: "dir", Where: "arg", Label: i18n.T("market.catalog.filesystem.dir_label"), Description: i18n.T("market.catalog.filesystem.dir_description"), Placeholder: "/Users/username/Developer", Required: true}}}),
	}
}

func init() {
	marketAllowedIcons.mu.Lock()
	for _, m := range curatedMarketServers() {
		if m.Icon != "" {
			marketAllowedIcons.m[m.ID] = m.Icon
		}
	}
	marketAllowedIcons.mu.Unlock()
}

func registerAllowedMarketIcon(id, iconURL string) {
	if id == "" || iconURL == "" {
		return
	}
	marketAllowedIcons.mu.Lock()
	if len(marketAllowedIcons.m) > 1000 {
		// Re-seed curated icons on overflow to prevent unbounded growth.
		clear(marketAllowedIcons.m)
		for _, m := range curatedMarketServers() {
			if m.Icon != "" {
				marketAllowedIcons.m[m.ID] = m.Icon
			}
		}
	}
	marketAllowedIcons.m[id] = iconURL
	marketAllowedIcons.mu.Unlock()
}

func (g *Service) MarketServers(ctx context.Context, query string) MarketSearchResult {
	installedByID, installedByKey := g.installedServerLookup()
	raw := strings.TrimSpace(query)
	q := strings.ToLower(raw)

	var out []MarketServer
	seenKeys := map[string]bool{}
	markInstalled := func(m *MarketServer) {
		if existing, ok := installedByID[m.ServerID]; ok {
			if existing.Transport == m.Transport || !m.SignIn {
				m.Installed = true
			}
		}
		if installedByKey[marketServerKey(*m)] {
			m.Installed = true
		}
	}

	for _, item := range curatedMarketServers() {
		copyItem := item
		copyItem.Args = slices.Clone(item.Args)
		copyItem.Inputs = slices.Clone(item.Inputs)
		markInstalled(&copyItem)
		seenKeys[marketServerKey(copyItem)] = true
		if q == "" || matchesMarketQuery(copyItem, q) {
			out = append(out, copyItem)
		}
	}

	var registryErr string
	if raw != "" {
		regItems, err := fetchOfficialMCPRegistry(ctx, raw)
		if err != nil {
			registryErr = err.Error()
		} else {
			for _, item := range regItems {
				k := marketServerKey(item)
				if seenKeys[k] {
					continue
				}
				seenKeys[k] = true
				markInstalled(&item)
				registerAllowedMarketIcon(item.ID, item.Icon)
				out = append(out, item)
			}
		}
	}

	if out == nil {
		out = []MarketServer{}
	}
	return MarketSearchResult{Items: out, RegistryError: registryErr}
}

func (g *Service) installedServerLookup() (map[string]registry.Server, map[string]bool) {
	snap := g.Snapshot()
	byID := make(map[string]registry.Server, len(snap.Servers))
	byKey := make(map[string]bool, len(snap.Servers))
	for _, s := range snap.Servers {
		if s.Demo {
			continue
		}
		byID[s.ID] = s
		if s.Transport == "stdio" {
			byKey["stdio:"+s.Command+":"+strings.Join(s.Args, " ")] = true
		} else if s.Endpoint != "" {
			byKey["http:"+strings.TrimRight(strings.ToLower(s.Endpoint), "/")] = true
		}
	}
	return byID, byKey
}

func marketServerKey(m MarketServer) string {
	if m.Transport == "stdio" {
		return "stdio:" + m.Command + ":" + strings.Join(m.Args, " ")
	}
	return "http:" + strings.TrimRight(strings.ToLower(m.Endpoint), "/")
}

func matchesMarketQuery(m MarketServer, q string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		m.ID, m.ServerID, m.Title, m.Publisher, m.Description, m.Endpoint, m.Command, strings.Join(m.Args, " "),
	}, " "))
	return strings.Contains(haystack, q)
}

type officialRegistryServer struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	WebsiteURL  string `json:"websiteUrl"`
	Repository  struct {
		URL string `json:"url"`
	} `json:"repository"`
	Icons []struct {
		Src string `json:"src"`
	} `json:"icons"`
	Remotes []struct {
		Type    string `json:"type"`
		URL     string `json:"url"`
		Headers []struct {
			Name        string `json:"name"`
			Value       string `json:"value"`
			Description string `json:"description"`
			IsRequired  bool   `json:"isRequired"`
			IsSecret    bool   `json:"isSecret"`
		} `json:"headers"`
	} `json:"remotes"`
	Packages []struct {
		RegistryType string `json:"registryType"`
		Identifier   string `json:"identifier"`
		Transport    struct {
			Type string `json:"type"`
		} `json:"transport"`
		EnvironmentVariables []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			IsRequired  bool   `json:"isRequired"`
			IsSecret    bool   `json:"isSecret"`
		} `json:"environmentVariables"`
	} `json:"packages"`
}

func fetchOfficialMCPRegistry(ctx context.Context, query string) ([]MarketServer, error) {
	u := marketRegistryURL + "?" + url.Values{
		"search":  {query},
		"limit":   {"24"},
		"version": {"latest"},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := marketHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("market.err.registry_connect"), err)
	}
	defer closeBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New(i18n.T("market.err.registry_http", resp.StatusCode))
	}
	body, err := input.ReadBounded(resp.Body, 4<<20)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Servers []struct {
			Server officialRegistryServer `json:"server"`
			Meta   map[string]struct {
				Status string `json:"status"`
			} `json:"_meta"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("market.err.registry_parse"), err)
	}
	var items []MarketServer
	for _, entry := range payload.Servers {
		if st := entry.Meta["io.modelcontextprotocol.registry/official"].Status; st != "" && st != "active" {
			continue
		}
		if item, ok := convertOfficialRegistryServer(entry.Server); ok {
			items = append(items, item)
		}
	}
	return items, nil
}

func convertOfficialRegistryServer(r officialRegistryServer) (MarketServer, bool) {
	m, ok := registryServerMetadata(r)
	if !ok {
		return MarketServer{}, false
	}
	if applyRegistryRemote(&m, r) {
		return m, true
	}
	if applyRegistryPackage(&m, r) {
		return m, true
	}
	return MarketServer{}, false
}

func registryServerMetadata(r officialRegistryServer) (MarketServer, bool) {
	short := r.Name
	if idx := strings.LastIndex(short, "/"); idx >= 0 {
		short = short[idx+1:]
	}
	serverID := sanitizeMarketServerID(r.Name)
	if serverID == "" {
		serverID = sanitizeMarketServerID(short)
	}
	if serverID == "" {
		return MarketServer{}, false
	}

	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = short
	}
	homepage := r.WebsiteURL
	if homepage == "" {
		homepage = r.Repository.URL
	}

	owner := extractGitHubOwner(r.Repository.URL)
	if owner == "" {
		if rest, ok := strings.CutPrefix(r.Name, "io.github."); ok {
			owner, _, _ = strings.Cut(rest, "/")
		}
	}
	publisher := owner
	if publisher == "" {
		ns, _, _ := strings.Cut(r.Name, "/")
		parts := strings.Split(ns, ".")
		slices.Reverse(parts)
		publisher = strings.Join(parts, ".")
	}

	icon := ""
	for _, ic := range r.Icons {
		if strings.HasPrefix(ic.Src, "https://") {
			icon = ic.Src
			break
		}
	}
	if icon == "" && owner != "" {
		icon = ghAvatar(owner)
	}

	return MarketServer{
		ID:          r.Name,
		ServerID:    serverID,
		Title:       title,
		Publisher:   publisher,
		Icon:        icon,
		Homepage:    homepage,
		Description: r.Description,
		Featured:    false,
		Inputs:      []MarketInput{},
	}, true
}

func applyRegistryRemote(m *MarketServer, r officialRegistryServer) bool {
	for _, rm := range r.Remotes {
		if rm.Type != "streamable-http" || strings.Contains(rm.URL, "{") || !strings.HasPrefix(rm.URL, "https://") {
			continue
		}
		m.Transport = "http"
		m.Runs = "远程"
		m.Endpoint = rm.URL
		for _, h := range rm.Headers {
			if !h.IsRequired && !h.IsSecret {
				continue
			}
			format := "{}"
			if h.Value != "" {
				format = marketPlaceholderRe.ReplaceAllString(h.Value, "{}")
			}
			m.Inputs = append(m.Inputs, MarketInput{
				Key:         h.Name,
				Where:       "header",
				Label:       h.Name,
				Description: h.Description,
				Secret:      h.IsSecret,
				Required:    h.IsRequired,
				Format:      format,
			})
		}
		return true
	}
	return false
}

func applyRegistryPackage(m *MarketServer, r officialRegistryServer) bool {
	for _, want := range []string{"npm", "pypi"} {
		for _, pkg := range r.Packages {
			if pkg.RegistryType != want || pkg.Identifier == "" || (pkg.Transport.Type != "" && pkg.Transport.Type != "stdio") {
				continue
			}
			m.Transport = "stdio"
			if want == "npm" {
				m.Runs = "npx"
				m.Command = "npx"
				m.Args = []string{"-y", pkg.Identifier}
			} else {
				m.Runs = "uvx"
				m.Command = "uvx"
				m.Args = []string{pkg.Identifier}
			}
			for _, ev := range pkg.EnvironmentVariables {
				if ev.IsRequired || ev.IsSecret || ev.Name != "" {
					m.Inputs = append(m.Inputs, MarketInput{
						Key:         ev.Name,
						Where:       "env",
						Label:       ev.Name,
						Description: ev.Description,
						Secret:      ev.IsSecret,
						Required:    ev.IsRequired,
					})
				}
			}
			return true
		}
	}
	return false
}

func extractGitHubOwner(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(u.Host, "github.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}

func sanitizeMarketServerID(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "io.github.")
	s = strings.ReplaceAll(s, "/", "-")
	s = marketNonIDRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, ".-")
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

func sanitizeMarketEnvName(serverID, key string) string {
	combined := strings.ToUpper(serverID + "_" + key)
	var b strings.Builder
	for _, r := range combined {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "MCP_SECRET"
	}
	return out
}

func (g *Service) InstallMarketServer(ctx context.Context, req InstallMarketRequest) (InstallMarketResult, error) {
	var spec MarketServer
	found := false
	for _, item := range curatedMarketServers() {
		if item.ID == req.ID {
			spec = item
			found = true
			break
		}
	}
	if !found && req.Server != nil {
		spec = *req.Server
		found = true
	}
	if !found {
		return InstallMarketResult{}, fault.New(fault.NotFound, i18n.T("market.err.not_found"), nil)
	}

	serverID := spec.ServerID
	if serverID == "" {
		serverID = sanitizeMarketServerID(spec.ID)
	}
	if !registry.ValidID(serverID) {
		return InstallMarketResult{}, invalid(i18n.T("market.err.invalid_id"))
	}

	server := registry.Server{
		ID:        serverID,
		Name:      spec.Title,
		Kind:      "mcp",
		Transport: spec.Transport,
		OAuth:     spec.SignIn,
		Enabled:   true,
	}
	if spec.Transport == "stdio" {
		server.Command = spec.Command
		server.Args = slices.Clone(spec.Args)
	} else {
		server.Endpoint = spec.Endpoint
	}

	credentialValues := map[string]string{}
	for _, inp := range spec.Inputs {
		val := strings.TrimSpace(req.Inputs[inp.Key])
		if inp.Required && val == "" {
			return InstallMarketResult{}, invalid(i18n.T("market.err.required", inp.Label))
		}
		if val == "" {
			continue
		}
		switch inp.Where {
		case "arg":
			server.Args = append(server.Args, val)
		case "header":
			if server.Headers == nil {
				server.Headers = map[string]string{}
			}
			envName := "MCP_HDR_" + sanitizeMarketEnvName(serverID, inp.Key)
			if strings.HasPrefix(val, "${") && strings.HasSuffix(val, "}") {
				server.Headers[inp.Key] = val
			} else {
				credentialValues[envName] = val
				format := inp.Format
				if format == "" {
					format = "{}"
				}
				server.Headers[inp.Key] = strings.ReplaceAll(format, "{}", "${"+envName+"}")
			}
		case "env":
			if server.Env == nil {
				server.Env = map[string]string{}
			}
			envName := sanitizeMarketEnvName(serverID, inp.Key)
			if strings.HasPrefix(val, "${") && strings.HasSuffix(val, "}") {
				server.Env[inp.Key] = val
			} else {
				credentialValues[envName] = val
				server.Env[inp.Key] = "${" + envName + "}"
			}
		}
	}

	if err := g.SaveCredentials(server, credentialValues); err != nil {
		return InstallMarketResult{}, err
	}

	count, err := g.ImportMCP(ctx, server.ID)
	out := InstallMarketResult{Server: server, ToolCount: count}
	if err != nil {
		out.SyncError = g.ErrorDetail(err)
	}
	return out, nil
}

// MarketIcon fetches and disk-caches a known market server icon under <dir>/market-icons.
func (g *Service) MarketIcon(ctx context.Context, id, rawURL string) ([]byte, string, error) {
	allowedURL := ""
	marketAllowedIcons.mu.RLock()
	allowedURL = marketAllowedIcons.m[id]
	marketAllowedIcons.mu.RUnlock()

	targetURL := allowedURL
	if targetURL == "" && rawURL != "" {
		// Allow explicit URLs only if registered in the allowed map by URL value.
		marketAllowedIcons.mu.RLock()
		for _, v := range marketAllowedIcons.m {
			if v == rawURL {
				targetURL = rawURL
				break
			}
		}
		marketAllowedIcons.mu.RUnlock()
	}
	if targetURL == "" {
		return nil, "", fault.New(fault.NotFound, i18n.T("market.err.icon_not_found"), nil)
	}

	sum := sha256.Sum256([]byte(targetURL))
	cacheDir := filepath.Join(g.dir, "market-icons")
	cacheKey := hex.EncodeToString(sum[:16])
	dataPath := filepath.Join(cacheDir, cacheKey+".bin")
	typePath := filepath.Join(cacheDir, cacheKey+".type")

	if b, err := os.ReadFile(dataPath); err == nil && len(b) > 0 { //nolint:gosec // G304: path is built from the user's own local config/data directories, not from remote input
		ctBytes, _ := os.ReadFile(typePath) //nolint:gosec // G304: path is built from the user's own local config/data directories, not from remote input
		ct := strings.TrimSpace(string(ctBytes))
		if ct == "" {
			ct = http.DetectContentType(b)
		}
		return b, ct, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := marketHTTPClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer closeBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("icon fetch returned HTTP %d", resp.StatusCode)
	}
	b, err := input.ReadBounded(resp.Body, 1<<20)
	if err != nil {
		return nil, "", err
	}
	ct := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if strings.Contains(strings.ToLower(targetURL), ".svg") || strings.Contains(strings.ToLower(ct), "svg") {
		ct = "image/svg+xml"
	} else if ct == "" || !strings.HasPrefix(ct, "image/") {
		ct = http.DetectContentType(b)
	}
	if !strings.HasPrefix(ct, "image/") {
		return nil, "", fmt.Errorf("unexpected icon content-type %q", ct)
	}

	if err := os.MkdirAll(cacheDir, 0700); err == nil {
		_ = os.WriteFile(dataPath, b, 0600)
		_ = os.WriteFile(typePath, []byte(ct), 0600)
	}
	return b, ct, nil
}

type Options struct {
	Dir             string
	Snapshot        func() registry.Registry
	ImportMCP       func(context.Context, string) (int, error)
	SaveCredentials func(registry.Server, map[string]string) error
	ErrorDetail     func(error) string
}
type Service struct {
	Options
	dir string
}

func New(opts Options) *Service { return &Service{Options: opts, dir: opts.Dir} }

func closeBody(resp *http.Response) {
	if err := resp.Body.Close(); err != nil {
		slog.Debug("close upstream response", "error", err)
	}
}
