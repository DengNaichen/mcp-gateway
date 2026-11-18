package main

import (
	"reflect"
	"testing"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"
)

func TestSummarizeServiceToolsAndReadOnlyIDs(t *testing.T) {
	tools := []apitypes.Tool{
		{ID: "srv.a", ServerID: "srv", Enabled: true, ReadOnly: true},
		{ID: "srv.b", ServerID: "srv", Enabled: true, ReadOnly: false},
		{ID: "srv.c", ServerID: "srv", Enabled: false, ReadOnly: true},
	}
	got := summarizeServiceTools(tools)
	want := serviceToolCounts{total: 3, enabled: 2, readOnly: 1, writable: 1}
	if got != want {
		t.Fatalf("summarizeServiceTools() = %+v, want %+v", got, want)
	}
	if ro := readOnlyToolIDs(tools); !reflect.DeepEqual(ro, []string{"srv.a", "srv.c"}) {
		t.Fatalf("readOnlyToolIDs() = %v, want [srv.a srv.c]", ro)
	}
}

func TestBuildAndFilterServiceListData(t *testing.T) {
	servers := []apitypes.Server{
		{ID: "ready-1", Name: "Ready One", Kind: "mcp", Transport: "http", Endpoint: "https://one.example/mcp", Enabled: true},
		{ID: "attn-1", Name: "Broken Auth", Kind: "mcp", Transport: "http", Endpoint: "https://two.example/mcp", Enabled: true},
		{ID: "pend-1", Name: "Unverified Svc", Kind: "mcp", Transport: "http", Endpoint: "https://three.example/mcp", Enabled: true},
		{ID: "demo-1", Name: "Demo Svc", Demo: true, Enabled: true},
		{ID: "off-1", Name: "Disabled Svc", Kind: "http", Transport: "http", Endpoint: "https://off.example", Enabled: false},
	}
	tools := []apitypes.Tool{
		{ID: "ready-1.ping", ServerID: "ready-1", Enabled: true, ReadOnly: true},
	}
	readinessFn := func(s apitypes.Server, _ []apitypes.Tool) serviceReadiness {
		switch s.ID {
		case "ready-1":
			return serviceReadiness{Kind: readinessReady}
		case "attn-1":
			return serviceReadiness{Kind: readinessMissingCreds, MissingVars: []string{"API_TOKEN"}}
		case "pend-1":
			return serviceReadiness{Kind: readinessUnverified}
		default:
			return serviceReadiness{Kind: readinessDisabled}
		}
	}

	data := buildServiceListData(servers, tools, readinessFn)
	if len(data.demoServers) != 1 || data.demoServers[0].ID != "demo-1" {
		t.Fatalf("unexpected demoServers: %+v", data.demoServers)
	}
	if len(data.rows) != 4 {
		t.Fatalf("expected 4 non-demo rows, got %d", len(data.rows))
	}
	// Stable triage order: attention, pending, ready, disabled.
	if data.rows[0].server.ID != "attn-1" || data.rows[1].server.ID != "pend-1" || data.rows[2].server.ID != "ready-1" || data.rows[3].server.ID != "off-1" {
		t.Fatalf("unexpected row order: %s, %s, %s, %s", data.rows[0].server.ID, data.rows[1].server.ID, data.rows[2].server.ID, data.rows[3].server.ID)
	}
	if data.enabledCount != 3 {
		t.Fatalf("enabledCount = %d, want 3", data.enabledCount)
	}
	if data.bucketCounts[triageAttention] != 1 || data.bucketCounts[triagePending] != 1 || data.bucketCounts[triageReady] != 1 || data.bucketCounts[triageDisabled] != 1 {
		t.Fatalf("unexpected bucketCounts: %+v", data.bucketCounts)
	}
	if len(data.probeTargets) != 1 || data.probeTargets[0].ID != "pend-1" {
		t.Fatalf("probeTargets = %+v, want [pend-1]", data.probeTargets)
	}

	byBucket := filterServiceListRows(data.rows, int(triageAttention)+1, "")
	if len(byBucket) != 1 || byBucket[0].server.ID != "attn-1" {
		t.Fatalf("filter by attention bucket = %+v", byBucket)
	}

	byVar := filterServiceListRows(data.rows, 0, "api_token")
	if len(byVar) != 1 || byVar[0].server.ID != "attn-1" {
		t.Fatalf("filter by missing var query = %+v", byVar)
	}

	if title := serviceListEmptyTitle(int(triageAttention)+1, ""); title != i18n.T("services.list.nothing_to_fix") {
		t.Fatalf("unexpected empty title: %q", title)
	}
	if title := serviceListEmptyTitle(0, "xyz"); title != i18n.T("services.list.no_match") {
		t.Fatalf("unexpected search empty title: %q", title)
	}
}

func TestCollectServerToolsAndConnFormRoundTrip(t *testing.T) {
	allTools := []apitypes.Tool{
		{ID: "a.1", ServerID: "a", Enabled: true, ReadOnly: true},
		{ID: "b.1", ServerID: "b", Enabled: true, ReadOnly: false},
		{ID: "a.2", ServerID: "a", Enabled: false, ReadOnly: true},
	}
	srvTools, counts := collectServerTools(allTools, "a")
	if len(srvTools) != 2 || counts != (serviceToolCounts{total: 2, enabled: 1, readOnly: 1, writable: 0}) {
		t.Fatalf("collectServerTools() = (%d, %+v)", len(srvTools), counts)
	}

	stdioOrig := apitypes.Server{
		ID:           "fs",
		Name:         "Filesystem",
		Description:  "Local files",
		Kind:         "mcp",
		Transport:    "stdio",
		Command:      "npx",
		WorkDir:      "/tmp",
		Args:         []string{"-y", "server-fs"},
		Env:          map[string]string{"TOKEN": "${FS_TOKEN}"},
		RedactEmails: true,
		RedactFields: []string{"ssn", "secret"},
		Enabled:      true,
	}
	var form serviceConnFormState
	form.hydrateConnForm(stdioOrig)
	form.connName = " Filesystem Renamed "
	form.connRedactFieldsText = "ssn, api_key"

	built, err := form.buildServerFromConnForm(stdioOrig)
	if err != nil {
		t.Fatalf("buildServerFromConnForm stdio error: %v", err)
	}
	if built.Name != "Filesystem Renamed" || !reflect.DeepEqual(built.RedactFields, []string{"ssn", "api_key"}) {
		t.Fatalf("unexpected built stdio server: %+v", built)
	}
	if built.Command != "npx" || !reflect.DeepEqual(built.Args, stdioOrig.Args) {
		t.Fatalf("stdio args/command mismatch: %+v", built)
	}

	form.connArgsText = "not json"
	if _, err := form.buildServerFromConnForm(stdioOrig); err == nil {
		t.Fatal("expected error for invalid args JSON")
	}

	httpOrig := apitypes.Server{
		ID:       "billing",
		Name:     "Billing",
		Kind:     "http",
		OAuth:    true,
		Endpoint: "https://api.example.com",
		Headers:  map[string]string{"Authorization": "Bearer ${KEY}"},
	}
	form.hydrateConnForm(httpOrig)
	builtHTTP, err := form.buildServerFromConnForm(httpOrig)
	if err != nil {
		t.Fatalf("buildServerFromConnForm http error: %v", err)
	}
	if builtHTTP.Transport != "http" || builtHTTP.OAuth || builtHTTP.Endpoint != "https://api.example.com" || builtHTTP.Headers["Authorization"] != "Bearer ${KEY}" {
		t.Fatalf("unexpected built http server: %+v", builtHTTP)
	}

	mcpRemote := apitypes.Server{
		ID:        "notion",
		Name:      "Notion",
		Kind:      "mcp",
		Transport: "http",
		OAuth:     true,
		Endpoint:  "https://mcp.notion.com/mcp",
	}
	form.hydrateConnForm(mcpRemote)
	builtMCP, err := form.buildServerFromConnForm(mcpRemote)
	if err != nil {
		t.Fatalf("buildServerFromConnForm mcp remote error: %v", err)
	}
	if !builtMCP.OAuth || builtMCP.Transport != "http" {
		t.Fatalf("expected OAuth=true preserved for mcp remote: %+v", builtMCP)
	}
}
