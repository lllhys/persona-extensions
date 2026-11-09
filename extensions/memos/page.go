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
	View      string `json:"v,omitempty"`
	Query     string `json:"q,omitempty"`
	PageToken string `json:"p,omitempty"`
	MemoID    string `json:"i,omitempty"`
	Mode      string `json:"m,omitempty"`
	DraftID   string `json:"d,omitempty"`
	Content   string `json:"c,omitempty"`
}

func readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || len(st.MemoID) > 80 || len(st.DraftID) > 128 || len(st.Content) > 11000 || len(st.PageToken) > 2048 || len(st.Query) > 300 {
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
		st.Query, st.PageToken = clipText(r.Values["q"], 200), ""
		if strings.TrimSpace(st.Query) != "" {
			st.View = "search"
		} else {
			st.View = "recent"
		}
	case "view":
		if r.Values["view"] != "recent" && r.Values["view"] != "pinned" {
			return nil, errors.New("invalid notes view")
		}
		st.View, st.Query, st.PageToken = r.Values["view"], "", ""
	case "more":
		if err := validPageToken(r.Values["token"]); err != nil || r.Values["token"] == "" {
			return nil, errors.New("invalid next page token")
		}
		st.PageToken = r.Values["token"]
	case "detail":
		if err := validMemoID(r.Values["id"]); err != nil {
			return nil, err
		}
		st.Mode, st.MemoID = "detail", r.Values["id"]
	case "create":
		if st.Mode != "review-create" {
			st = pageState{Mode: "create", DraftID: randomID()}
		} else {
			st.Mode = "create"
		}
	case "edit":
		st.Mode = "edit"
		if st.DraftID == "" {
			st.DraftID = randomID()
		}
	case "back":
		st.Mode, st.MemoID, st.DraftID, st.Content = "", "", "", ""
	case "review-create":
		if err := validContent(r.Values["content"]); err != nil {
			return nil, err
		}
		st.Content, st.Mode = r.Values["content"], "review-create"
	case "review-edit":
		if err := validContent(r.Values["content"]); err != nil {
			return nil, err
		}
		st.Content, st.Mode = r.Values["content"], "review-edit"
	case "confirm-create":
		raw, _ := json.Marshal(createArgs{Content: st.Content})
		out, err := a.doAction(ctx, "ui-create:"+st.DraftID, "memos_create", raw)
		if err != nil {
			return nil, err
		}
		return &wire.ActionResult{Toast: out["text"].(string), Pop: true}, nil
	case "confirm-edit":
		raw, _ := json.Marshal(updateArgs{MemoID: st.MemoID, Content: st.Content})
		out, err := a.doAction(ctx, "ui-edit:"+st.DraftID, "memos_update", raw)
		if err != nil {
			return nil, err
		}
		return &wire.ActionResult{Toast: out["text"].(string), Pop: true}, nil
	case "pin":
		if st.MemoID == "" || st.DraftID == "" {
			return nil, errors.New("memo and draft are required")
		}
		pinned, err := strconvBool(r.Values["pinned"])
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(pinArgs{MemoID: st.MemoID, Pinned: pinned})
		out, err := a.doAction(ctx, "ui-pin:"+st.DraftID, "memos_toggle_pin", raw)
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
	if a.client.base == "" || a.client.token == "" || a.creator == "" {
		return &wire.View{Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: t.missingConfig}}}}}, nil
	}
	switch st.Mode {
	case "create", "review-create", "edit", "review-edit":
		return a.editorView(r, st), nil
	case "detail":
		if err := validMemoID(st.MemoID); err != nil {
			return nil, err
		}
		item, err := a.client.memo(ctx, st.MemoID)
		a.record(err)
		if err != nil {
			return nil, err
		}
		if item.Creator != a.creator {
			return nil, errors.New("this note belongs to another user")
		}
		st.DraftID = randomID()
		return detailView(r, st, item), nil
	case "":
	default:
		return nil, errors.New("invalid notes view")
	}
	if st.View == "" {
		st.View = "recent"
	}
	page, err := a.client.memos(ctx, a.creator, st.View, st.Query, st.PageToken)
	a.record(err)
	if err != nil {
		return nil, err
	}
	v := &wire.View{Title: t.notes, State: ext.EncodeState(st), Search: &wire.Search{Placeholder: t.search, Value: st.Query, Action: wire.Action{ID: "search"}}, Filter: &wire.Filter{
		Name: "view", Title: t.notes, Value: st.View, Action: wire.Action{ID: "view"},
		Choices: []wire.Choice{{Value: "recent", Title: t.recent}, {Value: "pinned", Title: t.pinned}},
	}}
	if st.View == "search" {
		v.Filter.Value = "recent"
	}
	section := wire.Section{Title: fmt.Sprintf(t.results, len(page.Memos))}
	for _, item := range page.Memos {
		id := memoID(item.Name)
		if id == "" || item.Creator != a.creator {
			continue
		}
		title := item.Snippet
		if title == "" {
			title = item.Content
		}
		if title == "" {
			continue
		}
		subtitle := item.UpdateTime
		if item.Pinned {
			subtitle = "📌 " + subtitle
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "memo-" + id, Title: clipText(title, 1800), Subtitle: subtitle, Action: &wire.Action{ID: "detail", Values: map[string]string{"id": id}}})
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noNotes})
	}
	if page.NextPageToken != "" {
		section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more", Values: map[string]string{"token": page.NextPageToken}}}
	}
	v.Sections = []wire.Section{section, {Title: t.notes, Items: []wire.Component{{Type: wire.ComponentButton, Title: t.create, Action: &wire.Action{ID: "create", Push: &wire.Push{Title: t.create, State: ext.EncodeState(pageState{Mode: "create", DraftID: randomID()})}}}}}}
	return v, nil
}

func (a *app) editorView(r *ext.UIRequest, st pageState) *wire.View {
	t := textsFor(r.Locale)
	if st.Mode == "review-create" || st.Mode == "review-edit" {
		action := "confirm-create"
		if st.Mode == "review-edit" {
			action = "confirm-edit"
		}
		items := textComponents(st.Content)
		items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.save, Action: &wire.Action{ID: action, Confirm: t.save + "\n" + clipText(st.Content, 500)}}, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: map[bool]string{true: "edit", false: "create"}[st.Mode == "review-edit"]}})
		return &wire.View{Title: t.review, State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.review, Items: items}}}
	}
	action, title := "review-create", t.create
	if st.Mode == "edit" {
		action, title = "review-edit", t.edit
	}
	return &wire.View{Title: title, State: ext.EncodeState(st), Sections: []wire.Section{{Items: []wire.Component{
		{Type: wire.ComponentField, Kind: "text", Name: "content", Label: t.content, Value: st.Content},
		{Type: wire.ComponentButton, Title: t.review, Action: &wire.Action{ID: action, Submit: true}},
		{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
	}}}}
}

func detailView(r *ext.UIRequest, st pageState, item memo) *wire.View {
	t := textsFor(r.Locale)
	pinTitle := t.pin
	pinned := true
	if item.Pinned {
		pinTitle, pinned = t.unpin, false
	}
	items := []wire.Component{
		{Type: wire.ComponentText, Text: clipText(item.UpdateTime, 100)},
	}
	if len(item.Content) <= 10000 {
		items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.edit, Action: &wire.Action{ID: "edit", Push: &wire.Push{Title: t.edit, State: ext.EncodeState(pageState{Mode: "edit", MemoID: st.MemoID, Content: item.Content, DraftID: randomID()})}}})
	}
	items = append(items,
		wire.Component{Type: wire.ComponentButton, Title: pinTitle, Action: &wire.Action{ID: "pin", Values: map[string]string{"pinned": fmt.Sprint(pinned)}, Confirm: pinTitle + "\n" + clipText(item.Snippet, 500)}},
		wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
	)
	v := &wire.View{Title: clipText(item.Snippet, 1800), State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.notes, Items: items}}}
	if item.Content != "" {
		v.Sections = append(v.Sections, wire.Section{Title: t.content, Items: textComponents(clipText(item.Content, 60000))})
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

func strconvBool(value string) (bool, error) {
	if value == "true" {
		return true, nil
	}
	if value == "false" {
		return false, nil
	}
	return false, errors.New("invalid pinned flag")
}
