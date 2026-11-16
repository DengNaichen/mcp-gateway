package gateway

import (
	"gatewaylab/internal/activity"

	"errors"
	"fmt"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/store"
)

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
	g.activity, err = activity.New(store.Activity(g.db))
	return err
}
