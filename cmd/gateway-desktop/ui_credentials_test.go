package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apitypes"
)

func TestServiceCredentialStatusAndPasswordRendering(t *testing.T) {
	app := NewDesktopUI(&gatewayRuntime{UIURL: "http://127.0.0.1:1", Mode: modeAttached})
	server := apitypes.Server{ID: "github", Name: "GitHub"}
	for _, tc := range []struct {
		name                                       string
		persistent, saved, configured, environment bool
		status                                     string
	}{
		{"keychain", true, true, true, false, "已保存"},
		{"environment", true, true, true, true, "由运行环境提供"},
		{"memory", false, true, true, false, "仅本次运行"},
		{"missing", true, false, false, false, "未配置"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &servicesPageState{credentialServerID: server.ID, credentialValues: map[string]string{"GITHUB_TOKEN": "secret-must-be-masked"}, credentialState: apitypes.ServerCredentials{Persistent: tc.persistent, Credentials: []apitypes.CredentialStatus{{Name: "GITHUB_TOKEN", Configured: tc.configured, Saved: tc.saved, Environment: tc.environment}}}}
			tester := ui.NewTester(func(c *ui.Context) { app.renderServerCredentials(c, state, server) }, 1100, 500)
			if !tester.HasText("服务凭据") || !tester.HasText("GITHUB_TOKEN") || !tester.HasText(tc.status) {
				t.Fatal("missing credential status")
			}
			if tester.HasText("secret-must-be-masked") {
				t.Fatal("password rendered as plaintext")
			}
		})
	}
}
