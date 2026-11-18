package gateway

import (
	"gatewaylab/internal/access"
	"gatewaylab/internal/activity"
	"gatewaylab/internal/maintenance"
	"gatewaylab/internal/market"
	"gatewaylab/internal/model"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/scope"
)

func (g *Gateway) Access() *access.Policy               { return g.access }
func (g *Gateway) Activity() *activity.Service          { return g.activity }
func (g *Gateway) Maintenance() *maintenance.Maintainer { return g.maintenance }
func (g *Gateway) Scope() *scope.Service                { return g.scope }
func (g *Gateway) Market() *market.Service              { return g.market }
func (g *Gateway) DataDir() string                      { return g.dir }
func (g *Gateway) BaseURL() string                      { return g.baseURL }
func (g *Gateway) InstanceID() string                   { return g.instanceID }

func (g *Gateway) Registry() *registry.Service { return g.registry }

// ModelQueries excludes mutations: those must share publicationConfigMu with maintenance.
type ModelQueries interface {
	Config() (model.Settings, bool)
	ModelProfiles() []model.ModelProfile
}

func (g *Gateway) Model() ModelQueries { return g.model }
