package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gatewaylab/internal/maintenance"
)

func logsAcceptance(version, mode string) maintenance.AcceptanceSuite {
	suite := maintenance.AcceptanceSuite{ServerID: "service", Contracts: []maintenance.ContractCase{}, Routing: []maintenance.RoutingCase{{Name: "find-logs", Intent: "logs", Expected: []string{"service.logs"}}}}
	if mode == "empty" {
		suite.Routing = []maintenance.RoutingCase{{Name: "removed-logs", Intent: "logs", NoMatch: true, Forbidden: []string{"service.logs"}}}
		return suite
	}
	args := map[string]any{"service": "billing"}
	query := map[string][]string{"service": {"billing"}}
	path := "/logs/" + version
	if mode == "adapter" {
		path = "/logs/billing"
		query = nil
	}
	if mode == "real-pi" {
		args = map[string]any{}
		query = nil
		path = "/logs"
	}
	response := json.RawMessage(`{"ok":true}`)
	exchange := maintenance.HTTPExchange{Method: "GET", Path: path, Query: query, Status: 200, Response: response}
	if mode == "openapi" {
		args = map[string]any{"query": map[string]any{"filter": map[string]any{"service": "billing"}}}
		text := "fixture logs"
		exchange = maintenance.HTTPExchange{Method: "GET", Path: "/logs", Query: map[string][]string{"filter[service]": {"billing"}}, Status: 200, Text: &text}
		response = json.RawMessage(`{"text":"fixture logs"}`)
	}
	suite.Contracts = []maintenance.ContractCase{{Name: "logs-contract", ToolID: "service.logs", Arguments: args, Exchanges: []maintenance.HTTPExchange{exchange}, Expected: response}}
	return suite
}
func setLogsAcceptance(t *testing.T, m *maintenance.Maintainer, version, mode string) {
	t.Helper()
	if err := m.SaveAcceptance(logsAcceptance(version, mode)); err != nil {
		t.Fatal(err)
	}
}
func approveRun(t *testing.T, m *maintenance.Maintainer, id string) maintenance.MaintenanceRun {
	t.Helper()
	r := waitRun(t, m, id, "ready", "published")
	if r.Status == "published" {
		return r
	}
	if !r.Acceptance.Configured || !r.Acceptance.Passed {
		t.Fatalf("cannot approve failed acceptance: %+v", r.Acceptance)
	}
	if err := m.Publish(id, false); err != nil {
		t.Fatal(err)
	}
	return waitRun(t, m, id, "published")
}
func TestIndependentAcceptanceBlocksPublish(t *testing.T) {
	_, svcs, reference := testGateway(t)
	m := svcs.Maintenance
	// The coding agent's own verify script succeeds, but the owner's expected
	// request deliberately disagrees. Publication must still be blocked.
	putSource(t, reference, "v1", "adapter")
	suite := logsAcceptance("v1", "adapter")
	suite.Contracts[0].Exchanges[0].Path = "/approved-path"
	if err := m.SaveAcceptance(suite); err != nil {
		t.Fatal(err)
	}
	m.Start(context.Background())
	run := waitRun(t, m, enqueueTest(t, m, false).ID, "ready")
	if run.Acceptance.Passed || !run.Acceptance.Configured {
		t.Fatal("bad request passed owner acceptance")
	}
	if err := m.Publish(run.ID, false); err == nil {
		t.Fatal("failed independent tests published")
	}
	if len(serverTools(svcs.Registry.Snapshot(), "service")) != 0 {
		t.Fatal("failed draft exposed tools")
	}
	// Prove fixtures did not enter the generation input.
	input, err := os.ReadFile(filepath.Join(filepath.Join(svcs.DataDir, "maintenance", run.ID), "input.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(input), "approved-path") {
		t.Fatal("owner acceptance leaked to agent")
	}
}
func TestPublicationRequiresApprovalAndAcceptsCompatibleChanges(t *testing.T) {
	_, svcs, reference := testGateway(t)
	m := svcs.Maintenance
	m.Start(context.Background())
	first := waitRun(t, m, enqueueTest(t, m, false).ID, "ready")
	if !first.Review.Required || first.Tools[0].Enabled {
		t.Fatal("new capability was enabled automatically")
	}
	if err := m.PublishAs(first.ID, false, false, "scheduler"); err == nil {
		t.Fatal("automatic new capability publication accepted")
	}
	approveRun(t, m, first.ID)
	// Same wire contract and proven behavior; only the source's descriptive note
	// changes. The fake agent emits the same definition, so auto publication is safe.
	if err := writeJSON(reference, map[string]string{"version": "v1", "note": "documentation update"}); err != nil {
		t.Fatal(err)
	}
	second := waitRun(t, m, enqueueTest(t, m, false).ID, "published")
	if second.Review.Required || !second.Acceptance.Passed {
		t.Fatal("compatible change did not pass policy")
	}
}

func TestAcceptanceChangeInvalidatesDraft(t *testing.T) {
	_, svcs, _ := testGateway(t)
	m := svcs.Maintenance
	m.Start(context.Background())
	r := waitRun(t, m, enqueueTest(t, m, false).ID, "ready")
	suite := logsAcceptance("v1", "")
	suite.Routing[0].Intent = "error logs"
	if err := m.SaveAcceptance(suite); err != nil {
		t.Fatal(err)
	}
	if err := m.Publish(r.ID, false); err == nil {
		t.Fatal("draft published with stale acceptance evidence")
	}
}

func TestMaintenancePublicationRejectsChangedModelConfiguration(t *testing.T) {
	g, svcs, _ := testGateway(t)
	svcs.Maintenance.Start(context.Background())
	run := waitRun(t, svcs.Maintenance, enqueueTest(t, svcs.Maintenance, false).ID, "ready")
	settings, _ := svcs.Model.Config()
	settings.Model = "changed-after-acceptance"
	if err := g.SaveSettings(settings, "", false); err != nil {
		t.Fatal(err)
	}
	err := svcs.Maintenance.Publish(run.ID, false)
	if !errors.Is(err, maintenance.ErrConflict) || err.Error() != "路由配置已变化，请重新验收" {
		t.Fatalf("draft accepted stale model evidence: %v", err)
	}
	if len(serverTools(svcs.Registry.Snapshot(), "service")) != 0 {
		t.Fatal("stale acceptance changed active tools")
	}
	persisted, err := svcs.Maintenance.GetRun(run.ID)
	if err != nil || persisted.Status != "ready" {
		t.Fatalf("rejected publication changed draft: %+v %v", persisted, err)
	}
}
