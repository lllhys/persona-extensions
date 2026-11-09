package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

type pageState struct {
	Query   string `json:"q,omitempty"`
	Start   int    `json:"s,omitempty"`
	PaperID string `json:"i,omitempty"`
	Mode    string `json:"m,omitempty"`
}

func readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || st.Start < 0 || len(st.PaperID) > 100 || len(st.Query) > 250 {
		return pageState{}, errors.New("invalid page state")
	}
	return st, nil
}

func (a *app) render(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	st, err := readState(r)
	if err != nil {
		return nil, err
	}
	return a.page(ctx, r, st)
}

func (a *app) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	st, err := readState(r)
	if err != nil {
		return nil, err
	}
	switch r.Action {
	case "search":
		st.Query, st.Start, st.Mode = clipText(r.Values["q"], 200), 0, ""
	case "recent":
		st.Query, st.Start, st.Mode = "", 0, "recent"
	case "more":
		st.Start += 20
	case "detail":
		st.Mode, st.PaperID = "detail", r.Values["id"]
	case "back":
		st.Mode, st.PaperID = "", ""
	default:
		return nil, fmt.Errorf("unknown page action %q", r.Action)
	}
	v, err := a.page(ctx, r, st)
	return &wire.ActionResult{View: v}, err
}

func (a *app) page(ctx context.Context, r *ext.UIRequest, st pageState) (*wire.View, error) {
	t := textsFor(r.Locale)
	if st.Mode == "detail" {
		item, err := a.client.paper(ctx, st.PaperID)
		a.record(err)
		if err != nil {
			return nil, err
		}
		return detailView(r, st, item), nil
	}
	if st.Mode != "" && st.Mode != "recent" {
		return nil, errors.New("invalid paper view")
	}
	feed, err := a.client.query(ctx, st.Query, st.Mode == "recent" || st.Query == "", st.Start)
	a.record(err)
	if err != nil {
		return nil, err
	}
	v := &wire.View{Title: t.papers, State: ext.EncodeState(st), Search: &wire.Search{Placeholder: t.search, Value: st.Query, Action: wire.Action{ID: "search"}}}
	section := wire.Section{Title: fmt.Sprintf(t.results, len(feed.Entries))}
	for _, item := range feed.Entries {
		id := paperID(item)
		if id == "" || item.Title == "" {
			continue
		}
		var authors []string
		for _, author := range item.Authors {
			authors = append(authors, author.Name)
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "paper-" + id, Title: clipText(strings.Join(strings.Fields(item.Title), " "), 1800), Subtitle: clipText(strings.Join(authors, ", ")+" · "+item.Category.Term, 1800), Action: &wire.Action{ID: "detail", Values: map[string]string{"id": id}}})
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noPapers})
	}
	if len(feed.Entries) == 20 {
		section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more"}}
	}
	v.Sections = []wire.Section{section, {Title: t.recent, Items: []wire.Component{{Type: wire.ComponentButton, Title: t.recent, Action: &wire.Action{ID: "recent"}}}}}
	return v, nil
}

func paperID(item paper) string {
	if paperURL(item) == "" {
		return ""
	}
	id := strings.TrimPrefix(item.ID, "https://arxiv.org/abs/")
	if id == item.ID || len(id) > 100 || strings.ContainsAny(id, "\x00\r\n/?#") {
		return ""
	}
	return id
}

func detailView(r *ext.UIRequest, st pageState, item paper) *wire.View {
	t := textsFor(r.Locale)
	var authors []string
	for _, author := range item.Authors {
		authors = append(authors, author.Name)
	}
	items := []wire.Component{{Type: wire.ComponentText, Text: clipText(t.authors+": "+strings.Join(authors, ", "), 1800)}, {Type: wire.ComponentText, Text: t.category + ": " + item.Category.Term + " · " + t.published + ": " + item.Published}}
	if link := paperURL(item); link != "" {
		items = append(items, wire.Component{Type: wire.ComponentLink, Title: t.open, URL: link})
	}
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}})
	v := &wire.View{Title: clipText(strings.Join(strings.Fields(item.Title), " "), 1800), State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.papers, Items: items}}}
	if item.Summary != "" {
		v.Sections = append(v.Sections, wire.Section{Title: t.abstract, Items: textComponents(strings.Join(strings.Fields(item.Summary), " "))})
	} else {
		v.Sections = append(v.Sections, wire.Section{Title: t.abstract, Items: []wire.Component{{Type: wire.ComponentText, Text: t.noAbstract, Style: wire.TextStyleSecondary}}})
	}
	return v
}

func textComponents(text string) []wire.Component {
	var out []wire.Component
	for len([]rune(text)) > 0 {
		r := []rune(text)
		n := min(len(r), 1800)
		out = append(out, wire.Component{Type: wire.ComponentText, Text: string(r[:n])})
		text = string(r[n:])
		if len(out) >= 30 {
			out[len(out)-1].Text = clipText(out[len(out)-1].Text, 1700) + " […]"
			break
		}
	}
	if len(out) == 0 {
		out = append(out, wire.Component{Type: wire.ComponentText, Text: " ", Style: wire.TextStyleSecondary})
	}
	return out
}
