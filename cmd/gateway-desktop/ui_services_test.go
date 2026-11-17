package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestServicesAndPreviewScope401TransitionToNeedsLogin(t *testing.T) {
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

	// 2. Repos PreviewScope 401 -> NeedsLogin=true, BusyCount=0.
	uiRepos := NewDesktopUI(&gatewayRuntime{UIURL: srv.URL, MCPURL: boot.MCPURL, Mode: modeAttached})
	uiRepos.State = &boot
	uiRepos.Loading = false
	uiRepos.ViewMode = ViewRepos

	tester := ui.NewTester(uiRepos.View, 1280, 820)
	waitAsyncIdle(t, tester, uiRepos)
	if !uiRepos.NeedsLogin {
		t.Fatal("expected PreviewScope HTTP 401 to transition DesktopUI to NeedsLogin=true")
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
