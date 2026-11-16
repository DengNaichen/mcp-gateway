package maintenance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"gatewaylab/internal/store"
	"net/http"
	"strings"
	"testing"
)

func signedEvent(t *testing.T, body []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte("gateway-test-signing-secret"))
	if _, err := mac.Write(body); err != nil {
		t.Fatal(err)
	}
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
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

func TestPendingSignalsCoalesceAfterRestart(t *testing.T) {
	m := testMaintainer(t)
	sha := strings.Repeat("a", 40)
	t.Setenv("GATEWAY_TEST_WEBHOOK_SECRET", "gateway-test-signing-secret")
	source := m.sources["service"]
	source.Files = nil
	source.Repository = &RepositorySource{URL: t.TempDir(), Ref: "main", Files: []string{"api.json"}, Project: "team/service", EventsEnabled: true, WebhookSecretEnv: "GATEWAY_TEST_WEBHOOK_SECRET"}
	if err := m.SaveSource(source); err != nil {
		t.Fatal(err)
	}
	send := func(id string) (DeliveryResult, error) {
		body := mustJSON(t, map[string]any{"ref": "refs/heads/main", "after": sha, "repository": map[string]string{"full_name": "team/service"}})
		headers := http.Header{}
		headers.Set("X-GitHub-Delivery", id)
		headers.Set("X-GitHub-Event", "push")
		headers.Set("X-Hub-Signature-256", signedEvent(t, body))
		return m.ReceiveSourceEvent(source, "github", headers, body)
	}
	for _, id := range []string{"one", "two"} {
		if _, err := send(id); err != nil {
			t.Fatal(err)
		}
	}
	// Restart recovery reads pending deliveries from SQLite, before any job started.
	reloaded, err := New(testOptions(m))
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
	if err := m.db.QueryRow("SELECT COUNT(*) FROM maintenance_deliveries WHERE run_id=?", run.ID).Scan(&assigned); err != nil {
		t.Fatal(err)
	}
	if assigned != 2 {
		t.Fatal("deliveries not assigned transactionally")
	}
	if result, err := send("one"); err != nil || !result.Duplicate {
		t.Fatal("replay not deduplicated")
	}
	// A later event remains pending while this source already has a queued job.
	if _, err := send("three"); err != nil {
		t.Fatal(err)
	}
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

func TestSourceEventRejectsChangedConfiguration(t *testing.T) {
	m := testMaintainer(t)
	t.Setenv("GATEWAY_TEST_WEBHOOK_SECRET", "gateway-test-signing-secret")
	source, _ := m.Source("service")
	source.Repository = &RepositorySource{URL: t.TempDir(), Ref: "main", Files: []string{"api.json"}, Project: "team/service", EventsEnabled: true, WebhookSecretEnv: "GATEWAY_TEST_WEBHOOK_SECRET"}
	if err := m.SaveSource(source); err != nil {
		t.Fatal(err)
	}
	old, _ := m.Source("service")
	source.Instructions = "configuration changed while reading the request body"
	if err := m.SaveSource(source); err != nil {
		t.Fatal(err)
	}
	body := mustJSON(t, map[string]any{"ref": "refs/heads/main", "after": strings.Repeat("a", 40), "repository": map[string]string{"full_name": "team/service"}})
	headers := http.Header{}
	headers.Set("X-GitHub-Delivery", "changed-config")
	headers.Set("X-GitHub-Event", "push")
	headers.Set("X-Hub-Signature-256", signedEvent(t, body))
	_, err := m.ReceiveSourceEvent(old, "github", headers, body)
	if !errors.Is(err, ErrConflict) || err.Error() != "来源配置已变化，请重新投递事件" {
		t.Fatalf("configuration race was accepted: %v", err)
	}
	pending, err := store.MaintenanceDeliveries[sourceSignal](m.db).Pending()
	if err != nil || len(pending) != 0 {
		t.Fatalf("configuration conflict persisted a signal: %+v %v", pending, err)
	}
}

// FuzzParseSourceSignal feeds arbitrary headers and bodies to webhook parsing.
// It must never panic, an event whose signature or token does not match the
// secret must be rejected as unauthorized, and an accepted event must carry a
// valid delivery ID and commit SHA.
func FuzzParseSourceSignal(f *testing.F) {
	const secret = "gateway-test-signing-secret"
	f.Setenv("GATEWAY_FUZZ_WEBHOOK_SECRET", secret)
	repo := RepositorySource{Project: "team/service", Ref: "main", WebhookSecretEnv: "GATEWAY_FUZZ_WEBHOOK_SECRET"}
	sha := strings.Repeat("a", 40)
	push := `{"ref":"refs/heads/main","after":"` + sha + `","repository":{"full_name":"team/service"},"project":{"path_with_namespace":"team/service"}}`
	f.Add("github", "push", "d1", "", "", true, []byte(push))
	f.Add("github", "workflow_run", "d2", "", "", true, []byte(`{"action":"completed","repository":{"full_name":"team/service"},"workflow_run":{"head_branch":"main","head_sha":"`+sha+`"}}`))
	f.Add("github", "ping", "d3", "sha256=00", "", false, []byte(`{}`))
	f.Add("gitlab", "Push Hook", "d4", "", secret, false, []byte(push))
	f.Add("gitlab", "Pipeline Hook", "d5", "", "wrong", false, []byte(`{"project":{"path_with_namespace":"team/service"},"object_attributes":{"ref":"main","sha":"`+sha+`","status":"success"}}`))
	f.Add("generic", "", "d6", "", "", true, []byte(`{"project_id":"team/service","event":"release","ref":"main","commit":"`+sha+`"}`))
	f.Add("generic", "", "bad delivery id", "sha256=", "", true, []byte(`null`))
	f.Add("unknown", "", "", "SHA256=ABC", "", false, []byte(``))
	f.Add("github", "push", "d7", "sha256=zz", "", false, []byte(`{"ref":1}`))
	f.Fuzz(func(t *testing.T, provider, event, delivery, signature, token string, sign bool, body []byte) {
		headers := http.Header{}
		switch provider {
		case "github":
			headers.Set("X-GitHub-Event", event)
			headers.Set("X-GitHub-Delivery", delivery)
			headers.Set("X-Hub-Signature-256", signature)
		case "gitlab":
			headers.Set("X-Gitlab-Event", event)
			headers.Set("X-Gitlab-Event-UUID", delivery)
			headers.Set("X-Gitlab-Token", token)
		default:
			headers.Set("X-Gateway-Delivery", delivery)
			headers.Set("X-Gateway-Signature", signature)
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		correct := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		authentic := false
		switch provider {
		case "github":
			authentic = strings.EqualFold(signature, correct) && strings.HasPrefix(signature, "sha256=")
		case "gitlab":
			authentic = token == secret
		case "generic":
			authentic = strings.EqualFold(signature, correct) && strings.HasPrefix(signature, "sha256=")
		}
		if sign && provider == "github" {
			headers.Set("X-Hub-Signature-256", correct)
			authentic = true
		}
		if sign && provider != "github" && provider != "gitlab" {
			headers.Set("X-Gateway-Signature", correct)
			authentic = provider == "generic"
		}
		trigger, relevant, err := parseSourceSignal(provider, headers, body, repo)
		if !authentic && !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("unauthenticated event not rejected: provider=%q relevant=%v err=%v", provider, relevant, err)
		}
		if !authentic && relevant {
			t.Fatalf("unauthenticated event accepted: %+v", trigger)
		}
		if err == nil && relevant {
			if !deliveryName.MatchString(trigger.DeliveryID) || !gitSHA.MatchString(trigger.Commit) {
				t.Fatalf("accepted event with invalid identifiers: %+v", trigger)
			}
		}
		if err != nil && relevant {
			t.Fatalf("relevant event reported with error: %v", err)
		}
	})
}
