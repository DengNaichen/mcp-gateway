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

// ModelQueries excludes mutations: those must share publicationConfigMu with maintenance.
type ModelQueries interface {
	Config() (model.Settings, bool)
	ModelProfiles() []model.ModelProfile
}

// Services exposes the single-domain services and immutable runtime metadata
// assembled alongside a Gateway so callers inject them directly rather than
// routing single-service calls through Gateway as a service locator.
type Services struct {
	Access                *access.Policy
	LocalAdmin            *access.LocalAdminAuth
	Activity              *activity.Service
	Maintenance           *maintenance.Maintainer
	Scope                 *scope.Service
	Market                *market.Service
	Registry              *registry.Service
	Model                 ModelQueries
	CredentialsPersistent bool
	DataDir               string
	BaseURL               string
	InstanceID            string
}

func (g *Gateway) services() Services {
	return Services{
		Access:                g.access,
		LocalAdmin:            g.localAdmin,
		Activity:              g.activity,
		Maintenance:           g.maintenance,
		Scope:                 g.scope,
		Market:                g.market,
		Registry:              g.registry,
		Model:                 g.model,
		CredentialsPersistent: g.credentials.Persistent(),
		DataDir:               g.dir,
		BaseURL:               g.baseURL,
		InstanceID:            g.instanceID,
	}
}
