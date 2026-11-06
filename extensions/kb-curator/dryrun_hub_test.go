package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/store"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/stub"
)

// A trial uses the model and leaves the curator's own state, and the knowledge base, as they were; only its report
// is left behind.
func TestTrialChangesNothing(t *testing.T) {
	fx, err := kbsrc.LoadFixture("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	stateDir, dataDir := t.TempDir(), t.TempDir()
	st, err := store.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	m := &stub.Model{}
	a := &app{dataDir: dataDir, c: &curate.Curator{Src: fx, Model: m, St: st, Cfg: curate.Defaults(), Loc: time.UTC}}
	summary, err := a.dryRunDaily(context.Background(), "2026-11-03")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, n := range m.Calls {
		calls += n
	}
	if calls == 0 || !strings.Contains(summary, "试跑 2026-11-03") {
		t.Fatalf("no model calls (%v): %s", m.Calls, summary)
	}
	if len(st.S.Runs) != 0 || len(st.S.Items) != 0 || len(st.S.Proposals) != 0 || len(st.S.Usage) != 0 {
		t.Fatalf("the curator's own state changed: %d runs, %d items", len(st.S.Runs), len(st.S.Items))
	}
	if entries, _ := os.ReadDir(stateDir); len(entries) > 1 {
		t.Fatalf("the state directory got files: %v", entries)
	}
	if len(fx.Writes()) != 0 {
		t.Fatalf("the knowledge base was written: %v", fx.Writes())
	}
	report, err := os.ReadFile(filepath.Join(dataDir, "dryruns", "daily-2026-11-03.md"))
	if err != nil || !strings.Contains(string(report), "日级整理 2026-11-03") {
		t.Fatalf("report: %v %s", err, report)
	}
}
