package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// app holds what the pages change: the list's items (the edit page saves here) and the invalidate page's data.
type app struct {
	mu    sync.Mutex
	hub   *ext.Hub
	items []item
	// The invalidate page: when its data was asked for, and whether it came.
	asked, came time.Time
	// after is how long the data takes (3 s; tests make it shorter); now is the clock (tests fix it).
	after time.Duration
	now   func() time.Time
}

func newApp() *app {
	return &app{items: sampleItems(), after: 3 * time.Second, now: time.Now}
}

func (a *app) init(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	a.mu.Lock()
	a.hub = hub
	a.mu.Unlock()
	return nil, nil
}

// state is the page's state: which page, and the list's search, filter, length and open item.
type state struct {
	P      string `json:"p,omitempty"`
	Q      string `json:"q,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Shown  int    `json:"shown,omitempty"`
	ID     string `json:"id,omitempty"`
	Range  string `json:"range,omitempty"`
	Answer string `json:"answer,omitempty"`
}

// page is one page of the catalog.
type page struct {
	id, title, subtitle, icon string
	draw                      func(a *app, r *ext.UIRequest, s state) *wire.View
}

// pages is the catalog, in order. The list's detail and edit pages are reached from the list, not listed here.
var pages = []page{
	{"text", "文字", "四种样式、长文字", "doc", (*app).text},
	{"metric", "数字", "四种语气、说明", "gauge", (*app).metric},
	{"progress", "进度", "0、一部分、满、各种语气", "gauge", (*app).progress},
	{"row", "行", "副标题、值、角标、图标 / 头像 / 缩略图、可点", "list", (*app).row},
	{"button", "按钮", "普通、危险、先确认", "check", (*app).button},
	{"link", "链接", "在 App 里打开", "link", (*app).link},
	{"field", "输入", "五种输入、提交", "settings", (*app).field},
	{"chart", "图表", "柱状、折线、叠起来、单位", "chart", (*app).chart},
	{"stat_grid", "并排的数", "1 到 6 个", "gauge", (*app).statGrid},
	{"segmented", "分段选择", "选了马上换", "tag", (*app).segmented},
	{"markdown", "Markdown", "安全子集的全部写法", "doc", (*app).markdown},
	{"table", "表格", "窄表、宽表、对齐", "list", (*app).table},
	{"empty", "空状态", "有按钮、没按钮", "folder", (*app).empty},
	{"loading", "加载中", "有字、没字", "clock", (*app).loading},
	{"divider", "分隔线", "把一节分成几组", "list", (*app).divider},
	{"list", "列表", "搜索、筛选、加载更多；详情 → 编辑 → 返回", "search", (*app).list},
	{"invalidate", "invalidate", "先加载中，3 秒后扩展让 App 重画", "bell", (*app).invalidatePage},
}

func pageByID(id string) (page, bool) {
	for _, p := range pages {
		if p.id == id {
			return p, true
		}
	}
	return page{}, false
}

func (a *app) render(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	var s state
	if err := r.DecodeState(&s); err != nil {
		s = state{}
	}
	switch s.P {
	case "":
		return a.home(r), nil
	case "detail":
		return a.detail(r, s), nil
	case "edit":
		return a.edit(r, s), nil
	}
	p, ok := pageByID(s.P)
	if !ok {
		return a.home(r), nil
	}
	v := p.draw(a, r, s)
	if v.Title == "" {
		v.Title = p.title
	}
	if v.State == "" {
		v.State = ext.EncodeState(s)
	}
	if r.UIVersion < 3 { // an app before push and pop draws this page in place of the catalog: a way back
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{
			{Type: wire.ComponentButton, Title: "回到目录", Action: &wire.Action{ID: "home"}},
		}})
	}
	return v, nil
}

// home is the catalog: a row a page, each opening it on top.
func (a *app) home(r *ext.UIRequest) *wire.View {
	rows := make([]wire.Component, 0, len(pages))
	for _, p := range pages {
		rows = append(rows, wire.Component{Type: wire.ComponentRow, Key: p.id, Title: p.title, Subtitle: p.subtitle,
			Icon: p.icon, Action: ext.Open("open:"+p.id, "", state{P: p.id}, p.title)})
	}
	return &wire.View{Title: "组件展示", Sections: []wire.Section{
		{Items: []wire.Component{{Type: wire.ComponentText, Style: wire.TextStyleSecondary,
			Text: "扩展界面的每一种组件各一页。各端用同一份页面截图对齐（snapshots/）。"}}},
		{Title: "组件", Items: rows},
	}}
}

func (a *app) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	var s state
	_ = r.DecodeState(&s)
	switch r.Action {
	case "home":
		r.State = ""
		return nil, nil
	case "toast":
		return &wire.ActionResult{Toast: "点了一下"}, nil
	case "delete":
		return &wire.ActionResult{Toast: "删掉了（其实没有）"}, nil
	case "open-url":
		return &wire.ActionResult{OpenURL: "https://example.com/"}, nil
	case "submit":
		return &wire.ActionResult{Toast: "提交了：" + summarize(r.Values)}, nil
	case "range":
		s.Range = r.Values["range"]
	case "answer":
		s.Answer = r.Values["answer"]
	case "search":
		s.Q, s.Shown = strings.TrimSpace(r.Values["q"]), 0
		if k, ok := r.Values["kind"]; ok {
			s.Kind = k
		}
	case "kind":
		s.Kind, s.Shown = r.Values["kind"], 0
		if q, ok := r.Values["q"]; ok {
			s.Q = strings.TrimSpace(q)
		}
	case "more":
		s.Shown = shown(s) + pageSize
	case "back":
		s.P, s.ID = "list", ""
	case "save":
		return a.save(r, s)
	case "ask":
		a.ask()
	}
	r.State = ext.EncodeState(s)
	return nil, nil // draw the page again with the new state
}

// summarize writes a submit's values in a stable order, secrets hidden.
func summarize(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := values[k]
		if k == "secret" && v != "" {
			v = "••••"
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, "，")
}
