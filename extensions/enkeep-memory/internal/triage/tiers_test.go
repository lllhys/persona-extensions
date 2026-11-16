package triage

import (
	"fmt"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
)

func tiered() Config {
	return Config{ActivityDays: 3, MentionWindow: 5, AIMinRelevance: 60, Tiers: true, EngagedDays: 30, NoiseMembers: 200}
}

func groupDay(meta kbsrc.Meta, lines ...string) Chat {
	if meta.Kind == "" {
		meta.Kind = "group"
	}
	var ls []norm.Line
	for i, s := range lines {
		ls = append(ls, norm.Parse(i+1, s))
	}
	return Chat{Entity: "chats", Meta: meta, Lines: ls, OwnerNames: []string{"用户", "demo-owner"}}
}

func people(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("p%d", i)
	}
	return out
}

// With tiers each unit gets one: the user's own conversations are own; groups the user takes part in (spoke today,
// within activity_days, or within engaged_days) and mention windows are engaged; a group the user is quiet in is read
// for the lines that intersect with the user (3.3), whatever its size or flags; a day that is only feeds, alerts or
// one sender's announcements is skipped as noise, and one with nothing intersecting is dropped.
func TestTiersByRule(t *testing.T) {
	cfg := tiered()
	chat := "10:00 Avery: 下周把灰度扩到一半\n"
	talk := []string{"10:00 Avery: 下周把灰度扩到一半", "10:02 Blake: 好，我来准备回滚方案"}
	cases := []struct {
		name       string
		c          Chat
		rule, tier string
		action     string
	}{
		{"dm", Chat{Entity: "chats", Meta: kbsrc.Meta{ID: "dm", Kind: "dm"}, Lines: []norm.Line{norm.Parse(1, chat)}}, RDM, TierOwn, Include},
		{"code review", Chat{Entity: "chats", Meta: kbsrc.Meta{ID: "github-acme-app-7", Source: "github", Kind: "pr"}, Lines: []norm.Line{norm.Parse(1, "10:00 Avery: LGTM")}}, GCode, TierCode, Include},
		{"persona", Chat{Entity: "chats", Meta: kbsrc.Meta{ID: "persona-1", Source: "persona", Kind: "assistant"}, Lines: []norm.Line{norm.Parse(1, "10:00 我: 以后先给结论")}}, RPersona, TierOwn, Include},
		{"spoke today", groupDay(kbsrc.Meta{ID: "g1"}, "10:00 我: 我来看下", talk[0]), RActive, TierEngaged, Include},
		{"spoke 2 days ago", func() Chat {
			c := groupDay(kbsrc.Meta{ID: "g2"}, talk...)
			c.OwnerRecently, c.OwnerLast = true, 2
			return c
		}(), RActive, TierEngaged, Include},
		{"spoke 12 days ago", func() Chat { c := groupDay(kbsrc.Meta{ID: "g3"}, talk...); c.OwnerLast = 12; return c }(), RParticipant, TierEngaged, Include},
		{"named", groupDay(kbsrc.Meta{ID: "g4"}, "10:00 Avery: @demo-owner 你看下这个", talk[1]), RMention, TierEngaged, Window},
		{"quiet, two strangers", groupDay(kbsrc.Meta{ID: "g5", People: people(12)}, talk...), RNoIntersect, "", Drop},
		{"quiet, hub-ranked strangers", groupDay(kbsrc.Meta{ID: "g6", Relevance: 90, People: people(12)}, talk...), RNoIntersect, "", Drop},
		{"quiet, a contact chatting", withContacts(groupDay(kbsrc.Meta{ID: "g5b", People: people(12)}, talk...)), RIntersect, TierEngaged, Window},
		{"a feed", groupDay(kbsrc.Meta{ID: "g7"}, "08:00 Daily News: 一", "08:05 Daily News: 二"), RNoise, "", Drop},
		{"alerts only", groupDay(kbsrc.Meta{ID: "g8"}, "08:00 告警机器人: CPU 高", "08:05 Alert Bot: 恢复"), RNoise, "", Drop},
		{"a big crowd of strangers", groupDay(kbsrc.Meta{ID: "g9", People: people(300)}, talk...), RNoIntersect, "", Drop},
		{"a big crowd with a contact", withContacts(groupDay(kbsrc.Meta{ID: "g9b", People: people(300)}, talk...)), RIntersect, TierEngaged, Window},
		{"a big crowd naming the user", groupDay(kbsrc.Meta{ID: "g10", People: people(300)}, "10:00 Avery: @demo-owner 这个归你吗", talk[1]), RMention, TierEngaged, Window},
		{"muted, spoke last week", func() Chat {
			c := withContacts(groupDay(kbsrc.Meta{ID: "g11", Muted: true, People: people(12)}, talk...))
			c.OwnerLast = 6
			return c
		}(), RIntersect, TierEngaged, Window},
		{"folded, spoke today", groupDay(kbsrc.Meta{ID: "g12", Collapsed: true}, "10:00 我: 我来", talk[0]), RActive, TierEngaged, Include},
		{"done and quiet", groupDay(kbsrc.Meta{ID: "g13", Done: true, People: people(5)}, talk...), RNoIntersect, "", Drop},
	}
	for _, tc := range cases {
		d := Decide(tc.c, cfg)
		if d.Rule != tc.rule || d.Tier != tc.tier || d.Action != tc.action {
			t.Errorf("%s: %s %s %q, want %s %s %q (%s)", tc.name, d.Rule, d.Action, d.Tier, tc.rule, tc.action, tc.tier, d.Reason)
		}
	}
}

// With tiers off the rules are those before tiers, and no unit has a tier.
func TestTiersOffKeepTheOldRules(t *testing.T) {
	cfg := tiered()
	cfg.Tiers = false
	talk := []string{"10:00 Avery: 下周把灰度扩到一半", "10:02 Blake: 好，我来准备回滚方案"}
	for _, tc := range []struct {
		c    Chat
		rule string
	}{
		{func() Chat { c := groupDay(kbsrc.Meta{ID: "g3"}, talk...); c.OwnerLast = 12; return c }(), RNoIntersect},
		{groupDay(kbsrc.Meta{ID: "g6", Relevance: 90}, talk...), RNoIntersect},
		{withContacts(groupDay(kbsrc.Meta{ID: "g6b", Relevance: 90}, talk...)), RIntersect},
		{groupDay(kbsrc.Meta{ID: "g7"}, "08:00 Daily News: 一"), RNoise},
		{groupDay(kbsrc.Meta{ID: "g11", Muted: true}, "10:00 我: 我来"), RActive},
	} {
		if d := Decide(tc.c, cfg); d.Rule != tc.rule || d.Tier != "" {
			t.Errorf("%s: %s %q, want %s", tc.c.Meta.ID, d.Rule, d.Tier, tc.rule)
		}
	}
}

// withContacts resolves a group day's speakers by name and puts every non-user speaker on the intersection list.
func withContacts(c Chat) Chat {
	c.Who, c.Contacts = map[int]string{}, map[string]bool{}
	for _, l := range c.Lines {
		if l.Speaker == norm.Me {
			c.Who[l.N] = "self"
			continue
		}
		c.Who[l.N] = "person:" + l.Speaker
		c.Contacts["person:"+l.Speaker] = true
	}
	return c
}

// In a group the user is quiet in, chit-chat is kept when its speaker is resolved and has dealings with the user, or
// when it names the user, a project of the user's or a contact; an unresolved speaker and a stranger talking about
// nothing of the user's are dropped. Bots and one sender's announcements stay out.
func TestQuietGroupKeepsIntersectingChitChat(t *testing.T) {
	c := groupDay(kbsrc.Meta{ID: "q1", People: people(400)},
		"10:00 person-b: 周末一起去爬山吗",         // 1: a contact's chit-chat: kept
		"10:01 same-name: 我也去",             // 2: unresolved (two members share the name): dropped
		"10:02 stranger: 今天食堂的菜不错",         // 3: a stranger, nothing of the user's: dropped
		"10:03 stranger: project-x 下周要灰度",  // 4: a stranger naming the user's project: kept
		"10:04 stranger: 问下 person-b 有没有空", // 5: a stranger naming a contact: kept
		"10:05 Alert Bot: CPU 高",           // 6: a bot: dropped
	)
	c.Who = map[int]string{1: "person:b", 2: "", 3: "person:s", 4: "person:s", 5: "person:s", 6: ""}
	c.Contacts = map[string]bool{"person:b": true}
	c.Topics = []string{"project-x", "person-b"}
	d := Decide(c, tiered())
	if d.Rule != RIntersect || d.Action != Window || d.Tier != TierEngaged {
		t.Fatalf("%s %s %s (%s)", d.Rule, d.Action, d.Tier, d.Reason)
	}
	for n, want := range map[int]bool{1: true, 2: false, 3: false, 4: true, 5: true, 6: false} {
		if d.Shown[n] != want {
			t.Errorf("line %d shown %v, want %v", n, d.Shown[n], want)
		}
	}
}
