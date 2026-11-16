package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/activity"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/secrets"
	"gatewaylab/internal/store"
	"gatewaylab/internal/workspace"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

type Gateway struct {
	instanceID     string
	mu             sync.RWMutex
	registry       *registry.Service
	settings       Settings
	apiKey         string
	profiles       []ModelProfile
	profileKeys    map[string]string
	runtimeSecrets secrets.Store
	oauthTokens    map[string]oauth2.TokenSource
	activity       *activity.Service
	dir            string
	baseURL        string
	db             *sql.DB
	storage        *store.Store
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
	storage, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			if err := storage.Close(); err != nil {
				slog.Warn("close database after initialization failure", "error", err)
			}
		}
	}()
	scopeKey, err := workspace.EnsureScopeKey(dir)
	if err != nil {
		return nil, fmt.Errorf("initialize scope key: %w", err)
	}
	db := storage.DB
	g := &Gateway{
		instanceID:  uuid.NewString(),
		execution:   execution,
		access:      access,
		dir:         dir,
		baseURL:     baseURL,
		profileKeys: map[string]string{},
		oauthTokens: map[string]oauth2.TokenSource{},
		settings: Settings{
			BaseURL: "https://api.openai.com/v1", Model: "gpt-6-luna", MaxTools: 4, TimeoutSeconds: 45,
		},
		db:          db,
		storage:     storage,
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
	count, err := store.Settings[Settings](db).Count()
	if err != nil {
		return nil, fmt.Errorf("read settings count: %w", err)
	}
	if count == 0 {
		if err := g.registry.Update(func(Registry) (Registry, error) { return seedRegistry(baseURL), nil }); err != nil {
			return nil, err
		}
		if err := g.persistSettings(g.settings); err != nil {
			return nil, err
		}
	}
	settings, err := store.Settings[json.RawMessage](db).Load()
	if err != nil {
		return nil, fmt.Errorf("load model settings: %w", err)
	}
	if err := json.Unmarshal(settings, &g.settings); err != nil {
		return nil, fmt.Errorf("decode model settings: %w", err)
	}
	if err := g.loadEvents(); err != nil {
		return nil, err
	}
	// Relocate built-in demo endpoints when the listening port changes.
	relocated := g.Snapshot()
	for i := range relocated.Servers {
		if relocated.Servers[i].Demo {
			if relocated.Servers[i].Kind == "mcp" {
				relocated.Servers[i].Endpoint = baseURL + "/demo/mcp"
			} else {
				relocated.Servers[i].Endpoint = baseURL + "/demo/api"
			}
		}
	}
	g.registry = registry.New(relocated, store.Registry(db).Save)
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

func (g *Gateway) Snapshot() Registry { return g.registry.Snapshot() }

func (g *Gateway) Config() (Settings, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.settings, g.apiKey != ""
}

func (g *Gateway) lookupEnv(key string) string {
	if g == nil {
		return os.Getenv(key)
	}
	return g.runtimeSecrets.Lookup(key)
}

func (g *Gateway) expandEnv(value string) string { return os.Expand(value, g.lookupEnv) }

func (g *Gateway) setRuntimeSecret(key, value string) {
	if g != nil {
		g.runtimeSecrets.Set(key, value)
	}
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
	g.activity.Record(kind, actor, status, title, started, detail)
}

func newID() string { return fmt.Sprintf("%x", time.Now().UnixNano()) }
