package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

type pageState struct {
	Mode    string `json:"m,omitempty"`
	Tags    string `json:"t,omitempty"`
	Start   int    `json:"s,omitempty"`
	DraftID string `json:"d,omitempty"`
	URL     string `json:"u,omitempty"`
	Title   string `json:"a,omitempty"`
	Notes   string `json:"b,omitempty"`
	NewTags string `json:"g,omitempty"`
}

func readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || st.Start < 0 || len(st.DraftID) > 128 || len(st.URL) > 4096 || len(st.Title) > 300 || len(st.Notes) > 66000 || len(st.Tags) > 300 || len(st.NewTags) > 1000 {
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
		if err := validTags(r.Values["q"]); err != nil {
			return nil, err
		}
		st.Tags, st.Start, st.Mode = r.Values["q"], 0, "archive"
	case "view":
		if r.Values["view"] != "recent" && r.Values["view"] != "archive" {
			return nil, errors.New("invalid bookmark view")
		}
		st.Mode, st.Start = r.Values["view"], 0
	case "more":
		st.Start += 50
	case "create":
		if st.Mode != "review-create" {
			st = pageState{Mode: "create", DraftID: randomID()}
		} else {
			st.Mode = "create"
		}
	case "back":
		st = pageState{}
	case "review-create":
		in := addArgs{URL: r.Values["url"], Title: r.Values["title"], Notes: r.Values["notes"], Tags: r.Values["tags"]}
		if err := in.validate(); err != nil {
			return nil, err
		}
		st.URL, st.Title, st.Notes, st.NewTags, st.Mode = in.URL, in.Title, in.Notes, in.Tags, "review-create"
	case "confirm-create":
		raw, _ := json.Marshal(addArgs{URL: st.URL, Title: st.Title, Notes: st.Notes, Tags: st.NewTags})
		out, err := a.doAction(ctx, "ui-create:"+st.DraftID, "pinboard_add", raw)
		if err != nil {
			return nil, err
		}
		return &wire.ActionResult{Toast: out["text"].(string), Pop: true}, nil
	default:
		return nil, fmt.Errorf("unknown page action %q", r.Action)
	}
	v, err := a.page(ctx, r, st)
	return &wire.ActionResult{View: v}, err
}

func (a *app) page(ctx context.Context, r *ext.UIRequest, st pageState) (*wire.View, error) {
	t := textsFor(r.Locale)
	if a.client.token == "" {
		return &wire.View{Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: t.missingConfig}}}}}, nil
	}
	if st.Mode == "create" || st.Mode == "review-create" {
		return a.createView(r, st), nil
	}
	if st.Mode == "archive" {
		if err := validTags(st.Tags); err != nil {
			return nil, err
		}
	}
	if st.Mode != "" && st.Mode != "archive" {
		return nil, errors.New("invalid bookmark view")
	}
	var items bookmarkList
	var err error
	if st.Mode == "archive" {
		items, err = a.client.all(ctx, st.Tags, st.Start)
	} else {
		items, err = a.client.recent(ctx, st.Tags)
	}
	a.record(err)
	if err != nil {
		return nil, err
	}
	section := wire.Section{Title: t.bookmarks}
	for i, item := range items {
		if item.Description == "" || item.Href == "" {
			continue
		}
		row := wire.Component{Type: wire.ComponentRow, Key: fmt.Sprintf("bookmark-%d-%d", st.Start, i), Title: clipText(item.Description, 1800), Subtitle: clipText(item.Tags+" · "+item.Time, 1800)}
		if link := safeBookmarkLink(item.Href); link != "" {
			row = wire.Component{Type: wire.ComponentLink, Key: fmt.Sprintf("bookmark-%d-%d", st.Start, i), Title: clipText(item.Description, 1800), URL: link}
		}
		section.Items = append(section.Items, row)
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noBookmarks})
	}
	if st.Mode == "archive" && len(items) == 50 {
		section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more"}}
	}
	view := st.Mode
	if view == "" {
		view = "recent"
	}
	v := &wire.View{Title: t.bookmarks, State: ext.EncodeState(st), Search: &wire.Search{Placeholder: t.tags, Value: st.Tags, Action: wire.Action{ID: "search"}}, Filter: &wire.Filter{
		Name: "view", Title: t.bookmarks, Value: view, Action: wire.Action{ID: "view"},
		Choices: []wire.Choice{{Value: "recent", Title: t.recent}, {Value: "archive", Title: t.archive}},
	}, Sections: []wire.Section{section, {Title: t.bookmarks, Items: []wire.Component{
		{Type: wire.ComponentButton, Title: t.create, Action: &wire.Action{ID: "create", Push: &wire.Push{Title: t.create, State: ext.EncodeState(pageState{Mode: "create", DraftID: randomID()})}}},
	}}}}
	return v, nil
}

func (a *app) createView(r *ext.UIRequest, st pageState) *wire.View {
	t := textsFor(r.Locale)
	if st.Mode == "review-create" {
		in := addArgs{URL: st.URL, Title: st.Title, Notes: st.Notes, Tags: st.NewTags}
		if err := in.validate(); err != nil {
			return &wire.View{Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: err.Error()}}}}}
		}
		items := []wire.Component{{Type: wire.ComponentText, Text: clipText(st.Title, 1800)}, {Type: wire.ComponentText, Text: clipText(st.URL, 1800)}}
		if st.Notes != "" {
			items = append(items, textComponents(st.Notes)...)
		}
		items = append(items, wire.Component{Type: wire.ComponentText, Text: t.tags + ": " + st.NewTags}, wire.Component{Type: wire.ComponentButton, Title: t.create, Action: &wire.Action{ID: "confirm-create", Confirm: t.create + "\n" + st.Title}}, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "create"}})
		return &wire.View{Title: t.review, State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.create, Items: items}}}
	}
	return &wire.View{Title: t.create, State: ext.EncodeState(st), Sections: []wire.Section{{Items: []wire.Component{
		{Type: wire.ComponentField, Kind: "text", Name: "url", Label: t.url, Value: st.URL},
		{Type: wire.ComponentField, Kind: "text", Name: "title", Label: t.title, Value: st.Title},
		{Type: wire.ComponentField, Kind: "text", Name: "notes", Label: t.notes, Value: st.Notes},
		{Type: wire.ComponentField, Kind: "text", Name: "tags", Label: t.tags, Value: st.NewTags},
		{Type: wire.ComponentButton, Title: t.review, Action: &wire.Action{ID: "review-create", Submit: true}},
		{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
	}}}}
}

func textComponents(text string) []wire.Component {
	var out []wire.Component
	for _, line := range strings.Split(text, "\n") {
		r := []rune(line)
		for len(r) > 0 {
			n := min(len(r), 1800)
			out = append(out, wire.Component{Type: wire.ComponentText, Text: string(r[:n])})
			r = r[n:]
			if len(out) >= 240 {
				out[len(out)-1].Text = clipText(out[len(out)-1].Text, 1700) + " […]"
				return out
			}
		}
	}
	if len(out) == 0 {
		out = append(out, wire.Component{Type: wire.ComponentText, Text: " ", Style: wire.TextStyleSecondary})
	}
	return out
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
