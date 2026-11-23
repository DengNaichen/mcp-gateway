package activity

import (
	"errors"
	"strings"
	"time"

	"gatewaylab/internal/store"
)

var ErrInvalidQuery = errors.New("invalid activity query")

type Filter struct {
	Actor, RepoID, ServiceID, Protocol, Decision, Status, Query, Before, Action, SkillID string
	Limit                                                                                int
}

type EventPage struct {
	Items      []Event
	NextCursor string
	HasMore    bool
}

func (s *Service) Query(f Filter) (EventPage, error) {
	limit := f.Limit
	if limit < 0 {
		return EventPage{}, ErrInvalidQuery
	}
	if limit == 0 {
		limit = recentEventLimit
	}
	limit = min(limit, 500)
	filter := store.ActivityFilter{
		Actor: f.Actor, RepoID: f.RepoID, ServiceID: f.ServiceID,
		Protocol: f.Protocol, Decision: f.Decision, Status: f.Status, Query: f.Query,
		Action: f.Action, SkillID: f.SkillID, Limit: limit + 1,
	}
	if f.Before != "" {
		stamp, id, compound := strings.Cut(f.Before, "|")
		parsed, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil || compound && (id == "" || strings.Contains(id, "|")) {
			return EventPage{}, ErrInvalidQuery
		}
		filter.Before, filter.BeforeID = &parsed, id
	}
	rows, err := s.repo.Find(filter)
	if err != nil {
		return EventPage{}, err
	}
	page := EventPage{Items: make([]Event, 0, min(len(rows), limit)), HasMore: len(rows) > limit}
	for _, row := range rows[:min(len(rows), limit)] {
		page.Items = append(page.Items, eventFromRow(row))
	}
	if page.HasMore {
		last := page.Items[len(page.Items)-1]
		page.NextCursor = last.Time.Format(time.RFC3339Nano) + "|" + last.ID
	}
	return page, nil
}
