package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/workspace"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/oauth2"
	_ "modernc.org/sqlite"
)

type Gateway struct {
	instanceID     string
	mu             sync.RWMutex
	registry       Registry
	settings       Settings
	apiKey         string
	profiles       []ModelProfile
	profileKeys    map[string]string
	runtimeSecrets map[string]string
	oauthTokens    map[string]oauth2.TokenSource
	events         []Event
	dir            string
	baseURL        string
	db             *sql.DB
	sessionMu      sync.Mutex
	sessions       map[string]stdioSession
	access         *accessPolicy
	execution      executionPolicy
	routing        routingOptions
	maintenance    *Maintainer
	scopeKey       []byte
	hookBinPath    string
	scope          scopePolicyState
	jtiReplay      *jtiCache
	clock          func() time.Time
}

func New(dir, baseURL string) (*Gateway, error) {
	execution, err := loadExecutionPolicy()
	if err != nil {
		return nil, err
	}
	access, err := loadAccessPolicy(baseURL)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	scopeKey, err := workspace.EnsureScopeKey(dir)
	if err != nil {
		return nil, fmt.Errorf("initialize scope key: %w", err)
	}
	db, err := openDatabase(filepath.Join(dir, "gateway.db"))
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			if err := db.Close(); err != nil {
				slog.Warn("close database after initialization failure", "error", err)
			}
		}
	}()
	g := &Gateway{
		instanceID:     uuid.NewString(),
		execution:      execution,
		access:         access,
		dir:            dir,
		baseURL:        baseURL,
		events:         []Event{},
		profileKeys:    map[string]string{},
		runtimeSecrets: map[string]string{},
		oauthTokens:    map[string]oauth2.TokenSource{},
		settings: Settings{
			BaseURL: "https://api.openai.com/v1", Model: "gpt-6-luna", MaxTools: 4, TimeoutSeconds: 45,
		},
		db:          db,
		scopeKey:    scopeKey,
		hookBinPath: workspace.ResolveHookBinary("", dir),
		jtiReplay:   newJTICache(),
	}
	if err := g.loadRegistry(); err != nil {
		return nil, err
	}
	if err := g.loadScopes(); err != nil {
		return nil, err
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM settings").Scan(&count); err != nil {
		return nil, fmt.Errorf("read settings count: %w", err)
	}
	if count == 0 {
		g.registry = seedRegistry(baseURL)
		if err := g.persistRegistry(g.registry); err != nil {
			return nil, err
		}
		if err := g.persistSettings(g.settings); err != nil {
			return nil, err
		}
	}
	var settingsJSON string
	if err := db.QueryRow("SELECT definition FROM settings WHERE id=1").Scan(&settingsJSON); err != nil {
		return nil, fmt.Errorf("load model settings: %w", err)
	}
	if err := json.Unmarshal([]byte(settingsJSON), &g.settings); err != nil {
		return nil, fmt.Errorf("decode model settings: %w", err)
	}
	if err := g.loadEvents(); err != nil {
		return nil, err
	}
	// Relocate built-in demo endpoints when the listening port changes.
	for i := range g.registry.Servers {
		if g.registry.Servers[i].Demo {
			if g.registry.Servers[i].Kind == "mcp" {
				g.registry.Servers[i].Endpoint = baseURL + "/demo/mcp"
			} else {
				g.registry.Servers[i].Endpoint = baseURL + "/demo/api"
			}
		}
	}
	if v := os.Getenv("GATEWAY_MODEL_BASE_URL"); v != "" {
		g.settings.BaseURL = v
	}
	if v := os.Getenv("GATEWAY_MODEL"); v != "" {
		g.settings.Model = v
	}
	g.apiKey = os.Getenv("GATEWAY_MODEL_API_KEY")
	if err := validateSettings(g.settings); err != nil {
		return nil, fmt.Errorf("invalid model settings: %w", err)
	}
	if err := g.initModelProfiles(); err != nil {
		return nil, err
	}
	if g.routing, err = loadRoutingOptions(); err != nil {
		return nil, err
	}
	g.maintenance, err = newMaintainer(g)
	if err != nil {
		return nil, err
	}
	ready = true
	return g, nil
}

func (g *Gateway) Snapshot() Registry {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return Registry{Servers: slices.Clone(g.registry.Servers), Tools: slices.Clone(g.registry.Tools)}
}

func (g *Gateway) Config() (Settings, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.settings, g.apiKey != ""
}

func (g *Gateway) lookupEnv(key string) string {
	if g != nil {
		g.mu.RLock()
		v := g.runtimeSecrets[key]
		g.mu.RUnlock()
		if v != "" {
			return v
		}
	}
	return os.Getenv(key)
}

func (g *Gateway) expandEnv(value string) string {
	return os.Expand(value, g.lookupEnv)
}

func (g *Gateway) setRuntimeSecret(key, value string) {
	if g == nil || strings.TrimSpace(key) == "" {
		return
	}
	g.mu.Lock()
	if g.runtimeSecrets == nil {
		g.runtimeSecrets = map[string]string{}
	}
	if value == "" {
		delete(g.runtimeSecrets, key)
	} else {
		g.runtimeSecrets[key] = value
	}
	g.mu.Unlock()
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse endpoint: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return invalid("endpoint 必须是有效的 HTTP(S) 地址，凭据请使用环境变量请求头")
	}
	return nil
}

func validateSettings(s Settings) error {
	if err := validateURL(s.BaseURL); err != nil {
		return err
	}
	if s.MaxTools < 1 || s.MaxTools > 12 {
		return invalid("返回工具数需为 1–12")
	}
	if s.TimeoutSeconds < 5 || s.TimeoutSeconds > 180 {
		return invalid("超时需为 5–180 秒")
	}
	return nil
}

func (g *Gateway) initModelProfiles() error {
	profiles, err := g.loadModelProfiles()
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		profiles = []ModelProfile{{
			ID:             "default",
			BaseURL:        g.settings.BaseURL,
			Model:          g.settings.Model,
			MaxTools:       g.settings.MaxTools,
			TimeoutSeconds: g.settings.TimeoutSeconds,
			Active:         true,
			UpdatedAt:      time.Now().Format(time.RFC3339),
		}}
		if err := g.persistModelProfiles(profiles); err != nil {
			return err
		}
	}
	activeIdx := slices.IndexFunc(profiles, func(p ModelProfile) bool { return p.Active })
	if activeIdx < 0 {
		activeIdx = 0
		profiles[0].Active = true
	}
	// Keep env var overrides reflected in the active profile in memory.
	profiles[activeIdx].BaseURL = g.settings.BaseURL
	profiles[activeIdx].Model = g.settings.Model
	profiles[activeIdx].MaxTools = g.settings.MaxTools
	profiles[activeIdx].TimeoutSeconds = g.settings.TimeoutSeconds
	if g.apiKey != "" {
		g.profileKeys[profiles[activeIdx].ID] = g.apiKey
	}
	g.profiles = profiles
	return nil
}

func (g *Gateway) ModelProfiles() []ModelProfile {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]ModelProfile, len(g.profiles))
	for i, p := range g.profiles {
		p.KeyConfigured = g.profileKeys[p.ID] != "" || (p.Active && g.apiKey != "")
		out[i] = p
	}
	return out
}

func (g *Gateway) SaveSettings(s Settings, key string, clearKey bool) error {
	if err := validateSettings(s); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.persistSettings(s); err != nil {
		return err
	}
	g.settings = s
	if clearKey {
		g.apiKey = ""
	} else if key != "" {
		g.apiKey = key
	}
	if idx := slices.IndexFunc(g.profiles, func(p ModelProfile) bool { return p.Active }); idx >= 0 {
		g.profiles[idx].BaseURL = s.BaseURL
		g.profiles[idx].Model = s.Model
		g.profiles[idx].MaxTools = s.MaxTools
		g.profiles[idx].TimeoutSeconds = s.TimeoutSeconds
		g.profiles[idx].UpdatedAt = time.Now().Format(time.RFC3339)
		if clearKey {
			delete(g.profileKeys, g.profiles[idx].ID)
		} else if key != "" {
			g.profileKeys[g.profiles[idx].ID] = key
		}
		if err := g.persistModelProfiles(g.profiles); err != nil {
			return err
		}
	}
	return nil
}

func (g *Gateway) SaveModelProfile(p ModelProfile, key string, clearKey bool) (ModelProfile, error) {
	p.BaseURL = strings.TrimSpace(p.BaseURL)
	p.Model = strings.TrimSpace(p.Model)
	if p.Model == "" {
		return ModelProfile{}, invalid("请填写路由模型名称")
	}
	s := Settings{
		BaseURL:        p.BaseURL,
		Model:          p.Model,
		MaxTools:       p.MaxTools,
		TimeoutSeconds: p.TimeoutSeconds,
	}
	if err := validateSettings(s); err != nil {
		return ModelProfile{}, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now().Format(time.RFC3339)
	idx := -1
	if p.ID != "" {
		idx = slices.IndexFunc(g.profiles, func(item ModelProfile) bool { return item.ID == p.ID })
	}
	if idx < 0 {
		idx = slices.IndexFunc(g.profiles, func(item ModelProfile) bool {
			return item.BaseURL == p.BaseURL && item.Model == p.Model
		})
	}

	next := slices.Clone(g.profiles)
	for i := range next {
		next[i].Active = false
	}

	var savedID string
	if idx >= 0 {
		next[idx].BaseURL = p.BaseURL
		next[idx].Model = p.Model
		next[idx].MaxTools = p.MaxTools
		next[idx].TimeoutSeconds = p.TimeoutSeconds
		next[idx].Active = true
		next[idx].UpdatedAt = now
		savedID = next[idx].ID
	} else {
		savedID = p.ID
		if savedID == "" || !validID(savedID) {
			savedID = "mp-" + uuid.NewString()[:8]
		}
		p.ID = savedID
		p.Active = true
		p.UpdatedAt = now
		next = append([]ModelProfile{p}, next...)
	}

	if err := g.persistModelProfiles(next); err != nil {
		return ModelProfile{}, err
	}
	if err := g.persistSettings(s); err != nil {
		return ModelProfile{}, err
	}

	g.profiles = next
	g.settings = s
	if clearKey {
		delete(g.profileKeys, savedID)
		g.apiKey = ""
	} else if key != "" {
		g.profileKeys[savedID] = key
		g.apiKey = key
	} else {
		g.apiKey = g.profileKeys[savedID]
	}

	outIdx := slices.IndexFunc(g.profiles, func(item ModelProfile) bool { return item.ID == savedID })
	out := g.profiles[outIdx]
	out.KeyConfigured = g.profileKeys[savedID] != ""
	return out, nil
}

func (g *Gateway) ActivateModelProfile(id string) (ModelProfile, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	idx := slices.IndexFunc(g.profiles, func(p ModelProfile) bool { return p.ID == id })
	if idx < 0 {
		return ModelProfile{}, problem(404, "未找到指定的模型配置", nil)
	}

	next := slices.Clone(g.profiles)
	for i := range next {
		next[i].Active = (i == idx)
	}
	chosen := next[idx]
	s := Settings{
		BaseURL:        chosen.BaseURL,
		Model:          chosen.Model,
		MaxTools:       chosen.MaxTools,
		TimeoutSeconds: chosen.TimeoutSeconds,
	}
	if err := g.persistModelProfiles(next); err != nil {
		return ModelProfile{}, err
	}
	if err := g.persistSettings(s); err != nil {
		return ModelProfile{}, err
	}
	g.profiles = next
	g.settings = s
	g.apiKey = g.profileKeys[id]

	chosen.KeyConfigured = g.apiKey != ""
	return chosen, nil
}

func (g *Gateway) DeleteModelProfile(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.profiles) <= 1 {
		return invalid("至少保留一个模型配置")
	}
	idx := slices.IndexFunc(g.profiles, func(p ModelProfile) bool { return p.ID == id })
	if idx < 0 {
		return problem(404, "未找到指定的模型配置", nil)
	}
	wasActive := g.profiles[idx].Active
	next := slices.Delete(slices.Clone(g.profiles), idx, idx+1)

	nextSettings := g.settings
	nextKey := g.apiKey
	if wasActive {
		next[0].Active = true
		nextSettings = Settings{
			BaseURL:        next[0].BaseURL,
			Model:          next[0].Model,
			MaxTools:       next[0].MaxTools,
			TimeoutSeconds: next[0].TimeoutSeconds,
		}
		nextKey = g.profileKeys[next[0].ID]
		if err := g.persistSettings(nextSettings); err != nil {
			return err
		}
	}
	if err := g.persistModelProfiles(next); err != nil {
		return err
	}
	delete(g.profileKeys, id)
	g.profiles = next
	g.settings = nextSettings
	g.apiKey = nextKey
	return nil
}

func compileSchema(schema map[string]any) (*jsonschema.Schema, error) {
	if schema == nil {
		return nil, invalid("工具需要 input_schema")
	}
	// The validator accepts JSON-native []any, not typed Go slices (e.g. the
	// []string required lists built by deterministic importers and demo tools).
	b, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode input schema: %w", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(b, &normalized); err != nil {
		return nil, fmt.Errorf("normalize input schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("urn:gateway:input", normalized); err != nil {
		return nil, fmt.Errorf("register input schema: %w", err)
	}
	s, err := c.Compile("urn:gateway:input")
	if err != nil {
		return nil, fmt.Errorf("compile input schema: %w", err)
	}
	return s, nil
}

func validID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r))
	}) == -1
}

func (g *Gateway) SaveServer(s Server) error {
	if !validID(s.ID) || s.Name == "" || (s.Kind != "http" && s.Kind != "mcp") {
		return invalid("请提供合法 ID、名称及协议类型 http/mcp")
	}
	if s.Kind == "mcp" && s.Transport == "stdio" {
		if strings.TrimSpace(s.Command) == "" {
			return invalid("stdio 服务需要填写启动命令")
		}
	} else {
		if s.Transport != "" && s.Transport != "http" {
			return invalid("支持的 MCP 传输为 http 或 stdio")
		}
		if err := validateURL(s.Endpoint); err != nil {
			return err
		}
	}
	for _, value := range s.Headers {
		if !strings.Contains(value, "${") || !strings.Contains(value, "}") {
			return invalid("请求头的凭据请使用 ${ENV_VAR} 环境变量引用")
		}
	}
	for _, value := range s.Env {
		if !strings.Contains(value, "${") || !strings.Contains(value, "}") {
			return invalid("环境变量值请使用 ${ENV_VAR} 引用，由网关启动环境提供")
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	r := Registry{Servers: slices.Clone(g.registry.Servers), Tools: g.registry.Tools}
	i := slices.IndexFunc(r.Servers, func(old Server) bool { return old.ID == s.ID })
	if i >= 0 {
		s.Demo = r.Servers[i].Demo
		r.Servers[i] = s
	} else {
		r.Servers = append(r.Servers, s)
	}
	if err := g.persistRegistry(r); err != nil {
		return err
	}
	g.registry = r
	return nil
}

func (g *Gateway) SaveTool(t Tool) error {
	if err := validateMapping(t); err != nil {
		return err
	}
	if !validID(t.ID) || t.Name == "" || t.Description == "" || len(t.Actors) == 0 {
		return invalid("工具需要合法 ID、名称、描述及至少一个模拟身份")
	}
	if _, err := compileSchema(t.InputSchema); err != nil {
		return problem(400, "input_schema 无效", err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !slices.ContainsFunc(g.registry.Servers, func(s Server) bool { return s.ID == t.ServerID }) {
		return invalid("server_id 不存在")
	}
	r := Registry{Servers: g.registry.Servers, Tools: slices.Clone(g.registry.Tools)}
	i := slices.IndexFunc(r.Tools, func(old Tool) bool { return old.ID == t.ID })
	if i >= 0 {
		r.Tools[i] = t
	} else {
		r.Tools = append(r.Tools, t)
	}
	if err := g.persistRegistry(r); err != nil {
		return err
	}
	g.registry = r
	return nil
}

func actorName(actor string) string {
	if actor == "" {
		return "developer"
	}
	return actor
}

func allowed(t Tool, actor string) bool {
	return t.Enabled && (slices.Contains(t.Actors, "*") || slices.Contains(t.Actors, actorName(actor)))
}

func (g *Gateway) record(kind, actor, status, title string, started time.Time, detail any) {
	b, err := json.Marshal(detail)
	if err != nil {
		slog.Error("encode activity", "error", err)
		return
	}
	e := Event{ID: newID(), Time: time.Now(), Kind: kind, Actor: actorName(actor), Status: status, Title: title, DurationMS: time.Since(started).Milliseconds(), Detail: b}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.events = append([]Event{e}, g.events...)
	if len(g.events) > 100 {
		g.events = g.events[:100]
	}
	if _, err := g.db.Exec("INSERT INTO activity(id,time,kind,actor,status,title,duration_ms,detail) VALUES(?,?,?,?,?,?,?,?)", e.ID, e.Time.Format(time.RFC3339Nano), e.Kind, e.Actor, e.Status, e.Title, e.DurationMS, string(e.Detail)); err != nil {
		slog.Error("persist activity log", "error", err)
	}
}

func newID() string { return fmt.Sprintf("%x", time.Now().UnixNano()) }
