package store

import (
	"fmt"
	"time"
)

func migrateActivityV9(tx DBTX) error {
	if _, err := tx.Exec(`
CREATE TABLE policy_rules (id TEXT PRIMARY KEY, definition TEXT NOT NULL CHECK(json_valid(definition)));
ALTER TABLE activity ADD COLUMN repo_id TEXT NOT NULL DEFAULT '';
ALTER TABLE activity ADD COLUMN service_id TEXT NOT NULL DEFAULT '';
ALTER TABLE activity ADD COLUMN protocol TEXT NOT NULL DEFAULT '';
ALTER TABLE activity ADD COLUMN action TEXT NOT NULL DEFAULT '';
ALTER TABLE activity ADD COLUMN decision TEXT NOT NULL DEFAULT '';
ALTER TABLE activity ADD COLUMN rule_id TEXT NOT NULL DEFAULT '';
ALTER TABLE activity ADD COLUMN approval_id TEXT NOT NULL DEFAULT '';
ALTER TABLE activity ADD COLUMN skill_id TEXT NOT NULL DEFAULT '';
`); err != nil {
		return err
	}
	// Legacy times use variable fractional precision and local offsets. A
	// fixed-width UTC representation makes indexed text order chronological.
	var lastRow int64
	for {
		batch, err := activityTimeBatch(tx, lastRow)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, row := range batch {
			stamp, err := time.Parse(time.RFC3339Nano, row.stamp)
			if err != nil {
				return fmt.Errorf("parse activity time at row %d: %w", row.id, err)
			}
			if _, err := tx.Exec("UPDATE activity SET time=? WHERE rowid=?", activityTimestamp(stamp), row.id); err != nil {
				return err
			}
			lastRow = row.id
		}
	}
	_, err := tx.Exec(`
CREATE INDEX activity_by_repo_time ON activity(repo_id, time DESC);
CREATE INDEX activity_by_service_time ON activity(service_id, time DESC);
CREATE INDEX activity_by_decision_time ON activity(decision, time DESC);
CREATE INDEX activity_by_protocol_time ON activity(protocol, time DESC);
CREATE INDEX activity_by_time_id ON activity(time DESC, id DESC);
DROP INDEX IF EXISTS activity_by_time;
PRAGMA user_version=9;
`)
	return err
}

type activityTimeRow struct {
	id    int64
	stamp string
}

func activityTimeBatch(tx DBTX, after int64) ([]activityTimeRow, error) {
	rows, err := tx.Query("SELECT rowid,time FROM activity WHERE rowid>? ORDER BY rowid LIMIT 500", after)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var batch []activityTimeRow
	for rows.Next() {
		var row activityTimeRow
		if err := rows.Scan(&row.id, &row.stamp); err != nil {
			return nil, err
		}
		batch = append(batch, row)
	}
	return batch, rows.Err()
}
