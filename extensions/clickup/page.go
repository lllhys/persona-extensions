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
	Mode        string `json:"m,omitempty"`
	TeamID      string `json:"t,omitempty"`
	TeamName    string `json:"n,omitempty"`
	SpaceID     string `json:"s,omitempty"`
	SpaceName   string `json:"p,omitempty"`
	ListID      string `json:"l,omitempty"`
	ListName    string `json:"b,omitempty"`
	TaskID      string `json:"i,omitempty"`
	Page        int    `json:"g,omitempty"`
	DraftID     string `json:"d,omitempty"`
	Name        string `json:"a,omitempty"`
	Description string `json:"e,omitempty"`
	Status      string `json:"y,omitempty"`
}

func readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || st.Page < 0 || len(st.TeamID) > 100 || len(st.TeamName) > 1000 || len(st.SpaceID) > 100 || len(st.SpaceName) > 1000 || len(st.ListID) > 100 || len(st.ListName) > 1000 || len(st.TaskID) > 100 || len(st.DraftID) > 128 || len(st.Name) > 600 || len(st.Description) > 31000 || len(st.Status) > 120 {
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
	case "team":
		if err := validID(r.Values["id"]); err != nil || r.Values["name"] == "" {
			return nil, errors.New("invalid workspace")
		}
		st = pageState{Mode: "tasks", TeamID: r.Values["id"], TeamName: r.Values["name"]}
	case "teams":
		st = pageState{}
	case "spaces":
		st = pageState{Mode: "spaces", TeamID: st.TeamID, TeamName: st.TeamName}
	case "lists":
		st = pageState{Mode: "lists", TeamID: st.TeamID, TeamName: st.TeamName, SpaceID: r.Values["id"], SpaceName: r.Values["name"]}
	case "create":
		if st.Mode != "review-create" {
			st = pageState{Mode: "create", TeamID: st.TeamID, TeamName: st.TeamName, SpaceID: st.SpaceID, SpaceName: st.SpaceName, ListID: r.Values["id"], ListName: r.Values["name"], DraftID: randomID()}
		} else {
			st.Mode = "create"
		}
	case "more":
		st.Page++
	case "detail":
		if err := validID(r.Values["id"]); err != nil {
			return nil, err
		}
		st.Mode, st.TaskID = "detail", r.Values["id"]
	case "edit":
		st.Mode = "edit"
		if st.DraftID == "" {
			st.DraftID = randomID()
		}
	case "back":
		st.Mode, st.TaskID, st.DraftID = "tasks", "", ""
		st.Name, st.Description, st.Status = "", "", ""
	case "review-create":
		in := createArgs{ListID: st.ListID, Name: r.Values["name"], Description: r.Values["description"]}
		if err := in.validate(); err != nil {
			return nil, err
		}
		st.Name, st.Description, st.Mode = in.Name, in.Description, "review-create"
	case "review-edit":
		in := statusArgs{TaskID: st.TaskID, Status: r.Values["status"]}
		if err := in.validate(); err != nil {
			return nil, err
		}
		st.Status, st.Mode = in.Status, "review-edit"
	case "confirm-create":
		raw, _ := json.Marshal(createArgs{ListID: st.ListID, Name: st.Name, Description: st.Description})
		out, err := a.doAction(ctx, "ui-create:"+st.DraftID, "clickup_create_task", raw)
		if err != nil {
			return nil, err
		}
		return &wire.ActionResult{Toast: out["text"].(string), Pop: true}, nil
	case "confirm-edit":
		raw, _ := json.Marshal(statusArgs{TaskID: st.TaskID, Status: st.Status})
		out, err := a.doAction(ctx, "ui-edit:"+st.DraftID, "clickup_update_status", raw)
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
		return a.editorView(r, st), nil
	}
	if st.Mode == "detail" {
		item, err := a.client.task(ctx, st.TaskID)
		a.record(err)
		if err != nil {
			return nil, err
		}
		st.DraftID = randomID()
		return detailView(r, st, item), nil
	}
	if st.Mode == "spaces" {
		items, err := a.client.spaces(ctx, st.TeamID)
		a.record(err)
		if err != nil {
			return nil, err
		}
		section := wire.Section{Title: t.spaces}
		for _, item := range items {
			if item.ID == "" || item.Name == "" {
				continue
			}
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "space-" + item.ID, Title: clipText(item.Name, 1800), Subtitle: item.ID, Action: &wire.Action{ID: "lists", Values: map[string]string{"id": item.ID, "name": item.Name}}})
		}
		if len(section.Items) == 0 {
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.spaces})
		}
		return &wire.View{Title: t.spaces, State: ext.EncodeState(st), Sections: []wire.Section{section, {Items: []wire.Component{{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "teams"}}}}}}, nil
	}
	if st.Mode == "lists" {
		items, err := a.client.lists(ctx, st.SpaceID)
		a.record(err)
		if err != nil {
			return nil, err
		}
		section := wire.Section{Title: t.lists}
		for _, item := range items {
			if item.ID == "" || item.Name == "" || item.Archived {
				continue
			}
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "list-" + item.ID, Title: clipText(item.Name, 1800), Subtitle: item.ID, Action: &wire.Action{ID: "create", Values: map[string]string{"id": item.ID, "name": item.Name}}})
		}
		if len(section.Items) == 0 {
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.lists})
		}
		return &wire.View{Title: t.lists, State: ext.EncodeState(st), Sections: []wire.Section{section, {Items: []wire.Component{{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "spaces"}}}}}}, nil
	}
	if st.Mode == "tasks" {
		me, err := a.client.user(ctx)
		if err != nil {
			a.record(err)
			return nil, err
		}
		page, err := a.client.tasks(ctx, st.TeamID, me.ID, st.Page)
		a.record(err)
		if err != nil {
			return nil, err
		}
		section := wire.Section{Title: t.tasks}
		for _, item := range page.Tasks {
			if item.ID == "" || item.Name == "" {
				continue
			}
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "task-" + item.ID, Title: clipText(item.Name, 1800), Subtitle: clipText(item.Status.Status+" · "+item.List.Name+" · "+item.DueDate, 1800), Action: &wire.Action{ID: "detail", Values: map[string]string{"id": item.ID}}})
		}
		if len(section.Items) == 0 {
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noTasks})
		}
		if !page.Last && len(page.Tasks) == 100 {
			section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more"}}
		}
		v := &wire.View{Title: clipText(st.TeamName, 1800), State: ext.EncodeState(st), Sections: []wire.Section{section, {Title: t.tasks, Items: []wire.Component{
			{Type: wire.ComponentButton, Title: t.create, Action: &wire.Action{ID: "spaces", Push: &wire.Push{Title: t.spaces, State: ext.EncodeState(pageState{Mode: "spaces", TeamID: st.TeamID, TeamName: st.TeamName})}}},
			{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "teams"}},
		}}}}
		return v, nil
	}
	if st.Mode != "" {
		return nil, errors.New("invalid task view")
	}
	teams, err := a.client.teams(ctx)
	a.record(err)
	if err != nil {
		return nil, err
	}
	section := wire.Section{Title: t.teams}
	for _, item := range teams {
		if item.ID == "" || item.Name == "" {
			continue
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "team-" + item.ID, Title: clipText(item.Name, 1800), Subtitle: item.ID, Action: &wire.Action{ID: "team", Values: map[string]string{"id": item.ID, "name": item.Name}}})
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.teams})
	}
	return &wire.View{Title: t.tasks, State: ext.EncodeState(st), Sections: []wire.Section{section}}, nil
}

func (a *app) editorView(r *ext.UIRequest, st pageState) *wire.View {
	t := textsFor(r.Locale)
	if st.Mode == "review-create" || st.Mode == "review-edit" {
		action, back := "confirm-create", "create"
		if st.Mode == "review-edit" {
			action, back = "confirm-edit", "edit"
		}
		items := []wire.Component{{Type: wire.ComponentText, Text: clipText(st.Name+st.Status, 1800)}}
		if st.Description != "" {
			items = append(items, textComponents(st.Description)...)
		}
		items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.review, Action: &wire.Action{ID: action, Confirm: t.review + "\n" + clipText(st.Name+st.Status, 500)}}, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: back}})
		return &wire.View{Title: t.review, State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.review, Items: items}}}
	}
	var items []wire.Component
	action := "review-create"
	if st.Mode == "edit" {
		action = "review-edit"
		items = []wire.Component{{Type: wire.ComponentField, Kind: "text", Name: "status", Label: t.status, Value: st.Status}}
	} else {
		items = []wire.Component{{Type: wire.ComponentField, Kind: "text", Name: "name", Label: t.name, Value: st.Name}, {Type: wire.ComponentField, Kind: "text", Name: "description", Label: t.description, Value: st.Description}}
	}
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.review, Action: &wire.Action{ID: action, Submit: true}}, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}})
	return &wire.View{Title: t.review, State: ext.EncodeState(st), Sections: []wire.Section{{Items: items}}}
}

func detailView(r *ext.UIRequest, st pageState, item task) *wire.View {
	t := textsFor(r.Locale)
	items := []wire.Component{{Type: wire.ComponentText, Text: clipText(t.status+": "+item.Status.Status+" · "+t.list+": "+item.List.Name, 1800)}}
	if link := safeTaskLink(item.URL); link != "" {
		items = append(items, wire.Component{Type: wire.ComponentLink, Title: t.open, URL: link})
	}
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.update, Action: &wire.Action{ID: "edit", Push: &wire.Push{Title: t.update, State: ext.EncodeState(pageState{Mode: "edit", TaskID: item.ID, Status: item.Status.Status, DraftID: randomID()})}}}, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}})
	v := &wire.View{Title: clipText(item.Name, 1800), State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.tasks, Items: items}}}
	if item.Description != "" {
		v.Sections = append(v.Sections, wire.Section{Title: t.description, Items: textComponents(clipText(item.Description, 60000))})
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
