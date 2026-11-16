package curate

import (
	"context"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

func rule(text, scope string, durable bool, evs ...schema.Evidence) schema.Rule {
	return schema.Rule{Text: text, Scope: scope, Durable: durable, Evidence: evs, Confidence: "high"}
}

// A rule is the owner's own instruction to an AI that answers in the conversation: not an instruction to a person, not
// a bare agreement, not a vague wish. The same instruction on another day joins the rule; said once as a standing
// rule, or on two days, it is in force; a deleted rule is not made again.
func TestRuleGate(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	lines := []string{
		"10:00 助手: 我可以把备份清理掉吗？",
		"10:01 我: 备份上限 3 份是已经定好的规矩，本身就是授权，不用再问",
		"10:02 我: 好的",
		"10:03 我: 非常注重质量",
	}
	u, w := jwindow(c, lines)
	rs := []schema.Rule{
		rule("已有规矩即是授权，达到上限直接按规矩处理，不要再向用户确认", "global", true, jev(4, "本身就是授权，不用再问")),
		rule("对方说什么就照做吧好不好", "global", false, jev(5, "好的")),
		rule("做事时非常注重质量", "global", false, jev(6, "非常注重质量")),
	}
	done := &store.Done{Key: "k", Prompt: schema.DailyPrompt}
	run := &store.Run{Counts: map[string]int{}}
	c.ingestRules(w, u, triage.TierOwn, rs, done, run)
	got := map[string]string{}
	for _, r := range c.St.S.Rejections {
		got[r.Claim] = r.Code
	}
	if got[rs[1].Text] != verify.AssistantSource || got[rs[2].Text] != judgementGeneric {
		t.Errorf("rejections: %v", got)
	}
	if len(c.St.S.Rules) != 1 {
		t.Fatalf("rules: %+v (rejections %+v)", c.St.S.Rules, c.St.S.Rejections)
	}
	var r *store.Rule
	for _, x := range c.St.S.Rules {
		r = x
	}
	if r.Status != "active" || !r.Durable || len(r.Evidence) != 1 || r.Evidence[0].Role != voice.RoleUser || r.Evidence[0].Speaker != norm.Me {
		t.Errorf("rule: %+v", r)
	}
	// Said again on another day, worded a little differently: the same rule, two days.
	u2 := *u
	u2.Day = "2026-11-05"
	again := rule("已有规矩即是授权，达到上限直接按规矩处理，不要再向用户确认吧", "global", false, jev(4, "本身就是授权，不用再问"))
	c.ingestRules(w, &u2, triage.TierOwn, []schema.Rule{again}, done, run)
	if len(c.St.S.Rules) != 1 || r.Count != 2 {
		t.Errorf("a repeat joins the rule: %d rules, count %d", len(c.St.S.Rules), r.Count)
	}
	// A conversation with no AI in it gives no rule.
	u3, w3 := jwindow(c, []string{"10:01 Avery: 以后周报都发我", "10:02 我: 好，以后周报都发你，别再问我要了"})
	c.ingestRules(w3, u3, triage.TierOwn, []schema.Rule{rule("周报都发给他不要再问", "global", true, jev(4, "以后周报都发你，别再问我要了"))}, done, run)
	if c.St.S.Rejections[len(c.St.S.Rejections)-1].Code != ruleNotToAI {
		t.Errorf("an instruction to a person: %+v", c.St.S.Rejections[len(c.St.S.Rejections)-1])
	}
	// The user deletes it: it is not made again, and not in the brief.
	r.Deleted = true
	c.settleRules("2026-11-06")
	c.ingestRules(w, &u2, triage.TierOwn, []schema.Rule{again}, done, run)
	if run.Counts["rule_deleted_skipped"] != 1 {
		t.Errorf("deleted rule: %v", run.Counts)
	}
}

// A fact of the current prompt needs a kind and an impact; an answer to the earlier prompt is held to the earlier
// rules; a decision in the owner's own words is active at once, another person's word still needs a second source.
func TestFactImpactGate(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	lines := []string{"10:00 Avery: 发布改到周四", "10:01 我: 好，那我周五前把对账模块交给 Blake，不再等测试环境"}
	mk := func(claim, kind, impact string, n int, q string) schema.Item {
		return schema.Item{Claim: claim, Kind: kind, Impact: impact, Moment: "report", Domain: "work", Evidence: []schema.Evidence{{N: n, Quote: q}}, Confidence: "high"}
	}
	out := schema.DailyOutput{Facts: []schema.Item{
		mk("用户周五前把对账模块交给 Blake", "commitment", "周五前汇报时提醒交接对账模块", 4, "周五前把对账模块交给 Blake"),
		mk("他不再等测试环境", "", "", 4, "不再等测试环境"),
		mk("谁参加了评审会", "status", "无", 4, "周五前把对账模块"),
	}}
	run := ingestOneAs(t, c, []string{"Sam"}, lines, out, schema.DailyPrompt)
	if run.Counts["rejected"] != 2 || rejected(c, "他不再等测试环境") != factNoImpact || rejected(c, "谁参加了评审会") != factNoImpact {
		t.Errorf("rejections: %v %v", run.Counts, c.St.S.Rejections)
	}
	var kept *store.Item
	for _, it := range c.St.S.Items {
		kept = it
	}
	if len(c.St.S.Items) != 1 || kept.Kind != "commitment" || kept.Status != "active" || len(c.St.S.Entries) != 1 {
		t.Errorf("items: %+v", c.St.S.Items)
	}
	// An answer to daily/6 has no kind or impact and is kept as before.
	c2, _ := newCurator(t, nil, "shadow")
	run = ingestOneAs(t, c2, []string{"Sam"}, lines, schema.DailyOutput{Facts: []schema.Item{mk("他不再等测试环境", "", "", 4, "不再等测试环境")}}, "daily/6")
	if run.Counts["rejected"] != 0 || len(c2.St.S.Items) != 1 {
		t.Errorf("daily/6: %v", run.Counts)
	}
	_ = context.Background
}

// What the user did by hand stays: a rule of the old layer is its migrated entry; pinning, editing and deleting it
// act on the entry, the rule follows along, and a one-word text is refused.
func TestOwnLegacyRule(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	c.St.S.Rules["r-1"] = &store.Rule{ID: "r-1", Text: "rule-x for project-x", Scope: "global", Days: []string{"2026-11-03"}, Evidence: []store.Evidence{{Chat: "chats/a", Day: "2026-11-03", N: 3, Speaker: norm.Me, Role: voice.RoleUser, Quote: "quote-x"}}}
	if _, err := c.Own("r-1", "pin", ""); err != nil || !c.St.S.Rules["r-1"].Pinned {
		t.Fatalf("pin: %v %+v", err, c.St.S.Rules["r-1"])
	}
	e := c.St.S.Entries[legacyIDs(c.St.S)["r-1"]]
	if e == nil || !e.Pinned || e.Status != "stable" {
		t.Fatalf("entry: %+v", e)
	}
	if _, err := c.Own("r-1", "edit", "rule-x, the user's own words"); err != nil || !c.St.S.Rules["r-1"].Edited || e.User == nil {
		t.Fatalf("edit: %v", err)
	}
	if _, err := c.Own("r-1", "edit", "短"); err == nil {
		t.Error("a rule of one word")
	}
	if _, err := c.Own("r-1", "delete", ""); err != nil || !e.Deleted || !c.St.S.Rules["r-1"].Deleted {
		t.Errorf("delete: %v", err)
	}
}

// Block A: a fact about the owner's world (who owns what, who reports to whom, what a term means) from a group line
// is released when the line or the claim names one of the owner's projects (5.4); about something else it still
// needs the owner's voice. Someone's decision is not released by the model's About alone.
func TestWorldFactsTakeAnyonesWord(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	lines := []string{
		"10:00 Avery: Atlas 的对账模块现在由 Blake 负责，向 Casey 汇报",
		"10:01 Avery: 支付网关下周三迁移到新集群，由平台组负责",
		"10:02 Avery: 我决定把发布挪到周四",
		"10:03 助手: Atlas 的发布窗口是周五",
	}
	mk := func(claim, kind, about, impact string, n int, q string) schema.Item {
		return schema.Item{Claim: claim, Kind: kind, About: about, Impact: impact, Moment: "kb_lookup", ImpactObject: about, Domain: "work",
			Evidence: []schema.Evidence{{N: n, Quote: q}}, Confidence: "high"}
	}
	out := schema.DailyOutput{Facts: []schema.Item{
		mk("Atlas 的对账模块由 Blake 负责，向 Casey 汇报", "role", "Atlas", "问到对账模块找谁时答 Blake", 3, "对账模块现在由 Blake 负责"),
		mk("支付网关下周三迁移到新集群", "status", "支付网关", "问到支付网关时说明下周三迁移", 4, "支付网关下周三迁移到新集群"),
		mk("发布挪到周四", "decision", "Atlas", "按周四排发布提醒", 5, "我决定把发布挪到周四"),
		mk("Atlas 的发布窗口是周五", "status", "Atlas", "问到发布窗口时答周五", 6, "Atlas 的发布窗口是周五"),
	}}
	run := ingestOneAs(t, c, []string{"Sam"}, lines, out, schema.DailyPrompt)
	if !kept(c, "Atlas 的对账模块由 Blake 负责，向 Casey 汇报") {
		t.Errorf("a person's word about a known project is not kept: %v %v", run.Counts, c.St.S.Rejections)
	}
	for claim, want := range map[string]string{"支付网关下周三迁移到新集群": verify.NotAboutOwner, "发布挪到周四": verify.NotAboutOwner, "Atlas 的发布窗口是周五": verify.AssistantSource} {
		if got := rejected(c, claim); got != want {
			t.Errorf("%s: rejected %q, want %q", claim, got, want)
		}
	}
}
