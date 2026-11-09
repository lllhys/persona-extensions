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
	Mode      string `json:"m,omitempty"`
	BoardID   int64  `json:"b,omitempty"`
	BoardName string `json:"n,omitempty"`
	Cursor    string `json:"c,omitempty"`
	DraftID   string `json:"d,omitempty"`
	Name      string `json:"a,omitempty"`
	ItemID    int64  `json:"i,omitempty"`
	ColumnID  string `json:"l,omitempty"`
	Label     string `json:"s,omitempty"`
}

func readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || st.BoardID < 0 || st.ItemID < 0 || len(st.BoardName) > 1000 || len(st.DraftID) > 128 || len(st.Name) > 600 || len(st.Cursor) > 2048 || len(st.ColumnID) > 100 || len(st.Label) > 100 {
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
	case "board":
		id, err := strconv.ParseInt(r.Values["id"], 10, 64)
		if err != nil || id <= 0 || r.Values["name"] == "" {
			return nil, errors.New("invalid board")
		}
		st = pageState{Mode: "items", BoardID: id, BoardName: r.Values["name"]}
	case "boards":
		st = pageState{}
	case "more":
		if st.Cursor == "" || len(st.Cursor) > 2048 || strings.ContainsAny(st.Cursor, "\x00\r\n\"'") {
			return nil, errors.New("invalid next cursor")
		}
	case "detail":
		id, err := strconv.ParseInt(r.Values["id"], 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("invalid item ID")
		}
		st.Mode, st.ItemID = "detail", id
	case "create":
		if st.Mode != "review-create" {
			st = pageState{Mode: "create", BoardID: st.BoardID, BoardName: st.BoardName, DraftID: randomID()}
		} else {
			st.Mode = "create"
		}
	case "edit":
		st.Mode = "edit"
		if st.DraftID == "" {
			st.DraftID = randomID()
		}
	case "back":
		st.Mode, st.ItemID, st.DraftID, st.Name, st.ColumnID, st.Label = "items", 0, "", "", "", ""
	case "review-create":
		in := createArgs{BoardID: st.BoardID, Name: r.Values["name"]}
		if err := in.validate(); err != nil {
			return nil, err
		}
		st.Name, st.Mode = in.Name, "review-create"
	case "review-edit":
		in := statusArgs{BoardID: st.BoardID, ItemID: st.ItemID, ColumnID: r.Values["column_id"], Label: r.Values["label"]}
		if err := in.validate(); err != nil {
			return nil, err
		}
		st.ColumnID, st.Label, st.Mode = in.ColumnID, in.Label, "review-edit"
	case "confirm-create":
		raw, _ := json.Marshal(createArgs{BoardID: st.BoardID, Name: st.Name})
		out, err := a.doAction(ctx, "ui-create:"+st.DraftID, "monday_create_item", raw)
		if err != nil {
			return nil, err
		}
		return &wire.ActionResult{Toast: out["text"].(string), Pop: true}, nil
	case "confirm-edit":
		raw, _ := json.Marshal(statusArgs{BoardID: st.BoardID, ItemID: st.ItemID, ColumnID: st.ColumnID, Label: st.Label})
		out, err := a.doAction(ctx, "ui-edit:"+st.DraftID, "monday_update_status", raw)
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
	if st.Mode == "create" || st.Mode == "review-create" || st.Mode == "edit" || st.Mode == "review-edit" {
		return a.editorView(ctx, r, st)
	}
	if st.Mode == "detail" {
		board, err := a.client.board(ctx, st.BoardID)
		if err != nil {
			return nil, err
		}
		items, err := a.client.items(ctx, st.BoardID, "")
		if err != nil {
			return nil, err
		}
		for _, item := range items.Items {
			if item.ID == st.ItemID {
				st.DraftID = randomID()
				return detailView(r, st, board, item), nil
			}
		}
		return nil, errors.New("the item is not on the current page; use monday_items with the exact board and item ID")
	}
	if st.Mode == "items" {
		page, err := a.client.items(ctx, st.BoardID, st.Cursor)
		a.record(err)
		if err != nil {
			return nil, err
		}
		section := wire.Section{Title: t.items}
		for _, item := range page.Items {
			if item.ID <= 0 || item.Name == "" {
				continue
			}
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "item-" + fmt.Sprint(item.ID), Title: clipText(item.Name, 1800), Subtitle: clipText(item.UpdatedAt, 100), Action: &wire.Action{ID: "detail", Values: map[string]string{"id": fmt.Sprint(item.ID)}}})
		}
		if len(section.Items) == 0 {
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noItems})
		}
		if page.Cursor != "" {
			section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more"}}
		}
		v := &wire.View{Title: clipText(st.BoardName, 1800), State: ext.EncodeState(st), Sections: []wire.Section{section, {Title: t.items, Items: []wire.Component{
			{Type: wire.ComponentButton, Title: t.create, Action: &wire.Action{ID: "create", Push: &wire.Push{Title: t.create, State: ext.EncodeState(pageState{Mode: "create", BoardID: st.BoardID, BoardName: st.BoardName, DraftID: randomID()})}}},
			{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "boards"}},
		}}}}
		return v, nil
	}
	if st.Mode != "" {
		return nil, errors.New("invalid item view")
	}
	boards, err := a.client.boards(ctx)
	a.record(err)
	if err != nil {
		return nil, err
	}
	section := wire.Section{Title: t.boards}
	for _, item := range boards {
		if item.ID <= 0 || item.Name == "" {
			continue
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "board-" + fmt.Sprint(item.ID), Title: clipText(item.Name, 1800), Subtitle: fmt.Sprint(item.ID), Action: &wire.Action{ID: "board", Values: map[string]string{"id": fmt.Sprint(item.ID), "name": item.Name}}})
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noBoards})
	}
	return &wire.View{Title: t.boards, State: ext.EncodeState(st), Sections: []wire.Section{section}}, nil
}

func (a *app) editorView(ctx context.Context, r *ext.UIRequest, st pageState) (*wire.View, error) {
	t := textsFor(r.Locale)
	if st.Mode == "review-create" || st.Mode == "review-edit" {
		action, back := "confirm-create", "create"
		if st.Mode == "review-edit" {
			action, back = "confirm-edit", "edit"
		}
		items := []wire.Component{{Type: wire.ComponentText, Text: clipText(st.Name+st.Label, 1800)}}
		items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.review, Action: &wire.Action{ID: action, Confirm: t.review + "\n" + clipText(st.Name+st.Label, 500)}}, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: back}})
		return &wire.View{Title: t.review, State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.review, Items: items}}}, nil
	}
	var items []wire.Component
	action := "review-create"
	if st.Mode == "edit" {
		action = "review-edit"
		board, err := a.client.board(ctx, st.BoardID)
		if err != nil {
			return nil, err
		}
		choices := statusChoices(board, st.ColumnID)
		if len(choices) > 0 {
			items = append(items, wire.Component{Type: wire.ComponentField, Kind: "choice", Name: "label", Label: t.label, Value: choices[0].Value, Choices: choices})
		} else {
			items = append(items, wire.Component{Type: wire.ComponentField, Kind: "text", Name: "label", Label: t.label, Value: st.Label})
		}
	} else {
		items = []wire.Component{{Type: wire.ComponentField, Kind: "text", Name: "name", Label: t.name, Value: st.Name}}
	}
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.review, Action: &wire.Action{ID: action, Submit: true}}, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}})
	return &wire.View{Title: t.review, State: ext.EncodeState(st), Sections: []wire.Section{{Items: items}}}, nil
}

func detailView(r *ext.UIRequest, st pageState, board board, item item) *wire.View {
	t := textsFor(r.Locale)
	items := []wire.Component{{Type: wire.ComponentText, Text: clipText(item.UpdatedAt, 100)}}
	if link := safeBoardLink(item.URL); link != "" {
		items = append(items, wire.Component{Type: wire.ComponentLink, Title: t.open, URL: link})
	}
	for _, column := range item.ColumnValues {
		if column.Text != "" {
			items = append(items, wire.Component{Type: wire.ComponentText, Text: clipText(column.ID+": "+column.Text, 1800)})
		}
	}
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.update, Action: &wire.Action{ID: "edit", Push: &wire.Push{Title: t.update, State: ext.EncodeState(pageState{Mode: "edit", BoardID: st.BoardID, ItemID: st.ItemID, DraftID: randomID()})}}}, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}})
	return &wire.View{Title: clipText(item.Name, 1800), State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.items, Items: items}}}
}

func statusChoices(board board, selected string) []wire.Choice {
	for _, column := range board.Columns {
		if column.Type != "status" || (selected != "" && column.ID != selected) {
			continue
		}
		var settings struct {
			Labels map[string]string `json:"labels"`
		}
		if json.Unmarshal([]byte(column.SettingsStr), &settings) != nil {
			continue
		}
		var out []wire.Choice
		for _, name := range settings.Labels {
			if name != "" && len(out) < 50 {
				out = append(out, wire.Choice{Value: name, Title: name})
			}
		}
		return out
	}
	return nil
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
