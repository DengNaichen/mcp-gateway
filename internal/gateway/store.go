package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"

	"gatewaylab/internal/access"
	"gatewaylab/internal/activity"
	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/demo"
	"gatewaylab/internal/fault"
	"gatewaylab/internal/maintenance"
	"gatewaylab/internal/maintenance/sandbox"
	"gatewaylab/internal/market"
	"gatewaylab/internal/model"
	"gatewaylab/internal/policy"
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
	credentials         *secrets.Store
	activity            *activity.Service
	dir                 string
	baseURL             string
	db                  *sql.DB
	storage             *store.Store
	upstream            *upstream.Service
	access              *access.Policy
	localAdmin          *access.LocalAdminAuth
	execution           sandbox.Policy
	routing             routing.Options
	maintenance         *maintenance.Maintainer
	scope               *scope.Service
	policies            policyState
}

type Options struct {
	OpenBrowser        func(string) error
	PersistCredentials bool
	LocalAdminToken    string
	LookupAccessToken  func(string) string
	CredentialBackend  secrets.Backend
}

func New(dir, baseURL string) (*Gateway, Services, error) {
	return NewWithOptions(dir, baseURL, Options{})
}
func NewWithOptions(dir, baseURL string, opts Options) (*Gateway, Services, error) {
	execution, err := sandbox.LoadPolicy()
	if err != nil {
		return nil, Services{}, err
	}
	accessPolicy, err := access.LoadPolicy(baseURL, opts.LookupAccessToken)
	if err != nil {
		return nil, Services{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, Services{}, fmt.Errorf("create data directory: %w", err)
	}
	storage, err := store.Open(dir)
	if err != nil {
		return nil, Services{}, err
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
		return nil, Services{}, fmt.Errorf("initialize scope key: %w", err)
	}
	db := storage.DB
	g := &Gateway{
		instanceID: uuid.NewString(),
		execution:  execution,
		access:     accessPolicy,
		dir:        dir,
		baseURL:    baseURL,
		db:         db,
		storage:    storage,
	}
	if accessPolicy == nil {
		g.localAdmin = access.NewLocalAdminAuth(opts.LocalAdminToken)
	}
	g.credentials, err = openCredentialStore(dir, opts)
	if err != nil {
		return nil, Services{}, err
	}
	g.upstream = upstream.New(upstream.Config{LookupEnv: g.lookupServerEnv, LookupSecret: g.lookupServerSecretOnly, OpenBrowser: opts.OpenBrowser, LoadOAuth: g.credentials.Get, SaveOAuth: func(key, value string) error {
		return g.credentials.Update(map[string]string{key: value}, nil)
	}})
	if err := g.loadRegistry(); err != nil {
		return nil, Services{}, err
	}
	count, err := store.Settings[model.Settings](db).Count()
	if err != nil {
		return nil, Services{}, fmt.Errorf("read settings count: %w", err)
	}
	if count == 0 {
		if err := g.registry.Update(func(registry.Registry) (registry.Registry, error) { return demo.Registry(baseURL), nil }); err != nil {
			return nil, Services{}, err
		}
	}
	// Relocate built-in demo endpoints when the listening port changes.
	g.registry = registry.New(relocateDemoEndpoints(g.registry.Snapshot(), baseURL), store.Registry(db).Save)
	g.scope, err = scope.New(scope.Options{
		DB: db, Dir: dir, BaseURL: baseURL, Key: scopeKey,
		Clock: time.Now, Shared: g.access != nil, Snapshot: g.registry.Snapshot,
		AdminCaller: g.isAdminCaller,
	})
	if err != nil {
		return nil, Services{}, err
	}
	g.policies.rules, err = store.PolicyRules[policy.Rule](db).List()
	if err != nil {
		return nil, Services{}, err
	}
	g.model, err = model.New(db, g.credentials)
	if err != nil {
		return nil, Services{}, err
	}
	if err := g.loadEvents(); err != nil {
		return nil, Services{}, err
	}

	if g.routing, err = loadRoutingOptions(); err != nil {
		return nil, Services{}, err
	}
	g.maintenance, err = newMaintainer(g)
	if err != nil {
		return nil, Services{}, err
	}
	g.market = market.New(market.Options{Dir: dir, Snapshot: g.registry.Snapshot, ImportMCP: g.ImportMCP, SaveCredentials: g.saveServerCredentials, ErrorDetail: g.ErrorDetail})
	ready = true
	return g, g.services(), nil
}

func openCredentialStore(dir string, opts Options) (*secrets.Store, error) {
	backend := opts.CredentialBackend
	mode := os.Getenv("GATEWAY_CREDENTIAL_STORE")
	if mode != "" && mode != "memory" && mode != "keychain" {
		return nil, invalid("GATEWAY_CREDENTIAL_STORE 必须为 memory 或 keychain")
	}
	if backend == nil && mode != "memory" && (mode == "keychain" || (opts.PersistCredentials && runtime.GOOS == "darwin")) {
		var err error
		backend, err = secrets.Keychain(buildinfo.Current().Channel, dir)
		if err != nil {
			return nil, err
		}
	}
	return secrets.Open(backend)
}

func relocateDemoEndpoints(reg registry.Registry, baseURL string) registry.Registry {
	for i := range reg.Servers {
		if reg.Servers[i].Demo {
			switch reg.Servers[i].Kind {
			case "mcp":
				reg.Servers[i].Endpoint = baseURL + "/demo/mcp"
			case "http":
				reg.Servers[i].Endpoint = baseURL + "/demo/api"
			}
		}
	}
	return reg
}

func (g *Gateway) Close() error {
	if g.maintenance != nil {
		g.maintenance.Close()
	}
	result := g.upstream.Close()
	if err := g.storage.Close(); err != nil {
		result = errors.Join(result, fmt.Errorf("close sqlite: %w", err))
	}
	return result
}

func (g *Gateway) loadRegistry() error {
	initial, err := store.Registry(g.db).Load()
	if err != nil {
		return err
	}
	g.registry = registry.New(initial, store.Registry(g.db).Save)
	return nil
}

func (g *Gateway) loadEvents() (err error) {
	g.activity, err = activity.New(store.Activity(g.db), g.redactActivityDetail)
	return err
}

func invalid(message string) error { return fault.New(fault.Invalid, message, nil) }

func (g *Gateway) isAdminCaller(ctx context.Context) bool {
	if g.access == nil {
		return g.localAdmin == nil || access.IsLocalAdmin(ctx)
	}
	p, ok := access.PrincipalFrom(ctx)
	return ok && p.Admin
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
