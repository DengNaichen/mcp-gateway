package market

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"gatewaylab/internal/input"
	"gatewaylab/internal/registry"
)

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

func remoteMarketServer(id, title, publisher, icon, homepage, endpoint, desc string, signIn bool, inputs ...MarketInput) MarketServer {
	if inputs == nil {
		inputs = []MarketInput{}
	}
	return MarketServer{
		ID:          id,
		ServerID:    id,
		Title:       title,
		Publisher:   publisher,
		Icon:        icon,
		Homepage:    homepage,
		Description: desc,
		Transport:   "http",
		Runs:        "远程",
		Endpoint:    endpoint,
		SignIn:      signIn,
		Featured:    true,
		Inputs:      inputs,
	}
}

func localMarketServer(id, title, publisher, icon, homepage, command string, args []string, desc string, inputs ...MarketInput) MarketServer {
	if inputs == nil {
		inputs = []MarketInput{}
	}
	return MarketServer{
		ID:          id,
		ServerID:    id,
		Title:       title,
		Publisher:   publisher,
		Icon:        icon,
		Homepage:    homepage,
		Description: desc,
		Transport:   "stdio",
		Runs:        command,
		Command:     command,
		Args:        args,
		Featured:    true,
		Inputs:      inputs,
	}
}

// curatedMarketServers focuses on remote SaaS services (17 cloud SaaS MCPs)
// paired with 3 essential local tools (Playwright, Chrome DevTools, Filesystem).
var curatedMarketServers = []MarketServer{
	remoteMarketServer("notion", "Notion", "Notion", ghAvatar("makenotion"), "https://developers.notion.com/docs/mcp",
		"https://mcp.notion.com/mcp",
		"检索、读取并更新 Notion 工作区页面、数据库与评论上下文。", true),
	remoteMarketServer("linear", "Linear", "Linear", ghAvatar("linear"), "https://linear.app/docs/mcp",
		"https://mcp.linear.app/mcp",
		"查询、创建与更新 Linear Issue、项目进度和团队迭代周期。", true),
	remoteMarketServer("figma", "Figma", "Figma", ghAvatar("figma"), "https://help.figma.com/hc/en-us/articles/32132100833559",
		"https://mcp.figma.com/mcp",
		"读取 Figma 设计稿节点结构、组件规范与设计系统变量 Token。", true),
	remoteMarketServer("github", "GitHub", "GitHub", ghAvatar("github"), "https://github.com/github/github-mcp-server",
		"https://api.githubcopilot.com/mcp/",
		"检索并操作 GitHub 仓库、Issue、Pull Request、Actions 与代码。", false,
		MarketInput{Key: "Authorization", Where: "header", Label: "Personal Access Token", Description: "在 github.com/settings/personal-access-tokens 生成", Placeholder: "github_pat_...", Secret: true, Required: true, Format: "Bearer {}"}),
	remoteMarketServer("sentry", "Sentry", "Sentry", ghAvatar("getsentry"), "https://docs.sentry.io/product/sentry-mcp/",
		"https://mcp.sentry.dev/mcp",
		"查看 Sentry 线上报错堆栈、Issue 状态、Trace 链路与 Seer 诊断分析。", true),
	remoteMarketServer("atlassian", "Atlassian", "Atlassian", ghAvatar("atlassian"), "https://www.atlassian.com/platform/remote-mcp-server",
		"https://mcp.atlassian.com/v1/mcp",
		"访问 Atlassian Cloud 中的 Jira Issue 与 Confluence 团队知识库。", true),
	remoteMarketServer("supabase", "Supabase", "Supabase", ghAvatar("supabase"), "https://supabase.com/docs/guides/getting-started/mcp",
		"https://mcp.supabase.com/mcp",
		"管理 Supabase 项目的数据表、SQL 查询、数据库迁移与 Edge Functions。", true),
	remoteMarketServer("stripe", "Stripe", "Stripe", ghAvatar("stripe"), "https://docs.stripe.com/mcp",
		"https://mcp.stripe.com",
		"查询 Stripe 客户、支付订单、订阅账单以及最新官方 API 文档。", true),
	remoteMarketServer("vercel", "Vercel", "Vercel", ghAvatar("vercel"), "https://vercel.com/docs/mcp/vercel-mcp",
		"https://mcp.vercel.com",
		"管理 Vercel 项目、部署状态、构建运行日志与官方开发文档。", true),
	remoteMarketServer("neon", "Neon", "Neon", ghAvatar("neondatabase"), "https://neon.com/docs/ai/neon-mcp-server",
		"https://mcp.neon.tech/mcp",
		"操作 Neon Serverless Postgres 项目、数据库分支与 SQL 查询。", true),
	remoteMarketServer("context7", "Context7", "Upstash", ghAvatar("upstash"), "https://context7.com",
		"https://mcp.context7.com/mcp",
		"任意库的最新官方文档与代码示例，直接注入提示词上下文。", false,
		MarketInput{Key: "CONTEXT7_API_KEY", Where: "header", Label: "API Key（可选）", Description: "不填也可免费使用，填写后可提升限流配额", Placeholder: "ctx7_...", Secret: true, Format: "{}"}),
	remoteMarketServer("deepwiki", "DeepWiki", "Cognition", ghAvatar("CognitionAI"), "https://deepwiki.com",
		"https://mcp.deepwiki.com/mcp",
		"针对任意公开 GitHub 仓库提问，并读取自动生成的代码知识库。", false),
	remoteMarketServer("exa", "Exa", "Exa", ghAvatar("exa-labs"), "https://docs.exa.ai/reference/exa-mcp",
		"https://mcp.exa.ai/mcp",
		"专为 Agent 设计的网页与代码语义搜索，直接返回干净的页面正文。", false),
	remoteMarketServer("anysearch", "AnySearch", "AnySearch", ghAvatar("anysearch-ai"), "https://github.com/anysearch-ai/anysearch-mcp-server",
		"https://api.anysearch.com/mcp",
		"实时网页与垂类搜索（论文、代码、安全、财经）及整页转 Markdown。", false,
		MarketInput{Key: "Authorization", Where: "header", Label: "API Key（可选）", Description: "可留空试用，填写后提升搜索配额", Placeholder: "as_sk_...", Secret: true, Format: "Bearer {}"}),
	remoteMarketServer("cloudflare-docs", "Cloudflare Docs", "Cloudflare", ghAvatar("cloudflare"), "https://github.com/cloudflare/mcp-server-cloudflare",
		"https://docs.mcp.cloudflare.com/mcp",
		"实时检索 Cloudflare Workers、Pages、R2 与网关配置文档。", false),
	remoteMarketServer("microsoft-learn", "Microsoft Learn", "Microsoft", ghAvatar("microsoft"), "https://github.com/MicrosoftDocs/mcp",
		"https://learn.microsoft.com/api/mcp",
		"查询微软官方开发文档、Azure 参考架构与代码示例。", false),
	remoteMarketServer("huggingface", "Hugging Face", "Hugging Face", ghAvatar("huggingface"), "https://huggingface.co/settings/mcp",
		"https://huggingface.co/mcp",
		"搜索 Hugging Face Hub 上的开源模型、数据集、Spaces 与论文。", false),
	localMarketServer("playwright", "Playwright", "Microsoft", "https://playwright.dev/img/playwright-logo.svg", "https://github.com/microsoft/playwright-mcp",
		"npx", []string{"-y", "@playwright/mcp@latest"},
		"驱动真实浏览器：打开网页、点击输入，并通过无障碍树读取页面结构。"),
	localMarketServer("chrome-devtools", "Chrome DevTools", "Google", ghAvatar("ChromeDevTools"), "https://github.com/ChromeDevTools/chrome-devtools-mcp",
		"npx", []string{"-y", "chrome-devtools-mcp@latest"},
		"连接并调试运行中的 Chrome：查看控制台、网络请求、性能追踪与截图。"),
	localMarketServer("filesystem", "Filesystem", "Model Context Protocol", ghAvatar("modelcontextprotocol"), "https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem",
		"npx", []string{"-y", "@modelcontextprotocol/server-filesystem"},
		"读写与搜索本地文件，权限严格限制在你显式指定的目录内。",
		MarketInput{Key: "dir", Where: "arg", Label: "允许访问的目录路径", Description: "仅授予该目录及其子目录的访问权限", Placeholder: "/Users/username/Developer", Required: true}),
}

func init() {
	marketAllowedIcons.mu.Lock()
	for _, m := range curatedMarketServers {
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
		for _, m := range curatedMarketServers {
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
		if installedByID[m.ServerID] || installedByKey[marketServerKey(*m)] {
			m.Installed = true
		}
	}

	for _, item := range curatedMarketServers {
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

func (g *Service) installedServerLookup() (map[string]bool, map[string]bool) {
	snap := g.Snapshot()
	byID := make(map[string]bool, len(snap.Servers))
	byKey := make(map[string]bool, len(snap.Servers))
	for _, s := range snap.Servers {
		if s.Demo {
			continue
		}
		byID[s.ID] = true
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
		return nil, fmt.Errorf("无法连接官方 MCP Registry: %w", err)
	}
	defer closeBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("官方 MCP Registry 返回 HTTP %d", resp.StatusCode)
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
		return nil, fmt.Errorf("解析官方 MCP Registry 响应失败: %w", err)
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

	m := MarketServer{
		ID:          r.Name,
		ServerID:    serverID,
		Title:       title,
		Publisher:   publisher,
		Icon:        icon,
		Homepage:    homepage,
		Description: r.Description,
		Featured:    false,
		Inputs:      []MarketInput{},
	}

	for _, rm := range r.Remotes {
		if rm.Type != "streamable-http" || strings.Contains(rm.URL, "{") || !strings.HasPrefix(rm.URL, "https://") {
			continue
		}
		m.Transport = "http"
		m.Runs = "远程"
		m.Endpoint = rm.URL
		for _, h := range rm.Headers {
			if h.IsRequired || h.IsSecret {
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
		}
		return m, true
	}

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
			return m, true
		}
	}

	return MarketServer{}, false
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
	for _, item := range curatedMarketServers {
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
		return InstallMarketResult{}, fault.New(fault.NotFound, "未在 MCP 市场中找到该服务", nil)
	}

	serverID := spec.ServerID
	if serverID == "" {
		serverID = sanitizeMarketServerID(spec.ID)
	}
	if !registry.ValidID(serverID) {
		return InstallMarketResult{}, invalid("市场服务 ID 无效")
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
			return InstallMarketResult{}, invalid("请填写必填项：" + inp.Label)
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
		return nil, "", fault.New(fault.NotFound, "未找到该服务的图标", nil)
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
