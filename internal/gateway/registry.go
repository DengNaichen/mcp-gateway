package gateway

import (
	"gatewaylab/internal/registry"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func validID(s string) bool { return registry.ValidID(s) }
func compileSchema(s map[string]any) (*jsonschema.Schema, error) {
	v, e := registry.CompileSchema(s)
	return v, e
}
func validateMapping(t registry.Tool) error { return registry.ValidateMapping(t) }
func validateURL(s string) error            { return registry.ValidateURL(s) }
func supportedParameterStyle(in, style string, explode bool) bool {
	return registry.SupportedParameterStyle(in, style, explode)
}
func (g *Gateway) SaveServer(s registry.Server) error { return g.registry.SaveServer(s) }
func (g *Gateway) SaveTool(t registry.Tool) error     { return g.registry.SaveTool(t) }
