package maintenance

import (
	"os"
	"path/filepath"
	"testing"

	"gatewaylab/internal/jsonutil"
	"gatewaylab/internal/store"
)

func TestPublicationCommitsRegistryAndRunAtomically(t *testing.T) {
	m := testMaintainer(t)
	before := m.catalog.Snapshot()
	if err := store.Registry(m.db).Save(before); err != nil {
		t.Fatal(err)
	}
	r := enqueueTest(t, m, true)
	suite := AcceptanceSuite{ServerID: "service", Contracts: []ContractCase{}, Routing: []RoutingCase{{Name: "logs", Intent: "logs", Expected: []string{"service.logs"}}}}
	if err := m.SaveAcceptance(suite); err != nil {
		t.Fatal(err)
	}
	r.Status = "ready"
	r.Tools = []Tool{{ID: "service.logs", ServerID: "service", Name: "logs", Description: "Search logs", Actors: []string{"developer"}, InputSchema: objectSchema(map[string]any{}), Method: "GET", Path: "/logs"}}
	r.Acceptance = AcceptanceReport{Configured: true, Passed: true, Fingerprint: jsonutil.MustDigest(m.suites["service"]), RoutingFingerprint: m.router.RoutingFingerprint("service")}
	r.Review = PublicationReview{Required: true, Reasons: []ReviewReason{}}
	revision := filepath.Join(m.directory(r.ID), "revision")
	if err := os.MkdirAll(revision, 0700); err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"summary":"fixture","tools":[]}`)
	if err := os.WriteFile(filepath.Join(revision, "manifest.json"), content, 0400); err != nil {
		t.Fatal(err)
	}
	r.Artifacts = map[string]string{"manifest.json": jsonutil.MustDigest(string(content))}
	if err := m.saveRun(r); err != nil {
		t.Fatal(err)
	}
	// The catalog write succeeds first; rejecting the run update must roll it back.
	if _, err := m.db.Exec(`CREATE TRIGGER reject_publication BEFORE UPDATE ON maintenance_runs BEGIN SELECT RAISE(ABORT, 'fixture publication failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := m.Publish(r.ID, false); err == nil {
		t.Fatal("publication succeeded after a failed run write")
	}
	persisted, err := store.Registry(m.db).Load()
	if err != nil {
		t.Fatal(err)
	}
	if jsonutil.MustDigest(before) != jsonutil.MustDigest(persisted) || jsonutil.MustDigest(before) != jsonutil.MustDigest(m.catalog.Snapshot()) {
		t.Fatal("failed publication changed the persisted or visible catalog")
	}
	persistedRuns, err := store.MaintenanceRuns[MaintenanceRun](m.db).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(persistedRuns) != 1 || persistedRuns[0].Status != "ready" || m.runs[r.ID].Status != "ready" {
		t.Fatal("failed publication changed run state")
	}
	if _, err := m.db.Exec("DROP TRIGGER reject_publication"); err != nil {
		t.Fatal(err)
	}
	if err := m.Publish(r.ID, false); err != nil {
		t.Fatal(err)
	}
	persisted, err = store.Registry(m.db).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Tools) != 1 || !persisted.Tools[0].Enabled || jsonutil.MustDigest(persisted) != jsonutil.MustDigest(m.catalog.Snapshot()) {
		t.Fatal("publication did not expose the committed catalog")
	}
	persistedRuns, err = store.MaintenanceRuns[MaintenanceRun](m.db).List()
	if err != nil {
		t.Fatal(err)
	}
	if persistedRuns[0].Status != "published" || m.runs[r.ID].Status != "published" {
		t.Fatal("publication did not commit the run")
	}
	if err := m.Publish(r.ID, true); err != nil {
		t.Fatal(err)
	}
	persisted, err = store.Registry(m.db).Load()
	if err != nil {
		t.Fatal(err)
	}
	if jsonutil.MustDigest(before) != jsonutil.MustDigest(persisted) || m.runs[r.ID].Status != "rolled_back" {
		t.Fatal("rollback did not restore the committed catalog")
	}
}

func TestQuerySnapshotsDoNotShareWorkerState(t *testing.T) {
	m := testMaintainer(t)
	r := enqueueTest(t, m, false)
	snapshot := m.Status()
	snapshot.Sources[0].Files[0] = "caller mutation"
	snapshot.Runs[0].Source.Files[0] = "caller mutation"
	source, _ := m.Source("service")
	if source.Files[0] == "caller mutation" {
		t.Fatal("status query shared source files")
	}
	got, err := m.GetRun(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	got.Source.Files[0] = "another mutation"
	again, err := m.GetRun(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Source.Files[0] == "another mutation" {
		t.Fatal("run query shared worker state")
	}
}
