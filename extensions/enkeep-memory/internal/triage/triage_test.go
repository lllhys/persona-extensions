package triage

import (
	"slices"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
)

var cfg = Config{ActivityDays: 3, MentionWindow: 2, AIMinRelevance: 60}

func lines(ss ...string) []norm.Line {
	var out []norm.Line
	for i, s := range ss {
		out = append(out, norm.Parse(i+3, s))
	}
	return out
}

func TestRules(t *testing.T) {
	owner := []string{"Sam Rivera", "Sam"}
	cases := []struct {
		name   string
		chat   Chat
		cfg    Config
		rule   string
		action string
	}{
		{"dm", Chat{Meta: kbsrc.Meta{ID: "x-1", Kind: "dm"}, Lines: lines("10:00 A: hi there")}, cfg, RDM, Include},
		{"one sender's group", Chat{Meta: kbsrc.Meta{ID: "x-2", Kind: "group", Relevance: 10}, Lines: lines("10:00 A: lunch?")}, cfg, RNoise, Drop},
		{"strangers' group", Chat{Meta: kbsrc.Meta{ID: "x-2b", Kind: "group", Relevance: 10}, Lines: lines("10:00 A: lunch?", "10:01 B: sure")}, cfg, RNoIntersect, Drop},
		{"owner spoke in the last days", Chat{Meta: kbsrc.Meta{ID: "x-3", Kind: "group"}, Lines: lines("10:00 A: lunch?"), OwnerRecently: true}, cfg, RActive, Include},
		{"ranked by the hub, one sender", Chat{Meta: kbsrc.Meta{ID: "x-4", Kind: "group", Relevance: 70}, Lines: lines("10:00 A: lunch?")}, cfg, RNoise, Drop},
		{"at-all only", Chat{Meta: kbsrc.Meta{ID: "x-5", Kind: "group"}, Lines: lines("10:00 A: @所有人 停电")}, cfg, RAllOnly, DigestOnly},
		{"noreply mail", Chat{Meta: kbsrc.Meta{ID: "mail-1", Kind: "mail", Name: "noreply@example.com"}, Lines: lines("08:00 noreply@example.com: shipped")}, cfg, MBulk, Drop},
		{"bulk domain", Chat{Meta: kbsrc.Meta{ID: "mail-2", Kind: "mail", Name: "deals@shop.example"}, Lines: lines("08:00 deals@shop.example: sale")},
			Config{ActivityDays: 3, BulkDomains: []string{"shop.example"}}, MBulk, Drop},
		{"other mail", Chat{Meta: kbsrc.Meta{ID: "mail-3", Kind: "mail", Name: "a@b.example"}, Lines: lines("08:00 A: contract")}, cfg, MOther, AI},
		{"owner's mail thread", Chat{Meta: kbsrc.Meta{ID: "mail-4", Kind: "mail", Mine: true}, Lines: lines("08:00 A: contract")}, cfg, MMine, Include},
		{"blocked", Chat{Meta: kbsrc.Meta{ID: "x-6", Kind: "dm", Name: "Spam Room"}, Lines: lines("10:00 A: hi")},
			Config{BlockChats: []string{"Spam Room"}}, RBlock, Drop},
		{"pinned", Chat{Meta: kbsrc.Meta{ID: "x-7", Kind: "group", Name: "Quiet"}, Lines: lines("10:00 A: hi")},
			Config{PinChats: []string{"x-7"}}, RPin, Include},
		{"meeting", Chat{Entity: "meetings", Meta: kbsrc.Meta{ID: "m"}, Lines: lines("decided")}, cfg, DMeeting, Include},
	}
	for _, c := range cases {
		c.chat.OwnerNames = owner
		d := Decide(c.chat, c.cfg)
		if d.Rule != c.rule || d.Action != c.action {
			t.Errorf("%s: %s %s, want %s %s", c.name, d.Rule, d.Action, c.rule, c.action)
		}
	}
}

func TestMentionWindowAndPrivate(t *testing.T) {
	var ls []string
	for i := 0; i < 20; i++ {
		ls = append(ls, "10:00 A: filler line")
	}
	ls[10] = "10:10 B: @Sam 看一下"
	ls[12] = "10:12 C: 工资条在附件"
	d := Decide(Chat{Meta: kbsrc.Meta{ID: "g", Kind: "group"}, Lines: lines(ls...), OwnerNames: []string{"Sam"}},
		Config{ActivityDays: 3, MentionWindow: 2, Private: []string{"工资"}})
	if d.Action != Window || len(d.Shown) != 5 { // 2 readable lines before, the mention, 2 readable after (the private one skipped)
		t.Fatalf("%s %d %v", d.Action, len(d.Shown), d.Shown)
	}
	if d.Shown[15] {
		t.Fatal("a private line is never shown")
	}
}

func TestSmallDay(t *testing.T) {
	ds := []Decision{{Rule: RSilent, Tokens: 100}, {Rule: MBulk, Tokens: 1e6}}
	if !SmallDay(ds, Config{AdmitAllBelow: 1000}) {
		t.Fatal("bulk mail does not count towards the size")
	}
	if SmallDay(ds, Config{AdmitAllBelow: 50}) {
		t.Fatal("above the threshold")
	}
}

// The hub names its owner "用户" by default: a group where other people talk about "用户" (end users) all day is
// not a group that mentions the owner. Only "@name" or the owner's real name counts, and generic words in the
// owner's names never match, even when the configuration adds them.
func TestR3IgnoresGenericOwnerNames(t *testing.T) {
	quiet := []string{
		"10:00 A: 用户反馈登录慢",
		"10:01 B: 用户说页面白屏",
		"10:02 C: 这个用户量很大",
		"10:03 A: 用户体验要优化",
		"10:04 B: 老用户也受影响",
		"10:05 C: the owner of this service is on leave",
		"10:06 A: 我先看看日志",
	}
	group := func(names []string, extra ...string) Decision {
		return Decide(Chat{Meta: kbsrc.Meta{ID: "g", Kind: "group", Relevance: 10}, Lines: lines(append(slices.Clone(quiet), extra...)...),
			OwnerNames: names}, cfg)
	}
	if d := group([]string{"用户", "quillmoss"}); d.Rule == RMention {
		t.Fatalf("\"用户\" five times is not a mention of the owner: %s %s", d.Rule, d.Reason)
	}
	if d := group([]string{"用户", "quillmoss", "owner", "我"}); d.Rule == RMention {
		t.Fatalf("generic owner_aliases (owner, 我) never match: %s", d.Rule)
	}
	if d := group([]string{"用户", "quillmoss"}, "10:07 B: @quillmoss 帮忙看下"); d.Rule != RMention || d.Action != Window {
		t.Fatalf("an @-mention of the owner is R3: %s %s", d.Rule, d.Action)
	}
	if d := group([]string{"用户", "quillmoss"}, "10:07 B: 这个问题问问 quillmoss 吧"); d.Rule != RMention {
		t.Fatalf("the owner's real name written out is R3: %s", d.Rule)
	}
}

func TestPersonaConversationIncluded(t *testing.T) {
	for _, meta := range []kbsrc.Meta{
		{ID: "persona-0a1b2c3d", Source: "persona", Kind: "assistant", Mine: true},
		{ID: "persona-9f8e7d6c"}, // the id alone says it
	} {
		d := Decide(Chat{Entity: "chats", Meta: meta, Lines: lines("10:00 我: 以后先跑测试再提交", "10:01 助手: 好的")}, cfg)
		if d.Rule != RPersona || d.Action != Include || d.Score < 90 || d.Reason != "你和 Persona 助手的对话" {
			t.Errorf("%s: %s %s %.0f %q", meta.ID, d.Rule, d.Action, d.Score, d.Reason)
		}
		if len(d.Shown) != 2 {
			t.Errorf("%s: shown %v, want every line", meta.ID, d.Shown)
		}
	}
}
