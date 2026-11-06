package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/store"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// page is the main page's state.
type page struct {
	Tab   string `json:"tab,omitempty"`   // review | fact | practice | knowledge | graph | reports
	Open  string `json:"open,omitempty"`  // a proposal (K-…), an item (i-…), a node (n-… / me) or a run
	Class string `json:"class,omitempty"` //
}

var tabs = []wire.Tab{{Value: "review", Title: "待确认"}, {Value: "fact", Title: "事实"}, {Value: "practice", Title: "做事方式"},
	{Value: "knowledge", Title: "知识"}, {Value: "graph", Title: "图谱"}, {Value: "reports", Title: "报告"}}

var statusLabel = map[string]string{
	"candidate": "待确认", "active": "有效", "stale": "过时", "superseded": "被取代", "rejected": "已驳回", "merged": "已合并",
	"observed": "偶现", "recurring": "反复", "established": "固定", "dormant": "休眠",
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
		s.Tab = "review"
	}
	if a.c == nil {
		return &wire.View{Title: "知识整理", Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: "还没准备好"}}}}}, nil
	}
	var v *wire.View
	switch {
	case strings.HasPrefix(s.Open, "K-"):
		v = a.proposalPage(s)
	case strings.HasPrefix(s.Open, "i-"):
		v = a.itemPage(s)
	case strings.HasPrefix(s.Open, "n-") || s.Open == "me":
		v = a.nodePage(s)
	case s.Open != "":
		v = a.runPage(s)
	default:
		v = a.listPage(s)
		v.Tabs = &wire.Tabs{Name: "tab", Value: s.Tab, Items: tabs, Action: wire.Action{ID: "tab"}}
		if n := len(a.pending()); n > 0 {
			v.Tabs.Items[0].Badge = fmt.Sprint(n)
		}
	}
	v.State = ext.EncodeState(s)
	if r.UIVersion > 0 && r.UIVersion < 3 && s.Open != "" {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{button("返回", "back", "")}})
	}
	return v, nil
}

func (a *app) listPage(s page) *wire.View {
	c := a.c
	c.St.Lock()
	defer c.St.Unlock()
	v := &wire.View{Title: "知识整理"}
	head := wire.Section{}
	if c.Cfg.Shadow(c.St.S.Installed, time.Now()) {
		head.Items = append(head.Items, secondary(fmt.Sprintf("影子期（前 %d 天）：只提议、不改知识库。点「对」「不对」帮它校准，各类提议的准确率够了再开自动合入。", c.Cfg.ShadowDays)))
	}
	a.mu.Lock()
	if a.running != "" {
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
		for _, p := range c.St.S.Proposals {
			if p.Status != "pending" && p.Status != "later" && p.Status != "blocked" && p.Status != "held" && !(p.Status == "auto" && !p.Acked) {
				continue
			}
			rows = append(rows, wire.Component{Type: wire.ComponentRow, Title: short(p.Title, 60), Subtitle: curate.ProposalKinds[p.Kind].Title + " · " + p.ID,
				Badge: &wire.Badge{Text: statusLabel[p.Status], Tone: riskTone[p.Risk]}, Action: ext.Open("open:"+p.ID, "", page{Tab: s.Tab, Open: p.ID}, p.ID)})
			shown = append(shown, p.ID)
		}
		go c.MarkShown(shown) // expiry counts from the day a proposal was first shown
	case "fact", "practice", "knowledge":
		for _, it := range c.St.S.ItemsBy(s.Tab, func(it *store.Item) bool { return it.Status != "merged" && it.Status != "rejected" }) {
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
		for _, n := range c.St.S.Graph.Nodes {
			if n.MergedInto != "" {
				continue
			}
			k := len(c.St.S.Graph.About(n.ID, func(x *store.Assertion) bool { return x.Status == "active" || x.Status == "candidate" }))
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
	case "reports":
		for i := len(c.St.S.Runs) - 1; i >= 0 && len(rows) < 30; i-- {
			r := c.St.S.Runs[i]
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
	c := a.c
	c.St.Lock()
	defer c.St.Unlock()
	p := c.St.S.Proposal(s.Open)
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
			if it := c.St.S.Items[id]; it != nil {
				label = short(it.Claim, 30)
			} else if as := c.St.S.Graph.Assertions[id]; as != nil {
				label = fmt.Sprintf("%s %s %s", nodeName(c.St.S.Graph, as.S), as.P, nodeName(c.St.S.Graph, as.O))
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
	c := a.c
	c.St.Lock()
	defer c.St.Unlock()
	it := c.St.S.Items[s.Open]
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
	c := a.c
	c.St.Lock()
	defer c.St.Unlock()
	g := c.St.S.Graph
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
	c := a.c
	c.St.Lock()
	defer c.St.Unlock()
	for _, r := range c.St.S.Runs {
		if r.ID == s.Open {
			items := []wire.Component{{Type: wire.ComponentMarkdown, Text: r.Report}}
			for _, ap := range c.St.S.Applied {
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
	switch act := r.Action; {
	case act == "tab":
		s.Tab, s.Open = r.Values["tab"], ""
	case act == "back":
		s.Open = ""
	case strings.HasPrefix(act, "judge:"):
		err = c.Judge(s.Open, strings.TrimPrefix(act, "judge:"))
		toast = "记下了"
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
	c := a.c
	c.St.Lock()
	defer c.St.Unlock()
	count := func(class string) int {
		n := 0
		for _, it := range c.St.S.Items {
			if it.Class == class && (it.Status == "active" || it.Status == "established") {
				n++
			}
		}
		return n
	}
	pending := 0
	for _, p := range c.St.S.Proposals {
		if p.Status == "pending" || p.Status == "later" {
			pending++
		}
	}
	last := "还没运行过"
	for i := len(c.St.S.Runs) - 1; i >= 0; i-- {
		if c.St.S.Runs[i].Kind == "daily" {
			last = c.St.S.Runs[i].Period
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
		return &wire.View{Title: "整理体检", Sections: []wire.Section{{Items: []wire.Component{secondary("还没准备好")}}}}, nil
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
	v := &wire.View{Title: "整理体检"}
	v.Sections = append(v.Sections, wire.Section{Title: "我是谁", Footer: "在助手设置里改你的别名；这里额外的写法在扩展设置的 owner_aliases。",
		Items: []wire.Component{text(strings.Join(names, "、"))}})
	c.St.Lock()
	var days []string
	for d := range c.St.S.Funnel {
		days = append(days, d)
	}
	sort.Strings(days)
	if f := c.St.S.Funnel[last(days)]; len(f.Tokens) > 0 {
		var rows [][]string
		for _, k := range sortedKeys(f.Tokens) {
			rows = append(rows, []string{k, fmt.Sprint(f.Tokens[k])})
		}
		v.Sections = append(v.Sections, wire.Section{Title: "分拣 " + last(days), Items: []wire.Component{{Type: wire.ComponentTable,
			Columns: []wire.TableColumn{{Title: "结论"}, {Title: "约 token"}}, Rows: rows}}})
	}
	c.St.Unlock()
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
	return &wire.ActionResult{Toast: "开始了，跑完会在「知识整理」里看到"}, nil
}

func last(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[len(ss)-1]
}
