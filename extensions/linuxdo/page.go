package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/pluginkit"
)

// The page (ui contribution "hot", a plugin_page): a list — the user's choice of hot / today / this week / latest, filtered by
// their interests — and a topic opened in the same page (its words, its best replies, a link to the site). The state
// it carries is which list, which topic, and whether the filter is set aside.

type pageState struct {
	List  string `json:"l,omitempty"`
	Topic int64  `json:"t,omitempty"`
	All   bool   `json:"a,omitempty"` // the whole list, not filtered
}

const (
	openingMost = 12000 // characters of a first post in the page
	openingRoom = 200   // its text items: every <pre> is one more, and a view takes 300 in all
	chunkMost   = 1800  // characters of one text item (the apps take 2000)
	repliesShow = 10
	replyMost   = 1200
)

func (p *plugin) readState(raw string) pageState {
	var st pageState
	_ = json.Unmarshal([]byte(raw), &st)
	if _, _, ok := listOf(st.List); !ok {
		st.List = p.cfg.list()
	}
	return st
}

func (st pageState) encode() string {
	b, _ := json.Marshal(st)
	return string(b)
}

// draw is how the asking app draws: the user's time zone, and whether it has UI version 2 (a segmented control).
type draw struct {
	loc *time.Location
	v2  bool
}

func (p *plugin) drawFor(r pluginkit.UIRequest) draw {
	d := draw{loc: p.loc, v2: r.Draws(2)}
	if p.cfg.TZ == "" { // the plugin's own setting wins; else the user's, as the hub says it
		d.loc = r.Location()
	}
	return d
}

func (p *plugin) uiRender(ctx context.Context, _ *pluginkit.Hub, raw json.RawMessage) (any, error) {
	var r pluginkit.UIRequest
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return p.page(ctx, p.drawFor(r), p.readState(r.State), false), nil
}

func (p *plugin) uiAction(ctx context.Context, _ *pluginkit.Hub, raw json.RawMessage) (any, error) {
	var r pluginkit.UIRequest
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	st := p.readState(r.State)
	refresh, toast := false, ""
	switch kind, arg, _ := strings.Cut(r.Action, ":"); kind {
	case "list":
		if arg == "" { // the segmented control: the choice is in the values
			arg = r.Values["list"]
		}
		if _, _, ok := listOf(arg); ok {
			st.List, st.Topic = arg, 0
		}
	case "topic":
		if id, err := strconv.ParseInt(arg, 10, 64); err == nil && id > 0 {
			st.Topic = id
		}
	case "back":
		st.Topic = 0
	case "all":
		st.All = !st.All
	case "refresh":
		refresh, toast = true, "已刷新"
	}
	v := p.page(ctx, p.drawFor(r), st, refresh)
	return pluginkit.ActionResult{View: &v, Toast: toast}, nil
}

// page is the list or the open topic; what went wrong is drawn, with a way to try again.
func (p *plugin) page(ctx context.Context, d draw, st pageState, refresh bool) pluginkit.View {
	if st.Topic > 0 {
		return p.topicPage(ctx, d, st)
	}
	return p.listPage(ctx, d, st, refresh)
}

func (p *plugin) listPage(ctx context.Context, d draw, st pageState, refresh bool) pluginkit.View {
	now := time.Now()
	v := pluginkit.View{Title: "LINUX DO", State: st.encode()}
	if d.v2 {
		seg := pluginkit.Item{Type: "segmented", Name: "list", Value: st.List, Action: &pluginkit.UIAction{ID: "list"}}
		for _, l := range lists {
			seg.Choices = append(seg.Choices, pluginkit.Choice{Value: l.id, Title: l.short})
		}
		v.Sections = append(v.Sections, pluginkit.Section{Items: []pluginkit.Item{seg}})
	} else { // an app from before UI version 2: a row each
		var pick []pluginkit.Item
		for _, l := range lists {
			it := pluginkit.Item{Type: "row", Title: l.title, Action: &pluginkit.UIAction{ID: "list:" + l.id}}
			if l.id == st.List {
				it.Badge = &pluginkit.Badge{Text: "正在看", Tone: "positive"}
			}
			pick = append(pick, it)
		}
		v.Sections = append(v.Sections, pluginkit.Section{Title: "看什么", Items: pick})
	}

	title, _, _ := listOf(st.List)
	topics, err := p.hot(ctx, st.List, refresh)
	if err != nil {
		v.Sections = append(v.Sections, pluginkit.Section{Title: title, Items: []pluginkit.Item{
			{Type: "text", Text: err.Error(), Style: "secondary"},
			{Type: "button", Title: "重试", Action: &pluginkit.UIAction{ID: "refresh"}}}})
		return v
	}
	f := p.cfg.filter()
	shown := topics
	if !st.All {
		shown = f.apply(topics)
	}
	sec := pluginkit.Section{Title: title}
	switch {
	case st.All && f.describe() != "":
		sec.Title = fmt.Sprintf("%s · 全部 %d 条", title, len(topics))
		sec.Footer = "没按你的兴趣过滤"
	case f.describe() != "":
		sec.Title = fmt.Sprintf("%s · %d / %d 条", title, len(shown), len(topics))
		sec.Footer = "按你的兴趣：" + f.describe() + "。在 设置 › 插件 › LINUX DO 的设置里改"
	default:
		sec.Title = fmt.Sprintf("%s · %d 条", title, len(topics))
		sec.Footer = "想只看感兴趣的：在 设置 › 插件 › LINUX DO 的设置里填分类、关键词"
	}
	for _, t := range shown {
		sec.Items = append(sec.Items, pluginkit.Item{Type: "row", Title: clip(t.Title, 300), Subtitle: t.meta(now),
			Action: &pluginkit.UIAction{ID: fmt.Sprintf("topic:%d", t.ID)}})
	}
	if len(shown) == 0 {
		sec.Items = append(sec.Items, pluginkit.Item{Type: "text", Text: "没有符合你兴趣的帖子", Style: "secondary"})
	}
	if f.describe() != "" && (st.All || len(shown) < len(topics)) {
		label := fmt.Sprintf("看全部 %d 条", len(topics))
		if st.All {
			label = "按我的兴趣过滤"
		}
		sec.Items = append(sec.Items, pluginkit.Item{Type: "row", Title: label, Action: &pluginkit.UIAction{ID: "all"}})
	}
	v.Sections = append(v.Sections, sec, pluginkit.Section{Items: []pluginkit.Item{
		{Type: "text", Text: "读于 " + now.In(d.loc).Format("15:04") + "（几分钟内不重读）", Style: "caption"},
		{Type: "button", Title: "刷新", Action: &pluginkit.UIAction{ID: "refresh"}}}})
	return v
}

func (p *plugin) topicPage(ctx context.Context, d draw, st pageState) pluginkit.View {
	back := pluginkit.Item{Type: "button", Title: "返回列表", Action: &pluginkit.UIAction{ID: "back"}}
	v := pluginkit.View{Title: "LINUX DO", State: st.encode()}
	t, err := p.topic(ctx, st.Topic)
	if err != nil {
		v.Sections = []pluginkit.Section{{Items: []pluginkit.Item{
			{Type: "text", Text: err.Error(), Style: "secondary"},
			{Type: "link", Title: "在网页打开", URL: topicURL(st.Topic)}, back}}}
		return v
	}
	v.Title = clip(oneLine(t.Title), 200)

	head := []pluginkit.Item{{Type: "text", Text: topicMeta(t, d.loc), Style: "caption"}}
	if op, ok := t.opening(); ok {
		more := pluginkit.Item{Type: "text", Text: "正文太长，后面的在网页里看", Style: "caption"}
		left, room := openingMost, openingRoom
		for _, sg := range postText(op.Cooked) {
			if left <= 0 {
				head = append(head, more)
				break
			}
			n := utf8.RuneCountInString(sg.Text)
			style := "body"
			if sg.Code {
				style = "code"
			}
			cs := chunks(clip(sg.Text, left), chunkMost)
			if len(cs) > room {
				head = append(head, more)
				break
			}
			room -= len(cs)
			for _, c := range cs {
				head = append(head, pluginkit.Item{Type: "text", Text: c, Style: style})
			}
			if n > left {
				head = append(head, more)
				break
			}
			left -= n
		}
	}
	v.Sections = append(v.Sections, pluginkit.Section{Items: head})

	if rs := t.bestReplies(repliesShow); len(rs) > 0 {
		sec := pluginkit.Section{Title: "热门回复", Footer: fmt.Sprintf("前 %d 楼里最热的 %d 条（共 %d 帖）", len(t.PostStream.Posts), len(rs), t.PostsCount)}
		for i, r := range rs {
			if i > 0 {
				sec.Items = append(sec.Items, pluginkit.Item{Type: "divider"})
			}
			who := fmt.Sprintf("#%d %s · %s", r.PostNumber, r.Username, r.CreatedAt.In(d.loc).Format("01-02 15:04"))
			if n := r.likes(); n > 0 {
				who += fmt.Sprintf(" · %d 赞", n)
			}
			sec.Items = append(sec.Items, pluginkit.Item{Type: "text", Text: who, Style: "caption"},
				pluginkit.Item{Type: "text", Text: clip(plainText(r.Cooked), replyMost), Style: "body"})
		}
		v.Sections = append(v.Sections, sec)
	}
	v.Sections = append(v.Sections, pluginkit.Section{Footer: "网页要手机能访问 linux.do", Items: []pluginkit.Item{
		{Type: "link", Title: "在网页打开", URL: topicURL(t.ID)}, back}})
	return v
}

// topicMeta summarizes a topic (translated example): alice · 11-03 17:37 · 1098 views · 110 likes · 36 posts · 13 people · tags AI, Casual Chat.
func topicMeta(t topicDoc, loc *time.Location) string {
	var parts []string
	if op, ok := t.opening(); ok && op.Username != "" {
		parts = append(parts, op.Username)
	}
	if !t.CreatedAt.IsZero() {
		parts = append(parts, t.CreatedAt.In(loc).Format("01-02 15:04"))
	}
	parts = append(parts, fmt.Sprintf("%d 浏览 · %d 赞 · %d 帖 · %d 人", t.Views, t.LikeCount, t.PostsCount, t.ParticipantCount))
	if tags := t.tags(); len(tags) > 0 {
		parts = append(parts, "标签 "+strings.Join(tags, "、"))
	}
	return strings.Join(parts, " · ")
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
