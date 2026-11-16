package gateway

import (
	"errors"

	"gatewaylab/internal/registry"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func registryError(err error) error {
	var e *registry.ValidationError
	if errors.As(err, &e) {
		return problem(400, e.Message, e.Cause)
	}
	return err
}
func validID(s string) bool { return registry.ValidID(s) }
func compileSchema(s map[string]any) (*jsonschema.Schema, error) {
	v, e := registry.CompileSchema(s)
	return v, registryError(e)
}
func validateMapping(t Tool) error { return registryError(registry.ValidateMapping(t)) }
func validateURL(s string) error   { return registryError(registry.ValidateURL(s)) }
func supportedParameterStyle(in, style string, explode bool) bool {
	return registry.SupportedParameterStyle(in, style, explode)
}
func (g *Gateway) SaveServer(s Server) error { return registryError(g.registry.SaveServer(s)) }
func (g *Gateway) SaveTool(t Tool) error     { return registryError(g.registry.SaveTool(t)) }
