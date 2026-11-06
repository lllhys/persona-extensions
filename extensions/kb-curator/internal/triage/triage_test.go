package triage

import (
	"testing"

	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/norm"
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
		{"silent group", Chat{Meta: kbsrc.Meta{ID: "x-2", Kind: "group", Relevance: 10}, Lines: lines("10:00 A: lunch?")}, cfg, RSilent, Drop},
		{"owner spoke in the last days", Chat{Meta: kbsrc.Meta{ID: "x-3", Kind: "group"}, Lines: lines("10:00 A: lunch?"), OwnerRecently: true}, cfg, RActive, Include},
		{"ranked by the hub", Chat{Meta: kbsrc.Meta{ID: "x-4", Kind: "group", Relevance: 70}, Lines: lines("10:00 A: lunch?")}, cfg, RHubRanked, AI},
		{"at-all only", Chat{Meta: kbsrc.Meta{ID: "x-5", Kind: "group"}, Lines: lines("10:00 A: @所有人 停电")}, cfg, RAllOnly, DigestOnly},
		{"Contributor mail", Chat{Meta: kbsrc.Meta{ID: "mail-1", Kind: "mail", Name: "Contributor@example.com"}, Lines: lines("08:00 Contributor@example.com: shipped")}, cfg, MBulk, Drop},
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
