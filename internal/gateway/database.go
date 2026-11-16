package gateway

import (
	"encoding/json"
	"errors"
	"fmt"

	"gatewaylab/internal/activity"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/store"
)

func (g *Gateway) Close() error {
	if g.maintenance != nil {
		g.maintenance.Close()
	}
	result := g.closeDownstreams()
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

func (g *Gateway) persistSettings(s Settings) error {
	if _, err := json.Marshal(s); err != nil {
		return fmt.Errorf("encode model settings: %w", err)
	}
	if err := store.Settings[Settings](g.db).Save(s); err != nil {
		return fmt.Errorf("persist model settings: %w", err)
	}
	return nil
}
func (g *Gateway) loadModelProfiles() ([]ModelProfile, error) {
	profiles, err := store.ModelProfiles[ModelProfile](g.db).List()
	if err != nil {
		return nil, err
	}
	for i := range profiles {
		profiles[i].KeyConfigured = false
	}
	return profiles, nil
}
func (g *Gateway) persistModelProfiles(profiles []ModelProfile) error {
	tx, err := g.db.Begin()
	if err != nil {
		return fmt.Errorf("begin model profiles transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	repo := store.ModelProfiles[ModelProfile](tx)
	if err := repo.Clear(); err != nil {
		return fmt.Errorf("clear model profiles: %w", err)
	}
	for _, p := range profiles {
		p.KeyConfigured = false
		if _, err := json.Marshal(p); err != nil {
			return fmt.Errorf("encode model profile: %w", err)
		}
		if err := repo.Insert(p.ID, p); err != nil {
			return fmt.Errorf("persist model profile: %w", err)
		}
	}
	return tx.Commit()
}
func (g *Gateway) loadEvents() (err error) {
	g.activity, err = activity.New(store.Activity(g.db))
	return err
}
