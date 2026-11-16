package main

import (
	"context"
	"slices"
	"strings"
	"time"

	"fmt"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// v4Tab says whether a tab is one of the v4 tabs.
func (a *app) v4Tab(tab string) bool {
	return slices.ContainsFunc(v4Tabs(tr{}), func(x wire.Tab) bool { return x.Value == tab })
}

// v4Opens says whether an open page is one of the v4 pages.
func (a *app) v4Opens(open string) bool {
	return open == "onboarding" || open == "k2" || strings.HasPrefix(open, "e:") || strings.HasPrefix(open, "card:") || strings.HasPrefix(open, "types:")
}

func (a *app) v4Open(t tr, s page) *wire.View {
	st, done := a.read()
	defer done()
	switch {
	case s.Open == "onboarding":
		return a.onboardingPage(t, st)
	case s.Open == "k2":
		return a.k2Page(t, st, s)
	case strings.HasPrefix(s.Open, "e:"):
		return a.entryPage(t, st, strings.TrimPrefix(s.Open, "e:"))
	case strings.HasPrefix(s.Open, "card:"):
		return a.cardPage(t, st, s, strings.TrimPrefix(s.Open, "card:"))
	case strings.HasPrefix(s.Open, "types:"):
		return a.typesPage(t, st, entry.Facet(strings.TrimPrefix(s.Open, "types:")))
	}
	return &wire.View{Title: t.s("不见了", "Gone"), Sections: []wire.Section{{Items: []wire.Component{empty(t.s("这一页不见了", "This page is gone"))}}}}
}

// v4List draws a v4 tab. The review and reports tabs are the older pages' own.
func (a *app) v4List(ctx context.Context, t tr, s page) *wire.View {
	if s.Tab == tabReview || s.Tab == tabReports {
		return a.listPage(s)
	}
	st, done := a.read()
	defer done()
	v := &wire.View{Title: t.s("enkeep 记忆", "enkeep memory")}
	if head := a.headItems(t, st); len(head) > 0 {
		v.Sections = append(v.Sections, wire.Section{Items: head})
	}
	switch s.Tab {
	case tabStart:
		v.Sections = append(v.Sections, a.startPage(ctx, t, st)...)
	case tabFacets:
		secs, filter := a.facetPage(t, st, s)
		v.Sections, v.Filter = append(v.Sections, secs...), filter
	case tabPeople:
		v.Sections = append(v.Sections, a.peopleTab(t, st)...)
	case tabCheck:
		secs, filter := a.checkPage(t, st, s)
		v.Sections, v.Filter = append(v.Sections, secs...), filter
	case tabSwitches:
		v.Sections = append(v.Sections, a.switchesPage(t, st)...)
	case tabOlder:
		var rows []wire.Component
		names := map[string][2]string{"rules": {"规矩", "Rules"}, "judgement": {"思路", "Patterns"}, "memory": {"记忆", "Memory"},
			"fact": {"事实", "Facts"}, "knowledge": {"知识与做法", "Knowledge and practices"}, "graph": {"图谱", "Graph"}}
		for _, tab := range legacyTabs {
			if tab == "graph" && !a.c.Cfg.Graph {
				continue
			}
			n := names[tab]
			rows = append(rows, row(t.s(n[0], n[1]), "", nil, ext.Open("open:"+tab, "", page{Tab: tab}, t.s(n[0], n[1]))))
		}
		v.Sections = append(v.Sections, wire.Section{Title: t.s("旧版页面", "Older pages"),
			Footer: t.s("v4 之前的整理结果，只在这里看；助手读的是上面各个出口里的内容。", "What the curator kept before v4, for looking only; the assistant reads what is in the exits."), Items: rows})
	}
	return v
}

// headItems: the run in progress, another task running, the last error.
func (a *app) headItems(t tr, st *store.State) []wire.Component {
	var out []wire.Component
	if p, ok := a.c.Live.Progress(); ok {
		out = append(out, toned(progressText(p, time.Now())+t.s("。下面是上次整理完的结果，跑完会更新。", ". Below is the last finished result; it updates when the run ends."), wire.ToneCaution))
	}
	if text, tone := a.pauseLine(t, st, time.Now()); text != "" {
		out = append(out, toned(text, tone))
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running != "" && !strings.HasPrefix(a.running, "daily") && !strings.HasPrefix(a.running, "weekly") {
		out = append(out, secondary(t.s("正在运行：", "Running: ")+a.running))
	}
	if a.lastErr != "" {
		out = append(out, toned(t.s("上次运行出错：", "The last run failed: ")+a.lastErr, wire.ToneDanger))
	}
	return out
}

// pauseLine says, while the passes wait for the model (State.ModelPause), why, since when and when they try again.
// After curate.PausedAfter failures in a row it says the curator is paused; before, that the model is briefly
// unavailable. "" when nothing waits.
// st is the state the page is drawn from (the caller holds its lock).
func (a *app) pauseLine(t tr, st *store.State, now time.Time) (string, string) {
	if st == nil || st.ModelPause == nil {
		return "", ""
	}
	cp := *st.ModelPause
	clock := func(rfc string) string {
		x, err := time.Parse(time.RFC3339, rfc)
		if err != nil {
			return rfc
		}
		return x.In(a.c.Loc).Format("01-02 15:04")
	}
	why := cp.Reason
	if why == "" {
		why = t.s("hub 没有给出原因", "the hub gave no reason")
	}
	if cp.What == "assistant" {
		// The assistant's computer was away: its conversations, one of the day's sources, could not be read.
		return fmt.Sprintf(t.s("助手的电脑没连上（%s），%s 这一天先不算读完，%s 再试。",
			"The assistant's computer is not connected (%s); %s is not counted as read and will be tried again at %s."),
			why, cp.Day, clock(cp.NextTry)), wire.ToneCaution
	}
	if cp.Fails < curate.PausedAfter {
		return fmt.Sprintf(t.s("模型暂时不可用（%s），%s 再试 %s 这一天。", "The model is briefly unavailable (%s); %s will be tried again at %s."),
			why, cp.Day, clock(cp.NextTry)), wire.ToneCaution
	}
	return fmt.Sprintf(t.s("模型不可用，暂停中：%s 起连续 %d 次调不到模型（%s）。没读完的日子不算完成，%s 自动再试，恢复后从 %s 接着跑。",
		"The model is unavailable; paused: since %s, %d tries in a row found no model (%s). Days not read are not counted as done; the next try is at %s, and the run goes on from %s when the model is back."),
		clock(cp.Since), cp.Fails, why, clock(cp.NextTry), cp.Day), wire.ToneDanger
}

// logf logs through the curator's logger when it has one.
func (a *app) logf(format string, args ...any) {
	if a.c != nil && a.c.Logf != nil {
		a.c.Logf(format, args...)
	}
}

// exitsDir is where the exit files and exits.json are: the data directory, which is the store's in a hub; a replay
// keeps them apart (the page subcommand's -data).
func (a *app) exitsDir() string {
	if a.exitDir != "" {
		return a.exitDir
	}
	return a.c.St.Dir
}
