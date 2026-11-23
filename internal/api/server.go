package api

import (
	"context"
	"net/http"

	"gatewaylab/internal/access"
	"gatewaylab/internal/activity"
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/maintenance"
	"gatewaylab/internal/market"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/scope"
)

type registryStore interface {
	Snapshot() registry.Registry
	SaveTool(registry.Tool) error
}

type server struct {
	gw                    *gateway.Gateway
	access                *access.Policy
	localAdmin            *access.LocalAdminAuth
	activity              *activity.Service
	maintenance           *maintenance.Maintainer
	scope                 *scope.Service
	market                *market.Service
	registry              registryStore
	model                 gateway.ModelQueries
	credentialsPersistent bool
	dataDir               string
	baseURL               string
	instanceID            string
}

func newServer(g *gateway.Gateway, svcs gateway.Services) *server {
	return &server{
		gw:                    g,
		access:                svcs.Access,
		localAdmin:            svcs.LocalAdmin,
		activity:              svcs.Activity,
		maintenance:           svcs.Maintenance,
		scope:                 svcs.Scope,
		market:                svcs.Market,
		registry:              svcs.Registry,
		model:                 svcs.Model,
		credentialsPersistent: svcs.CredentialsPersistent,
		dataDir:               svcs.DataDir,
		baseURL:               svcs.BaseURL,
		instanceID:            svcs.InstanceID,
	}
}

func Handler(g *gateway.Gateway, svcs gateway.Services) http.Handler {
	return newServer(g, svcs).handler()
}

// Instance is a Gateway backend opened for hosting behind a caller-owned
// listener. Hosts (cmd/gateway, the desktop app) reach the backend only through
// Handler; they never call Gateway methods directly.
type Instance struct {
	g           *gateway.Gateway
	maintenance *maintenance.Maintainer
	handler     http.Handler
}

// Options configures the backend host credential policy.
type Options struct {
	PersistCredentials bool
	LocalAdminToken    string
	LookupAccessToken  func(string) string
}

// Open opens the Gateway data in dataDir. baseURL is the externally visible
// origin used to build MCP and OAuth callback URLs.
func Open(dataDir, baseURL string, opts Options) (*Instance, error) {
	g, svcs, err := gateway.NewWithOptions(dataDir, baseURL, gateway.Options{
		PersistCredentials: opts.PersistCredentials,
		LocalAdminToken:    opts.LocalAdminToken,
		LookupAccessToken:  opts.LookupAccessToken,
	})
	if err != nil {
		return nil, err
	}
	return &Instance{g: g, maintenance: svcs.Maintenance, handler: Handler(g, svcs)}, nil
}

func (i *Instance) Handler() http.Handler { return i.handler }

// StartMaintenance runs background maintenance until ctx is cancelled.
func (i *Instance) StartMaintenance(ctx context.Context) { i.maintenance.Start(ctx) }

func (i *Instance) Close() error { return i.g.Close() }
