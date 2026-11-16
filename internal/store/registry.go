package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"gatewaylab/internal/registry"
)

type RegistryRepository struct{ db *sql.DB }

func Registry(db *sql.DB) RegistryRepository { return RegistryRepository{db} }
func (r RegistryRepository) Load() (registry.Registry, error) {
	result := registry.Registry{Servers: []registry.Server{}, Tools: []registry.Tool{}}
	for _, table := range []string{"servers", "tools"} {
		rows, err := r.db.Query("SELECT definition FROM " + table + " ORDER BY position,rowid")
		if err != nil {
			return result, fmt.Errorf("query registry table %s: %w", table, err)
		}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				_ = rows.Close()
				return result, fmt.Errorf("read registry definition: %w", err)
			}
			if table == "servers" {
				var s registry.Server
				if err := json.Unmarshal([]byte(raw), &s); err != nil {
					_ = rows.Close()
					return result, fmt.Errorf("decode server definition: %w", err)
				}
				result.Servers = append(result.Servers, s)
			} else {
				var t registry.Tool
				if err := json.Unmarshal([]byte(raw), &t); err != nil {
					_ = rows.Close()
					return result, fmt.Errorf("decode tool definition: %w", err)
				}
				result.Tools = append(result.Tools, t)
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return result, fmt.Errorf("iterate registry: %w", err)
		}
	}
	return result, nil
}
func (r RegistryRepository) Save(reg registry.Registry) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin registry transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := WriteRegistry(tx, reg); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit registry: %w", err)
	}
	return nil
}

// WriteRegistry reconciles by primary key, retaining row identity and touching
// only changed definitions. It can share a publication transaction with jobs.
func WriteRegistry(tx *sql.Tx, reg registry.Registry) error {
	// Upsert parents before children, then prune children before parents. Never
	// REPLACE a server: SQLite implements REPLACE as DELETE followed by INSERT.
	for position, s := range reg.Servers {
		b, err := json.Marshal(s)
		if err != nil {
			return fmt.Errorf("encode server: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO servers(id,definition,position) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET definition=excluded.definition,position=excluded.position WHERE servers.definition<>excluded.definition OR servers.position<>excluded.position`, s.ID, string(b), position); err != nil {
			return fmt.Errorf("persist server: %w", err)
		}
	}
	for position, t := range reg.Tools {
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("encode tool: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO tools(id,server_id,definition,position) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET server_id=excluded.server_id,definition=excluded.definition,position=excluded.position WHERE tools.position<>excluded.position OR tools.server_id<>excluded.server_id OR tools.definition<>excluded.definition`, t.ID, t.ServerID, string(b), position); err != nil {
			return fmt.Errorf("persist tool: %w", err)
		}
	}
	tools := make(map[string]bool, len(reg.Tools))
	for _, t := range reg.Tools {
		tools[t.ID] = true
	}
	servers := make(map[string]bool, len(reg.Servers))
	for _, s := range reg.Servers {
		servers[s.ID] = true
	}
	for _, table := range []struct {
		name string
		keep map[string]bool
	}{{"tools", tools}, {"servers", servers}} {
		rows, err := tx.Query("SELECT id FROM " + table.name)
		if err != nil {
			return fmt.Errorf("replace registry: %w", err)
		}
		var removed []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			if !table.keep[id] {
				removed = append(removed, id)
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		for _, id := range removed {
			if _, err := tx.Exec("DELETE FROM "+table.name+" WHERE id=?", id); err != nil {
				return fmt.Errorf("replace registry: %w", err)
			}
		}
	}
	return nil
}
