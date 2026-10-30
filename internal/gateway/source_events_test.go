package gateway

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(processEnvironment(nil), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Gateway Test", "GIT_AUTHOR_EMAIL=gateway@example.invalid", "GIT_COMMITTER_NAME=Gateway Test", "GIT_COMMITTER_EMAIL=gateway@example.invalid")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
func testRepository(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	testGit(t, dir, "init", "-b", "main")
	putSource(t, filepath.Join(dir, "api.json"), "v1", "")
	testGit(t, dir, "add", "api.json")
	testGit(t, dir, "commit", "-m", "initial contract")
	return dir, testGit(t, dir, "rev-parse", "HEAD")
}
func TestRepositorySnapshotAndContentChange(t *testing.T) {
	g, _ := testGateway(t)
	repo, firstSHA := testRepository(t)
	m := g.maintenance
	m.mu.Lock()
	source := m.sources["service"]
	m.mu.Unlock()
	source.Files = nil
	source.Repository = &RepositorySource{URL: repo, Ref: "main", Files: []string{"api.json"}}
	if err := m.SaveSource(source); err != nil {
		t.Fatal(err)
	}
	g.StartMaintenance(context.Background())
	first := approveRun(t, m, enqueueTest(t, m, false).ID)
	if first.Provenance.Commit != firstSHA || first.Provenance.Documents["repository:api.json"] == "" {
		t.Fatal("missing provenance")
	}
	// A commit outside selected source files does not regenerate the integration.
	if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "add", "notes.txt")
	testGit(t, repo, "commit", "-m", "unrelated change")
	unchanged := waitRun(t, m, enqueueTest(t, m, false).ID, "no_change")
	if unchanged.Provenance.Commit == firstSHA {
		t.Fatal("new source commit not recorded")
	}
	putSource(t, filepath.Join(repo, "api.json"), "v2", "")
	testGit(t, repo, "add", "api.json")
	testGit(t, repo, "commit", "-m", "interface changed")
	setLogsAcceptance(t, g, "v2", "")
	changed := approveRun(t, m, enqueueTest(t, m, false).ID)
	if changed.Tools[0].Path != "/logs/v2" || changed.Provenance.Commit != testGit(t, repo, "rev-parse", "HEAD") {
		t.Fatal("new contract was not pinned")
	}
	if testGit(t, repo, "status", "--porcelain") != "" {
		t.Fatal("gateway modified the source checkout")
	}
}
func signedEvent(t *testing.T, body []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte("gateway-test-signing-secret"))
	if _, err := mac.Write(body); err != nil {
		t.Fatal(err)
	}
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
func TestSourceWebhookAuthenticationCoalescingAndReplay(t *testing.T) {
	g, _ := testGateway(t)
	repo, sha := testRepository(t)
	m := g.maintenance
	t.Setenv("GATEWAY_TEST_WEBHOOK_SECRET", "gateway-test-signing-secret")
	m.mu.Lock()
	source := m.sources["service"]
	m.mu.Unlock()
	source.Files = nil
	source.Repository = &RepositorySource{URL: repo, Ref: "main", Files: []string{"api.json"}, Project: "team/service", EventsEnabled: true, WebhookSecretEnv: "GATEWAY_TEST_WEBHOOK_SECRET"}
	if err := m.SaveSource(source); err != nil {
		t.Fatal(err)
	}
	handler := g.Handler(os.DirFS(t.TempDir()))
	send := func(id, signature, ref string) *httptest.ResponseRecorder {
		body := mustJSON(t, map[string]any{"ref": ref, "after": sha, "repository": map[string]string{"full_name": "team/service"}})
		req := httptest.NewRequest(http.MethodPost, "/api/maintenance/sources/service/webhook/github", bytes.NewReader(body))
		req.Host = "localhost"
		req.Header.Set("X-GitHub-Delivery", id)
		req.Header.Set("X-GitHub-Event", "push")
		if signature == "valid" {
			signature = signedEvent(t, body)
		}
		req.Header.Set("X-Hub-Signature-256", signature)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	if got := send("bad", "sha256=bad", "refs/heads/main"); got.Code != 401 {
		t.Fatalf("bad signature accepted: %d", got.Code)
	}
	if got := send("wrong-branch", "valid", "refs/heads/other"); got.Code != 200 || !strings.Contains(got.Body.String(), "ignored") {
		t.Fatal("wrong branch not ignored")
	}
	if got := send("one", "valid", "refs/heads/main"); got.Code != 202 {
		t.Fatalf("event not accepted: %d %s", got.Code, got.Body)
	}
	if got := send("two", "valid", "refs/heads/main"); got.Code != 202 {
		t.Fatal("second event not accepted")
	}
	// Restart recovery reads pending deliveries from SQLite, before any job started.
	reloaded, err := newMaintainer(g)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	reloaded.mu.Lock()
	err = reloaded.dispatchSignals()
	reloaded.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.runs) != 1 {
		t.Fatalf("events were not coalesced: %d", len(reloaded.runs))
	}
	var run MaintenanceRun
	for _, r := range reloaded.runs {
		run = r
	}
	if run.Trigger.DeliveryID != "two" || run.Trigger.Commit != sha {
		t.Fatalf("wrong trigger: %+v", run.Trigger)
	}
	var assigned int
	if err := g.db.QueryRow("SELECT COUNT(*) FROM maintenance_deliveries WHERE run_id=?", run.ID).Scan(&assigned); err != nil {
		t.Fatal(err)
	}
	if assigned != 2 {
		t.Fatal("deliveries not assigned transactionally")
	}
	if got := send("one", "valid", "refs/heads/main"); got.Code != 200 || !strings.Contains(got.Body.String(), "duplicate") {
		t.Fatal("replay not deduplicated")
	}
	// A later event remains pending while this source already has a queued job.
	send("three", "valid", "refs/heads/main")
	reloaded.mu.Lock()
	err = reloaded.dispatchSignals()
	reloaded.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.runs) != 1 {
		t.Fatal("overlapping job created")
	}
	reloaded.mu.Lock()
	run.Status = "ready"
	err = reloaded.saveRun(run)
	if err == nil {
		err = reloaded.dispatchSignals()
	}
	reloaded.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.runs) != 2 {
		t.Fatal("follow-up event was lost")
	}
}
func TestSourceCIAndReleaseEvents(t *testing.T) {
	t.Setenv("GATEWAY_TEST_WEBHOOK_SECRET", "gateway-test-signing-secret")
	repo := RepositorySource{Project: "team/service", Ref: "main", WebhookSecretEnv: "GATEWAY_TEST_WEBHOOK_SECRET"}
	sha := strings.Repeat("a", 40)
	for _, test := range []struct {
		provider, event string
		body            any
		kind            string
	}{
		{"gitlab", "Pipeline Hook", map[string]any{"project": map[string]string{"path_with_namespace": "team/service"}, "object_attributes": map[string]string{"ref": "main", "sha": sha, "status": "success"}}, "ci_completed"},
		{"github", "workflow_run", map[string]any{"repository": map[string]string{"full_name": "team/service"}, "action": "completed", "workflow_run": map[string]string{"head_branch": "main", "head_sha": sha}}, "ci_completed"},
		{"generic", "", map[string]string{"project_id": "team/service", "event": "release", "ref": "main", "commit": sha}, "release"},
	} {
		body := mustJSON(t, test.body)
		headers := http.Header{}
		headers.Set("X-Gitlab-Token", "gateway-test-signing-secret")
		headers.Set("X-Gitlab-Event-UUID", "ci-delivery")
		headers.Set("X-Gitlab-Event", test.event)
		headers.Set("X-Hub-Signature-256", signedEvent(t, body))
		headers.Set("X-GitHub-Delivery", "ci-delivery")
		headers.Set("X-GitHub-Event", test.event)
		headers.Set("X-Gateway-Signature", signedEvent(t, body))
		headers.Set("X-Gateway-Delivery", "release-delivery")
		signal, relevant, err := parseSourceSignal(test.provider, headers, body, repo)
		if err != nil || !relevant || signal.Kind != test.kind {
			t.Fatalf("event parsing: %+v %v %v", signal, relevant, err)
		}
	}
}
