package curate

import (
	"context"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
)

func TestFocusTerms(t *testing.T) {
	got := FocusTerms([]string{"推进 project-x 上线", "the atlas launch"}, []string{"Atlas", "Old"})
	want := map[string]bool{norm.Q("推进 project-x 上线"): true, norm.Q("project-x"): true, norm.Q("the atlas launch"): true,
		norm.Q("atlas"): true, norm.Q("launch"): true}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected term %q in %v", g, got)
		}
	}
	for _, short := range []string{norm.Q("推进"), norm.Q("上线"), norm.Q("the")} {
		for _, g := range got {
			if g == short {
				t.Errorf("two-character word %q is a term: it would match everything", g)
			}
		}
	}
	if !FocusRelated(&Unit{Meta: kbsrc.Meta{Name: "weekly sync"}, Lines: []norm.Line{{N: 1, Body: "project-x 明天上线"}}}, got) {
		t.Error("a unit mentioning project-x is not related")
	}
	if FocusRelated(&Unit{Meta: kbsrc.Meta{Name: "lunch"}, Lines: []norm.Line{{N: 1, Body: "推进一下午饭"}}}, got) {
		t.Error("an unrelated unit is related")
	}
}

// TestFocusFirstDuringBackfill: first-run question 3 — during the history replay the units about what the user
// said they are pushing go first, the rest keeps the triage order; outside the replay nothing moves.
func TestFocusFirstDuringBackfill(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fx, err := kbsrc.LoadFixture("../../testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	c := &Curator{Src: fx, St: st, Cfg: Defaults()}
	units := map[string]*Unit{
		"chats/a": {Ref: "chats/a", Meta: kbsrc.Meta{Name: "group a"}, Lines: []norm.Line{{N: 1, Body: "lunch?"}}},
		"chats/b": {Ref: "chats/b", Meta: kbsrc.Meta{Name: "group b"}, Lines: []norm.Line{{N: 1, Body: "project-x 的发布清单"}}},
		"chats/c": {Ref: "chats/c", Meta: kbsrc.Meta{Name: "group c"}, Lines: []norm.Line{{N: 1, Body: "status"}}},
		"chats/d": {Ref: "chats/d", Meta: kbsrc.Meta{Name: "Atlas 周会"}, Lines: []norm.Line{{N: 1, Body: "notes"}}},
	}
	order := func() []triage.Decision {
		return []triage.Decision{{Chat: "chats/a"}, {Chat: "chats/b"}, {Chat: "chats/c"}, {Chat: "chats/d"}}
	}
	ids := func(ds []triage.Decision) string {
		s := ""
		for _, d := range ds {
			s += d.Chat[len(d.Chat)-1:]
		}
		return s
	}
	st.S.Onboarding = &entry.Onboarding{Focus: []string{"推进 project-x", "atlas 上线"}}
	// No replay planned: nothing moves.
	ds := order()
	if n := c.focusFirst(context.Background(), ds, units); n != 0 || ids(ds) != "abcd" {
		t.Fatalf("moved outside the replay: %d %s", n, ids(ds))
	}
	st.S.Backfill = &store.Backfill{From: "2026-11-01", To: "2026-11-03", Next: "2026-11-01"}
	ds = order()
	if n := c.focusFirst(context.Background(), ds, units); n != 2 || ids(ds) != "bdac" {
		t.Fatalf("focus first: %d %s", n, ids(ds))
	}
	// Without answers, or once the replay is finished, the triage order stays.
	st.S.Onboarding = &entry.Onboarding{}
	ds = order()
	if n := c.focusFirst(context.Background(), ds, units); n != 0 || ids(ds) != "abcd" {
		t.Fatalf("moved without answers: %s", ids(ds))
	}
	st.S.Onboarding = &entry.Onboarding{Focus: []string{"project-x"}}
	st.S.Backfill.Finished = "2026-11-04T00:00:00Z"
	ds = order()
	if n := c.focusFirst(context.Background(), ds, units); n != 0 || ids(ds) != "abcd" {
		t.Fatalf("moved after the replay: %s", ids(ds))
	}
}
