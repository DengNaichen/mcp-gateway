package maintenance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaintenanceAdapterUsesServiceLookup(t *testing.T) {
	t.Setenv("GATEWAY_ADMIN_TOKEN", "process-fixture")
	t.Setenv("SERVICE_TOKEN", "process-fixture")
	options := testOptions(testMaintainer(t))
	options.ServerLookupEnv = func(serverID, key string) string {
		if serverID == "service" && key == "SERVICE_TOKEN" {
			return "service-fixture"
		}
		return ""
	}
	m, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)

	dir := t.TempDir()
	const script = `
import fs from 'node:fs';
const { headers, env } = JSON.parse(fs.readFileSync(0, 'utf8'));
console.log(JSON.stringify({
  adminHeaderBlocked: headers['X-Admin'] === '',
  adminEnvBlocked: env.ADMIN === '',
  serviceCredentialUsed: headers['X-Service'] === 'service-fixture'
}));
`
	if err := os.WriteFile(filepath.Join(dir, "adapter.mjs"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	result, callErr, err := m.runHTTPContract(t.Context(), contractInvocation{
		Server: Server{
			ID: "service", Kind: "http",
			Headers: map[string]string{"X-Admin": "${GATEWAY_ADMIN_TOKEN}", "X-Service": "${SERVICE_TOKEN}"},
			Env:     map[string]string{"ADMIN": "${GATEWAY_ADMIN_TOKEN}"},
		},
		Tool: Tool{Name: "read", Adapter: "adapter.mjs"},
		Dir:  dir,
	})
	if err != nil || callErr != nil {
		t.Fatalf("run adapter contract: %v %v", err, callErr)
	}
	want := []byte(`{"adminHeaderBlocked":true,"adminEnvBlocked":true,"serviceCredentialUsed":true}`)
	if !equalJSON(mustJSON(t, result), want) {
		t.Fatalf("adapter received incorrect credential sources: %v", result)
	}
}
