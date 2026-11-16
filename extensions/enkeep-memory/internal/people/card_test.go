package people

import (
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
)

// All names and texts are placeholders (user-a, person-b, project-x).

// The list on three kinds of data: nothing at all, direct chats only, groups only.
func TestListEmptyDMOnlyGroupOnly(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		list := map[string]*entry.Contact{}
		// Units with nothing resolvable: no one is on the list, and the card list is empty.
		Observe(list, "2026-01-01", []Unit{{Entity: "chats", Kind: "group", Lines: ls("10:00 someone: hi"), Who: map[int]string{1: ""}}}, owner)
		if len(Ranked(list)) != 0 {
			t.Fatalf("list %v", keys(list))
		}
		if got := List(Input{Contacts: list, Entries: map[string]*entry.Entry{}}); len(got) != 0 {
			t.Fatalf("cards %v", got)
		}
	})
	t.Run("dm only", func(t *testing.T) {
		list := map[string]*entry.Contact{}
		names := map[string]string{"p1": "person-b", "p2": "person-c"}
		for _, day := range []string{"2026-01-01", "2026-01-02"} {
			Observe(list, day, []Unit{
				{Entity: "chats", Source: "wechat", Kind: "dm", Members: []string{"p1"}, Names: names,
					Lines: ls("09:00 我: ping", "09:05 person-b: pong"), Who: map[int]string{1: entry.WhoSelf, 2: "person:p1"}},
				{Entity: "chats", Source: "wechat", Kind: "dm", Members: []string{"p2"}, Names: names,
					Lines: ls("11:00 person-c: hello"), Who: map[int]string{1: "person:p2"}},
			}, owner)
		}
		r := Ranked(list)
		if len(r) != 2 || r[0].Ties[TieDM] != 2 || r[0].Ties[TieGroup] != 0 {
			t.Fatalf("dm only: %+v", r)
		}
		cards := List(Input{Contacts: list, Entries: map[string]*entry.Entry{}})
		if len(cards) != 2 || cards[0].Name == "" {
			t.Fatalf("cards %+v", cards)
		}
	})
	t.Run("group only", func(t *testing.T) {
		list := map[string]*entry.Contact{}
		names := map[string]string{"p1": "person-b", "p2": "person-c", "p3": "person-d"}
		Observe(list, "2026-01-01", []Unit{
			{Entity: "chats", Kind: "group", Members: []string{"p1", "p2"}, Names: names,
				Lines: ls("10:00 我: plan for project-x", "10:01 person-b: ok"), Who: map[int]string{1: entry.WhoSelf, 2: "person:p1"}},
			// A group the user is silent in, nobody mentions the user: no dealing.
			{Entity: "chats", Kind: "group", Members: []string{"p3"}, Names: names,
				Lines: ls("12:00 person-d: lunch?"), Who: map[int]string{1: "person:p3"}},
		}, owner)
		r := Ranked(list)
		if len(r) != 1 || r[0].Person != "person:p1" || r[0].Ties[TieGroup] != 1 {
			t.Fatalf("group only: %+v", r)
		}
	})
}

func TestRhythm(t *testing.T) {
	m := map[string]*Rhythm{}
	ObserveRhythm(m, "2026-01-01", []Unit{
		{Entity: "chats", Kind: "dm", Lines: ls("09:00 person-b: question", "09:30 我: answer", "09:40 person-b: thanks", "10:40 person-b: one more"),
			Who: map[int]string{1: "person:p1", 2: entry.WhoSelf, 3: "person:p1", 4: "person:p1"}},
		// The user alone: no exchange.
		{Entity: "chats", Kind: "dm", Lines: ls("09:00 我: note to self"), Who: map[int]string{1: entry.WhoSelf}},
		// A group is not counted.
		{Entity: "chats", Kind: "group", Lines: ls("09:00 我: a", "09:01 person-c: b"), Who: map[int]string{1: entry.WhoSelf, 2: "person:p2"}},
	})
	ObserveRhythm(m, "2026-01-02", []Unit{
		{Entity: "chats", Kind: "dm", Lines: ls("08:00 我: hi", "08:20 person-b: hi"), Who: map[int]string{1: entry.WhoSelf, 2: "person:p1"}},
	})
	r := m["person:p1"]
	if r == nil || r.Days != 2 || r.ByThem != 1 || r.ByUser != 1 {
		t.Fatalf("rhythm %+v", r)
	}
	if md, n := Median(r.TheirGaps); n != 2 || md != 20 {
		t.Errorf("their answers %v", r.TheirGaps)
	}
	if md, n := Median(r.UserGaps); n != 1 || md != 30 {
		t.Errorf("user answers %v", r.UserGaps)
	}
	if m["person:p2"] != nil {
		t.Error("a group counted")
	}
}

func ev(ref, day, who, quote string, reported bool) entry.Evidence {
	return entry.Evidence{Source: entry.SrcChat, Ref: ref, Day: day, N: 1, Who: who, Quote: quote, Reported: reported}
}

// cardFixture: a card with a fact line resting on the person's own lines, an inferred line resting only on what
// others said, a user evaluation, a pending action, and promises both ways.
func cardFixture() Input {
	es := map[string]*entry.Entry{
		"a1": {ID: "a1", Facet: entry.People, Grain: entry.Action, Person: "person:p1", Part: entry.PartFact, Parent: "c1", Count: 1,
			First: "2026-01-01", Last: "2026-01-01", Evidence: []entry.Evidence{ev("chats/g1", "2026-01-01", "person:p1", "quote-own-1", false)}},
		"a2": {ID: "a2", Facet: entry.People, Grain: entry.Action, Person: "person:p1", Part: entry.PartFact, Parent: "c1", Count: 1,
			First: "2026-01-02", Last: "2026-01-02", Evidence: []entry.Evidence{ev("chats/g2", "2026-01-02", "person:p1", "quote-own-2", false)}},
		"a3": {ID: "a3", Facet: entry.People, Grain: entry.Action, Person: "person:p1", Part: entry.PartFact, Parent: "c1", Count: 1,
			First: "2026-01-03", Last: "2026-01-03", Evidence: []entry.Evidence{ev("chats/g3", "2026-01-03", "person:p1", "quote-own-3", false)}},
		"c1": {ID: "c1", Facet: entry.People, Grain: entry.Class, Person: "person:p1", Part: entry.PartFact, Type: "responsibility",
			Text: "responsible for project-x releases", Members: []string{"a1", "a2", "a3"}, Count: 3, First: "2026-01-01", Last: "2026-01-03"},
		"a4": {ID: "a4", Facet: entry.People, Grain: entry.Action, Person: "person:p1", Part: entry.PartInferred, Parent: "c2", Count: 1, ReportedOnly: true,
			First: "2026-01-02", Last: "2026-01-02", Evidence: []entry.Evidence{ev("chats/g2", "2026-01-02", "person:p9", "quote-reported", true)}},
		"c2": {ID: "c2", Facet: entry.People, Grain: entry.Class, Person: "person:p1", Part: entry.PartInferred, Inferred: true, Type: "style",
			Text: "inferred-style-x", Members: []string{"a4"}, Count: 1, First: "2026-01-02", Last: "2026-01-02"},
		"a5": {ID: "a5", Facet: entry.People, Grain: entry.Action, Person: "person:p1", Part: entry.PartUserEval, Parent: "c3", Count: 1,
			First: "2026-01-02", Last: "2026-01-02", Evidence: []entry.Evidence{ev("chats/persona-1", "2026-01-02", entry.WhoSelf, "user-words-x", false)}},
		"c3": {ID: "c3", Facet: entry.People, Grain: entry.Class, Person: "person:p1", Part: entry.PartUserEval, Type: "reliability",
			Text: "user-eval-x", Members: []string{"a5"}, Count: 1, First: "2026-01-02", Last: "2026-01-02"},
		"a6": {ID: "a6", Facet: entry.People, Grain: entry.Action, Person: "person:p1", Part: entry.PartFact, Count: 1, ReportedOnly: true,
			First: "2026-01-04", Last: "2026-01-04", Text: "pending-fact", Evidence: []entry.Evidence{ev("chats/g4", "2026-01-04", "person:p8", "quote-r2", true)}},
		"k1": {ID: "k1", Facet: entry.People, Grain: entry.Action, Person: "person:p1", Part: entry.PartCommitment, Count: 1, First: "2026-01-01", Last: "2026-01-01",
			Commitment: &entry.Commitment{From: "person:p1", To: entry.WhoSelf, What: "send-x", Due: "2026-01-02"}},
		"k2": {ID: "k2", Facet: entry.People, Grain: entry.Action, Person: "person:p1", Part: entry.PartCommitment, Count: 1, First: "2026-01-03", Last: "2026-01-03",
			Commitment: &entry.Commitment{From: entry.WhoSelf, To: "person:p1", What: "review-y", Due: "2026-01-06"}},
		"t1": {ID: "t1", Facet: entry.People, Grain: entry.Class, Person: "person:p1", Part: PartAdvice, Inferred: true, Text: "tip-x"},
	}
	return Input{Entries: es, Contacts: map[string]*entry.Contact{"person:p1": {Person: "person:p1", Name: "person-b", Ties: map[string]int{TieDM: 2}, Score: 6}},
		Rhythm: map[string]*Rhythm{"person:p1": {Days: 2, ByThem: 1, ByUser: 1, TheirGaps: []int{20, 20}}},
		Due:    map[string]bool{"k2": true}, Overdue: map[string]bool{"k1": true}}
}

func line(c *Card, id string) *Line {
	for _, sec := range Sections {
		for i := range c.Parts[sec] {
			if c.Parts[sec][i].ID == id {
				return &c.Parts[sec][i]
			}
		}
	}
	return nil
}

// What others said about the person is kept apart from their own words: counted, and a line resting only on it is
// marked; a line with little evidence says so.
func TestCardReportedAndFew(t *testing.T) {
	c := BuildCard(cardFixture(), "person:p1")
	own, rep := line(c, "c1"), line(c, "c2")
	if own == nil || own.Section != SecFacts || own.ReportedOnly || own.Reported != 0 || own.Few || own.Evidence != 3 {
		t.Fatalf("own line %+v", own)
	}
	if rep == nil || rep.Section != SecInferred || !rep.ReportedOnly || rep.Reported != 1 || !rep.Few {
		t.Fatalf("reported line %+v", rep)
	}
	if p := line(c, "a6"); p == nil || !p.Pending || !p.ReportedOnly {
		t.Fatalf("pending action %+v", p)
	}
	if line(c, "a1") != nil {
		t.Fatal("an action folded into a line is shown again on its own")
	}
	if ev := line(c, "c3"); ev == nil || ev.Section != SecUserEval || len(ev.Quotes) != 1 || ev.Quotes[0].Text != "user-words-x" {
		t.Fatalf("user eval %+v", ev)
	}
	k1, k2 := line(c, "k1"), line(c, "k2")
	if k1.Direction != "they_owe" || !k1.Overdue || k2.Direction != "user_owes" || !k2.DueSoon {
		t.Fatalf("ledger %+v %+v", k1, k2)
	}
	if c.Advice != "tip-x" || c.Pending != 1 {
		t.Fatalf("advice %q pending %d", c.Advice, c.Pending)
	}
	// The tip and the evaluation are apart: the tip is not a line of any part.
	for _, sec := range Sections {
		for _, l := range c.Parts[sec] {
			if l.ID == "t1" {
				t.Fatal("the tip is a card line")
			}
		}
	}
}

func TestSwitchesOnCard(t *testing.T) {
	in := cardFixture()
	in.Entries["c1"].Sensitive = entry.SensFamily
	in.Switches = map[string]entry.SwitchSetting{entry.K15Inferred: {Value: "off"}}
	c := BuildCard(in, "person:p1")
	if line(c, "c1") != nil {
		t.Error("a sensitive category the user did not turn on is on the card")
	}
	if line(c, "c2") != nil || c.Advice != "" {
		t.Error("K15 off: inferred lines and the tip are on the card")
	}
	if line(c, "c3") == nil {
		t.Error("K15 off keeps the user's own evaluation")
	}
	in.Switches[entry.K13Sensitive+"."+entry.SensFamily] = entry.SwitchSetting{Value: "on"}
	if line(BuildCard(in, "person:p1"), "c1") == nil {
		t.Error("the user turned the category on")
	}
}

func TestRenderPage(t *testing.T) {
	c := BuildCard(cardFixture(), "person:p1")
	p := RenderPage(c, Options{Inferred: true, Today: "2026-01-03", Lang: "zh-Hans"})
	for _, want := range []string{"### 事实", "事实：responsible for project-x releases", "证据 3 条", "### 和用户的合作", "统计：私聊 2 天",
		"### 用户原话评价", "用户原话：「user-words-x」（chats/persona-1 2026-01-02 #1）", "### 推断", "推断：inferred-style-x",
		"证据少（1 条）", "只来自转述", "对方答应用户：send-x", "可能逾期", "用户答应对方：review-y", "快到期"} {
		if !strings.Contains(p.Summary, want) {
			t.Errorf("summary lacks %q:\n%s", want, p.Summary)
		}
	}
	if strings.Contains(p.Summary, "user-eval-x") || strings.Contains(p.Summary, "tip-x") || strings.Contains(p.Summary, "pending-fact") {
		t.Errorf("the evaluation is shown other than in the user's words, or the tip or a pending action is written:\n%s", p.Summary)
	}
	if len(p.Facts) != 1 || p.Facts[0].Entry != "c1" || !strings.Contains(p.Facts[0].Line, "chats/g3") {
		t.Fatalf("pinned facts %+v", p.Facts)
	}
	// K15 off: no inferred line is written.
	off := RenderPage(c, Options{Inferred: false, Today: "2026-01-03", Lang: "en"})
	if strings.Contains(off.Summary, "inferred-style-x") || off.Counts[SecInferred] != 0 {
		t.Errorf("K15 off writes inferred lines:\n%s", off.Summary)
	}
	if !strings.Contains(off.Summary, "Fact: responsible") {
		t.Errorf("English words:\n%s", off.Summary)
	}
}

func TestMergedGroupCard(t *testing.T) {
	in := cardFixture()
	in.Entries["b1"] = &entry.Entry{ID: "b1", Facet: entry.People, Grain: entry.Action, Person: "person:p2", Part: entry.PartFact, Count: 1,
		Text: "fact-from-other-entry", First: "2026-01-05", Last: "2026-01-05"}
	in.Contacts["person:p2"] = &entry.Contact{Person: "person:p2", Name: "person-b2", Ties: map[string]int{TieMail: 1}, Score: 2}
	in.Merges = []entry.PersonMerge{{ID: "M-1", Persons: []string{"person:p1", "person:p2"}, Name: "person-b"}}
	c := BuildCard(in, "person:p2")
	if c.Person != "person:p1" || len(c.Persons) != 2 || line(c, "b1") == nil || line(c, "c1") == nil {
		t.Fatalf("merged card %+v", c)
	}
	if c.Contact.Ties[TieDM] != 2 || c.Contact.Ties[TieMail] != 1 {
		t.Errorf("ties summed %+v", c.Contact.Ties)
	}
	if l := List(in); len(l) != 1 || l[0].Merge != "M-1" {
		t.Fatalf("merged group listed once: %+v", l)
	}
}
