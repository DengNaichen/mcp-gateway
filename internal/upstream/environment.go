package upstream

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

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
func ProcessEnvironment(extra map[string]string) []string {
	env := []string{}
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "LANG", "SYSTEMROOT"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read bounded input: %w", err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("input exceeds %d bytes", limit)
	}
	return b, nil
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
	b, err := json.Marshal([]any{s, values})
	if err != nil {
		return "", fmt.Errorf("encode connection fingerprint: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
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
