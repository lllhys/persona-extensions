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
)

// Placeholder people of the synthetic units below.
func d8known() *known {
	k := &known{people: map[string]string{"pa": "Alex Park", "pb": "Blair Chen", "pc": "Casey Moe"}, projects: map[string]string{"px": "project-x"},
		byName: map[string][]string{}}
	for id, n := range k.people {
		k.byName[norm.Q(n)] = append(k.byName[norm.Q(n)], "people/"+id)
	}
	k.byName[norm.Q("project-x")] = []string{"projects/px"}
	return k
}

// d8unit is a unit of the given ref and kind; lines are numbered from 1.
func d8unit(ref, kind string, lines ...string) (*Unit, triage.Decision) {
	u := &Unit{Ref: ref, Entity: "chats", Day: "2026-11-05", Meta: kbsrc.Meta{ID: strings.TrimPrefix(ref, "chats/"), Kind: kind, Name: "unit",
		People: []string{"pa", "pb", "pc"}}}
	d := triage.Decision{Chat: ref, Shown: map[int]bool{}, Tier: triage.TierOwn}
	for i, s := range lines {
		l := voice.Relabel(norm.Parse(i+1, s))
		u.Lines = append(u.Lines, l)
		d.Shown[l.N] = true
	}
	return u, d
}

// d8ingest runs one daily/8 answer through ingest.
func d8ingest(t *testing.T, c *Curator, u *Unit, d triage.Decision, out schema.DailyOutput, fix rewriter) *store.Run {
	t.Helper()
	if out.Summary == nil {
		out.Summary = []schema.Point{}
	}
	text, _ := json.Marshal(out)
	run := &store.Run{ID: "run-test", Counts: map[string]int{}}
	done := &store.Done{Key: "k-" + u.Ref, Prompt: schema.DailyPrompt, Day: u.Day}
	c.ingest(c.builder(u.Day, nil), u, d, []string{"Sam Rivera"}, d8known(), string(text), done, run, fix)
	return run
}

func obs(facet, text string, n int, quote string) schema.Observation {
	return schema.Observation{Facet: facet, Text: text, Choice: text, Situation: "x", Domain: entry.Work, Confidence: "high",
		Impact:   schema.ObsImpact{Moment: "every_turn", Action: "给选项时附上每个选项的全称和取舍"},
		Evidence: []schema.Evidence{{N: n, Quote: quote}}}
}

func obsRejected(c *Curator, text string) string {
	for _, r := range c.St.S.Rejections {
		if r.Claim == verify.Redact(text) && strings.HasPrefix(r.Class, "obs:") {
			return r.Code
		}
	}
	return ""
}

func entryWith(c *Curator, text string) *entry.Entry {
	for _, e := range c.St.S.Entries {
		if e.Text == text {
			return e
		}
	}
	return nil
}

var d8persona = []string{
	"10:00 我: 以后给我选项的时候不要只写代号，每个选项写清楚是什么",
	"10:01 助手: 好的，以后选项都写全称，并附上取舍",
	"10:02 我: 好",
	"10:03 我: 部署前先在预发跑一遍冒烟测试，再让我确认上线",
	"10:04 助手: 建议每次改动后都跑一遍全量测试",
	"10:05 我: 我最近在推进 project-x 的上线",
	"10:06 我: 连测试机用 jump-dev 这个别名，密码在 keychain 里的 dev-db 条目，token=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef123456",
}

// The impact check (design 5.0): an entry names a moment of entry.Moments, an action the assistant takes then, and
// the object of a moment that is about one. Anything else is not kept.
func TestImpactCheck(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	u, d := d8unit("chats/persona-a1", "assistant", d8persona...)
	good := obs("interaction", "选项要写全称，不能只写代号", 1, "不要只写代号")
	noMoment := obs("interaction", "选项不能只写代号一", 1, "不要只写代号")
	noMoment.Impact.Moment = ""
	badMoment := obs("interaction", "选项不能只写代号二", 1, "不要只写代号")
	badMoment.Impact.Moment = "whenever"
	empty := obs("interaction", "选项不能只写代号三", 1, "不要只写代号")
	empty.Impact.Action = "更好地理解用户的偏好"
	emptyEn := obs("interaction", "选项不能只写代号四", 1, "不要只写代号")
	emptyEn.Impact.Action = "better understand the user"
	same := obs("interaction", "选项不能只写代号五", 1, "不要只写代号")
	same.Impact.Action = same.Text
	noObject := obs("workflow", "部署前先在预发跑冒烟测试再确认上线", 4, "部署前先在预发跑一遍冒烟测试")
	noObject.Impact = schema.ObsImpact{Moment: "task_start", Action: "部署类任务开始时先排预发冒烟测试"}
	dispatch := obs("workflow", "部署前先在预发跑冒烟测试再让用户确认上线", 4, "部署前先在预发跑一遍冒烟测试，再让我确认上线")
	dispatch.Impact = schema.ObsImpact{Moment: "dispatch", Action: "派部署任务给 Agent 时要求先在预发跑冒烟测试"} // any agent task: no object needed
	evalEvery := schema.Observation{Facet: "people", Person: "Alex Park", Part: "user_eval", Text: "用户认为 Alex Park 回复慢", Domain: entry.Work,
		Confidence: "high", Impact: schema.ObsImpact{Moment: "every_turn", Action: "给 Alex Park 的事项预留更长的回复时间"},
		Evidence: []schema.Evidence{{N: 6, Quote: "我最近在推进 project-x"}}}
	run := d8ingest(t, c, u, d, schema.DailyOutput{Observations: []schema.Observation{good, noMoment, badMoment, empty, emptyEn, same, noObject, evalEvery, dispatch}}, nil)
	for text, want := range map[string]string{
		noMoment.Text: impactMoment, badMoment.Text: impactMoment, empty.Text: impactAction, emptyEn.Text: impactAction,
		same.Text: impactAction, noObject.Text: impactObject, evalEvery.Text: impactMoment,
	} {
		if got := obsRejected(c, text); got != want {
			t.Errorf("%s: rejected %q, want %q", text, got, want)
		}
	}
	if entryWith(c, dispatch.Text) == nil {
		t.Errorf("dispatch without an object: %q", obsRejected(c, dispatch.Text))
	}
	if e := entryWith(c, good.Text); e == nil || e.Impact.Moment != "every_turn" || e.Facet != entry.Interaction || e.Origin != entry.Behavior {
		t.Errorf("a real impact is kept: %+v %v", e, run.Counts)
	}
	// A daily/8 fact is held to the same check.
	c2, _ := newCurator(t, nil, "shadow")
	fact := func(claim, moment, action, object string) schema.Item {
		return schema.Item{Claim: claim, Kind: "commitment", Moment: moment, Impact: action, ImpactObject: object, Domain: entry.Work, Confidence: "high",
			Evidence: []schema.Evidence{{N: 6, Quote: "推进 project-x 的上线"}}}
	}
	d8ingest(t, c2, u, d, schema.DailyOutput{Facts: []schema.Item{
		fact("用户在推进 project-x 上线", "report", "汇报时先说 project-x 上线进度", ""),
		fact("用户在推进 project-x 上线一", "kb_lookup", "问到上线进度时答 project-x 在推进", ""),
		fact("用户在推进 project-x 上线二", "report", "记住这一点", ""),
	}}, nil)
	if !kept(c2, "用户在推进 project-x 上线") || rejected(c2, "用户在推进 project-x 上线一") != impactObject || rejected(c2, "用户在推进 project-x 上线二") != impactAction {
		t.Errorf("facts: %+v", c2.St.S.Rejections)
	}
	if e := entryWith(c2, "用户在推进 project-x 上线"); e == nil || e.Facet != entry.World || !e.SingleSource {
		t.Errorf("a kept fact is a world entry: %+v", e)
	}
}

// Judgement, workflow, persona and the user's asks of the AI rest on the user's own words: another person's line, a
// bare agreement and words only in the AI's text are not the user's.
func TestSelfOnly(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	u, d := d8unit("chats/grp", "group",
		"09:00 Alex Park: 周报没必要写，浪费时间，先停一周",
		"09:01 我: 好",
		"09:02 我: 发版前一定要先灰度，拆开看分人群的数据再全量",
		"09:03 助手: 以后每次发版都先跑全量回归",
		"09:04 我: This PR adds the parser. Generated with Claude Code",
	)
	other := obs("judgement", "周报没必要写先停一周", 1, "周报没必要写")
	agree := obs("judgement", "周报先停一周", 2, "好")
	ai := obs("workflow", "每次发版先跑全量回归", 4, "以后每次发版都先跑全量回归")
	ghost := obs("persona", "用户在写解析器", 5, "This PR adds the parser")
	mine := obs("judgement", "发版前先灰度并拆分人群看数据再全量", 3, "发版前一定要先灰度")
	mine.Impact = schema.ObsImpact{Moment: "ask_user", Action: "请示发版时默认建议先灰度，按人群拆开看数据"}
	d8ingest(t, c, u, d, schema.DailyOutput{Observations: []schema.Observation{other, agree, ai, ghost, mine}}, nil)
	for text, want := range map[string]string{other.Text: obsNotSelf, agree.Text: obsAgreementOnly, ai.Text: verify.AssistantSource, ghost.Text: verify.AssistantSource} {
		if got := obsRejected(c, text); got != want {
			t.Errorf("%s: rejected %q, want %q", text, got, want)
		}
	}
	e := entryWith(c, mine.Text)
	if e == nil || len(e.Evidence) != 1 || e.Evidence[0].Who != entry.WhoSelf || e.Evidence[0].Source != entry.SrcChat {
		t.Fatalf("the user's own judgement: %+v", e)
	}
	// Unit.Who (internal/speaker) wins over the fallback: a "我" line the resolution could not place is nobody's.
	c2, _ := newCurator(t, nil, "shadow")
	u.Who = map[int]string{1: "person:pa", 2: entry.WhoSelf, 3: "", 4: "", 5: ""}
	d8ingest(t, c2, u, d, schema.DailyOutput{Observations: []schema.Observation{mine}}, nil)
	if got := obsRejected(c2, mine.Text); got != obsNotSelf {
		t.Errorf("an unresolved line: %q", got)
	}
	// ① needs an AI in the unit: an instruction to a colleague is not how the user works with the AI.
	c3, _ := newCurator(t, nil, "shadow")
	u3, d3 := d8unit("chats/grp2", "group", "09:00 我: 以后给我的方案都附上取舍再发")
	ask := obs("interaction", "方案要附上取舍", 1, "以后给我的方案都附上取舍")
	d8ingest(t, c3, u3, d3, schema.DailyOutput{Observations: []schema.Observation{ask}}, nil)
	if got := obsRejected(c3, ask.Text); got != obsNotToAI {
		t.Errorf("an ask in a group with no AI: %q", got)
	}
}

// The user's "about me" gives interaction, tools and persona, never judgement or workflow; its entries are
// self-reported and start stable. The assistant's memory files are hand-written.
func TestAboutMeAndMemoryFiles(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	u, d := d8unit(refAboutMe, "about", "09:00 我: 我在一家创业公司带平台组，做决定时先看数据，选项要写全称")
	j := obs("judgement", "做决定时先看数据", 1, "做决定时先看数据")
	w := obs("workflow", "做事先看数据再定", 1, "做决定时先看数据")
	p := obs("persona", "用户在创业公司带平台组", 1, "带平台组")
	p.Type = "role"
	i := obs("interaction", "选项要写全称", 1, "选项要写全称")
	d8ingest(t, c, u, d, schema.DailyOutput{Observations: []schema.Observation{j, w, p, i}}, nil)
	if obsRejected(c, j.Text) != obsFacetNotAllowed || obsRejected(c, w.Text) != obsFacetNotAllowed {
		t.Errorf("about/me gave ③④: %+v", c.St.S.Rejections)
	}
	for _, text := range []string{p.Text, i.Text} {
		e := entryWith(c, text)
		if e == nil || e.Origin != entry.SelfReport || e.Status != entry.Stable || e.Evidence[0].Source != entry.SrcAbout {
			t.Errorf("%s: %+v", text, e)
		}
	}
	c2, _ := newCurator(t, nil, "shadow")
	u2, d2 := d8unit("memory/feedback_options.md", "memory", "- 选项不能只写代号，要写出每个选项是什么")
	m := obs("interaction", "选项不能只写代号", 1, "选项不能只写代号")
	d8ingest(t, c2, u2, d2, schema.DailyOutput{Observations: []schema.Observation{m}}, nil)
	if e := entryWith(c2, m.Text); e == nil || e.Origin != entry.Handwritten || e.Evidence[0].Source != entry.SrcMemory {
		t.Errorf("memory file: %+v %+v", e, c2.St.S.Rejections)
	}
}

// Wording in another language than the target, or speaking of the user as "我" / "I" / "owner", is asked for once
// more; what is still wrong is dropped.
func TestLanguageCheck(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	u, d := d8unit("chats/persona-a1", "assistant", d8persona...)
	en := obs("interaction", "Options must spell out what each one is, not just a code", 1, "不要只写代号")
	en.Choice, en.Situation = en.Text, "when offering options"
	en.Impact.Action = "spells out every option with its trade-off"
	first := obs("interaction", "我要求选项不要只写代号", 1, "不要只写代号")
	owner := obs("interaction", "主人要求选项不要只写代号", 1, "不要只写代号")
	d8ingest(t, c, u, d, schema.DailyOutput{Observations: []schema.Observation{en, first, owner}}, nil)
	for text, want := range map[string]string{en.Text: obsLanguage, first.Text: obsSelfWord, owner.Text: obsSelfWord} {
		if got := obsRejected(c, text); got != want {
			t.Errorf("%s: rejected %q, want %q", text, got, want)
		}
	}
	// A rewrite that fixes it is kept, marked; the rewrite is asked once for the whole answer.
	c2, _ := newCurator(t, nil, "shadow")
	calls := 0
	fix := func(in schema.RewriteInput) (schema.RewriteOutput, bool) {
		calls++
		if in.Language != "zh-Hans" || in.UserWord != "用户" || len(in.Items) != 2 {
			t.Errorf("rewrite input: %+v", in)
		}
		var out schema.RewriteOutput
		for _, it := range in.Items {
			f := map[string]string{}
			for k, v := range it.Fields {
				f[k] = strings.ReplaceAll(v, "我", "用户")
			}
			out.Items = append(out.Items, schema.RewriteItem{I: it.I, Fields: f})
		}
		return out, true
	}
	d8ingest(t, c2, u, d, schema.DailyOutput{Observations: []schema.Observation{en, first}}, fix)
	if calls != 1 {
		t.Errorf("rewrite calls %d", calls)
	}
	if e := entryWith(c2, "用户要求选项不要只写代号"); e == nil || !slices.Contains(e.Flags, flagRewritten) {
		t.Errorf("rewritten: %+v", e)
	}
	if obsRejected(c2, en.Text) != obsLanguage {
		t.Errorf("still English after the rewrite: %+v", c2.St.S.Rejections)
	}
	// With English as the target, English wording passes, Chinese does not, and "the owner" does not.
	c3, _ := newCurator(t, nil, "shadow")
	c3.Cfg.Language = "en"
	zh := obs("interaction", "选项要写全称，不能只写代号", 1, "不要只写代号")
	own := en
	own.Text = "The owner wants options spelled out"
	d8ingest(t, c3, u, d, schema.DailyOutput{Observations: []schema.Observation{en, zh, own}}, nil)
	if e := entryWith(c3, en.Text); e == nil || e.Evidence[0].Quote != "不要只写代号" {
		t.Errorf("English entry with its quote kept as said: %+v %+v", e, c3.St.S.Rejections)
	}
	if obsRejected(c3, zh.Text) != obsLanguage || obsRejected(c3, own.Text) != obsSelfWord {
		t.Errorf("en target: %+v", c3.St.S.Rejections)
	}
}

// K13: a sensitive category is dropped while the user has not turned it on; once on, it is kept and marked (the
// exits keep it out). A money matter the model did not label is still finance.
func TestSensitiveSwitch(t *testing.T) {
	u, d := d8unit("chats/persona-a1", "assistant", append(slices.Clone(d8persona), "10:07 我: 最近家里老人住院，这周晚上都别排会")...)
	fam := obs("persona", "用户家里老人住院，这周晚上不排会", 8, "这周晚上都别排会")
	fam.Sensitive, fam.Domain = entry.SensFamily, entry.Life
	fam.Impact = schema.ObsImpact{Moment: "ask_user", Action: "这周安排日程时避开晚上"}
	c, _ := newCurator(t, nil, "shadow")
	d8ingest(t, c, u, d, schema.DailyOutput{Observations: []schema.Observation{fam}}, nil)
	if obsRejected(c, fam.Text) != obsSensitiveOff || len(c.St.S.Entries) != 0 {
		t.Errorf("K13.family off: %+v", c.St.S.Rejections)
	}
	c2, _ := newCurator(t, nil, "shadow")
	c2.St.S.Switches["K13.family"] = entry.SwitchSetting{Value: "on", By: "user"}
	c2.St.S.Switches["K13"] = entry.SwitchSetting{Value: "on", By: "user"} // a base value never turns a category on
	d8ingest(t, c2, u, d, schema.DailyOutput{Observations: []schema.Observation{fam}}, nil)
	if e := entryWith(c2, fam.Text); e == nil || e.Sensitive != entry.SensFamily || e.Domain != entry.Life {
		t.Errorf("K13.family on: %+v %+v", e, c2.St.S.Rejections)
	}
	c3, _ := newCurator(t, nil, "shadow")
	c3.St.S.Switches["K13"] = entry.SwitchSetting{Value: "on", By: "user"}
	d8ingest(t, c3, u, d, schema.DailyOutput{Observations: []schema.Observation{fam}}, nil)
	if obsRejected(c3, fam.Text) != obsSensitiveOff {
		t.Errorf("K13 base on, family not: %+v", c3.St.S.Rejections)
	}
	money := obs("persona", "用户这个月工资到账后要还信用卡", 6, "推进 project-x")
	c4, _ := newCurator(t, nil, "shadow")
	d8ingest(t, c4, u, d, schema.DailyOutput{Observations: []schema.Observation{money}}, nil)
	if obsRejected(c4, money.Text) != obsSensitiveOff {
		t.Errorf("unlabelled money: %+v", c4.St.S.Rejections)
	}
}

// Credentials are never kept: an observation or a fact that carries one in its words is dropped; a quote that
// carries one is kept with the credential cut out (tools keep the alias and where the secret lives).
func TestSecretsNotExtracted(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	u, d := d8unit("chats/persona-a1", "assistant", d8persona...)
	tool := func(text, choice string) schema.Observation {
		o := obs("tools", text, 7, "连测试机用 jump-dev 这个别名")
		o.Object, o.Type, o.Choice = "测试机", "machine", choice
		o.Impact = schema.ObsImpact{Moment: "use_object", Action: "连测试机时用 jump-dev 别名，不走默认端口", Object: "测试机"}
		return o
	}
	okTool := tool("连测试机用 jump-dev 别名，密码在钥匙串 dev-db 条目", "用 jump-dev 别名连，密码从钥匙串 dev-db 条目取")
	okTool.Evidence = append(okTool.Evidence, schema.Evidence{N: 7, Quote: "token=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef123456"})
	leakTok := tool("测试机令牌是 ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef123456", "用这个令牌登录")
	leakConn := tool("测试库连接串 postgres://dev:hunter22@192.0.2.5:5432/app", "直接用连接串连")
	leakPwd := tool("测试库 password: Tr0ub4dor3x 登录", "用这个密码登录")
	run := d8ingest(t, c, u, d, schema.DailyOutput{
		Observations: []schema.Observation{okTool, leakTok, leakConn, leakPwd},
		Facts: []schema.Item{{Claim: "测试库的 api_key=sk-proj-abcdefghijklmnopqrstuvwx 已更新", Kind: "status", About: "project-x", Moment: "kb_lookup",
			Impact: "问到测试库时答已更新", ImpactObject: "测试库", Domain: entry.Work, Confidence: "high",
			Evidence: []schema.Evidence{{N: 6, Quote: "推进 project-x 的上线"}}}},
	}, nil)
	for _, text := range []string{leakTok.Text, leakConn.Text, leakPwd.Text} {
		if got := obsRejected(c, text); got != verify.SecretFound {
			t.Errorf("%s: %q", text, got)
		}
	}
	if rejected(c, "测试库的 api_key=[secret] 已更新") != verify.SecretFound {
		t.Errorf("fact with a key: %+v", c.St.S.Rejections)
	}
	e := entryWith(c, okTool.Text)
	if e == nil {
		t.Fatalf("the alias entry: %v %+v", run.Counts, c.St.S.Rejections)
	}
	raw, _ := json.Marshal(c.St.S)
	if strings.Contains(string(raw), "ghp_ABCDEFGHIJ") || strings.Contains(string(raw), "hunter22") || strings.Contains(string(raw), "sk-proj-abcdef") {
		t.Error("a credential reached the state")
	}
	if !slices.ContainsFunc(e.Evidence, func(x entry.Evidence) bool { return strings.Contains(x.Quote, verify.Redacted) }) {
		t.Errorf("the quote with a token is kept redacted: %+v", e.Evidence)
	}
	if len(e.Scopes) != 1 || e.Scopes[0].Kind != entry.ScopeObject || e.Scopes[0].Name != "测试机" {
		t.Errorf("tools scope: %+v", e.Scopes)
	}
}

// A person's card rests on lines attributed to them, the user's lines about them, and others' lines naming them
// (reported); a line nobody can be placed on is nobody's, and an evaluation is the user's own.
func TestPeopleCards(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	u, d := d8unit("chats/grp", "group",
		"09:00 Alex Park: 接口文档我今晚补齐，明天上午给你",
		"09:01 Blair Chen: Alex Park 一般都很晚才回消息",
		"09:02 我: Alex Park 做事很靠谱，就是回消息慢",
		"09:03 Someone Else: Alex Park 在跟 project-x",
		"09:04 Casey Moe: 收到",
	)
	card := func(part, text string, n int, q string) schema.Observation {
		return schema.Observation{Facet: "people", Person: "Alex Park", Part: part, Type: "collaboration", Text: text, Domain: entry.Work, Confidence: "medium",
			Impact:   schema.ObsImpact{Moment: "reply_person", Action: "回 Alex Park 的消息时先确认时间点再催", Object: "Alex Park"},
			Evidence: []schema.Evidence{{N: n, Quote: q}}}
	}
	commit := card("commitment", "Alex Park 答应明天上午给接口文档", 1, "接口文档我今晚补齐")
	commit.Commitment = &schema.ObsCommitment{From: "Alex Park", To: "me", What: "补齐接口文档", Due: "2026-11-06"}
	reported := card("inferred", "Alex Park 回消息偏晚", 2, "一般都很晚才回消息")
	eval := card("user_eval", "用户评价 Alex Park 做事靠谱但回消息慢", 3, "做事很靠谱，就是回消息慢")
	evalOther := card("user_eval", "Blair Chen 认为 Alex Park 回得晚", 2, "一般都很晚才回消息")
	unresolved := card("fact", "Alex Park 在跟 project-x", 4, "Alex Park 在跟 project-x")
	nobody := card("fact", "某人负责接口文档", 1, "接口文档我今晚补齐")
	nobody.Person = "Dana Unknown"
	user := card("fact", "用户自己的事", 3, "做事很靠谱")
	user.Person = "我"
	badPromise := card("commitment", "用户答应补接口文档", 1, "接口文档我今晚补齐")
	badPromise.Commitment = &schema.ObsCommitment{From: "me", To: "Alex Park", What: "补接口文档"}
	d8ingest(t, c, u, d, schema.DailyOutput{Observations: []schema.Observation{commit, reported, eval, evalOther, unresolved, nobody, user, badPromise}}, nil)
	e := entryWith(c, commit.Text)
	if e == nil || e.Person != "person:pa" || e.Commitment == nil || e.Commitment.From != "person:pa" || e.Commitment.To != entry.WhoSelf || e.Evidence[0].Reported {
		t.Errorf("commitment: %+v", e)
	}
	if e := entryWith(c, reported.Text); e == nil || !e.Inferred || !e.ReportedOnly || !e.Evidence[0].Reported || e.Evidence[0].Who != "person:pb" {
		t.Errorf("reported inferred line: %+v", e)
	}
	if e := entryWith(c, eval.Text); e == nil || e.Part != entry.PartUserEval || e.ReportedOnly || e.Evidence[0].Who != entry.WhoSelf {
		t.Errorf("user's evaluation: %+v", e)
	}
	for text, want := range map[string]string{evalOther.Text: obsEvalNotSelf, unresolved.Text: obsUnattributed, nobody.Text: obsPersonUnknown,
		user.Text: obsPersonUnknown, badPromise.Text: verify.SpeakerMismatch} {
		if got := obsRejected(c, text); got != want {
			t.Errorf("%s: rejected %q, want %q", text, got, want)
		}
	}
}

// The old answers (daily/7: judgements and rules, no observations) are still read: Revalidate replays them without a
// model and without crashing.
func TestRevalidateOldAnswers(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	ctx := context.Background()
	units, err := c.units(ctx, "2026-11-04")
	if err != nil {
		t.Fatal(err)
	}
	var u Unit
	ok := false
	for _, x := range units {
		if slices.ContainsFunc(x.Lines, func(l norm.Line) bool { return l.Speaker == norm.Me && len([]rune(l.Body)) >= 8 }) {
			u, ok = x, true
			break
		}
	}
	if !ok {
		t.Skip("no unit with the user's line in the sample")
	}
	var mine norm.Line
	for _, l := range u.Lines {
		if l.Speaker == norm.Me && len([]rune(l.Body)) >= 8 {
			mine = l
			break
		}
	}
	q := string([]rune(mine.Body)[:6])
	old := schema.DailyOutput{Summary: []schema.Point{}, Facts: []schema.Item{}, Mentions: []schema.Mention{}, Assertions: []schema.Assertion{},
		Judgements: []schema.Judgement{{Claim: "x", Trigger: "y", Choice: "z", Dimension: "决策", Confidence: "high", Evidence: []schema.Evidence{{N: mine.N, Quote: q}}}},
		Rules:      []schema.Rule{{Text: "以后选项都写全称不要只写代号", Scope: "global", Confidence: "high", Evidence: []schema.Evidence{{N: mine.N, Quote: q}}}}}
	raw, _ := json.Marshal(old)
	name, err := c.St.PutRaw("old-key", raw)
	if err != nil {
		t.Fatal(err)
	}
	var shown []int
	for _, l := range u.Lines {
		shown = append(shown, l.N)
	}
	c.St.S.Done["old-key"] = &store.Done{Key: "old-key", Step: "daily", Chat: u.Ref, Day: u.Day, Raw: name, Prompt: "daily/7", Ruleset: "rules/4", Shown: shown}
	n, err := c.Revalidate(ctx)
	if err != nil || n != 1 {
		t.Fatalf("revalidate: %d %v", n, err)
	}
	if c.St.S.Done["old-key"].Ruleset != schema.Ruleset || len(c.St.S.Entries) != 0 {
		t.Errorf("an old answer gives no observations: %+v", c.St.S.Entries)
	}
}

// Every daily call stays within the batch size: a long unit is read in parts, in line order, covering every line.
func TestDailyParts(t *testing.T) {
	var lines []string
	for i := 0; i < 600; i++ {
		lines = append(lines, "10:00 Alex Park: "+strings.Repeat("项目进展汇报内容", 6))
	}
	u, d := d8unit("chats/big", "group", lines...)
	parts := dailyParts(u, d)
	if len(parts) < 2 {
		t.Fatalf("a big unit is one call: %d parts", len(parts))
	}
	seen := 0
	for _, p := range parts {
		size := 0
		for _, l := range u.Lines {
			if p.d.Shown[l.N] {
				size += norm.Tokens(l.Speaker+": "+l.Body) + 4
				seen++
			}
		}
		if size > dailyBatchTokens || p.part == "" {
			t.Errorf("part %q: %d tokens", p.part, size)
		}
	}
	if seen != len(lines) {
		t.Errorf("parts cover %d of %d lines", seen, len(lines))
	}
	small, sd := d8unit("chats/small", "dm", "10:00 我: 你好")
	if ps := dailyParts(small, sd); len(ps) != 1 || ps[0].part != "" {
		t.Errorf("a small unit: %+v", ps)
	}
}

// The request carries the target language, the word for the user, the unit's source and origin, and each line's
// attribution; a different language is a different call.
func TestDailyRequest(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	u, d := d8unit("chats/grp", "group", "09:00 Alex Park: 接口文档我今晚补齐", "09:01 我: 好的，Blair Chen 也看一下")
	req, key := c.digestRequest(c.builder(u.Day, nil), u, d, nil, d8known())
	var in schema.DailyInput
	if err := json.Unmarshal([]byte(req.User), &in); err != nil {
		t.Fatal(err)
	}
	if in.Language != "zh-Hans" || in.UserWord != "用户" || in.Source != entry.SrcChat || in.Origin != string(entry.Behavior) ||
		in.Lines[0].Who != "person:pa" || in.Lines[1].Who != entry.WhoSelf || req.MaxTokens != dailyMaxTokens {
		t.Errorf("request: %+v", in)
	}
	names := map[string]bool{}
	for _, e := range in.Known {
		names[e.Name] = true
	}
	if !names["Alex Park"] || !names["Blair Chen"] || names["Casey Moe"] {
		t.Errorf("known people are those who spoke or were named: %+v", in.Known)
	}
	c.Cfg.Language = "en"
	req2, key2 := c.digestRequest(c.builder(u.Day, nil), u, d, nil, d8known())
	if key2 == key || !strings.Contains(req2.User, `"user_word":"the user"`) {
		t.Errorf("language change: %s %s", key, key2)
	}
}
