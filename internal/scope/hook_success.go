package scope

import (
	"log/slog"

	"gatewaylab/internal/workspace"
)

func (g *Service) recordHookSuccess(claims workspace.Claims) {
	if err := workspace.RecordHookSuccess(g.dir, claims.Agent, claims.InstallationID); err != nil {
		slog.Warn("persist verified hook success", "error", err)
	}
}
