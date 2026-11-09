package page

import (
	"fmt"
	"time"

	"github.com/nwverse/persona-extensions/extensions/x/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/x/internal/x"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The page in Grok mode (internal/x/grokdigest.go): a header (the source, the last refresh, today's Grok runs, the
// state), a note that a model's search may be incomplete, Grok's summary, its topics with their posts, then the other
// posts of the last 24 hours. Without grok on the hub machine, how to set up either way.

func (p *Page) grokView(r *ext.UIRequest, t text, o x.Overview, v *wire.View) *wire.View {
	if !o.GrokFound {
		v.Sections = []wire.Section{{Items: []wire.Component{grokSetUp(r, t, o)}}}
		return v
	}
	loc := location(r, p.c.Location())
	d := p.c.GrokDigest()
	b := &builder{left: componentsMost}
	v.Sections = append(v.Sections, grokHeader(r, t, o, d, loc, b))

	if d.Handles == 0 && d.TopicsWatched == 0 {
		v.Sections = append(v.Sections, wire.Section{Items: b.add(nil,
			wire.Component{Type: wire.ComponentText, Key: "grok:nothing", Text: t.grokNothing, Style: wire.TextStyleSecondary})})
	}

	sum := wire.Section{Title: t.grokSummary}
	for i, s := range d.Summaries {
		for j, c := range chunks(s, chunkMost) {
			sum.Items = b.add(sum.Items, wire.Component{Type: wire.ComponentText, Key: fmt.Sprintf("grok:sum:%d:%d", i, j), Text: c, Style: wire.TextStyleBody})
		}
	}
	v.Sections = append(v.Sections, filled(sum, t, b))

	for i, tp := range d.Topics {
		sec := wire.Section{Title: clip(tp.Title, 200)}
		if tp.Summary != "" {
			sec.Items = b.add(sec.Items, wire.Component{Type: wire.ComponentText, Key: fmt.Sprintf("grok:topic:%d", i), Text: clip(tp.Summary, chunkMost), Style: wire.TextStyleSecondary})
		}
		for _, gp := range tp.Posts {
			sec.Items = append(sec.Items, b.grokPost(fmt.Sprintf("gt%d", i), gp, t, loc)...)
		}
		if len(sec.Items) > 0 {
			v.Sections = append(v.Sections, sec)
		}
	}

	posts := wire.Section{Title: t.grokPosts}
	for _, gp := range d.Posts {
		posts.Items = append(posts.Items, b.grokPost("gp", gp, t, loc)...)
	}
	v.Sections = append(v.Sections, filled(posts, t, b))

	tail := []wire.Component{{Type: wire.ComponentButton, Key: "refresh", Title: t.refresh, Action: &wire.Action{ID: "refresh"}}}
	v.Sections = append(v.Sections, wire.Section{Footer: t.grokFooter, Items: tail})
	return v
}

// grokHeader is the source, the last refresh, today's runs and the state, then the note and any error.
func grokHeader(r *ext.UIRequest, t text, o x.Overview, d x.GrokDigest, loc *time.Location, b *builder) wire.Section {
	last := t.never
	if !d.RefreshedAt.IsZero() {
		last = d.RefreshedAt.In(loc).Format("01-02 15:04")
	}
	st := o.GrokStatus
	state, tone := t.stateOK, wire.TonePositive
	switch st.State {
	case connector.StateRateLimited:
		state, tone = t.stateRateLimited, wire.ToneCaution
	case connector.StateNotSignedIn, connector.StateError, connector.StateNotConfigured:
		state, tone = t.stateError, wire.ToneDanger
	}
	if o.GrokBusy {
		state = t.grokSearching
	}
	runs := fmt.Sprintf("%d / %d", d.Runs, d.Cap)
	var items []wire.Component
	if r.UIVersion >= 2 {
		items = []wire.Component{{Type: wire.ComponentStatGrid, Key: "header", Stats: []wire.Stat{
			{Label: t.source, Value: "Grok"}, {Label: t.lastRefresh, Value: last}, {Label: t.grokRuns, Value: runs},
			{Label: t.state, Value: state, Tone: tone}}}}
	} else {
		items = []wire.Component{
			{Type: wire.ComponentRow, Key: "source", Title: t.source, Value: "Grok"},
			{Type: wire.ComponentRow, Key: "last", Title: t.lastRefresh, Value: last},
			{Type: wire.ComponentRow, Key: "runs", Title: t.grokRuns, Value: runs},
			{Type: wire.ComponentRow, Key: "state", Title: t.state, Badge: &wire.Badge{Text: state, Tone: tone}},
		}
	}
	items = append(items, wire.Component{Type: wire.ComponentText, Key: "grok:note", Text: t.grokNote, Style: wire.TextStyleCaption})
	if st.Error != "" && st.State != connector.StateOK {
		items = append(items, wire.Component{Type: wire.ComponentText, Key: "error", Text: clip(st.Error, chunkMost), Style: wire.TextStyleSecondary})
		for i, g := range st.Guide {
			items = append(items, wire.Component{Type: wire.ComponentText, Key: fmt.Sprintf("guide:%d", i), Text: clip(fmt.Sprintf("%d. %s", i+1, g), chunkMost), Style: wire.TextStyleCaption})
		}
	}
	b.left -= len(items)
	return wire.Section{Items: items}
}

// grokPost is one post Grok found: "Name @author", the text as Grok quoted it, why it matters, the permalink.
func (b *builder) grokPost(section string, gp x.GrokPost, t text, loc *time.Location) []wire.Component {
	key := section + ":" + gp.ID
	sub := t.grokNoTime
	if !gp.At.IsZero() {
		sub = gp.At.In(loc).Format("01-02 15:04")
	}
	if gp.Why != "" {
		sub += " · " + clip(gp.Why, 300)
	}
	out := []wire.Component{{Type: wire.ComponentRow, Key: key, Title: clip(name(gp.Name, gp.Author), 300), Subtitle: sub}}
	cs := chunks(gp.Text, chunkMost)
	cut := false
	room := min(postChunksMost, b.left-reserve-len(out)-2)
	if len(cs) > room {
		cs, cut = cs[:max(room, 0)], true
	}
	for i, c := range cs {
		out = append(out, wire.Component{Type: wire.ComponentText, Key: fmt.Sprintf("%s:t%d", key, i), Text: c, Style: wire.TextStyleBody})
	}
	if cut {
		out = append(out, wire.Component{Type: wire.ComponentText, Key: key + ":more", Text: t.moreOnX, Style: wire.TextStyleCaption})
	}
	out = append(out, wire.Component{Type: wire.ComponentLink, Key: key + ":link", Title: t.openOnX, URL: gp.Permalink()})
	if b.left-len(out) < reserve {
		return nil
	}
	b.left -= len(out)
	return out
}

// grokSetUp is the page while neither way is set up (or Grok is chosen and grok is missing): how to set up each.
func grokSetUp(r *ext.UIRequest, t text, o x.Overview) wire.Component {
	detail := t.setUpDetail
	if r.UIVersion >= 3 {
		return wire.Component{Type: wire.ComponentEmpty, Key: "signin", Title: t.setUpTitle, Detail: detail, Icon: wire.NavIconPerson}
	}
	return wire.Component{Type: wire.ComponentText, Key: "signin", Text: t.setUpTitle + "\n" + detail}
}
