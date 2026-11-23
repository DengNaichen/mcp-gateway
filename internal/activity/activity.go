package activity

import (
	"encoding/json"
	"log/slog"
	"slices"
	"sync"
	"time"

	"gatewaylab/internal/store"

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
	RepoID     string          `json:"repo_id,omitempty"`
	ServiceID  string          `json:"service_id,omitempty"`
	Protocol   string          `json:"protocol,omitempty"`
	Action     string          `json:"action,omitempty"`
	Decision   string          `json:"decision,omitempty"`
	RuleID     string          `json:"rule_id,omitempty"`
	ApprovalID string          `json:"approval_id,omitempty"`
	SkillID    string          `json:"skill_id,omitempty"`
}

type Repository interface {
	Recent(int) ([]store.ActivityRow, error)
	Find(store.ActivityFilter) ([]store.ActivityRow, error)
	Insert(store.ActivityRow) error
	LatestAgent() (*store.ActivityRow, error)
}

const (
	recentEventLimit   = 100
	pulseLoadLimit     = 2000
	PulseBucketCount   = 48
	PulseBucketMinutes = 15
	pulseWindow        = time.Duration(PulseBucketCount*PulseBucketMinutes) * time.Minute
)

// PulseBucket aggregates gateway activity over a 15-minute window.
type PulseBucket struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Total int       `json:"total"`
	Error int       `json:"error"`
}

type pulseEntry struct {
	time   time.Time
	status string
}

type Service struct {
	mu      sync.RWMutex
	events  []Event
	history []pulseEntry
	repo    Repository
	redact  func(json.RawMessage) (json.RawMessage, error)
}

func eventFromRow(row store.ActivityRow) Event {
	ev := Event(row)
	ev.Detail = slices.Clone(ev.Detail)
	return ev
}

func New(repo Repository, redact func(json.RawMessage) (json.RawMessage, error)) (*Service, error) {
	rows, err := repo.Recent(pulseLoadLimit)
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().Add(-pulseWindow - time.Duration(PulseBucketMinutes)*time.Minute)
	s := &Service{repo: repo, redact: redact, events: make([]Event, 0, min(len(rows), recentEventLimit))}
	for i, row := range rows {
		if i < recentEventLimit {
			s.events = append(s.events, eventFromRow(row))
		}
		if !row.Time.Before(cutoff) {
			s.history = append(s.history, pulseEntry{time: row.Time, status: row.Status})
		}
	}
	return s, nil
}
func (s *Service) LatestAgent() (*Event, error) {
	row, err := s.repo.LatestAgent()
	if err != nil || row == nil {
		return nil, err
	}
	ev := eventFromRow(*row)
	return &ev, nil
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

// Pulse returns 48 15-minute activity buckets covering the 12 hours ending at
// the end of the 15-minute slot containing now.
func (s *Service) Pulse(now time.Time) []PulseBucket {
	slotDur := time.Duration(PulseBucketMinutes) * time.Minute
	end := now.Truncate(slotDur).Add(slotDur)
	start := end.Add(-pulseWindow)

	buckets := make([]PulseBucket, PulseBucketCount)
	for i := range buckets {
		bStart := start.Add(time.Duration(i) * slotDur)
		buckets[i] = PulseBucket{
			Start: bStart,
			End:   bStart.Add(slotDur),
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.history {
		if entry.time.Before(start) || !entry.time.Before(end) {
			continue
		}
		idx := int(entry.time.Sub(start) / slotDur)
		if idx < 0 || idx >= PulseBucketCount {
			continue
		}
		buckets[idx].Total++
		if entry.status == "error" || entry.status == "failed" {
			buckets[idx].Error++
		}
	}
	return buckets
}

func (s *Service) Record(kind, actor, status, title string, started time.Time, detail any) {
	s.RecordAudit(kind, actor, status, title, started, AuditMeta{}, detail)
}

// AuditMeta carries the request and decision associated with an activity row.
type AuditMeta struct {
	RepoID, ServiceID, Protocol, Action, Decision, RuleID, ApprovalID, SkillID string
}

func (s *Service) RecordAudit(kind, actor, status, title string, started time.Time, meta AuditMeta, detail any) {
	b, err := json.Marshal(detail)
	if err != nil {
		slog.Error("encode activity", "error", err)
		return
	}
	if s.redact != nil {
		b, err = s.redact(b)
		if err != nil {
			slog.Error("redact activity detail", "error", err)
			b = []byte("null")
		}
	}

	if actor == "" {
		actor = "developer"
	}
	now := time.Now().UTC()
	e := Event{ID: uuid.NewString(), Time: now, Kind: kind, Actor: actor, Status: status, Title: title, DurationMS: time.Since(started).Milliseconds(), Detail: b,
		RepoID: meta.RepoID, ServiceID: meta.ServiceID, Protocol: meta.Protocol,
		Action: meta.Action, Decision: meta.Decision, RuleID: meta.RuleID, ApprovalID: meta.ApprovalID, SkillID: meta.SkillID,
	}
	cutoff := now.Add(-pulseWindow - time.Duration(PulseBucketMinutes)*time.Minute)
	s.mu.Lock()
	s.events = append([]Event{e}, s.events...)
	if len(s.events) > recentEventLimit {
		s.events = s.events[:recentEventLimit]
	}
	s.history = append([]pulseEntry{{time: now, status: status}}, s.history...)
	kept := s.history[:0]
	for _, item := range s.history {
		if !item.time.Before(cutoff) {
			kept = append(kept, item)
		}
	}
	s.history = kept
	s.mu.Unlock()
	if err := s.repo.Insert(store.ActivityRow(e)); err != nil {
		slog.Error("persist activity log", "error", err)
	}
}
