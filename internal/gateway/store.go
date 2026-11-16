package gateway

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"gatewaylab/internal/access"
	"gatewaylab/internal/activity"
	"gatewaylab/internal/demo"
	"gatewaylab/internal/maintenance"
	"gatewaylab/internal/maintenance/sandbox"
	"gatewaylab/internal/market"
	"gatewaylab/internal/model"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/routing"
	"gatewaylab/internal/scope"
	"gatewaylab/internal/secrets"
	"gatewaylab/internal/store"
	"gatewaylab/internal/upstream"
	"gatewaylab/internal/workspace"
)

type Gateway struct {
	market     *market.Service
	instanceID string
	// publicationConfigMu serializes model mutations with final maintenance
	// fingerprint validation and catalog publication; domain state owns its own locks.
	publicationConfigMu sync.RWMutex
	registry            *registry.Service
	model               *model.Service
	runtimeSecrets      secrets.Store
	activity            *activity.Service
	dir                 string
	baseURL             string
	db                  *sql.DB
	storage             *store.Store
	upstream            *upstream.Service
	access              *access.Policy
	execution           sandbox.Policy
	routing             routing.Options
	maintenance         *maintenance.Maintainer
	scope               *scope.Service
}

type Options struct{ OpenBrowser func(string) error }

func New(dir, baseURL string) (*Gateway, error) { return NewWithOptions(dir, baseURL, Options{}) }
func NewWithOptions(dir, baseURL string, opts Options) (*Gateway, error) {
	execution, err := sandbox.LoadPolicy()
	if err != nil {
		return nil, err
	}
	policy, err := access.LoadPolicy(baseURL)
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
		instanceID: uuid.NewString(),
		execution:  execution,
		access:     policy,
		dir:        dir,
		baseURL:    baseURL,
		db:         db,
		storage:    storage,
	}
	g.upstream = upstream.New(upstream.Config{LookupEnv: g.lookupEnv, OpenBrowser: opts.OpenBrowser})
	if err := g.loadRegistry(); err != nil {
		return nil, err
	}
	g.scope, err = scope.New(scope.Options{
		DB: db, Dir: dir, BaseURL: baseURL, Key: scopeKey,
		Clock: time.Now, Shared: g.access != nil, Snapshot: g.Snapshot,
		AdminCaller: g.isAdminCaller, IdentityAllowsTool: g.identityAllowsTool,
	})
	if err != nil {
		return nil, err
	}
	count, err := store.Settings[model.Settings](db).Count()
	if err != nil {
		return nil, fmt.Errorf("read settings count: %w", err)
	}
	if count == 0 {
		if err := g.registry.Update(func(registry.Registry) (registry.Registry, error) { return demo.Registry(baseURL), nil }); err != nil {
			return nil, err
		}
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
	g.model, err = model.New(db)
	if err != nil {
		return nil, err
	}

	if g.routing, err = loadRoutingOptions(); err != nil {
		return nil, err
	}
	g.maintenance, err = newMaintainer(g)
	if err != nil {
		return nil, err
	}
	g.market = market.New(market.Options{Dir: dir, Snapshot: g.Snapshot, SaveServer: g.Registry().SaveServer, ImportMCP: g.ImportMCP, SetRuntimeSecret: g.setRuntimeSecret, ErrorDetail: g.ErrorDetail})
	ready = true
	return g, nil
}

func (g *Gateway) Snapshot() registry.Registry { return g.registry.Snapshot() }

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

func (g *Gateway) record(kind, actor, status, title string, started time.Time, detail any) {
	g.activity.Record(kind, actor, status, title, started, detail)
}

func newID() string { return fmt.Sprintf("%x", time.Now().UnixNano()) }

// Model owns its state lock. This coordination prevents model configuration
// changing between maintenance fingerprint validation and catalog publication.
func (g *Gateway) SaveSettings(s model.Settings, key string, clearKey bool) error {
	g.publicationConfigMu.Lock()
	defer g.publicationConfigMu.Unlock()
	return g.model.SaveSettings(s, key, clearKey)
}
func (g *Gateway) SaveModelProfile(p model.ModelProfile, key string, clearKey bool) (model.ModelProfile, error) {
	g.publicationConfigMu.Lock()
	defer g.publicationConfigMu.Unlock()
	out, err := g.model.SaveModelProfile(p, key, clearKey)
	return out, err
}
func (g *Gateway) ActivateModelProfile(id string) (model.ModelProfile, error) {
	g.publicationConfigMu.Lock()
	defer g.publicationConfigMu.Unlock()
	out, err := g.model.ActivateModelProfile(id)
	return out, err
}
func (g *Gateway) DeleteModelProfile(id string) error {
	g.publicationConfigMu.Lock()
	defer g.publicationConfigMu.Unlock()
	return g.model.DeleteModelProfile(id)
}
