package gateway

import (
	"errors"
	"gatewaylab/internal/openapi"
	"regexp"
	"sort"
)

func ImportOpenAPI(serverID string, data []byte, operationIDs []string) ([]Tool, error) {
	tools, err := openapi.ImportOpenAPI(serverID, data, operationIDs)
	var e *openapi.ValidationError
	if errors.As(err, &e) {
		return nil, problem(400, e.Message, nil)
	}
	return tools, registryError(err)
}

// Shared map ordering and path templates remain with their gateway callers.
func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var pathVariable = regexp.MustCompile(`\{([^{}]+)\}`)
