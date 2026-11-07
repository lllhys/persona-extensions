package memory

import (
	"math"
	"strings"
	"testing"
)

var P = DefaultParams()

// say is one sentence of the user's, in a session, on a day.
func say(day, src, quote string, t Tags) Evidence {
	if t.Support == "" {
		t.Support = SupportDirect
	}
	return Evidence{Day: day, Source: src, Anchor: src + "#" + day, Quote: quote, Speaker: SpeakerUser, Tags: t}
}

var (
	correctClaim  = Tags{Scene: SceneCorrect, Expression: ExprClaim, HoldsStance: true, Stance: StanceRequirement}
	correctStated = Tags{Scene: SceneCorrect, Expression: ExprClaim, HoldsStance: true, Stance: StancePosition}
	workClaim     = Tags{Scene: SceneWork, Expression: ExprClaim, HoldsStance: true, Stance: StanceRequirement}
	workSuggest   = Tags{Scene: SceneWork, Expression: ExprSuggest, HoldsStance: true, Stance: StanceRequirement}
	assignClaim   = Tags{Scene: SceneAssign, Expression: ExprClaim, HoldsStance: true, Stance: StanceRequirement}
	chatClaim     = Tags{Scene: SceneChat, Expression: ExprClaim, HoldsStance: true, Stance: StanceRequirement, DescriptiveValue: "习惯"}
)

func guideline(id string, ev ...Evidence) *Entry {
	return &Entry{ID: id, Text: id, Layer: Guideline, Subject: SubjectAI, Use: UseFollow, ThingType: "判断与验证",
		LifeAreas: []string{"AI 与个人系统"}, Evidence: ev}
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.11 }

func TestRuleDMatrix(t *testing.T) {
	cases := []struct {
		name  string
		tags  Tags
		band  Band
		ruleB bool
		route Route
	}{
		{"correction with a requirement", correctClaim, BandHigh, true, ""},
		{"correction stated, with a position", correctStated, BandHigh, true, ""},
		{"correction stating a plain fact", Tags{Scene: SceneCorrect, Expression: ExprClaim, HoldsStance: true, Stance: StanceNone}, BandHigh, false, ""},
		{"correction rhetorical question", Tags{Scene: SceneCorrect, Expression: ExprQuestion, HoldsStance: true, Stance: StancePosition}, BandHigh, true, ""},
		{"correction question not meant", Tags{Scene: SceneCorrect, Expression: ExprQuestion}, BandNone, false, ""},
		{"correction venting, nothing asked", Tags{Scene: SceneCorrect, Expression: ExprVent, HoldsStance: true, Stance: StanceNone}, BandHigh, false, ""},
		{"correction joke with attitude", Tags{Scene: SceneCorrect, Expression: ExprJoke, Attitude: "不满"}, BandMid, false, ""},
		{"correction aside", Tags{Scene: SceneCorrect, Expression: ExprAside}, BandLow, false, ""},
		{"review claim with stance", Tags{Scene: SceneReview, Expression: ExprClaim, HoldsStance: true, Stance: StanceRequirement}, BandHigh, true, ""},
		{"work claim without stance", Tags{Scene: SceneWork, Expression: ExprClaim, HoldsStance: true, Stance: StanceNone}, BandMid, false, ""},
		{"work suggestion", workSuggest, BandMid, false, ""},
		{"work proposal question", Tags{Scene: SceneWork, Expression: ExprQuestion, ProposalQuestion: true}, BandNone, false, RouteIntent},
		{"work information question", Tags{Scene: SceneWork, Expression: ExprQuestion}, BandNone, false, ""},
		{"work vent", Tags{Scene: SceneWork, Expression: ExprVent, HoldsStance: true, Stance: StanceNone}, BandMid, false, ""},
		{"work joke", Tags{Scene: SceneWork, Expression: ExprJoke, Attitude: "烦"}, BandNone, false, ""},
		{"assignment, one-off", assignClaim, BandMid, false, ""},
		{"assignment, standing rule", Tags{Scene: SceneAssign, Expression: ExprClaim, HoldsStance: true, Stance: StanceRequirement, LongTerm: true}, BandHigh, true, ""},
		{"assignment vent", Tags{Scene: SceneAssign, Expression: ExprVent}, BandLow, false, ""},
		{"small talk claim", chatClaim, BandNone, false, RouteDescribe},
		{"small talk proposal question", Tags{Scene: SceneChat, Expression: ExprQuestion, ProposalQuestion: true}, BandNone, false, RouteIntent},
		{"does not support", Tags{Scene: SceneCorrect, Expression: ExprClaim, HoldsStance: true, Stance: StanceRequirement, Support: SupportNone}, BandNone, false, ""},
		{"partial support counts", Tags{Scene: SceneCorrect, Expression: ExprClaim, HoldsStance: true, Stance: StanceRequirement, Support: SupportPartial}, BandHigh, true, ""},
	}
	for _, c := range cases {
		if c.tags.Support == "" {
			c.tags.Support = SupportDirect
		}
		g := GradeSentence(TargetLayers, SpeakerUser, c.tags)
		if g.Band != c.band || g.RuleB != c.ruleB || g.Route != c.route {
			t.Errorf("%s: got %+v, want band %q rule B %v route %q", c.name, g, c.band, c.ruleB, c.route)
		}
	}
	// Other speakers, AI summaries and old imports are the low band for conclusions; facts have their own starts.
	for sp, want := range map[Speaker][2]Band{SpeakerOther: {BandLow, BandFactQuote}, SpeakerAI: {BandLow, BandFactAI}, SpeakerLegacy: {BandLow, BandFactLegacy}} {
		if g := GradeSentence(TargetLayers, sp, Tags{}); g.Band != want[0] {
			t.Errorf("%s for layers: %q", sp, g.Band)
		}
		if g := GradeSentence(TargetFact, sp, Tags{}); g.Band != want[1] {
			t.Errorf("%s for facts: %q", sp, g.Band)
		}
	}
	// Facts: stated anywhere (small talk too); a question or another matter is an AI summary; a joke's words nothing.
	for _, c := range []struct {
		tags Tags
		band Band
	}{
		{Tags{Scene: SceneChat, Expression: ExprClaim, Support: SupportDirect}, BandFactQuote},
		{Tags{Scene: SceneWork, Expression: ExprClaim, Support: SupportNone}, BandFactAI},
		{Tags{Scene: SceneWork, Expression: ExprQuestion, Support: SupportPartial}, BandFactAI},
		{Tags{Scene: SceneWork, Expression: ExprQuestion, ProposalQuestion: true, Support: SupportDirect}, BandIntent},
		{Tags{Scene: SceneChat, Expression: ExprJoke, Support: SupportDirect}, BandNone},
	} {
		if g := GradeSentence(TargetFact, SpeakerUser, c.tags); g.Band != c.band {
			t.Errorf("fact %+v: %q, want %q", c.tags, g.Band, c.band)
		}
	}
	// Descriptive attributes: any setting; an aside is low; a joke counts by its attitude.
	for _, c := range []struct {
		tags Tags
		band Band
	}{
		{chatClaim, BandMid},
		{Tags{Scene: SceneChat, Expression: ExprAside, Support: SupportDirect}, BandLow},
		{Tags{Scene: SceneChat, Expression: ExprJoke, Attitude: "自嘲", Support: SupportDirect}, BandMid},
		{Tags{Scene: SceneChat, Expression: ExprJoke, Support: SupportDirect}, BandNone},
		{Tags{Scene: SceneChat, Expression: ExprClaim, Support: SupportNone}, BandNone},
	} {
		if g := GradeSentence(TargetDescriptive, SpeakerUser, c.tags); g.Band != c.band {
			t.Errorf("descriptive %+v: %q, want %q", c.tags, g.Band, c.band)
		}
	}
}

func TestRuleBByStanceNotWording(t *testing.T) {
	// A correction stated, not phrased with 应该 / 不要: a guideline at once, like any other high source with a stance.
	e := guideline("g", say("2026-10-30", "s1", "我是在和你讨论我的判断，不是改你的结论", correctStated))
	st := Evaluate(e, "2026-10-30", P)
	if st.Layer != Guideline || st.State != Active || !st.RuleB || st.RuleBBy != "sentence" || st.Score != 60 {
		t.Fatalf("stated correction: %+v", st)
	}
	// Venting that asks for nothing: high, but it alone cannot set up a guideline.
	v := guideline("v", say("2026-10-30", "s1", "太难用了", Tags{Scene: SceneCorrect, Expression: ExprVent, HoldsStance: true, Stance: StanceNone}))
	st = Evaluate(v, "2026-10-30", P)
	if st.Layer != Experience || st.RuleB || st.Score != 60 || st.State != Active {
		t.Fatalf("venting: %+v", st)
	}
	// A suggestion repeated in a second week satisfies rule B.
	s := guideline("s", say("2026-10-05", "s1", "可以试试", workSuggest), say("2026-10-14", "s2", "可以试试", workSuggest))
	st = Evaluate(s, "2026-10-14", P)
	if st.Layer != Guideline || st.RuleBBy != "weeks" {
		t.Fatalf("suggestion in two weeks: %+v", st)
	}
}

func TestAccumulationIndependenceAndWeeks(t *testing.T) {
	mid := Tags{Scene: SceneWork, Expression: ExprClaim, HoldsStance: true, Stance: StanceNone}
	// One mid occurrence: 35, a candidate.
	e := guideline("a", say("2026-10-05", "s1", "x", mid))
	if st := Evaluate(e, "2026-10-05", P); st.Score != 35 || st.State != Candidate {
		t.Fatalf("one mid: %+v", st)
	}
	// The same session the same day, said three times: still one occurrence.
	e.Evidence = append(e.Evidence, say("2026-10-05", "s1", "y", mid), say("2026-10-05", "s1", "z", mid))
	if st := Evaluate(e, "2026-10-05", P); st.Count != 1 || st.Score != 35 {
		t.Fatalf("same session same day: %+v", st)
	}
	// The same words copied into another session the same day: still one.
	e.Evidence = append(e.Evidence, say("2026-10-05", "s2", "x", mid))
	if st := Evaluate(e, "2026-10-05", P); st.Count != 1 {
		t.Fatalf("copied into another session: %+v", st)
	}
	// Another day in the same week: k = 0.5 → 35 + 35 × 0.5 × 0.65.
	same := guideline("b", say("2026-10-05", "s1", "x", mid), say("2026-10-06", "s1", "x2", mid))
	st := Evaluate(same, "2026-10-06", P)
	if !near(st.Score, 46.4) || st.State != Candidate || st.Count != 2 {
		t.Fatalf("same week: %+v", st)
	}
	// The next week instead: k = 1 → 57.75, and two weeks make a mid-only entry active.
	other := guideline("c", say("2026-10-05", "s1", "x", mid), say("2026-10-12", "s1", "x2", mid))
	st = Evaluate(other, "2026-10-12", P)
	if !near(st.Score, 57.8) || st.State != Active || len(st.Weeks) != 2 {
		t.Fatalf("other week: %+v", st)
	}
	// The formula never reaches 100.
	var ev []Evidence
	for i := 0; i < 5; i++ {
		ev = append(ev, say(addDay("2026-01-05", 7*i), "s", "x", correctClaim))
	}
	if st := Evaluate(guideline("d", ev...), addDay("2026-01-05", 7*4), P); st.Score != 99 { // 100 × (1 − 0.4⁵)
		t.Fatalf("score reached %v", st.Score)
	}
}

func TestDecayGraceHalfLifeAndDormancy(t *testing.T) {
	e := guideline("a", say("2026-01-05", "s1", "x", correctClaim))
	if st := Evaluate(e, "2026-01-12", P); st.Score != 60 {
		t.Fatalf("grace: %+v", st)
	}
	// One week seen: half-life 28 days; 60 → 30 four weeks after the grace.
	if st := Evaluate(e, addDay("2026-01-12", 28), P); !near(st.Score, 30) || st.State != Active {
		t.Fatalf("half-life: %+v", st)
	}
	// Below 15 (two half-lives after the grace, about nine weeks): dormant.
	if st := Evaluate(e, addDay("2026-01-12", 57), P); st.State != Dormant {
		t.Fatalf("dormant: %+v", st)
	}
	// Dormant, then new evidence: a candidate again from the current score, and active once it is over the line.
	e.Evidence = append(e.Evidence, say("2026-04-06", "s2", "x again", correctClaim))
	st := Evaluate(e, "2026-04-06", P)
	if st.State != Active || st.Score < 60 {
		t.Fatalf("back: %+v", st)
	}
	// Spread over three weeks: half-life 56 days.
	spread := guideline("b", say("2026-01-05", "s", "1", correctClaim), say("2026-01-12", "s", "2", correctClaim), say("2026-01-19", "s", "3", correctClaim))
	s0 := Evaluate(spread, "2026-01-26", P).Score
	if s := Evaluate(spread, addDay("2026-01-26", 56), P).Score; !near(s, s0/2) {
		t.Fatalf("three weeks: %v → %v", s0, s)
	}
	// The half-life stops at 84 days.
	if halfLife(20, P) != 84 {
		t.Fatal("max half-life")
	}
	// Facts do not decay.
	f := &Entry{ID: "f", Layer: Fact, FactCategory: Decision, Subject: SubjectSelf, Use: UseReference,
		Evidence: []Evidence{say("2026-01-05", "s", "定了", Tags{Scene: SceneWork, Expression: ExprClaim})}}
	if st := Evaluate(f, "2027-01-05", P); st.Score != 50 || st.State != Active {
		t.Fatalf("fact: %+v", st)
	}
}

func TestFloorForDontAndHardRules(t *testing.T) {
	e := guideline("a", say("2026-01-05", "s1", "不要用代号", correctClaim))
	e.Prohibition = true
	st := Evaluate(e, "2027-01-05", P)
	if st.State != Active || st.Score != 50 || !st.Floor {
		t.Fatalf("a don't rule stops at the line: %+v", st)
	}
	h := guideline("h", say("2026-01-05", "s1", "必须先查证", correctClaim))
	h.Hard = true
	if st := Evaluate(h, "2027-01-05", P); st.Score != 50 || st.State != Active {
		t.Fatalf("hard rule: %+v", st)
	}
	// Not for a candidate that never became active.
	c := guideline("c", say("2026-01-05", "s1", "别这样", Tags{Scene: SceneWork, Expression: ExprClaim, HoldsStance: true, Stance: StanceNone}))
	c.Prohibition = true
	if st := Evaluate(c, "2026-06-05", P); st.State != Dormant {
		t.Fatalf("candidate decays: %+v", st)
	}
	// A rejection still voids it.
	e.Events = append(e.Events, EntryEvent{Day: "2026-06-01", Kind: EvReject})
	if st := Evaluate(e, "2026-06-01", P); st.State != Obsolete || st.Score != 0 {
		t.Fatalf("rejected: %+v", st)
	}
}

func TestConfirmAcceleratesRejectVetoes(t *testing.T) {
	mid := Tags{Scene: SceneWork, Expression: ExprSuggest, HoldsStance: true, Stance: StanceRequirement}
	e := guideline("a", say("2026-10-05", "s1", "可以试试先报上", mid))
	e.Events = []EntryEvent{{Day: "2026-10-06", Kind: EvConfirm}}
	st := Evaluate(e, "2026-10-06", P)
	if st.State != Active || st.Layer != Guideline || st.Score < 50 || st.RuleBBy != "confirm" {
		t.Fatalf("confirmed: %+v", st)
	}
	// A principle confirmed is active at once, without weeks or settings.
	p := &Entry{ID: "p", Layer: Principle, Subject: SubjectSelf, Use: UseUnderstand, Evidence: []Evidence{say("2026-10-05", "s", "先论证再动手", workClaim)}}
	p.Events = []EntryEvent{{Day: "2026-10-05", Kind: EvConfirm}}
	if st := Evaluate(p, "2026-10-05", P); st.Layer != Principle || st.State != Active || st.Score < 70 {
		t.Fatalf("confirmed principle: %+v", st)
	}
	// Rejected: zero, obsolete; what comes later is counted and ignored.
	r := guideline("r", say("2026-10-05", "s1", "x", correctClaim))
	r.Events = []EntryEvent{{Day: "2026-10-06", Kind: EvReject}}
	r.Evidence = append(r.Evidence, say("2026-10-08", "s2", "x", correctClaim))
	st = Evaluate(r, "2026-10-08", P)
	if st.State != Obsolete || st.Score != 0 || !st.Vetoed || st.VetoHits != 1 {
		t.Fatalf("rejected: %+v", st)
	}
	// Only the user saying it again (a confirmation) lifts the veto.
	r.Events = append(r.Events, EntryEvent{Day: "2026-10-09", Kind: EvConfirm})
	if st := Evaluate(r, "2026-10-09", P); st.State != Active || st.Vetoed {
		t.Fatalf("lifted: %+v", st)
	}
}

func TestContradictionNewerAndStrongerWins(t *testing.T) {
	// The newer is a correction (60) against a mid-only older one (35): it wins.
	b2 := NewBook()
	o := guideline("o", say("2026-10-05", "s1", "x", Tags{Scene: SceneWork, Expression: ExprClaim, HoldsStance: true, Stance: StanceNone}))
	n := guideline("n", say("2026-10-12", "s2", "y", correctClaim))
	b2.Put(o)
	b2.Put(n)
	b2.Contradict("o", "n")
	b2.Tick("2026-10-12", P)
	if !o.Status.Lost || n.Status.State != Active {
		t.Fatalf("newer and stronger wins: old %+v new %+v", o.Status, n.Status)
	}
	in := Inject(b2, P)
	for _, x := range in {
		if x.ID == "o" && x.Level != None {
			t.Fatalf("the loser is not injected: %+v", x)
		}
	}
	// The loser decays without grace and four times faster, and is obsolete under the dormant line.
	b2.Tick("2026-10-26", P)
	if o.Status.State != Obsolete {
		t.Fatalf("loser obsolete: %+v", o.Status)
	}
	// The newer side not ahead yet: held at candidate, even over the line.
	b3 := NewBook()
	o3 := guideline("o", say("2026-10-05", "s1", "x", correctClaim), say("2026-10-12", "s1", "x", correctClaim))
	n3 := guideline("n", say("2026-10-13", "s2", "y", correctClaim))
	b3.Put(o3)
	b3.Put(n3)
	b3.Contradict("o", "n")
	b3.Tick("2026-10-13", P)
	if o3.Status.Lost || n3.Status.State != Candidate || !n3.Status.Held {
		t.Fatalf("held: old %+v new %+v", o3.Status, n3.Status)
	}
	// Facts: a tie goes to the newer, and the older is replaced at once.
	b4 := NewBook()
	fo := &Entry{ID: "fo", Layer: Fact, FactCategory: Attribute, Subject: SubjectSelf, Evidence: []Evidence{say("2026-10-05", "s", "12 月中旬回", Tags{Scene: SceneChat, Expression: ExprClaim})}}
	fn := &Entry{ID: "fn", Layer: Fact, FactCategory: Attribute, Subject: SubjectSelf, Evidence: []Evidence{say("2026-10-20", "s", "12 月底回", Tags{Scene: SceneChat, Expression: ExprClaim})}}
	b4.Put(fo)
	b4.Put(fn)
	b4.Contradict("fo", "fn")
	b4.Tick("2026-10-20", P)
	if fo.Status.State != Obsolete || fn.Status.State != Active {
		t.Fatalf("fact replaced: old %+v new %+v", fo.Status, fn.Status)
	}
}

func TestActivationLines(t *testing.T) {
	// Facts: one quote; an AI summary needs three occurrences.
	ai := &Entry{ID: "f", Layer: Fact, FactCategory: Knowledge, Subject: SubjectSelf}
	for i, d := range []string{"2026-10-05", "2026-10-12", "2026-10-19"} {
		ai.Evidence = append(ai.Evidence, Evidence{Day: d, Source: "mem", Anchor: d, Speaker: SpeakerAI})
		st := Evaluate(ai, d, P)
		if (i < 2) != (st.State == Candidate) {
			t.Fatalf("AI summary, %d: %+v", i+1, st)
		}
	}
	// A descriptive attribute needs two weeks even when over the line.
	d := &Entry{ID: "d", Layer: Fact, Descriptive: true, FactCategory: Attribute, Subject: SubjectSelf, Use: UseUnderstand,
		Evidence: []Evidence{say("2026-10-05", "s", "a", chatClaim), say("2026-10-06", "s2", "b", chatClaim), say("2026-10-07", "s", "c", chatClaim)}}
	if st := Evaluate(d, "2026-10-07", P); st.State != Candidate || st.Score < 50 {
		t.Fatalf("descriptive, one week: %+v", st)
	}
	d.Evidence = append(d.Evidence, say("2026-10-13", "s", "c", chatClaim))
	if st := Evaluate(d, "2026-10-13", P); st.State != Active {
		t.Fatalf("descriptive, two weeks: %+v", st)
	}
	// A principle: 70, three weeks, two settings, a user sentence with rule B. Short of that it acts as a guideline.
	p := &Entry{ID: "p", Layer: Principle, Subject: SubjectSelf, Use: UseUnderstand}
	p.Evidence = []Evidence{say("2026-10-05", "s", "先查证", correctClaim), say("2026-10-12", "s", "先查证", correctClaim)}
	if st := Evaluate(p, "2026-10-12", P); st.Layer != Guideline || st.State != Active {
		t.Fatalf("principle short of weeks: %+v", st)
	}
	review := Tags{Scene: SceneReview, Expression: ExprClaim, HoldsStance: true, Stance: StanceRequirement, ThingType: "决策与权衡"}
	p.Evidence = append(p.Evidence, say("2026-10-19", "r", "先查证再拍板", review))
	if st := Evaluate(p, "2026-10-19", P); st.Layer != Principle || st.State != Active || st.Score < 70 {
		t.Fatalf("principle: %+v", st)
	}
	one := &Entry{ID: "p1", Layer: Principle, Subject: SubjectSelf, Use: UseUnderstand,
		Evidence: []Evidence{say("2026-10-05", "s", "a", correctClaim), say("2026-10-12", "s", "b", correctClaim), say("2026-10-19", "s", "c", correctClaim)}}
	if st := Evaluate(one, "2026-10-19", P); st.Layer != Guideline {
		t.Fatalf("principle in one setting only: %+v", st)
	}
}

func TestOneOffAndConversions(t *testing.T) {
	// An instruction for one case: an experience, until the same meaning comes again on another occasion.
	e := guideline("a", say("2026-10-05", "s1", "这次先不落地，好好设计", assignClaim))
	if st := Evaluate(e, "2026-10-05", P); st.Layer != Experience {
		t.Fatalf("one-off: %+v", st)
	}
	e.Evidence = append(e.Evidence, say("2026-10-14", "s2", "先别落地，先设计", assignClaim))
	if st := Evaluate(e, "2026-10-14", P); st.Layer != Guideline || st.RuleBBy != "again" {
		t.Fatalf("one-off again: %+v", st)
	}
	// Everything the user said about it was small talk: a descriptive attribute if it says who the user is.
	d := &Entry{ID: "d", Layer: Guideline, Subject: SubjectSelf, Use: UseUnderstand, Evidence: []Evidence{say("2026-10-05", "s", "周末就该睡到自然醒", chatClaim)}}
	if st := Evaluate(d, "2026-10-05", P); !st.Descriptive || st.Layer != Fact || st.Score != 35 {
		t.Fatalf("small talk → descriptive: %+v", st)
	}
	// …and nothing at all if it does not.
	n := &Entry{ID: "n", Layer: Guideline, Subject: SubjectSelf, Evidence: []Evidence{say("2026-10-05", "s", "哈哈", Tags{Scene: SceneChat, Expression: ExprAside})}}
	if st := Evaluate(n, "2026-10-05", P); st.State != Dropped {
		t.Fatalf("small talk without value: %+v", st)
	}
	// A requirement on the AI said only in small talk: an experience, its sentences at the low band.
	a := guideline("ai", say("2026-10-05", "s", "你以后别这么啰嗦", chatClaim))
	if st := Evaluate(a, "2026-10-05", P); st.Layer != Experience || st.Score != 15 {
		t.Fatalf("AI requirement in small talk: %+v", st)
	}
	// Only proposal questions: an intent (a fact, 35, retrieval only).
	q := &Entry{ID: "q", Layer: Guideline, Subject: SubjectSelf, Evidence: []Evidence{say("2026-10-05", "s", "是不是可以先灰度一周？", Tags{Scene: SceneWork, Expression: ExprQuestion, ProposalQuestion: true})}}
	if st := Evaluate(q, "2026-10-05", P); st.Layer != Fact || st.Category != Intent || st.Score != 35 || st.State != Candidate {
		t.Fatalf("intent: %+v", st)
	}
	// Team rules and other people's views are facts.
	tm := &Entry{ID: "t", Layer: Guideline, Subject: SubjectTeam, Evidence: []Evidence{{Day: "2026-10-05", Source: "s", Quote: "上线前要过评审", Speaker: SpeakerOther}}}
	if st := Evaluate(tm, "2026-10-05", P); st.Layer != Fact || st.Score != 50 {
		t.Fatalf("team rule: %+v", st)
	}
	// Every sentence fails to support it: registered once at the low band.
	u := guideline("u", say("2026-10-05", "s", "政治只是一部分，对吧？", Tags{Scene: SceneCorrect, Expression: ExprQuestion, HoldsStance: true, Support: SupportNone}))
	if st := Evaluate(u, "2026-10-05", P); st.Score != 15 || st.Count != 1 {
		t.Fatalf("unsupported: %+v", st)
	}
}

func TestFollowUpBackfill(t *testing.T) {
	sug := say("2026-10-05", "grp", "下次可以先灰度", workSuggest)
	// Stated as done: the serious band less what it got (60 − 35), no occurrence, no new week; rule B.
	e := guideline("a", sug)
	e.Events = []EntryEvent{{Day: "2026-10-09", Kind: EvFollowUp, Follow: FollowStated, Quote: "已经按灰度上了"}}
	st := Evaluate(e, "2026-10-09", P)
	if !near(st.Score, 35+25*0.65) || st.Count != 1 || len(st.Weeks) != 1 || st.Layer != Guideline || st.FollowUps != 1 || st.RuleBBy != "follow_up" {
		t.Fatalf("stated: %+v", st)
	}
	// Seen in a work product counts the same.
	a := guideline("b", sug)
	a.Events = []EntryEvent{{Day: "2026-10-09", Kind: EvFollowUp, Follow: FollowArtifact, Anchor: "docs/runbook#3"}}
	if st := Evaluate(a, "2026-10-09", P); st.FollowUps != 1 {
		t.Fatalf("artifact: %+v", st)
	}
	// Words of agreement do not.
	v := guideline("c", sug)
	v.Events = []EntryEvent{{Day: "2026-10-09", Kind: EvFollowUp, Follow: FollowVerbal, Quote: "合理"}}
	if st := Evaluate(v, "2026-10-09", P); st.Score != 35 || st.FollowUps != 0 || st.VerbalOnly != 1 || st.Layer != Experience {
		t.Fatalf("verbal: %+v", st)
	}
	// A follow-through before the suggestion is not one.
	early := guideline("d", sug)
	early.Events = []EntryEvent{{Day: "2026-10-01", Kind: EvFollowUp, Follow: FollowStated}}
	if st := Evaluate(early, "2026-10-09", P); st.FollowUps != 0 {
		t.Fatalf("before: %+v", st)
	}
	// A one-off assignment carried out: the score only, still an experience.
	o := guideline("e", say("2026-10-05", "dm", "你先跑一下", assignClaim))
	o.Events = []EntryEvent{{Day: "2026-10-06", Kind: EvFollowUp, Follow: FollowStated}}
	if st := Evaluate(o, "2026-10-06", P); st.FollowUps != 1 || st.Layer != Experience {
		t.Fatalf("one-off carried out: %+v", st)
	}
	// Waiting for a follow-through within the window.
	if st := Evaluate(guideline("f", sug), "2026-10-20", P); !st.Awaiting {
		t.Fatalf("awaiting: %+v", st)
	}
	if st := Evaluate(guideline("g", sug), "2026-12-20", P); st.Awaiting {
		t.Fatalf("not awaiting after the window: %+v", st)
	}
}

func TestEnkeepImport(t *testing.T) {
	b := NewBook()
	vent := &Tags{Scene: SceneCorrect, Expression: ExprVent, HoldsStance: true, Stance: StanceNone, Support: SupportDirect}
	recs := []EnkeepRecord{
		// Confirmed rule whose evidence is only venting: still a guideline (the migration exemption).
		{Table: "rules", ID: "R1", Status: "confirmed", Text: "不要编数据", Entry: Entry{Layer: Guideline, Subject: SubjectAI, Use: UseFollow, Prohibition: true},
			Events: []EnkeepEvent{{Day: "2026-03-02", Workspace: "w", Kind: "violate", Quote: "又编", Tags: vent}}},
		// Retired pattern: obsolete, and enkeep's later confirmation does not bring it back.
		{Table: "patterns", ID: "P1", Status: "retired", Text: "习惯 x",
			Events: []EnkeepEvent{{Day: "2026-03-02", Workspace: "w", Kind: "new", Quote: "x"}, {Day: "2026-10-21", Workspace: "w", Kind: "user_confirm"}}},
		// Dormant rule: dormant.
		{Table: "rules", ID: "R2", Status: "dormant", Text: "y", Entry: Entry{Layer: Guideline, Subject: SubjectAI, Use: UseFollow},
			Events: []EnkeepEvent{{Day: "2026-01-05", Workspace: "w", Kind: "violate"}}},
		// Rejected by the user in enkeep: obsolete with a veto.
		{Table: "rules", ID: "R3", Status: "candidate", Text: "z", Entry: Entry{Layer: Guideline, Subject: SubjectAI, Use: UseFollow},
			Events: []EnkeepEvent{{Day: "2026-02-02", Workspace: "w", Kind: "new", Quote: "z"}, {Day: "2026-02-03", Workspace: "w", Kind: "user_reject"}}},
		// A legacy import without quotes: 15, dormant a little over a week later.
		{Table: "knowledge_entries", ID: "K1", Status: "active", Source: "legacy_kb", Text: "k", Entry: Entry{Layer: Guideline, Subject: SubjectAI, Use: UseFollow}},
		// A legacy import that says what R1 says: side evidence on R1, not an entry.
		{Table: "knowledge_entries", ID: "K2", Status: "active", Source: "legacy_kb", Text: "不要编", SameAs: "ek-rules-R1"},
		// Repeated in enkeep's history on many days long before the import: no decay before the import day.
		{Table: "rules", ID: "R4", Status: "candidate", Text: "w", Entry: Entry{Layer: Guideline, Subject: SubjectAI, Use: UseFollow},
			Events: []EnkeepEvent{{Day: "2025-01-06", Workspace: "a", Kind: "violate"}, {Day: "2025-01-06", Workspace: "a", Kind: "trigger"}, {Day: "2025-02-03", Workspace: "b", Kind: "violate"}}},
		// Workspace state: a fact (progress).
		{Table: "workspace_state", ID: "W1", Status: "active", Text: "进展", Events: []EnkeepEvent{{Day: "2026-10-01", Workspace: "w", Kind: "new", Quote: "做完一半"}}},
	}
	n, joined, err := Import(b, recs, "2026-10-08")
	if err != nil || n != 7 || joined != 1 {
		t.Fatalf("import: %d %d %v", n, joined, err)
	}
	b.Tick("2026-10-08", P)
	get := func(id string) Status { return b.Entries[id].Status }
	if s := get("ek-rules-R1"); s.State != Active || s.Layer != Guideline || s.RuleBBy != "migration" {
		t.Fatalf("confirmed rule: %+v", s)
	}
	if len(b.Entries["ek-rules-R1"].Evidence) != 2 {
		t.Fatalf("legacy side evidence joined: %+v", b.Entries["ek-rules-R1"].Evidence)
	}
	if s := get("ek-patterns-P1"); s.State != Obsolete || !s.Retired {
		t.Fatalf("retired pattern: %+v", s)
	}
	b.Tick("2026-10-25", P)
	if s := get("ek-patterns-P1"); s.State != Obsolete {
		t.Fatalf("enkeep's confirmation brought it back: %+v", s)
	}
	if s := get("ek-rules-R2"); s.State != Dormant {
		t.Fatalf("dormant rule: %+v", s)
	}
	if s := get("ek-rules-R3"); s.State != Obsolete || !s.Vetoed {
		t.Fatalf("rejected rule: %+v", s)
	}
	if s := get("ek-knowledge_entries-K1"); s.State != Dormant {
		t.Fatalf("legacy import after two weeks: %+v", s)
	}
	b.Tick("2026-10-08", P)
	if s := get("ek-knowledge_entries-K1"); s.Score != 15 || s.State != Candidate {
		t.Fatalf("legacy import on the day: %+v", s)
	}
	if s := get("ek-rules-R4"); s.Count != 2 || s.Score < 75 || s.State != Active {
		t.Fatalf("history without decay: %+v", s)
	}
	if s := get("ek-workspace_state-W1"); s.Layer != Fact || s.State != Active {
		t.Fatalf("workspace state: %+v", s)
	}
	// The grace starts on the import day.
	if s := Evaluate(b.Entries["ek-rules-R4"], "2026-10-15", P); s.Score != get("ek-rules-R4").Score {
		t.Fatalf("grace from the import day: %v", s.Score)
	}
}

func TestPrivacyBySentence(t *testing.T) {
	// Synthetic sentences: one ordinary, one that gives away a health matter.
	e := &Entry{ID: "a", Text: "习惯先跑步再工作。上周体检血压偏高。", Sentences: []string{"习惯先跑步再工作。", "上周体检血压偏高。"},
		PrivateSentences: []PrivateSentence{{I: 1, Category: "health"}}}
	whole, vis := Privacy(e)
	if whole || vis != "习惯先跑步再工作。" {
		t.Fatalf("one private sentence: %v %q", whole, vis)
	}
	e.PrivateSentences = append(e.PrivateSentences, PrivateSentence{I: 0, Category: "health"})
	if whole, _ := Privacy(e); !whole {
		t.Fatal("every sentence private")
	}
	// An enkeep profile domain is private as a whole…
	if whole, _ := Privacy(&Entry{Text: "x", Domain: "finance"}); !whole {
		t.Fatal("finance domain")
	}
	// …but a guard rule for the AI is not private for its topic.
	if whole, vis := Privacy(&Entry{Text: "感情话题除非用户主动提起，否则不评价", Domain: "relationship_family", Use: UseFollow}); whole || vis == "" {
		t.Fatal("guard rule")
	}
}

func TestInjectionLevels(t *testing.T) {
	b := NewBook()
	day := "2026-10-19"
	rp := Tags{Scene: SceneReview, Expression: ExprClaim, HoldsStance: true, Stance: StanceRequirement, ThingType: "决策与权衡"}
	principle := &Entry{ID: "p", Text: "陈述事实前先查证", Layer: Principle, Subject: SubjectSelf, Use: UseUnderstand,
		Evidence: []Evidence{say("2026-10-05", "s", "a", correctClaim), say("2026-10-12", "s", "b", correctClaim), say(day, "r", "c", rp)}}
	hard := guideline("hard", say(day, "s", "绝不用代号", correctClaim))
	hard.Hard, hard.Global, hard.Text = true, true, "不要用代号"
	scoped := guideline("scoped", say(day, "s", "这个仓库必须先跑测试", correctClaim))
	scoped.Hard = true
	soft := guideline("soft", say(day, "s", "尽量先给结论", correctClaim))
	descr := &Entry{ID: "descr", Text: "喜欢先看结论", Layer: Fact, Descriptive: true, FactCategory: Attribute, Subject: SubjectSelf, Use: UseUnderstand,
		Evidence: []Evidence{say("2026-10-05", "s", "a", chatClaim), say("2026-10-12", "s", "b", chatClaim)}}
	exp := guideline("exp", say(day, "s", "这次先回滚", assignClaim))
	fact := &Entry{ID: "fact", Text: "定了周五上线", Layer: Fact, FactCategory: Decision, Subject: SubjectSelf, Evidence: []Evidence{say(day, "s", "周五上", workClaim)}}
	cand := &Entry{ID: "cand", Text: "也许换框架", Layer: Fact, FactCategory: Intent, Subject: SubjectSelf,
		Evidence: []Evidence{say(day, "s", "要不要换框架？", Tags{Scene: SceneWork, Expression: ExprQuestion, ProposalQuestion: true})}}
	private := &Entry{ID: "priv", Text: "体检结果正常", Layer: Fact, FactCategory: Attribute, Subject: SubjectSelf,
		PrivateSentences: []PrivateSentence{{I: 0, Category: "health"}}, Evidence: []Evidence{say(day, "s", "x", workClaim)}}
	dormant := guideline("dorm", say("2026-01-05", "s", "x", correctClaim))
	for _, e := range []*Entry{principle, hard, scoped, soft, descr, exp, fact, cand, private, dormant} {
		b.Put(e)
	}
	b.Tick(day, P)
	got := map[string]Injection{}
	for _, x := range Inject(b, P) {
		got[x.ID] = x
	}
	want := map[string]Level{"p": Resident, "hard": Resident, "scoped": Recall, "soft": Recall, "descr": Recall,
		"exp": Search, "fact": Search, "cand": Search, "priv": None, "dorm": None}
	for id, l := range want {
		if got[id].Level != l {
			t.Errorf("%s: %s (%s), want %s; status %+v", id, got[id].Level, got[id].Why, l, b.Entries[id].Status)
		}
	}
	if !got["cand"].Candidate {
		t.Error("a candidate reference is marked")
	}
	// Over the resident budget: the rest by score are recalled.
	q := P
	q.ResidentHardCh = 3
	for _, x := range Inject(b, q) {
		if x.ID == "hard" && x.Level != Recall {
			t.Errorf("over budget: %+v", x)
		}
	}
	if n := Levels(Inject(b, P)); n[Resident] != 2 {
		t.Errorf("levels: %v", n)
	}
}

func TestParamsAreTheDesignsNumbers(t *testing.T) {
	p := DefaultParams()
	got := []float64{p.High, p.Mid, p.Low, p.FactQuote, p.FactAI, p.FactLegacy, p.Intent, p.RepeatWeekFactor, float64(p.GraceDays),
		p.HalfLifeDays, p.MaxHalfLifeDays, p.ActiveLine, p.PrincipleLine, p.DormantLine}
	want := []float64{60, 35, 15, 50, 25, 15, 35, 0.5, 7, 14, 84, 50, 70, 15}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("param %d: %v, want %v", i, got[i], want[i])
		}
	}
	if !strings.HasPrefix(isoWeek("2026-01-01"), "2026-W01") || isoWeek("2027-01-01") != "2026-W53" {
		t.Errorf("iso weeks: %s %s", isoWeek("2026-01-01"), isoWeek("2027-01-01"))
	}
}
