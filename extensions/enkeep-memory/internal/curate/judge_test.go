package curate

import (
	"context"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// fakeModel answers each call with what fn returns.
type fakeModel func(r model.Request) string

func (f fakeModel) Complete(_ context.Context, r model.Request) (model.Response, error) {
	return model.Response{Text: f(r), Model: "fake"}, nil
}

func jwindow(c *Curator, lines []string) (*Unit, *verify.Window) {
	u := &Unit{Ref: "chats/g", Entity: "chats", Meta: kbsrc.Meta{ID: "g", Kind: "group", Name: "G"}, Day: "2026-11-03"}
	shown := map[int]bool{}
	for i, s := range lines {
		l := voice.Relabel(norm.Parse(3+i, s))
		u.Lines = append(u.Lines, l)
		shown[l.N] = true
	}
	return u, c.window(u, shown, []string{"Sam"})
}

func judgement(claim, trigger, choice, why string, evs ...schema.Evidence) schema.Judgement {
	return schema.Judgement{Claim: claim, Trigger: trigger, Choice: choice, Why: why, Dimension: "决策", Evidence: evs, Confidence: "high"}
}

func jev(n int, q string) schema.Evidence { return schema.Evidence{N: n, Quote: q} }

// A judgement rests on the owner's own lines: others' lines are not kept as its evidence, and the gate throws away
// what no own line supports, a bare agreement, a choice that says nothing, a claim that does not stand on its quotes,
// money, and anything from a background group.
func TestJudgementGate(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	lines := []string{
		"10:00 Avery: 整体涨了 20%，可以全量了",              // 3: someone else
		"10:01 我: 先看分人群的数据，整体涨可能是某一类用户拉的，拆开没问题再全量", // 4: the owner
		"10:02 我: 好的", // 5: bare agreement
		"10:03 我: 这个先放一放，今天先发 A",                // 6
		"10:04 我: 工资卡入账 12000 元，先拆分人群再决定",       // 7: money
		"10:05 我: 最牛的模型拿来设计框架和提出问题，实现可以交给次旗舰模型", // 8
	}
	u, w := jwindow(c, lines)
	js := []schema.Judgement{
		judgement("看到整体指标上涨时，先拆分人群确认，再决定全量", "同事以整体指标上涨为由提议全量", "先按人群拆开看数据，没问题再全量", "整体涨可能是某一类用户拉的",
			jev(3, "整体涨了 20%"), jev(4, "先看分人群的数据")), // cites another person's line too
		judgement("只引别人的话", "别人提议", "照着别人说的做事情吧", "", jev(3, "可以全量了")),
		judgement("附和", "对方提议后", "对方说什么就做什么吧", "", jev(5, "好的")),
		judgement("泛泛", "做事时", "非常注重质量", "", jev(6, "先放一放")),
		judgement("引文对不上", "遇到评审时", "把代码逐行重写一遍再说", "", jev(6, "今天先发 A")),
		judgement("钱的事", "拆分", "先拆分人群再决定", "", jev(7, "工资卡入账 12000 元")),
		judgement("模型分工", "任务能拆成定方向和照着做", "最强模型管框架和问题，实现交给次旗舰模型", "", jev(8, "最牛的模型拿来设计框架和提出问题")),
	}
	done := &store.Done{Key: "k", Prompt: schema.DailyPrompt}
	run := &store.Run{Counts: map[string]int{}}
	c.ingestJudgements(w, u, triage.TierOwn, js, done, run)
	got := map[string]string{}
	for _, r := range c.St.S.Rejections {
		got[r.Claim] = r.Code
	}
	for claim, want := range map[string]string{"只引别人的话": verify.NotAboutOwner, "附和": verify.AssistantSource, "泛泛": judgementGeneric,
		"引文对不上": judgementUngrounded, "钱的事": verify.PrivateFinance} {
		if got[claim] != want {
			t.Errorf("%s: rejected %q, want %q", claim, got[claim], want)
		}
	}
	if len(c.St.S.Judgements) != 2 {
		t.Fatalf("kept %d judgements, want 2: %+v (rejections %v)", len(c.St.S.Judgements), c.St.S.Judgements, got)
	}
	for _, j := range c.St.S.Judgements {
		for _, e := range j.Evidence {
			if e.Role != voice.RoleUser || e.Speaker != norm.Me {
				t.Errorf("%s rests on a line that is not the owner's: %+v", j.Claim, e)
			}
		}
	}
	// A background group gives no judgements.
	before := len(c.St.S.Judgements)
	c.ingestJudgements(w, u, triage.TierBackground, js[6:], done, run)
	if len(c.St.S.Judgements) != before || got["模型分工"] != "" {
		t.Errorf("a background unit gave a judgement")
	}
	if last := c.St.S.Rejections[len(c.St.S.Rejections)-1]; last.Code != verify.TierNotAllowed {
		t.Errorf("background: %+v", last)
	}
	// The same answer ingested again changes nothing (replay).
	c.ingestJudgements(w, u, triage.TierOwn, js[:1], done, run)
	if len(c.St.S.Judgements) != before {
		t.Errorf("replay added a judgement")
	}
}

// putJudgement stores a judgement as the digest would have kept it.
func putJudgement(c *Curator, id, chat, day, claim, choice, why, rejected, quote string) {
	c.St.S.Judgements[id] = &store.Judgement{ID: id, Claim: claim, Trigger: "遇到" + claim, Choice: choice, Why: why, Rejected: rejected, Dimension: "决策",
		Evidence: []store.Evidence{{Chat: chat, Day: day, N: 3, Speaker: norm.Me, Role: voice.RoleUser, Quote: quote}}, Confidence: "high", Chat: chat, Day: day}
}
