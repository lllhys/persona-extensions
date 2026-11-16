package main

import (
	"context"
	"testing"
	"time"
)

// A job that never changed Cold must not write its startup copy over a restore that committed meanwhile.
func TestJobNoOpPreservesConcurrentColdChange(t *testing.T) {
	a := newTestApp(t)
	if err := a.db().update(func(st *State, now time.Time) error {
		st.Machines = []*Machine{{ID: "build-box", Host: "build-box", Name: "build-box", Enabled: true}}
		st.setCold("build-box", &ColdCatalog{MachineID: "build-box", Entries: []ColdEntry{{ID: "old", State: "archived"}}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	_, _, err := a.startJob("build-box", jobRefresh, "test", func(ctx context.Context, sc *scratch, now time.Time) (string, error) {
		close(started)
		<-release
		return "", nil // the job itself never touches Cold
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err := a.db().update(func(st *State, now time.Time) error {
		st.setCold("build-box", &ColdCatalog{MachineID: "build-box", Entries: []ColdEntry{{ID: "new", State: "restored"}}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	a.waitJobs()
	var entries []ColdEntry
	if err := a.db().read(func(st *State) { entries = st.cold("build-box").Entries }); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "new" || entries[0].State != "restored" {
		t.Fatalf("the no-op job reverted the concurrent restore: %+v", entries)
	}
}

func TestBrewScriptRefusesSilentStatusFailure(t *testing.T) {
	dir := t.TempDir()
	fakeBrew(t, dir, "0.3.0", false, "status-fail")
	if o := runBrewScript(t, dir); o.ok || o.step != "verify" {
		t.Fatalf("a silent sessions status failure still reported install success: %+v", o)
	}
}
