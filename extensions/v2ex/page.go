package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The page (ui contribution "topics", a plugin_page): a list and a topic opened on top of it (in its place in the apps
// before UI version 3). The state it carries is which list, which topic, and whether the filter is set aside.

type pageState struct {
	List  string `json:"l,omitempty"`
	Topic int64  `json:"t,omitempty"`
	All   bool   `json:"a,omitempty"` // the whole list, not filtered
}

const (
	openingMost = 12000 // characters of a topic's text in the page
	chunkMost   = 1800  // characters of one text item (the apps take 2000)
	repliesShow = 20
	replyMost   = 1200
)

// renderBudget is how long a page may read: an app waits for its page through the hub, so a slow site gets a page of
// what was read, in time.
var renderBudget = 15 * time.Second

// offers says whether id is a list to show: nodes only with nodes to read.
func (a *app) offers(id string) bool {
	if id == "nodes" {
		return len(a.nodes) > 0
	}
	return slices.ContainsFunc(lists, func(l listDef) bool { return l.id == id })
}

func (a *app) readState(r *ext.UIRequest) pageState {
	var st pageState
	if r.DecodeState(&st) != nil {
		st = pageState{}
	}
	if !a.offers(st.List) {
		st.List = "hot"
		if a.offers(a.cfg.List) {
			st.List = a.cfg.List
		}
	}
	return st
}

// locFor is the time zone to draw in: the extension's own setting, else the user's as the hub says it.
func (a *app) locFor(r *ext.UIRequest) *time.Location {
	if a.cfg.TZ == "" && r.TZ != "" {
		if loc, err := time.LoadLocation(r.TZ); err == nil {
			return loc
		}
	}
	return a.loc
}

func (a *app) render(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	ctx, cancel := context.WithTimeout(ctx, renderBudget)
	defer cancel()
	return a.page(ctx, r, a.readState(r), false), nil
}

func (a *app) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, renderBudget)
	defer cancel()
	st := a.readState(r)
	refresh := false
	switch kind, arg, _ := strings.Cut(r.Action, ":"); kind {
	case "list":
		if arg == "" { // the segmented control: the choice is in the values
			arg = r.Values["list"]
		}
		if a.offers(arg) {
			st.List, st.Topic = arg, 0
		}
	case "topic": // an app that sends no UI version knows no push: it asks here, and the SDK leaves it to us
		if id, err := strconv.ParseInt(arg, 10, 64); err == nil && id > 0 {
			st.Topic = id
		}
	case "back":
		st.Topic = 0
	case "all":
		st.All = !st.All
	case "refresh":
		refresh = true
	}
	return &wire.ActionResult{View: a.page(ctx, r, st, refresh)}, nil
}

// page is the list or the open topic; what went wrong is drawn, with a way to try again.
func (a *app) page(ctx context.Context, r *ext.UIRequest, st pageState, refresh bool) *wire.View {
	if st.Topic > 0 {
		return a.topicPage(ctx, r, st, refresh)
	}
	return a.listPage(ctx, r, st, refresh)
}

func text(s, style string) wire.Component {
	return wire.Component{Type: wire.ComponentText, Text: s, Style: style}
}

func button(title, id string) wire.Component {
	return wire.Component{Type: wire.ComponentButton, Title: title, Action: &wire.Action{ID: id}}
}

// staleNote says that a page shows an older copy, and why.
func staleNote(stale error, at time.Time, loc *time.Location) wire.Component {
	return text(fmt.Sprintf("这次没读到：%s。下面是 %s 读到的", clip(oneLine(stale.Error()), 300), at.In(loc).Format("01-02 15:04")), wire.TextStyleSecondary)
}

// picker chooses the list: a segmented control, or a row each before UI version 2.
func (a *app) picker(r *ext.UIRequest, st pageState) wire.Section {
	if r.UIVersion >= 2 {
		seg := wire.Component{Type: wire.ComponentSegmented, Name: "list", Value: st.List, Action: &wire.Action{ID: "list"}}
		for _, l := range lists {
			if a.offers(l.id) {
				seg.Choices = append(seg.Choices, wire.Choice{Value: l.id, Title: l.title})
			}
		}
		return wire.Section{Items: []wire.Component{seg}}
	}
	sec := wire.Section{Title: "看什么"}
	for _, l := range lists {
		if !a.offers(l.id) {
			continue
		}
		it := wire.Component{Type: wire.ComponentRow, Title: l.title, Action: &wire.Action{ID: "list:" + l.id}}
		if l.id == st.List {
			it.Badge = &wire.Badge{Text: "正在看", Tone: wire.TonePositive}
		}
		sec.Items = append(sec.Items, it)
	}
	return sec
}

func (a *app) listPage(ctx context.Context, r *ext.UIRequest, st pageState, refresh bool) *wire.View {
	now, loc := time.Now(), a.locFor(r)
	v := &wire.View{Title: "V2EX", State: ext.EncodeState(st), Sections: []wire.Section{a.picker(r, st)}}
	if len(a.badNodes) > 0 {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{
			text(clip("这些不是节点名，没有读："+strings.Join(a.badNodes, "、")+"。要写节点的英文名，也就是节点页面链接 /go/ 后面那段", 1900), wire.TextStyleSecondary)}})
	}

	title := listTitle(st.List)
	l, err := a.list(ctx, st.List, refresh)
	if err != nil {
		v.Sections = append(v.Sections, wire.Section{Title: title, Items: []wire.Component{
			text(clip(oneLine(err.Error()), 1900), wire.TextStyleSecondary), button("重试", "refresh")}})
		return v
	}
	var notes []wire.Component
	if l.stale != nil {
		notes = append(notes, staleNote(l.stale, l.at, loc))
	}
	if len(l.missed) > 0 {
		notes = append(notes, text(clip("这些节点没读到："+strings.Join(l.missed, "、"), 1900), wire.TextStyleSecondary))
	}
	if len(notes) > 0 {
		v.Sections = append(v.Sections, wire.Section{Items: notes})
	}

	f := a.cfg.filter()
	shown := l.topics
	if !st.All {
		shown = f.apply(l.topics)
	}
	sec := wire.Section{Title: fmt.Sprintf("%s · %d 条", title, len(l.topics)), Items: []wire.Component{}}
	switch d := f.describe(); {
	case d == "":
	case st.All:
		sec.Title = fmt.Sprintf("%s · 全部 %d 条", title, len(l.topics))
		sec.Footer = "没按你的兴趣过滤"
	default:
		sec.Title = fmt.Sprintf("%s · %d / %d 条", title, len(shown), len(l.topics))
		sec.Footer = clip("按你的兴趣："+d+"。在这个扩展的设置里改", 1900)
	}
	for _, t := range shown {
		state := pageState{List: st.List, Topic: t.ID, All: st.All}
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentRow, Title: clip(t.title(), 300), Subtitle: t.meta(now),
			Action: ext.Open(fmt.Sprintf("topic:%d", t.ID), "", state, clip(t.title(), 200))})
	}
	if len(shown) == 0 {
		sec.Items = append(sec.Items, text("这里没有可看的帖子", wire.TextStyleSecondary))
	}
	if f.describe() != "" && (st.All || len(shown) < len(l.topics)) {
		label := fmt.Sprintf("看全部 %d 条", len(l.topics))
		if st.All {
			label = "按我的兴趣过滤"
		}
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentRow, Title: label, Action: &wire.Action{ID: "all"}})
	}
	v.Sections = append(v.Sections, sec)

	tail := wire.Section{}
	if !l.at.IsZero() {
		tail.Items = append(tail.Items, text("读于 "+l.at.In(loc).Format("15:04")+"（几分钟内不重读）", wire.TextStyleCaption))
	}
	tail.Items = append(tail.Items, button("刷新", "refresh"))
	v.Sections = append(v.Sections, tail)
	return v
}

func (a *app) topicPage(ctx context.Context, r *ext.UIRequest, st pageState, refresh bool) *wire.View {
	loc := a.locFor(r)
	// Before UI version 3 the topic is drawn in the list's place, and only this goes back.
	tail := func(id int64) wire.Section {
		sec := wire.Section{Footer: "网页要手机能访问 www.v2ex.com", Items: []wire.Component{
			{Type: wire.ComponentLink, Title: "在网页打开", URL: topicURL(id)}, button("刷新", "refresh")}}
		if r.UIVersion < 3 {
			sec.Items = append(sec.Items, button("返回列表", "back"))
		}
		return sec
	}
	v := &wire.View{Title: "V2EX", State: ext.EncodeState(st)}
	th, err := a.thread(ctx, st.Topic, true, refresh)
	if err != nil {
		v.Sections = []wire.Section{{Items: []wire.Component{text(clip(oneLine(err.Error()), 1900), wire.TextStyleSecondary)}}, tail(st.Topic)}
		return v
	}
	t := th.topic
	v.Title = clip(t.title(), 200)

	head := []wire.Component{text(clip(topicMeta(t, loc), 1900), wire.TextStyleCaption)}
	if th.stale != nil {
		head = append(head, staleNote(th.stale, th.at, loc))
	}
	body := tidy(t.Content)
	for _, c := range chunks(clip(body, openingMost), chunkMost) {
		head = append(head, text(c, wire.TextStyleBody))
	}
	if utf8.RuneCountInString(body) > openingMost {
		head = append(head, text("正文太长，后面的在网页里看", wire.TextStyleCaption))
	}
	v.Sections = append(v.Sections, wire.Section{Items: head})

	switch {
	case th.repliesErr != nil:
		v.Sections = append(v.Sections, wire.Section{Title: "回复", Items: []wire.Component{
			text(clip("回复这次没读到："+oneLine(th.repliesErr.Error()), 1900), wire.TextStyleSecondary)}})
	case len(th.replies) > 0:
		floors, rs := latestReplies(th.replies, repliesShow)
		sec := wire.Section{Title: "最新回复", Footer: fmt.Sprintf("最新 %d 条，共 %d 条；更早的在网页里看", len(rs), len(th.replies))}
		for i, rp := range rs {
			if i > 0 {
				sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentDivider})
			}
			who := fmt.Sprintf("#%d %s · %s", floors[i], rp.Member.Username, time.Unix(rp.Created, 0).In(loc).Format("01-02 15:04"))
			sec.Items = append(sec.Items, text(who, wire.TextStyleCaption))
			if s := tidy(rp.Content); s != "" {
				sec.Items = append(sec.Items, text(clip(s, replyMost), wire.TextStyleBody))
			}
		}
		v.Sections = append(v.Sections, sec)
	}
	v.Sections = append(v.Sections, tail(t.ID))
	return v
}

// chunks cuts s into pieces of at most n characters, at a line break when there is one.
func chunks(s string, n int) []string {
	var out []string
	r := []rune(s)
	for len(r) > n {
		cut := n
		for i := n; i > n/2; i-- {
			if r[i-1] == '\n' {
				cut = i
				break
			}
		}
		if piece := strings.TrimRight(string(r[:cut]), "\n"); strings.TrimSpace(piece) != "" {
			out = append(out, piece)
		}
		r = r[cut:]
	}
	if rest := strings.TrimSpace(string(r)); rest != "" {
		out = append(out, rest)
	}
	return out
}
