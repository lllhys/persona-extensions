package curate

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/stub"
)

// All texts here are placeholders (user-a, project-x): the tests check the plumbing and the gates; whether two texts
// mean the same is the model's call, played by funcModel.

// act is a placeholder action entry with one quoted line.
func act(f entry.Facet, text, ref, day, quote string) *entry.Entry {
	return &entry.Entry{Facet: f, Grain: entry.Action, Text: text, Domain: entry.Work, Scopes: []entry.Scope{{Kind: entry.ScopeGlobal}},
		Evidence: []entry.Evidence{{Source: entry.SrcChat, Ref: ref, Day: day, N: 1, Who: entry.WhoSelf, Role: "user", Quote: quote}}}
}

// funcModel answers each request with what answer returns for its step and decoded input.
type funcModel struct {
	answer func(step string, in map[string]any) any
	reqs   []model.Request
}

func (m *funcModel) Complete(_ context.Context, r model.Request) (model.Response, error) {
	m.reqs = append(m.reqs, r)
	var in map[string]any
	_ = json.Unmarshal([]byte(r.User), &in)
	b, _ := json.Marshal(m.answer(r.Step, in))
	return model.Response{Text: string(b)}, nil
}

// items lists a consolidation input's items with their candidates' texts, for a funcModel to decide on.
func items(in map[string]any) (out []map[string]any, text map[string]string) {
	text = map[string]string{}
	for _, e := range in["entries"].([]any) {
		m := e.(map[string]any)
		text[m["id"].(string)] = m["text"].(string)
	}
	for _, it := range in["items"].([]any) {
		out = append(out, it.(map[string]any))
	}
	return out, text
}

func newRunT() *store.Run { return &store.Run{ID: "t", Counts: map[string]int{}} }

func lockedCurator(t *testing.T) *Curator {
	t.Helper()
	c, _ := newCurator(t, &stub.Model{}, "review")
	c.St.Lock()
	t.Cleanup(c.St.Unlock)
	return c
}

// Synonyms are merged by the model's judgement, across languages: an English class and a Chinese action that mean
// the same are one; two new actions of one kind in a batch start one class; a new class named like an existing one
// is filed there by the hard check.
func TestSynonymsMerged(t *testing.T) {
	c := lockedCurator(t)
	ctx := context.Background()
	en := c.create(&entry.Entry{Facet: entry.Interaction, Grain: entry.Class, Text: "project-x: report the conclusion first, details after"}, "t")
	zh := c.Observe(act(entry.Interaction, "project-x：汇报先说结论再讲细节", "chats/persona-a", "2026-11-02", "quote-a"), "t")
	n1 := c.Observe(act(entry.Workflow, "user-a checks the diff before merging (1)", "chats/c1", "2026-11-02", "quote-b"), "t")
	n2 := c.Observe(act(entry.Workflow, "user-a checks the diff before merging (2)", "chats/c2", "2026-11-03", "quote-c"), "t")
	c.create(&entry.Entry{Facet: entry.Judgement, Grain: entry.Class, Text: "class-y existing"}, "t")
	n3 := c.Observe(act(entry.Judgement, "action-y", "chats/c3", "2026-11-03", "quote-d"), "t")
	m := &funcModel{answer: func(step string, in map[string]any) any {
		its, text := items(in)
		var ds []map[string]any
		for _, it := range its {
			id := it["id"].(string)
			switch in["facet"] {
			case "interaction": // the model reads the Chinese item as the English class
				for _, cand := range it["candidates"].([]any) {
					if strings.Contains(text[cand.(string)], "conclusion first") {
						ds = append(ds, map[string]any{"item": id, "action": "file", "entry": cand, "reason": "same"})
					}
				}
			case "workflow":
				ds = append(ds, map[string]any{"item": id, "action": "new", "reason": "kind", "new": map[string]any{"key": "k1", "type": "review", "text": "user-a checks the diff before merging"}})
			case "judgement": // a "new" class whose text is an existing class's
				ds = append(ds, map[string]any{"item": id, "action": "new", "reason": "kind", "new": map[string]any{"key": "k", "type": "t", "text": "class-y existing"}})
			}
		}
		return map[string]any{"decisions": ds}
	}}
	run := newRunT()
	if err := c.Consolidate(ctx, m, "2026-11-03", run); err != nil {
		t.Fatal(err)
	}
	s := c.St.S
	if s.Entries[zh].Parent != en.ID || en.Count != 1 || !slices.Contains(en.Members, zh) {
		t.Fatalf("cross-language synonym not filed: parent %q members %v count %d", s.Entries[zh].Parent, en.Members, en.Count)
	}
	p1, p2 := s.Entries[n1].Parent, s.Entries[n2].Parent
	if p1 == "" || p1 != p2 || s.Entries[p1].Count != 2 || len(s.Entries[p1].ActiveDays) != 2 || s.Entries[p1].Sessions != 2 {
		t.Fatalf("one new class for both: %q %q %+v", p1, p2, s.Entries[p1])
	}
	if s.Entries[n3].Parent != EntryID(entry.Judgement, entry.Class, "class-y existing", "") || run.Counts["consolidate_hard_merged"] != 1 {
		t.Fatalf("hard check: %q %v", s.Entries[n3].Parent, run.Counts)
	}
	// every action handled exactly once: nothing is pending
	if len(pending(s, "2026-11-03")[entry.Workflow]) != 0 || len(pending(s, "2026-11-03")[entry.Interaction]) != 0 {
		t.Error("an action is still pending")
	}
	// undo: the merge into the existing class is undone, the action stands alone and is not filed again
	if err := c.Unfile(n3, "t"); err != nil || s.Entries[n3].Parent != "" || !slices.Contains(s.Entries[n3].Flags, flagStandalone) {
		t.Fatalf("unfile: %v %+v", err, s.Entries[n3])
	}
}

// One sighting counts once per entry, session and day; the scopes of a class are the union of its members'.
func TestCountsOncePerSessionAndDay(t *testing.T) {
	c := lockedCurator(t)
	a := act(entry.Workflow, "action-z", "chats/c1", "2026-11-02", "q1")
	a.Scopes = []entry.Scope{{Kind: entry.ScopeProject, Name: "project-x"}}
	id := c.Observe(a, "t")
	again := act(entry.Workflow, "action-z", "chats/c1", "2026-11-02", "q2")
	again.Evidence[0].N = 2
	again.Scopes = []entry.Scope{{Kind: entry.ScopeProject, Name: "project-y"}}
	c.Observe(again, "t")
	third := act(entry.Workflow, "action-z", "chats/c2", "2026-11-02", "q3")
	third.Scopes = again.Scopes
	c.Observe(third, "t")
	e := c.St.S.Entries[id]
	if e.Count != 2 || e.Sessions != 2 || len(e.ActiveDays) != 1 || len(e.Scopes) != 2 {
		t.Fatalf("counts: count %d sessions %d days %v scopes %v", e.Count, e.Sessions, e.ActiveDays, e.Scopes)
	}
}

// A counter-example is kept apart: not a member, it adds a chance and not an occurrence, and enough of them lately
// make the class shifting.
func TestCounterKeptApart(t *testing.T) {
	c := lockedCurator(t)
	k := c.create(&entry.Entry{Facet: entry.Judgement, Grain: entry.Class, Text: "class-c"}, "t")
	for i, d := range []string{"2026-11-01", "2026-11-02", "2026-11-03"} {
		id := c.Observe(act(entry.Judgement, "pos-"+d, "chats/c"+d, d, "q"), "t")
		if err := c.File(id, k.ID, "t"); err != nil {
			t.Fatal(err, i)
		}
	}
	neg := c.Observe(act(entry.Judgement, "neg", "chats/n", "2026-11-04", "q"), "t")
	m := &funcModel{answer: func(step string, in map[string]any) any {
		its, _ := items(in)
		return map[string]any{"decisions": []any{map[string]any{"item": its[0]["id"], "action": "counter", "entry": its[0]["candidates"].([]any)[0], "reason": "other way"}}}
	}}
	if err := c.Consolidate(context.Background(), m, "2026-11-04", newRunT()); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(k.Members, neg) || !slices.Contains(k.Counters, neg) || k.Count != 3 || k.Chances != 4 {
		t.Fatalf("counter: members %v counters %v count %d chances %d", k.Members, k.Counters, k.Count, k.Chances)
	}
	if k.Status != entry.Shifting {
		t.Errorf("1 of 4 recent chances against: status %s", k.Status)
	}
}

// The user's own entries survive a weekly pass: their text, pin and status; the review does not demote them and
// the consolidation never merges them away.
func TestUserEditedSurvivesWeekly(t *testing.T) {
	c, _ := newCurator(t, &stub.Model{}, "review")
	c.St.Lock()
	k := c.create(&entry.Entry{Facet: entry.Workflow, Grain: entry.Class, Type: "review", Text: "class-u", Impact: entry.Impact{Moment: "task_start", Action: "x"}}, "t")
	for _, d := range []string{"2026-11-03", "2026-11-04", "2026-11-05"} {
		id := c.Observe(act(entry.Workflow, "u-"+d, "chats/u"+d, d, "q"), "t")
		_ = c.File(id, k.ID, "t")
	}
	if err := c.UserAct(k.ID, ActEdit, "user-a's own words for class-u"); err != nil {
		t.Fatal(err)
	}
	_ = c.UserAct(k.ID, ActPin, "")
	other := c.create(&entry.Entry{Facet: entry.Workflow, Grain: entry.Class, Type: "review", Text: "class-u copy"}, "t")
	if err := c.Merge(k.ID, other.ID, "test", "t"); err == nil {
		t.Fatal("the user's entry was merged away")
	}
	c.St.Unlock()
	c.Model = &funcModel{answer: func(step string, in map[string]any) any {
		switch step {
		case "review": // the review says it changes nothing: the user's entry is not even asked
			var as []any
			for _, it := range in["items"].([]any) {
				as = append(as, map[string]any{"id": it.(map[string]any)["id"], "moment": "none", "reason": "none", "holds_for_anyone": true})
			}
			return map[string]any{"answers": as}
		case "abstract":
			return map[string]any{"abstracts": []any{}}
		}
		return map[string]any{"decisions": []any{}}
	}}
	if _, err := c.RunWeekly(context.Background(), "2026-W45", false); err != nil {
		t.Fatal(err)
	}
	e := c.St.S.Entries[k.ID]
	if e.User == nil || e.User.Text != "user-a's own words for class-u" || e.Text != "class-u" || !e.Pinned || e.Status != entry.Stable || !e.Live() {
		t.Fatalf("after weekly: %+v", e)
	}
}

// A deleted entry's content is dropped when it comes again: the same text at once, and the same meaning when the
// model files a new action into the deleted class.
func TestTombstoneDrops(t *testing.T) {
	c := lockedCurator(t)
	id := c.Observe(act(entry.Persona, "persona-x", "chats/a", "2026-11-01", "q"), "t")
	if err := c.UserAct(id, ActDelete, ""); err != nil {
		t.Fatal(err)
	}
	if got := c.Observe(act(entry.Persona, "persona-x", "chats/b", "2026-11-02", "q"), "t"); got != "" {
		t.Fatalf("the same content made again: %s", got)
	}
	k := c.create(&entry.Entry{Facet: entry.Persona, Grain: entry.Class, Text: "class-d"}, "t")
	if err := c.UserAct(k.ID, ActDelete, ""); err != nil {
		t.Fatal(err)
	}
	a := c.Observe(act(entry.Persona, "persona-y says class-d again", "chats/c", "2026-11-03", "q"), "t")
	m := &funcModel{answer: func(step string, in map[string]any) any {
		its, text := items(in)
		for _, cand := range its[0]["candidates"].([]any) {
			if text[cand.(string)] == "class-d" {
				return map[string]any{"decisions": []any{map[string]any{"item": its[0]["id"], "action": "file", "entry": cand, "reason": "same"}}}
			}
		}
		return map[string]any{"decisions": []any{}}
	}}
	run := newRunT()
	if err := c.Consolidate(context.Background(), m, "2026-11-03", run); err != nil {
		t.Fatal(err)
	}
	if !c.St.S.Entries[a].Deleted || run.Counts["consolidate_tombstoned"] != 1 || slices.Contains(k.Members, a) {
		t.Fatalf("tombstoned meaning kept: %+v %v", c.St.S.Entries[a], run.Counts)
	}
}

// Replaying events.jsonl over nothing gives the same entries, after every kind of change.
func TestReplayEvents(t *testing.T) {
	c := lockedCurator(t)
	s := c.St.S
	k := c.create(&entry.Entry{Facet: entry.Judgement, Grain: entry.Class, Text: "class-r"}, "t")
	k2 := c.create(&entry.Entry{Facet: entry.Judgement, Grain: entry.Class, Text: "class-r2"}, "t")
	a := c.Observe(act(entry.Judgement, "r-a", "chats/a", "2026-11-01", "q"), "t")
	b := c.Observe(act(entry.Judgement, "r-b", "chats/b", "2026-11-02", "q"), "t")
	n := c.Observe(act(entry.Judgement, "r-n", "chats/n", "2026-11-03", "q"), "t")
	c.Observe(act(entry.Judgement, "r-a", "chats/a2", "2026-11-04", "q"), "t")
	_ = c.File(a, k.ID, "t")
	_ = c.File(b, k2.ID, "t")
	_ = c.Counter(n, k.ID, "t")
	_ = c.Merge(k2.ID, k.ID, "same", "t")
	_ = c.Unmerge(k2.ID, "t")
	w := c.Observe(&entry.Entry{Facet: entry.World, Grain: entry.Class, Text: "user-a reports_to user-b", Subject: "person:a", Predicate: "reports_to",
		Target: "person:b", Evidence: []entry.Evidence{{Ref: "chats/w", Day: "2026-11-01", Quote: "q"}}}, "t")
	w2 := c.Observe(act(entry.World, "user-a now reports to user-c", "chats/w2", "2026-11-05", "q"), "t")
	_ = c.Invalidate(w, w2, "2026-11-05", "t")
	_ = c.UserAct(a, ActEdit, "user text")
	_ = c.UserAct(b, ActPin, "")
	_ = c.UserAct(n, ActWrong, "note")
	c.settleAll("2026-11-05", newRunT())
	evs, err := c.St.Events()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReplayEntries(evs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, s.Entries) {
		for id, e := range s.Entries {
			if !reflect.DeepEqual(got[id], e) {
				x, _ := json.Marshal(got[id])
				y, _ := json.Marshal(e)
				t.Errorf("%s replayed:\n%s\nwant:\n%s", id, x, y)
			}
		}
	}
	if s.Entries[w].ValidTo != "2026-11-05" || s.Entries[w].ValidFrom != "2026-11-01" || s.Entries[w].Live() {
		t.Errorf("relation not invalidated with its times: %+v", s.Entries[w])
	}
}

// The abstract gate: all three conditions or nothing; it does not relax with little data.
func TestAbstractGate(t *testing.T) {
	c := lockedCurator(t)
	s := c.St.S
	var members []string
	types := []string{"type-a", "type-b", "type-a"}
	for i, d := range []string{"2026-11-01", "2026-11-02", "2026-11-03"} {
		k := c.create(&entry.Entry{Facet: entry.Judgement, Grain: entry.Class, Type: types[i], Text: "class-g" + d}, "t")
		id := c.Observe(act(entry.Judgement, "g-"+d, "chats/g"+d, d, "quote"), "t")
		_ = c.File(id, k.ID, "t")
		members = append(members, k.ID)
	}
	sits := map[string][]string{"situation-a": {members[0], members[2]}, "situation-b": {members[1]}}
	good := func() *entry.Entry {
		return &entry.Entry{Facet: entry.Judgement, Grain: entry.Abstract, Text: "user-a does z", Default: "a capable stranger does y",
			Members: slices.Clone(members), Situations: []string{"situation-a", "situation-b"}}
	}
	if g := AbstractGate(s, good(), sits, false); g != "" {
		t.Fatalf("all three hold, blocked: %s", g)
	}
	noDefault := good()
	noDefault.Default = ""
	if g := AbstractGate(s, noDefault, sits, false); g != gateNoDefault {
		t.Errorf("no default: %q", g)
	}
	if g := AbstractGate(s, good(), sits, true); g != gateGeneric {
		t.Errorf("holds for anyone: %q", g)
	}
	two := good()
	two.Members = members[:2]
	if g := AbstractGate(s, two, sits, false); g != gateMembers {
		t.Errorf("two members: %q", g)
	}
	oneSit := good()
	oneSit.Situations = []string{"situation-a"}
	if g := AbstractGate(s, oneSit, map[string][]string{"situation-a": members}, false); g != gateSituations {
		t.Errorf("one situation: %q", g)
	}
	sameType := good()
	for _, id := range members {
		s.Entries[id].Type = "type-a"
	}
	if g := AbstractGate(s, sameType, sits, false); g != gateSituations {
		t.Errorf("one kind among the members: %q", g)
	}
	s.Entries[members[1]].Type = "type-b"
	for _, id := range members { // all members on one active day
		for _, mid := range s.Entries[id].Members {
			s.Entries[mid].Evidence[0].Day = "2026-11-01"
		}
	}
	if g := AbstractGate(s, good(), sits, false); g != gateDays {
		t.Errorf("one active day: %q", g)
	}
}

// Through the weekly lift: a proposal the fixed question says holds for anyone is not lifted, one that passes is
// kept with its members.
func TestLiftAbstracts(t *testing.T) {
	c := lockedCurator(t)
	var ids []string
	types := []string{"type-a", "type-b", "type-c"}
	for i, d := range []string{"2026-11-01", "2026-11-02", "2026-11-03"} {
		k := c.create(&entry.Entry{Facet: entry.Workflow, Grain: entry.Class, Type: types[i], Text: "class-l" + d}, "t")
		id := c.Observe(act(entry.Workflow, "l-"+d, "chats/l"+d, d, "quote"), "t")
		_ = c.File(id, k.ID, "t")
		ids = append(ids, k.ID)
	}
	m := &funcModel{answer: func(step string, in map[string]any) any {
		if step == "abstract" {
			sit := []any{map[string]any{"name": "situation-a", "members": []any{"c1", "c2"}}, map[string]any{"name": "situation-b", "members": []any{"c3"}}}
			return map[string]any{"abstracts": []any{
				map[string]any{"text": "generic-text", "default": "stranger-default", "situations": sit, "reason": "r"},
				map[string]any{"text": "specific-text", "default": "stranger-default", "situations": sit, "reason": "r"},
			}}
		}
		var as []any
		for _, it := range in["items"].([]any) {
			x := it.(map[string]any)
			as = append(as, map[string]any{"id": x["id"], "holds_for_anyone": x["text"] == "generic-text", "reason": "r"})
		}
		return map[string]any{"answers": as}
	}}
	run := newRunT()
	if err := c.liftAbstracts(context.Background(), m, run); err != nil {
		t.Fatal(err)
	}
	if run.Counts["abstract_blocked:"+gateGeneric] != 1 || run.Counts["abstract_new"] != 1 {
		t.Fatalf("counts %v", run.Counts)
	}
	a := c.St.S.Entries[EntryID(entry.Workflow, entry.Abstract, "specific-text", "")]
	if a == nil || len(a.Members) != 3 || a.Default != "stranger-default" || len(a.Situations) != 2 {
		t.Fatalf("abstract: %+v", a)
	}
}

// Bots and bare courtesy are not relations; a relation with content is.
func TestRelationProblems(t *testing.T) {
	rel := func(text, s, p, o, quote, who, role string) *entry.Entry {
		return &entry.Entry{Facet: entry.World, Grain: entry.Class, Text: text, Subject: s, Predicate: p, Target: o,
			Evidence: []entry.Evidence{{Ref: "chats/r", Day: "2026-11-01", Quote: quote, Who: who, Role: role, Speaker: strings.SplitN(text, " ", 2)[0]}}}
	}
	cases := []struct {
		e    *entry.Entry
		want string
	}{
		{rel("发布通知机器人 owns project-x", "person:bot1", "owns", "project:x", "project-x 已发布", "person:bot1", "other"), "bot"},
		{rel("user-b owns project-x", "person:b", "owns", "project:x", "project-x 的发布", "person:b", "bot"), "bot"},
		{rel("user-b mentors user-a", "person:b", "mentors", "person:a", "好的师父", "person:a", "other"), "courtesy"},
		{rel("user-b mentors user-a", "person:b", "mentors", "person:a", "thanks boss!", "person:a", "other"), "courtesy"},
		{rel("user-b owns tool-x", "person:b", "owns", "tool-x", "我来负责 tool-x", "person:b", "other"), ""},
		{rel("user-b mentors user-a", "person:b", "mentors", "person:a", "师父，这个接口的设计我按你说的改成分页了", "person:a", "other"), ""},
	}
	for _, x := range cases {
		if got := RelationProblem(x.e); got != x.want {
			t.Errorf("%s / %q: %q, want %q", x.e.Text, x.e.Evidence[0].Quote, got, x.want)
		}
	}
	c := lockedCurator(t)
	id := c.Observe(cases[2].e, "t")
	c.cleanRelations(newRunT())
	if e := c.St.S.Entries[id]; e.Live() || !slices.Contains(e.Flags, flagRelationOff+"courtesy") {
		t.Errorf("courtesy relation kept: %+v", e)
	}
}

// The migration moves judgements, patterns, traits and rules into entries once; a second run changes nothing.
func TestMigrateIdempotent(t *testing.T) {
	c := lockedCurator(t)
	s := c.St.S
	ev := []store.Evidence{{Chat: "chats/persona-a", Day: "2026-11-01", N: 1, Speaker: "我", Role: "user", Quote: "quote-m"}}
	s.Judgements["j-1"] = &store.Judgement{ID: "j-1", Claim: "judgement-1", Trigger: "fork-1", Choice: "choice-1", Dimension: "决策", Evidence: ev, Chat: "chats/persona-a", Day: "2026-11-01", Pattern: "p-1"}
	s.Judgements["j-2"] = &store.Judgement{ID: "j-2", Claim: "judgement-2", Dimension: "沟通", Evidence: ev, Chat: "chats/persona-a", Day: "2026-11-02"}
	s.Patterns["p-1"] = &store.Pattern{ID: "p-1", Name: "pattern-1", Dimension: "决策", Definition: "pattern-1 definition", Members: []string{"j-1"}, Status: "habitual", Pinned: true}
	s.Traits["t-1"] = &store.Trait{ID: "t-1", Name: "trait-1", Definition: "trait-1 definition", Patterns: []string{"p-1"}, Status: "stable"}
	s.Rules["r-1"] = &store.Rule{ID: "r-1", Text: "rule-1 text", Scope: "global", Status: "active", Evidence: ev, Edited: true, FirstSeen: "2026-11-01", LastSeen: "2026-11-01"}
	s.Rules["r-2"] = &store.Rule{ID: "r-2", Text: "rule-2 text", Scope: "global", Status: "candidate", Evidence: ev, Deleted: true}
	rep, err := c.Migrate()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries != 6 || rep.New != 6 || rep.Backup == "" {
		t.Fatalf("first: %+v", rep)
	}
	before, _ := json.Marshal(s.Entries)
	rep2, err := c.Migrate()
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(s.Entries)
	if rep2.New != 0 || rep2.Backup != "" || string(before) != string(after) {
		t.Fatalf("second run changed something: %+v", rep2)
	}
	ids := legacyIDs(s)
	p := s.Entries[ids["p-1"]]
	if p.Grain != entry.Class || !p.Pinned || p.Status != entry.Stable || s.Entries[ids["j-1"]].Parent != p.ID {
		t.Errorf("pattern: %+v", p)
	}
	if s.Entries[ids["j-2"]].Facet != entry.Workflow || s.Entries[ids["r-1"]].User == nil || !s.Entries[ids["r-2"]].Deleted ||
		!tombstoned(s, entry.Interaction, "rule-2 text", "") || !slices.Contains(s.Entries[ids["t-1"]].Flags, "legacy_trait") {
		t.Error("judgement facet, the user's edit, deletion or the trait mark lost")
	}
	if msg, err := c.ownLocked("p-1", "unpin"); err != nil || s.Entries[ids["p-1"]].Pinned {
		t.Errorf("own on a legacy id: %q %v", msg, err)
	}
}

// ownLocked calls Own from a test that holds the lock.
func (c *Curator) ownLocked(id, action string) (string, error) {
	c.St.Unlock()
	defer c.St.Lock()
	return c.Own(id, action, "")
}

// The open type tables: the starter pack follows the first-run answer, a proposed type joins at its second use, and
// the user may rename or merge.
func TestTypes(t *testing.T) {
	c := lockedCurator(t)
	s := c.St.S
	s.Onboarding = &entry.Onboarding{Use: entry.Work}
	c.seedTypes()
	if !slices.Contains(c.typeNames(entry.Judgement), "技术选型") || slices.Contains(c.typeNames(entry.Judgement), "花钱") {
		t.Fatalf("work pack: %v", c.typeNames(entry.Judgement))
	}
	c.noteType(entry.Workflow, "type-new")
	if slices.Contains(c.typeNames(entry.Workflow), "type-new") {
		t.Fatal("joined at first use")
	}
	c.noteType(entry.Workflow, "type-new")
	if !slices.Contains(c.typeNames(entry.Workflow), "type-new") {
		t.Fatal("not joined at second use")
	}
	k := c.create(&entry.Entry{Facet: entry.Workflow, Grain: entry.Class, Type: "type-new", Text: "class-t"}, "t")
	if err := c.RenameType(entry.Workflow, "type-new", "type-renamed"); err != nil || k.Type != "type-renamed" {
		t.Fatalf("rename: %v %s", err, k.Type)
	}
	if err := c.MergeTypes(entry.Workflow, "调研", "type-renamed"); err != nil || k.Type != "调研" || slices.Contains(c.typeNames(entry.Workflow), "type-renamed") {
		t.Fatalf("merge: %v %s %v", err, k.Type, c.typeNames(entry.Workflow))
	}
}

// The weekly merge: classes the model groups as one kind of situation are merged into one, members, counts and active
// days together, the kept class takes the group's situation; a class of another person, the user's own class, and an
// id it was not shown are not merged away; Unmerge undoes it.
func TestMergeClasses(t *testing.T) {
	c := lockedCurator(t)
	s := c.St.S
	var ks []*entry.Entry
	for i, d := range []string{"2026-11-01", "2026-11-02", "2026-11-03"} {
		k := c.create(&entry.Entry{Facet: entry.Judgement, Grain: entry.Class, Type: "type-x", Situation: "event-" + d, Text: "class-m" + d}, "t")
		id := c.Observe(act(entry.Judgement, "m-"+d, "chats/m"+d, d, "q"), "t")
		_ = c.File(id, k.ID, "t")
		ks = append(ks, k)
		_ = i
	}
	other := c.create(&entry.Entry{Facet: entry.Judgement, Grain: entry.Class, Type: "type-y", Text: "class-other"}, "t")
	_ = c.UserAct(ks[2].ID, ActEdit, "the user's own words")
	m := &funcModel{answer: func(step string, in map[string]any) any {
		ids := map[string]string{}
		for _, x := range in["classes"].([]any) {
			cl := x.(map[string]any)
			ids[cl["text"].(string)] = cl["id"].(string)
		}
		return map[string]any{"kinds": []any{map[string]any{"classes": []any{ids["class-m2026-11-01"], ids["class-m2026-11-02"],
			ids["the user's own words"], "c99"}, "situation": "situation-type-x", "text": "kind-text", "reason": "same"},
			map[string]any{"classes": []any{ids["class-other"]}, "situation": "situation-type-y", "text": "t", "reason": "alone"}}}
	}}
	run := newRunT()
	if err := c.mergeClasses(context.Background(), m, run); err != nil {
		t.Fatal(err)
	}
	// the user's class is kept, the other two merged into it
	keep := ks[2]
	if run.Counts["classes_merged"] != 2 || ks[0].MergedInto != keep.ID || ks[1].MergedInto != keep.ID || keep.MergedInto != "" {
		t.Fatalf("merged: %v %q %q", run.Counts, ks[0].MergedInto, ks[1].MergedInto)
	}
	if keep.Count != 3 || len(keep.ActiveDays) != 3 || len(keep.Members) != 3 || keep.Situation != "event-2026-11-03" || keep.User == nil {
		t.Fatalf("kept: count %d days %v members %v situation %q", keep.Count, keep.ActiveDays, keep.Members, keep.Situation)
	}
	if !other.Live() || other.MergedInto != "" || other.Situation != "situation-type-y" || other.Text != "class-other" {
		t.Errorf("a class alone in its kind: %+v", other)
	}
	if err := c.Unmerge(ks[0].ID, "t"); err != nil || ks[0].MergedInto != "" || len(keep.Members) != 2 || s.Entries[ks[0].Members[0]].Parent != ks[0].ID {
		t.Fatalf("unmerge: %v %+v", err, keep.Members)
	}
}
