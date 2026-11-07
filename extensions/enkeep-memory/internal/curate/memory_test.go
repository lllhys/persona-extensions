package curate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/memory"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/stub"
)

// The daily pass asks the model for the memory of every admitted unit (through model.complete, step "memory"),
// checks the citations like the digest's, and the memory package scores what passes.
func TestDailyMemory(t *testing.T) {
	m := &stub.Model{}
	c, _ := newCurator(t, m, "shadow")
	ctx := context.Background()
	run, err := c.RunDaily(ctx, "2026-11-04", false)
	if err != nil {
		t.Fatal(err)
	}
	if m.Calls["memory"] == 0 || run.Counts["memory_units"] == 0 || run.Counts["memory_errors"] != 0 {
		t.Fatalf("memory calls %d, counts %v, notes %v", m.Calls["memory"], run.Counts, run.Notes)
	}
	var e *memory.Entry
	for _, x := range c.St.S.Memory.Entries {
		if strings.Contains(x.Text, "冒烟测试") {
			e = x
		}
	}
	if e == nil {
		t.Fatalf("the owner's rule is remembered: %v", c.St.S.Memory.Entries)
	}
	if e.Evidence[0].Speaker != memory.SpeakerUser || e.Evidence[0].Anchor != "chats/lark-dm-1#4" || e.Use != memory.UseUnderstand {
		t.Fatalf("evidence: %+v", e)
	}
	if st := e.Status; st.State != memory.Active || st.Layer != memory.Guideline || st.Score != 60 {
		t.Fatalf("a requirement in a work discussion is a guideline at once: %+v", st)
	}
	// The weekly report lists what became active, and only counts what is private.
	e.PrivateSentences = []memory.PrivateSentence{{I: 0, Category: "health"}}
	other := &memory.Entry{ID: "m-other", Text: "每周一先看周报", Layer: memory.Guideline, Subject: memory.SubjectSelf, Use: memory.UseUnderstand,
		Evidence: []memory.Evidence{{Day: "2026-11-04", Source: "chats/x", Anchor: "chats/x#1", Quote: "每周一先看周报", Speaker: memory.SpeakerUser,
			Tags: memory.Tags{Scene: memory.SceneWork, Expression: memory.ExprClaim, HoldsStance: true, Stance: memory.StanceRequirement, Support: memory.SupportDirect}}}}
	c.St.S.Memory.Put(other)
	wk, err := c.RunWeekly(ctx, "2026-W45", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wk.Report, "## 记忆") || !strings.Contains(wk.Report, "新生效的准则：每周一先看周报") ||
		!strings.Contains(wk.Report, "隐私条目 1 条") || strings.Contains(wk.Report, "冒烟测试") {
		t.Fatalf("weekly report:\n%s", wk.Report)
	}
	e.PrivateSentences = nil
	delete(c.St.S.Memory.Entries, "m-other")
	// Again: nothing changed, no second call, no second occurrence.
	calls := m.Calls["memory"]
	if _, err := c.RunDaily(ctx, "2026-11-04", false); err != nil {
		t.Fatal(err)
	}
	if m.Calls["memory"] != calls || e.Status.Count != 1 {
		t.Fatalf("idempotent: calls %d → %d, count %d", calls, m.Calls["memory"], e.Status.Count)
	}
	// Without a model the day still settles: the scores move with time.
	c.Model = nil
	if _, err := c.RunDaily(ctx, "2027-03-01", false); err != nil {
		t.Fatal(err)
	}
	if e.Status.AsOf != "2027-03-01" || e.Status.State != memory.Dormant {
		t.Fatalf("decayed without a model: %+v", e.Status)
	}
	// A forgotten source takes its memory evidence with it.
	if _, err := c.Forget("lark"); err != nil {
		t.Fatal(err)
	}
	if c.St.S.Memory.Entries[e.ID] != nil {
		t.Fatal("forgotten source: entry left")
	}
}

// A sentence whose quote is not in the cited line is thrown away with the whole conclusion.
func TestMemoryRejectsBadCitations(t *testing.T) {
	c, _ := newCurator(t, &stub.Model{}, "shadow")
	units, err := c.units(context.Background(), "2026-11-04")
	if err != nil {
		t.Fatal(err)
	}
	var u *Unit
	for i := range units {
		if units[i].Ref == "chats/lark-dm-1" {
			u = &units[i]
		}
	}
	shown := map[int]bool{}
	for _, l := range u.Lines {
		shown[l.N] = true
	}
	w := c.window(u, shown, []string{"我"})
	bad := `{"text":"x","subject":"self","layer":"guideline","evidence":[{"n":4,"quote":"这句不在原文里","scene":"work","expression":"claim","holds_stance":true,"stance":"requirement","support":"direct"}]}`
	conc := mustConclusion(t, bad)
	if _, rej := c.memoryEntry(w, u, conc); rej == "" {
		t.Fatal("bad quote accepted")
	}
	enum := strings.Replace(bad, `"scene":"work"`, `"scene":"party"`, 1)
	if _, rej := c.memoryEntry(w, u, mustConclusion(t, enum)); rej == "" {
		t.Fatal("unknown scene accepted")
	}
}

func mustConclusion(t *testing.T, s string) schema.MemoryConclusion {
	t.Helper()
	var mc schema.MemoryConclusion
	if err := json.Unmarshal([]byte(s), &mc); err != nil {
		t.Fatal(err)
	}
	return mc
}
