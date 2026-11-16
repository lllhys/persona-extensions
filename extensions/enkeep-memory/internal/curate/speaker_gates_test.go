package curate

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
	"github.com/nwverse/persona/sdk/go/wire"
)

// ingestKind is ingestOne for a unit of the given kind and source, resolved as unit() resolves it (speakers,
// SelfOff), answered with out under the current daily prompt.
func ingestKind(t *testing.T, c *Curator, id, source, kind string, lines []string, out schema.DailyOutput) *Unit {
	t.Helper()
	ctx := context.Background()
	u := &Unit{Ref: "chats/" + id, Entity: "chats", Meta: kbsrc.Meta{ID: id, Source: source, Kind: kind, Name: id}, Day: "2026-11-03"}
	d := triage.Decision{Chat: u.Ref, Shown: map[int]bool{}}
	for i, s := range lines {
		l := voice.Relabel(norm.Parse(3+i, s))
		u.Lines = append(u.Lines, l)
		d.Shown[l.N] = true
	}
	c.attribute(ctx, u)
	if out.Summary == nil {
		out.Summary = []schema.Point{}
	}
	text, _ := json.Marshal(out)
	owner := c.ownerNames(ctx)
	run := &store.Run{Counts: map[string]int{}}
	done := &store.Done{Key: "k-" + id, Prompt: schema.DailyPrompt}
	c.ingest(c.builder(u.Day, owner), u, d, owner, c.knownEntities(ctx), string(text), done, run, nil)
	return u
}

func fact(claim, kind string, n int, quote string) schema.Item {
	return schema.Item{Claim: claim, Kind: kind, About: "x", Moment: "reply_person", Impact: "助手在回复这个人时据此安排", ImpactObject: "x", Domain: entry.Work, Evidence: []schema.Evidence{{N: n, Quote: quote}}, Confidence: "high"}
}

// The release rules for facts about the user's world (5.4): the other side of a direct chat is talking to the
// user; a group line is released only when it (or the claim) names one of the user's projects or its alias; a
// person being in the knowledge base releases nothing; nothing else is opened up.
func TestReleaseRules(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	// Direct chat: the other side's word counts.
	ingestKind(t, c, "lark-dm-x", "lark", "dm", []string{"10:00 Avery Lin: 结算服务下周起由我来负责"}, schema.DailyOutput{
		Facts: []schema.Item{fact("结算服务下周起由 Avery Lin 负责", "role", 3, "结算服务下周起由我来负责")},
	})
	if !kept(c, "结算服务下周起由 Avery Lin 负责") {
		t.Errorf("a direct chat's other side: %v", c.St.S.Rejections)
	}
	// A group: a project name (or its alias) in the line releases; a known person alone does not; neither does
	// chatter about nothing of the user's.
	ingestKind(t, c, "lark-grp-x", "lark", "group", []string{
		"10:00 Avery Lin: Atlas 项目群里说对账模块归 Blake 了", // names the project's alias
		"10:01 Avery Lin: 结算服务下周起切到新机房",             // a known person, nothing of the user's
		"10:02 Blake: 楼下咖啡店换了新菜单",                   // chatter
	}, schema.DailyOutput{Facts: []schema.Item{
		fact("对账模块归 Blake", "role", 3, "对账模块归 Blake 了"),
		fact("结算服务下周起切到新机房", "status", 4, "结算服务下周起切到新机房"),
		fact("楼下咖啡店换了新菜单", "status", 5, "楼下咖啡店换了新菜单"),
	}})
	if !kept(c, "对账模块归 Blake") {
		t.Errorf("a group line naming the user's project (alias): %v", c.St.S.Rejections)
	}
	for _, claim := range []string{"结算服务下周起切到新机房", "楼下咖啡店换了新菜单"} {
		if got := rejected(c, claim); got != verify.NotAboutOwner || kept(c, claim) {
			t.Errorf("%s: rejected %q, want NOT_ABOUT_OWNER (being in the knowledge base releases nothing)", claim, got)
		}
	}
	// K7.group off: the project branch closes, the direct chat stays.
	c.St.S.Switches[entry.K7GroupByProject] = entry.SwitchSetting{Value: "off", By: "user"}
	ingestKind(t, c, "lark-grp-y", "lark", "group", []string{"10:00 Avery Lin: Atlas 二期排到十二月"}, schema.DailyOutput{
		Facts: []schema.Item{fact("Atlas 二期排到十二月", "status", 3, "Atlas 二期排到十二月")},
	})
	if kept(c, "Atlas 二期排到十二月") {
		t.Error("K7.group off still released a group line by project name")
	}
	// A practice (④) is the user's own way: never released from someone else's words, even in a direct chat.
	ingestKind(t, c, "lark-dm-y", "lark", "dm", []string{"10:00 Avery Lin: 我每次发布前都先跑冒烟测试"}, schema.DailyOutput{
		Practices: []schema.Item{item("发布前先跑冒烟测试", 3, "发布前都先跑冒烟测试")},
	})
	if kept(c, "发布前先跑冒烟测试") {
		t.Error("someone else's habit kept as the user's practice")
	}
}

// Mail: an older mail quoted inside a message is not the outer sender's words, so a claim resting on it is not the
// user's even when the user sent the message.
func TestMailQuotedPartIsNotTheSenders(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	ingestKind(t, c, "mail-x", "mail", "mail", []string{
		"10:00 我: 收到，我这边周五前给结论。\nOn Mon, Avery Lin <avery@example.com> wrote:\n> 我们团队以后所有接口都先写文档再写代码",
	}, schema.DailyOutput{Practices: []schema.Item{
		item("所有接口都先写文档再写代码", 3, "以后所有接口都先写文档再写代码"),
		item("周五前给结论", 3, "周五前给结论"),
	}})
	if kept(c, "所有接口都先写文档再写代码") {
		t.Error("a quoted older mail counted as the user's words")
	}
	if !kept(c, "周五前给结论") {
		t.Errorf("the user's own words in the same message: %v", c.St.S.Rejections)
	}
}

// With kb.owner empty (or a name only), the user's lines in groups and mail are known by the connector alone and a
// mention of the user cannot be told: ③④⑥ are not taken from them (the page gets the reason); direct chats and the
// assistant's conversations go on as usual.
func TestWeakOwnerKeepsGroupsOutOfThreeFourSix(t *testing.T) {
	c, fx := newCurator(t, nil, "shadow")
	c.Src = ownerAs{Source: fx, o: wire.KBOwner{}}
	group := ingestKind(t, c, "lark-grp-w", "lark", "group", []string{
		"10:00 我: 以后评审我都先看测试再看实现",
	}, schema.DailyOutput{Practices: []schema.Item{item("评审先看测试再看实现", 3, "评审我都先看测试再看实现")}})
	if group.SelfOff == "" {
		t.Fatal("a weak owner's group is not marked")
	}
	if got := rejected(c, "评审先看测试再看实现"); got != selfUnverified {
		t.Errorf("④ from a group with a weak owner: rejected %q", got)
	}
	// ③: a judgement from the same group.
	run := &store.Run{Counts: map[string]int{}}
	w := c.window(group, map[int]bool{3: true}, nil)
	c.ingestJudgements(w, group, triage.TierEngaged, []schema.Judgement{
		judgement("先看测试再看实现", "评审代码时", "先看测试", "测试说明意图", jev(3, "评审我都先看测试再看实现")),
	}, &store.Done{Key: "k-j"}, run)
	if run.Counts["judgement"] != 0 || rejected(c, "先看测试再看实现") != selfUnverified {
		t.Errorf("③ from a group with a weak owner: %v %v", run.Counts, c.St.S.Rejections)
	}
	// A direct chat is used as usual.
	dm := ingestKind(t, c, "lark-dm-w", "lark", "dm", []string{"10:00 我: 以后合并前我都先跑一遍全量测试"},
		schema.DailyOutput{Practices: []schema.Item{item("合并前先跑全量测试", 3, "合并前我都先跑一遍全量测试")}})
	if dm.SelfOff != "" || !kept(c, "合并前先跑全量测试") {
		t.Errorf("a direct chat with a weak owner: selfoff %q, %v", dm.SelfOff, c.St.S.Rejections)
	}
	// The page is told why.
	c.weakOwnerNote([]Unit{*group})
	if st := c.St.S.Sources["S8/lark"]; st == nil || st.Note == "" {
		t.Errorf("no hint on the sources card: %+v", st)
	}
}

// Attribution checks (U8): up to 30 attributed lines a source a round, the unresolved share counted; K16 stays on
// until the user marks; one line put on the user wrongly turns the source off; a later round that passes turns it
// back on; a rerun of a day counts nothing twice.
func TestSpeakerChecksAndK16(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	mk := func(day string, n int) []Unit {
		u := Unit{Ref: "chats/lark-grp-c", Entity: "chats", Meta: kbsrc.Meta{ID: "lark-grp-c", Source: "lark", Kind: "group"}, Day: day,
			Who: map[int]string{}, WhoWhy: map[int]string{}}
		for i := 1; i <= n; i++ {
			u.Lines = append(u.Lines, norm.Line{N: i, Speaker: "person-b", Body: "line"})
			switch {
			case i%10 == 0:
				u.Who[i], u.WhoWhy[i] = "", "ambiguous"
			case i%2 == 0:
				u.Who[i], u.WhoWhy[i] = entry.WhoSelf, "self"
			default:
				u.Who[i], u.WhoWhy[i] = "person:p1", "unique"
			}
		}
		return []Unit{u}
	}
	c.checkSpeakers("2026-11-02", mk("2026-11-02", 50))
	c.checkSpeakers("2026-11-02", mk("2026-11-02", 50)) // rerun: nothing counted twice
	st := c.St.S.Sources["S8/lark"]
	if st == nil || len(c.St.S.SpeakerChecks) != SpeakerSample || st.Unresolved != 0.1 || !st.LearnPeople {
		t.Fatalf("checks %d, status %+v", len(c.St.S.SpeakerChecks), st)
	}
	// The user marks one line put on them as wrong: the source stops teaching people.
	var self *entry.SpeakerCheck
	for i := range c.St.S.SpeakerChecks {
		if c.St.S.SpeakerChecks[i].Who == entry.WhoSelf {
			self = &c.St.S.SpeakerChecks[i]
		}
	}
	for i := range c.St.S.SpeakerChecks {
		chk := c.St.S.SpeakerChecks[i]
		mark := "right"
		if chk.Ref == self.Ref && chk.N == self.N {
			mark = "wrong"
		}
		if err := c.MarkSpeakerCheck(chk.Ref, chk.Day, chk.N, mark); err != nil {
			t.Fatal(err)
		}
	}
	if st.LearnPeople || LearnPeople(c.St.S, "lark") || st.Note == "" {
		t.Fatalf("one wrong self line: %+v", st)
	}
	// The next round, all marked right: back on.
	c.checkSpeakers("2026-11-20", mk("2026-11-20", 20))
	for _, chk := range c.St.S.SpeakerChecks {
		if chk.Day == "2026-11-20" {
			if err := c.MarkSpeakerCheck(chk.Ref, chk.Day, chk.N, "right"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !st.LearnPeople || !LearnPeople(c.St.S, "lark") {
		t.Fatalf("a passing round did not turn the source back on: %+v %v", st, c.St.S.Switches)
	}
}

// Relations (5.4): a conversation id is never an entity, it stands for the conversation's one project; a new type
// joins the user's table the second time it comes up; a relation between two people needs resolved speakers.
func TestGraphSessionsTypesAndSpeakers(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	ctx := context.Background()
	owner := c.ownerNames(ctx)
	run := &store.Run{Counts: map[string]int{}}
	mkUnit := func(id string, projects []string, lines ...string) (*Unit, *verify.Window) {
		u := &Unit{Ref: "chats/" + id, Entity: "chats", Meta: kbsrc.Meta{ID: id, Source: "lark", Kind: "group", Name: id, Projects: projects}, Day: "2026-11-03"}
		shown := map[int]bool{}
		for i, s := range lines {
			l := norm.Parse(3+i, s)
			u.Lines = append(u.Lines, l)
			shown[l.N] = true
		}
		c.attribute(ctx, u)
		return u, c.window(u, shown, owner)
	}
	k := c.knownEntities(ctx)
	var atlas string
	for id, name := range k.projects {
		if name == "Atlas" {
			atlas = id
		}
	}
	b := c.builder("2026-11-03", owner)
	u, w := mkUnit("lark-grp-s", []string{atlas}, "10:00 Avery Lin: 结算服务依赖这个群里说的 Billing 系统")
	c.ingestGraph(b, w, u, schema.DailyOutput{Assertions: []schema.Assertion{
		{S: "chats/lark-grp-s", P: "depends_on", O: "Billing", Evidence: []schema.Evidence{{N: 3, Quote: "依赖这个群里说的 Billing 系统"}}, Confidence: "high"},
	}}, k, &store.Done{Key: "k-s"}, run)
	found := false
	for _, a := range c.St.S.Graph.Assertions {
		if a.P == "depends_on" {
			if n := c.St.S.Graph.Nodes[a.S]; n == nil || n.Type != "project" {
				t.Errorf("the session became %+v", n)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("the session's relation was not resolved to its project: %v %v", run.Counts, c.St.S.Rejections)
	}
	// Without one project, a session's relation is not written.
	u, w = mkUnit("lark-grp-t", nil, "10:00 Avery Lin: 这个群依赖 Billing 系统")
	before := len(c.St.S.Graph.Assertions)
	c.ingestGraph(b, w, u, schema.DailyOutput{Assertions: []schema.Assertion{
		{S: "chats/lark-grp-t", P: "depends_on", O: "Billing", Evidence: []schema.Evidence{{N: 3, Quote: "这个群依赖 Billing 系统"}}, Confidence: "high"},
	}}, k, &store.Done{Key: "k-t"}, run)
	if len(c.St.S.Graph.Assertions) != before {
		t.Error("a session with no single project became an entity")
	}
	// A new type: pending the first time, learned the second (another unit-day).
	newType := func(id string) {
		u, w := mkUnit(id, nil, "10:00 Avery Lin: Blake 把 Billing 交接给了 Casey")
		c.ingestGraph(b, w, u, schema.DailyOutput{Assertions: []schema.Assertion{
			{S: "Billing", P: "handed_over_to", O: "Casey", Evidence: []schema.Evidence{{N: 3, Quote: "Billing 交接给了 Casey"}}, Confidence: "high"},
		}}, k, &store.Done{Key: "k-" + id}, run)
	}
	newType("lark-grp-n1")
	if rejected(c, "Billing handed_over_to Casey") != typePending {
		t.Fatalf("first sighting: %v", c.St.S.Rejections)
	}
	newType("lark-grp-n2")
	if run.Counts["graph_type_learned"] != 1 || !hasType(c.St.S.Types[entry.World], "handed_over_to") {
		t.Fatalf("second sighting: %v %+v", run.Counts, c.St.S.Types[entry.World])
	}
	// "attended" never joins.
	u, w = mkUnit("lark-grp-a", nil, "10:00 Avery Lin: Blake 参加了评审会")
	c.ingestGraph(b, w, u, schema.DailyOutput{Assertions: []schema.Assertion{
		{S: "Blake", P: "attended", O: "评审会", Evidence: []schema.Evidence{{N: 3, Quote: "Blake 参加了评审会"}}, Confidence: "high"},
	}}, k, &store.Done{Key: "k-a"}, run)
	if rejected(c, "Blake attended 评审会") != relationEmpty || hasType(c.St.S.Types[entry.World], "attended") {
		t.Errorf("attended: %v", c.St.S.Rejections)
	}
	// Between two people, from a line whose speaker is unresolved: not written.
	u, w = mkUnit("lark-grp-u", nil, "10:00 someone-unknown: Blake 向 Casey 汇报")
	c.ingestGraph(b, w, u, schema.DailyOutput{Assertions: []schema.Assertion{
		{S: "Blake", P: "reports_to", O: "Casey", Evidence: []schema.Evidence{{N: 3, Quote: "Blake 向 Casey 汇报"}}, Confidence: "high"},
	}}, k, &store.Done{Key: "k-u"}, run)
	if got := rejected(c, "Blake reports_to Casey"); got != speakerUnres {
		t.Errorf("unresolved speaker: %q", got)
	}
}

func hasType(ts []entry.TypeDef, name string) bool {
	for _, t := range ts {
		if t.Name == name {
			return true
		}
	}
	return false
}

// Write-back of ⑤ facts: one source with the original words is written all the same, marked single source; two
// independent sources are not marked; a fact without words or about nobody known is not written.
func TestWorldWriteBackSingleSource(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	k := c.knownEntities(context.Background())
	var atlas string
	for id, name := range k.projects {
		if name == "Atlas" {
			atlas = id
		}
	}
	day := "2026-11-03"
	ev := func(ref, q string) entry.Evidence {
		return entry.Evidence{Source: entry.SrcChat, Ref: ref, Day: day, N: 3, Quote: q}
	}
	put := func(text string, evs ...entry.Evidence) *entry.Entry {
		e := &entry.Entry{Facet: entry.World, Grain: entry.Action, Text: text, Subject: "project:" + atlas, Evidence: evs}
		id := c.Observe(e, "")
		return c.St.S.Entries[id]
	}
	one := put("Atlas 对账模块由 Blake 负责", ev("chats/a", "对账模块由 Blake 负责"))
	two := put("Atlas 二期 12 月上线", ev("chats/a", "二期 12 月上线"), ev("chats/b", "Atlas 二期十二月上线"))
	bare := put("Atlas 有个新需求", entry.Evidence{Source: entry.SrcChat, Ref: "chats/a", Day: day})
	run := &store.Run{Counts: map[string]int{}}
	c.proposeWorld(context.Background(), []string{"2026-11-02", day, "2026-11-08"}, run)
	if !one.SingleSource || two.SingleSource {
		t.Fatalf("single source marks: %v %v", one.SingleSource, two.SingleSource)
	}
	lines := map[string]string{}
	for _, p := range c.St.S.Proposals {
		var ch Change
		_ = json.Unmarshal(p.Change, &ch)
		lines[p.Items[0]] = ch.PinLine
	}
	if l := lines[one.ID]; l == "" || !strings.Contains(l, singleSourceMark) {
		t.Errorf("single source line: %q", l)
	}
	if l := lines[two.ID]; l == "" || strings.Contains(l, singleSourceMark) {
		t.Errorf("two sources line: %q", l)
	}
	if lines[bare.ID] != "" {
		t.Error("a fact without the original words was written")
	}
}

// Commitments: due within three days go to the weekly report; past due without a sign of being done are flagged
// possibly overdue and stay open; done ones are left alone.
func TestCommitmentsDueAndOverdue(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	mk := func(what, due, done string) *entry.Entry {
		e := &entry.Entry{Facet: entry.World, Grain: entry.Action, Text: what,
			Commitment: &entry.Commitment{From: entry.WhoSelf, To: "person:p1", What: what, Due: due, Done: done},
			Evidence:   []entry.Evidence{{Source: entry.SrcChat, Ref: "chats/a", Day: "2026-11-01", Quote: what}}}
		return c.St.S.Entries[c.Observe(e, "")]
	}
	soon := mk("周四前发方案", "2026-11-12", "")
	late := mk("上周五给评审意见", "2026-11-07", "")
	done := mk("周一交报告", "2026-11-03", "2026-11-03")
	far := mk("月底复盘", "2026-11-30", "")
	run := &store.Run{Counts: map[string]int{}}
	due, overdue := c.markCommitments("2026-11-10", run)
	if due != 1 || overdue != 1 {
		t.Fatalf("due %d overdue %d", due, overdue)
	}
	if !slices.Contains(late.Flags, PossiblyOverdue) || late.Status == entry.Retired || !late.Live() {
		t.Errorf("overdue: %+v", late)
	}
	for _, e := range []*entry.Entry{soon, done, far} {
		if slices.Contains(e.Flags, PossiblyOverdue) {
			t.Errorf("%s flagged", e.Text)
		}
	}
	// Done later: the flag comes off.
	late.Commitment.Done = "2026-11-11"
	c.markCommitments("2026-11-11", run)
	if slices.Contains(late.Flags, PossiblyOverdue) {
		t.Error("a commitment done since is still flagged")
	}
}
