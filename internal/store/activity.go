package store

import (
	"encoding/json"
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
}
type ActivityRepository struct{ db DBTX }

func Activity(db DBTX) ActivityRepository { return ActivityRepository{db} }
func (r ActivityRepository) Insert(e ActivityRow) error {
	_, err := r.db.Exec("INSERT INTO activity(id,time,kind,actor,status,title,duration_ms,detail) VALUES(?,?,?,?,?,?,?,?)", e.ID, e.Time.Format(time.RFC3339Nano), e.Kind, e.Actor, e.Status, e.Title, e.DurationMS, string(e.Detail))
	return err
}
func (r ActivityRepository) Recent(limit int) ([]ActivityRow, error) {
	return r.query(readLabels{query: "load activity", scan: "read activity", decode: "parse activity time", iterate: "iterate activity"}, "SELECT id,time,kind,actor,status,title,duration_ms,detail FROM activity ORDER BY time DESC LIMIT ?", limit)
}
func (r ActivityRepository) LatestAgent() (*ActivityRow, error) {
	events, err := r.query(readLabels{query: "read agent connection activity", scan: "read agent connection activity", decode: "parse agent connection time", iterate: "read agent connection activity"}, "SELECT id,time,kind,actor,status,title,duration_ms,detail FROM activity WHERE kind='agent' ORDER BY time DESC LIMIT 1")
	if err != nil || len(events) == 0 {
		return nil, err
	}
	return &events[0], nil
}
func (r ActivityRepository) query(labels readLabels, query string, args ...any) ([]ActivityRow, error) {
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, labelError(labels.query, err)
	}
	defer func() { _ = rows.Close() }()
	out := []ActivityRow{}
	for rows.Next() {
		var e ActivityRow
		var stamp, detail string
		if err := rows.Scan(&e.ID, &stamp, &e.Kind, &e.Actor, &e.Status, &e.Title, &e.DurationMS, &detail); err != nil {
			return nil, labelError(labels.scan, err)
		}
		e.Time, err = time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return nil, labelError(labels.decode, err)
		}
		e.Detail = json.RawMessage(detail)
		out = append(out, e)
	}
	return out, labelError(labels.iterate, rows.Err())
}
