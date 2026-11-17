package activity

import (
	"testing"
	"time"

	"gatewaylab/internal/store"

	"github.com/google/uuid"
)

type blockedRepository struct {
	entered chan struct{}
	release chan struct{}
}

func (r *blockedRepository) Recent(int) ([]store.ActivityRow, error) { return nil, nil }
func (r *blockedRepository) LatestAgent() (*store.ActivityRow, error) {
	return nil, nil
}
func (r *blockedRepository) Insert(store.ActivityRow) error {
	close(r.entered)
	<-r.release
	return nil
}

func TestRecordPublishesWithoutHoldingLockDuringDatabaseWrite(t *testing.T) {
	repo := &blockedRepository{make(chan struct{}), make(chan struct{})}
	s, err := New(repo)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		s.Record("invoke", "", "ok", "hello", time.Now(), map[string]string{"value": "original"})
		close(done)
	}()
	<-repo.entered
	defer func() { close(repo.release); <-done }()
	recent := make(chan []Event, 1)
	go func() { recent <- s.Recent() }()
	select {
	case events := <-recent:
		if len(events) != 1 || events[0].Actor != "developer" {
			t.Fatalf("cache: %+v", events)
		}
		if _, err := uuid.Parse(events[0].ID); err != nil {
			t.Fatalf("ID is not UUID: %v", err)
		}
		events[0].Detail[0] = '!'
		if s.Recent()[0].Detail[0] == '!' {
			t.Fatal("cache exposes mutable event detail")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("activity read blocked on database write")
	}
}

func TestRecordRetentionAndReload(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := New(store.Activity(db.DB))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 105; i++ {
		s.Record("test", "actor", "ok", "event", time.Now(), i)
	}
	events := s.Recent()
	if len(events) != 100 {
		t.Fatalf("retention: %d", len(events))
	}
	seen := map[string]bool{}
	for _, e := range events {
		if seen[e.ID] {
			t.Fatal("duplicate ID")
		}
		seen[e.ID] = true
	}
	reloaded, err := New(store.Activity(db.DB))
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Recent(); len(got) != 100 || got[0].ID != events[0].ID {
		t.Fatal("persisted activity differs")
	}
	pulse := reloaded.Pulse(time.Now())
	if len(pulse) != PulseBucketCount {
		t.Fatalf("pulse buckets = %d, want %d", len(pulse), PulseBucketCount)
	}
	totalPulse := 0
	for _, b := range pulse {
		totalPulse += b.Total
	}
	if totalPulse != 105 {
		t.Fatalf("pulse total = %d, want 105 (beyond recent 100 cap)", totalPulse)
	}
	if latest, err := reloaded.LatestAgent(); err != nil || latest != nil {
		t.Fatalf("expected nil latest agent, got %+v %v", latest, err)
	}
	reloaded.Record("agent", "codex", "ok", "discover", time.Now(), map[string]string{"intent": "search"})
	reloaded.Record("invoke", "codex", "ok", "call", time.Now(), nil)
	latest, err := reloaded.LatestAgent()
	if err != nil || latest == nil || latest.Kind != "agent" || latest.Title != "discover" {
		t.Fatalf("latest agent mismatch: %+v %v", latest, err)
	}
}
