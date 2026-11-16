package curate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/stub"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
)

// The hub's refusal, as model.Hub passes it on.
var errDeclined = fmt.Errorf("%w: model.complete: ai: the model declined to answer", model.ErrRefused)

// A refused batch is split in halves down to a few lines; only the batch that is still refused is skipped, and every
// refused batch is kept with its reason.
func TestRefusedBatchIsSplit(t *testing.T) {
	c := &Curator{}
	u := &Unit{Ref: "chats/c1", Meta: kbsrc.Meta{Name: "一个群"}, Day: "2026-11-03"}
	d := triage.Decision{Chat: u.Ref, Shown: map[int]bool{}}
	for n := 1; n <= 40; n++ {
		d.Shown[n] = true
	}
	run := &store.Run{Counts: map[string]int{}}
	var read []int
	parts := map[string]bool{}
	err := c.inBatches("daily", u, d, run, func(d triage.Decision, part string) error {
		if d.Shown[17] { // one line the model will not touch
			return errDeclined
		}
		parts[part] = true
		read = append(read, shownLines(d)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(read)
	var want []int
	for n := 1; n <= 40; n++ {
		if n < 16 || n > 20 {
			want = append(want, n)
		}
	}
	if !slices.Equal(read, want) {
		t.Fatalf("read %v, want every line but 16–20", read)
	}
	for _, p := range []string{"1-10", "11-15", "21-40"} {
		if !parts[p] {
			t.Errorf("no batch %s (got %v)", p, parts)
		}
	}
	// Refused: 1–40, 1–20, 11–20 split; 16–20 skipped (three splits deep).
	var got []string
	for _, r := range run.Refusals {
		got = append(got, fmt.Sprintf("%d-%d %s", r.From, r.To, r.Outcome))
		if r.Reason != "ai: the model declined to answer" || r.Unit != "chats/c1" || r.Step != "daily" {
			t.Errorf("refusal kept as %+v", r)
		}
	}
	if w := []string{"1-40 split", "1-20 split", "11-20 split", "16-20 skipped"}; !slices.Equal(got, w) {
		t.Fatalf("refusals %v, want %v", got, w)
	}
	if run.Counts["daily_refused"] != 4 || run.Counts["daily_refused_skipped"] != 1 || run.Counts["daily_refused_lines"] != 5 {
		t.Errorf("counts %v", run.Counts)
	}
	// Any other error is the caller's: not split, not skipped.
	run = &store.Run{Counts: map[string]int{}}
	calls := 0
	err = c.inBatches("daily", u, d, run, func(triage.Decision, string) error { calls++; return model.ErrUnavailable })
	if !errors.Is(err, model.ErrUnavailable) || calls != 1 || len(run.Refusals) != 0 {
		t.Fatalf("an unavailable model: %v after %d calls, %d refusals", err, calls, len(run.Refusals))
	}
}

// A group whose day the model declines because of one line is not skipped: the rest of it is read, the declined lines
// are listed in the report and on the run, and the next run does not digest the parts it read again.
func TestDailyRefusalKeepsTheRest(t *testing.T) {
	m := &stub.Model{}
	declines := stub.Scripted(func(r model.Request) (string, error) {
		if (r.Step == "daily" || r.Step == "memory") && strings.Contains(r.User, "限流上限") {
			return "", errDeclined
		}
		resp, err := m.Complete(context.Background(), r)
		return resp.Text, err
	})
	c, _ := newCurator(t, declines, "shadow")
	c.Live = &Live{}
	run, err := c.RunDaily(context.Background(), "2026-11-03", false)
	if err != nil {
		t.Fatal(err)
	}
	var atlas []store.Refusal
	for _, r := range run.Refusals {
		if r.Unit == "chats/lark-grp-atlas" && r.Step == "daily" {
			atlas = append(atlas, r)
		}
	}
	if len(atlas) != 2 || atlas[0].Outcome != store.RefusalSplit || atlas[1].Outcome != store.RefusalSkipped ||
		atlas[1].From != 5 || atlas[1].To != 7 {
		t.Fatalf("Atlas refusals: %+v", atlas)
	}
	if c.St.S.Digests["chats/lark-grp-atlas@2026-11-03#3-4"] == nil {
		t.Fatalf("the lines before the declined one were not digested: %v", keys(c.St.S.Digests))
	}
	// What the model answered for those lines is kept: the part's digest cites line 3. (Line 3 is another person on
	// their own work, so it is a summary point, not an item about the user.)
	owned := false
	for _, p := range c.St.S.Digests["chats/lark-grp-atlas@2026-11-03#3-4"].Points {
		for _, e := range p.Evidence {
			owned = owned || e.N == 3
		}
	}
	if !owned {
		t.Error("nothing kept from the Atlas lines the model did answer for")
	}
	if run.Counts["digested"] == 0 || run.Counts["digest_errors"] != 0 {
		t.Errorf("the other units: %v", run.Counts)
	}
	for _, s := range []string{"## 模型拒答", "chats/lark-grp-atlas", "Atlas 项目群", "5–7（3 行）", "ai: the model declined to answer", "跳过", "拆小重试"} {
		if !strings.Contains(run.Report, s) {
			t.Errorf("the report does not say %q:\n%s", s, run.Report)
		}
	}
	// The health page's snapshot has them.
	if s := c.Live.Snapshot(); s == nil || s.Daily == nil || s.Daily.Period != "2026-11-03" || len(s.Daily.Refusals) != len(run.Refusals) || s.Daily.Skipped != 3 {
		t.Fatalf("snapshot: %+v", s)
	}
	// Again: the parts read are unchanged; only the refused batches are tried again.
	calls := m.Calls["daily"]
	again, err := c.RunDaily(context.Background(), "2026-11-03", false)
	if err != nil {
		t.Fatal(err)
	}
	if m.Calls["daily"] != calls || again.Counts["digest_unchanged"] == 0 {
		t.Errorf("parts digested again: %d → %d calls, %v", calls, m.Calls["daily"], again.Counts)
	}
	// Forgetting the source takes its refusals and their report lines with it.
	if _, err := c.Forget("lark"); err != nil {
		t.Fatal(err)
	}
	for _, r := range c.St.S.Runs {
		if slices.ContainsFunc(r.Refusals, func(x store.Refusal) bool { return strings.HasPrefix(x.Unit, "chats/lark-") }) ||
			strings.Contains(r.Report, "chats/lark-grp-atlas") {
			t.Fatalf("a forgotten source's refusal is still there: %+v\n%s", r.Refusals, r.Report)
		}
	}
	if s := c.Live.Snapshot(); slices.ContainsFunc(s.Daily.Refusals, func(x store.Refusal) bool { return strings.HasPrefix(x.Unit, "chats/lark-") }) {
		t.Fatalf("the snapshot still has a forgotten source's refusal: %+v", s.Daily.Refusals)
	}
}

// A declined triage call sends the unit to the weekly pass, as when the model is down, and says why.
func TestTriageRefusal(t *testing.T) {
	m := &stub.Model{}
	declines := stub.Scripted(func(r model.Request) (string, error) {
		if r.Step == "triage" {
			return "", errDeclined
		}
		resp, err := m.Complete(context.Background(), r)
		return resp.Text, err
	})
	c, _ := newCurator(t, declines, "shadow")
	run, err := c.RunDaily(context.Background(), "2026-11-03", false)
	if err != nil {
		t.Fatal(err)
	}
	if run.Counts["triage_refused"] == 0 || !slices.ContainsFunc(run.Refusals, func(r store.Refusal) bool {
		return r.Step == "triage" && r.Outcome == store.RefusalWeekly
	}) {
		t.Fatalf("triage refusals: %v %+v", run.Counts, run.Refusals)
	}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
