package store

import (
	"encoding/json"
	"strings"
	"time"
)

// ActivityRow is the persisted representation; recording and retention policy
// belong to the activity domain.
type ActivityRow struct {
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

type ActivityFilter struct {
	Actor, RepoID, ServiceID, Protocol, Decision, Status, Query string
	Before                                                      *time.Time
	BeforeID                                                    string
	Limit                                                       int
}

type ActivityRepository struct{ db DBTX }

func Activity(db DBTX) ActivityRepository { return ActivityRepository{db} }

const activityColumns = "id,time,kind,actor,status,title,duration_ms,detail,repo_id,service_id,protocol,action,decision,rule_id,approval_id,skill_id"

func activityTimestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

func (r ActivityRepository) Insert(e ActivityRow) error {
	_, err := r.db.Exec("INSERT INTO activity("+activityColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		e.ID, activityTimestamp(e.Time), e.Kind, e.Actor, e.Status, e.Title, e.DurationMS, string(e.Detail),
		e.RepoID, e.ServiceID, e.Protocol, e.Action, e.Decision, e.RuleID, e.ApprovalID, e.SkillID)
	return err
}

func (r ActivityRepository) Recent(limit int) ([]ActivityRow, error) {
	return r.Find(ActivityFilter{Limit: limit})
}

func (r ActivityRepository) LatestAgent() (*ActivityRow, error) {
	events, err := r.query("SELECT " + activityColumns + " FROM activity WHERE kind='agent' ORDER BY time DESC,id DESC LIMIT 1")
	if err != nil || len(events) == 0 {
		return nil, err
	}
	return &events[0], nil
}

func (r ActivityRepository) Find(f ActivityFilter) ([]ActivityRow, error) {
	var clauses []string
	var args []any
	for _, field := range []struct{ column, value string }{
		{"actor", f.Actor}, {"repo_id", f.RepoID}, {"service_id", f.ServiceID},
		{"protocol", f.Protocol}, {"decision", f.Decision}, {"status", f.Status},
	} {
		if field.value != "" {
			clauses = append(clauses, field.column+" = ?")
			args = append(args, field.value)
		}
	}
	if f.Query != "" {
		clauses = append(clauses, "(instr(lower(title),lower(?)) > 0 OR instr(lower(action),lower(?)) > 0 OR instr(lower(service_id),lower(?)) > 0)")
		args = append(args, f.Query, f.Query, f.Query)
	}
	if f.Before != nil {
		stamp := activityTimestamp(*f.Before)
		if f.BeforeID != "" {
			clauses = append(clauses, "(time,id) < (?,?)")
			args = append(args, stamp, f.BeforeID)
		} else {
			clauses = append(clauses, "time < ?")
			args = append(args, stamp)
		}
	}

	query := "SELECT " + activityColumns + " FROM activity"
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	return r.query(query+" ORDER BY time DESC,id DESC LIMIT ?", append(args, f.Limit)...)
}

func (r ActivityRepository) query(query string, args ...any) ([]ActivityRow, error) {
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, labelError("load activity", err)
	}
	defer func() { _ = rows.Close() }()
	out := []ActivityRow{}
	for rows.Next() {
		var e ActivityRow
		var stamp, detail string
		if err := rows.Scan(&e.ID, &stamp, &e.Kind, &e.Actor, &e.Status, &e.Title, &e.DurationMS, &detail,
			&e.RepoID, &e.ServiceID, &e.Protocol, &e.Action, &e.Decision, &e.RuleID, &e.ApprovalID, &e.SkillID); err != nil {
			return nil, labelError("read activity", err)
		}
		e.Time, err = time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return nil, labelError("parse activity time", err)
		}
		e.Detail = json.RawMessage(detail)
		out = append(out, e)
	}
	return out, labelError("iterate activity", rows.Err())
}
