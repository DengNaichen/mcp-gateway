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

func TestFilterPaginateAndBuildToolPayloads(t *testing.T) {
	tools := []apitypes.Tool{
		{ID: "s.read_a", Name: "read_a", Description: "Read alpha", Enabled: true, ReadOnly: true},
		{ID: "s.write_b", Name: "write_b", Description: "Write beta", Enabled: true, ReadOnly: false},
		{ID: "s.read_c", Name: "read_c", Description: "Read gamma", Enabled: false, ReadOnly: true},
	}

	if ids := allToolIDs(tools); !reflect.DeepEqual(ids, []string{"s.read_a", "s.write_b", "s.read_c"}) {
		t.Fatalf("allToolIDs() = %v", ids)
	}
	if got := filterServiceTools(tools, 1, ""); len(got) != 2 {
		t.Fatalf("enabled filter count = %d, want 2", len(got))
	}
	if got := filterServiceTools(tools, 2, ""); len(got) != 1 || got[0].ID != "s.read_c" {
		t.Fatalf("disabled filter = %+v", got)
	}
	if got := filterServiceTools(tools, 3, "gamma"); len(got) != 1 || got[0].ID != "s.read_c" {
		t.Fatalf("readonly+query filter = %+v", got)
	}

	pg := paginateServiceTools(tools, 99, 2)
	if pg.page != 2 || pg.totalPages != 2 || pg.start != 2 || pg.end != 3 || !pg.hasFooter || len(pg.items) != 1 {
		t.Fatalf("unexpected pagination: %+v", pg)
	}
	pgMin := paginateServiceTools(tools, 0, 2)
	if pgMin.page != 1 || pgMin.start != 0 || pgMin.end != 2 {
		t.Fatalf("unexpected clamped low page: %+v", pgMin)
	}

	toggled := buildWhitelistWithToolToggled(tools, "s.read_c", true)
	if !reflect.DeepEqual(toggled, []string{"s.read_a", "s.write_b", "s.read_c"}) {
		t.Fatalf("buildWhitelistWithToolToggled(enable) = %v", toggled)
	}
	toggledOff := buildWhitelistWithToolToggled(tools, "s.write_b", false)
	if !reflect.DeepEqual(toggledOff, []string{"s.read_a"}) {
		t.Fatalf("buildWhitelistWithToolToggled(disable) = %v", toggledOff)
	}

	var edit serviceToolEditState
	edit.open(tools[0])
	edit.desc = " Updated description "
	edit.actors = "developer, observer"
	edit.readOnly = false
	builtEdit, err := edit.buildPayload(tools[0])
	if err != nil {
		t.Fatalf("edit.buildPayload error: %v", err)
	}
	if builtEdit.Description != "Updated description" || builtEdit.ReadOnly || !reflect.DeepEqual(builtEdit.Actors, []string{"developer", "observer"}) {
		t.Fatalf("unexpected edited tool payload: %+v", builtEdit)
	}
	edit.tab = 1
	edit.rawJSON = "{bad json"
	if _, err := edit.buildPayload(tools[0]); err == nil {
		t.Fatal("expected error for invalid tool JSON")
	}

	httpSt := defaultServiceHTTPToolState()
	httpSt.name = "get_invoice"
	httpSt.path = "/invoices/{id}"
	httpSt.desc = "Get invoice"
	httpTool, err := httpSt.buildPayload("billing")
	if err != nil {
		t.Fatalf("httpSt.buildPayload error: %v", err)
	}
	if httpTool.ID != "billing.get_invoice" || httpTool.Method != "GET" || !httpTool.Enabled || !httpTool.ReadOnly {
		t.Fatalf("unexpected HTTP tool payload: %+v", httpTool)
	}
	httpSt.name = ""
	if _, err := httpSt.buildPayload("billing"); err == nil {
		t.Fatal("expected error for missing required HTTP tool fields")
	}
}

func TestAddServiceFormParseAndPayloads(t *testing.T) {
	// 1. Standard mcpServers stdio JSON
	add := defaultServiceAddFormState()
	add.show = true
	add.tab = 3
	add.pasteText = `{"mcpServers":{"GitHub Tools":{"command":"npx","cwd":"/tmp","args":["-y","@modelcontextprotocol/server-github"],"env":{"GITHUB_TOKEN":"${GITHUB_TOKEN}"}}}}`
	add.parsePasteJSON()
	if add.errMsg != "" || add.tab != 0 || add.id != "github-tools" || add.command != "npx" || add.workDir != "/tmp" {
		t.Fatalf("unexpected stdio paste result: %+v", add)
	}
	mcpSrv, err := add.buildMCPServerPayload()
	if err != nil {
		t.Fatalf("buildMCPServerPayload stdio error: %v", err)
	}
	if mcpSrv.Transport != "stdio" || mcpSrv.Env["GITHUB_TOKEN"] != "${GITHUB_TOKEN}" || len(mcpSrv.Args) != 2 {
		t.Fatalf("unexpected stdio payload: %+v", mcpSrv)
	}

	// 2. Standard mcpServers SSE + OAuth JSON
	add.pasteText = `{"mcpServers":{"Linear":{"type":"sse","url":"https://mcp.linear.app/sse","oauth":true,"headers":{"Authorization":"Bearer ${LIN_TOKEN}"}}}}`
	add.parsePasteJSON()
	if add.errMsg != "" || add.tab != 1 || !add.remoteOAuth || add.remoteProto != i18n.T("services.add.proto_sse") || add.endpoint != "https://mcp.linear.app/sse" {
		t.Fatalf("unexpected SSE paste result: %+v", add)
	}
	remoteSrv, err := add.buildMCPServerPayload()
	if err != nil {
		t.Fatalf("buildMCPServerPayload remote error: %v", err)
	}
	if remoteSrv.Transport != "http" || !remoteSrv.OAuth || remoteSrv.Headers["Authorization"] != "Bearer ${LIN_TOKEN}" {
		t.Fatalf("unexpected remote payload: %+v", remoteSrv)
	}

	// 3. Multi-entry mcpServers error
	add.pasteText = `{"mcpServers":{"a":{},"b":{}}}`
	add.parsePasteJSON()
	if add.errMsg == "" {
		t.Fatal("expected error when pasting multiple mcpServers entries")
	}

	// 4. Gateway Server HTTP format -> OpenAPI tab
	add.pasteText = `{"id":"billing","name":"Billing","kind":"http","endpoint":"https://api.acme.dev","headers":{"X-Key":"${K}"}}`
	add.parsePasteJSON()
	if add.errMsg != "" || add.tab != 2 || add.openAPIBaseURL != "https://api.acme.dev" {
		t.Fatalf("unexpected gateway http paste result: %+v", add)
	}
	add.openAPISpecURL = "https://api.acme.dev/openapi.json"
	add.openAPIOpsText = "listInvoices, createInvoice"
	req, err := add.buildOpenAPIServerPayload()
	if err != nil {
		t.Fatalf("buildOpenAPIServerPayload error: %v", err)
	}
	if req.server.Kind != "http" || !reflect.DeepEqual(req.opIDs, []string{"listInvoices", "createInvoice"}) {
		t.Fatalf("unexpected openapi payload: %+v", req)
	}

	// 5. Reset preserves show/tab while restoring defaults
	add.show = true
	add.tab = 2
	add.reset()
	if !add.show || add.tab != 2 || add.argsText != "[]" || add.envText != "{}" || add.openAPIBaseURL != "" {
		t.Fatalf("unexpected reset state: %+v", add)
	}
}
