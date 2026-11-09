package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The nav page "RSSHub": the instance and the unread count on top, then either the latest entries with the
// subscriptions grouped by tag, one feed's entries, one entry's text, or a route's preview; a form at the bottom
// previews or subscribes to a route.

const (
	pageEntries    = 30
	pageFeedMost   = 60
	pageTextMost   = 1800 // characters of one text component
	pageEntryParts = 6    // text components of an entry's body
	refreshBudget  = 90 * time.Second
)

// pageState is what the page keeps between draws.
type pageState struct {
	Feed    string `json:"f,omitempty"`
	Entry   string `json:"e,omitempty"`
	Preview string `json:"p,omitempty"`
	Unread  bool   `json:"u,omitempty"`
}

func readState(r *ext.UIRequest) pageState {
	var st pageState
	_ = r.DecodeState(&st)
	return st
}

func (a *app) render(_ context.Context, r *ext.UIRequest) (*wire.View, error) {
	return a.page(r, readState(r)), nil
}

func (a *app) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	st := readState(r)
	kind, arg, _ := strings.Cut(r.Action, ":")
	toast := ""
	switch kind {
	case "refresh":
		ctx, cancel := context.WithTimeout(ctx, refreshBudget)
		defer cancel()
		if arg != "" {
			n, err := a.refresh(ctx, arg)
			if err != nil {
				toast = "刷新失败：" + clip(oneLine(err.Error()), 120)
			} else {
				toast = fmt.Sprintf("刷新好了，新增 %d 条", n)
			}
			a.badge()
			break
		}
		if a.health().Config != nil {
			toast = a.health().Config.Error()
			break
		}
		n, failed := a.refreshAll(ctx)
		toast = fmt.Sprintf("刷新好了，新增 %d 条", n)
		if failed > 0 {
			toast += fmt.Sprintf("，%d 个订阅没读到", failed)
		}
	case "ping":
		ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
		defer cancel()
		if err := a.ping(ctx); err != nil {
			toast = "连不上实例：" + clip(oneLine(err.Error()), 120)
		} else {
			toast = "实例连通"
		}
	case "feed":
		st.Feed, st.Entry, st.Preview = arg, "", ""
	case "entry":
		st.Entry = arg
		_, _ = a.markRead("", []string{arg}, false, true)
	case "unread":
		if _, err := a.markRead("", []string{arg}, false, false); err == nil {
			toast = "标回未读了"
		}
		st.Entry = ""
	case "read":
		n, err := a.markRead(arg, nil, arg == "", true)
		if err != nil {
			toast = err.Error()
		} else {
			toast = fmt.Sprintf("%d 条标为已读", n)
		}
	case "only_unread":
		st.Unread = !st.Unread
		if v, ok := r.Values["filter"]; ok {
			st.Unread = v == "unread"
		}
	case "back":
		switch {
		case st.Entry != "":
			st.Entry = ""
		case st.Preview != "":
			st.Preview = ""
		default:
			st.Feed = ""
		}
	case "preview":
		route := strings.TrimSpace(firstNonEmpty(r.Values["route"], arg))
		p, err := a.preview(ctx, route)
		if err != nil {
			toast = err.Error()
			break
		}
		a.keepPreview(p)
		st.Preview, st.Feed, st.Entry = p.Source, "", ""
	case "subscribe":
		route := strings.TrimSpace(firstNonEmpty(r.Values["route"], st.Preview))
		ctx, cancel := context.WithTimeout(ctx, fetchTimeout+5*time.Second)
		defer cancel()
		f, added, n, err := a.subscribe(ctx, route, r.Values["title"], r.Values["tag"])
		switch {
		case err != nil:
			toast = clip(oneLine(err.Error()), 200)
		case !added:
			toast = "已经订阅过了"
			st.Feed, st.Preview, st.Entry = f.ID, "", ""
		default:
			toast = fmt.Sprintf("订阅了「%s」，读到 %d 条", f.name(), n)
			st.Feed, st.Preview, st.Entry = f.ID, "", ""
		}
	case "unsubscribe":
		f, err := a.unsubscribe(arg)
		if err != nil {
			toast = err.Error()
		} else {
			toast = "取消订阅了「" + f.name() + "」"
		}
		st.Feed, st.Entry = "", ""
	}
	return &wire.ActionResult{View: a.page(r, st), Toast: toast}, nil
}

// ping asks the instance for its front page: whether it answers at all.
func (a *app) ping(ctx context.Context) error {
	inst := a.instance()
	if h := a.health(); h.Config != nil {
		return h.Config
	}
	u := *inst.base
	u.Path += "/"
	_, err := fetch(ctx, a.client, dialURL(&u, a.getenv), "", "", about.Version)
	a.instanceAnswered(err)
	if err != nil {
		return fmt.Errorf("%s", inst.redact(err.Error()))
	}
	return nil
}

// --- drawing ---

func (a *app) page(r *ext.UIRequest, st pageState) *wire.View {
	loc := zone(r.TZ)
	st0 := a.feeds()
	feeds := st0.list()
	v := &wire.View{Title: "RSSHub", Sections: []wire.Section{a.overview(r, feeds)}}
	switch {
	case st.Entry != "":
		if f, e, ok := st0.entryByID(st.Entry); ok {
			v.Sections = append(v.Sections, entrySection(f, e, loc)...)
			break
		}
		st.Entry = ""
		fallthrough
	case st.Preview != "":
		if st.Preview != "" {
			if p, ok := a.previews.Load(st.Preview); ok {
				v.Sections = append(v.Sections, previewSection(p.(*preview), loc))
				break
			}
			st.Preview = ""
		}
		fallthrough
	case st.Feed != "":
		if st.Feed != "" {
			if f, ok := st0.get(st.Feed); ok {
				v.Sections = append(v.Sections, feedSections(r, f, st, loc)...)
				break
			}
			st.Feed = ""
		}
		fallthrough
	default:
		v.Sections = append(v.Sections, a.latest(r, st, loc))
		v.Sections = append(v.Sections, feedGroups(feeds, loc)...)
	}
	v.Sections = append(v.Sections, addSection(st))
	v.State = ext.EncodeState(st)
	if r.UIVersion >= 2 {
		v.RefreshAfter = 300
	}
	return v
}

func (a *app) overview(r *ext.UIRequest, feeds []feed) wire.Section {
	h := a.health()
	unread := 0
	for _, f := range feeds {
		unread += f.unread()
	}
	state, tone := "未检查", wire.ToneNeutral
	switch {
	case h.Config != nil:
		state, tone = "地址不对", wire.ToneDanger
	case h.failing():
		state, tone = "连不上", wire.ToneDanger
	case !h.OK.IsZero():
		state, tone = "连通", wire.TonePositive
	}
	sec := wire.Section{}
	if r.UIVersion >= 2 {
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentStatGrid, Stats: []wire.Stat{
			{Label: "未读", Value: strconv.Itoa(unread)},
			{Label: "订阅", Value: strconv.Itoa(len(feeds))},
			{Label: "实例", Value: state, Detail: clip(h.Host, 60), Tone: tone},
		}})
	} else {
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentText,
			Text: fmt.Sprintf("未读 %d · 订阅 %d · 实例 %s（%s）", unread, len(feeds), clip(h.Host, 60), state)})
	}
	switch {
	case h.Config != nil:
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentText, Text: h.Config.Error(), Style: wire.TextStyleSecondary})
	case h.failing():
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentText, Text: clip("上次没连上："+oneLine(h.Err.Error()), pageTextMost), Style: wire.TextStyleSecondary})
	}
	sec.Items = append(sec.Items,
		wire.Component{Type: wire.ComponentButton, Title: "全部刷新", Action: &wire.Action{ID: "refresh"}},
		wire.Component{Type: wire.ComponentButton, Title: "全部标为已读", Action: &wire.Action{ID: "read", Confirm: "把所有订阅的条目都标为已读？"}},
		wire.Component{Type: wire.ComponentButton, Title: "检查实例连通", Action: &wire.Action{ID: "ping"}},
	)
	sec.Footer = fmt.Sprintf("每 %d 分钟在后台刷新", int(h.Every/time.Minute))
	return sec
}

// latest is the newest entries of every feed (or the unread ones).
func (a *app) latest(r *ext.UIRequest, st pageState, loc *time.Location) wire.Section {
	list, total := a.feeds().entries(entryFilter{UnreadOnly: st.Unread, Limit: pageEntries})
	title := "最新条目"
	if st.Unread {
		title = "未读条目"
	}
	sec := wire.Section{Title: title}
	sec.Items = append(sec.Items, unreadToggle(r, st))
	if len(list) == 0 {
		text := "还没有条目：在下面预览或订阅一条 RSSHub 路由"
		if st.Unread {
			text = "没有未读的了"
		}
		sec.Items = append(sec.Items, emptyState(r, text))
		return sec
	}
	for _, e := range list {
		sec.Items = append(sec.Items, entryRow(e.entry, e.FeedName, loc))
	}
	if total > len(list) {
		sec.Footer = fmt.Sprintf("共 %d 条，这里是最新的 %d 条", total, len(list))
	}
	return sec
}

func unreadToggle(r *ext.UIRequest, st pageState) wire.Component {
	value := "all"
	if st.Unread {
		value = "unread"
	}
	if r.UIVersion >= 2 {
		return wire.Component{Type: wire.ComponentSegmented, Name: "filter", Value: value, Action: &wire.Action{ID: "only_unread"},
			Choices: []wire.Choice{{Value: "all", Title: "全部"}, {Value: "unread", Title: "只看未读"}}}
	}
	title := "只看未读"
	if st.Unread {
		title = "看全部"
	}
	return wire.Component{Type: wire.ComponentButton, Title: title, Action: &wire.Action{ID: "only_unread"}}
}

func emptyState(r *ext.UIRequest, text string) wire.Component {
	if r.UIVersion >= 3 {
		return wire.Component{Type: wire.ComponentEmpty, Title: text, Icon: wire.NavIconList}
	}
	return wire.Component{Type: wire.ComponentText, Text: text, Style: wire.TextStyleSecondary}
}

func entryRow(e entry, from string, loc *time.Location) wire.Component {
	var sub []string
	if from != "" {
		sub = append(sub, from)
	}
	if t := e.when(); !t.IsZero() {
		sub = append(sub, t.In(loc).Format("01-02 15:04"))
	}
	if e.Summary != "" {
		sub = append(sub, e.Summary)
	}
	row := wire.Component{Type: wire.ComponentRow, Title: clip(firstNonEmpty(e.Title, "（无标题）"), 300),
		Subtitle: clip(strings.Join(sub, " · "), 300), Action: &wire.Action{ID: "entry:" + e.ID}}
	if !e.Read {
		row.Badge = &wire.Badge{Text: "未读", Tone: wire.ToneCaution}
	}
	return row
}

// feedGroups is the subscriptions, one section a tag.
func feedGroups(feeds []feed, loc *time.Location) []wire.Section {
	if len(feeds) == 0 {
		return nil
	}
	var out []wire.Section
	for _, t := range tagsOf(feeds) {
		sec := wire.Section{Title: "订阅 · " + t}
		for _, f := range feeds {
			if f.tag() != t {
				continue
			}
			row := wire.Component{Type: wire.ComponentRow, Title: clip(f.name(), 300), Subtitle: clip(f.Source+" · "+fetchState(f, loc), 300),
				Action: &wire.Action{ID: "feed:" + f.ID}}
			switch {
			case f.LastError != "":
				row.Badge = &wire.Badge{Text: "失败", Tone: wire.ToneDanger}
			case f.unread() > 0:
				row.Badge = &wire.Badge{Text: strconv.Itoa(f.unread()), Tone: wire.ToneCaution}
			}
			sec.Items = append(sec.Items, row)
		}
		out = append(out, sec)
	}
	return out
}

func fetchState(f feed, loc *time.Location) string {
	switch {
	case f.LastFetch.IsZero():
		return "还没刷新"
	case f.LastError != "":
		return f.LastFetch.In(loc).Format("01-02 15:04") + " 失败：" + f.LastError
	}
	return f.LastOK.In(loc).Format("01-02 15:04") + " 刷新"
}

// feedSections is one feed: what it is, its buttons and its entries.
func feedSections(r *ext.UIRequest, f feed, st pageState, loc *time.Location) []wire.Section {
	head := wire.Section{Title: clip(f.name(), 200), Items: []wire.Component{
		{Type: wire.ComponentText, Text: clip(fmt.Sprintf("%s · 分类 %s · 未读 %d / %d · %s", f.Source, f.tag(), f.unread(), len(f.Entries), fetchState(f, loc)), pageTextMost), Style: wire.TextStyleSecondary},
	}}
	if strings.HasPrefix(f.SiteLink, "https://") {
		head.Items = append(head.Items, wire.Component{Type: wire.ComponentLink, Title: "打开网站", URL: f.SiteLink})
	}
	head.Items = append(head.Items,
		wire.Component{Type: wire.ComponentButton, Title: "刷新", Action: &wire.Action{ID: "refresh:" + f.ID}},
		wire.Component{Type: wire.ComponentButton, Title: "全部标为已读", Action: &wire.Action{ID: "read:" + f.ID}},
		wire.Component{Type: wire.ComponentButton, Title: "取消订阅", Role: wire.ButtonRoleDestructive,
			Action: &wire.Action{ID: "unsubscribe:" + f.ID, Confirm: "取消订阅「" + clip(f.name(), 60) + "」？它的条目会一起删掉。"}},
		wire.Component{Type: wire.ComponentButton, Title: "返回", Action: &wire.Action{ID: "back"}},
	)
	list := wire.Section{Title: "条目"}
	list.Items = append(list.Items, unreadToggle(r, st))
	n := 0
	for _, e := range f.Entries {
		if st.Unread && e.Read {
			continue
		}
		if n++; n > pageFeedMost {
			break
		}
		list.Items = append(list.Items, entryRow(e, "", loc))
	}
	if n == 0 {
		list.Items = append(list.Items, emptyState(r, "没有条目"))
	}
	return []wire.Section{head, list}
}

// entrySection is one entry's text.
func entrySection(f feed, e entry, loc *time.Location) []wire.Section {
	var meta []string
	meta = append(meta, f.name())
	if e.Author != "" {
		meta = append(meta, e.Author)
	}
	if t := e.when(); !t.IsZero() {
		meta = append(meta, t.In(loc).Format("2006-01-02 15:04"))
	}
	sec := wire.Section{Title: clip(firstNonEmpty(e.Title, "（无标题）"), 300), Items: []wire.Component{
		{Type: wire.ComponentText, Text: clip(strings.Join(meta, " · "), pageTextMost), Style: wire.TextStyleSecondary},
	}}
	sec.Items = append(sec.Items, bodyTexts(firstNonEmpty(e.Content, e.Summary))...)
	switch {
	case strings.HasPrefix(e.Link, "https://"):
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentLink, Title: "打开原文", URL: e.Link})
	case e.Link != "":
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentText, Text: clip(e.Link, pageTextMost), Style: wire.TextStyleCaption})
	}
	sec.Items = append(sec.Items,
		wire.Component{Type: wire.ComponentButton, Title: "标回未读", Action: &wire.Action{ID: "unread:" + e.ID}},
		wire.Component{Type: wire.ComponentButton, Title: "返回", Action: &wire.Action{ID: "back"}},
	)
	return []wire.Section{sec}
}

// bodyTexts cuts a text into a few text components at paragraph ends.
func bodyTexts(s string) []wire.Component {
	s = clip(s, pageTextMost*pageEntryParts)
	var out []wire.Component
	var cur strings.Builder
	push := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			out = append(out, wire.Component{Type: wire.ComponentText, Text: clip(t, pageTextMost)})
		}
		cur.Reset()
	}
	for _, para := range strings.Split(s, "\n") {
		if cur.Len() > 0 && len([]rune(cur.String()))+len([]rune(para)) > pageTextMost-2 {
			push()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n")
		}
		cur.WriteString(para)
	}
	push()
	if len(out) > pageEntryParts {
		out = out[:pageEntryParts]
	}
	return out
}

// previewSection is a route read without subscribing.
func previewSection(p *preview, loc *time.Location) wire.Section {
	sec := wire.Section{Title: "预览 · " + clip(p.Source, 200)}
	if p.Err != "" {
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentText, Text: clip("读不到："+p.Err, pageTextMost), Style: wire.TextStyleSecondary})
	} else {
		f := p.Feed
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentText,
			Text: clip(fmt.Sprintf("%s · %d 条 · %s 读取", firstNonEmpty(f.Title, p.Source), len(f.Entries), p.At.In(loc).Format("15:04")), pageTextMost), Style: wire.TextStyleSecondary})
		for i, pe := range f.Entries {
			if i >= 15 {
				break
			}
			var sub []string
			if !pe.Published.IsZero() {
				sub = append(sub, pe.Published.In(loc).Format("01-02 15:04"))
			}
			if pe.Summary != "" {
				sub = append(sub, pe.Summary)
			}
			sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentRow, Title: clip(firstNonEmpty(pe.Title, "（无标题）"), 300), Subtitle: clip(strings.Join(sub, " · "), 300)})
		}
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentButton, Title: "订阅这条", Action: &wire.Action{ID: "subscribe", Submit: true}})
	}
	sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentButton, Title: "返回", Action: &wire.Action{ID: "back"}})
	return sec
}

// addSection is the form that previews or subscribes to a route.
func addSection(st pageState) wire.Section {
	return wire.Section{Title: "预览或订阅", Items: []wire.Component{
		{Type: wire.ComponentField, Name: "route", Label: "RSSHub 路由或订阅源网址", Kind: wire.FieldKindText, Value: st.Preview,
			Placeholder: "/github/issue/nwverse/persona"},
		{Type: wire.ComponentField, Name: "tag", Label: "分类（订阅时用）", Kind: wire.FieldKindText, Placeholder: "开发"},
		{Type: wire.ComponentField, Name: "title", Label: "标题（可不填）", Kind: wire.FieldKindText},
		{Type: wire.ComponentButton, Title: "预览", Action: &wire.Action{ID: "preview", Submit: true}},
		{Type: wire.ComponentButton, Title: "订阅", Action: &wire.Action{ID: "subscribe", Submit: true}},
	}, Footer: "路由写法见 docs.rsshub.app/routes，如 /github/trending/daily/go/en、/bilibili/user/video/2267573"}
}

func zone(tz string) *time.Location {
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			return l
		}
	}
	return time.Local
}
