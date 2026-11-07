package memory

import "testing"

// Regression cases from the fifth trial replay (30 days, rule D). The sentences are the trial's own where they
// are plain work talk, and rewritten where they were not; sessions and ids are synthetic. Nothing personal (health,
// finance, relationships, beliefs) is in them: this repository is published.
//
// Each case pins the outcome the design wants after the 11-07 fourth revision: a correction counts by its stance,
// not its wording; a follow-through counts only when stated or visible in a work product.

const (
	workGroup = "chats/work-group"   // a project group chat
	assistant = "chats/assistant-dm" // a conversation with the AI assistant
	review    = "meetings/review"    // a design review
)

func injectionOf(t *testing.T, b *Book, id string) Injection {
	t.Helper()
	for _, x := range Inject(b, P) {
		if x.ID == id {
			return x
		}
	}
	t.Fatalf("%s: not in the book", id)
	return Injection{}
}

// 「下次这种不放心的可以试试灰度和 ab 了」: a suggestion in a work discussion, said once. Only an experience: a
// candidate, retrieval only, waiting for a follow-through. (In the fourth round it became a guideline at once.)
func TestRegressionSuggestionIsOnlyExperience(t *testing.T) {
	b := NewBook()
	e := &Entry{ID: "canary", Text: "对没把握的上线改动，下次用灰度和 AB 实验兜底，而不是靠回滚", Layer: Guideline, Subject: SubjectSelf,
		Use: UseUnderstand, ThingType: "设计与构建", LifeAreas: []string{"工作"},
		Evidence: []Evidence{say("2026-10-20", workGroup, "下次这种不放心的可以试试灰度和 ab 了。。。",
			Tags{Scene: SceneWork, Expression: ExprSuggest, HoldsStance: true, Stance: StanceRequirement, DescriptiveValue: "习惯"})}}
	b.Put(e)
	b.Tick("2026-10-20", P)
	st := e.Status
	if st.Layer != Experience || st.State != Candidate || st.Score != 35 || st.RuleB || !st.Awaiting {
		t.Fatalf("suggestion: %+v", st)
	}
	if in := injectionOf(t, b, "canary"); in.Level != Search || !in.Candidate {
		t.Fatalf("injection: %+v", in)
	}
	// Someone only agreeing in words changes nothing.
	b.FollowUp("canary", "2026-10-21", FollowVerbal, workGroup+"#40", "是的，这个可以试试")
	b.Tick("2026-10-21", P)
	if e.Status.Layer != Experience || e.Status.FollowUps != 0 {
		t.Fatalf("verbal agreement: %+v", e.Status)
	}
	// Stated as done: backfilled to the serious band, rule B by the follow-through: a guideline.
	b.FollowUp("canary", "2026-10-27", FollowStated, workGroup+"#12", "这次按灰度加 AB 上的")
	b.Tick("2026-10-27", P)
	if e.Status.Layer != Guideline || e.Status.FollowUps != 1 || e.Status.Count != 1 {
		t.Fatalf("carried out: %+v", e.Status)
	}
}

// 「网页全是假的」: venting while correcting the AI. The user really holds it, so it is the high band, however rough
// the words. With the requirement behind it (do not take made-up pages as evidence) it is rule B; venting that asks
// for nothing (「太难用了」) is high too but cannot set up a guideline alone.
func TestRegressionVentingCorrectionIsHigh(t *testing.T) {
	vent := Tags{Scene: SceneCorrect, Expression: ExprVent, HoldsStance: true, Stance: StanceRequirement, Attitude: "AI 给的网页信息不可信"}
	g := GradeSentence(TargetLayers, SpeakerUser, withSupport(vent))
	if g.Band != BandHigh || !g.RuleB {
		t.Fatalf("grade: %+v", g)
	}
	b := NewBook()
	e := &Entry{ID: "verify", Text: "事实断言先查证，不拿搜索到的网页当依据", Layer: Guideline, Subject: SubjectAI, Use: UseFollow,
		ThingType: "判断与验证", LifeAreas: []string{"AI 与个人系统"}, Prohibition: true,
		Evidence: []Evidence{say("2026-10-15", assistant, "网页全是假的", vent)}}
	b.Put(e)
	b.Tick("2026-10-15", P)
	if e.Status.Score != 60 || e.Status.State != Active || e.Status.Layer != Guideline || !e.Status.HighSource {
		t.Fatalf("venting correction: %+v", e.Status)
	}
	if in := injectionOf(t, b, "verify"); in.Level != Recall {
		t.Fatalf("injection: %+v", in)
	}
	// Plain venting, nothing asked: still high, but an experience.
	plain := Tags{Scene: SceneCorrect, Expression: ExprVent, HoldsStance: true, Stance: StanceNone}
	x := &Entry{ID: "x", Text: "这个工具难用", Layer: Guideline, Subject: SubjectAI, Use: UseFollow,
		Evidence: []Evidence{say("2026-10-21", assistant, "太难用了。。", plain)}}
	if st := Evaluate(x, "2026-10-21", P); st.Score != 60 || st.Layer != Experience {
		t.Fatalf("plain venting: %+v", st)
	}
}

// Jokes do not score: their literal words are not facts, and they add nothing to a conclusion. Only the attitude
// behind one counts, for a descriptive attribute.
func TestRegressionJokesDoNotScore(t *testing.T) {
	joke := Tags{Scene: SceneWork, Expression: ExprJoke, Support: SupportDirect}
	// 「回滚是不可能回滚的」 on an experience about this release.
	e := &Entry{ID: "release", Text: "这次上线出问题不回滚，直接修", Layer: Experience, Subject: SubjectSelf, Use: UseReference,
		Evidence: []Evidence{say("2026-10-20", workGroup, "回滚是不可能回滚的", joke)}}
	if st := Evaluate(e, "2026-10-20", P); st.Score != 0 || st.Count != 0 {
		t.Fatalf("joke on an experience: %+v", st)
	}
	// 「请假一时爽」 recorded literally as an event: nothing.
	f := &Entry{ID: "leave", Text: "用户那周在休假", Layer: Fact, FactCategory: Event, Subject: SubjectSelf, Use: UseReference,
		Evidence: []Evidence{say("2026-10-09", "chats/friends", "请假一时爽", Tags{Scene: SceneChat, Expression: ExprJoke, Support: SupportDirect})}}
	if st := Evaluate(f, "2026-10-09", P); st.Score != 0 || st.State != Candidate {
		t.Fatalf("joke as a fact: %+v", st)
	}
	// The attitude behind a joke («我直接原地退休», design 6.21 §5) is evidence for who the user is: not that they
	// retire, but that the chore annoys them.
	d := &Entry{ID: "chore", Text: "对重复的手工对账很烦", Layer: Fact, Descriptive: true, FactCategory: Attribute,
		Subject: SubjectSelf, Use: UseUnderstand, SelfSubnode: "认知风格",
		Evidence: []Evidence{say("2026-10-30", workGroup, "再让我手工对一遍我直接原地退休", Tags{Scene: SceneWork, Expression: ExprJoke, Attitude: "对重复的手工活很烦", Support: SupportDirect})}}
	if st := Evaluate(d, "2026-10-30", P); st.Score != 35 {
		t.Fatalf("attitude of a joke: %+v", st)
	}
}

// 「讨论不等于授权」: the user corrects the AI with statements, not with 应该 / 不要. Since the fourth revision these
// count like any other evidence: high, rule B by their stance, accumulated normally, no special threshold. (In the
// fifth round they stayed experiences, retrieval only, until a confirmation; that question is void now.)
func TestRegressionStatedCorrectionAccumulatesNormally(t *testing.T) {
	stated := Tags{Scene: SceneCorrect, Expression: ExprClaim, HoldsStance: true, Stance: StancePosition}
	b := NewBook()
	e := &Entry{ID: "discuss", Text: "讨论不等于授权：与 AI 讨论方案不代表批准执行", Layer: Guideline, Subject: SubjectAI, Use: UseFollow,
		ThingType: "风险与边界", LifeAreas: []string{"AI 与个人系统"}, Global: true,
		Evidence: []Evidence{
			say("2026-10-23", assistant, "我是想和你分析讨论一下我这个判断合理吗", stated),
			say("2026-10-30", "chats/assistant-dm-2", "我是在和你讨论我的判断，不是改你的结论", stated),
		}}
	b.Put(e)
	b.Tick("2026-10-23", P)
	if st := e.Status; st.Score != 60 || st.State != Active || st.Layer != Guideline || st.RuleBBy != "sentence" {
		t.Fatalf("first correction: %+v", st)
	}
	if in := injectionOf(t, b, "discuss"); in.Level != Recall {
		t.Fatalf("injection after the first: %+v", in)
	}
	// The next week: 60 + 60 × (1 − 0.6) = 84, the ordinary formula.
	b.Tick("2026-10-30", P)
	if st := e.Status; st.Score != 84 || st.Count != 2 || len(st.Weeks) != 2 {
		t.Fatalf("second correction: %+v", st)
	}
	// 「我不是让你开发云盘，我只是在想方案」 for 「设计类的指令默认只出方案」: the same, at once.
	plan := &Entry{ID: "plan", Text: "只说设计、方案、规划而没说实现的指令，默认只输出方案，不直接动手实现", Layer: Guideline, Subject: SubjectAI,
		Use: UseFollow, ThingType: "计划与执行", Evidence: []Evidence{say("2026-10-16", assistant, "我不是让你开发云盘，我只是在想方案", stated)}}
	if st := Evaluate(plan, "2026-10-16", P); st.Layer != Guideline || st.State != Active {
		t.Fatalf("plan only: %+v", st)
	}
	// The same sentence attached to a rule it does not support counts nothing for that rule.
	wrong := &Entry{ID: "wrong", Text: "架构类方案先列出待确认的问题清单", Layer: Guideline, Subject: SubjectAI, Use: UseFollow,
		Evidence: []Evidence{say("2026-10-16", assistant, "我不是让你开发云盘，我只是在想方案", Tags{Scene: SceneCorrect, Expression: ExprClaim, HoldsStance: true, Stance: StancePosition, Support: SupportNone})}}
	if st := Evaluate(wrong, "2026-10-16", P); st.Score != 15 || st.Layer != Experience {
		t.Fatalf("does not support: %+v", st)
	}
}

// A complaint in a work discussion about a tool is the user's opinion: a fact, retrieval only.
func TestRegressionComplaintIsAnOpinion(t *testing.T) {
	b := NewBook()
	e := &Entry{ID: "tool", Text: "用户认为这个内部试用的工具太难用", Layer: Fact, FactCategory: Opinion, Subject: SubjectSelf, Use: UseReference,
		Evidence: []Evidence{say("2026-10-21", workGroup, "没啥更多的体验了，太难用了。。",
			Tags{Scene: SceneWork, Expression: ExprVent, HoldsStance: true, Stance: StanceNone, Support: SupportPartial, Attitude: "对易用性不满"})}}
	b.Put(e)
	b.Tick("2026-10-21", P)
	if e.Status.Score != 50 || e.Status.State != Active {
		t.Fatalf("opinion: %+v", e.Status)
	}
	if in := injectionOf(t, b, "tool"); in.Level != Search || in.Candidate {
		t.Fatalf("injection: %+v", in)
	}
}

// A rule confirmed in enkeep whose evidence is only venting stays a guideline after the migration, and a pattern
// enkeep had retired is not brought back by a later batch confirmation.
func TestRegressionMigration(t *testing.T) {
	b := NewBook()
	vent := &Tags{Scene: SceneCorrect, Expression: ExprVent, HoldsStance: true, Stance: StanceNone, Support: SupportDirect}
	_, _, err := Import(b, []EnkeepRecord{
		{Table: "rules", ID: "R-1", Status: "confirmed", Text: "不要用代号称呼文件和项目", IsHard: true,
			Entry:  Entry{Layer: Guideline, Subject: SubjectAI, Use: UseFollow, Global: true, Prohibition: true, ThingType: "表达与创作"},
			Events: []EnkeepEvent{{Day: "2026-09-01", Workspace: "ws", Kind: "violate", Quote: "这又是啥代号", Tags: vent}}},
		{Table: "profile_facts", ID: "PF-1", Status: "retired", Text: "常用的编辑器",
			Entry:  Entry{Layer: Fact, FactCategory: Attribute, Subject: SubjectSelf, SelfSubnode: "表达偏好"},
			Events: []EnkeepEvent{{Day: "2026-08-01", Workspace: "ws", Kind: "new", Quote: "我用 x"}, {Day: "2026-10-21", Workspace: "ws", Kind: "user_confirm"}}},
	}, "2026-10-08")
	if err != nil {
		t.Fatal(err)
	}
	b.Tick("2026-11-06", P)
	r := b.Entries["ek-rules-R-1"].Status
	if r.Layer != Guideline || r.State != Active || !r.Floor || r.Score < 50 {
		t.Fatalf("confirmed rule: %+v", r)
	}
	if in := injectionOf(t, b, "ek-rules-R-1"); in.Level != Resident {
		t.Fatalf("a global hard rule is resident: %+v", in)
	}
	if pf := b.Entries["ek-profile_facts-PF-1"].Status; pf.State != Obsolete {
		t.Fatalf("retired profile fact: %+v", pf)
	}
}

func withSupport(t Tags) Tags {
	if t.Support == "" {
		t.Support = SupportDirect
	}
	return t
}
