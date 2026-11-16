package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// The old judgement layer, migrated, is on the 记忆 tab as entries: a pattern is a class counted from its judgements,
// and its page shows the judgements under it with the owner's own words; the legacy pages still open.
func TestJudgementTab(t *testing.T) {
	fx, err := kbsrc.LoadFixture("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := st.S
	for i, day := range []string{"2026-10-28", "2026-11-04", "2026-11-05"} {
		id := "j-" + string(rune('a'+i))
		chat := []string{"chats/a", "chats/b", "chats/a"}[i]
		s.Judgements[id] = &store.Judgement{ID: id, Claim: "整体指标上涨时先拆分人群", Trigger: "整体指标上涨", Choice: "先按人群拆开看", Why: "整体涨可能是某一类用户拉的", Rejected: "直接全量",
			Dimension: "决策", Chat: chat, Day: day, Pattern: "p-001", Confidence: "high",
			Evidence: []store.Evidence{{Chat: chat, Day: day, N: 4, Speaker: "我", Role: "user", Quote: "先看分人群的数据，拆开没问题再全量"}}}
	}
	for i, day := range []string{"2026-10-29", "2026-11-03", "2026-11-06"} {
		id := "j-s" + string(rune('a'+i))
		chat := []string{"chats/c", "chats/b", "chats/c"}[i]
		s.Judgements[id] = &store.Judgement{ID: id, Claim: "头部案例的结论要抽样", Trigger: "头部案例", Choice: "要求随机抽样看整体", Why: "头部集中会让结论偏", Dimension: "思考", Chat: chat, Day: day, Pattern: "p-003", Confidence: "high",
			Evidence: []store.Evidence{{Chat: chat, Day: day, N: 5, Speaker: "我", Role: "user", Quote: "随机抽 case 可能是另一个画风"}}}
	}
	s.Patterns["p-003"] = &store.Pattern{ID: "p-003", Name: "先问样本有没有偏", Dimension: "思考", Definition: "头部案例的结论要随机抽样再信"}
	s.Patterns["p-001"] = &store.Pattern{ID: "p-001", Name: "先拆分再拍板", Dimension: "决策", Definition: "整体指标上涨时先按人群拆开看，没问题再全量"}
	s.Patterns["p-002"] = &store.Pattern{ID: "p-002", Name: "种子模式", Dimension: "思考", Definition: "还没有证据", Status: "seed", Seed: true}
	s.Traits["t-001"] = &store.Trait{ID: "t-001", Name: "证据先于结论", Definition: "先审样本和前提", Patterns: []string{"p-001", "p-003"}, Status: "candidate"}
	a := &app{c: &curate.Curator{Src: fx, St: st, Cfg: curate.Defaults(), Loc: time.UTC, Live: &curate.Live{}}}
	st.Lock()
	if _, err := a.c.Migrate(); err != nil {
		t.Fatal(err)
	}
	a.c.Recount()
	a.c.Publish()
	st.Unlock()
	ctx := context.Background()
	home := func(p page) string {
		return within(t, "the judgement tab", func() (any, error) { return a.home(ctx, uiReq(p, "")) })
	}
	list := home(page{Tab: "memory"})
	for _, want := range []string{"整体指标上涨时先按人群拆开看", "3 次", "稳定"} {
		if !strings.Contains(list, want) {
			t.Errorf("the tab does not show %q: %s", want, list)
		}
	}
	var pid string
	for id, e := range st.S.Entries {
		if e.Text == "整体指标上涨时先按人群拆开看，没问题再全量" {
			pid = id
		}
	}
	page1 := home(page{Tab: "memory", Open: "e:" + pid})
	for _, want := range []string{"先看分人群的数据，拆开没问题再全量", "整体指标上涨时先拆分人群", "同类情境出现 3 次", "3 个活跃天"} {
		if !strings.Contains(page1, want) {
			t.Errorf("the entry page does not show %q: %s", want, page1)
		}
	}
	home(page{Tab: "judgement"})
	home(page{Tab: "judgement", Open: "t-001"})
	home(page{Tab: "judgement", Open: "p-404"})
}
