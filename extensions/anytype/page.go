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
	Mode      string `json:"m,omitempty"`
	SpaceID   string `json:"s,omitempty"`
	SpaceName string `json:"n,omitempty"`
	ObjectID  string `json:"i,omitempty"`
	Offset    int    `json:"o,omitempty"`
	Query     string `json:"q,omitempty"`
	DraftID   string `json:"d,omitempty"`
	Name      string `json:"a,omitempty"`
	Body      string `json:"b,omitempty"`
	TypeKey   string `json:"t,omitempty"`
}

func readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || st.Offset < 0 || len(st.SpaceID) > 256 || len(st.SpaceName) > 1000 || len(st.ObjectID) > 256 || len(st.DraftID) > 128 || len(st.Name) > 300 || len(st.Body) > 31000 || len(st.Query) > 250 {
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
		st.Query, st.Offset, st.Mode = clipText(r.Values["q"], 200), 0, "search"
	case "space":
		if err := validObjectID(r.Values["id"]); err != nil {
			return nil, err
		}
		if r.Values["name"] == "" || len(r.Values["name"]) > 255 {
			return nil, errors.New("invalid space name")
		}
		st = pageState{Mode: "objects", SpaceID: r.Values["id"], SpaceName: r.Values["name"]}
	case "spaces":
		st = pageState{}
	case "more":
		st.Offset += 50
	case "detail":
		if err := validObjectID(r.Values["id"]); err != nil {
			return nil, err
		}
		if r.Values["space"] != "" {
			st.SpaceID = r.Values["space"]
		}
		st.Mode, st.ObjectID = "detail", r.Values["id"]
	case "create":
		if st.Mode != "review-create" {
			st = pageState{Mode: "create", SpaceID: st.SpaceID, SpaceName: st.SpaceName, TypeKey: "page", DraftID: randomID()}
		} else {
			st.Mode = "create"
		}
	case "back":
		st.Mode, st.ObjectID, st.DraftID, st.Name, st.Body = "objects", "", "", "", ""
	case "review-create":
		in := createArgs{SpaceID: st.SpaceID, TypeKey: r.Values["type_key"], Name: r.Values["name"], Body: r.Values["body"]}
		if err := in.validate(); err != nil {
			return nil, err
		}
		st.TypeKey, st.Name, st.Body, st.Mode = in.TypeKey, in.Name, in.Body, "review-create"
	case "confirm-create":
		raw, _ := json.Marshal(createArgs{SpaceID: st.SpaceID, TypeKey: st.TypeKey, Name: st.Name, Body: st.Body})
		out, err := a.doAction(ctx, "ui-create:"+st.DraftID, "anytype_create_object", raw)
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
	if a.client.base == "" || a.client.token == "" {
		return &wire.View{Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: t.missingConfig}}}}}, nil
	}
	if st.Mode == "create" || st.Mode == "review-create" {
		return a.createView(ctx, r, st)
	}
	if st.Mode == "search" {
		return a.searchView(ctx, r, st)
	}
	if st.Mode == "detail" {
		item, err := a.client.object(ctx, st.SpaceID, st.ObjectID)
		a.record(err)
		if err != nil {
			return nil, err
		}
		return detailView(r, st, item), nil
	}
	if st.Mode == "objects" {
		if err := validObjectID(st.SpaceID); err != nil {
			return nil, err
		}
		page, err := a.client.objects(ctx, st.SpaceID, st.Offset)
		a.record(err)
		if err != nil {
			return nil, err
		}
		section := wire.Section{Title: fmt.Sprintf(t.objects, len(page.Data))}
		for _, item := range page.Data {
			if item.ID == "" || item.Name == "" {
				continue
			}
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "object-" + item.ID, Title: clipText(item.Name, 1800), Subtitle: clipText(item.Type.Name+" · "+item.Snippet, 1800), Action: &wire.Action{ID: "detail", Values: map[string]string{"id": item.ID, "space": st.SpaceID}}})
		}
		if len(section.Items) == 0 {
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noObjects})
		}
		if page.Pagination.HasMore {
			section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more"}}
		}
		v := &wire.View{Title: clipText(st.SpaceName, 1800), State: ext.EncodeState(st), Sections: []wire.Section{section, {Title: t.objects, Items: []wire.Component{
			{Type: wire.ComponentButton, Title: t.create, Action: &wire.Action{ID: "create", Push: &wire.Push{Title: t.create, State: ext.EncodeState(pageState{Mode: "create", SpaceID: st.SpaceID, SpaceName: st.SpaceName, TypeKey: "page", DraftID: randomID()})}}},
			{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "spaces"}},
		}}}}
		return v, nil
	}
	if st.Mode != "" {
		return nil, errors.New("invalid knowledge view")
	}
	page, err := a.client.spaces(ctx, st.Offset)
	a.record(err)
	if err != nil {
		return nil, err
	}
	section := wire.Section{Title: t.spaces}
	for _, item := range page.Data {
		if item.ID == "" || item.Name == "" {
			continue
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "space-" + item.ID, Title: clipText(item.Name, 1800), Subtitle: item.ID, Action: &wire.Action{ID: "space", Values: map[string]string{"id": item.ID, "name": item.Name}}})
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noSpaces})
	}
	if page.Pagination.HasMore {
		section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more"}}
	}
	return &wire.View{Title: t.knowledge, State: ext.EncodeState(st), Search: &wire.Search{Placeholder: t.search, Value: st.Query, Action: wire.Action{ID: "search"}}, Sections: []wire.Section{section}}, nil
}

func (a *app) searchView(ctx context.Context, r *ext.UIRequest, st pageState) (*wire.View, error) {
	t := textsFor(r.Locale)
	page, err := a.client.search(ctx, st.Query, st.Offset)
	a.record(err)
	if err != nil {
		return nil, err
	}
	section := wire.Section{Title: fmt.Sprintf(t.objects, len(page.Data))}
	for _, item := range page.Data {
		if item.ID == "" || item.Name == "" || item.SpaceID == "" {
			continue
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "object-" + item.SpaceID + "-" + item.ID, Title: clipText(item.Name, 1800), Subtitle: clipText(item.Type.Name+" · "+item.Snippet, 1800), Action: &wire.Action{ID: "detail", Values: map[string]string{"id": item.ID, "space": item.SpaceID}}})
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noObjects})
	}
	if page.Pagination.HasMore {
		section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more"}}
	}
	return &wire.View{Title: t.search, State: ext.EncodeState(st), Search: &wire.Search{Placeholder: t.search, Value: st.Query, Action: wire.Action{ID: "search"}}, Sections: []wire.Section{section, {Items: []wire.Component{{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "spaces"}}}}}}, nil
}

func (a *app) createView(ctx context.Context, r *ext.UIRequest, st pageState) (*wire.View, error) {
	t := textsFor(r.Locale)
	if st.Mode == "review-create" {
		in := createArgs{SpaceID: st.SpaceID, TypeKey: st.TypeKey, Name: st.Name, Body: st.Body}
		if err := in.validate(); err != nil {
			return nil, err
		}
		items := []wire.Component{{Type: wire.ComponentText, Text: clipText(st.Name, 1800)}}
		items = append(items, textComponents(st.Body)...)
		items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.create, Action: &wire.Action{ID: "confirm-create", Confirm: t.create + "\n" + st.Name}}, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "create"}})
		return &wire.View{Title: t.review, State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.create, Items: items}}}, nil
	}
	if err := validObjectID(st.SpaceID); err != nil {
		return nil, err
	}
	return &wire.View{Title: t.create, State: ext.EncodeState(st), Sections: []wire.Section{{Title: st.SpaceName, Items: []wire.Component{
		{Type: wire.ComponentField, Kind: "text", Name: "name", Label: t.name, Value: st.Name},
		{Type: wire.ComponentField, Kind: "text", Name: "body", Label: t.body, Value: st.Body},
		{Type: wire.ComponentField, Kind: "choice", Name: "type_key", Label: t.typeLabel, Value: selectedType(st.TypeKey), Choices: []wire.Choice{{Value: "page", Title: t.pageType}, {Value: "note", Title: t.noteType}}},
		{Type: wire.ComponentButton, Title: t.review, Action: &wire.Action{ID: "review-create", Submit: true}},
		{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
	}}}}, nil
}

func detailView(r *ext.UIRequest, st pageState, item knowledgeObject) *wire.View {
	t := textsFor(r.Locale)
	items := []wire.Component{{Type: wire.ComponentText, Text: clipText(item.Type.Name+" · "+item.Snippet, 1800)}, {Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}}}
	v := &wire.View{Title: clipText(item.Name, 1800), State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.objects, Items: items}}}
	if text := objectText(item); text != "" {
		v.Sections = append(v.Sections, wire.Section{Title: t.body, Items: textComponents(clipText(text, 60000))})
	}
	return v
}

func selectedType(value string) string {
	if value == "note" {
		return "note"
	}
	return "page"
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
