package curate

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	srcs "github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/sources"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

type aboutOnlyHub struct{ hubapi.None }

func (aboutOnlyHub) AboutMe(context.Context) (string, error) {
	return "I work on project-x.\nI prefer short answers.", nil
}

// A model-down day commits no source cursor or active day. The successful retry must read About Me again and commit
// the production sources.With ledger, rather than skipping it because the failed pass advanced its cursor.
func TestModelDownDoesNotCommitSourceLedger(t *testing.T) {
	fx, err := kbsrc.LoadFixture("../../testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	more := srcs.With(fx, aboutOnlyHub{}, time.UTC)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &flaky{down: true}
	cfg := Defaults()
	cfg.AdmitAllBelowTokens = 0
	now := time.Date(2026, 11, 9, 4, 0, 0, 0, time.UTC)
	c := &Curator{Src: more, Collect: more, Model: m, St: st, Cfg: cfg, Loc: time.UTC,
		Now: func() time.Time { return now }}
	const day = "2026-11-03"
	if _, err := c.RunDaily(context.Background(), day, false); !errors.Is(err, ErrModelDown) {
		t.Fatalf("model down: %v, want ErrModelDown", err)
	}
	if got := st.S.Cursors[entry.SrcAbout]; got != "" {
		t.Fatalf("failed pass committed About Me cursor %q", got)
	}
	if slices.Contains(st.S.ActiveDays, day) {
		t.Fatalf("failed pass marked %s active: %v", day, st.S.ActiveDays)
	}

	m.down = false
	now = now.Add(10 * time.Minute)
	if _, err := c.RunDaily(context.Background(), day, false); err != nil {
		t.Fatalf("recovered pass: %v", err)
	}
	if got := st.S.Cursors[entry.SrcAbout]; got == "" {
		t.Fatal("successful retry did not read and commit About Me")
	}
	if !slices.Contains(st.S.ActiveDays, day) {
		t.Fatalf("successful retry did not mark %s active: %v", day, st.S.ActiveDays)
	}
}
