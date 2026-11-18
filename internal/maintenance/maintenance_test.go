package maintenance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMaintenanceRecoveryAndSchedule(t *testing.T) {
	m := testMaintainer(t)
	r := enqueueTest(t, m, false)
	m.mu.Lock()
	r.Status = "running"
	if err := m.saveRun(r); err != nil {
		t.Fatal(err)
	}
	m.mu.Unlock()
	reloaded, err := New(testOptions(m))
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if reloaded.runs[r.ID].Status != "interrupted" {
		t.Fatal("running task was not interrupted on restart")
	}
	m.mu.Lock()
	m.runs[r.ID] = reloaded.runs[r.ID]
	s := m.sources["service"]
	s.Enabled = true
	s.NextRunAt = time.Now().Add(-time.Minute)
	if err := m.saveSource(s); err != nil {
		t.Fatal(err)
	}
	m.mu.Unlock()
	m.Start(context.Background())
	deadline := time.Now().Add(12 * time.Second)
	found := ""
	for time.Now().Before(deadline) {
		m.mu.Lock()
		for id, run := range m.runs {
			if id != r.ID && run.ServerID == "service" {
				found = id
			}
		}
		m.mu.Unlock()
		if found != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if found == "" {
		t.Fatal("due schedule did not create task")
	}
	waitRun(t, m, found, "ready")
}
func TestPiErrorAndTimeout(t *testing.T) {
	for _, mode := range []string{"error", "crash", "hang"} {
		t.Run(mode, func(t *testing.T) {
			m := testMaintainer(t)
			reference := m.sources["service"].Files[0]
			putSource(t, reference, "v1", mode)
			r := enqueueTest(t, m, true)
			r.Status = "running"
			m.mu.Lock()
			if err := m.saveRun(r); err != nil {
				t.Fatal(err)
			}
			m.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			err := m.build(ctx, &r)
			if err == nil {
				t.Fatal("worker failure was accepted")
			}
			if mode == "hang" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("wrong timeout error: %v", err)
			}
		})
	}
}
func TestArtifactTraversalAndSymlink(t *testing.T) {
	dir := t.TempDir()
	if _, err := regularArtifact(dir, "../secret"); err == nil {
		t.Fatal("traversal accepted")
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "adapter.mjs")); err != nil {
		t.Fatal(err)
	}
	if _, err := regularArtifact(dir, "adapter.mjs"); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestValidationAndAcceptanceCharacterization(t *testing.T) {
	t.Run("validateRepository rules", func(t *testing.T) {
		valid := RepositorySource{
			URL:              "https://github.com/org/repo.git",
			Ref:              "main",
			Files:            []string{"openapi.yaml"},
			EventsEnabled:    true,
			Project:          "org/repo",
			WebhookSecretEnv: "WEBHOOK_SECRET",
		}
		if err := validateRepository(valid); err != nil {
			t.Fatalf("valid repo rejected: %v", err)
		}
		invalidCases := []RepositorySource{
			{URL: "ftp://example.com/repo.git", Ref: "main", Files: []string{"a.yaml"}},
			{URL: "https://user:pass@github.com/org/repo.git", Ref: "main", Files: []string{"a.yaml"}},
			{URL: "https://github.com/org/repo.git?x=1", Ref: "main", Files: []string{"a.yaml"}},
			{URL: "https://github.com/org/repo.git", Ref: "main..evil", Files: []string{"a.yaml"}},
			{URL: "https://github.com/org/repo.git", Ref: "main", Files: []string{"../secret"}},
			{URL: "https://github.com/org/repo.git", Ref: "main", Files: []string{"a.yaml"}, EventsEnabled: true, Project: ""},
		}
		for i, tc := range invalidCases {
			if err := validateRepository(tc); err == nil {
				t.Errorf("case %d: expected error, got nil", i)
			}
		}
	})

	t.Run("SaveSource validation", func(t *testing.T) {
		m := testMaintainer(t)
		ref := m.sources["service"].Files[0]
		cases := []MaintenanceSource{
			{ServerID: "service", Format: "yaml", Files: []string{ref}, IntervalMinutes: 10, TimeoutSeconds: 60},
			{ServerID: "service", Format: "pi", Files: []string{ref}, IntervalMinutes: 0, TimeoutSeconds: 60},
			{ServerID: "service", Format: "pi", Files: []string{ref}, IntervalMinutes: 10, TimeoutSeconds: 10},
			{ServerID: "service", Format: "pi", Files: []string{ref}, IntervalMinutes: 10, TimeoutSeconds: 60, Provider: "openai"},
			{ServerID: "service", Format: "pi", Files: []string{"relative.json"}, IntervalMinutes: 10, TimeoutSeconds: 60},
			{ServerID: "service", Format: "native", Files: []string{ref}, IntervalMinutes: 10, TimeoutSeconds: 60},
			{ServerID: "missing", Format: "pi", Files: []string{ref}, IntervalMinutes: 10, TimeoutSeconds: 60},
		}
		for i, tc := range cases {
			if err := m.SaveSource(tc); err == nil {
				t.Errorf("case %d: expected SaveSource error, got nil", i)
			}
		}
	})

	t.Run("validateBundle and EvaluateAcceptance", func(t *testing.T) {
		m := testMaintainer(t)
		rev := t.TempDir()
		manifest := map[string]any{
			"summary": "ok",
			"tools": []map[string]any{
				{
					"id":           "service.logs",
					"server_id":    "service",
					"name":         "logs",
					"description":  "Search logs",
					"input_schema": objectSchema(map[string]any{}),
					"method":       "GET",
					"path":         "/logs",
					"example":      map[string]any{},
				},
			},
		}
		if err := os.WriteFile(filepath.Join(rev, "manifest.json"), mustJSON(t, manifest), 0600); err != nil {
			t.Fatal(err)
		}
		r := enqueueTest(t, m, true)
		bundle, err := m.validateBundle(context.Background(), r, rev)
		if err != nil || len(bundle.Tools) != 1 {
			t.Fatalf("validateBundle failed: %v", err)
		}

		// Bad manifest cases
		badDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(badDir, "manifest.json"), []byte(`{"summary":"","tools":[]}`), 0600); err == nil {
			if _, err := m.validateBundle(context.Background(), r, badDir); err == nil {
				t.Fatal("empty summary accepted")
			}
		}
		if err := os.WriteFile(filepath.Join(badDir, "manifest.json"), mustJSON(t, map[string]any{
			"summary": "ok",
			"tools": []map[string]any{{
				"id":           "service.bad",
				"server_id":    "service",
				"name":         "bad",
				"description":  "bad",
				"input_schema": objectSchema(map[string]any{}),
				"method":       "GET",
				"path":         "/../evil",
			}},
		}), 0600); err == nil {
			if _, err := m.validateBundle(context.Background(), r, badDir); err == nil {
				t.Fatal("path traversal accepted")
			}
		}

		// EvaluateAcceptance missing coverage vs passing
		r.Tools = bundle.Tools
		suite := AcceptanceSuite{
			ServerID:  "service",
			Contracts: []ContractCase{{Name: "c1", ToolID: "service.logs", Arguments: map[string]any{}, ExpectError: true}},
			Routing:   []RoutingCase{{Name: "r1", Intent: "logs", Expected: []string{"service.logs"}}},
		}
		if err := m.SaveAcceptance(suite); err != nil {
			t.Fatal(err)
		}
		r.AcceptanceInput = &suite
		report := m.EvaluateAcceptance(context.Background(), r, rev)
		if report.Passed || len(report.MissingCoverage) == 0 {
			t.Fatalf("expected missing contract coverage for expect_error-only case, got %+v", report)
		}
	})
}
