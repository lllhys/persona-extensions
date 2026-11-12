package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

type pageState struct {
	Collection int64    `json:"c,omitempty"`
	Query      string   `json:"q,omitempty"`
	Pages      int      `json:"n,omitempty"`
	Bookmark   int64    `json:"b,omitempty"`
	Mode       string   `json:"m,omitempty"`
	DraftID    string   `json:"d,omitempty"`
	Save       saveArgs `json:"s,omitempty"`
}

func (a *app) readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || st.Collection < -1 || st.Bookmark < 0 || st.Pages < 0 || st.Pages > 5 {
		return pageState{}, errors.New("invalid page state")
	}
	return st, nil
}

func (a *app) render(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	st, err := a.readState(r)
	if err != nil {
		return nil, err
	}
	if st.Mode == "" {
		st.Collection = a.defaultID
	}
	return a.page(ctx, r, st)
}

func (a *app) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	st, err := a.readState(r)
	if err != nil {
		return nil, err
	}
	switch r.Action {
	case "search":
		st.Query, st.Pages, st.Bookmark, st.Mode = r.Values["q"], 0, 0, "list"
	case "collection":
		id, err := strconv.ParseInt(r.Values["collection"], 10, 64)
		if err != nil || id < -1 {
			return nil, errors.New("invalid collection")
		}
		st.Collection, st.Pages, st.Bookmark, st.Mode = id, 0, 0, "list"
	case "more":
		st.Pages = min(st.Pages+1, searchPagesMost-1)
	case "detail":
		id, err := strconv.ParseInt(r.Values["id"], 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("invalid bookmark ID")
		}
		st.Bookmark, st.Mode = id, "detail"
	case "back":
		st.Bookmark, st.Mode, st.Save = 0, "list", saveArgs{}
	case "save-form":
		st.Mode, st.DraftID = "form", randomID()
	case "review-save":
		id, err := strconv.ParseInt(r.Values["collection"], 10, 64)
		if err != nil || id < -1 {
			return nil, errors.New("choose Unsorted or an existing collection")
		}
		args := saveArgs{URL: r.Values["url"], Title: r.Values["title"], Note: r.Values["note"], Tags: splitTags(r.Values["tags"]), CollectionID: id}
		if err := normalizeSave(&args); err != nil {
			return nil, err
		}
		st.Save, st.Mode = args, "review"
	case "save":
		return a.saveFromPage(ctx, r, st)
	default:
		return nil, fmt.Errorf("unknown page action %q", r.Action)
	}
	v, err := a.page(ctx, r, st)
	return &wire.ActionResult{View: v}, err
}

func (a *app) page(ctx context.Context, r *ext.UIRequest, st pageState) (*wire.View, error) {
	switch st.Mode {
	case "form":
		return a.saveView(ctx, r, st)
	case "review":
		return a.reviewSaveView(ctx, r, st)
	case "detail":
		item, err := a.read(ctx, func() (raindrop, error) { return a.api.bookmark(ctx, st.Bookmark) })
		if err != nil {
			return nil, err
		}
		return a.detailView(r, st, item), nil
	}
	if !a.hasToken() {
		return errorView(textsFor(r.Locale).missingToken), nil
	}
	t := textsFor(r.Locale)
	result, err := a.read(ctx, func() (page, error) { return a.api.search(ctx, st.Collection, st.Query, "-created", 0, st.Pages+1) })
	if err != nil {
		return nil, err
	}
	v := &wire.View{Title: t.recent, State: ext.EncodeState(st), Search: &wire.Search{Placeholder: t.placeholders.search, Value: st.Query, Action: wire.Action{ID: "search"}}}
	if cols, err := a.listCollections(ctx); err == nil {
		choices := []wire.Choice{{Value: "0", Title: t.all}, {Value: "-1", Title: t.unsorted}}
		selected := false
		for _, c := range cols {
			selected = selected || c.ID == st.Collection
			choices = append(choices, wire.Choice{Value: strconv.FormatInt(c.ID, 10), Title: clipText(c.Title, 60)})
			if len(choices) == 20 {
				break
			}
		}
		if selected || st.Collection <= 0 {
			v.Filter = &wire.Filter{Name: "collection", Title: t.collection, Value: strconv.FormatInt(st.Collection, 10), Choices: choices, Action: wire.Action{ID: "collection"}}
		}
	}
	section := wire.Section{Title: fmt.Sprintf(t.results, result.Count), Items: []wire.Component{{
		Type: wire.ComponentButton, Title: t.save, Action: &wire.Action{ID: "save-form", Push: &wire.Push{State: ext.EncodeState(pageState{Mode: "form", DraftID: randomID()}), Title: t.save}},
	}}}
	for _, item := range result.Items {
		section.Items = append(section.Items, bookmarkRow(item))
	}
	// The page reads st.Pages+1 pages, so Load more is offered only while search can read one more.
	if len(result.Items) == 50*(st.Pages+1) && len(result.Items) < result.Count && st.Pages+1 < searchPagesMost {
		section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more"}}
	}
	if len(result.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.none})
	}
	v.Sections = []wire.Section{section}
	return v, nil
}

func bookmarkRow(item raindrop) wire.Component {
	subtitle := clipText(item.Domain+" · "+strings.Join(item.Tags, ", ")+"\n"+clipText(item.Excerpt, 180), 2000)
	return wire.Component{Type: wire.ComponentRow, Key: "bookmark-" + strconv.FormatInt(item.ID, 10), Title: clipText(item.Title, 2000),
		Subtitle: subtitle, Action: &wire.Action{ID: "detail", Values: map[string]string{"id": strconv.FormatInt(item.ID, 10)}}}
}

func (a *app) detailView(r *ext.UIRequest, st pageState, item raindrop) *wire.View {
	t := textsFor(r.Locale)
	v := &wire.View{Title: clipText(item.Title, 2000), State: ext.EncodeState(st), Sections: []wire.Section{{
		Title: t.detail, Items: compactComponents(
			textComponent(item.Domain, wire.TextStyleSecondary),
			wire.Component{Type: wire.ComponentLink, Title: t.open, URL: item.Link},
			textComponent(item.Excerpt, ""),
			wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
		),
	}}}
	if len(item.Tags) > 0 {
		v.Sections[0].Items = append(v.Sections[0].Items, wire.Component{Type: wire.ComponentText, Text: clipText(strings.Join(item.Tags, ", "), 2000)})
	}
	if item.Note != "" {
		v.Sections = append(v.Sections, wire.Section{Title: t.note, Items: textComponents(item.Note)})
	}
	if len(item.Highlights) > 0 {
		section := wire.Section{Title: t.highlights}
		for _, h := range item.Highlights {
			section.Items = append(section.Items, textComponents(h.Text)...)
			section.Items = append(section.Items, textComponents(h.Note)...)
		}
		v.Sections = append(v.Sections, section)
	}
	return v
}

func (a *app) saveView(ctx context.Context, r *ext.UIRequest, st pageState) (*wire.View, error) {
	t := textsFor(r.Locale)
	cols, err := a.listCollections(ctx)
	if err != nil {
		return nil, err
	}
	items := []wire.Component{
		{Type: wire.ComponentField, Name: "url", Label: "URL", Value: st.Save.URL, Placeholder: t.placeholders.url},
		{Type: wire.ComponentField, Name: "title", Label: t.detail, Value: st.Save.Title, Placeholder: t.placeholders.title},
		{Type: wire.ComponentField, Name: "tags", Label: t.tags, Value: strings.Join(st.Save.Tags, ", "), Placeholder: t.placeholders.tags},
		{Type: wire.ComponentField, Name: "note", Label: t.note, Value: st.Save.Note, Placeholder: t.placeholders.note},
	}
	choices := []wire.Choice{{Value: "-1", Title: t.unsorted}}
	for _, c := range cols {
		choices = append(choices, wire.Choice{Value: strconv.FormatInt(c.ID, 10), Title: clipText(c.Title, 80)})
	}
	selected := st.Save.CollectionID
	if selected == 0 {
		selected = -1
	}
	items = append(items, wire.Component{Type: wire.ComponentField, Kind: "choice", Name: "collection", Label: t.collection, Value: strconv.FormatInt(selected, 10), Choices: choices})
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.review, Action: &wire.Action{ID: "review-save", Submit: true}})
	return &wire.View{Title: t.save, State: ext.EncodeState(st), Sections: []wire.Section{{Items: items}}}, nil
}

func (a *app) reviewSaveView(ctx context.Context, r *ext.UIRequest, st pageState) (*wire.View, error) {
	t := textsFor(r.Locale)
	cols, err := a.listCollections(ctx)
	if err != nil {
		return nil, err
	}
	items := []wire.Component{
		{Type: wire.ComponentText, Text: st.Save.URL},
		{Type: wire.ComponentText, Text: t.collection + ": " + collectionName(strconv.FormatInt(st.Save.CollectionID, 10), cols, t)},
		{Type: wire.ComponentText, Text: t.tags + ": " + strings.Join(st.Save.Tags, ", ")},
	}
	if st.Save.Title != "" {
		items = append(items, textComponents(st.Save.Title)...)
	}
	items = append(items, textComponents(st.Save.Note)...)
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.confirmSave, Action: &wire.Action{ID: "save", Submit: true, Confirm: t.save + "\n" + st.Save.URL}})
	return &wire.View{Title: t.review, State: ext.EncodeState(st), Sections: []wire.Section{{Items: items}}}, nil
}

func textComponent(text, style string) wire.Component {
	return wire.Component{Type: wire.ComponentText, Text: clipText(text, 2000), Style: style}
}

func compactComponents(items ...wire.Component) []wire.Component {
	out := make([]wire.Component, 0, len(items))
	for _, item := range items {
		if item.Type == wire.ComponentText && item.Text == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func textComponents(text string) []wire.Component {
	var out []wire.Component
	for _, part := range strings.Split(text, "\n") {
		runes := []rune(part)
		for len(runes) > 0 {
			n := min(len(runes), 2000)
			out = append(out, wire.Component{Type: wire.ComponentText, Text: string(runes[:n])})
			runes = runes[n:]
		}
	}
	return out
}

func collectionName(id string, cols []collection, t *texts) string {
	for _, c := range cols {
		if strconv.FormatInt(c.ID, 10) == id {
			return c.Title
		}
	}
	return t.unsorted
}

func collectionLabel(item raindrop, t *texts) string {
	if item.Collection.Title != "" {
		return item.Collection.Title
	}
	if item.Collection.ID == -1 {
		return t.unsorted
	}
	return "Raindrop"
}

func errorView(text string) *wire.View {
	return &wire.View{Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: text}}}}}
}

func (a *app) hasToken() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.api != nil && strings.TrimSpace(a.api.token) != ""
}
