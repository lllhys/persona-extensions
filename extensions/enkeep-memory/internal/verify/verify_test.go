package verify

import (
	"slices"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
)

func window() *Window {
	w := &Window{Chat: "chats/c", Day: "2026-11-03", Kind: "group", All: map[int]norm.Line{}, Shown: map[int]bool{}, Owner: []string{"Sam"}}
	for _, l := range []norm.Line{
		norm.Parse(3, "10:00 Avery: 发布窗口定了：下周三晚上八点"),
		norm.Parse(4, "10:01 构建机器人: nightly 构建成功，版本 1.2.3"),
		norm.Parse(5, "10:02 Blake: 我来负责回滚预案"),
		norm.Parse(6, "10:03 Casey: 这一行没有展示给模型"),
	} {
		w.All[l.N] = l
		if l.N != 6 {
			w.Shown[l.N] = true
		}
	}
	return w
}

func TestEvidenceCodes(t *testing.T) {
	w := window()
	cases := []struct {
		ev   []schema.Evidence
		code string
	}{
		{nil, NoEvidence},
		{[]schema.Evidence{{N: 9, Quote: "发布窗口定了"}}, AnchorNotFound},
		{[]schema.Evidence{{N: 6, Quote: "没有展示给模型"}}, OutOfScope},
		{[]schema.Evidence{{N: 3, Quote: "发布窗口改了"}}, QuoteNotFound},
		{[]schema.Evidence{{N: 3, Quote: "我来负责回滚预案"}}, QuoteWrongLine},
		{[]schema.Evidence{{N: 3, Quote: "发布窗口定了", Speaker: "Blake"}}, QuoteWrongSpeaker},
		{[]schema.Evidence{{N: 3, Quote: "发布窗口定了", Speaker: "Avery"}}, ""},
	}
	for _, c := range cases {
		if _, code, _ := w.Evidence(c.ev); code != c.code {
			t.Errorf("%+v: %q, want %q", c.ev, code, c.code)
		}
	}
	if _, _, flags := w.Evidence([]schema.Evidence{{N: 4, Quote: "nightly 构建成功"}}); len(flags) != 1 || flags[0] != FromBot {
		t.Errorf("bot line: %v", flags)
	}
}

func TestItemFlags(t *testing.T) {
	w := window()
	_, rej, flags := w.Item(schema.Item{Claim: "发布在 2026-11-11 晚上", Evidence: []schema.Evidence{{N: 3, Quote: "下周三晚上八点"}}, Confidence: "high"})
	if rej != "" || len(flags) != 0 {
		t.Errorf("next Wednesday, 8 days on: %q %v", rej, flags)
	}
	_, _, flags = w.Item(schema.Item{Claim: "发布在 2027-03-01", Evidence: []schema.Evidence{{N: 3, Quote: "下周三晚上八点"}}, Confidence: "high"})
	if len(flags) == 0 || flags[0] != DateMismatch {
		t.Errorf("a date months away from 'next Wednesday': %v", flags)
	}
	_, _, flags = w.Item(schema.Item{Claim: "我答应了回滚预案", Evidence: []schema.Evidence{{N: 5, Quote: "我来负责回滚预案"}}, Confidence: "medium"})
	if len(flags) == 0 || flags[0] != SelfAmbiguous {
		t.Errorf("'I' when the owner did not speak: %v", flags)
	}
	if _, rej, _ := w.Item(schema.Item{Claim: "x", Evidence: []schema.Evidence{{N: 3, Quote: "发布窗口定了"}}, Confidence: "sure"}); rej != Schema {
		t.Errorf("bad confidence: %q", rej)
	}
}

func TestAssertionSpeakerBound(t *testing.T) {
	w := window()
	a := schema.Assertion{S: "Avery", P: "committed", O: "回滚预案", Evidence: []schema.Evidence{{N: 5, Quote: "我来负责回滚预案"}}, Confidence: "high"}
	if _, rej, _ := w.Assertion(a, []string{"Avery"}, []string{"回滚预案"}, true); rej != SpeakerMismatch {
		t.Errorf("Avery promising on Blake's line: %q", rej)
	}
	a.S = "Blake"
	if _, rej, _ := w.Assertion(a, []string{"Blake"}, []string{"回滚预案"}, true); rej != "" {
		t.Errorf("Blake on Blake's line: %q", rej)
	}
	a2 := schema.Assertion{S: "Dana", P: "owns", O: "Kafka", Evidence: []schema.Evidence{{N: 3, Quote: "发布窗口定了"}}, Confidence: "high"}
	if _, rej, _ := w.Assertion(a2, []string{"Dana"}, []string{"Kafka"}, true); rej != EntityNotInEvidence {
		t.Errorf("neither end in the line: %q", rej)
	}
}

// "用户" in other people's lines is not the owner being named, even when the hub calls its owner "用户" and the
// configuration lists "我" and "owner": SELF_AMBIGUOUS still flags a claim about the owner, and a relation whose
// only link to the owner is that word does not pass.
func TestOwnerNamedIgnoresGenericNames(t *testing.T) {
	w := &Window{Chat: "chats/g", Day: "2026-11-03", Kind: "group", All: map[int]norm.Line{}, Shown: map[int]bool{},
		Owner: []string{"用户", "我", "owner", "quillmoss"}}
	for _, l := range []norm.Line{
		norm.Parse(3, "10:00 Avery: 用户反馈登录慢，我来跟进"),
		norm.Parse(4, "10:01 Blake: the owner of the gateway is out"),
	} {
		w.All[l.N] = l
		w.Shown[l.N] = true
	}
	if w.OwnerNamed() {
		t.Fatal("generic words are not the owner's name")
	}
	_, _, flags := w.Item(schema.Item{Claim: "用户负责跟进登录慢", Evidence: []schema.Evidence{{N: 3, Quote: "用户反馈登录慢"}}, Confidence: "high"})
	if !slices.Contains(flags, SelfAmbiguous) {
		t.Fatalf("a claim about the owner who neither spoke nor was named: %v", flags)
	}
	a := schema.Assertion{S: "我", P: "owns", O: "Kafka", Evidence: []schema.Evidence{{N: 3, Quote: "用户反馈登录慢"}}, Confidence: "high"}
	if _, rej, _ := w.Assertion(a, []string{"我", "用户", "owner"}, []string{"Kafka"}, true); rej != EntityNotInEvidence {
		t.Fatalf("the owner is not in a line that only says 用户: %q", rej)
	}
	w.All[5] = norm.Parse(5, "10:02 Casey: @quillmoss 你看下")
	w.Shown[5] = true
	if !w.OwnerNamed() {
		t.Fatal("an @-mention names the owner")
	}
}
