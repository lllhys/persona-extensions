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
	Mode            string `json:"m,omitempty"`
	StoryID         int64  `json:"i,omitempty"`
	Next            string `json:"n,omitempty"`
	DraftID         string `json:"d,omitempty"`
	Name            string `json:"a,omitempty"`
	Description     string `json:"b,omitempty"`
	WorkflowStateID int64  `json:"s,omitempty"`
	StoryType       string `json:"t,omitempty"`
	Deadline        string `json:"e,omitempty"`
}

func readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || st.StoryID < 0 || st.WorkflowStateID < 0 || len(st.DraftID) > 128 || len(st.Name) > 600 || len(st.Description) > 11000 || len(st.Next) > 2048 {
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
	case "more":
		if err := validNext(r.Values["next"]); err != nil || r.Values["next"] == "" {
			return nil, errors.New("invalid next token")
		}
		st.Next = r.Values["next"]
	case "detail":
		id, err := strconv.ParseInt(r.Values["id"], 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("invalid story ID")
		}
		st.Mode, st.StoryID = "detail", id
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
		st.Mode, st.StoryID, st.DraftID = "", 0, ""
		st.Name, st.Description, st.StoryType, st.Deadline = "", "", "", ""
		st.WorkflowStateID = 0
	case "review-create":
		in := createArgs{Name: r.Values["name"], Description: r.Values["description"], StoryType: r.Values["story_type"], Deadline: r.Values["deadline"]}
		in.WorkflowStateID, err = strconv.ParseInt(r.Values["state_id"], 10, 64)
		if err != nil {
			return nil, errors.New("invalid workflow state")
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		st.Name, st.Description, st.WorkflowStateID, st.StoryType, st.Deadline = in.Name, in.Description, in.WorkflowStateID, in.StoryType, in.Deadline
		st.Mode = "review-create"
	case "review-edit":
		st.WorkflowStateID, err = strconv.ParseInt(r.Values["state_id"], 10, 64)
		if err != nil || st.WorkflowStateID <= 0 {
			return nil, errors.New("invalid workflow state")
		}
		st.Mode = "review-edit"
	case "confirm-create":
		in := createArgs{Name: st.Name, Description: st.Description, WorkflowStateID: st.WorkflowStateID, StoryType: st.StoryType, Deadline: st.Deadline}
		raw, _ := json.Marshal(in)
		out, err := a.doAction(ctx, "ui-create:"+st.DraftID, "shortcut_create_story", raw)
		if err != nil {
			return nil, err
		}
		return &wire.ActionResult{Toast: out["text"].(string), Pop: true}, nil
	case "confirm-edit":
		in := updateArgs{StoryID: st.StoryID, WorkflowStateID: st.WorkflowStateID}
		raw, _ := json.Marshal(in)
		out, err := a.doAction(ctx, "ui-edit:"+st.DraftID, "shortcut_update_story_state", raw)
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
	switch st.Mode {
	case "", "detail", "create", "review-create", "edit", "review-edit":
	default:
		return nil, errors.New("invalid story view")
	}
	if st.Mode == "create" || st.Mode == "review-create" {
		return a.createView(ctx, r, st)
	}
	if st.Mode == "" {
		me, err := a.client.member(ctx)
		if err != nil {
			a.record(err)
			return nil, err
		}
		page, err := a.client.stories(ctx, me.MentionName, st.Next)
		a.record(err)
		if err != nil {
			return nil, err
		}
		section := wire.Section{Title: t.stories}
		for _, item := range page.Data {
			if item.ID <= 0 || item.Name == "" {
				continue
			}
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "story-" + fmt.Sprint(item.ID), Title: clipText(item.Name, 1800), Subtitle: fmt.Sprintf("#%d · %s · %s", item.ID, item.StoryType, item.Deadline), Action: &wire.Action{ID: "detail", Values: map[string]string{"id": fmt.Sprint(item.ID)}}})
		}
		if len(section.Items) == 0 {
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noStories})
		}
		if validNext(page.Next) == nil && page.Next != "" {
			section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more", Values: map[string]string{"next": page.Next}}}
		}
		v := &wire.View{Title: t.stories, State: ext.EncodeState(st), Sections: []wire.Section{section, {Title: t.stories, Items: []wire.Component{
			{Type: wire.ComponentButton, Title: t.create, Action: &wire.Action{ID: "create", Push: &wire.Push{Title: t.create, State: ext.EncodeState(pageState{Mode: "create", DraftID: randomID()})}}},
		}}}}
		return v, nil
	}
	if st.StoryID <= 0 {
		return nil, errors.New("story ID is required")
	}
	item, err := a.client.story(ctx, st.StoryID)
	a.record(err)
	if err != nil {
		return nil, err
	}
	workflows, err := a.client.workflows(ctx)
	if err != nil {
		return nil, err
	}
	if st.Mode == "edit" || st.Mode == "review-edit" {
		if st.Mode == "edit" {
			st.WorkflowStateID = item.WorkflowStateID
		}
		return editView(r, st, item, workflows), nil
	}
	st.DraftID = randomID()
	return detailView(r, st, item, workflows), nil
}

func (a *app) createView(ctx context.Context, r *ext.UIRequest, st pageState) (*wire.View, error) {
	t := textsFor(r.Locale)
	if st.Mode == "review-create" {
		in := createArgs{Name: st.Name, Description: st.Description, WorkflowStateID: st.WorkflowStateID, StoryType: st.StoryType, Deadline: st.Deadline}
		if err := in.validate(); err != nil {
			return nil, err
		}
		items := []wire.Component{{Type: wire.ComponentText, Text: clipText(st.Name, 1800)}}
		if st.Description != "" {
			items = append(items, textComponents(st.Description)...)
		}
		items = append(items, wire.Component{Type: wire.ComponentText, Text: fmt.Sprintf("%s: %d · %s: %s", t.state, st.WorkflowStateID, t.deadline, st.Deadline)}, wire.Component{Type: wire.ComponentButton, Title: t.create, Action: &wire.Action{ID: "confirm-create", Confirm: t.create + "\n" + st.Name}}, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "create"}})
		return &wire.View{Title: t.review, State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.create, Items: items}}}, nil
	}
	workflows, err := a.client.workflows(ctx)
	if err != nil {
		return nil, err
	}
	choices := stateChoices(workflows, 0)
	if len(choices) == 0 {
		return nil, errors.New("no Shortcut workflow state is available")
	}
	items := []wire.Component{
		{Type: wire.ComponentField, Kind: "text", Name: "name", Label: t.name, Value: st.Name},
		{Type: wire.ComponentField, Kind: "text", Name: "description", Label: t.description, Value: st.Description},
		{Type: wire.ComponentField, Kind: "choice", Name: "state_id", Label: t.state, Value: selectedState(st.WorkflowStateID, choices), Choices: choices},
		{Type: wire.ComponentField, Kind: "choice", Name: "story_type", Label: t.typeLabel, Value: selectedType(st.StoryType), Choices: []wire.Choice{{Value: "feature", Title: t.feature}, {Value: "bug", Title: t.bug}, {Value: "chore", Title: t.chore}}},
		{Type: wire.ComponentField, Kind: "text", Name: "deadline", Label: t.deadline, Value: st.Deadline},
		{Type: wire.ComponentButton, Title: t.review, Action: &wire.Action{ID: "review-create", Submit: true}},
		{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
	}
	return &wire.View{Title: t.create, State: ext.EncodeState(st), Sections: []wire.Section{{Items: items}}}, nil
}

func editView(r *ext.UIRequest, st pageState, item story, workflows []workflow) *wire.View {
	t := textsFor(r.Locale)
	choices := stateChoices(workflows, item.WorkflowID)
	if len(choices) == 0 {
		return &wire.View{Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: t.state}}}}}
	}
	if st.Mode == "review-edit" {
		stateName := ""
		for _, choice := range choices {
			if choice.Value == fmt.Sprint(st.WorkflowStateID) {
				stateName = choice.Title
			}
		}
		return &wire.View{Title: t.review, State: ext.EncodeState(st), Sections: []wire.Section{{Items: []wire.Component{
			{Type: wire.ComponentText, Text: t.state + ": " + stateName},
			{Type: wire.ComponentButton, Title: t.save, Action: &wire.Action{ID: "confirm-edit", Confirm: t.save + "\n" + item.Name + "\n" + t.state + ": " + stateName}},
			{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "edit"}},
		}}}}
	}
	return &wire.View{Title: t.edit, State: ext.EncodeState(st), Sections: []wire.Section{{Items: []wire.Component{
		{Type: wire.ComponentField, Kind: "choice", Name: "state_id", Label: t.state, Value: selectedState(st.WorkflowStateID, choices), Choices: choices},
		{Type: wire.ComponentButton, Title: t.review, Action: &wire.Action{ID: "review-edit", Submit: true}},
		{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
	}}}}
}

func detailView(r *ext.UIRequest, st pageState, item story, workflows []workflow) *wire.View {
	t := textsFor(r.Locale)
	stateName := ""
	for _, workflow := range workflows {
		if workflow.ID == item.WorkflowID {
			for _, state := range workflow.States {
				if state.ID == item.WorkflowStateID {
					stateName = state.Name
				}
			}
		}
	}
	items := []wire.Component{{Type: wire.ComponentText, Text: clipText(t.state+": "+stateName+" · "+t.typeLabel+": "+item.StoryType+" · "+t.deadline+": "+item.Deadline, 1800)}}
	if link := safeStoryLink(item.AppURL); link != "" {
		items = append(items, wire.Component{Type: wire.ComponentLink, Title: t.open, URL: link})
	}
	items = append(items,
		wire.Component{Type: wire.ComponentButton, Title: t.edit, Action: &wire.Action{ID: "edit", Push: &wire.Push{Title: t.edit, State: ext.EncodeState(pageState{Mode: "edit", StoryID: st.StoryID, WorkflowStateID: item.WorkflowStateID, DraftID: randomID()})}}},
		wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
	)
	sections := []wire.Section{{Title: fmt.Sprintf("#%d", item.ID), Items: items}}
	if item.Description != "" {
		sections = append(sections, wire.Section{Title: t.description, Items: textComponents(clipText(item.Description, 60000))})
	}
	comments := wire.Section{Title: t.comments}
	for _, comment := range item.Comments {
		if comment.Deleted || comment.Text == "" {
			continue
		}
		comments.Items = append(comments.Items, textComponents(clipText(comment.Text, 12000))...)
	}
	if len(comments.Items) == 0 {
		comments.Items = append(comments.Items, wire.Component{Type: wire.ComponentText, Text: t.noComments, Style: wire.TextStyleSecondary})
	}
	sections = append(sections, comments)
	return &wire.View{Title: clipText(item.Name, 1800), State: ext.EncodeState(st), Sections: sections}
}

func stateChoices(workflows []workflow, workflowID int64) []wire.Choice {
	out := []wire.Choice{}
	for _, workflow := range workflows {
		if workflowID != 0 && workflow.ID != workflowID {
			continue
		}
		for _, state := range workflow.States {
			if state.ID > 0 && state.Name != "" && len(out) < 50 {
				out = append(out, wire.Choice{Value: fmt.Sprint(state.ID), Title: clipText(workflow.Name+" · "+state.Name, 100)})
			}
		}
	}
	return out
}

func selectedState(value int64, choices []wire.Choice) string {
	for _, choice := range choices {
		if choice.Value == fmt.Sprint(value) {
			return choice.Value
		}
	}
	return choices[0].Value
}

func selectedType(value string) string {
	if value == "feature" || value == "bug" || value == "chore" {
		return value
	}
	return "feature"
}

func textComponents(text string) []wire.Component {
	var out []wire.Component
	for _, line := range strings.Split(text, "\n") {
		r := []rune(line)
		for len(r) > 0 {
			n := min(len(r), 1800)
			out = append(out, wire.Component{Type: wire.ComponentText, Text: string(r[:n])})
			r = r[n:]
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
