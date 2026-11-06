package verify

import (
	"testing"

	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/schema"
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
