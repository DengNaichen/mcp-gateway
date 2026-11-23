package store

import (
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"gatewaylab/internal/registry"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func TestDataDirectoryLockHelper(t *testing.T) {
	dir := os.Getenv("GATEWAY_STORE_LOCK_TEST_DIR")
	if dir == "" {
		return
	}
	db, err := Open(dir)
	if db != nil {
		_ = db.Close()
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("expected process lock, got %v", err)
	}
}
func TestDataDirectoryLockLifetime(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if other, err := Open(dir); !errors.Is(err, ErrLocked) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("duplicate open: %v", err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if other, err := Open(alias); !errors.Is(err, ErrLocked) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("symlink duplicate open: %v", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDataDirectoryLockHelper$")
	cmd.Env = append(os.Environ(), "GATEWAY_STORE_LOCK_TEST_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("second process: %v\n%s", err, out)
	}
	independent, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = independent.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = reopened.Close()
}
func TestFailedOpenReleasesLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.db")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if db, err := Open(dir); err == nil {
		_ = db.Close()
		t.Fatal("expected open failure")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("lock leaked after failure: %v", err)
	}
	_ = db.Close()
}

func sampleRegistry() registry.Registry {
	return registry.Registry{Servers: []registry.Server{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}, Tools: []registry.Tool{{ID: "a.one", ServerID: "a", Name: "one"}, {ID: "b.two", ServerID: "b", Name: "two"}}}
}
func TestRegistryUpsertRetainsRowsAndDoesNotTouchUnchangedDefinitions(t *testing.T) {
	db := openTestStore(t)
	repo := Registry(db.DB)
	reg := sampleRegistry()
	if err := repo.Save(reg); err != nil {
		t.Fatal(err)
	}
	// A dependent table would make DELETE-all fail, even with the same IDs.
	if _, err := db.Exec(`CREATE TABLE dependent(server_id TEXT REFERENCES servers(id));INSERT INTO dependent VALUES('a');
 CREATE TABLE writes(kind TEXT);
 CREATE TRIGGER server_deleted AFTER DELETE ON servers BEGIN INSERT INTO writes VALUES('delete');END;
 CREATE TRIGGER server_updated AFTER UPDATE ON servers BEGIN INSERT INTO writes VALUES('server');END;
 CREATE TRIGGER tool_updated AFTER UPDATE ON tools BEGIN INSERT INTO writes VALUES('tool');END;`); err != nil {
		t.Fatal(err)
	}
	var rowid int64
	if err := db.QueryRow("SELECT rowid FROM servers WHERE id='a'").Scan(&rowid); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(reg); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM writes").Scan(&count); err != nil || count != 0 {
		t.Fatalf("unchanged save touched rows: %d %v", count, err)
	}
	reg.Servers[0].Name = "Updated"
	if err := repo.Save(reg); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := db.QueryRow("SELECT rowid FROM servers WHERE id='a'").Scan(&after); err != nil || after != rowid {
		t.Fatalf("row identity changed: %d -> %d (%v)", rowid, after, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM writes WHERE kind='server'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("wanted one updated server: %d %v", count, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM writes WHERE kind<>'server'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("unrelated rows touched: %d %v", count, err)
	}
}
func TestRegistryReconciliationOrderingDeletionAndRollback(t *testing.T) {
	db := openTestStore(t)
	repo := Registry(db.DB)
	reg := sampleRegistry()
	if err := repo.Save(reg); err != nil {
		t.Fatal(err)
	}
	reg.Servers[0], reg.Servers[1] = reg.Servers[1], reg.Servers[0]
	reg.Tools[0], reg.Tools[1] = reg.Tools[1], reg.Tools[0]
	if err := repo.Save(reg); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Load()
	if err != nil || !reflect.DeepEqual(got, reg) {
		t.Fatalf("ordering changed: %+v %v", got, err)
	}
	failed := registry.Clone(reg)
	failed.Servers[0].Name = "must roll back"
	failed.Tools[0].ServerID = "missing"
	if err := repo.Save(failed); err == nil {
		t.Fatal("expected foreign key failure")
	}
	got, err = repo.Load()
	if err != nil || !reflect.DeepEqual(got, reg) {
		t.Fatalf("failed save partially committed: %+v %v", got, err)
	}
	// A tool can move away from a removed parent within the same transaction.
	reg.Servers = reg.Servers[:1]
	reg.Tools[1].ServerID = reg.Servers[0].ID
	if err := repo.Save(reg); err != nil {
		t.Fatal(err)
	}
	reg.Tools = reg.Tools[:1]
	if err := repo.Save(reg); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Load()
	if err != nil || !reflect.DeepEqual(got, reg) {
		t.Fatalf("reconciliation failed: %+v %v", got, err)
	}
	if err := repo.Save(registry.Registry{}); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Load()
	if err != nil || len(got.Servers) != 0 || len(got.Tools) != 0 {
		t.Fatalf("clear registry: %+v %v", got, err)
	}
}
func TestRegistrySharesAtomicPublicationTransaction(t *testing.T) {
	db := openTestStore(t)
	repo := Registry(db.DB)
	reg := sampleRegistry()
	if err := repo.Save(reg); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	reg.Servers[0].Name = "uncommitted"
	if err := WriteRegistry(tx, reg); err != nil {
		t.Fatal(err)
	}
	if err := MaintenanceRuns[map[string]string](tx).Insert("run", map[string]string{"status": "published"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Load()
	if err != nil || got.Servers[0].Name != "A" {
		t.Fatalf("rolled back registry visible: %+v %v", got, err)
	}
	runs, err := MaintenanceRuns[map[string]string](db.DB).List()
	if err != nil || len(runs) != 0 {
		t.Fatalf("rolled back job visible: %+v %v", runs, err)
	}
}

func TestLegacyMigrationRetainsRegistryAndScopeData(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", filepath.Join(dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE servers(id TEXT PRIMARY KEY,definition TEXT NOT NULL CHECK(json_valid(definition)));
 CREATE TABLE tools(id TEXT PRIMARY KEY,server_id TEXT NOT NULL REFERENCES servers(id),definition TEXT NOT NULL CHECK(json_valid(definition)));
 INSERT INTO servers VALUES('b','{"id":"b"}'),('a','{"id":"a"}');
 INSERT INTO tools VALUES('two','b','{"id":"two","server_id":"b"}'),('one','a','{"id":"one","server_id":"a"}');
 PRAGMA user_version=5;`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Registry(db.DB).Load()
	if err != nil || got.Servers[0].ID != "b" || got.Tools[0].ID != "two" {
		t.Fatalf("migration reordered data: %+v %v", got, err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 9 {
		t.Fatalf("version=%d, %v", version, err)
	}
	rules, err := ScopeRules[map[string]any](db.DB).List()
	if err != nil || len(rules) != 1 || rules[0]["repo_key"] != "*" {
		t.Fatalf("scope seed: %+v %v", rules, err)
	}
	if _, hasAgentKey := rules[0]["agent_key"]; hasAgentKey {
		t.Fatalf("migrated rule should not retain agent_key: %+v", rules[0])
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("migration not idempotent: %v", err)
	}
	_ = reopened.Close()
}

func TestLegacyScopeRulesMigrationDropsAgentOverrides(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", filepath.Join(dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
CREATE TABLE servers(id TEXT PRIMARY KEY,definition TEXT NOT NULL CHECK(json_valid(definition)),position INTEGER NOT NULL DEFAULT 0);
CREATE TABLE tools(id TEXT PRIMARY KEY,server_id TEXT NOT NULL REFERENCES servers(id),definition TEXT NOT NULL CHECK(json_valid(definition)),position INTEGER NOT NULL DEFAULT 0);
CREATE TABLE scope_state(id INTEGER PRIMARY KEY CHECK(id=1),definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE repositories(id TEXT PRIMARY KEY,definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE scope_rules(repo_key TEXT NOT NULL,agent_key TEXT NOT NULL,definition TEXT NOT NULL CHECK(json_valid(definition)),PRIMARY KEY(repo_key,agent_key));
CREATE TABLE scope_observations(fingerprint TEXT PRIMARY KEY,last_seen TEXT NOT NULL,definition TEXT NOT NULL CHECK(json_valid(definition)));
INSERT INTO scope_state(id,definition) VALUES(1,'{"enforce":true}');
INSERT INTO scope_rules(repo_key,agent_key,definition) VALUES
  ('*','*','{"repo_key":"*","agent_key":"*","grants":[]}'),
  ('*','codex','{"repo_key":"*","agent_key":"codex","grants":[{"server_id":"svc","access":"rw"}]}'),
  ('repo-1','*','{"repo_key":"repo-1","agent_key":"*","grants":[{"server_id":"svc","access":"ro"}]}'),
  ('repo-1','claude','{"repo_key":"repo-1","agent_key":"claude","grants":[{"server_id":"svc","access":"rw"}]}');
PRAGMA user_version=7;`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	rules, err := ScopeRules[map[string]any](db.DB).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected only 2 repo-level rules after v8 migration, got %+v", rules)
	}
	if rules[0]["repo_key"] != "*" || len(rules[0]["grants"].([]any)) != 0 {
		t.Fatalf("unexpected migrated global default rule: %+v", rules[0])
	}
	if rules[1]["repo_key"] != "repo-1" {
		t.Fatalf("unexpected migrated repo-1 rule: %+v", rules[1])
	}
	grants := rules[1]["grants"].([]any)
	if len(grants) != 1 || grants[0].(map[string]any)["access"] != "ro" {
		t.Fatalf("expected repo-1 to retain ro rule rather than agent override, got %+v", rules[1])
	}
	for _, r := range rules {
		if _, hasAgentKey := r["agent_key"]; hasAgentKey {
			t.Fatalf("migrated rule still contains agent_key: %+v", r)
		}
	}
}

func TestRunTxAndErrNotFound(t *testing.T) {
	db := openTestStore(t)
	if _, _, err := ConnectionChecks[map[string]any](db.DB).Get("missing"); !errors.Is(err, ErrNotFound) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ConnectionChecks.Get missing: %v", err)
	}
	if _, err := Settings[map[string]any](db.DB).Load(); !errors.Is(err, ErrNotFound) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Settings.Load missing: %v", err)
	}
	if _, err := MaintenanceDeliveries[map[string]any](db.DB).RunID("svc", "github", "deliv"); !errors.Is(err, ErrNotFound) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("MaintenanceDeliveries.RunID missing: %v", err)
	}

	sentinel := errors.New("abort tx")
	if err := db.Tx(func(tx DBTX) error {
		if err := Settings[map[string]string](tx).Save(map[string]string{"k": "v"}); err != nil {
			return err
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
	if _, err := Settings[map[string]string](db.DB).Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled back write visible: %v", err)
	}

	if err := RunTx(db.DB, func(tx DBTX) error {
		return Settings[map[string]string](tx).Save(map[string]string{"k": "v"})
	}); err != nil {
		t.Fatal(err)
	}
	got, err := Settings[map[string]string](db.DB).Load()
	if err != nil || got["k"] != "v" {
		t.Fatalf("committed write missing: %+v %v", got, err)
	}
}

func TestActivityV9MigrationPreservesChronologyAndLegacyRows(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", filepath.Join(dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
CREATE TABLE activity (id TEXT PRIMARY KEY, time TEXT NOT NULL, kind TEXT NOT NULL, actor TEXT NOT NULL, status TEXT NOT NULL, title TEXT NOT NULL, duration_ms INTEGER NOT NULL, detail TEXT NOT NULL CHECK(json_valid(detail)));
CREATE INDEX activity_by_time ON activity(time DESC);
INSERT INTO activity VALUES
 ('a','2026-11-23T09:01:00Z','invoke','developer','success','legacy A',9,'{"preserved":true}'),
 ('b','2026-11-23T17:01:00+08:00','invoke','developer','success','legacy B',10,'{}'),
 ('c','2026-11-23T09:01:00.000000001Z','invoke','developer','success','legacy C',11,'{}'),
 ('d','2026-11-23T17:00:59.999999999+08:00','invoke','developer','success','legacy D',12,'{}');
PRAGMA user_version=8;
`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := Activity(db.DB)
	rows, err := repo.Find(ActivityFilter{Limit: 2})
	if err != nil || len(rows) != 2 || rows[0].ID != "c" || rows[1].ID != "b" {
		t.Fatalf("newest rows: %+v, %v", rows, err)
	}
	rows, err = repo.Find(ActivityFilter{Limit: 2, Before: &rows[1].Time, BeforeID: rows[1].ID})
	if err != nil || len(rows) != 2 || rows[0].ID != "a" || rows[1].ID != "d" {
		t.Fatalf("composite cursor failed to preserve rows with tied times: %+v, %v", rows, err)
	}
	legacy := rows[0]
	if string(legacy.Detail) != `{"preserved":true}` || legacy.DurationMS != 9 ||
		legacy.RepoID != "" || legacy.ServiceID != "" || legacy.Protocol != "" || legacy.Action != "" ||
		legacy.Decision != "" || legacy.RuleID != "" || legacy.ApprovalID != "" || legacy.SkillID != "" {
		t.Fatalf("legacy row changed: %+v", legacy)
	}
}
