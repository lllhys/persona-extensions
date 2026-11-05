package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The list page: search, a filter, 加载更多 (20 at a time out of 60), and a row opening its detail, whose 编辑 opens a
// form that saves and goes back (list → detail → edit → save → pop).

type item struct {
	ID, Title, Kind, Note string
	Done                  bool
}

var kinds = []wire.Choice{{Value: "all", Title: "全部"}, {Value: "doc", Title: "文档"}, {Value: "chat", Title: "聊天"}, {Value: "mail", Title: "邮件"}}

func sampleItems() []item {
	out := make([]item, 0, 60)
	for i := 1; i <= 60; i++ {
		k := kinds[1+(i-1)%3].Value
		out = append(out, item{ID: fmt.Sprint(i), Title: fmt.Sprintf("示例条目 %02d", i), Kind: k,
			Note: fmt.Sprintf("第 %d 条的备注。", i), Done: i%4 == 0})
	}
	return out
}

const pageSize = 20

func shown(s state) int {
	if s.Shown <= 0 {
		return pageSize
	}
	return s.Shown
}

func (a *app) matching(s state) []item {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []item
	for _, it := range a.items {
		if (s.Kind == "" || s.Kind == "all" || it.Kind == s.Kind) && (s.Q == "" || strings.Contains(it.Title, s.Q)) {
			out = append(out, it)
		}
	}
	return out
}

func kindTitle(k string) string {
	for _, c := range kinds {
		if c.Value == k {
			return c.Title
		}
	}
	return k
}

func (a *app) list(r *ext.UIRequest, s state) *wire.View {
	all := a.matching(s)
	n := shown(s)
	if n > len(all) {
		n = len(all)
	}
	rows := make([]wire.Component, 0, n+2)
	for _, it := range all[:n] {
		row := wire.Component{Type: wire.ComponentRow, Key: "item-" + it.ID, Title: it.Title, Subtitle: kindTitle(it.Kind),
			Icon: it.Kind, Action: ext.Open("detail:"+it.ID, "", state{P: "detail", ID: it.ID}, it.Title)}
		if it.Done {
			row.Badge = &wire.Badge{Text: "完成", Tone: wire.TonePositive}
		}
		rows = append(rows, row)
	}
	if len(all) == 0 {
		rows = append(rows, wire.Component{Type: wire.ComponentEmpty, Title: "没有找到「" + s.Q + "」", Icon: wire.NavIconSearch})
	}
	sec := wire.Section{Title: fmt.Sprintf("%d 条，显示 %d 条", len(all), n), Items: rows}
	if n < len(all) && r.UIVersion >= 3 {
		sec.More = &wire.More{Action: wire.Action{ID: "more"}}
	}
	v := &wire.View{Title: "列表", State: ext.EncodeState(s), Sections: []wire.Section{sec}}
	if r.UIVersion >= 3 {
		v.Search = &wire.Search{Placeholder: "条目名", Value: s.Q, Action: wire.Action{ID: "search"}}
		v.Filter = &wire.Filter{Name: "kind", Title: "种类", Choices: kinds, Value: orDefault(s.Kind, "all"), Action: wire.Action{ID: "kind"}}
		return v
	}
	// An app before UI version 3: no search box, filter or 加载更多; a field, a choice and buttons instead.
	tools := []wire.Component{
		{Type: wire.ComponentField, Name: "q", Label: "搜索", Kind: wire.FieldKindText, Value: s.Q, Placeholder: "条目名"},
		{Type: wire.ComponentField, Name: "kind", Label: "种类", Kind: wire.FieldKindChoice, Value: orDefault(s.Kind, "all"), Choices: kinds},
		{Type: wire.ComponentButton, Title: "搜索", Action: &wire.Action{ID: "search", Submit: true}},
	}
	v.Sections = append([]wire.Section{{Items: tools}}, v.Sections...)
	if n < len(all) {
		v.Sections[1].Items = append(v.Sections[1].Items, wire.Component{Type: wire.ComponentButton, Title: "加载更多", Action: &wire.Action{ID: "more"}})
	}
	return v
}

func (a *app) find(id string) (item, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, it := range a.items {
		if it.ID == id {
			return it, true
		}
	}
	return item{}, false
}

func gone() *wire.View {
	return &wire.View{Title: "不见了", Sections: []wire.Section{{Items: items(wire.Component{Type: wire.ComponentEmpty, Title: "这一条不见了"})}}}
}

func (a *app) detail(r *ext.UIRequest, s state) *wire.View {
	it, ok := a.find(s.ID)
	if !ok {
		return gone()
	}
	done := "没有"
	if it.Done {
		done = "完成了"
	}
	c := items(
		wire.Component{Type: wire.ComponentMarkdown, Key: "note", Text: "**" + it.Title + "**\n\n" + it.Note},
		wire.Component{Type: wire.ComponentRow, Key: "kind", Title: "种类", Value: kindTitle(it.Kind), Icon: it.Kind},
		wire.Component{Type: wire.ComponentRow, Key: "done", Title: "完成", Value: done},
		wire.Component{Type: wire.ComponentButton, Title: "编辑", Action: ext.Open("edit:"+it.ID, "", state{P: "edit", ID: it.ID}, "编辑")},
	)
	if r.UIVersion < 3 {
		c = append(c, wire.Component{Type: wire.ComponentButton, Title: "回到列表", Action: &wire.Action{ID: "back"}})
	}
	return &wire.View{Title: it.Title, State: ext.EncodeState(s), Sections: []wire.Section{{Items: c}}}
}

func (a *app) edit(r *ext.UIRequest, s state) *wire.View {
	it, ok := a.find(s.ID)
	if !ok {
		return gone()
	}
	choices := kinds[1:]
	return &wire.View{Title: "编辑 " + it.Title, State: ext.EncodeState(s), Sections: []wire.Section{
		{Items: items(
			wire.Component{Type: wire.ComponentField, Name: "title", Label: "标题", Kind: wire.FieldKindText, Value: it.Title},
			wire.Component{Type: wire.ComponentField, Name: "kind", Label: "种类", Kind: wire.FieldKindChoice, Value: it.Kind, Choices: choices},
			wire.Component{Type: wire.ComponentField, Name: "note", Label: "备注", Kind: wire.FieldKindText, Value: it.Note},
			wire.Component{Type: wire.ComponentField, Name: "done", Label: "完成", Kind: wire.FieldKindToggle, Value: fmt.Sprint(it.Done)},
		)},
		{Items: items(wire.Component{Type: wire.ComponentButton, Title: "保存", Action: &wire.Action{ID: "save", Submit: true}})},
	}}
}

// save keeps the edit and goes back to the detail (which is drawn again).
func (a *app) save(r *ext.UIRequest, s state) (*wire.ActionResult, error) {
	title := strings.TrimSpace(r.Values["title"])
	if title == "" {
		return &wire.ActionResult{Toast: "标题不能是空的"}, nil
	}
	a.mu.Lock()
	for i := range a.items {
		if a.items[i].ID == s.ID {
			a.items[i].Title = title
			if k := r.Values["kind"]; k != "" {
				a.items[i].Kind = k
			}
			a.items[i].Note = r.Values["note"]
			a.items[i].Done = r.Values["done"] == "true"
		}
	}
	a.mu.Unlock()
	return &wire.ActionResult{Toast: "保存了", Pop: true}, nil
}

// The invalidate page: 「要数据」 answers loading at once; the data comes after a.after, and the extension tells the hub
// (ui.invalidate), which has the apps showing the page draw it again.

func (a *app) ask() {
	a.mu.Lock()
	a.asked, a.came = a.now(), time.Time{}
	after, hub := a.after, a.hub
	a.mu.Unlock()
	time.AfterFunc(after, func() {
		a.mu.Lock()
		a.came = a.now()
		a.mu.Unlock()
		if hub != nil {
			_ = hub.Invalidate(pageUI)
		}
	})
}

func (a *app) invalidatePage(r *ext.UIRequest, s state) *wire.View {
	a.mu.Lock()
	asked, came := a.asked, a.came
	a.mu.Unlock()
	head := wire.Component{Type: wire.ComponentText, Style: wire.TextStyleSecondary,
		Text: "点「要数据」：扩展先回加载中，3 秒后发 ui.invalidate，正在显示这一页的 App 自己重画，不用下拉刷新。"}
	ask := wire.Component{Type: wire.ComponentButton, Title: "要数据", Action: &wire.Action{ID: "ask"}}
	var body wire.Component
	switch {
	case asked.IsZero():
		body = wire.Component{Type: wire.ComponentEmpty, Title: "还没要过数据", Icon: wire.NavIconBell}
	case came.IsZero():
		body = wire.Component{Type: wire.ComponentLoading, Title: "数据在路上"}
	default:
		body = wire.Component{Type: wire.ComponentMetric, Label: "数据到了", Value: came.Format("15:04:05"),
			Detail: fmt.Sprintf("等了 %.1f 秒", came.Sub(asked).Seconds()), Tone: wire.TonePositive}
	}
	return &wire.View{Sections: []wire.Section{{Items: items(head, body, ask)}}}
}
