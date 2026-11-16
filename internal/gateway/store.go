package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"time"

	"gatewaylab/internal/activity"
	"gatewaylab/internal/model"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/scope"
	"gatewaylab/internal/secrets"
	"gatewaylab/internal/store"
	"gatewaylab/internal/upstream"
	"gatewaylab/internal/workspace"

	"github.com/google/uuid"
)

type Gateway struct {
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
	access              *accessPolicy
	execution           executionPolicy
	routing             routingOptions
	maintenance         *Maintainer
	scopeKey            []byte
	scope               *scope.Service
	clock               func() time.Time
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
		instanceID: uuid.NewString(),
		execution:  execution,
		access:     access,
		dir:        dir,
		baseURL:    baseURL,
		db:         db,
		storage:    storage,
		scopeKey:   scopeKey,
	}
	g.upstream = upstream.New(upstream.Config{LookupEnv: g.lookupEnv})
	if err := g.loadRegistry(); err != nil {
		return nil, err
	}
	g.scope, err = scope.New(scope.Options{
		DB: db, Dir: dir, BaseURL: baseURL, Key: scopeKey,
		Clock: g.now, Shared: g.access != nil, Snapshot: g.Snapshot,
		AdminCaller: g.isAdminCaller, IdentityAllowsTool: g.identityAllowsTool,
	})
	if err != nil {
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
	ready = true
	return g, nil
}

func (g *Gateway) Snapshot() Registry { return g.registry.Snapshot() }

func (g *Gateway) Config() (Settings, bool) { return g.model.Config() }

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

func (g *Gateway) ModelProfiles() []ModelProfile { return g.model.ModelProfiles() }

// Model owns its state lock. This coordination prevents model configuration
// changing between maintenance fingerprint validation and catalog publication.
func (g *Gateway) SaveSettings(s Settings, key string, clearKey bool) error {
	g.publicationConfigMu.Lock()
	defer g.publicationConfigMu.Unlock()
	return modelError(g.model.SaveSettings(s, key, clearKey))
}
func (g *Gateway) SaveModelProfile(p ModelProfile, key string, clearKey bool) (ModelProfile, error) {
	g.publicationConfigMu.Lock()
	defer g.publicationConfigMu.Unlock()
	out, err := g.model.SaveModelProfile(p, key, clearKey)
	return out, modelError(err)
}
func (g *Gateway) ActivateModelProfile(id string) (ModelProfile, error) {
	g.publicationConfigMu.Lock()
	defer g.publicationConfigMu.Unlock()
	out, err := g.model.ActivateModelProfile(id)
	return out, modelError(err)
}
func (g *Gateway) DeleteModelProfile(id string) error {
	g.publicationConfigMu.Lock()
	defer g.publicationConfigMu.Unlock()
	return modelError(g.model.DeleteModelProfile(id))
}
func (g *Gateway) PingModel(ctx context.Context, s Settings, profileID, apiKey string, clearKey bool) (PingModelResult, error) {
	out, err := g.model.PingModel(ctx, s, profileID, apiKey, clearKey)
	return out, modelError(err)
}
