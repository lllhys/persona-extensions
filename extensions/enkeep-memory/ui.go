package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// page is the main page's state.
type page struct {
	Tab   string `json:"tab,omitempty"`   // the v4 tabs (page_v4.go), or an older one: rules | judgement | memory | fact | knowledge | graph
	Open  string `json:"open,omitempty"`  // a proposal (K-…), an item (i-…), a node (n-… / me), a run; v4: e:<entry>, card:<person>, types:<facet>, onboarding
	Class string `json:"class,omitempty"` //
	Facet string `json:"facet,omitempty"` // the facets tab's facet
	Check string `json:"check,omitempty"` // the check page's part: u1…u8, f1
	Sub   string `json:"sub,omitempty"`   // the check part's card (U7) or source (U8)
	Show  string `json:"show,omitempty"`  // "reported": a card with its reported-only lines unfolded
}

var allTabs = []wire.Tab{{Value: "review", Title: "待确认"}, {Value: "rules", Title: "规矩"}, {Value: "judgement", Title: "思路"}, {Value: "memory", Title: "记忆"},
	{Value: "fact", Title: "事实"}, {Value: "knowledge", Title: "知识与做法"}, {Value: "graph", Title: "图谱"}, {Value: "reports", Title: "报告"}}

// tabsFor are the page's tabs (at most eight): the graph's only when the graph is on.
func tabsFor(graph bool) []wire.Tab {
	var out []wire.Tab
	for _, t := range allTabs {
		if t.Value != "graph" || graph {
			out = append(out, t)
		}
	}
	return out
}

var statusLabel = map[string]string{
	"candidate": "待确认", "active": "有效", "stale": "过时", "superseded": "被取代", "rejected": "已驳回", "merged": "已合并",
	"habitual": "稳定习惯", "stable": "稳定", "shifting": "在变", "deleted": "已删", "seed": "种子（还没有证据）", "observed": "偶现", "recurring": "反复", "established": "固定", "dormant": "休眠",
	"pending": "待确认", "later": "稍后", "accepted": "已合入", "auto": "自动合入", "expired": "已过期", "blocked": "等 hub 支持", "held": "被锁着", "undone": "已撤销",
}

var riskTone = map[string]string{"low": wire.TonePositive, "medium": wire.ToneCaution, "high": wire.ToneDanger}

func text(s string) wire.Component {
	return wire.Component{Type: wire.ComponentText, Text: s}
}

func secondary(s string) wire.Component {
	return wire.Component{Type: wire.ComponentText, Text: s, Style: wire.TextStyleSecondary}
}

func button(title, id, confirm string) wire.Component {
	return wire.Component{Type: wire.ComponentButton, Title: title, Action: &wire.Action{ID: id, Confirm: confirm}}
}

func (a *app) home(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	var s page
	if err := r.DecodeState(&s); err != nil {
		return nil, err
	}
	if s.Tab == "" {
		s.Tab = tabStart
	}
	if a.c == nil {
		return &wire.View{Title: "enkeep 记忆", Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: "还没准备好"}}}}}, nil
	}
	t := a.lang(r)
	var v *wire.View
	switch {
	case s.Open != "" && a.v4Opens(s.Open):
		v = a.v4Open(t, s)
	case s.Open == "" && a.v4Tab(s.Tab):
		v = a.v4List(ctx, t, s)
		v.Tabs = &wire.Tabs{Name: "tab", Value: s.Tab, Items: v4Tabs(t), Action: wire.Action{ID: "tab"}}
		if n := len(a.pending()); n > 0 {
			v.Tabs.Items[5].Badge = fmt.Sprint(n)
		}
	case strings.HasPrefix(s.Open, "K-"):
		v = a.proposalPage(s)
	case strings.HasPrefix(s.Open, "i-"):
		v = a.itemPage(s)
	case strings.HasPrefix(s.Open, "n-") || s.Open == "me":
		v = a.nodePage(s)
	case strings.HasPrefix(s.Open, "p-") || strings.HasPrefix(s.Open, "t-"):
		v = a.judgementPage(s)
	case strings.HasPrefix(s.Open, "r-"):
		v = a.rulePage(s)
	case s.Open != "":
		v = a.runPage(s)
	default:
		v = a.listPage(s)
		v.Tabs = &wire.Tabs{Name: "tab", Value: s.Tab, Items: tabsFor(a.c.Cfg.Graph), Action: wire.Action{ID: "tab"}}
		if n := len(a.pending()); n > 0 {
			v.Tabs.Items[0].Badge = fmt.Sprint(n)
		}
	}
	v.State = ext.EncodeState(s)
	if r.UIVersion > 0 && r.UIVersion < 3 && s.Open != "" {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{button(t.s("返回", "Back"), "back", "")}})
	}
	return v, nil
}

// lang is the page's language: the request's, then the hub's.
func (a *app) lang(r *ext.UIRequest) tr { return trOf(r.Locale, a.c.Lang) }

// read is the state the pages show: the last snapshot (curate.Live), never the store's own, whose lock a run holds
// for minutes while the hub gives up on a page after 20 seconds (a 502). Only a curator without a snapshot (one
// built by hand in a test) is read from the store, under its lock.
func (a *app) read() (*store.State, func()) {
	if s := a.c.Live.Snapshot(); s != nil && s.State != nil {
		return s.State, func() {}
	}
	a.c.St.Lock()
	return a.c.St.S, a.c.St.Unlock
}

func (a *app) listPage(s page) *wire.View {
	c := a.c
	st, done := a.read()
	defer done()
	v := &wire.View{Title: "enkeep 记忆"}
	head := wire.Section{}
	if c.Cfg.Shadow(st.Installed, time.Now()) {
		head.Items = append(head.Items, secondary(fmt.Sprintf("影子期（前 %d 天）：只提议、不改知识库。点「对」「不对」帮它校准，各类提议的准确率够了再开自动合入。", c.Cfg.ShadowDays)))
	}
	if p, ok := c.Live.Progress(); ok {
		head.Items = append(head.Items, wire.Component{Type: wire.ComponentText, Text: progressText(p, time.Now()) + "。下面是上次整理完的结果，跑完会更新。", Tone: wire.ToneCaution})
	}
	a.mu.Lock()
	if a.running != "" && !strings.HasPrefix(a.running, "daily") && !strings.HasPrefix(a.running, "weekly") {
		head.Items = append(head.Items, secondary("正在运行："+a.running))
	}
	if a.lastErr != "" {
		head.Items = append(head.Items, wire.Component{Type: wire.ComponentText, Text: "上次运行出错：" + a.lastErr, Tone: wire.ToneDanger})
	}
	a.mu.Unlock()
	if len(head.Items) > 0 {
		v.Sections = append(v.Sections, head)
	}
	var rows []wire.Component
	switch s.Tab {
	case "review":
		var shown []string
		for _, p := range st.Proposals {
			if p.Status != "pending" && p.Status != "later" && p.Status != "blocked" && p.Status != "held" && !(p.Status == "auto" && !p.Acked) {
				continue
			}
			rows = append(rows, wire.Component{Type: wire.ComponentRow, Title: short(p.Title, 60), Subtitle: curate.ProposalKinds[p.Kind].Title + " · " + p.ID,
				Badge: &wire.Badge{Text: statusLabel[p.Status], Tone: riskTone[p.Risk]}, Action: ext.Open("open:"+p.ID, "", page{Tab: s.Tab, Open: p.ID}, p.ID)})
			shown = append(shown, p.ID)
		}
		go c.MarkShown(shown) // expiry counts from the day a proposal was first shown
	case "fact", "practice", "knowledge":
		class := s.Tab
		if class == "knowledge" {
			class = "" // knowledge and practices share a tab
		}
		for _, it := range st.ItemsBy(class, func(it *store.Item) bool {
			return it.Status != "merged" && it.Status != "rejected" && (s.Tab != "knowledge" || it.Class != store.Fact)
		}) {
			if len(rows) >= 100 {
				break
			}
			sub := fmt.Sprintf("%s · %d 个来源", it.LastSeen, it.Sources)
			if len(it.Flags) > 0 {
				sub += " · 有标记"
			}
			rows = append(rows, wire.Component{Type: wire.ComponentRow, Title: short(it.Claim, 60), Subtitle: sub,
				Badge: &wire.Badge{Text: statusLabel[it.Status]}, Action: ext.Open("open:"+it.ID, "", page{Tab: s.Tab, Open: it.ID}, short(it.Claim, 20))})
		}
	case "graph":
		type nn struct {
			n *store.Node
			k int
		}
		var ns []nn
		for _, n := range st.Graph.Nodes {
			if n.MergedInto != "" {
				continue
			}
			k := len(st.Graph.About(n.ID, func(x *store.Assertion) bool { return x.Status == "active" || x.Status == "candidate" }))
			if k > 0 {
				ns = append(ns, nn{n, k})
			}
		}
		sort.Slice(ns, func(i, j int) bool { return ns[i].k > ns[j].k || (ns[i].k == ns[j].k && ns[i].n.ID < ns[j].n.ID) })
		for _, x := range ns {
			if len(rows) >= 100 {
				break
			}
			rows = append(rows, wire.Component{Type: wire.ComponentRow, Title: x.n.Name, Subtitle: fmt.Sprintf("%s · %d 条关系", x.n.Type, x.k),
				Action: ext.Open("open:"+x.n.ID, "", page{Tab: s.Tab, Open: x.n.ID}, x.n.Name)})
		}
	case "memory":
		for _, f := range entry.Facets {
			for _, e := range curate.EntriesOf(st, f, func(e *entry.Entry) bool { return e.Grain != entry.Action }) {
				if len(rows) >= 200 {
					break
				}
				rows = append(rows, wire.Component{Type: wire.ComponentRow, Title: short(e.Shown(), 60),
					Subtitle: fmt.Sprintf("%s · %s · %d 次 · %d 个活跃天", f, e.Grain, e.Count, len(e.ActiveDays)),
					Badge:    &wire.Badge{Text: statusLabel[string(e.Status)]}, Action: ext.Open("open:"+e.ID, "", page{Tab: s.Tab, Open: "e:" + e.ID}, short(e.Shown(), 20))})
			}
		}
	case "judgement":
		rows = a.judgementRows(st, v)
	case "rules":
		rows = a.ruleRows(st)
	case "reports":
		for i := len(st.Runs) - 1; i >= 0 && len(rows) < 30; i-- {
			r := st.Runs[i]
			rows = append(rows, wire.Component{Type: wire.ComponentRow, Title: map[string]string{"daily": "日级", "weekly": "周级"}[r.Kind] + " " + r.Period,
				Subtitle: fmt.Sprintf("约 %d token", r.Tokens), Action: ext.Open("open:"+r.ID, "", page{Tab: s.Tab, Open: r.ID}, r.Period)})
		}
	}
	if len(rows) == 0 {
		rows = append(rows, wire.Component{Type: wire.ComponentEmpty, Title: "这里还没有东西"})
	}
	v.Sections = append(v.Sections, wire.Section{Items: rows})
	return v
}

func evidenceItems(ev []store.Evidence) []wire.Component {
	var out []wire.Component
	for i, e := range ev {
		if i >= 8 {
			out = append(out, secondary(fmt.Sprintf("还有 %d 处", len(ev)-i)))
			break
		}
		out = append(out, wire.Component{Type: wire.ComponentRow, Title: "「" + e.Quote + "」", Subtitle: fmt.Sprintf("%s · %s %s #%d", e.Speaker, e.Chat, e.Day, e.N)})
	}
	return out
}

func (a *app) proposalPage(s page) *wire.View {
	st, done := a.read()
	defer done()
	p := st.Proposal(s.Open)
	if p == nil {
		return &wire.View{Title: "不见了", Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: "这条提议不见了"}}}}}
	}
	v := &wire.View{Title: p.ID}
	info := []wire.Component{text(p.Title)}
	if p.Detail != "" {
		info = append(info, secondary(p.Detail))
	}
	info = append(info, secondary(fmt.Sprintf("风险：%s · 状态：%s · 改动：%s", p.Risk, statusLabel[p.Status], p.Target)))
	switch {
	case p.Status == "held":
		info = append(info, secondary(p.Needs+"。要改，请先在知识库里自己改那一处，或者把锁交还。"))
	case p.Needs != "":
		info = append(info, secondary("要等 hub 支持："+p.Needs+"。现在只列出来，不执行。"))
	}
	var ch curate.Change
	_ = json.Unmarshal(p.Change, &ch)
	if ch.PinLine != "" {
		info = append(info, wire.Component{Type: wire.ComponentField, Name: "pin", Label: "写成", Kind: wire.FieldKindText, Value: ch.PinLine})
	}
	v.Sections = append(v.Sections, wire.Section{Items: info})
	if len(p.Evidence) > 0 {
		v.Sections = append(v.Sections, wire.Section{Title: "出处", Items: evidenceItems(p.Evidence)})
	}
	var acts []wire.Component
	switch {
	case p.Status == "auto":
		acts = append(acts, button("知道了", "ack", ""))
		if p.ApplyLog != "" {
			acts = append(acts, button("撤销", "undo", "把这一处改回去？"))
		}
	case p.Status == "accepted" && p.ApplyLog != "":
		acts = append(acts, button("撤销", "undo", "把这一处改回去？"))
	case p.Status != "pending" && p.Status != "later":
	case len(ch.IDs) > 0:
		for _, id := range ch.IDs {
			label := id
			if it := st.Items[id]; it != nil {
				label = short(it.Claim, 30)
			} else if as := st.Graph.Assertions[id]; as != nil {
				label = fmt.Sprintf("%s %s %s", nodeName(st.Graph, as.S), as.P, nodeName(st.Graph, as.O))
			}
			acts = append(acts, button("以这条为准："+label, "keep:"+id, ""))
		}
		acts = append(acts, button("都不对", "reject", ""))
	case p.Shadow && curate.ProposalKinds[p.Kind].KB:
		acts = append(acts, button("对", "right", ""), button("不对", "wrong", ""))
	default:
		acts = append(acts, button("接受", "accept", "按这条改知识库？能撤销。"))
		if ch.PinLine != "" {
			acts = append(acts, wire.Component{Type: wire.ComponentButton, Title: "按上面改过的接受", Action: &wire.Action{ID: "edit", Submit: true}})
		}
		acts = append(acts, button("不改", "reject", ""), button("稍后", "later", ""))
	}
	if len(acts) > 0 {
		v.Sections = append(v.Sections, wire.Section{Items: acts})
	}
	return v
}

func nodeName(g *store.Graph, id string) string {
	if n := g.Nodes[g.Resolve(id)]; n != nil {
		return n.Name
	}
	return id
}

func (a *app) itemPage(s page) *wire.View {
	st, done := a.read()
	defer done()
	it := st.Items[s.Open]
	if it == nil {
		return &wire.View{Title: "不见了", Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: "这一条不见了"}}}}}
	}
	info := []wire.Component{text(it.Claim), secondary(fmt.Sprintf("状态：%s · 第一次 %s · 最近 %s · %d 个独立来源 · 置信度 %s",
		statusLabel[it.Status], it.FirstSeen, it.LastSeen, it.Sources, it.Confidence))}
	if it.Scope != "" {
		info = append(info, secondary("适用："+it.Scope))
	}
	if len(it.Flags) > 0 {
		info = append(info, wire.Component{Type: wire.ComponentText, Text: "标记：" + strings.Join(it.Flags, "、"), Tone: wire.ToneCaution})
	}
	v := &wire.View{Title: map[string]string{store.Fact: "事实", store.Practice: "做事方式", store.Knowledge: "知识"}[it.Class],
		Sections: []wire.Section{{Items: info}, {Title: "出处", Items: evidenceItems(it.Evidence)}}}
	judge := []wire.Component{button("对", "judge:right", ""), button("不对", "judge:wrong", "标成不对，以后不再用这一条？")}
	if it.Verdict != "" {
		judge = append([]wire.Component{secondary("你判过：" + map[string]string{"right": "对", "wrong": "不对"}[it.Verdict])}, judge...)
	}
	v.Sections = append(v.Sections, wire.Section{Title: "抽检", Items: judge})
	return v
}

func (a *app) nodePage(s page) *wire.View {
	st, done := a.read()
	defer done()
	g := st.Graph
	n := g.Nodes[g.Resolve(s.Open)]
	if n == nil {
		return &wire.View{Title: "不见了", Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: "这个节点不见了"}}}}}
	}
	info := []wire.Component{secondary(fmt.Sprintf("类型：%s · 状态：%s", n.Type, n.Status))}
	if len(n.Refs) > 0 {
		info = append(info, secondary("知识库："+strings.Join(n.Refs, "、")))
	}
	var names []string
	for _, al := range n.Aliases {
		names = append(names, al.Name)
	}
	if len(names) > 0 {
		info = append(info, secondary("也叫："+strings.Join(names, "、")))
	}
	v := &wire.View{Title: n.Name, Sections: []wire.Section{{Items: info}}}
	by := map[string][]wire.Component{}
	for _, as := range g.About(n.ID, func(x *store.Assertion) bool { return x.Status != "rejected" && x.Status != "retracted" }) {
		other, dir := as.O, "→"
		if g.Resolve(as.O) == g.Resolve(n.ID) {
			other, dir = as.S, "←"
		}
		sub := fmt.Sprintf("%s 起 · %s · %s", as.ValidFrom, statusLabel[as.Status], as.Confidence)
		if as.ValidTo != "" {
			sub = fmt.Sprintf("%s 到 %s · %s", as.ValidFrom, as.ValidTo, statusLabel[as.Status])
		}
		if len(as.Evidence) > 0 {
			sub += " · 「" + short(as.Evidence[0].Quote, 30) + "」"
		}
		by[as.P] = append(by[as.P], wire.Component{Type: wire.ComponentRow, Title: dir + " " + nodeName(g, other), Subtitle: sub,
			Action: ext.Open("open:"+other, "", page{Tab: "graph", Open: g.Resolve(other)}, nodeName(g, other))})
	}
	for _, p := range sortedKeys(by) {
		v.Sections = append(v.Sections, wire.Section{Title: p, Items: by[p]})
	}
	return v
}

func sortedKeys[V any](m map[string]V) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func (a *app) runPage(s page) *wire.View {
	st, done := a.read()
	defer done()
	for _, r := range st.Runs {
		if r.ID == s.Open {
			items := []wire.Component{{Type: wire.ComponentMarkdown, Text: r.Report}}
			for _, ap := range st.Applied {
				if ap.Run == r.ID && ap.Undone == "" {
					items = append(items, button("撤销这次运行的所有自动合入", "undo-run", "把这次运行自动改过的地方都改回去？被别人改过的不会覆盖。"))
					break
				}
			}
			return &wire.View{Title: r.Period, Sections: []wire.Section{{Items: items}}}
		}
	}
	return &wire.View{Title: "不见了", Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: "这次运行不见了"}}}}}
}

func (a *app) homeAction(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	var s page
	_ = r.DecodeState(&s)
	c := a.c
	toast := ""
	var err error
	// Everything but moving around changes the state, which a run holds for minutes: say so instead of waiting
	// (the hub gives up after 20 seconds), and take a new snapshot after the change, for the page drawn next.
	changes := r.Action != "tab" && r.Action != "back" && r.Action != "facet" && r.Action != "check"
	t := a.lang(r)
	if p, busy := c.Live.Progress(); changes && busy {
		return &wire.ActionResult{Toast: progressText(p, time.Now()) + t.s("。跑完再改；现在看到的是上次整理完的结果。", ". Change it after the run; what you see is the last finished result.")}, nil
	}
	v4toast, handled, v4err := a.v4Action(ctx, &s, r.Action, r.Values, t)
	switch act := r.Action; {
	case handled:
		toast, err = v4toast, v4err
	case act == "tab":
		s.Tab, s.Open = r.Values["tab"], ""
	case act == "back":
		s.Open = ""
	case act == "mem:confirm" || act == "mem:reject":
		mark := map[bool]string{true: curate.ActRight, false: curate.ActWrong}[act == "mem:confirm"]
		c.St.Lock()
		if err = c.UserAct(strings.TrimPrefix(s.Open, "e:"), mark, ""); err == nil {
			c.Recount()
			err = c.St.Save()
			toast = map[bool]string{true: "确认了", false: "标了不对：这一条撤出所有出口，留作反例"}[act == "mem:confirm"]
		}
		c.St.Unlock()
	case strings.HasPrefix(act, "judge:"):
		err = c.Judge(s.Open, strings.TrimPrefix(act, "judge:"))
		toast = "记下了"
	case strings.HasPrefix(act, "own:"):
		toast, err = c.Own(strings.TrimPrefix(s.Open, "e:"), strings.TrimPrefix(act, "own:"), r.Values["text"])
		if err == nil && strings.HasSuffix(act, ":delete") {
			s.Open = ""
		}
	case act == "ack":
		c.St.Lock()
		if p := c.St.S.Proposal(s.Open); p != nil {
			p.Acked = true
			_ = c.St.Save()
		}
		c.St.Unlock()
	case act == "undo":
		c.St.Lock()
		id := ""
		if p := c.St.S.Proposal(s.Open); p != nil {
			id = p.ApplyLog
		}
		c.St.Unlock()
		if err = c.Undo(ctx, id); err == nil {
			toast = "已撤销"
		}
	case act == "undo-run":
		n, skipped, e := c.UndoRun(ctx, s.Open)
		err = e
		toast = fmt.Sprintf("撤销了 %d 处", n)
		if len(skipped) > 0 {
			toast += fmt.Sprintf("，%d 处被改过没有覆盖", len(skipped))
		}
	case act == "accept" || act == "reject" || act == "later" || act == "right" || act == "wrong" || act == "edit" || strings.HasPrefix(act, "keep:"):
		toast, err = c.Decide(ctx, s.Open, act, r.Values["pin"])
		if err == nil && act != "right" && act != "wrong" {
			s.Open = ""
		}
	}
	if changes {
		a.publish()
		a.badge()
	}
	if err != nil {
		return &wire.ActionResult{Toast: err.Error()}, nil
	}
	r.State = ext.EncodeState(s)
	v, verr := a.home(ctx, r)
	if verr != nil {
		return nil, verr
	}
	return &wire.ActionResult{View: v, Toast: toast}, nil
}

func (a *app) today(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	if a.c == nil {
		return &wire.View{Sections: []wire.Section{{Items: []wire.Component{secondary("还没准备好")}}}}, nil
	}
	st, done := a.read()
	defer done()
	count := func(class string) int {
		n := 0
		for _, it := range st.Items {
			if it.Class == class && (it.Status == "active" || it.Status == "established") {
				n++
			}
		}
		return n
	}
	pending := 0
	for _, p := range st.Proposals {
		if p.Status == "pending" || p.Status == "later" {
			pending++
		}
	}
	last := "还没运行过"
	for i := len(st.Runs) - 1; i >= 0; i-- {
		if st.Runs[i].Kind == "daily" {
			last = st.Runs[i].Period
			break
		}
	}
	return &wire.View{Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentStatGrid, Stats: []wire.Stat{
		{Label: "待确认", Value: fmt.Sprint(pending)},
		{Label: "事实", Value: fmt.Sprint(count(store.Fact))},
		{Label: "做事方式", Value: fmt.Sprint(count(store.Practice))},
		{Label: "知识", Value: fmt.Sprint(count(store.Knowledge))},
		{Label: "最近一次日级", Value: last},
	}}}}}}, nil
}

// health is the settings-side page: who "I" am, the latest triage funnel, run buttons.
func (a *app) health(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	if a.c == nil {
		return &wire.View{Title: "记忆体检", Sections: []wire.Section{{Items: []wire.Component{secondary("还没准备好")}}}}, nil
	}
	c := a.c
	owner, _ := c.Src.Owner(ctx)
	var names []string
	for _, n := range append(append([]string{owner.Name}, owner.Aliases...), c.Cfg.OwnerAliases...) {
		if strings.TrimSpace(n) != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		names = []string{"还不知道：请在助手设置里填你的名字和别名"}
	}
	v := &wire.View{Title: "记忆体检"}
	v.Sections = append(v.Sections, wire.Section{Title: "我是谁", Footer: "在助手设置里改你的别名；这里额外的写法在扩展设置的 owner_aliases。",
		Items: []wire.Component{text(strings.Join(names, "、"))}})
	// Never the store's lock here: a daily pass holds it for its whole run, and the hub gives up on a page after 20
	// seconds (a 502). What the page shows is the snapshot taken when the state last settled, and the progress of the
	// run in progress.
	if p, ok := c.Live.Progress(); ok {
		v.Sections = append(v.Sections, wire.Section{Title: "整理中", Footer: "下面是上次整理完的结果；这次跑完会更新。",
			Items: []wire.Component{{Type: wire.ComponentText, Text: progressText(p, time.Now()), Tone: wire.ToneCaution}}})
	}
	snap := c.Live.Snapshot()
	if snap != nil && snap.State != nil && snap.State.SourceClean != nil {
		v.Sections = append(v.Sections, sourceCleanSection(snap.State.SourceClean))
	}
	if snap != nil && len(snap.FunnelToks) > 0 {
		var rows [][]string
		for _, k := range sortedKeys(snap.FunnelToks) {
			rows = append(rows, []string{k, fmt.Sprint(snap.FunnelToks[k])})
		}
		v.Sections = append(v.Sections, wire.Section{Title: "分拣 " + snap.FunnelDay, Items: []wire.Component{{Type: wire.ComponentTable,
			Columns: []wire.TableColumn{{Title: "结论"}, {Title: "约 token"}}, Rows: rows}}})
	}
	if snap != nil && snap.Daily != nil && len(snap.Daily.Refusals) > 0 {
		v.Sections = append(v.Sections, refusalSection(snap.Daily))
	}
	v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{
		button("现在跑一次日级（昨天）", "run-daily", ""), button("现在跑一次周级（上周）", "run-weekly", ""),
	}})
	a.mu.Lock()
	trial, failed := a.lastTrial, a.lastErr
	a.mu.Unlock()
	items := []wire.Component{
		{Type: wire.ComponentField, Name: "day", Label: "哪天", Kind: wire.FieldKindDate, Placeholder: "空着就是昨天"},
		{Type: wire.ComponentButton, Title: "试跑这一天", Action: &wire.Action{ID: "trial-daily", Submit: true}},
	}
	if trial != "" {
		items = append(items, secondary(trial))
	}
	if strings.HasPrefix(failed, "试跑") {
		items = append(items, secondary(failed))
	}
	v.Sections = append(v.Sections, wire.Section{Title: "试跑", Footer: "用你给这个扩展选的模型跑一天的日级整理：不写知识库、不保存结果，报告留在扩展的数据目录 dryruns/。", Items: items})
	return v, nil
}

func (a *app) healthAction(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	now := time.Now().In(a.c.Loc)
	ok := false
	switch r.Action {
	case "run-daily":
		ok = a.start(context.WithoutCancel(ctx), "daily", now.AddDate(0, 0, -1).Format("2006-01-02"))
	case "run-weekly":
		ok = a.start(context.WithoutCancel(ctx), "weekly", curate.ISOWeek(now.AddDate(0, 0, -7).Format("2006-01-02")))
	case "trial-daily": // a day's pass that changes nothing (dryrun_hub.go); values.day, default yesterday
		day := strings.TrimSpace(r.Values["day"])
		if day == "" {
			day = now.AddDate(0, 0, -1).Format("2006-01-02")
		}
		if _, err := time.Parse("2006-01-02", day); err != nil {
			return &wire.ActionResult{Toast: "日期要写成 YYYY-MM-DD"}, nil
		}
		if !a.startTrial(context.WithoutCancel(ctx), day) {
			return &wire.ActionResult{Toast: "已经有一次在跑了"}, nil
		}
		return &wire.ActionResult{Toast: "开始试跑 " + day + "：不写知识库、不保存结果，跑完结果显示在这里"}, nil
	}
	if !ok {
		return &wire.ActionResult{Toast: "已经有一次在跑了"}, nil
	}
	return &wire.ActionResult{Toast: "开始了，跑完会在「enkeep 记忆」里看到"}, nil
}

// progressText is a run in progress in one line: what, which stage, how far, for how long.
func progressText(p curate.Progress, now time.Time) string {
	kind := map[string]string{"daily": "日级", "weekly": "周级"}[p.Kind]
	t := "正在整理：" + kind + " " + p.Period
	if p.Stage != "" {
		t += " · " + p.Stage
		if p.Total > 0 {
			t += fmt.Sprintf(" %d/%d", p.Done, p.Total)
		}
	}
	if p.Unit != "" {
		t += " · 「" + short(p.Unit, 20) + "」"
	}
	if !p.Started.IsZero() {
		t += " · 已经 " + now.Sub(p.Started).Round(time.Minute).String()
	}
	if p.Refused > 0 {
		t += fmt.Sprintf(" · 模型拒答 %d 批", p.Refused)
	}
	return t
}

// refusalSection lists what the model declined in the last daily run, and what became of each batch.
func refusalSection(r *curate.RunBrief) wire.Section {
	var rows [][]string
	skipped := 0
	for _, x := range r.Refusals {
		if x.Outcome == store.RefusalSkipped {
			skipped++
		}
		rows = append(rows, []string{short(x.Name, 20), curate.StepLabel(x.Step), fmt.Sprintf("%d–%d（%d 行）", x.From, x.To, x.Lines),
			short(x.Reason, 60), curate.RefusalOutcome(x.Outcome)})
	}
	footer := "模型拒答的一批会拆成更小的批再试；拆到很小仍被拒的才跳过，那几行没读。"
	if skipped > 0 {
		footer = fmt.Sprintf("跳过 %d 批，摘要没读 %d 行。", skipped, r.Skipped) + footer
	}
	return wire.Section{Title: "模型拒答 " + r.Period, Footer: footer, Items: []wire.Component{{Type: wire.ComponentTable,
		Columns: []wire.TableColumn{{Title: "来源"}, {Title: "步骤"}, {Title: "行"}, {Title: "原因"}, {Title: "结果"}}, Rows: rows}}}
}

func firstQuote(e *entry.Entry) string {
	for _, x := range e.Evidence {
		if x.Quote != "" {
			return x.Quote
		}
	}
	return ""
}

// cleanLabels name the categories of what the one-off clean removed.
var cleanLabels = map[string]string{
	"agent":     "Agent 以你的名义写的、Persona 替你打进去的",
	"assistant": "助手的话（含转述「你说过…」）",
	"decision":  "你只回了「好」，其实是助手的决定",
	"bot":       "机器人、推送",
	"others":    "别人说的、没点名你",
}

// sourceCleanSection shows what the one-off clean of the old state did: before and after, what went by category.
func sourceCleanSection(sc *store.SourceClean) wire.Section {
	var rows [][]string
	for _, k := range sortedKeys(sc.Removed) {
		layer, cat, _ := strings.Cut(k, ":")
		name := map[string]string{"memory": "记忆", "item": "条目"}[layer]
		label := cleanLabels[cat]
		if label == "" {
			label = cat
		}
		rows = append(rows, []string{name, label, fmt.Sprint(sc.Removed[k])})
	}
	items := []wire.Component{
		text(fmt.Sprintf("记忆 %d → %d 条，条目 %d → %d 条；%d 条改为待确认，%d 个提议撤回。", sc.MemoryBefore, sc.MemoryAfter,
			sc.ItemsBefore, sc.ItemsAfter, sc.Candidates, sc.ProposalsExpired)),
	}
	if len(rows) > 0 {
		items = append(items, wire.Component{Type: wire.ComponentTable,
			Columns: []wire.TableColumn{{Title: "层"}, {Title: "来源其实是"}, {Title: "删掉"}}, Rows: rows})
	}
	return wire.Section{Title: "旧数据清理 " + sc.At[:10], Footer: "只依据你本人说的话；删之前备份在扩展的数据目录（" + filepath.Base(sc.Backup) + "）。",
		Items: items}
}

// judgementRows lists how the owner works: traits first, then patterns by how often they occur. What the assistant
// may be shown (curate.Brief) comes first, as text.
func (a *app) judgementRows(st *store.State, v *wire.View) []wire.Component {
	v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{secondary("旧的思路层已迁移成条目，见「六个方面」页；这里只读旧数据。")}})
	var rows []wire.Component
	for _, id := range sortedIDs(st.Traits) {
		t := st.Traits[id]
		if t.Status == "seed" {
			continue
		}
		rows = append(rows, wire.Component{Type: wire.ComponentRow, Title: "特征 · " + short(t.Name, 40), Subtitle: fmt.Sprintf("%d 个模式 · %s", len(t.Patterns), short(t.Definition, 60)),
			Badge: &wire.Badge{Text: statusLabel[t.Status]}, Action: ext.Open("open:"+t.ID, "", page{Tab: "judgement", Open: t.ID}, short(t.Name, 20))})
	}
	var ps []*store.Pattern
	for _, p := range st.Patterns {
		if p.Status != "seed" {
			ps = append(ps, p)
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Count != ps[j].Count {
			return ps[i].Count > ps[j].Count
		}
		return ps[i].ID < ps[j].ID
	})
	for _, p := range ps {
		if len(rows) >= 150 {
			break
		}
		rows = append(rows, wire.Component{Type: wire.ComponentRow, Title: short(p.Name, 40), Subtitle: fmt.Sprintf("%s · %d 次 · %d 个对话 · %s", p.Dimension, p.Count, len(p.Chats), short(p.Definition, 60)),
			Badge: &wire.Badge{Text: statusLabel[p.Status]}, Action: ext.Open("open:"+p.ID, "", page{Tab: "judgement", Open: p.ID}, short(p.Name, 20))})
	}
	return rows
}

// judgementPage shows a trait with its patterns, or a pattern with the judgements under it and, for each, the
// owner's own words it rests on.
func (a *app) judgementPage(s page) *wire.View {
	st, done := a.read()
	defer done()
	if t := st.Traits[s.Open]; t != nil {
		info := []wire.Component{text(t.Definition), secondary(fmt.Sprintf("状态：%s · %d 个模式 · %s 到 %s", statusLabel[t.Status], len(t.Patterns), t.FirstSeen, t.LastSeen))}
		if t.Predicts != "" {
			info = append(info, secondary("预测："+t.Predicts))
		}
		var rows []wire.Component
		for _, pid := range t.Patterns {
			if p := st.Patterns[pid]; p != nil {
				rows = append(rows, wire.Component{Type: wire.ComponentRow, Title: short(p.Name, 40), Subtitle: fmt.Sprintf("%d 次 · %s", p.Count, short(p.Definition, 60)),
					Badge: &wire.Badge{Text: statusLabel[p.Status]}, Action: ext.Open("open:"+p.ID, "", page{Tab: s.Tab, Open: p.ID}, short(p.Name, 20))})
			}
		}
		if len(t.Open) > 0 {
			info = append(info, secondary("还没想清："+strings.Join(t.Open, "；")))
		}
		return &wire.View{Title: t.Name, Sections: []wire.Section{{Items: info}, {Title: "模式", Items: rows}, {Items: ownButtons(t.Pinned)}}}
	}
	p := st.Patterns[s.Open]
	if p == nil {
		return &wire.View{Title: "不见了", Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: "这一条不见了"}}}}}
	}
	info := []wire.Component{text(p.Definition), secondary(fmt.Sprintf("%s · %s · %d 次独立出现 · %d 个对话 · %s 到 %s", p.Dimension, statusLabel[p.Status], p.Count, len(p.Chats), p.FirstSeen, p.LastSeen))}
	if p.Trait != "" && st.Traits[p.Trait] != nil {
		info = append(info, secondary("属于特征："+st.Traits[p.Trait].Name))
	}
	if len(p.Counters) > 0 {
		info = append(info, secondary(fmt.Sprintf("反例：他有 %d 次选了相反的", len(p.Counters))))
	}
	v := &wire.View{Title: p.Name, Sections: []wire.Section{{Items: info}, {Items: ownButtons(p.Pinned)}}}
	for _, jid := range p.Members {
		j := st.Judgements[jid]
		if j == nil {
			continue
		}
		items := []wire.Component{text(j.Claim), secondary("遇到：" + j.Trigger + " → 选择：" + j.Choice)}
		if j.Inferred {
			items = append(items, secondary("（推断，不是他明说的）"))
		}
		if j.Rejected != "" {
			items = append(items, secondary("不选："+j.Rejected))
		}
		if j.Why != "" {
			items = append(items, secondary("原因："+j.Why))
		}
		items = append(items, evidenceItems(j.Evidence)...)
		v.Sections = append(v.Sections, wire.Section{Title: j.Day, Items: items})
	}
	return v
}

// ownButtons are the user's hand on a rule, pattern or trait.
func ownButtons(pinned bool) []wire.Component {
	pin := button("钉住", "own:pin", "")
	if pinned {
		pin = button("取消钉住", "own:unpin", "")
	}
	return []wire.Component{pin, button("删掉", "own:delete", "删掉后同样的内容不再生成。确定？")}
}

// ruleRows lists the rules in force first, then the candidates and the dormant ones.
func (a *app) ruleRows(st *store.State) []wire.Component {
	order := map[string]int{"active": 0, "candidate": 1, "dormant": 2}
	var rs []*store.Rule
	for _, r := range st.Rules {
		if !r.Deleted {
			rs = append(rs, r)
		}
	}
	sort.Slice(rs, func(i, j int) bool {
		if order[rs[i].Status] != order[rs[j].Status] {
			return order[rs[i].Status] < order[rs[j].Status]
		}
		if rs[i].Hard != rs[j].Hard {
			return rs[i].Hard
		}
		if rs[i].Count != rs[j].Count {
			return rs[i].Count > rs[j].Count
		}
		return rs[i].ID < rs[j].ID
	})
	var rows []wire.Component
	for _, r := range rs {
		if len(rows) >= 150 {
			break
		}
		sub := fmt.Sprintf("%s · %s", r.Scope, map[bool]string{true: "说了 " + fmt.Sprint(r.Count) + " 天", false: "说了 1 天"}[r.Count > 1])
		if r.Pinned {
			sub += " · 已钉住"
		}
		rows = append(rows, wire.Component{Type: wire.ComponentRow, Title: map[bool]string{true: "【必须】", false: ""}[r.Hard] + short(r.Text, 60), Subtitle: sub,
			Badge: &wire.Badge{Text: map[string]string{"active": "生效", "candidate": "候选", "dormant": "休眠"}[r.Status]}, Action: ext.Open("open:"+r.ID, "", page{Tab: "rules", Open: r.ID}, short(r.Text, 20))})
	}
	return rows
}

func (a *app) rulePage(s page) *wire.View {
	st, done := a.read()
	defer done()
	r := st.Rules[s.Open]
	if r == nil || r.Deleted {
		return &wire.View{Title: "不见了", Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: "这一条不见了"}}}}}
	}
	info := []wire.Component{
		{Type: wire.ComponentField, Name: "text", Label: "规矩", Kind: wire.FieldKindText, Value: r.Text},
		secondary(fmt.Sprintf("范围：%s · %s · 第一次 %s · 最近 %s", r.Scope, map[string]string{"active": "生效", "candidate": "候选（只说过一次，还没当成长期规矩）", "dormant": "休眠"}[r.Status], r.FirstSeen, r.LastSeen)),
	}
	if r.Why != "" {
		info = append(info, secondary("原因："+r.Why))
	}
	if r.Edited {
		info = append(info, secondary("这一条是你改的，以后不会被覆盖。"))
	}
	acts := append([]wire.Component{{Type: wire.ComponentButton, Title: "按上面改过的保存", Action: &wire.Action{ID: "own:edit", Submit: true}}}, ownButtons(r.Pinned)...)
	return &wire.View{Title: "规矩", Sections: []wire.Section{{Items: info}, {Title: "出处（他本人的话）", Items: evidenceItems(r.Evidence)}, {Items: acts}}}
}
