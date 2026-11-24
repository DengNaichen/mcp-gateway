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
	if err := os.Chmod(path, 0600); err != nil { //nolint:gosec // G703: path is the user's own local data/socket path from configuration, not remote input
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
		if err := RunTx(db, func(tx DBTX) error {
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
			return nil
		}); err != nil {
			return fmt.Errorf("migrate v6: %w", err)
		}
		version = 6
	}
	if version < 7 {
		if err := RunTx(db, func(tx DBTX) error {
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
			return nil
		}); err != nil {
			return fmt.Errorf("migrate v7: %w", err)
		}
		version = 7
	}
	if version < 8 {
		if err := RunTx(db, func(tx DBTX) error {
			if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS scope_rules_v8 (
	repo_key TEXT PRIMARY KEY,
	definition TEXT NOT NULL CHECK(json_valid(definition))
);`); err != nil {
				return fmt.Errorf("create v8 scope_rules table: %w", err)
			}
			rows, err := tx.Query("SELECT repo_key, agent_key, definition FROM scope_rules")
			if err != nil {
				return fmt.Errorf("read legacy scope_rules: %w", err)
			}
			type legacyRow struct {
				repoKey, agentKey, definition string
			}
			var legacy []legacyRow
			for rows.Next() {
				var row legacyRow
				if err := rows.Scan(&row.repoKey, &row.agentKey, &row.definition); err != nil {
					_ = rows.Close()
					return fmt.Errorf("scan legacy scope_rule: %w", err)
				}
				legacy = append(legacy, row)
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return fmt.Errorf("iterate legacy scope_rules: %w", err)
			}
			_ = rows.Close()

			for _, row := range legacy {
				if row.agentKey != "*" {
					continue
				}
				var def map[string]any
				if err := json.Unmarshal([]byte(row.definition), &def); err != nil {
					return fmt.Errorf("decode legacy scope_rule %s: %w", row.repoKey, err)
				}
				if ak, ok := def["agent_key"].(string); ok && ak != "" && ak != "*" {
					continue
				}
				delete(def, "agent_key")
				cleaned, err := json.Marshal(def)
				if err != nil {
					return fmt.Errorf("encode migrated scope_rule %s: %w", row.repoKey, err)
				}
				if _, err := tx.Exec(`INSERT INTO scope_rules_v8(repo_key, definition) VALUES(?, ?) ON CONFLICT(repo_key) DO UPDATE SET definition=excluded.definition`, row.repoKey, string(cleaned)); err != nil {
					return fmt.Errorf("insert migrated scope_rule %s: %w", row.repoKey, err)
				}
			}
			defaultRule, err := json.Marshal(map[string]any{"repo_key": "*", "grants": []any{}, "updated_at": time.Now().UTC().Format(time.RFC3339)})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO scope_rules_v8(repo_key, definition) VALUES('*', ?)`, string(defaultRule)); err != nil {
				return fmt.Errorf("seed default v8 scope rule: %w", err)
			}
			if _, err := tx.Exec(`DROP TABLE scope_rules; ALTER TABLE scope_rules_v8 RENAME TO scope_rules; PRAGMA user_version=8`); err != nil {
				return fmt.Errorf("finalize v8 scope_rules migration: %w", err)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("migrate v8: %w", err)
		}
		version = 8
	}
	if version < 9 {
		if err := RunTx(db, migrateActivityV9); err != nil {
			return fmt.Errorf("migrate v9: %w", err)
		}
	}
	if version < 10 {
		if err := RunTx(db, migrateApprovalsV10); err != nil {
			return fmt.Errorf("migrate v10: %w", err)
		}
	}
	if version < 11 {
		if err := RunTx(db, func(tx DBTX) error {
			_, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS skills (
	id TEXT PRIMARY KEY,
	definition TEXT NOT NULL CHECK(json_valid(definition))
);
PRAGMA user_version=11;
`)
			return err
		}); err != nil {
			return fmt.Errorf("migrate v11: %w", err)
		}
	}
	return nil
}
