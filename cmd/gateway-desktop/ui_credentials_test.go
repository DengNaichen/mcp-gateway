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

func TestServiceCredentialsRendersOAuthStatusAndUpgradeHint(t *testing.T) {
	app := NewDesktopUI(&gatewayRuntime{UIURL: "http://127.0.0.1:1", Mode: modeAttached})

	t.Run("stdio upgrade banner", func(t *testing.T) {
		srv := apitypes.Server{ID: "notion", Name: "Notion", Kind: "mcp", Transport: "stdio"}
		state := &servicesPageState{
			credentialServerID: "notion",
			credentialState: apitypes.ServerCredentials{
				Persistent: true,
				Credentials: []apitypes.CredentialStatus{
					{Name: "NOTION_TOKEN", Configured: false},
				},
				OAuthUpgrade: &apitypes.OAuthUpgradeInfo{
					Title:    "Notion",
					Endpoint: "https://mcp.notion.com/mcp",
				},
			},
		}
		tester := ui.NewTester(func(c *ui.Context) { app.renderServerCredentials(c, state, srv) }, 1100, 500)
		if !tester.HasText("Notion 官方支持远程 OAuth 登录") || !tester.HasText("切换为远程 OAuth 并登录") {
			t.Fatal("expected OAuth upgrade prompt in credential card")
		}
	})

	t.Run("oauth unauthorized", func(t *testing.T) {
		srv := apitypes.Server{ID: "notion", Name: "Notion", Kind: "mcp", Transport: "http", Endpoint: "https://mcp.notion.com/mcp", OAuth: true}
		state := &servicesPageState{
			credentialServerID: "notion",
			credentialState: apitypes.ServerCredentials{
				Persistent: true,
				OAuth:      &apitypes.OAuthStatus{Enabled: true, Authorized: false},
			},
		}
		tester := ui.NewTester(func(c *ui.Context) { app.renderServerCredentials(c, state, srv) }, 1100, 500)
		if !tester.HasText("未授权（需浏览器登录）") || !tester.HasText("浏览器 OAuth 登录") {
			t.Fatal("expected OAuth login button when unauthorized")
		}
	})

	t.Run("oauth authorized", func(t *testing.T) {
		srv := apitypes.Server{ID: "notion", Name: "Notion", Kind: "mcp", Transport: "http", Endpoint: "https://mcp.notion.com/mcp", OAuth: true}
		state := &servicesPageState{
			credentialServerID: "notion",
			credentialState: apitypes.ServerCredentials{
				Persistent: true,
				OAuth:      &apitypes.OAuthStatus{Enabled: true, Authorized: true},
			},
		}
		tester := ui.NewTester(func(c *ui.Context) { app.renderServerCredentials(c, state, srv) }, 1100, 500)
		if !tester.HasText("已通过浏览器授权") || !tester.HasText("重新授权") || !tester.HasText("清除授权") {
			t.Fatal("expected authorized status and reauth/clear actions")
		}
	})
}
