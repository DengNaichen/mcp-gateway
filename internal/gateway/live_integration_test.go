package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Explicit opt-in: only GETs. Tokens and log contents are never printed or saved.
func TestLiveGitLabMaintenanceChangeAndRollback(t *testing.T) {
	endpoint, project := os.Getenv("GATEWAY_TEST_GITLAB_URL"), os.Getenv("GATEWAY_TEST_GITLAB_PROJECT")
	if endpoint == "" || project == "" || os.Getenv("GATEWAY_TEST_GITLAB_TOKEN") == "" {
		t.Skip("set GATEWAY_TEST_GITLAB_URL/PROJECT/TOKEN for read-only CI/log integration")
	}
	g, _ := testGateway(t)
	m := g.maintenance
	if err := g.SaveServer(Server{ID: "gitlab", Name: "GitLab CI", Kind: "http", Endpoint: endpoint, Enabled: true, Headers: map[string]string{"Private-Token": "${GATEWAY_TEST_GITLAB_TOKEN}"}, RedactEmails: true}); err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile("../../examples/gitlab-openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(file, spec, 0600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../examples/gitlab-acceptance.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite AcceptanceSuite
	if err := json.Unmarshal(b, &suite); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveAcceptance(suite); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveSource(MaintenanceSource{ServerID: "gitlab", Format: "openapi", Files: []string{file}, AutoPublish: true, IntervalMinutes: 60, TimeoutSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	g.StartMaintenance(context.Background())
	run, err := m.Run("gitlab", false)
	if err != nil {
		t.Fatal(err)
	}
	first := approveRun(t, m, run.ID)
	invoke := func(id string, args map[string]any) any {
		t.Helper()
		r, err := g.Invoke(context.Background(), InvokeRequest{ToolID: "gitlab." + id, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return r.Data
	}
	pipelines := invoke("pipelines", map[string]any{"path": map[string]any{"project": project}, "query": map[string]any{"per_page": float64(20)}}).([]any)
	if len(pipelines) == 0 {
		t.Fatal("no real pipelines available")
	}
	var pipeline map[string]any
	var jobs []any
	var jobID any
	// The newest pipeline may have just started. Select an already finished job
	// rather than treating ordinary upstream activity as an integration failure.
	for _, candidate := range pipelines {
		pipeline = candidate.(map[string]any)
		if !slices.Contains([]any{"success", "failed", "canceled"}, pipeline["status"]) {
			continue
		}
		jobs = invoke("jobs", map[string]any{"path": map[string]any{"project": project, "pipeline_id": pipeline["id"]}, "query": map[string]any{"per_page": float64(100)}}).([]any)
		for _, v := range jobs {
			j := v.(map[string]any)
			if slices.Contains([]any{"success", "failed"}, j["status"]) {
				jobID = j["id"]
				break
			}
		}
		if jobID != nil {
			break
		}
	}
	if jobID == nil {
		t.Fatal("no finished job with logs available")
	}
	log := invoke("job_trace", map[string]any{"path": map[string]any{"project": project, "job_id": jobID}}).(map[string]any)["text"].(string)
	if len(log) == 0 {
		t.Fatal("empty live log")
	}
	t.Logf("real pipeline status=%v, jobs=%d, log bytes=%d; contents withheld", pipeline["status"], len(jobs), len(log))
	changed := strings.ReplaceAll(string(spec), "List CI pipeline statuses for a project", "List latest CI pipeline statuses for a project")
	if err := os.WriteFile(file, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	next, err := m.Run("gitlab", false)
	if err != nil {
		t.Fatal(err)
	}
	updated := waitRun(t, m, next.ID, "published")
	if updated.Fingerprint == first.Fingerprint || updated.Review.Required {
		t.Fatal("compatible source change was not detected and automatically released")
	}
	route, err := g.Discover(context.Background(), DiscoverRequest{Intent: "latest CI pipeline status", Mode: "retrieval"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(route.Tools, func(s Selection) bool { return s.ID == "gitlab.pipelines" }) {
		t.Fatal("updated tool missing from routing candidates")
	}
	if err := m.Publish(next.ID, true); err != nil {
		t.Fatal(err)
	}
	if maintenanceDigest(serverTools(g.Snapshot(), "gitlab")) != maintenanceDigest(first.Tools) {
		t.Fatal("rollback did not restore prior catalog")
	}
	t.Log("source update, independent acceptance, automatic compatible publication, routing and rollback passed")
}
