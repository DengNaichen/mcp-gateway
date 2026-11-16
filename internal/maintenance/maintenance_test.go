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
