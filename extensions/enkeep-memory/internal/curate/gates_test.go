package curate

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/stub"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
	"github.com/nwverse/persona/sdk/go/wire"
)

// seedItem puts an item straight into the state: one evidence line per "chat@day" ref, each with its own quote
// unless quote is given.
func seedItem(c *Curator, id, class, claim, about, status string, quote string, refs ...string) *store.Item {
	it := &store.Item{ID: id, Class: class, Claim: claim, About: about, Status: status, Confidence: "high"}
	for i, r := range refs {
		chat, day, _ := strings.Cut(r, "@")
		q := quote
		if q == "" {
			q = claim + " #" + string(rune('a'+i))
		}
		it.Evidence = append(it.Evidence, store.Evidence{Chat: chat, Day: day, N: 3 + i, Speaker: norm.Me, Quote: q})
		if !slices.Contains(it.Days, day) {
			it.Days = append(it.Days, day)
		}
		if w := ISOWeek(day); !slices.Contains(it.Weeks, w) {
			it.Weeks = append(it.Weeks, w)
		}
	}
	slices.Sort(it.Days)
	it.FirstSeen, it.LastSeen, it.Updated = it.Days[0], it.Days[len(it.Days)-1], it.Days[len(it.Days)-1]
	it.Sources = sources(it.Evidence)
	c.St.S.Items[id] = it
	return it
}

// ownerAs is a knowledge base whose owner the hub calls by the given name and aliases.
type ownerAs struct {
	kbsrc.Source
	o wire.KBOwner
}

func (s ownerAs) Owner(context.Context) (wire.KBOwner, error) { return s.o, nil }

// The hub names its owner "用户" by default and the configuration may add "我" or "owner": none of them is ever a
// name the curator looks for, so neither the triage nor the checks take "用户" in other people's lines for the owner.
func TestOwnerNamesDropGeneric(t *testing.T) {
	c, fx := newCurator(t, &stub.Model{}, "shadow")
	c.Src = ownerAs{Source: fx, o: wire.KBOwner{Name: "用户", Aliases: []string{"quillmoss", "Owner"}}}
	c.Cfg.OwnerAliases = []string{"我", "owner", "me"}
	got := c.ownerNames(context.Background())
	if len(got) != 1 || got[0] != "quillmoss" {
		t.Fatalf("owner names for matching: %v", got)
	}
	u := &Unit{Ref: "chats/g", Day: "2026-11-03", Meta: kbsrc.Meta{Kind: "group"},
		Lines: []norm.Line{norm.Parse(3, "10:00 Avery: 用户反馈登录慢，owner 在休假")}}
	if c.window(u, map[int]bool{3: true}, got).OwnerNamed() {
		t.Fatal("\"用户\" and \"owner\" in a line are not the owner")
	}
	d := triage.Decide(triage.Chat{Entity: "chats", Meta: kbsrc.Meta{ID: "g", Kind: "group"}, Lines: u.Lines, OwnerNames: got}, c.Cfg.triage())
	if d.Rule == triage.RMention {
		t.Fatalf("not a mention: %s", d.Rule)
	}
}

// ingestOne checks one scripted answer for a hand-made chat-day.
func ingestOne(t *testing.T, c *Curator, owner []string, lines []string, out schema.DailyOutput) *store.Run {
	t.Helper()
	u := &Unit{Ref: "chats/g", Entity: "chats", Meta: kbsrc.Meta{ID: "g", Kind: "group", Name: "G"}, Day: "2026-11-03"}
	d := triage.Decision{Chat: u.Ref, Shown: map[int]bool{}}
	for i, s := range lines {
		l := voice.Relabel(norm.Parse(3+i, s))
		u.Lines = append(u.Lines, l)
		d.Shown[l.N] = true
	}
	if out.Summary == nil {
		out.Summary = []schema.Point{}
	}
	text, _ := json.Marshal(out)
	run := &store.Run{Counts: map[string]int{}}
	done := &store.Done{Key: "k-" + u.Day}
	c.ingest(c.builder(u.Day, owner), u, d, owner, c.knownEntities(context.Background()), string(text), done, run, nil)
	return run
}

// ingestOneAs is ingestOne for an answer to the given prompt version.
func ingestOneAs(t *testing.T, c *Curator, owner []string, lines []string, out schema.DailyOutput, prompt string) *store.Run {
	t.Helper()
	u := &Unit{Ref: "chats/g", Entity: "chats", Meta: kbsrc.Meta{ID: "g", Kind: "group", Name: "G"}, Day: "2026-11-03"}
	d := triage.Decision{Chat: u.Ref, Shown: map[int]bool{}}
	for i, s := range lines {
		l := voice.Relabel(norm.Parse(3+i, s))
		u.Lines = append(u.Lines, l)
		d.Shown[l.N] = true
	}
	out.Summary = []schema.Point{}
	text, _ := json.Marshal(out)
	run := &store.Run{Counts: map[string]int{}}
	done := &store.Done{Key: "k-" + u.Day, Prompt: prompt}
	c.ingest(c.builder(u.Day, owner), u, d, owner, c.knownEntities(context.Background()), string(text), done, run, nil)
	return run
}

func item(claim string, n int, quote string) schema.Item {
	return schema.Item{Claim: claim, Evidence: []schema.Evidence{{N: n, Quote: quote}}, Confidence: "high"}
}

func rejected(c *Curator, claim string) string {
	for _, r := range c.St.S.Rejections {
		if r.Claim == claim {
			return r.Code
		}
	}
	return ""
}

func kept(c *Curator, claim string) bool {
	return slices.ContainsFunc(c.St.S.ItemsBy("", nil), func(it *store.Item) bool { return it.Claim == claim })
}

// An item is kept only when one of its lines is the user's own words or names the user: other people's statements,
// a line an agent wrote under the user's name, and "用户" in someone's line are context, not items.
func TestItemsNeedTheUsersVoice(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	owner := []string{"用户", "Sam Rivera", "Sam"}
	lines := []string{
		"10:00 Avery: 支付网关下周三迁移到新集群",                                          // 3: someone else's statement
		"10:01 我: 以后评审我都先看测试再看实现",                                             // 4: the user's own words
		"10:02 我: 修复登录超时问题 🤖 Generated with [Claude Code](https://x.example)", // 5: ghost-written
		"10:03 Blake: @Sam 周五前给 Atlas 二期排期",                                   // 6: names the user
		"10:04 Casey: 用户说页面白屏，要尽快修",                                           // 7: "用户" is not the owner
	}
	ingestOne(t, c, owner, lines, schema.DailyOutput{
		Facts: []schema.Item{
			item("支付网关下周三迁移到新集群", 3, "支付网关下周三迁移"),
			item("Blake 请 Sam 周五前给 Atlas 二期排期", 6, "周五前给 Atlas 二期排期"),
			item("页面白屏要尽快修", 7, "页面白屏，要尽快修"),
		},
		Practices: []schema.Item{
			item("用户评审时先看测试再看实现", 4, "先看测试再看实现"),
			item("登录超时问题已修复", 5, "修复登录超时问题"),
		},
	})
	for claim, want := range map[string]string{
		"支付网关下周三迁移到新集群": verify.NotAboutOwner,
		"登录超时问题已修复":     verify.AssistantSource, // an agent wrote it under the user\'s name
		"页面白屏要尽快修":      verify.NotAboutOwner,
	} {
		if got := rejected(c, claim); got != want || kept(c, claim) {
			t.Errorf("%s: rejected %q, want %q", claim, got, want)
		}
	}
	for _, claim := range []string{"用户评审时先看测试再看实现", "Blake 请 Sam 周五前给 Atlas 二期排期"} {
		if !kept(c, claim) {
			t.Errorf("kept: %s (rejections %+v)", claim, c.St.S.Rejections)
		}
	}
	if !slices.Contains(verify.RejectCodes, verify.NotAboutOwner) || !slices.Contains(verify.RejectCodes, verify.PrivateFinance) {
		t.Error("the new codes are listed for reports")
	}
}

// Bank, payroll and money notices never become items, whoever wrote them, and neither does a claim about them.
func TestFinanceItemsDropped(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	owner := []string{"Sam"}
	lines := []string{
		"10:00 我: 工资到账了，这个月比上个月多",     // 3
		"10:01 我: 信用卡这期我改成自动还款，以后都这样", // 4
		"10:02 我: 发布前我都会先跑一遍冒烟测试",     // 5
	}
	ingestOne(t, c, owner, lines, schema.DailyOutput{
		Facts:     []schema.Item{item("用户这个月工资比上月多", 3, "工资到账了")},
		Practices: []schema.Item{item("用户的卡改成自动处理", 4, "改成自动还款"), item("用户发布前先跑冒烟测试", 5, "先跑一遍冒烟测试")},
		Knowledge: []schema.Item{item("用户的账户余额够付房租", 5, "先跑一遍冒烟测试")},
	})
	for _, claim := range []string{"用户这个月工资比上月多", "用户的卡改成自动处理", "用户的账户余额够付房租"} {
		if got := rejected(c, claim); got != verify.PrivateFinance || kept(c, claim) {
			t.Errorf("%s: %q, want %s", claim, got, verify.PrivateFinance)
		}
	}
	if !kept(c, "用户发布前先跑冒烟测试") {
		t.Errorf("the user's practice is kept: %+v", c.St.S.Rejections)
	}
}

// One chat's word is a candidate however sure the model was: a fact becomes active only with a second independent
// source, and a practice said once in one chat stays observed.
func TestSingleSourceHighStaysCandidate(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	ev := func(chat, day, q string) []store.Evidence {
		return []store.Evidence{{Chat: chat, Day: day, N: 3, Speaker: norm.Me, Quote: q}}
	}
	fact := schema.Item{Claim: "Atlas 二期周五上线", Confidence: "high"}
	id := c.keepItem(store.Fact, fact, ev("chats/lark-dm-1", "2026-11-03", "Atlas 二期周五上线"), nil, "2026-11-03")
	if it := c.St.S.Items[id]; it.Status != "candidate" || it.Sources != 1 {
		t.Fatalf("single source, high: %s, %d sources", it.Status, it.Sources)
	}
	// The same chat again, another person, another day: still one source.
	more := []store.Evidence{{Chat: "chats/lark-dm-1", Day: "2026-11-04", N: 5, Speaker: "Avery", Quote: "周五上线没问题"}}
	c.keepItem(store.Fact, fact, more, nil, "2026-11-04")
	if it := c.St.S.Items[id]; it.Status != "candidate" || it.Sources != 1 {
		t.Fatalf("one chat, two speakers: %s, %d sources", it.Status, it.Sources)
	}
	c.keepItem(store.Fact, fact, ev("chats/mail-mine", "2026-11-04", "二期周五上线"), nil, "2026-11-04")
	if it := c.St.S.Items[id]; it.Status != "active" || it.Sources != 2 {
		t.Fatalf("two chats: %s, %d sources", it.Status, it.Sources)
	}
	pr := schema.Item{Claim: "用户发布前先跑冒烟测试", Confidence: "high"}
	pid := c.keepItem(store.Practice, pr, ev("chats/lark-dm-1", "2026-11-03", "先跑冒烟测试"), nil, "2026-11-03")
	if it := c.St.S.Items[pid]; it.Status != "observed" {
		t.Fatalf("a practice said once: %s", it.Status)
	}
	c.keepItem(store.Practice, pr, ev("chats/lark-dm-1", "2026-11-04", "冒烟测试先跑"), nil, "2026-11-04")
	if it := c.St.S.Items[pid]; it.Status != "recurring" {
		t.Fatalf("a practice on two days: %s", it.Status)
	}
}

// The same words in several chats (a mirrored repository, a cross-post) are one source.
func TestMirroredQuotesCountOnce(t *testing.T) {
	ev := []store.Evidence{
		{Chat: "chats/github-pr-1", Day: "2026-11-03", N: 3, Speaker: norm.Me, Quote: "LGTM, but keep the retry limit at 3"},
		{Chat: "chats/github-pr-mirror", Day: "2026-11-03", N: 3, Speaker: norm.Me, Quote: "LGTM,  but keep the retry limit at 3!"},
		{Chat: "chats/lark-grp-atlas", Day: "2026-11-03", N: 9, Speaker: norm.Me, Quote: "LGTM but keep the retry limit at 3"},
	}
	if n := sources(ev); n != 1 {
		t.Fatalf("one comment mirrored to two repositories and cross-posted: %d sources", n)
	}
	c, _ := newCurator(t, nil, "shadow")
	it := schema.Item{Claim: "用户要求重试上限保持 3 次", Confidence: "high"}
	id := c.keepItem(store.Knowledge, it, ev, nil, "2026-11-03")
	if got := c.St.S.Items[id]; got.Status != "candidate" || got.Sources != 1 {
		t.Fatalf("mirrors: %s, %d sources", got.Status, got.Sources)
	}
	c.keepItem(store.Knowledge, it, []store.Evidence{{Chat: "chats/mail-mine", Day: "2026-11-04", N: 4, Speaker: norm.Me, Quote: "重试上限还是 3 次"}}, nil, "2026-11-04")
	if got := c.St.S.Items[id]; got.Status != "active" || got.Sources != 2 {
		t.Fatalf("a second, independent source: %s, %d sources", got.Status, got.Sources)
	}
}

func pinTargets(c *Curator) map[string]string {
	out := map[string]string{}
	for _, p := range c.St.S.Proposals {
		if strings.HasPrefix(p.Kind, "pin_") {
			out[p.Items[0]] = p.Target
		}
	}
	return out
}

// Pins go on a person or project of the knowledge base, never on the chat the evidence came from.
func TestPinsOnlyOnEntities(t *testing.T) {
	c, _ := newCurator(t, nil, "review")
	two := []string{"chats/lark-dm-1@2026-11-03", "chats/mail-mine@2026-11-04"}
	seedItem(c, "i-proj", store.Fact, "Atlas 二期由用户负责", "projects/atlas", "active", "", two...)
	seedItem(c, "i-name", store.Fact, "Blake Wu 负责支付网关", "Blake Wu", "active", "", two...)
	seedItem(c, "i-chat", store.Fact, "发布窗口是周三", "chats/lark-dm-1", "active", "", two...)
	seedItem(c, "i-none", store.Fact, "周会改到周二", "", "active", "", two...)
	seedItem(c, "i-unknown", store.Fact, "Dana 换了组", "Dana", "active", "", two...)
	seedItem(c, "i-noid", store.Fact, "某个项目暂停", "projects/nope", "active", "", two...)
	seedItem(c, "i-practice", store.Practice, "Atlas 合代码前先提评审单", "projects/atlas", "established", "", two...)
	days, _ := WeekDays("2026-W45")
	c.proposePins(context.Background(), days, &store.Run{Counts: map[string]int{}})
	got := pinTargets(c)
	want := map[string]string{"i-proj": "projects/atlas", "i-name": "people/lark-u2", "i-practice": "projects/atlas"}
	if len(got) != len(want) {
		t.Fatalf("pins: %v, want %v", got, want)
	}
	for id, target := range want {
		if got[id] != target {
			t.Errorf("%s pinned on %q, want %q", id, got[id], target)
		}
	}
}

// Nothing from a single source is pinned: not an item that became active under the old rule, not a practice that
// is established only because it came up in two weeks of one chat.
func TestSingleSourceNoPin(t *testing.T) {
	c, _ := newCurator(t, nil, "review")
	seedItem(c, "i-old", store.Fact, "Atlas 二期周五上线", "projects/atlas", "active", "", "chats/lark-dm-1@2026-11-03")
	seedItem(c, "i-weeks", store.Practice, "用户发布前先跑冒烟测试", "projects/atlas", "established", "",
		"chats/lark-dm-1@2026-10-28", "chats/lark-dm-1@2026-11-04")
	seedItem(c, "i-mirror", store.Knowledge, "重试上限是 3 次", "projects/atlas", "active", "重试上限是 3 次",
		"chats/github-pr-1@2026-11-03", "chats/lark-grp-atlas@2026-11-03")
	days, _ := WeekDays("2026-W45")
	c.proposePins(context.Background(), days, &store.Run{Counts: map[string]int{}})
	if got := pinTargets(c); len(got) != 0 {
		t.Fatalf("single-source pins: %v", got)
	}
}

// A week's run works on that week: run late (in W45) for W44, it promotes, merges, pins and samples only what was
// seen in W44, leaves items first seen in W45 as they were, and reports W44.
func TestWeeklyOnlyThisWeek(t *testing.T) {
	c, _ := newCurator(t, nil, "review") // now: 2026-11-09, in W46
	ctx := context.Background()
	w44 := []string{"chats/lark-dm-1@2026-10-27", "chats/mail-mine@2026-10-29"}
	w45 := []string{"chats/lark-dm-1@2026-11-03", "chats/mail-mine@2026-11-05"}
	seedItem(c, "i-a44", store.Fact, "Atlas 二期的压测方案在周四前发群里", "", "candidate", "", w44[0])
	seedItem(c, "i-b44", store.Fact, "Atlas 二期的压测方案在周四前发群里。", "", "candidate", "", w44[1])
	seedItem(c, "i-pin44", store.Fact, "Atlas 二期由用户负责", "projects/atlas", "candidate", "", w44...)
	seedItem(c, "i-a45", store.Fact, "上线检查单由用户整理好再发", "", "candidate", "", w45[0])
	seedItem(c, "i-b45", store.Fact, "上线检查单由用户整理好再发。", "", "candidate", "", w45[1])
	seedItem(c, "i-pin45", store.Fact, "Atlas 三期由用户牵头", "projects/atlas", "candidate", "", w45...)
	before := map[string]store.Item{}
	for _, id := range []string{"i-a45", "i-b45", "i-pin45"} {
		before[id] = *c.St.S.Items[id]
	}
	run, err := c.RunWeekly(ctx, "2026-W44", false)
	if err != nil {
		t.Fatal(err)
	}
	if run.Period != "2026-W44" || !strings.Contains(run.Report, "2026-10-26 到 2026-11-01") {
		t.Fatalf("period %s; report:\n%s", run.Period, run.Report)
	}
	for id, b := range before {
		if it := c.St.S.Items[id]; it.Status != b.Status || it.MergedInto != "" || len(it.Evidence) != len(b.Evidence) {
			t.Errorf("a W45 item was touched by the W44 run: %s %+v", id, it)
		}
	}
	a, b := c.St.S.Items["i-a44"], c.St.S.Items["i-b44"]
	if a.Status != "merged" && b.Status != "merged" {
		t.Errorf("the W44 duplicates are merged: %s %s", a.Status, b.Status)
	}
	if got := c.St.S.Items["i-pin44"].Status; got != "active" {
		t.Errorf("a W44 fact from two chats is promoted: %s", got)
	}
	if got := pinTargets(c); len(got) != 1 || got["i-pin44"] != "projects/atlas" {
		t.Errorf("pins of the W44 run: %v", got)
	}
	for _, p := range c.St.S.Proposals {
		for _, id := range p.Items {
			if strings.HasSuffix(id, "45") {
				t.Errorf("a W45 item in a W44 proposal: %s %s", p.Kind, id)
			}
		}
	}
	sample := c.St.S.Samples["2026-W44"]
	if len(sample) == 0 {
		t.Fatal("no sample")
	}
	for _, id := range sample {
		if strings.HasSuffix(id, "45") {
			t.Errorf("a W45 item in the W44 sample: %v", sample)
		}
	}
}

// The coverage check holds the curator only to the days a daily run covered: after one run nothing is missing; a
// run whose digests failed leaves the units it admitted that day flagged, and a dry run records nothing.
func TestCoverageOnlyRunDays(t *testing.T) {
	c, _ := newCurator(t, &stub.Model{}, "shadow")
	ctx := context.Background()
	if _, err := c.RunDaily(ctx, "2026-11-03", false); err != nil {
		t.Fatal(err)
	}
	coverage := func() []Finding {
		var out []Finding
		for _, f := range c.health(ctx, c.ownerNames(ctx), "2026-11-08", &store.Run{Counts: map[string]int{}}) {
			if f.Check == "coverage" {
				out = append(out, f)
			}
		}
		return out
	}
	if got := coverage(); len(got) != 0 {
		t.Fatalf("after one complete daily run: %+v", got)
	}
	if len(c.St.S.Admitted["2026-11-03"]) == 0 {
		t.Fatal("the run records what it admitted")
	}
	c.Model = nil // no model configured: the day is triaged, nothing digested
	if _, err := c.RunDaily(ctx, "2026-11-05", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.St.S.Admitted["2026-11-05"]; ok {
		t.Fatal("a dry run records nothing")
	}
	if _, err := c.RunDaily(ctx, "2026-11-04", false); err != nil {
		t.Fatal(err)
	}
	admitted := c.St.S.Admitted["2026-11-04"]
	got := coverage()
	if len(admitted) == 0 || len(got) != len(admitted) {
		t.Fatalf("admitted on 11-04 without a digest: %v; findings %+v", admitted, got)
	}
	for _, f := range got {
		if !slices.Contains(admitted, f.Target) || !strings.Contains(f.Text, "2026-11-04") || strings.Contains(f.Text, "2026-11-03") {
			t.Errorf("finding: %+v", f)
		}
	}
}

// A group the user belongs to is full of lines that are not the user's business: a job posting and its requirements,
// a shop's campaign and a giveaway, one member's measurement for another. None is the user's words, none names the
// user, so none becomes a fact, a practice or knowledge, however useful it reads; the same giveaway in a line that
// names the user (an @-mention) is kept.
func TestGroupNoiseIsNotTheUsers(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	owner := []string{"用户", "Sam Rivera", "Sam"}
	lines := []string{
		"10:00 Avery: 招聘实习生，要求在读本科及以上，熟练使用 Excel、SQL，英语流利", // 3: posting
		"10:01 Blake: 超级宠粉日来袭，活动锁定每月8号，福袋59.9起",            // 4: shop campaign
		"10:02 Casey: 内测有bug反馈排行榜，前十会发购物卡",                 // 5: giveaway
		"10:03 Dana: 我核查了一下，评分根据延迟、重传等指标，每秒采样取最近5次均值",      // 6: a member's answer
		"10:04 Avery: @Sam 内测反馈排行榜前十有购物卡，你的问题我已转给测试",       // 7: names the user
	}
	ingestOne(t, c, owner, lines, schema.DailyOutput{
		Facts:     []schema.Item{item("该实习岗位要求在读本科及以上", 3, "要求在读本科及以上")},
		Practices: []schema.Item{item("商店的宠粉日固定在每月8号", 4, "活动锁定每月8号"), item("内测设有bug反馈排行榜，前十发购物卡", 5, "bug反馈排行榜")},
		Knowledge: []schema.Item{item("评分基于延迟、重传等指标，每秒采样取近5次均值", 6, "每秒采样取最近5次均值")},
	})
	for _, claim := range []string{"该实习岗位要求在读本科及以上", "商店的宠粉日固定在每月8号", "内测设有bug反馈排行榜，前十发购物卡", "评分基于延迟、重传等指标，每秒采样取近5次均值"} {
		if got := rejected(c, claim); got != verify.NotAboutOwner || kept(c, claim) {
			t.Errorf("%s: rejected %q, want %q", claim, got, verify.NotAboutOwner)
		}
	}
	if n := len(c.St.S.ItemsBy("", nil)); n != 0 {
		t.Errorf("%d items kept from other people's lines that do not name the user", n)
	}
}
