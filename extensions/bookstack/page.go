package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

type pageState struct {
	Mode       string `json:"m,omitempty"`
	BookID     int64  `json:"b,omitempty"`
	BookName   string `json:"n,omitempty"`
	PageID     int64  `json:"i,omitempty"`
	Offset     int    `json:"o,omitempty"`
	Search     string `json:"q,omitempty"`
	SearchPage int    `json:"s,omitempty"`
	DraftID    string `json:"d,omitempty"`
	Name       string `json:"a,omitempty"`
	Markdown   string `json:"c,omitempty"`
}

func readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || st.BookID < 0 || st.PageID < 0 || st.Offset < 0 || st.SearchPage < 0 || len(st.BookName) > 1000 || len(st.DraftID) > 128 || len(st.Name) > 300 || len(st.Markdown) > 65000 || len(st.Search) > 250 {
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
		st.Search, st.SearchPage, st.Mode = clipText(r.Values["q"], 200), 1, "search"
	case "more-search":
		st.SearchPage++
	case "book":
		id, err := strconv.ParseInt(r.Values["id"], 10, 64)
		if err != nil || id <= 0 || r.Values["name"] == "" || len(r.Values["name"]) > 255 {
			return nil, errors.New("invalid book")
		}
		st = pageState{Mode: "pages", BookID: id, BookName: r.Values["name"]}
	case "books":
		st = pageState{}
	case "more":
		st.Offset += 50
	case "detail":
		id, err := strconv.ParseInt(r.Values["id"], 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("invalid page ID")
		}
		st.Mode, st.PageID = "detail", id
	case "create":
		if st.Mode != "review-create" {
			st = pageState{Mode: "create", BookID: st.BookID, BookName: st.BookName, DraftID: randomID()}
		} else {
			st.Mode = "create"
		}
	case "edit":
		st.Mode = "edit"
		if st.DraftID == "" {
			st.DraftID = randomID()
		}
	case "back":
		st.Mode, st.PageID, st.DraftID, st.Name, st.Markdown = "pages", 0, "", "", ""
	case "review-create":
		in := createArgs{BookID: st.BookID, Name: r.Values["name"], Markdown: r.Values["markdown"]}
		if err := in.validate(); err != nil {
			return nil, err
		}
		st.Name, st.Markdown, st.Mode = in.Name, in.Markdown, "review-create"
	case "review-edit":
		in := updateArgs{PageID: st.PageID, Name: r.Values["name"], Markdown: r.Values["markdown"]}
		if err := in.validate(); err != nil {
			return nil, err
		}
		st.Name, st.Markdown, st.Mode = in.Name, in.Markdown, "review-edit"
	case "confirm-create":
		raw, _ := json.Marshal(createArgs{BookID: st.BookID, Name: st.Name, Markdown: st.Markdown})
		out, err := a.doAction(ctx, "ui-create:"+st.DraftID, "bookstack_create_page", raw)
		if err != nil {
			return nil, err
		}
		return &wire.ActionResult{Toast: out["text"].(string), Pop: true}, nil
	case "confirm-edit":
		raw, _ := json.Marshal(updateArgs{PageID: st.PageID, Name: st.Name, Markdown: st.Markdown})
		out, err := a.doAction(ctx, "ui-edit:"+st.DraftID, "bookstack_update_page", raw)
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
	if a.client.base == "" || a.client.tokenID == "" || a.client.tokenSecret == "" {
		return &wire.View{Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: t.missingConfig}}}}}, nil
	}
	if st.Mode == "create" || st.Mode == "review-create" || st.Mode == "edit" || st.Mode == "review-edit" {
		return a.editorView(r, st), nil
	}
	if st.Mode == "search" {
		return a.searchView(ctx, r, st)
	}
	if st.Mode == "detail" {
		item, err := a.client.page(ctx, st.PageID)
		a.record(err)
		if err != nil {
			return nil, err
		}
		text := item.Markdown
		if text == "" {
			text, err = a.client.pageText(ctx, st.PageID)
			if err != nil {
				return nil, err
			}
		}
		st.DraftID = randomID()
		return detailView(r, st, item, text), nil
	}
	if st.Mode == "pages" {
		if err := validID(st.BookID); err != nil {
			return nil, err
		}
		page, err := a.client.pages(ctx, st.BookID, st.Offset)
		a.record(err)
		if err != nil {
			return nil, err
		}
		section := wire.Section{Title: fmt.Sprintf(t.pages, len(page.Data))}
		for _, item := range page.Data {
			if item.ID <= 0 || item.Name == "" {
				continue
			}
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "page-" + fmt.Sprint(item.ID), Title: clipText(item.Name, 1800), Subtitle: clipText(item.UpdatedAt, 100), Action: &wire.Action{ID: "detail", Values: map[string]string{"id": fmt.Sprint(item.ID)}}})
		}
		if len(section.Items) == 0 {
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noPages})
		}
		if len(page.Data) == 50 {
			section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more"}}
		}
		v := &wire.View{Title: clipText(st.BookName, 1800), State: ext.EncodeState(st), Sections: []wire.Section{section, {Title: t.pages, Items: []wire.Component{
			{Type: wire.ComponentButton, Title: t.create, Action: &wire.Action{ID: "create", Push: &wire.Push{Title: t.create, State: ext.EncodeState(pageState{Mode: "create", BookID: st.BookID, BookName: st.BookName, DraftID: randomID()})}}},
			{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "books"}},
		}}}}
		return v, nil
	}
	if st.Mode != "" {
		return nil, errors.New("invalid knowledge view")
	}
	page, err := a.client.books(ctx, st.Offset)
	a.record(err)
	if err != nil {
		return nil, err
	}
	section := wire.Section{Title: t.books}
	for _, item := range page.Data {
		if item.ID <= 0 || item.Name == "" {
			continue
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "book-" + fmt.Sprint(item.ID), Title: clipText(item.Name, 1800), Subtitle: clipText(item.Description, 500), Action: &wire.Action{ID: "book", Values: map[string]string{"id": fmt.Sprint(item.ID), "name": item.Name}}})
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noBooks})
	}
	if len(page.Data) == 50 {
		section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more"}}
	}
	v := &wire.View{Title: t.knowledge, State: ext.EncodeState(st), Search: &wire.Search{Placeholder: t.search, Value: st.Search, Action: wire.Action{ID: "search"}}, Sections: []wire.Section{section}}
	return v, nil
}

func (a *app) searchView(ctx context.Context, r *ext.UIRequest, st pageState) (*wire.View, error) {
	t := textsFor(r.Locale)
	if st.SearchPage < 1 {
		st.SearchPage = 1
	}
	page, err := a.client.search(ctx, st.Search, st.SearchPage)
	a.record(err)
	if err != nil {
		return nil, err
	}
	section := wire.Section{Title: fmt.Sprintf(t.pages, len(page.Data))}
	for _, item := range page.Data {
		if item.ID <= 0 || item.Name == "" {
			continue
		}
		row := wire.Component{Type: wire.ComponentRow, Key: item.Type + "-" + fmt.Sprint(item.ID), Title: clipText(item.Name, 1800), Subtitle: item.Type}
		if item.Type == "page" {
			row.Action = &wire.Action{ID: "detail", Values: map[string]string{"id": fmt.Sprint(item.ID)}}
		} else if link := safePageLink(item.URL); link != "" {
			row = wire.Component{Type: wire.ComponentLink, Key: item.Type + "-" + fmt.Sprint(item.ID), Title: clipText(item.Name, 1800), URL: link}
		}
		section.Items = append(section.Items, row)
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noPages})
	}
	if len(page.Data) == 50 {
		section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more-search"}}
	}
	return &wire.View{Title: t.search, State: ext.EncodeState(st), Search: &wire.Search{Placeholder: t.search, Value: st.Search, Action: wire.Action{ID: "search"}}, Sections: []wire.Section{section, {Items: []wire.Component{{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "books"}}}}}}, nil
}

func (a *app) editorView(r *ext.UIRequest, st pageState) *wire.View {
	t := textsFor(r.Locale)
	if st.Mode == "review-create" || st.Mode == "review-edit" {
		action := "confirm-create"
		if st.Mode == "review-edit" {
			action = "confirm-edit"
		}
		items := []wire.Component{{Type: wire.ComponentText, Text: clipText(st.Name, 1800)}}
		items = append(items, textComponents(st.Markdown)...)
		items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.save, Action: &wire.Action{ID: action, Confirm: t.save + "\n" + st.Name}}, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: map[bool]string{true: "edit", false: "create"}[st.Mode == "review-edit"]}})
		return &wire.View{Title: t.review, State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.review, Items: items}}}
	}
	action, title := "review-create", t.create
	if st.Mode == "edit" {
		action, title = "review-edit", t.edit
	}
	return &wire.View{Title: title, State: ext.EncodeState(st), Sections: []wire.Section{{Items: []wire.Component{
		{Type: wire.ComponentField, Kind: "text", Name: "name", Label: t.name, Value: st.Name},
		{Type: wire.ComponentField, Kind: "text", Name: "markdown", Label: t.content, Value: st.Markdown},
		{Type: wire.ComponentButton, Title: t.review, Action: &wire.Action{ID: action, Submit: true}},
		{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
	}}}}
}

func detailView(r *ext.UIRequest, st pageState, item page, text string) *wire.View {
	t := textsFor(r.Locale)
	items := []wire.Component{{Type: wire.ComponentText, Text: clipText(item.UpdatedAt, 100)}}
	if link := safePageLink(item.URL); link != "" {
		items = append(items, wire.Component{Type: wire.ComponentLink, Title: t.open, URL: link})
	}
	if item.Markdown != "" && len(item.Markdown) <= 10000 {
		items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.edit, Action: &wire.Action{ID: "edit", Push: &wire.Push{Title: t.edit, State: ext.EncodeState(pageState{Mode: "edit", BookID: item.BookID, PageID: item.ID, Name: item.Name, Markdown: item.Markdown, DraftID: randomID()})}}})
	} else if item.Markdown == "" {
		items = append(items, wire.Component{Type: wire.ComponentText, Text: t.noMarkdown, Style: wire.TextStyleSecondary})
	}
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}})
	v := &wire.View{Title: clipText(item.Name, 1800), State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.pages, Items: items}}}
	if text != "" {
		v.Sections = append(v.Sections, wire.Section{Title: t.content, Items: textComponents(clipText(text, 60000))})
	} else {
		v.Sections = append(v.Sections, wire.Section{Title: t.content, Items: []wire.Component{{Type: wire.ComponentText, Text: t.noContent, Style: wire.TextStyleSecondary}}})
	}
	return v
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
