package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// DBTX lets repositories participate in a caller-owned transaction. Domain
// types are supplied as T so store never imports model, scope or maintenance.
type DBTX interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

type Definitions[T any] struct {
	db    DBTX
	table string
}

func ModelProfiles[T any](db DBTX) Definitions[T] { return Definitions[T]{db, "model_profiles"} }
func Repositories[T any](db DBTX) Definitions[T]  { return Definitions[T]{db, "repositories"} }
func MaintenanceSources[T any](db DBTX) Definitions[T] {
	return Definitions[T]{db, "maintenance_sources"}
}
func MaintenanceRuns[T any](db DBTX) Definitions[T] { return Definitions[T]{db, "maintenance_runs"} }
func MaintenanceAcceptance[T any](db DBTX) Definitions[T] {
	return Definitions[T]{db, "maintenance_acceptance"}
}

func marshalDefinition[T any](value T) (string, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r Definitions[T]) List() ([]T, error) {
	return listDefinitions[T](r.db, definitionLabels(r.table), "SELECT definition FROM "+r.table+" ORDER BY rowid")
}
func (r Definitions[T]) Put(id string, value T) error {
	raw, err := marshalDefinition(value)
	if err != nil {
		return err
	}
	_, err = r.db.Exec("INSERT INTO "+r.table+"(id,definition) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET definition=excluded.definition", id, raw)
	return err
}
func (r Definitions[T]) Insert(id string, value T) error {
	raw, err := marshalDefinition(value)
	if err != nil {
		return err
	}
	_, err = r.db.Exec("INSERT INTO "+r.table+"(id,definition) VALUES(?,?)", id, raw)
	return err
}
func (r Definitions[T]) Update(id string, value T) error {
	raw, err := marshalDefinition(value)
	if err != nil {
		return err
	}
	_, err = r.db.Exec("UPDATE "+r.table+" SET definition=? WHERE id=?", raw, id)
	return err
}
func (r Definitions[T]) Delete(id string) error {
	_, err := r.db.Exec("DELETE FROM "+r.table+" WHERE id=?", id)
	return err
}

// Clear supports ordered profile replacement inside a transaction; registry
// reconciliation uses its own repository and never clears its tables.
func (r Definitions[T]) Clear() error { _, err := r.db.Exec("DELETE FROM " + r.table); return err }

type Singleton[T any] struct {
	db    DBTX
	table string
}

func Settings[T any](db DBTX) Singleton[T]   { return Singleton[T]{db, "settings"} }
func ScopeState[T any](db DBTX) Singleton[T] { return Singleton[T]{db, "scope_state"} }
func (r Singleton[T]) Count() (int, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM " + r.table).Scan(&count)
	return count, err
}
func (r Singleton[T]) Load() (T, error) {
	var raw string
	var out T
	if err := r.db.QueryRow("SELECT definition FROM " + r.table + " WHERE id=1").Scan(&raw); err != nil {
		return out, wrapNotFound(err)
	}
	err := json.Unmarshal([]byte(raw), &out)
	return out, err
}
func (r Singleton[T]) Save(value T) error {
	raw, err := marshalDefinition(value)
	if err != nil {
		return err
	}
	_, err = r.db.Exec("INSERT INTO "+r.table+"(id,definition) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET definition=excluded.definition", raw)
	return err
}

func listDefinitions[T any](db DBTX, labels readLabels, query string, args ...any) ([]T, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, labelError(labels.query, err)
	}
	defer func() { _ = rows.Close() }()
	out := []T{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, labelError(labels.scan, err)
		}
		var value T
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, labelError(labels.decode, err)
		}
		out = append(out, value)
	}
	return out, labelError(labels.iterate, rows.Err())
}

type ScopeRuleRepository[T any] struct{ db DBTX }

func ScopeRules[T any](db DBTX) ScopeRuleRepository[T] { return ScopeRuleRepository[T]{db} }
func (r ScopeRuleRepository[T]) List() ([]T, error) {
	return listDefinitions[T](r.db, readLabels{query: "query scope_rules", decode: "decode scope_rule"}, "SELECT definition FROM scope_rules ORDER BY repo_key,agent_key")
}
func (r ScopeRuleRepository[T]) Put(repo, agent string, value T) error {
	raw, err := marshalDefinition(value)
	if err != nil {
		return err
	}
	_, err = r.db.Exec("INSERT INTO scope_rules(repo_key,agent_key,definition) VALUES(?,?,?) ON CONFLICT(repo_key,agent_key) DO UPDATE SET definition=excluded.definition", repo, agent, raw)
	return err
}
func (r ScopeRuleRepository[T]) Delete(repo, agent string) error {
	_, err := r.db.Exec("DELETE FROM scope_rules WHERE repo_key=? AND agent_key=?", repo, agent)
	return err
}
func (r ScopeRuleRepository[T]) DeleteRepository(repo string) error {
	_, err := r.db.Exec("DELETE FROM scope_rules WHERE repo_key=?", repo)
	return err
}

type ObservationRepository[T any] struct{ db DBTX }

func ScopeObservations[T any](db DBTX) ObservationRepository[T] { return ObservationRepository[T]{db} }
func (r ObservationRepository[T]) List(limit int) ([]T, error) {
	return listDefinitions[T](r.db, readLabels{query: "query scope_observations", decode: "decode scope_observation"}, "SELECT definition FROM scope_observations ORDER BY last_seen DESC LIMIT ?", limit)
}
func (r ObservationRepository[T]) Put(fingerprint, lastSeen string, value T) error {
	raw, err := marshalDefinition(value)
	if err != nil {
		return err
	}
	_, err = r.db.Exec("INSERT INTO scope_observations(fingerprint,last_seen,definition) VALUES(?,?,?) ON CONFLICT(fingerprint) DO UPDATE SET last_seen=excluded.last_seen,definition=excluded.definition", fingerprint, lastSeen, raw)
	return err
}
func (r ObservationRepository[T]) Delete(fingerprint string) error {
	_, err := r.db.Exec("DELETE FROM scope_observations WHERE fingerprint=?", fingerprint)
	return err
}

type CheckRepository[T any] struct{ db DBTX }

func ConnectionChecks[T any](db DBTX) CheckRepository[T] { return CheckRepository[T]{db} }
func (r CheckRepository[T]) Put(id, fingerprint string, value T) error {
	raw, err := marshalDefinition(value)
	if err != nil {
		return err
	}
	_, err = r.db.Exec("INSERT INTO connection_checks(id,fingerprint,definition) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET fingerprint=excluded.fingerprint,definition=excluded.definition", id, fingerprint, raw)
	return err
}
func (r CheckRepository[T]) Get(id string) (T, string, error) {
	var value T
	var fingerprint, raw string
	err := r.db.QueryRow("SELECT fingerprint,definition FROM connection_checks WHERE id=?", id).Scan(&fingerprint, &raw)
	if err != nil {
		return value, fingerprint, wrapNotFound(err)
	}
	err = json.Unmarshal([]byte(raw), &value)
	return value, fingerprint, err
}

type DeliveryRepository[T any] struct{ db DBTX }

func MaintenanceDeliveries[T any](db DBTX) DeliveryRepository[T] { return DeliveryRepository[T]{db} }
func (r DeliveryRepository[T]) RunID(server, provider, delivery string) (string, error) {
	var id string
	err := r.db.QueryRow("SELECT run_id FROM maintenance_deliveries WHERE server_id=? AND provider=? AND delivery_id=?", server, provider, delivery).Scan(&id)
	return id, wrapNotFound(err)
}
func (r DeliveryRepository[T]) Insert(server, provider, delivery, receivedAt string, value T) error {
	raw, err := marshalDefinition(value)
	if err != nil {
		return err
	}
	_, err = r.db.Exec("INSERT INTO maintenance_deliveries(server_id,provider,delivery_id,received_at,run_id,definition) VALUES(?,?,?,?,'',?)", server, provider, delivery, receivedAt, raw)
	return err
}
func (r DeliveryRepository[T]) Pending() ([]T, error) {
	return listDefinitions[T](r.db, readLabels{query: "read pending signals", scan: "read signal", decode: "decode signal", iterate: "iterate signals"}, "SELECT definition FROM maintenance_deliveries WHERE run_id='' ORDER BY received_at DESC")
}
func (r DeliveryRepository[T]) Assign(server, run string) error {
	_, err := r.db.Exec("UPDATE maintenance_deliveries SET run_id=? WHERE server_id=? AND run_id=''", run, server)
	return err
}

type readLabels struct{ query, scan, decode, iterate string }

func labelError(label string, err error) error {
	if err == nil || label == "" {
		return err
	}
	return fmt.Errorf("%s: %w", label, err)
}
func definitionLabels(table string) readLabels {
	switch table {
	case "model_profiles":
		return readLabels{query: "query model profiles", scan: "read model profile", decode: "decode model profile"}
	case "repositories":
		return readLabels{query: "query repositories", decode: "decode repository"}
	case "maintenance_sources":
		return readLabels{query: "load maintenance", scan: "read maintenance", decode: "decode maintenance source", iterate: "iterate maintenance"}
	case "maintenance_runs":
		return readLabels{query: "load maintenance", scan: "read maintenance", decode: "decode maintenance run", iterate: "iterate maintenance"}
	case "maintenance_acceptance":
		return readLabels{query: "load maintenance", scan: "read maintenance", decode: "decode acceptance suite", iterate: "iterate maintenance"}
	}
	panic("unknown definition table")
}
