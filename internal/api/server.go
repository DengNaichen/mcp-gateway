package api

import (
	"context"
	"net/http"

	"gatewaylab/internal/gateway"
)

type server struct{ gw *gateway.Gateway }

func Handler(g *gateway.Gateway) http.Handler { return (&server{gw: g}).handler() }

// Instance is a Gateway backend opened for hosting behind a caller-owned
// listener. Hosts (cmd/gateway, the desktop app) reach the backend only through
// Handler; they never call Gateway methods directly.
type Instance struct {
	g       *gateway.Gateway
	handler http.Handler
}

// Options configures the backend host credential policy.
type Options struct{ PersistCredentials bool }

// Open opens the Gateway data in dataDir. baseURL is the externally visible
// origin used to build MCP and OAuth callback URLs.
func Open(dataDir, baseURL string, opts Options) (*Instance, error) {
	g, err := gateway.NewWithOptions(dataDir, baseURL, gateway.Options{PersistCredentials: opts.PersistCredentials})
	if err != nil {
		return nil, err
	}
	return &Instance{g: g, handler: Handler(g)}, nil
}

func (i *Instance) Handler() http.Handler { return i.handler }

// StartMaintenance runs background maintenance until ctx is cancelled.
func (i *Instance) StartMaintenance(ctx context.Context) { i.g.StartMaintenance(ctx) }

func (i *Instance) Close() error { return i.g.Close() }
