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
	IssueID     int64  `json:"i,omitempty"`
	Offset      int    `json:"o,omitempty"`
	ProjectID   int64  `json:"p,omitempty"`
	ProjectName string `json:"n,omitempty"`
	DraftID     string `json:"d,omitempty"`
	Subject     string `json:"a,omitempty"`
	Description string `json:"b,omitempty"`
}

func readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || st.IssueID < 0 || st.Offset < 0 || st.ProjectID < 0 || len(st.ProjectName) > 1000 || len(st.DraftID) > 128 || len(st.Subject) > 600 || len(st.Description) > 31000 {
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
		st.Offset += 50
	case "detail":
		id, err := parseID(r.Values["id"])
		if err != nil {
			return nil, err
		}
		st.Mode, st.IssueID = "detail", id
	case "projects":
		st = pageState{Mode: "projects"}
	case "create":
		if st.Mode != "review-create" {
			st = pageState{Mode: "create", ProjectID: st.ProjectID, ProjectName: st.ProjectName, DraftID: randomID()}
		} else {
			st.Mode = "create"
		}
	case "back":
		st.Mode, st.IssueID, st.DraftID, st.Subject, st.Description = "", 0, "", "", ""
	case "review-create":
		in := createArgs{ProjectID: st.ProjectID, Subject: r.Values["subject"], Description: r.Values["description"]}
		if err := in.validate(); err != nil {
			return nil, err
		}
		st.Subject, st.Description, st.Mode = in.Subject, in.Description, "review-create"
	case "confirm-create":
		raw, _ := json.Marshal(createArgs{ProjectID: st.ProjectID, Subject: st.Subject, Description: st.Description})
		out, err := a.doAction(ctx, "ui-create:"+st.DraftID, "redmine_create_issue", raw)
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
		return a.createView(r, st), nil
	}
	if st.Mode == "projects" {
		page, err := a.client.projects(ctx, 0)
		a.record(err)
		if err != nil {
			return nil, err
		}
		section := wire.Section{Title: t.projects}
		for _, item := range page.Projects {
			if item.ID <= 0 || item.Name == "" {
				continue
			}
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "project-" + fmt.Sprint(item.ID), Title: clipText(item.Name, 1800), Subtitle: fmt.Sprint(item.ID), Action: &wire.Action{ID: "create", Push: &wire.Push{Title: t.create, State: ext.EncodeState(pageState{Mode: "create", ProjectID: item.ID, ProjectName: item.Name, DraftID: randomID()})}}})
		}
		if len(section.Items) == 0 {
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noProjects})
		}
		return &wire.View{Title: t.projects, State: ext.EncodeState(st), Sections: []wire.Section{section, {Items: []wire.Component{{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}}}}}}, nil
	}
	if st.Mode == "detail" {
		item, err := a.client.issue(ctx, st.IssueID)
		a.record(err)
		if err != nil {
			return nil, err
		}
		return detailView(r, st, item), nil
	}
	if st.Mode != "" {
		return nil, errors.New("invalid issue view")
	}
	page, err := a.client.issues(ctx, st.Offset)
	a.record(err)
	if err != nil {
		return nil, err
	}
	section := wire.Section{Title: t.issues}
	for _, item := range page.Issues {
		if item.ID <= 0 || item.Subject == "" {
			continue
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "issue-" + fmt.Sprint(item.ID), Title: clipText(item.Subject, 1800), Subtitle: clipText(item.Project.Name+" · "+item.Status.Name+" · "+item.Priority.Name, 1800), Action: &wire.Action{ID: "detail", Values: map[string]string{"id": fmt.Sprint(item.ID)}}})
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noIssues})
	}
	if page.Offset+len(page.Issues) < page.TotalCount {
		section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more"}}
	}
	v := &wire.View{Title: t.issues, State: ext.EncodeState(st), Sections: []wire.Section{section, {Title: t.issues, Items: []wire.Component{
		{Type: wire.ComponentButton, Title: t.create, Action: &wire.Action{ID: "projects", Push: &wire.Push{Title: t.projects, State: ext.EncodeState(pageState{Mode: "projects"})}}},
	}}}}
	return v, nil
}

func (a *app) createView(r *ext.UIRequest, st pageState) *wire.View {
	t := textsFor(r.Locale)
	if st.Mode == "review-create" {
		in := createArgs{ProjectID: st.ProjectID, Subject: st.Subject, Description: st.Description}
		if err := in.validate(); err != nil {
			return &wire.View{Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: err.Error()}}}}}
		}
		items := []wire.Component{{Type: wire.ComponentText, Text: clipText(st.Subject, 1800)}}
		if st.Description != "" {
			items = append(items, textComponents(st.Description)...)
		}
		items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.create, Action: &wire.Action{ID: "confirm-create", Confirm: t.create + "\n" + st.Subject}}, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "create"}})
		return &wire.View{Title: t.review, State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.create, Items: items}}}
	}
	return &wire.View{Title: t.create, State: ext.EncodeState(st), Sections: []wire.Section{{Title: st.ProjectName, Items: []wire.Component{
		{Type: wire.ComponentField, Kind: "text", Name: "subject", Label: t.subject, Value: st.Subject},
		{Type: wire.ComponentField, Kind: "text", Name: "description", Label: t.description, Value: st.Description},
		{Type: wire.ComponentButton, Title: t.review, Action: &wire.Action{ID: "review-create", Submit: true}},
		{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
	}}}}
}

func detailView(r *ext.UIRequest, st pageState, item issue) *wire.View {
	t := textsFor(r.Locale)
	items := []wire.Component{
		{Type: wire.ComponentText, Text: clipText(t.project+": "+item.Project.Name+" · "+item.Status.Name+" · "+item.Priority.Name, 1800)},
		{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
	}
	v := &wire.View{Title: clipText(item.Subject, 1800), State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.issues, Items: items}}}
	if item.Description != "" {
		v.Sections = append(v.Sections, wire.Section{Title: t.description, Items: textComponents(clipText(item.Description, 60000))})
	}
	section := wire.Section{Title: t.journals}
	for _, item := range item.Journals {
		if item.Notes == "" {
			continue
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentText, Text: clipText(item.User.Name+" · "+item.CreatedOn, 1800), Style: wire.TextStyleSecondary})
		section.Items = append(section.Items, textComponents(clipText(item.Notes, 12000))...)
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentText, Text: t.noJournals, Style: wire.TextStyleSecondary})
	}
	v.Sections = append(v.Sections, section)
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

func parseID(value string) (int64, error) {
	var id int64
	if _, err := fmt.Sscan(value, &id); err != nil || id <= 0 {
		return 0, errors.New("invalid issue ID")
	}
	return id, nil
}
