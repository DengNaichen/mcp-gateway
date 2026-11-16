package activity

import (
	"encoding/json"
	"gatewaylab/internal/store"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Event struct {
	ID         string          `json:"id"`
	Time       time.Time       `json:"time"`
	Kind       string          `json:"kind"`
	Actor      string          `json:"actor"`
	Status     string          `json:"status"`
	Title      string          `json:"title"`
	DurationMS int64           `json:"duration_ms"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

type Repository interface {
	Recent(int) ([]store.ActivityRow, error)
	Insert(store.ActivityRow) error
}
type Service struct {
	mu     sync.RWMutex
	events []Event
	repo   Repository
}

func New(repo Repository) (*Service, error) {
	rows, err := repo.Recent(100)
	if err != nil {
		return nil, err
	}
	s := &Service{repo: repo, events: []Event{}}
	for _, row := range rows {
		s.events = append(s.events, Event(row))
	}
	return s, nil
}
func (s *Service) Recent() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	for i := range out {
		out[i].Detail = slices.Clone(out[i].Detail)
	}
	return out
}
func (s *Service) Record(kind, actor, status, title string, started time.Time, detail any) {
	b, err := json.Marshal(detail)
	if err != nil {
		slog.Error("encode activity", "error", err)
		return
	}
	if actor == "" {
		actor = "developer"
	}
	e := Event{ID: uuid.NewString(), Time: time.Now(), Kind: kind, Actor: actor, Status: status, Title: title, DurationMS: time.Since(started).Milliseconds(), Detail: b}
	s.mu.Lock()
	s.events = append([]Event{e}, s.events...)
	if len(s.events) > 100 {
		s.events = s.events[:100]
	}
	s.mu.Unlock()
	if err := s.repo.Insert(store.ActivityRow(e)); err != nil {
		slog.Error("persist activity log", "error", err)
	}
}
