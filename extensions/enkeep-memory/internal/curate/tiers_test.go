package curate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/stub"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// ingestTier checks an answer for a unit of the given tier, as RunDaily would.
func ingestTier(t *testing.T, c *Curator, tier string, owner []string, lines []string, out schema.DailyOutput) (*store.Run, *store.Digest) {
	t.Helper()
	u := &Unit{Ref: "chats/g", Entity: "chats", Meta: kbsrc.Meta{ID: "g", Kind: "group", Name: "G"}, Day: "2026-11-03"}
	d := triage.Decision{Chat: u.Ref, Shown: map[int]bool{}, Tier: tier}
	for i, s := range lines {
		l := voice.Relabel(norm.Parse(3+i, s))
		u.Lines = append(u.Lines, l)
		d.Shown[l.N] = true
	}
	text, _ := json.Marshal(out)
	run := &store.Run{Counts: map[string]int{}}
	done := &store.Done{Key: "k-" + tier}
	c.ingest(c.builder(u.Day, owner), u, d, owner, c.knownEntities(context.Background()), string(text), done, run, nil)
	return run, c.St.S.Digests[u.Ref+"@"+u.Day]
}

// A background group gives entities and their structural relations only: its summary is dropped, every item and
// every other relation is refused (TIER_NOT_ALLOWED), even an item that names the user.
func TestBackgroundGivesEntitiesOnly(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	owner := []string{"用户", "Sam"}
	lines := []string{
		"10:00 Avery: Atlas 项目由 Blake 负责，下周三上线", // 3
		"10:01 Blake: @Sam 上线后你来验收",             // 4
	}
	out := schema.DailyOutput{
		Summary:  []schema.Point{{Text: "Atlas 下周三上线", Evidence: []schema.Evidence{{N: 3, Quote: "下周三上线"}}}},
		Facts:    []schema.Item{item("Sam 负责 Atlas 上线后的验收", 4, "上线后你来验收")},
		Mentions: []schema.Mention{{Surface: "Atlas", Type: "project", N: 3}, {Surface: "Blake", Type: "person", N: 3}},
		Assertions: []schema.Assertion{
			{S: "Blake", P: "owns", O: "Atlas", Evidence: []schema.Evidence{{N: 3, Quote: "Atlas 项目由 Blake 负责"}}, Confidence: "high"},
			{S: "Blake", P: "committed", O: "Atlas 上线", Evidence: []schema.Evidence{{N: 3, Quote: "下周三上线"}}, Confidence: "high"},
		},
	}
	run, dg := ingestTier(t, c, triage.TierBackground, owner, lines, out)
	if dg == nil || len(dg.Points) != 0 || len(dg.Items) != 0 {
		t.Fatalf("digest: %+v", dg)
	}
	if code := rejected(c, "Sam 负责 Atlas 上线后的验收"); code != verify.TierNotAllowed {
		t.Errorf("the item: %q", code)
	}
	if code := rejected(c, "Blake committed Atlas 上线"); code != verify.TierNotAllowed {
		t.Errorf("a commitment: %q", code)
	}
	owns := 0
	for _, a := range c.St.S.Graph.Assertions {
		if a.P == "owns" {
			owns++
		}
	}
	if owns != 1 || run.Counts["rejected_tier"] != 2 {
		t.Errorf("owns %d, counts %v", owns, run.Counts)
	}
}

// In a group the user takes part in, a fact may rest on a line that names the user, but a way of working or a piece
// of knowledge must be the user's own words: the group's consensus is not the user's practice.
func TestEngagedPracticeNeedsTheUsersWords(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	owner := []string{"用户", "Sam"}
	lines := []string{
		"10:00 Avery: @Sam 你周五前把压测报告发出来",    // 3
		"10:01 Blake: 以后我们组发版都要先跑冒烟测试 @Sam", // 4
		"10:02 我: 以后我评审都先看测试再看实现",           // 5
	}
	out := schema.DailyOutput{
		Facts:     []schema.Item{item("Sam 周五前要发压测报告", 3, "你周五前把压测报告发出来")},
		Practices: []schema.Item{item("组里发版前先跑冒烟测试", 4, "发版都要先跑冒烟测试"), item("Sam 评审先看测试再看实现", 5, "评审都先看测试再看实现")},
	}
	ingestTier(t, c, triage.TierEngaged, owner, lines, out)
	if !kept(c, "Sam 周五前要发压测报告") {
		t.Errorf("a fact naming the user: %q", rejected(c, "Sam 周五前要发压测报告"))
	}
	if code := rejected(c, "组里发版前先跑冒烟测试"); code != verify.TierNotAllowed {
		t.Errorf("the group's practice: %q", code)
	}
	if !kept(c, "Sam 评审先看测试再看实现") {
		t.Errorf("the user's own practice: %q", rejected(c, "Sam 评审先看测试再看实现"))
	}
	// The same practice from an own unit (a DM) is kept: there the tier does not restrict it.
	c2, _ := newCurator(t, nil, "shadow")
	ingestTier(t, c2, triage.TierOwn, owner, lines, out)
	if !kept(c2, "组里发版前先跑冒烟测试") {
		t.Errorf("own tier: %q", rejected(c2, "组里发版前先跑冒烟测试"))
	}
}

// Over the sample, a day with tiers has its funnel by tier; the small-day
// shortcut reads no noise day; with tiers off the funnel has none.
func TestDailyTierFunnel(t *testing.T) {
	m := &stub.Model{}
	c, _ := newCurator(t, m, "shadow")
	c.Cfg.AdmitAllBelowTokens = 1 << 30 // every day is small: the shortcut must still leave noise out
	run, err := c.RunDaily(context.Background(), "2026-11-03", false)
	if err != nil {
		t.Fatal(err)
	}
	f := c.St.S.Funnel["2026-11-03"]
	if f.Tiers[triage.TierOwn] == 0 || f.Tiers[triage.TierEngaged] == 0 || f.Tiers[triage.TierBackground] == 0 {
		t.Fatalf("tiers: %v (chats %v)", f.Tiers, f.Chats)
	}
	total := 0
	for _, n := range []string{triage.TierOwn, triage.TierEngaged, triage.TierCode, triage.TierBackground} {
		total += f.Tiers[n]
	}
	if total != f.Admitted {
		t.Errorf("tiers %v add up to %d, admitted %d", f.Tiers, total, f.Admitted)
	}
	_ = run
	for _, d := range c.St.S.Done {
		if d.Tier == "" {
			t.Errorf("a unit read with no tier: %s %s", d.Step, d.Chat)
		}
	}
	if f.Chats[triage.RNoise+"→"+triage.Drop] == 0 && f.Tiers["noise"] == 0 {
		t.Logf("the sample has no noise day (chats %v)", f.Chats)
	}
	// Tiers off: as before.
	c2, _ := newCurator(t, &stub.Model{}, "shadow")
	c2.Cfg.Tiers = false
	if _, err := c2.RunDaily(context.Background(), "2026-11-03", false); err != nil {
		t.Fatal(err)
	}
	if f := c2.St.S.Funnel["2026-11-03"]; f.Tiers != nil {
		t.Errorf("tiers off, but %v", f.Tiers)
	}
}

// A noise day, and a day with nothing intersecting with the user, stay out even on a small day.
func TestSmallDayLeavesNoise(t *testing.T) {
	dir := t.TempDir()
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "chats/feed/meta.json"), `{"id":"feed","source":"lark","kind":"group","name":"Daily feed","people":["a","b","c"]}`)
	write(filepath.Join(dir, "chats/feed/messages/2026-11-03.md"), "# 2026-11-03\n\n- 08:00 Daily News: 一\n- 09:00 Daily News: 二\n")
	write(filepath.Join(dir, "chats/team/meta.json"), `{"id":"team","source":"lark","kind":"group","name":"Team","people":["a","b"]}`)
	write(filepath.Join(dir, "chats/team/messages/2026-11-03.md"), "# 2026-11-03\n\n- 10:00 Avery: 下周灰度\n- 10:01 Blake: 好\n")
	fx, err := kbsrc.LoadFixture(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := newCurator(t, &stub.Model{}, "shadow")
	c.Src = fx
	c.Cfg.AdmitAllBelowTokens = 1 << 30
	if _, err := c.RunDaily(context.Background(), "2026-11-03", false); err != nil {
		t.Fatal(err)
	}
	f := c.St.S.Funnel["2026-11-03"]
	// The feed is noise; the strangers' chat is dropped for having nothing to do with the user (3.3), small day or not.
	if f.Chats[triage.RNoise+"→"+triage.Drop] != 1 || f.Tiers["noise"] != 1 || f.Chats[triage.RNoIntersect+"→"+triage.Drop] != 1 {
		t.Errorf("funnel: chats %v tiers %v", f.Chats, f.Tiers)
	}
}

// The owner's last message in a group is looked up over engaged_days of its earlier days: ten days ago makes the
// group the owner's (R4, engaged); forty days ago does not (background).
func TestOwnerLastWithinEngagedDays(t *testing.T) {
	dir := t.TempDir()
	write := func(path, text string) {
		t.Helper()
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	day := "- 10:00 Avery: 下周灰度\n- 10:01 Blake: 好\n"
	for id, back := range map[string]string{"recent": "2026-10-24", "old": "2026-09-24"} {
		write(filepath.Join(dir, "chats", id, "meta.json"), `{"id":"`+id+`","source":"lark","kind":"group","name":"`+id+`","people":["a","b"]}`)
		write(filepath.Join(dir, "chats", id, "messages", back+".md"), "# x\n\n- 09:00 我: 我来跟进\n")
		write(filepath.Join(dir, "chats", id, "messages", "2026-11-03.md"), "# x\n\n"+day)
	}
	fx, err := kbsrc.LoadFixture(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := newCurator(t, &stub.Model{}, "shadow")
	c.Src = fx
	units, err := c.units(context.Background(), "2026-11-03")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, u := range units {
		got[u.Meta.ID] = u.OwnerLast
	}
	if got["recent"] != 10 || got["old"] != 0 {
		t.Errorf("owner last: %v", got)
	}
}

// Code review gives the user's own judgements and practices, but no facts (a pull request's progress is the forge's
// own record) and no relations (its links are the forge's too).
func TestCodeReviewGivesNoFactsOrRelations(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	owner := []string{"用户", "Sam"}
	lines := []string{
		"10:00 我: 合并前先补上回滚的测试，以后这类改动都要有",  // 3: the user's own review comment
		"10:01 Avery: 已补，CI 绿了，@Sam 可以合了", // 4
		"10:02 我: LGTM，合了",                // 5
	}
	out := schema.DailyOutput{
		Summary:    []schema.Point{{Text: "补了回滚测试后合并", Evidence: []schema.Evidence{{N: 5, Quote: "LGTM，合了"}}}},
		Facts:      []schema.Item{item("Sam 合并了这个 PR", 5, "LGTM，合了")},
		Practices:  []schema.Item{item("Sam 要求这类改动合并前都有回滚测试", 3, "以后这类改动都要有"), item("合并前要 CI 绿", 4, "CI 绿了")},
		Mentions:   []schema.Mention{{Surface: "Avery", Type: "person", N: 4}},
		Assertions: []schema.Assertion{{S: "Avery", P: "participates", O: "回滚测试", Evidence: []schema.Evidence{{N: 4, Quote: "已补"}}, Confidence: "high"}},
	}
	run, dg := ingestTier(t, c, triage.TierCode, owner, lines, out)
	if dg == nil || len(dg.Points) != 1 {
		t.Errorf("the summary stays: %+v", dg)
	}
	if code := rejected(c, "Sam 合并了这个 PR"); code != verify.TierNotAllowed {
		t.Errorf("a progress fact: %q", code)
	}
	if !kept(c, "Sam 要求这类改动合并前都有回滚测试") {
		t.Errorf("the user's practice: %q", rejected(c, "Sam 要求这类改动合并前都有回滚测试"))
	}
	if code := rejected(c, "合并前要 CI 绿"); code != verify.TierNotAllowed {
		t.Errorf("someone else's practice: %q", code)
	}
	if len(c.St.S.Graph.Assertions) != 0 || run.Counts["rejected_tier"] != 3 {
		t.Errorf("relations %d, counts %v", len(c.St.S.Graph.Assertions), run.Counts)
	}
}
