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
	Query    string     `json:"q,omitempty"`
	Token    string     `json:"t,omitempty"`
	IssueKey string     `json:"k,omitempty"`
	Mode     string     `json:"m,omitempty"`
	DraftID  string     `json:"d,omitempty"`
	Create   createArgs `json:"c,omitempty"`
}

func readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || len(st.Token) > maxPageToken || len(st.IssueKey) > 64 || len(st.DraftID) > 128 {
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
		st.Query, st.Token, st.Mode = r.Values["q"], "", "list"
	case "more":
		token := r.Values["token"]
		if token == "" || len(token) > maxPageToken {
			return nil, errors.New("invalid page token")
		}
		st.Token = token
	case "detail":
		if err := validKey(r.Values["id"]); err != nil {
			return nil, err
		}
		st.IssueKey, st.Mode = r.Values["id"], "detail"
	case "back":
		st.Mode, st.IssueKey = "list", ""
	case "create":
		st.Mode, st.DraftID = "create", randomID()
	case "review-create":
		in := createArgs{ProjectID: r.Values["project_id"], IssueTypeID: r.Values["issuetype_id"], Summary: r.Values["summary"], Description: r.Values["description"]}
		if err := in.validate(); err != nil {
			return nil, err
		}
		st.Create, st.Mode = in, "review"
	case "confirm-create":
		if st.DraftID == "" {
			return nil, errors.New("the create form expired")
		}
		raw, _ := json.Marshal(st.Create)
		out, err := a.doAction(ctx, "ui-create:"+st.DraftID, "jira_create", raw)
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
	if a.client.base == "" {
		return &wire.View{Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: t.missingConfig}}}}}, nil
	}
	if st.Mode == "create" {
		return a.createView(ctx, r, st)
	}
	if st.Mode == "review" {
		return a.reviewCreateView(ctx, r, st)
	}
	if st.Mode == "detail" {
		item, err := a.client.issue(ctx, st.IssueKey)
		a.record(err)
		if err != nil {
			return nil, err
		}
		return detailView(r, st, item), nil
	}
	page, err := a.client.search(ctx, st.Token)
	a.record(err)
	if err != nil {
		return nil, err
	}
	v := &wire.View{Title: t.issues, State: ext.EncodeState(st), Search: &wire.Search{Placeholder: t.search, Value: st.Query, Action: wire.Action{ID: "search"}}}
	section := wire.Section{Title: t.recent, Items: []wire.Component{{
		Type: wire.ComponentButton, Title: t.create, Action: &wire.Action{ID: "create", Push: &wire.Push{State: ext.EncodeState(pageState{Mode: "create", DraftID: randomID()}), Title: t.create}},
	}}}
	for _, item := range page.Issues {
		if st.Query != "" && !strings.Contains(strings.ToLower(item.Fields.Summary+" "+item.Key), strings.ToLower(st.Query)) {
			continue
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "issue-" + item.Key, Title: item.Key + " " + clipText(item.Fields.Summary, 1800), Subtitle: item.Fields.Project.Key + " · " + item.Fields.Status.Name, Action: &wire.Action{ID: "detail", Values: map[string]string{"id": item.Key}}})
	}
	if len(section.Items) == 1 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noIssues})
	}
	if next := page.next(); next != "" {
		section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more", Values: map[string]string{"token": next}}}
	}
	v.Sections = []wire.Section{section}
	return v, nil
}

func detailView(r *ext.UIRequest, st pageState, item issue) *wire.View {
	t := textsFor(r.Locale)
	items := []wire.Component{
		{Type: wire.ComponentText, Text: item.Fields.Summary},
		{Type: wire.ComponentText, Text: t.project + ": " + item.Fields.Project.Name + " · " + t.status + ": " + item.Fields.Status.Name},
	}
	if item.Fields.Assignee != nil {
		items = append(items, wire.Component{Type: wire.ComponentText, Text: t.assignee + ": " + item.Fields.Assignee.DisplayName})
	}
	if item.Self != "" {
		items = append(items, wire.Component{Type: wire.ComponentLink, Title: t.open, URL: item.Self})
	}
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}})
	v := &wire.View{Title: item.Key, State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.issues, Items: items}}}
	if text := richText(item.Fields.Description); text != "" {
		v.Sections = append(v.Sections, wire.Section{Title: t.description, Items: textComponents(text)})
	}
	if len(item.Fields.Comment.Comments) > 0 {
		section := wire.Section{Title: t.comments}
		for _, c := range item.Fields.Comment.Comments {
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentText, Text: c.Author.DisplayName + " · " + c.Created, Style: wire.TextStyleSecondary})
			section.Items = append(section.Items, textComponents(richText(c.Body))...)
		}
		v.Sections = append(v.Sections, section)
	}
	return v
}

func (a *app) createView(ctx context.Context, r *ext.UIRequest, st pageState) (*wire.View, error) {
	t := textsFor(r.Locale)
	projects, err := a.client.projects(ctx)
	if err != nil {
		return nil, err
	}
	choices := []wire.Choice{}
	for _, project := range projects {
		choices = append(choices, wire.Choice{Value: fmt.Sprint(project["id"]), Title: fmt.Sprint(project["name"])})
	}
	if len(choices) == 0 {
		return nil, errors.New("no accessible Jira project")
	}
	items := []wire.Component{
		{Type: wire.ComponentField, Kind: "choice", Name: "project_id", Label: t.project, Value: choices[0].Value, Choices: choices},
		{Type: wire.ComponentField, Kind: "text", Name: "issuetype_id", Label: "Issue type ID"},
		{Type: wire.ComponentField, Kind: "text", Name: "summary", Label: t.issues},
		{Type: wire.ComponentField, Kind: "text", Name: "description", Label: t.description},
		{Type: wire.ComponentButton, Title: t.review, Action: &wire.Action{ID: "review-create", Submit: true}},
	}
	return &wire.View{Title: t.create, State: ext.EncodeState(st), Sections: []wire.Section{{Items: items}}}, nil
}

func (a *app) reviewCreateView(ctx context.Context, r *ext.UIRequest, st pageState) (*wire.View, error) {
	t := textsFor(r.Locale)
	items := []wire.Component{{Type: wire.ComponentText, Text: st.Create.Summary}, {Type: wire.ComponentText, Text: t.project + ": " + st.Create.ProjectID + " · " + st.Create.IssueTypeID}}
	items = append(items, textComponents(st.Create.Description)...)
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.confirm, Action: &wire.Action{ID: "confirm-create", Confirm: t.create + "\n" + st.Create.Summary}})
	return &wire.View{Title: t.review, State: ext.EncodeState(st), Sections: []wire.Section{{Items: items}}}, nil
}

func textComponents(text string) []wire.Component {
	var out []wire.Component
	for _, line := range strings.Split(text, "\n") {
		r := []rune(line)
		for len(r) > 0 {
			n := min(len(r), 2000)
			out = append(out, wire.Component{Type: wire.ComponentText, Text: string(r[:n])})
			r = r[n:]
		}
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
