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

// The page (ui contribution "stories", a plugin_page): a list and a story opened on top of it (in its place in the
// apps before UI version 3). The state it carries is which list, which story, and whether the filter is set aside.

type pageState struct {
	List  string `json:"l,omitempty"`
	Story int64  `json:"s,omitempty"`
	All   bool   `json:"a,omitempty"` // the whole list, not filtered
}

const (
	textMost     = 12000 // characters of a story's text in the page
	chunkMost    = 1800  // characters of one text item (the apps take 2000)
	commentsShow = 15
	commentMost  = 1500
)

// renderBudget is how long a page may read: an app waits for its page through the hub.
var renderBudget = 15 * time.Second

func (a *app) readState(r *ext.UIRequest) pageState {
	var st pageState
	if r.DecodeState(&st) != nil {
		st = pageState{}
	}
	if _, ok := listEndpoint(st.List); !ok {
		st.List = a.cfg.List
	}
	return st
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
		if _, ok := listEndpoint(arg); ok {
			st.List, st.Story = arg, 0
		}
	case "story": // an app that knows no push asks here
		if id, err := strconv.ParseInt(arg, 10, 64); err == nil && id > 0 {
			st.Story = id
		}
	case "back":
		st.Story = 0
	case "all":
		st.All = !st.All
	case "refresh":
		refresh = true
	}
	return &wire.ActionResult{View: a.page(ctx, r, st, refresh)}, nil
}

func (a *app) page(ctx context.Context, r *ext.UIRequest, st pageState, refresh bool) *wire.View {
	if st.Story > 0 {
		return a.storyPage(ctx, r, st, refresh)
	}
	return a.listPage(ctx, r, st, refresh)
}

func text(s, style string) wire.Component {
	return wire.Component{Type: wire.ComponentText, Text: clip(s, chunkMost), Style: style}
}

func button(title, id string) wire.Component {
	return wire.Component{Type: wire.ComponentButton, Title: title, Action: &wire.Action{ID: id}}
}

// picker chooses the list: a segmented control, or a row each before UI version 2.
func picker(r *ext.UIRequest, t *texts, st pageState) wire.Section {
	if r.UIVersion >= 2 {
		seg := wire.Component{Type: wire.ComponentSegmented, Name: "list", Value: st.List, Action: &wire.Action{ID: "list"}}
		for _, l := range lists {
			seg.Choices = append(seg.Choices, wire.Choice{Value: l.id, Title: t.lists[l.id]})
		}
		return wire.Section{Items: []wire.Component{seg}}
	}
	sec := wire.Section{Title: t.pick}
	for _, l := range lists {
		it := wire.Component{Type: wire.ComponentRow, Title: t.lists[l.id], Action: &wire.Action{ID: "list:" + l.id}}
		if l.id == st.List {
			it.Badge = &wire.Badge{Text: t.viewing, Tone: wire.TonePositive}
		}
		sec.Items = append(sec.Items, it)
	}
	return sec
}

func (a *app) listPage(ctx context.Context, r *ext.UIRequest, st pageState, refresh bool) *wire.View {
	t, now := textsFor(r.Locale), time.Now()
	v := &wire.View{Title: "Hacker News", State: ext.EncodeState(st), Sections: []wire.Section{picker(r, t, st)}}
	title := t.lists[st.List]
	l, err := a.stories(ctx, st.List, listMost, refresh)
	if err != nil {
		v.Sections = append(v.Sections, wire.Section{Title: title, Items: []wire.Component{
			text(fmt.Sprintf(t.failed, oneLine(err.Error())), wire.TextStyleSecondary), button(t.retry, "refresh")}})
		return v
	}
	var notes []wire.Component
	if l.stale != nil {
		notes = append(notes, text(fmt.Sprintf(t.stale, oneLine(l.stale.Error()), t.ago(now.Sub(l.at))), wire.TextStyleSecondary))
	}
	if l.missed > 0 {
		notes = append(notes, text(fmt.Sprintf(t.missed, l.missed), wire.TextStyleSecondary))
	}
	if len(notes) > 0 {
		v.Sections = append(v.Sections, wire.Section{Items: notes})
	}

	f := a.cfg.filter()
	shown := l.stories
	if !st.All {
		shown = f.apply(l.stories)
	}
	sec := wire.Section{Title: fmt.Sprintf(t.count, title, len(l.stories)), Items: []wire.Component{}}
	switch d := t.describe(f); {
	case d == "":
	case st.All:
		sec.Title, sec.Footer = fmt.Sprintf(t.countAll, title, len(l.stories)), t.unfiltered
	default:
		sec.Title, sec.Footer = fmt.Sprintf(t.countOf, title, len(shown), len(l.stories)), clip(fmt.Sprintf(t.byInterests, d), chunkMost)
	}
	for _, it := range shown {
		next := pageState{List: st.List, Story: it.ID, All: st.All}
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentRow, Title: clip(it.Title, 300), Subtitle: clip(t.meta(it, now), 300),
			Action: ext.Open(fmt.Sprintf("story:%d", it.ID), "", next, clip(it.Title, 200))})
	}
	if len(shown) == 0 {
		sec.Items = append(sec.Items, text(t.nothing, wire.TextStyleSecondary))
	}
	if f.active() && (st.All || len(shown) < len(l.stories)) {
		label := fmt.Sprintf(t.showAll, len(l.stories))
		if st.All {
			label = t.filterAgain
		}
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentRow, Title: label, Action: &wire.Action{ID: "all"}})
	}
	v.Sections = append(v.Sections, sec, wire.Section{Items: []wire.Component{
		text(fmt.Sprintf(t.readAgo, t.ago(now.Sub(l.at))), wire.TextStyleCaption), button(t.refresh, "refresh")}})
	return v
}

func (a *app) storyPage(ctx context.Context, r *ext.UIRequest, st pageState, refresh bool) *wire.View {
	t, now := textsFor(r.Locale), time.Now()
	// Before UI version 3 the story is drawn in the list's place, and only this goes back.
	tail := func(it item) wire.Section {
		sec := wire.Section{Footer: t.textOnly}
		if link := it.link(); link != "" {
			sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentLink, Title: t.openArticle, URL: link})
		}
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentLink, Title: t.openSite, URL: itemURL(st.Story)}, button(t.refresh, "refresh"))
		if r.UIVersion < 3 {
			sec.Items = append(sec.Items, button(t.back, "back"))
		}
		return sec
	}
	v := &wire.View{Title: "Hacker News", State: ext.EncodeState(st)}
	th, err := a.thread(ctx, st.Story, commentsShow, refresh)
	if err != nil {
		v.Sections = []wire.Section{{Items: []wire.Component{text(fmt.Sprintf(t.failed, oneLine(err.Error())), wire.TextStyleSecondary)}}, tail(item{})}
		return v
	}
	s := th.story
	if s.gone() {
		v.Sections = []wire.Section{{Items: []wire.Component{text(t.gone, wire.TextStyleSecondary)}}, tail(item{})}
		return v
	}
	v.Title = clip(s.Title, 200)
	head := []wire.Component{text(t.meta(s, now), wire.TextStyleCaption)}
	body := plain(s.Text)
	for _, c := range chunks(clip(body, textMost), chunkMost) {
		head = append(head, text(c, wire.TextStyleBody))
	}
	v.Sections = append(v.Sections, wire.Section{Items: head})

	sec := wire.Section{Title: t.comments}
	if th.missed > 0 {
		sec.Items = append(sec.Items, text(fmt.Sprintf(t.commentsMissed, th.missed), wire.TextStyleSecondary))
	}
	for i, c := range th.comments {
		if i > 0 {
			sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentDivider})
		}
		sec.Items = append(sec.Items, text(c.By+" · "+t.ago(now.Sub(time.Unix(c.Time, 0))), wire.TextStyleCaption))
		if s := plain(c.Text); s != "" {
			sec.Items = append(sec.Items, text(clip(s, commentMost), wire.TextStyleBody))
		}
	}
	switch {
	case len(th.comments) > 0:
		sec.Footer = fmt.Sprintf(t.commentsFoot, len(th.comments), s.Descendants)
	case th.missed == 0:
		sec.Items = append(sec.Items, text(t.noComments, wire.TextStyleSecondary))
	}
	v.Sections = append(v.Sections, sec, tail(s))
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
