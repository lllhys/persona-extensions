package curate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	srcs "github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/sources"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/stub"
)

func dailyRuns(st *store.State) []string {
	var out []string
	for _, r := range st.Runs {
		if r.Kind == "daily" && r.Finished != "" {
			out = append(out, r.Period)
		}
	}
	return out
}

// The first-run replay runs the days oldest first, closes each ISO week with a weekly pass, and after a stop goes
// on from the next day, not from the start.
func TestBackfillOldestFirstResumable(t *testing.T) {
	c, _ := newCurator(t, &stub.Model{}, "shadow")
	c.Cfg.BackfillDays = 4
	c.StartBackfill("2026-11-06") // 11-02 … 11-05
	b := c.St.S.Backfill
	if b == nil || b.From != "2026-11-02" || b.To != "2026-11-05" || b.Next != "2026-11-02" || c.BackfillDone() {
		t.Fatalf("plan: %+v", b)
	}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if done, err := c.BackfillStep(ctx); err != nil || done {
			t.Fatalf("step %d: %v %v", i, done, err)
		}
	}
	// A restart: a new curator on the same state directory picks up where it stopped.
	st, err := store.Open(c.St.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.S.Backfill.Next != "2026-11-04" || st.S.Backfill.Finished != "" {
		t.Fatalf("saved progress: %+v", st.S.Backfill)
	}
	c2 := &Curator{Src: c.Src, Model: c.Model, St: st, Cfg: c.Cfg, Loc: c.Loc, Now: c.Now}
	c2.StartBackfill("2026-11-09") // planned already: kept, not moved
	if err := c2.RunBackfill(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(dailyRuns(st.S), ","); got != "2026-11-02,2026-11-03,2026-11-04,2026-11-05" {
		t.Errorf("daily runs: %s", got)
	}
	var weeks []string
	for _, r := range st.S.Runs {
		if r.Kind == "weekly" {
			weeks = append(weeks, r.Period)
		}
	}
	// 11-02 (Monday) … 11-05: no Sunday in the range, the last day closes W45.
	if strings.Join(weeks, ",") != "2026-W45" {
		t.Errorf("weekly passes: %v", weeks)
	}
	if !c2.BackfillDone() || st.S.Backfill.Finished == "" || st.S.Backfill.Next != "" {
		t.Errorf("finished: %+v", st.S.Backfill)
	}
	if done, err := c2.BackfillStep(ctx); !done || err != nil {
		t.Errorf("a finished replay does nothing: %v %v", done, err)
	}
}

func TestBackfillOff(t *testing.T) {
	c, _ := newCurator(t, &stub.Model{}, "shadow")
	c.Cfg.BackfillDays = 0
	c.StartBackfill("2026-11-06")
	if c.St.S.Backfill != nil || !c.BackfillDone() {
		t.Errorf("backfill_days 0: %+v", c.St.S.Backfill)
	}
}

// A replay that cannot go on (the context ends) stops without losing its place.
func TestBackfillStopsOnContext(t *testing.T) {
	c, _ := newCurator(t, &stub.Model{}, "shadow")
	c.Cfg.BackfillDays = 3
	c.StartBackfill("2026-11-06")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.RunBackfill(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err: %v", err)
	}
	if c.St.S.Backfill.Next != "2026-11-03" || c.BackfillDone() {
		t.Errorf("progress: %+v", c.St.S.Backfill)
	}
}

// With the sources wrapped in, a daily pass reads agent sessions as units of their own and the ledger lands in the
// state: the sources card, the cursors and the active day.
func TestDailyWithSources(t *testing.T) {
	c, fx := newCurator(t, &stub.Model{}, "shadow")
	h := &agentHub{}
	src := srcs.With(fx, h, time.UTC)
	c.Src, c.Collect = src, src
	if _, err := c.RunDaily(context.Background(), "2026-11-03", false); err != nil {
		t.Fatal(err)
	}
	st := c.St.S
	if s := st.Sources["S3/agents"]; s == nil || !s.Available || s.Units != 1 || s.Bytes == 0 {
		t.Errorf("S3 on the card: %+v", s)
	}
	if s := st.Sources["S6/memory"]; s == nil || s.Waiting != "H2" {
		t.Errorf("S6 waits for H2: %+v", s)
	}
	if st.Cursors["S3/m1/s1"] == "" {
		t.Errorf("cursor: %v", st.Cursors)
	}
	if len(st.ActiveDays) != 1 || st.ActiveDays[0] != "2026-11-03" {
		t.Errorf("active days: %v", st.ActiveDays)
	}
	units, err := c.units(context.Background(), "2026-11-03")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range units {
		if u.Ref == "agents/m1/s1" {
			found = true
			if u.Meta.Code != "S3" || len(u.Lines) == 0 || u.Lines[0].Speaker != "我" {
				t.Errorf("agent unit: %+v", u)
			}
		}
	}
	if !found {
		t.Error("no agent unit")
	}
}

type agentHub struct{ hubapi.None }

func (agentHub) Sessions(context.Context, time.Time) ([]hubapi.Session, error) {
	t := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	return []hubapi.Session{{Machine: "m1", ID: "s1", Title: "project-x", Started: t, Updated: t}}, nil
}

func (agentHub) Items(_ context.Context, _, _ string, q hubapi.ItemsQuery) ([]hubapi.Item, error) {
	t := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	items := []hubapi.Item{{Seq: 1, At: t, Kind: hubapi.KindUser, Text: "build project-x"},
		{Seq: 2, At: t, Kind: hubapi.KindToolUse, Tool: "Bash"}, {Seq: 3, At: t, Kind: hubapi.KindAgent, Text: "done"}}
	if q.Fold {
		items = hubapi.FoldWork(items, false)
	}
	return items, nil
}
