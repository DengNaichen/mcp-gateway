package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
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
CREATE TABLE IF NOT EXISTS activity (id TEXT PRIMARY KEY, time TEXT NOT NULL, kind TEXT NOT NULL, actor TEXT NOT NULL, status TEXT NOT NULL, title TEXT NOT NULL, duration_ms INTEGER NOT NULL, detail TEXT NOT NULL CHECK(json_valid(detail)));
CREATE INDEX IF NOT EXISTS activity_by_time ON activity(time DESC);
CREATE TABLE IF NOT EXISTS connection_checks (id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, definition TEXT NOT NULL CHECK(json_valid(definition)));
PRAGMA user_version=2;`); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("close failed database", "error", closeErr)
		}
		return nil, fmt.Errorf("initialize sqlite schema: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("close database", "error", closeErr)
		}
		return nil, fmt.Errorf("restrict sqlite file permissions: %w", err)
	}
	return db, nil
}

func (g *Gateway) Close() error {
	result := g.closeDownstreams()
	if err := g.db.Close(); err != nil {
		result = errors.Join(result, fmt.Errorf("close sqlite: %w", err))
	}
	return result
}

func (g *Gateway) loadRegistry() error {
	g.registry = Registry{Servers: []Server{}, Tools: []Tool{}}
	for _, table := range []string{"servers", "tools"} {
		rows, err := g.db.Query("SELECT definition FROM " + table + " ORDER BY rowid")
		if err != nil {
			return fmt.Errorf("query registry table %s: %w", table, err)
		}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				closeRows(rows)
				return fmt.Errorf("read registry definition: %w", err)
			}
			if table == "servers" {
				var s Server
				if err := json.Unmarshal([]byte(raw), &s); err != nil {
					closeRows(rows)
					return fmt.Errorf("decode server definition: %w", err)
				}
				g.registry.Servers = append(g.registry.Servers, s)
			} else {
				var t Tool
				if err := json.Unmarshal([]byte(raw), &t); err != nil {
					closeRows(rows)
					return fmt.Errorf("decode tool definition: %w", err)
				}
				g.registry.Tools = append(g.registry.Tools, t)
			}
		}
		if err := rows.Err(); err != nil {
			closeRows(rows)
			return fmt.Errorf("iterate registry: %w", err)
		}
		closeRows(rows)
	}
	return nil
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		slog.Debug("close sqlite rows", "error", err)
	}
}

func (g *Gateway) persistRegistry(reg Registry) error {
	tx, err := g.db.Begin()
	if err != nil {
		return fmt.Errorf("begin registry transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if err := tx.Rollback(); err != nil {
				slog.Warn("rollback registry transaction", "error", err)
			}
		}
	}()
	for _, query := range []string{"DELETE FROM tools", "DELETE FROM servers"} {
		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("replace registry: %w", err)
		}
	}
	for _, s := range reg.Servers {
		b, err := json.Marshal(s)
		if err != nil {
			return fmt.Errorf("encode server: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO servers(id,definition) VALUES(?,?)", s.ID, string(b)); err != nil {
			return fmt.Errorf("persist server: %w", err)
		}
	}
	for _, t := range reg.Tools {
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("encode tool: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO tools(id,server_id,definition) VALUES(?,?,?)", t.ID, t.ServerID, string(b)); err != nil {
			return fmt.Errorf("persist tool: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit registry: %w", err)
	}
	committed = true
	return nil
}

func (g *Gateway) persistSettings(s Settings) error {
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode model settings: %w", err)
	}
	if _, err := g.db.Exec("INSERT INTO settings(id,definition) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET definition=excluded.definition", string(b)); err != nil {
		return fmt.Errorf("persist model settings: %w", err)
	}
	return nil
}

func (g *Gateway) loadEvents() error {
	rows, err := g.db.Query("SELECT id,time,kind,actor,status,title,duration_ms,detail FROM activity ORDER BY time DESC LIMIT 100")
	if err != nil {
		return fmt.Errorf("load activity: %w", err)
	}
	defer closeRows(rows)
	for rows.Next() {
		var e Event
		var timestamp, detail string
		if err := rows.Scan(&e.ID, &timestamp, &e.Kind, &e.Actor, &e.Status, &e.Title, &e.DurationMS, &detail); err != nil {
			return fmt.Errorf("read activity: %w", err)
		}
		t, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			return fmt.Errorf("parse activity time: %w", err)
		}
		e.Time = t
		e.Detail = json.RawMessage(detail)
		g.events = append(g.events, e)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate activity: %w", err)
	}
	return nil
}
