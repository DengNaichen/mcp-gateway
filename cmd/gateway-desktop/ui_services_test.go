package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gatewaylab/internal/apitypes"

	"github.com/egoist/mygo/ui"
)

func TestSaveMaintenanceConfigPreservesRepositoryProviderAndModel(t *testing.T) {
	nextRun := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	existingSource := apitypes.MaintenanceSource{
		ServerID:        "svc-billing",
		Format:          "openapi",
		Files:           []string{"/specs/old.yaml"},
		URLs:            []string{"https://example.com/old.yaml"},
		OperationIDs:    []string{"listInvoices"},
		Instructions:    "Keep endpoints concise.",
		Enabled:         true,
		AutoPublish:     false,
		IntervalMinutes: 30,
		TimeoutSeconds:  600,
		Provider:        "openai",
		Model:           "gpt-5.4",
		Repository: &apitypes.RepositorySource{
			URL:              "https://github.com/acme/billing-api",
			Ref:              "main",
			Files:            []string{"api/openapi.yaml"},
			Project:          "billing",
			EventsEnabled:    true,
			WebhookSecretEnv: "BILLING_WEBHOOK_SECRET",
		},
		NextRunAt: nextRun,
	}

	var (
		mu        sync.Mutex
		savedBody apitypes.MaintenanceSource
		saveCalls int
	)

	boot := apitypes.Bootstrap{
		MCPURL: "http://127.0.0.1:8788/mcp",
		Registry: apitypes.Registry{
			Servers: []apitypes.Server{
				{ID: "svc-billing", Name: "Billing API", Kind: "http", Transport: "http", Endpoint: "https://api.acme.dev", Enabled: true},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/bootstrap":
			_ = json.NewEncoder(w).Encode(boot)
		case r.Method == http.MethodGet && r.URL.Path == "/api/maintenance":
			_ = json.NewEncoder(w).Encode(apitypes.MaintenanceStatus{
				Sources: []apitypes.MaintenanceSource{existingSource},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/maintenance/sources":
			var in apitypes.MaintenanceSource
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Fatalf("decode saved maintenance source: %v", err)
			}
			mu.Lock()
			savedBody = in
			saveCalls++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(apitypes.Saved{Saved: true})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	uiApp := NewDesktopUI(&gatewayRuntime{UIURL: srv.URL, MCPURL: boot.MCPURL, Mode: modeAttached})
	uiApp.State = &boot
	uiApp.Loading = false

	st := uiApp.servicesState()
	server := boot.Registry.Servers[0]
	uiApp.loadMaintenanceOverview(st, server)

	deadline := time.Now().Add(3 * time.Second)
	for {
		leave := uiApp.enterHeadlessFrame()
		done := uiApp.BusyCount == 0 && !st.maintLoading && st.maintOverview != nil
		leave()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for loadMaintenanceOverview")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Edit only the form fields exposed in the desktop UI.
	st.maintURLsText = "https://example.com/new.yaml"
	st.maintIntervalStr = "45"
	st.maintEnabled = false

	uiApp.saveMaintenanceConfig(st, server, false, false)

	deadline = time.Now().Add(3 * time.Second)
	for {
		leave := uiApp.enterHeadlessFrame()
		done := uiApp.BusyCount == 0 && !st.maintBusy
		leave()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for saveMaintenanceConfig")
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if saveCalls != 1 {
		t.Fatalf("expected 1 PUT /api/maintenance/sources call, got %d", saveCalls)
	}
	if savedBody.Provider != "openai" || savedBody.Model != "gpt-5.4" {
		t.Fatalf("expected Provider/Model preserved, got provider=%q model=%q", savedBody.Provider, savedBody.Model)
	}
	if !reflect.DeepEqual(savedBody.Repository, existingSource.Repository) {
		t.Fatalf("expected Repository preserved: got %+v, want %+v", savedBody.Repository, existingSource.Repository)
	}
	if !savedBody.NextRunAt.Equal(nextRun) {
		t.Fatalf("expected NextRunAt preserved: got %v, want %v", savedBody.NextRunAt, nextRun)
	}
	if savedBody.IntervalMinutes != 45 || savedBody.Enabled || !reflect.DeepEqual(savedBody.URLs, []string{"https://example.com/new.yaml"}) {
		t.Fatalf("expected edited form fields applied, got %+v", savedBody)
	}
}

func TestServicesAndRepos401TransitionToNeedsLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"session expired"}`))
	}))
	defer srv.Close()

	boot := apitypes.Bootstrap{
		MCPURL: "http://127.0.0.1:8788/mcp",
		Registry: apitypes.Registry{
			Servers: []apitypes.Server{
				{ID: "svc-1", Name: "Svc", Kind: "http", Transport: "http", Endpoint: "https://api.example.com", Enabled: true},
			},
		},
	}

	// 1. Services saveMaintenanceConfig 401 -> NeedsLogin=true, BusyCount=0.
	uiSvc := NewDesktopUI(&gatewayRuntime{UIURL: srv.URL, MCPURL: boot.MCPURL, Mode: modeAttached})
	uiSvc.State = &boot
	uiSvc.Loading = false
	uiSvc.saveMaintenanceConfig(uiSvc.servicesState(), boot.Registry.Servers[0], false, false)

	deadline := time.Now().Add(3 * time.Second)
	for {
		leave := uiSvc.enterHeadlessFrame()
		done := uiSvc.BusyCount == 0 && !uiSvc.servicesState().maintBusy
		needsLogin := uiSvc.NeedsLogin
		leave()
		if done && needsLogin {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected NeedsLogin=true and BusyCount=0 after 401 in saveMaintenanceConfig")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 2. Repos mutation 401 -> NeedsLogin=true, BusyCount=0.
	uiRepos := NewDesktopUI(&gatewayRuntime{UIURL: srv.URL, MCPURL: boot.MCPURL, Mode: modeAttached})
	uiRepos.State = &boot
	uiRepos.Loading = false
	uiRepos.ViewMode = ViewRepos
	uiRepos.marketState().loaded = true

	tester := ui.NewTester(uiRepos.View, 1280, 820)
	if err := tester.Click("启用仓库隔离拦截"); err != nil {
		t.Fatalf("click enforce toggle: %v", err)
	}
	waitAsyncIdle(t, tester, uiRepos)
	if !uiRepos.NeedsLogin || uiRepos.BusyCount != 0 {
		t.Fatal("expected Repos HTTP 401 to transition DesktopUI to NeedsLogin=true with BusyCount=0")
	}
}

func TestDesktopUIStateIsolationAndCatalogToolIntent(t *testing.T) {
	a := NewDesktopUI(nil)
	b := NewDesktopUI(nil)

	a.servicesState().listQuery = "github"
	if b.servicesState().listQuery != "" {
		t.Fatalf("expected isolated per-instance servicesState, got %q", b.servicesState().listQuery)
	}

	a.OpenCatalogTool("github.list_prs", "github")
	if a.CurrentView() != ViewCatalog {
		t.Fatalf("expected ViewCatalog, got %s", a.CurrentView())
	}
	toolID, serverID, ok := a.consumePendingCatalogTool()
	if !ok || toolID != "github.list_prs" || serverID != "github" {
		t.Fatalf("unexpected pending catalog tool: ok=%v tool=%q server=%q", ok, toolID, serverID)
	}
	if _, _, again := a.consumePendingCatalogTool(); again {
		t.Fatal("expected pending catalog tool intent to be consumed once")
	}
}

const testGHDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func ghTestServer() apitypes.Server {
	return apitypes.Server{ID: "gh", Name: "GitHub CLI", Kind: "cli", Command: "/opt/homebrew/bin/gh", CommandSHA256: testGHDigest, Env: ghPresetEnv(), Enabled: true}
}

func ghTestTools() []apitypes.Tool {
	return []apitypes.Tool{
		{ID: "gh.pr.view", ServerID: "gh", Name: "pr.view", Description: "View a pull request on GitHub", ReadOnly: true, Enabled: true},
		{ID: "gh.pr.list", ServerID: "gh", Name: "pr.list", Description: "List pull requests on GitHub", ReadOnly: true, Enabled: true},
		{ID: "gh.pr.merge", ServerID: "gh", Name: "pr.merge", Description: "Merge a pull request on GitHub", Enabled: true},
	}
}

func TestGHPresetBuildsControlledCLIPayload(t *testing.T) {
	add := defaultServiceAddFormState()
	add.open(0)
	add.applyGHPreset()
	if add.tab != serviceAddTabCLI || add.id != "gh" || add.cliCommand != "gh" {
		t.Fatalf("preset did not fill the CLI form: %+v", add)
	}
	payload, err := add.buildCLIServerPayload()
	if err != nil {
		t.Fatal(err)
	}
	want := apitypes.Server{ID: "gh", Name: "GitHub CLI", Kind: "cli", Command: "gh", Env: map[string]string{"GH_TOKEN": "${GH_TOKEN}"}, Enabled: true}
	if !reflect.DeepEqual(payload, want) {
		t.Fatalf("payload = %+v, want %+v", payload, want)
	}

	add.cliSHA = strings.ToUpper(testGHDigest)
	if payload, err = add.buildCLIServerPayload(); err != nil || payload.CommandSHA256 != testGHDigest {
		t.Fatalf("explicit digest must be normalized: %+v err=%v", payload, err)
	}
	add.cliSHA = "deadbeef"
	if _, err := add.buildCLIServerPayload(); err == nil {
		t.Fatal("short digest must be rejected")
	}

	paste := defaultServiceAddFormState()
	paste.pasteText = `{"id":"gh","name":"GitHub CLI","kind":"cli","command":"/usr/local/bin/gh","command_sha256":"` + testGHDigest + `"}`
	paste.parsePasteJSON()
	if paste.tab != serviceAddTabCLI || paste.cliCommand != "/usr/local/bin/gh" || paste.cliSHA != testGHDigest {
		t.Fatalf("gateway CLI JSON must open the CLI form: %+v", paste)
	}
}

func TestServiceConnFormPreservesCLIPin(t *testing.T) {
	orig := ghTestServer()
	var st serviceConnFormState
	st.hydrateConnForm(orig)
	st.connName = "GitHub (gh)"
	st.connEndpoint = "https://should-not-apply"
	st.connHeadersText = `{"X":"${Y}"}`
	out, err := st.buildServerFromConnForm(orig)
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "GitHub (gh)" || out.Command != orig.Command || out.CommandSHA256 != testGHDigest ||
		!reflect.DeepEqual(out.Env, orig.Env) || out.Endpoint != "" || out.Transport != "" || len(out.Headers) != 0 {
		t.Fatalf("CLI pin/env must be preserved and HTTP fields ignored: %+v", out)
	}
}

// TestSubmitAddCLIServiceSavesPinAndKeychainToken covers the one-step gh preset:
// register (server pins the digest), store GH_TOKEN in the gateway keychain,
// then verify the source.
func TestSubmitAddCLIServiceSavesPinAndKeychainToken(t *testing.T) {
	var (
		mu        sync.Mutex
		savedSrv  apitypes.Server
		credPaths []string
		credBody  apitypes.SaveCredentialsRequest
		probed    apitypes.Server
	)
	boot := apitypes.Bootstrap{MCPURL: "http://127.0.0.1:8788/mcp"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/bootstrap":
			_ = json.NewEncoder(w).Encode(boot)
		case r.Method == http.MethodPut && r.URL.Path == "/api/servers":
			_ = json.NewDecoder(r.Body).Decode(&savedSrv)
			pinned := savedSrv
			pinned.Command, pinned.CommandSHA256 = "/opt/homebrew/bin/gh", testGHDigest
			boot.Registry.Servers = []apitypes.Server{pinned}
			boot.Registry.Tools = ghTestTools()
			_ = json.NewEncoder(w).Encode(pinned)
		case r.Method == http.MethodPut && r.URL.Path == "/api/servers/gh/credentials":
			credPaths = append(credPaths, r.URL.Path)
			_ = json.NewDecoder(r.Body).Decode(&credBody)
			_, _ = w.Write([]byte(`{"saved":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/setup/source":
			_ = json.NewDecoder(r.Body).Decode(&probed)
			_ = json.NewEncoder(w).Encode(apitypes.ConnectionCheck{Status: "success", Message: "已校验 GitHub CLI 和网关中保存的凭据", ToolCount: 8})
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/gh/credentials":
			_ = json.NewEncoder(w).Encode(apitypes.ServerCredentials{Persistent: true, Credentials: []apitypes.CredentialStatus{{Name: "GH_TOKEN", Configured: true, Saved: true}}})
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	uiApp := NewDesktopUI(&gatewayRuntime{UIURL: srv.URL, MCPURL: boot.MCPURL, Mode: modeAttached})
	bootCopy := boot
	uiApp.State = &bootCopy
	uiApp.Loading = false
	uiApp.ViewMode = ViewServices
	uiApp.marketState().loaded = true
	tester := ui.NewTester(uiApp.View, 1400, 1200)
	waitAsyncIdle(t, tester, uiApp)

	st := uiApp.servicesState()
	st.add.open(serviceAddTabCLI)
	st.add.applyGHPreset()
	st.add.cliToken = "ghp_secret_token_value"
	tester.Frame()
	if !tester.HasText("GitHub CLI（gh）受控接入") || tester.HasText("ghp_secret_token_value") {
		t.Fatal("CLI preset form must render with the token masked")
	}
	if err := tester.Click("钉死并接入"); err != nil {
		t.Fatal(err)
	}
	waitAsyncIdle(t, tester, uiApp)

	mu.Lock()
	if savedSrv.Kind != "cli" || savedSrv.Command != "gh" || savedSrv.Transport != "" || savedSrv.Env["GH_TOKEN"] != "${GH_TOKEN}" {
		t.Fatalf("unexpected registered server: %+v", savedSrv)
	}
	if strings.Contains(fmt.Sprint(savedSrv), "ghp_secret_token_value") {
		t.Fatal("token must never be sent as part of the server definition")
	}
	if len(credPaths) != 1 || credBody.Values["GH_TOKEN"] != "ghp_secret_token_value" {
		t.Fatalf("token must be stored via the credentials endpoint: paths=%v body=%+v", credPaths, credBody)
	}
	if probed.CommandSHA256 != testGHDigest {
		t.Fatalf("probe must use the pinned definition: %+v", probed)
	}
	mu.Unlock()

	if st.add.show || st.add.cliToken != "" || uiApp.activeServiceID() != "gh" {
		t.Fatalf("expected modal closed, token cleared and detail opened (active=%q)", uiApp.activeServiceID())
	}
	for _, want := range []string{"CLI", "/opt/homebrew/bin/gh", "0123456789ab", "SHA-256"} {
		if !tester.HasText(want) {
			t.Fatalf("detail page missing %q: %v", want, tester.Texts())
		}
	}
	if tester.HasText(testGHDigest) {
		t.Fatal("detail summary should show only the 12-character digest prefix")
	}
	for _, unwanted := range []string{"同步下游工具", "浏览器 OAuth 登录", "健康与维护概览", "编辑"} {
		if tester.HasText(unwanted) {
			t.Fatalf("CLI detail must not show %q", unwanted)
		}
	}
}

func TestCLIServiceListRowAndCredentialStatus(t *testing.T) {
	app := NewDesktopUI(&gatewayRuntime{UIURL: "http://127.0.0.1:1", Mode: modeAttached})
	server := ghTestServer()

	if serviceProtocolTag(server) != "CLI" || ServerTargetText(server) != "/opt/homebrew/bin/gh" {
		t.Fatalf("unexpected CLI tag/target: %q %q", serviceProtocolTag(server), ServerTargetText(server))
	}

	for _, tc := range []struct {
		name               string
		saved, environment bool
		status             string
	}{
		{"keychain", true, false, "已托管在钥匙串"},
		{"keychain wins over env", true, true, "已托管在钥匙串"},
		{"host env ignored", false, true, "未托管（宿主机环境变量不生效）"},
		{"missing", false, false, "未配置"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &servicesPageState{credentialServerID: "gh", credentialValues: map[string]string{}, credentialState: apitypes.ServerCredentials{
				Persistent:  true,
				Credentials: []apitypes.CredentialStatus{{Name: "GH_TOKEN", Configured: tc.saved, Saved: tc.saved, Environment: tc.environment}},
			}}
			tester := ui.NewTester(func(c *ui.Context) { app.renderServerCredentials(c, state, server) }, 1100, 500)
			if !tester.HasText("GH_TOKEN") || !tester.HasText(tc.status) {
				t.Fatalf("missing %q: %v", tc.status, tester.Texts())
			}
			if !tester.HasText("受控 CLI 仅读取网关凭据库（系统钥匙串）中的凭据，不读取宿主机环境变量") {
				t.Fatal("CLI credential card must explain the keychain-only source")
			}
			if tester.HasText("浏览器 OAuth 登录") {
				t.Fatal("CLI credentials must not offer OAuth")
			}
		})
	}

	// Readiness: an env-only GH_TOKEN is still missing for a controlled CLI.
	creds := &apitypes.ServerCredentials{Persistent: true, Credentials: []apitypes.CredentialStatus{{Name: "GH_TOKEN", Configured: false, Environment: true}}}
	r := evaluateServiceReadiness(server, ghTestTools(), apitypes.ConnectionCheck{}, false, creds, nil)
	if r.Kind != readinessMissingCreds || !strings.Contains(r.AlertDetail, "不读取宿主机环境变量") {
		t.Fatalf("expected CLI-specific missing credential readiness, got kind=%v detail=%q", r.Kind, r.AlertDetail)
	}

	boot := apitypes.Bootstrap{MCPURL: "http://127.0.0.1:8788/mcp"}
	boot.Registry.Servers = []apitypes.Server{server}
	boot.Registry.Tools = ghTestTools()
	boot.Setup.SourceChecks = map[string]apitypes.ConnectionCheck{"gh": {Status: "success", CheckedAt: time.Now()}}
	app.State = &boot
	app.Loading = false
	app.ViewMode = ViewServices
	app.marketState().loaded = true
	app.applyServerCredentialsSnapshot(boot.Registry.Servers, map[string]apitypes.ServerCredentials{"gh": {
		Persistent: true, Credentials: []apitypes.CredentialStatus{{Name: "GH_TOKEN", Configured: true, Saved: true}},
	}})
	st := app.servicesState()
	tester := ui.NewTester(func(c *ui.Context) {
		defer app.enterHeadlessFrame()()
		app.renderServiceList(c, st)
	}, 1400, 900)
	for _, want := range []string{"GitHub CLI", "CLI", "sha256:0123456789ab"} {
		if !tester.HasText(want) {
			t.Fatalf("service list row missing %q: %v", want, tester.Texts())
		}
	}
}
