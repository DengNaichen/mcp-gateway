package upstream

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"gatewaylab/internal/jsonutil"
	"gatewaylab/internal/registry"
)

type Server = registry.Server
type Tool = registry.Tool
type HTTPParameter = registry.HTTPParameter
type HTTPMapping = registry.HTTPMapping
type HTTPResponse = registry.HTTPResponse

func noRedirect(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
func closeBody(resp *http.Response) {
	if err := resp.Body.Close(); err != nil {
		slog.Debug("close upstream response", "error", err)
	}
}

func Fingerprint(s Server, lookup func(string) string) (string, error) {
	if lookup == nil {
		lookup = os.Getenv
	}
	values := map[string]string{}
	for k, v := range s.Headers {
		values["header:"+k] = os.Expand(v, lookup)
	}
	for k, v := range s.Env {
		values["env:"+k] = os.Expand(v, lookup)
	}
	sum, err := jsonutil.Digest([]any{s, values})
	if err != nil {
		return "", fmt.Errorf("encode connection fingerprint: %w", err)
	}
	return sum, nil
}

func CheckEnvironment(s Server, lookup func(string) string) error {
	if lookup == nil {
		lookup = os.Getenv
	}
	for _, values := range []map[string]string{s.Headers, s.Env} {
		for _, value := range values {
			missing := []string{}
			os.Expand(value, func(key string) string {
				v := lookup(key)
				if v == "" {
					missing = append(missing, key)
				}
				return v
			})
			if len(missing) > 0 {
				return fmt.Errorf("gateway process environment is missing: %s; export the variables and restart the gateway", strings.Join(missing, ", "))
			}
		}
	}
	return nil
}
