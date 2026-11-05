// Package page is the extension's GitHub page (ui contribution "github", a nav_page): what needs the user on GitHub
// in five tabs — PRs waiting for their review, their own PRs with CI and reviews, what is assigned to them or mentions
// them, the notifications, and the activity of what they follow — and a PR or issue opened on top of it. It reads
// with the connector's own token, so it shows only what the connector may read; nothing is written from here.
package page

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/github/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/github/internal/github"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// ID is the ui contribution's (persona-extension.json).
const ID = "github"

// Page draws the GitHub page for the connector.
type Page struct {
	c   *github.Connector
	now func() time.Time

	mu    sync.Mutex
	badge string // the nav entry's badge as last sent
	hub   *ext.Hub
}

// New is the page of c.
func New(c *github.Connector) *Page { return &Page{c: c, now: time.Now} }

// state is the page's: the tab, the search, and (a page on top) the PR or issue open.
type state struct {
	Tab    string `json:"t,omitempty"`
	Q      string `json:"q,omitempty"`
	Repo   string `json:"r,omitempty"`
	Number int    `json:"n,omitempty"`
}

type tab struct{ id, short, title, empty string }

var tabs = []tab{
	{github.TabReview, "待 review", "等你 review 的 PR", "没有等你 review 的 PR"},
	{github.TabMine, "我的 PR", "你开着的 PR", "你没有开着的 PR"},
	{github.TabInvolved, "和我有关", "指派给你、提到你的", "没有指派给你、提到你的 issue 和 PR"},
	{github.TabNotify, "通知", "GitHub 通知", "没有通知"},
	{github.TabActivity, "动态", "你关注的仓库和人的动态", "最近没有动态"},
}

func tabOf(id string) tab {
	for _, t := range tabs {
		if t.id == id {
			return t
		}
	}
	return tabs[0]
}

// refreshAfter is how often a page on screen is drawn again.
const refreshAfter = 300

// Program is the page as the SDK serves it.
func (p *Page) Program() ext.Page { return ext.Page{Render: p.Render, Action: p.Action} }

// Render draws the page for r.
func (p *Page) Render(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	var s state
	_ = r.DecodeState(&s)
	p.keepHub(r.Hub)
	if s.Number > 0 {
		return p.detail(ctx, r, s, false), nil
	}
	return p.list(ctx, r, s, false), nil
}

// Action answers a tap.
func (p *Page) Action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	var s state
	_ = r.DecodeState(&s)
	p.keepHub(r.Hub)
	kind, arg, _ := strings.Cut(r.Action, ":")
	switch kind {
	case "tab":
		if arg == "" { // the segmented control: the choice is in the values
			arg = r.Values["tab"]
		}
		s.Tab, s.Q = tabOf(arg).id, ""
		return &wire.ActionResult{View: p.list(ctx, r, s, false)}, nil
	case "search":
		s.Q = strings.TrimSpace(r.Values["q"])
		return &wire.ActionResult{View: p.list(ctx, r, s, false)}, nil
	case "refresh":
		if s.Number > 0 {
			p.c.ForgetDetail(s.Repo, s.Number)
			return &wire.ActionResult{View: p.detail(ctx, r, s, true), Toast: "已刷新"}, nil
		}
		return &wire.ActionResult{View: p.list(ctx, r, s, true), Toast: "已刷新"}, nil
	case "web":
		if u, ok := httpsURL(arg); ok {
			return &wire.ActionResult{OpenURL: u}, nil
		}
		return &wire.ActionResult{Toast: "这一条没有能打开的链接"}, nil
	case "back": // apps before UI version 3: the open one closes in place
		s.Repo, s.Number = "", 0
		return &wire.ActionResult{View: p.list(ctx, r, s, false)}, nil
	}
	return nil, nil
}

func (p *Page) keepHub(h *ext.Hub) {
	if h == nil {
		return
	}
	p.mu.Lock()
	p.hub = h
	p.mu.Unlock()
}

// unavailable is the page while the connector cannot read GitHub: why, and where to fix it.
func (p *Page) unavailable(ctx context.Context) (wire.Component, bool) {
	st := p.c.Status(ctx)
	switch st.State {
	case connector.StateOK, connector.StateRateLimited:
		return wire.Component{}, false
	}
	detail := st.Error
	if detail == "" {
		detail = "GitHub 还没准备好"
	}
	return wire.Component{Type: wire.ComponentEmpty, Key: "signin", Title: "还没连上 GitHub", Icon: wire.NavIconSettings,
		Detail: clip(detail+"。在 设置 › 连接器 › GitHub 登录（选一台电脑上的 gh 登录，或者用设备码登录）；连接器关着时先把它打开", 2000)}, true
}

func (p *Page) list(ctx context.Context, r *ext.UIRequest, s state, fresh bool) *wire.View {
	t := tabOf(s.Tab)
	s.Tab = t.id
	v := &wire.View{Title: "GitHub", State: ext.EncodeState(s), RefreshAfter: refreshAfter}
	v.Sections = append(v.Sections, p.tabControl(r, t))
	if empty, ok := p.unavailable(ctx); ok {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{empty}})
		return v
	}
	entries, at, err := p.c.List(ctx, t.id, fresh)
	if t.id == github.TabReview && err == nil {
		p.setBadge(len(entries))
	}
	if r.UIVersion >= 3 {
		v.Search = &wire.Search{Placeholder: "标题、仓库、作者", Value: s.Q, Action: wire.Action{ID: "search"}}
	}
	shown := filter(entries, s.Q)
	sec := wire.Section{Title: fmt.Sprintf("%s · %d", t.title, len(shown))}
	if s.Q != "" {
		sec.Title = fmt.Sprintf("%s · 「%s」%d / %d", t.title, clip(s.Q, 40), len(shown), len(entries))
	}
	for _, e := range shown {
		sec.Items = append(sec.Items, p.row(r, s, e))
	}
	switch {
	case err != nil && len(entries) == 0:
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentEmpty, Key: "error", Title: "读不到 GitHub", Icon: wire.NavIconWarning,
			Detail: clip(github.Scrub(err.Error()), 2000), Button: &wire.EmptyButton{Title: "重试", Action: wire.Action{ID: "refresh"}}})
	case len(shown) == 0 && s.Q != "":
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentEmpty, Key: "none", Title: "没有找到「" + clip(s.Q, 40) + "」", Icon: wire.NavIconSearch})
	case len(shown) == 0:
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentEmpty, Key: "none", Title: t.empty, Icon: wire.NavIconCheck})
	}
	v.Sections = append(v.Sections, sec)
	foot := "读于 " + p.clock(r, at)
	switch {
	case err != nil && len(entries) > 0:
		foot += "，刚才没读到：" + clip(github.Scrub(err.Error()), 200)
	case p.c.RatesLow():
		foot += "（这个小时的 GitHub 请求额度快用完了，先显示上次读到的）"
	}
	tail := []wire.Component{{Type: wire.ComponentText, Key: "read-at", Text: foot, Style: wire.TextStyleCaption}}
	if r.UIVersion < 3 { // no search box: the search goes in a field
		tail = append(tail, wire.Component{Type: wire.ComponentField, Name: "q", Label: "搜索", Kind: wire.FieldKindText, Value: s.Q, Placeholder: "标题、仓库、作者"},
			wire.Component{Type: wire.ComponentButton, Title: "搜索", Action: &wire.Action{ID: "search", Submit: true}})
	}
	tail = append(tail, wire.Component{Type: wire.ComponentButton, Key: "refresh", Title: "刷新", Action: &wire.Action{ID: "refresh"}})
	v.Sections = append(v.Sections, wire.Section{Items: tail})
	return v
}

// tabControl is the five tabs: a segmented control, or a row each for an app before UI version 2.
func (p *Page) tabControl(r *ext.UIRequest, cur tab) wire.Section {
	if r.UIVersion >= 2 {
		seg := wire.Component{Type: wire.ComponentSegmented, Key: "tabs", Name: "tab", Value: cur.id, Action: &wire.Action{ID: "tab"}}
		for _, t := range tabs {
			seg.Choices = append(seg.Choices, wire.Choice{Value: t.id, Title: t.short})
		}
		return wire.Section{Items: []wire.Component{seg}}
	}
	var rows []wire.Component
	for _, t := range tabs {
		row := wire.Component{Type: wire.ComponentRow, Title: t.title, Action: &wire.Action{ID: "tab:" + t.id}}
		if t.id == cur.id {
			row.Badge = &wire.Badge{Text: "正在看", Tone: wire.TonePositive}
		}
		rows = append(rows, row)
	}
	return wire.Section{Title: "看什么", Items: rows}
}

// row is an entry: a PR or issue opens on top of the page, anything else opens on GitHub.
func (p *Page) row(r *ext.UIRequest, s state, e github.Entry) wire.Component {
	sub := []string{e.Ref()}
	if e.Why != "" {
		sub = append(sub, e.Why)
	}
	if e.Author != "" && !strings.HasPrefix(e.Title, e.Author+" ") {
		sub = append(sub, e.Author)
	}
	sub = append(sub, ago(p.now(), e.Updated))
	row := wire.Component{Type: wire.ComponentRow, Key: rowKey(e), Title: clip(oneLine(e.Title), 300), Subtitle: strings.Join(sub, " · ")}
	if b := badgeOf(e); b != nil {
		row.Badge = b
	}
	if r.UIVersion >= 3 {
		switch {
		case e.Avatar != "" && strings.HasPrefix(e.Avatar, "https://"):
			row.Avatar = &wire.Avatar{URL: e.Avatar}
		case e.PR:
			row.Icon = wire.NavIconCheck
		case e.Number > 0:
			row.Icon = wire.NavIconDoc
		default:
			row.Icon = wire.NavIconBell
		}
	}
	if e.Number > 0 && e.Repo != "" {
		row.Action = ext.Open("open:"+e.Ref(), "", state{Tab: s.Tab, Q: s.Q, Repo: e.Repo, Number: e.Number}, e.Ref())
	} else if u, ok := httpsURL(e.URL); ok {
		if len("web:"+u) > 128 { // an action's id is at most 128: the repository's page instead
			u = p.c.Web() + "/" + e.Repo
		}
		row.Action = &wire.Action{ID: "web:" + u}
	}
	return row
}

func rowKey(e github.Entry) string {
	k := e.Ref() + "@" + strconv.FormatInt(e.Updated.Unix(), 10) + ":" + e.Title
	for len(k) > 128 { // at most 128 bytes, cut on a character
		_, size := utf8.DecodeLastRuneInString(k)
		k = k[:len(k)-size]
	}
	return k
}

// badgeOf is a row's badge: the CI and review of a PR, a draft, an unread notification.
func badgeOf(e github.Entry) *wire.Badge {
	switch {
	case e.Draft:
		return &wire.Badge{Text: "草稿", Tone: wire.ToneNeutral}
	case e.CI == "FAILURE" || e.CI == "ERROR":
		return &wire.Badge{Text: "CI 失败", Tone: wire.ToneDanger}
	case e.Decision == "CHANGES_REQUESTED":
		return &wire.Badge{Text: "要求修改", Tone: wire.ToneCaution}
	case e.CI == "PENDING" || e.CI == "EXPECTED":
		return &wire.Badge{Text: "CI 进行中", Tone: wire.ToneNeutral}
	case e.Decision == "APPROVED":
		return &wire.Badge{Text: "已批准", Tone: wire.TonePositive}
	case e.CI == "SUCCESS":
		return &wire.Badge{Text: "CI 通过", Tone: wire.TonePositive}
	case e.Decision == "REVIEW_REQUIRED":
		return &wire.Badge{Text: "等 review", Tone: wire.ToneCaution}
	case e.Unread:
		return &wire.Badge{Text: "未读", Tone: wire.ToneCaution}
	}
	return nil
}

// filter keeps the entries whose title, repository or author has q (case aside).
func filter(entries []github.Entry, q string) []github.Entry {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return entries
	}
	var out []github.Entry
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Title+" "+e.Ref()+" "+e.Author), q) {
			out = append(out, e)
		}
	}
	return out
}

// setBadge puts the count of PRs waiting for review on the nav entry (cleared at none), when it changed.
func (p *Page) setBadge(n int) {
	text := ""
	if n > 0 {
		text = strconv.Itoa(n)
		if n >= 30 {
			text = "30+"
		}
	}
	p.mu.Lock()
	hub, same := p.hub, p.badge == text
	p.badge = text
	p.mu.Unlock()
	if hub != nil && !same {
		_ = hub.Badge(ID, text, wire.ToneCaution)
	}
}

// KeepBadge reads the review requests every interval while ctx lasts, so the nav entry's count is right without the
// page being open. Only while signed in; nothing when the hub has not started the page (the contribution is off).
func (p *Page) KeepBadge(ctx context.Context, hub *ext.Hub, interval time.Duration) {
	p.keepHub(hub)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		if _, off := p.unavailable(ctx); off || p.c.RatesLow() {
			continue
		}
		if entries, _, err := p.c.List(ctx, github.TabReview, false); err == nil {
			p.setBadge(len(entries))
		}
	}
}

func (p *Page) clock(r *ext.UIRequest, at time.Time) string {
	if at.IsZero() {
		return "—"
	}
	loc := time.Local
	if r.TZ != "" {
		if l, err := time.LoadLocation(r.TZ); err == nil {
			loc = l
		}
	}
	return at.In(loc).Format("15:04")
}

// ago is how long before now t was, the way people say it.
func ago(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
	return t.Format("2006-01-02")
}

func httpsURL(u string) (string, bool) {
	if strings.HasPrefix(u, "https://") && len(u) > len("https://") && len(u) <= 2000 {
		return u, true
	}
	return "", false
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// clip keeps s to n characters, with an ellipsis when it cut.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
