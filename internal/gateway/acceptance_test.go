package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func logsAcceptance(version, mode string) AcceptanceSuite {
	suite := AcceptanceSuite{ServerID: "service", Contracts: []ContractCase{}, Routing: []RoutingCase{{Name: "find-logs", Intent: "logs", Mode: "retrieval", Expected: []string{"service.logs"}}}}
	if mode == "empty" {
		suite.Routing = []RoutingCase{{Name: "removed-logs", Intent: "logs", Mode: "retrieval", NoMatch: true, Forbidden: []string{"service.logs"}}}
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
	exchange := HTTPExchange{Method: "GET", Path: path, Query: query, Status: 200, Response: response}
	if mode == "openapi" {
		args = map[string]any{"query": map[string]any{"filter": map[string]any{"service": "billing"}}}
		text := "fixture logs"
		exchange = HTTPExchange{Method: "GET", Path: "/logs", Query: map[string][]string{"filter[service]": {"billing"}}, Status: 200, Text: &text}
		response = json.RawMessage(`{"text":"fixture logs"}`)
	}
	suite.Contracts = []ContractCase{{Name: "logs-contract", ToolID: "service.logs", Arguments: args, Exchanges: []HTTPExchange{exchange}, Expected: response}}
	return suite
}
func setLogsAcceptance(t *testing.T, g *Gateway, version, mode string) {
	t.Helper()
	if err := g.maintenance.SaveAcceptance(logsAcceptance(version, mode)); err != nil {
		t.Fatal(err)
	}
}
func approveRun(t *testing.T, m *Maintainer, id string) MaintenanceRun {
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
	g, reference := testGateway(t)
	m := g.maintenance
	// The coding agent's own verify script succeeds, but the owner's expected
	// request deliberately disagrees. Publication must still be blocked.
	putSource(t, reference, "v1", "adapter")
	suite := logsAcceptance("v1", "adapter")
	suite.Contracts[0].Exchanges[0].Path = "/approved-path"
	if err := m.SaveAcceptance(suite); err != nil {
		t.Fatal(err)
	}
	g.StartMaintenance(context.Background())
	run := waitRun(t, m, enqueueTest(t, m, false).ID, "ready")
	if run.Acceptance.Passed || !run.Acceptance.Configured {
		t.Fatal("bad request passed owner acceptance")
	}
	if err := m.Publish(run.ID, false); err == nil {
		t.Fatal("failed independent tests published")
	}
	if len(serverTools(g.Snapshot(), "service")) != 0 {
		t.Fatal("failed draft exposed tools")
	}
	// Prove fixtures did not enter the generation input.
	input, err := os.ReadFile(filepath.Join(m.directory(run.ID), "input.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(input), "approved-path") {
		t.Fatal("owner acceptance leaked to agent")
	}
}
func TestPublicationRequiresApprovalAndAcceptsCompatibleChanges(t *testing.T) {
	g, reference := testGateway(t)
	m := g.maintenance
	g.StartMaintenance(context.Background())
	first := waitRun(t, m, enqueueTest(t, m, false).ID, "ready")
	if !first.Review.Required || first.Tools[0].Enabled {
		t.Fatal("new capability was enabled automatically")
	}
	if err := m.publish(first.ID, false, false); err == nil {
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
	// Required input widening and deletion require explicit review even if tests pass.
	previous := second.Tools[0]
	changed := previous
	changed.InputSchema = objectSchema(map[string]any{"service": map[string]any{"type": "string"}, "region": map[string]any{"type": "string"}}, "service", "region")
	if !publicationReview([]Tool{previous}, []Tool{changed}).Required {
		t.Fatal("new required argument was considered compatible")
	}
	changed = previous
	changed.ReadOnly = false
	if !publicationReview([]Tool{previous}, []Tool{changed}).Required {
		t.Fatal("write escalation allowed")
	}
	if !publicationReview([]Tool{previous}, nil).Required {
		t.Fatal("removal did not require approval")
	}
}
func TestAcceptanceChangeInvalidatesDraft(t *testing.T) {
	g, _ := testGateway(t)
	m := g.maintenance
	g.StartMaintenance(context.Background())
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

func TestMappingAndExecutableChangesRequireReview(t *testing.T) {
	tools, err := ImportOpenAPI("ci", []byte(openAPIFixture), []string{"create_build"})
	if err != nil {
		t.Fatal(err)
	}
	before := tools[0]
	var after Tool
	if err := json.Unmarshal(mustJSON(t, before), &after); err != nil {
		t.Fatal(err)
	}
	// Serialization can change while the advertised input schema stays identical.
	after.HTTP.Parameters[1].Explode = !after.HTTP.Parameters[1].Explode
	if !publicationReview([]Tool{before}, []Tool{after}).Required {
		t.Fatal("serialization change auto-published")
	}
	g, _ := testGateway(t)
	old := Tool{ID: "service.logs", Adapter: "adapter.mjs", Revision: "previous"}
	g.maintenance.runs["previous"] = MaintenanceRun{Artifacts: map[string]string{"adapter.mjs": "old-hash"}}
	r := MaintenanceRun{Before: []Tool{old}, Tools: []Tool{old}, Artifacts: map[string]string{"adapter.mjs": "changed-hash"}}
	review := publicationReview(r.Before, r.Tools)
	g.maintenance.reviewArtifacts(r, &review)
	if !review.Required {
		t.Fatal("changed executable published without owner review")
	}
}
func TestSchemaCompatibilityConservative(t *testing.T) {
	old := objectSchema(map[string]any{"service": map[string]any{"type": "string"}}, "service")
	next := objectSchema(map[string]any{"service": map[string]any{"type": "string", "description": "renamed description"}, "limit": map[string]any{"type": "integer"}}, "service")
	if !inputCompatible(old, next) {
		t.Fatal("optional extension should be compatible")
	}
	if inputCompatible(next, old) {
		t.Fatal("removed parameter considered compatible")
	}
}

func TestPropertyNamesAreNotSchemaAnnotations(t *testing.T) {
	old := objectSchema(map[string]any{"description": map[string]any{"type": "string"}}, "description")
	changed := objectSchema(map[string]any{"description": map[string]any{"type": "integer"}}, "description")
	if inputCompatible(old, changed) {
		t.Fatal("property named description was mistaken for annotation")
	}
}
