package gateway

import (
	"encoding/json"

	"gatewaylab/internal/cliexec"
)

func (g *Gateway) redactActivityDetail(detail json.RawMessage) (json.RawMessage, error) {
	_, modelKey := g.model.RuntimeConfig()
	servers := g.registry.Servers()
	extraSecrets := []string{modelKey}
	for _, s := range servers {
		if s.Kind == "cli" {
			extraSecrets = append(extraSecrets, cliexec.SecretVariants(g.lookupServerSecretOnly(s.ID, "GH_TOKEN"))...)
		}
	}
	return g.upstream.RedactAuditDetail(detail, servers, extraSecrets...)
}
