package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func openDatabase(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve sqlite path: %w", err)
	}
	path = absolute
	u := url.URL{Scheme: "file", Path: path}
	query := u.Query()
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;
CREATE TABLE IF NOT EXISTS servers (id TEXT PRIMARY KEY, definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE IF NOT EXISTS tools (id TEXT PRIMARY KEY, server_id TEXT NOT NULL REFERENCES servers(id), definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE IF NOT EXISTS settings (id INTEGER PRIMARY KEY CHECK(id=1), definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE IF NOT EXISTS model_profiles (id TEXT PRIMARY KEY, definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE IF NOT EXISTS activity (id TEXT PRIMARY KEY, time TEXT NOT NULL, kind TEXT NOT NULL, actor TEXT NOT NULL, status TEXT NOT NULL, title TEXT NOT NULL, duration_ms INTEGER NOT NULL, detail TEXT NOT NULL CHECK(json_valid(detail)));
CREATE INDEX IF NOT EXISTS activity_by_time ON activity(time DESC);
CREATE TABLE IF NOT EXISTS connection_checks (id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE IF NOT EXISTS maintenance_sources (id TEXT PRIMARY KEY, definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE IF NOT EXISTS maintenance_runs (id TEXT PRIMARY KEY, definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE IF NOT EXISTS maintenance_acceptance (id TEXT PRIMARY KEY, definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE IF NOT EXISTS maintenance_deliveries (server_id TEXT NOT NULL, provider TEXT NOT NULL, delivery_id TEXT NOT NULL, received_at TEXT NOT NULL, run_id TEXT NOT NULL, definition TEXT NOT NULL CHECK(json_valid(definition)), PRIMARY KEY(server_id,provider,delivery_id));
CREATE INDEX IF NOT EXISTS maintenance_pending_deliveries ON maintenance_deliveries(run_id,received_at);`); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("close failed database", "error", closeErr)
		}
		return nil, fmt.Errorf("initialize sqlite schema: %w", err)
	}
	if err := migrateDatabase(db); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("close failed database after migration", "error", closeErr)
		}
		return nil, fmt.Errorf("migrate sqlite schema: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("close database", "error", closeErr)
		}
		return nil, fmt.Errorf("restrict sqlite file permissions: %w", err)
	}
	return db, nil
}

func migrateDatabase(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read sqlite user_version: %w", err)
	}
	if version < 5 {
		if _, err := db.Exec("PRAGMA user_version=5"); err != nil {
			return fmt.Errorf("set sqlite user_version=5: %w", err)
		}
		version = 5
	}
	if version < 6 {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin v6 migration: %w", err)
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		// Scope grants intentionally outlive registry entries for owner review.
		if _, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS scope_state (
	id INTEGER PRIMARY KEY CHECK(id=1),
	definition TEXT NOT NULL CHECK(json_valid(definition))
);
CREATE TABLE IF NOT EXISTS repositories (
	id TEXT PRIMARY KEY,
	definition TEXT NOT NULL CHECK(json_valid(definition))
);
CREATE TABLE IF NOT EXISTS scope_rules (
	repo_key TEXT NOT NULL,
	agent_key TEXT NOT NULL,
	definition TEXT NOT NULL CHECK(json_valid(definition)),
	PRIMARY KEY(repo_key, agent_key)
);
CREATE TABLE IF NOT EXISTS scope_observations (
	fingerprint TEXT PRIMARY KEY,
	last_seen TEXT NOT NULL,
	definition TEXT NOT NULL CHECK(json_valid(definition))
);
CREATE INDEX IF NOT EXISTS scope_observations_by_last_seen ON scope_observations(last_seen DESC);
`); err != nil {
			return fmt.Errorf("create v6 scope tables: %w", err)
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO scope_state(id, definition) VALUES(1, '{"enforce":false}')`); err != nil {
			return fmt.Errorf("seed scope_state: %w", err)
		}
		defaultRule, err := json.Marshal(map[string]any{"repo_key": "*", "agent_key": "*", "grants": []any{}, "updated_at": time.Now().UTC().Format(time.RFC3339)})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO scope_rules(repo_key, agent_key, definition) VALUES('*', '*', ?)`, string(defaultRule)); err != nil {
			return fmt.Errorf("seed default scope rule: %w", err)
		}
		if _, err := tx.Exec("PRAGMA user_version=6"); err != nil {
			return fmt.Errorf("set sqlite user_version=6: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit v6 migration: %w", err)
		}
		committed = true
		version = 6
	}
	if version < 7 {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin v7 migration: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		for _, query := range []string{
			"ALTER TABLE servers ADD COLUMN position INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE tools ADD COLUMN position INTEGER NOT NULL DEFAULT 0",
			"UPDATE servers SET position=rowid",
			"UPDATE tools SET position=rowid",
			"PRAGMA user_version=7",
		} {
			if _, err := tx.Exec(query); err != nil {
				return fmt.Errorf("migrate registry ordering: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit v7 migration: %w", err)
		}
	}
	return nil
}
