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

	"github.com/santhosh-tekuri/jsonschema/v6"
	_ "modernc.org/sqlite"
)

type Gateway struct {
	mu       sync.RWMutex
	registry Registry
	settings Settings
	apiKey   string
	events   []Event
	dir      string
	baseURL  string
	db       *sql.DB
}

func New(dir, baseURL string) (*Gateway, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
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
	g := &Gateway{dir: dir, baseURL: baseURL, events: []Event{}, settings: Settings{
		BaseURL: "https://api.openai.com/v1", Mode: "retrieval", CandidateLimit: 16, MaxTools: 4, TimeoutSeconds: 45, JSONMode: true,
	}, db: db}
	if err := g.loadRegistry(); err != nil {
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
		g.settings.Mode = "model"
	}
	g.apiKey = os.Getenv("GATEWAY_MODEL_API_KEY")
	if err := validateSettings(g.settings); err != nil {
		return nil, fmt.Errorf("invalid model settings: %w", err)
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
	if s.Mode != "model" && s.Mode != "retrieval" {
		return invalid("mode 必须为 model 或 retrieval")
	}
	if s.CandidateLimit < 1 || s.CandidateLimit > 100 || s.MaxTools < 1 || s.MaxTools > 12 || s.MaxTools > s.CandidateLimit {
		return invalid("候选数需为 1–100，返回工具数需为 1–12 且不大于候选数")
	}
	if s.TimeoutSeconds < 5 || s.TimeoutSeconds > 180 {
		return invalid("超时需为 5–180 秒")
	}
	return nil
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
	return nil
}

func compileSchema(schema map[string]any) (*jsonschema.Schema, error) {
	if schema == nil {
		return nil, invalid("工具需要 input_schema")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("urn:gateway:input", schema); err != nil {
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
	if err := validateURL(s.Endpoint); err != nil {
		return err
	}
	for _, value := range s.Headers {
		if !strings.Contains(value, "${") || !strings.Contains(value, "}") {
			return invalid("请求头的凭据请使用 ${ENV_VAR} 环境变量引用")
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
	return t.Enabled && slices.Contains(t.Actors, actorName(actor))
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
