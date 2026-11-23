package gateway

import "encoding/json"

func (g *Gateway) redactActivityDetail(detail json.RawMessage) (json.RawMessage, error) {
	_, modelKey := g.model.RuntimeConfig()
	return g.upstream.RedactAuditDetail(detail, g.registry.Servers(), modelKey)
}
