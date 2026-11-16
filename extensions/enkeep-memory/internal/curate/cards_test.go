package curate

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/people"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// Placeholders only: person:lark-f0 and person:lark-f1 are people of the sample knowledge base; the model's answers
// are played by funcModel.

const (
	pA = "person:lark-f0"
	pB = "person:lark-f1"
)

// cardAct is a placeholder card observation about a person.
func cardAct(c *Curator, person, part, typ, text, ref, day, who string, reported bool) string {
	e := &entry.Entry{Facet: entry.People, Grain: entry.Action, Person: person, Part: part, Type: typ, Text: text, Domain: entry.Work,
		Inferred: part == entry.PartInferred, ReportedOnly: reported, Impact: entry.Impact{Moment: "kb_lookup"},
		Evidence: []entry.Evidence{{Source: entry.SrcChat, Ref: ref, Day: day, N: 1, Who: who, Quote: "quote " + text, Reported: reported}}}
	return c.Observe(e, "t")
}

// cardsAnswer answers the card step with answer(in) and every other step with nothing to do.
func cardsAnswer(answer func(in map[string]any) any) *funcModel {
	return &funcModel{answer: func(step string, in map[string]any) any {
		switch step {
		case "cards":
			return answer(in)
		case "consolidate":
			return map[string]any{"decisions": []any{}}
		case "abstract":
			return map[string]any{"abstracts": []any{}}
		case "review":
			return map[string]any{"answers": []any{}}
		case "merge":
			return map[string]any{"groups": []any{}}
		}
		return map[string]any{}
	}}
}

// ids of the card input's items by their text, and lines by their text.
func cardIDs(in map[string]any) (items, lines map[string]string) {
	items, lines = map[string]string{}, map[string]string{}
	for _, x := range in["items"].([]any) {
		m := x.(map[string]any)
		items[m["text"].(string)] = m["id"].(string)
	}
	for _, x := range in["lines"].([]any) {
		m := x.(map[string]any)
		lines[m["text"].(string)] = m["id"].(string)
	}
	return items, lines
}

func cardCurator(t *testing.T) *Curator {
	c := lockedCurator(t)
	c.Cards = PeopleCards{}
	c.St.S.Contacts[pA] = &entry.Contact{Person: pA, Name: "person-b", Ties: map[string]int{people.TieDM: 3}, Score: 9, First: "2026-11-01", Last: "2026-11-05"}
	return c
}

func peopleClasses(s *store.State, person, part string) []*entry.Entry {
	var out []*entry.Entry
	for _, id := range sortedKeys(s.Entries) {
		e := s.Entries[id]
		if e.Facet == entry.People && e.Grain == entry.Class && e.Person == person && e.Part == part && e.Live() {
			out = append(out, e)
		}
	}
	return out
}

// The card pass folds single observations into a few lines per part, keeps the parts no stronger than their items,
// and keeps the tip apart.
func TestCardPassFolds(t *testing.T) {
	c := cardCurator(t)
	a1 := cardAct(c, pA, entry.PartFact, "responsibility", "fact-1", "chats/g1", "2026-11-02", pA, false)
	a2 := cardAct(c, pA, entry.PartFact, "responsibility", "fact-2", "chats/g2", "2026-11-03", pA, false)
	a3 := cardAct(c, pA, entry.PartFact, "responsibility", "fact-3", "chats/g3", "2026-11-04", pA, false)
	a4 := cardAct(c, pA, entry.PartInferred, "style", "infer-1", "chats/g2", "2026-11-03", "person:lark-f5", true)
	m := cardsAnswer(func(in map[string]any) any {
		it, _ := cardIDs(in)
		return map[string]any{"lines": []any{
			map[string]any{"line": "", "key": "n1", "part": "fact", "dim": "responsibility", "text": "folded-fact", "items": []string{it["fact-1"], it["fact-2"], it["fact-3"]}},
			// The model calls a reading of a reported line a fact: it is kept as inferred.
			map[string]any{"line": "", "key": "n2", "part": "fact", "dim": "style", "text": "folded-infer", "items": []string{it["infer-1"]}},
		}, "skip": []string{}, "advice": "tip-x"}
	})
	run := newRunT()
	c.RunCards(context.Background(), m, "2026-11-08", run)
	facts := peopleClasses(c.St.S, pA, entry.PartFact)
	if len(facts) != 1 || facts[0].Text != "folded-fact" || len(facts[0].Members) != 3 || facts[0].Count != 3 {
		t.Fatalf("facts %+v", facts)
	}
	for _, id := range []string{a1, a2, a3} {
		if c.St.S.Entries[id].Parent != facts[0].ID {
			t.Fatalf("%s not filed", id)
		}
	}
	inf := peopleClasses(c.St.S, pA, entry.PartInferred)
	if len(inf) != 1 || !inf[0].Inferred || !inf[0].ReportedOnly || c.St.S.Entries[a4].Parent != inf[0].ID {
		t.Fatalf("inferred %+v", inf)
	}
	tips := peopleClasses(c.St.S, pA, people.PartAdvice)
	if len(tips) != 1 || tips[0].Impact.Moment != "reply_person" || !tips[0].Inferred || facts[0].Impact.Moment != "kb_lookup" {
		t.Fatalf("tip %+v", tips)
	}
	card := c.PersonCard(pA, "2026-11-08")
	if card.Advice != "tip-x" || card.Count(people.SecFacts) != 1 || card.Count(people.SecInferred) != 1 || card.Pending != 0 || card.Actions != 4 {
		t.Fatalf("card %+v", card)
	}
	if c.PersonTip(pA) != "tip-x" {
		t.Fatal("tip for the inbox")
	}
	// The input never names the person, and the user's lines are sent as lines, the new ones as items.
	if strings.Contains(m.reqs[0].User, "person-b") {
		t.Error("the person's name is sent to the model")
	}
}

// A tip that repeats an inferred line or the user's words is not kept.
func TestCardTipNoQuote(t *testing.T) {
	c := cardCurator(t)
	cardAct(c, pA, entry.PartInferred, "style", "infer-1", "chats/g2", "2026-11-03", pA, false)
	m := cardsAnswer(func(in map[string]any) any {
		it, _ := cardIDs(in)
		return map[string]any{"lines": []any{
			map[string]any{"line": "", "part": "inferred", "dim": "style", "text": "replies briefly to long questions", "items": []string{it["infer-1"]}},
		}, "skip": []string{}, "advice": "Note: replies briefly to long questions."}
	})
	run := newRunT()
	c.RunCards(context.Background(), m, "2026-11-08", run)
	if len(peopleClasses(c.St.S, pA, people.PartAdvice)) != 0 || run.Counts["cards_advice_quotes"] != 1 {
		t.Fatalf("a tip quoting an inferred line was kept: %v", run.Counts)
	}
}

// A line the user edited keeps their wording through a weekly pass; new items are still filed under it. A line the
// user deleted is not made again, however the model words it.
func TestCardUserEditSurvivesWeekly(t *testing.T) {
	c := cardCurator(t)
	ctx := context.Background()
	old := cardAct(c, pA, entry.PartFact, "role", "role-1", "chats/g1", "2026-11-02", pA, false)
	line := c.create(&entry.Entry{Facet: entry.People, Grain: entry.Class, Person: pA, Part: entry.PartFact, Type: "role", Text: "model-role",
		Flags: []string{people.FlagCard, flagNoAutoMerge}}, "t")
	if err := c.File(old, line.ID, "t"); err != nil {
		t.Fatal(err)
	}
	if err := c.UserAct(line.ID, ActEdit, "user-role"); err != nil {
		t.Fatal(err)
	}
	gone := c.create(&entry.Entry{Facet: entry.People, Grain: entry.Class, Person: pA, Part: entry.PartInferred, Type: "style", Text: "deleted-judgement"}, "t")
	if err := c.UserAct(gone.ID, ActDelete, ""); err != nil {
		t.Fatal(err)
	}
	fresh := cardAct(c, pA, entry.PartFact, "role", "role-2", "chats/g2", "2026-11-04", pA, false)
	again := cardAct(c, pA, entry.PartInferred, "style", "style-2", "chats/g3", "2026-11-05", pA, false)
	m := cardsAnswer(func(in map[string]any) any {
		it, ls := cardIDs(in)
		if ls["user-role"] == "" {
			t.Errorf("the user's wording is not what the model is shown: %v", ls)
		}
		if d, _ := json.Marshal(in["deleted"]); !strings.Contains(string(d), "deleted-judgement") {
			t.Errorf("deleted lines not shown: %s", d)
		}
		return map[string]any{"lines": []any{
			map[string]any{"line": ls["user-role"], "part": "fact", "dim": "role", "text": "model-rewording", "items": []string{it["role-2"]}},
			map[string]any{"line": "", "part": "inferred", "dim": "style", "text": "Deleted-judgement.", "items": []string{it["style-2"]}},
		}, "skip": []string{}, "advice": ""}
	})
	c.Model = m
	c.St.Unlock()
	_, err := c.RunWeekly(ctx, "2026-W45", false)
	c.St.Lock()
	if err != nil {
		t.Fatal(err)
	}
	l := c.St.S.Entries[line.ID]
	if l.Text != "model-role" || l.User == nil || l.User.Text != "user-role" || l.Shown() != "user-role" {
		t.Fatalf("the user's line was overwritten: %q / %+v", l.Text, l.User)
	}
	if c.St.S.Entries[fresh].Parent != line.ID {
		t.Fatal("new evidence not filed under the user's line")
	}
	for _, e := range peopleClasses(c.St.S, pA, entry.PartInferred) {
		t.Fatalf("a deleted judgement came back: %q", e.Text)
	}
	if !slices.Contains(c.St.S.Entries[again].Flags, flagStandalone) {
		t.Fatal("the item of a deleted judgement waits to be offered again")
	}
}

// K14 off writes nothing to the person page; K15 off writes no inferred line; the facts are pinned with where they
// come from; the whole card waits for the hub.
func TestCardWriteBackSwitches(t *testing.T) {
	setup := func(t *testing.T, sw map[string]string) *Curator {
		c := cardCurator(t)
		for k, v := range sw {
			c.St.S.Switches[k] = entry.SwitchSetting{Value: v, By: "user"}
		}
		f := cardAct(c, pA, entry.PartFact, "role", "role-1", "chats/g1", "2026-11-02", pA, false)
		i := cardAct(c, pA, entry.PartInferred, "style", "style-1", "chats/g2", "2026-11-03", pA, false)
		fl := c.create(&entry.Entry{Facet: entry.People, Grain: entry.Class, Person: pA, Part: entry.PartFact, Type: "role", Text: "fact-line"}, "t")
		il := c.create(&entry.Entry{Facet: entry.People, Grain: entry.Class, Person: pA, Part: entry.PartInferred, Inferred: true, Type: "style", Text: "inferred-line"}, "t")
		_ = c.File(f, fl.ID, "t")
		_ = c.File(i, il.ID, "t")
		c.proposePeople(context.Background(), "2026-11-08", newRunT())
		return c
	}
	props := func(c *Curator, kind string) []*store.Proposal {
		var out []*store.Proposal
		for _, p := range c.St.S.Proposals {
			if p.Kind == kind {
				out = append(out, p)
			}
		}
		return out
	}
	t.Run("on", func(t *testing.T) {
		c := setup(t, nil)
		facts, cards := props(c, "person_fact"), props(c, "person_card")
		if len(facts) != 1 || len(cards) != 1 {
			t.Fatalf("proposals %d %d", len(facts), len(cards))
		}
		var ch Change
		_ = json.Unmarshal(facts[0].Change, &ch)
		if ch.Update.Kind != "people" || ch.Update.ID != "lark-f0" || !strings.Contains(ch.PinLine, "事实：fact-line") || !strings.Contains(ch.PinLine, "chats/g1") {
			t.Fatalf("pinned fact %+v %q", ch.Update, ch.PinLine)
		}
		_ = json.Unmarshal(cards[0].Change, &ch)
		if ch.Profile == nil || len(ch.Profile.Facts) != 1 || ch.Profile.Facts[0].Text != "fact-line" || len(ch.Profile.Inferences) != 1 ||
			ch.Profile.Inferences[0].Text != "inferred-line" || ch.Update.Summary != "" || ch.Update.Log != "" {
			t.Fatalf("profile %+v", ch.Profile)
		}
		if cards[0].Status != "blocked" || cards[0].Needs != ProfileNeeds {
			t.Fatalf("the profile is written before the hub is released: %s", cards[0].Status)
		}
	})
	t.Run("K15 off", func(t *testing.T) {
		c := setup(t, map[string]string{entry.K15Inferred: "off"})
		var ch Change
		_ = json.Unmarshal(props(c, "person_card")[0].Change, &ch)
		if len(ch.Profile.Inferences) != 0 || len(ch.Profile.Facts) != 1 {
			t.Fatalf("K15 off: %+v", ch.Profile)
		}
	})
	t.Run("K14 off", func(t *testing.T) {
		c := setup(t, map[string]string{entry.K14People: "off"})
		if n := len(props(c, "person_fact")) + len(props(c, "person_card")); n != 0 {
			t.Fatalf("K14 off: %d proposals", n)
		}
		if c.PersonTip(pA) != "" {
			t.Fatal("K14 off: a tip for the inbox")
		}
	})
}

// The fact proposal goes through the proposal flow to a pinned line on the person page.
func TestCardFactApplied(t *testing.T) {
	c := cardCurator(t)
	f := cardAct(c, pA, entry.PartFact, "role", "role-1", "chats/g1", "2026-11-02", pA, false)
	fl := c.create(&entry.Entry{Facet: entry.People, Grain: entry.Class, Person: pA, Part: entry.PartFact, Type: "role", Text: "fact-line"}, "t")
	_ = c.File(f, fl.ID, "t")
	c.proposePeople(context.Background(), "2026-11-08", newRunT())
	var id string
	for _, p := range c.St.S.Proposals {
		if p.Kind == "person_fact" {
			id = p.ID
		}
	}
	c.St.Unlock()
	_, err := c.Decide(context.Background(), id, "accept", "")
	c.St.Lock()
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Src.Get(context.Background(), "people", "lark-f0")
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	_ = json.Unmarshal(d.Meta, &meta)
	if !strings.Contains(meta["pinned"].(string), "事实：fact-line") {
		t.Fatalf("pinned %v", meta["pinned"])
	}
}

// Merging two person entries is kept in the plugin: one card over both; splitting gives each its own again.
func TestCardMergeUndo(t *testing.T) {
	c := cardCurator(t)
	c.St.S.Contacts[pB] = &entry.Contact{Person: pB, Name: "person-b2", Ties: map[string]int{people.TieMail: 1}, Score: 2}
	a := cardAct(c, pA, entry.PartFact, "role", "role-a", "chats/g1", "2026-11-02", pA, false)
	b := cardAct(c, pB, entry.PartFact, "role", "role-b", "chats/m1", "2026-11-03", pB, false)
	id, err := c.MergePeople([]string{pA, pB}, "person-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.MergePeople([]string{pB, "person:lark-f2"}, ""); err == nil {
		t.Fatal("a person merged twice")
	}
	if l := c.PersonCards("2026-11-08"); len(l) != 1 || len(l[0].Persons) != 2 {
		t.Fatalf("one card for the merge: %+v", l)
	}
	m := cardsAnswer(func(in map[string]any) any {
		it, _ := cardIDs(in)
		return map[string]any{"lines": []any{
			map[string]any{"line": "", "part": "fact", "dim": "role", "text": "role-merged", "items": []string{it["role-a"], it["role-b"]}},
		}, "skip": []string{}, "advice": ""}
	})
	c.RunCards(context.Background(), m, "2026-11-08", newRunT())
	lines := peopleClasses(c.St.S, pA, entry.PartFact)
	if len(lines) != 1 || len(lines[0].Members) != 2 || c.St.S.Entries[b].Parent != lines[0].ID {
		t.Fatalf("merged card line %+v", lines)
	}
	if card := c.PersonCard(pB, "2026-11-08"); card.Person != pA || card.Count(people.SecFacts) != 1 {
		t.Fatalf("card of the other entry %+v", card)
	}
	if err := c.SplitPeople(id); err != nil {
		t.Fatal(err)
	}
	if len(c.St.S.PersonMerges) != 0 {
		t.Fatal("merge kept")
	}
	if c.St.S.Entries[b].Parent != "" || c.St.S.Entries[a].Parent != lines[0].ID || len(c.St.S.Entries[lines[0].ID].Members) != 1 {
		t.Fatalf("split: b parent %q, a parent %q", c.St.S.Entries[b].Parent, c.St.S.Entries[a].Parent)
	}
	cb := c.PersonCard(pB, "2026-11-08")
	if cb.Person != pB || cb.Pending != 1 {
		t.Fatalf("the other entry's card %+v", cb)
	}
	if ca := c.PersonCard(pA, "2026-11-08"); ca.Count(people.SecFacts) != 1 || ca.Pending != 0 {
		t.Fatalf("the first entry's card %+v", ca)
	}
}

// The list of cards on an empty state is empty, and adding a person by hand gives them a card.
func TestCardsManualAdd(t *testing.T) {
	c := lockedCurator(t)
	if len(c.PersonCards("2026-11-08")) != 0 {
		t.Fatal("cards on an empty state")
	}
	if err := c.SetContact(pB, "person-c", people.Added); err != nil {
		t.Fatal(err)
	}
	if l := c.PersonCards("2026-11-08"); len(l) != 1 || l[0].Person != pB {
		t.Fatalf("added %+v", l)
	}
	if err := c.SetContact(pB, "", people.Removed); err != nil || len(c.PersonCards("2026-11-08")) != 0 {
		t.Fatal("removed")
	}
}

// Promises either way go into the weekly report: due soon, and possibly overdue (never closed by the curator).
func TestCardPromisesInWeeklyReport(t *testing.T) {
	c := cardCurator(t)
	mk := func(from, to, what, due string) {
		e := &entry.Entry{Facet: entry.People, Grain: entry.Action, Person: pA, Part: entry.PartCommitment, Text: what, Domain: entry.Work,
			Commitment: &entry.Commitment{From: from, To: to, What: what, Due: due},
			Evidence:   []entry.Evidence{{Source: entry.SrcChat, Ref: "chats/d1", Day: "2026-11-03", N: 1, Who: from, Quote: "quote " + what}}}
		c.Observe(e, "t")
	}
	mk(pA, entry.WhoSelf, "send-x", "2026-11-10")
	mk(entry.WhoSelf, pA, "review-y", "2026-11-01")
	notes := c.peopleNotes("2026-11-08", newRunT())
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "快到期") || !strings.Contains(joined, "对方答应用户：send-x") ||
		!strings.Contains(joined, "可能逾期") || !strings.Contains(joined, "用户答应：review-y") {
		t.Fatalf("notes %q", notes)
	}
	card := c.PersonCard(pA, "2026-11-08")
	if card.Count(people.SecCommitments) != 2 {
		t.Fatalf("ledger on the card: %+v", card.Parts[people.SecCommitments])
	}
}
