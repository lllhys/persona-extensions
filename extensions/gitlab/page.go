package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

type pageState struct {
	Scope   string `json:"s,omitempty"`
	Query   string `json:"q,omitempty"`
	Page    int    `json:"p,omitempty"`
	Project string `json:"r,omitempty"`
	IID     int64  `json:"i,omitempty"`
	Mode    string `json:"m,omitempty"`
	DraftID string `json:"d,omitempty"`
	Body    string `json:"b,omitempty"`
}

func readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || st.Page < 0 || st.IID < 0 || len(st.Project) > 255 || len(st.DraftID) > 128 || len([]rune(st.Body)) > 10000 {
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
		st.Query, st.Page, st.Mode = clipText(r.Values["q"], 200), 0, "list"
	case "scope":
		if r.Values["view"] != "assigned_to_me" && r.Values["view"] != "reviews_for_me" {
			return nil, errors.New("invalid review scope")
		}
		st.Scope, st.Page, st.Mode = r.Values["view"], 0, "list"
	case "more":
		st.Page++
	case "detail":
		project, err := url.QueryUnescape(r.Values["project"])
		if err != nil || validProjectID(project) != nil {
			return nil, errors.New("invalid project ID")
		}
		iid, err := strconv.ParseInt(r.Values["iid"], 10, 64)
		if err != nil || iid <= 0 {
			return nil, errors.New("invalid merge request IID")
		}
		st.Project, st.IID, st.Mode = project, iid, "detail"
	case "back":
		st.Mode, st.IID, st.Project = "list", 0, ""
	case "comment":
		st.Mode, st.DraftID, st.Body = "comment", randomID(), ""
	case "review-comment":
		st.Body = r.Values["body"]
		if err := validComment(st.Body); err != nil {
			return nil, err
		}
		st.Mode = "review-comment"
	case "edit-comment":
		st.Mode = "comment"
	case "approve", "unapprove":
		if st.IID <= 0 || validProjectID(st.Project) != nil {
			return nil, errors.New("project and merge request IID are required")
		}
		raw, _ := json.Marshal(reviewArgs{Project: st.Project, IID: st.IID})
		out, err := a.doAction(ctx, "ui-"+r.Action+":"+st.DraftID, "gitlab_"+r.Action, raw)
		if err != nil {
			return nil, err
		}
		return &wire.ActionResult{Toast: out["text"].(string), Pop: true}, nil
	case "confirm-comment":
		raw, _ := json.Marshal(commentArgs{Project: st.Project, IID: st.IID, Body: st.Body})
		out, err := a.doAction(ctx, "ui-comment:"+st.DraftID, "gitlab_comment", raw)
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
	switch st.Mode {
	case "detail", "comment", "review-comment":
		if st.IID <= 0 || validProjectID(st.Project) != nil {
			return nil, errors.New("project and merge request IID are required")
		}
		if st.Mode == "comment" {
			return commentView(r, st), nil
		}
		if st.Mode == "review-comment" {
			if err := validComment(st.Body); err != nil {
				return nil, err
			}
			return reviewCommentView(r, st), nil
		}
		item, notes, next, err := a.readDetail(ctx, st.Project, st.IID)
		if err != nil {
			return nil, err
		}
		st.DraftID = randomID()
		return detailView(r, st, item, notes, next), nil
	}
	if st.Scope != "assigned_to_me" && st.Scope != "reviews_for_me" {
		return nil, errors.New("invalid review scope")
	}
	if len([]rune(st.Query)) > 200 || strings.ContainsRune(st.Query, 0) {
		return nil, errors.New("invalid merge request search")
	}
	if st.Page < 1 {
		st.Page = 1
	}
	page, err := a.client.mergeRequests(ctx, st.Scope, st.Query, st.Page)
	a.record(err)
	if err != nil {
		return nil, err
	}
	v := &wire.View{Title: t.reviews, State: ext.EncodeState(st), Search: &wire.Search{Placeholder: t.search, Value: st.Query, Action: wire.Action{ID: "search"}}, Filter: &wire.Filter{
		Name: "view", Title: t.open, Value: st.Scope, Action: wire.Action{ID: "scope"},
		Choices: []wire.Choice{{Value: "assigned_to_me", Title: t.assigned}, {Value: "reviews_for_me", Title: t.review}},
	}}
	section := wire.Section{Title: fmt.Sprintf(t.results, len(page.Items))}
	for _, item := range page.Items {
		if item.ProjectID <= 0 || item.IID <= 0 {
			continue
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "mr-" + itoa(item.ID), Title: fmt.Sprintf("!%d %s", item.IID, clipText(item.Title, 1800)), Subtitle: clipText(item.Author.Name+" · "+item.References.Full+" · "+item.SourceBranch, 1800), Action: &wire.Action{ID: "detail", Values: map[string]string{"iid": itoa(item.IID), "project": url.QueryEscape(itoa(item.ProjectID))}}})
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noMRs})
	}
	if page.Next != "" {
		section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more"}}
	}
	v.Sections = []wire.Section{section}
	return v, nil
}

func detailView(r *ext.UIRequest, st pageState, item mergeRequest, notes []note, next ...string) *wire.View {
	t := textsFor(r.Locale)
	items := []wire.Component{
		{Type: wire.ComponentText, Text: clipText(item.Title, 1800)},
		{Type: wire.ComponentText, Text: t.author + ": " + clipText(item.Author.Name+" · "+item.SourceBranch+" → "+item.TargetBranch, 1800)},
	}
	if safeHTTPSLink(item.WebURL) {
		items = append(items, wire.Component{Type: wire.ComponentLink, Title: t.openMR, URL: item.WebURL})
	}
	items = append(items,
		wire.Component{Type: wire.ComponentButton, Title: t.approve, Action: &wire.Action{ID: "approve", Confirm: t.approve + "\n" + item.Title}},
		wire.Component{Type: wire.ComponentButton, Title: t.unapprove, Action: &wire.Action{ID: "unapprove", Confirm: t.unapprove + "\n" + item.Title}},
		wire.Component{Type: wire.ComponentButton, Title: t.comment, Action: &wire.Action{ID: "comment", Push: &wire.Push{Title: t.comment, State: ext.EncodeState(pageState{Project: st.Project, IID: st.IID, Mode: "comment", DraftID: randomID()})}}},
		wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
	)
	v := &wire.View{Title: fmt.Sprintf("MR !%d", item.IID), State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.reviews, Items: items}}}
	if item.Description != "" {
		v.Sections = append(v.Sections, wire.Section{Title: t.description, Items: textComponents(clipText(item.Description, 60000))})
	}
	budget := 250 - viewItemCount(v)
	if len(item.Reviewers) > 0 {
		section := wire.Section{Title: t.reviewers}
		for _, reviewer := range limitUsers(item.Reviewers, 100) {
			name := strings.TrimSpace(reviewer.Name)
			if name == "" {
				name = reviewer.Username
			}
			if name == "" {
				continue
			}
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Title: clipText(name, 1800), Subtitle: "@" + reviewer.Username})
		}
		if len(section.Items) > 0 {
			v.Sections = append(v.Sections, section)
		}
	}
	section := wire.Section{Title: t.comments}
	for _, item := range notes {
		if item.System || budget < 2 {
			continue
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentText, Text: clipText(authorLabel(item.Author), 1800), Style: wire.TextStyleSecondary})
		budget--
		content := textComponents(clipText(item.Body, 12000))
		if len(content) > budget {
			content = content[:budget]
			content[len(content)-1].Text = clipText(content[len(content)-1].Text, 1700) + " […]"
		}
		section.Items = append(section.Items, content...)
		budget -= len(content)
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentText, Text: t.noComments, Style: wire.TextStyleSecondary})
	}
	if len(next) > 0 && next[0] != "" {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentText,
			Text: "Discussion is incomplete; next notes page: " + next[0], Style: wire.TextStyleSecondary})
	}
	v.Sections = append(v.Sections, section)
	return v
}

func authorLabel(user user) string {
	name := strings.TrimSpace(user.Name)
	if name == "" {
		name = user.Username
	}
	if name == "" {
		name = "GitLab user"
	}
	if user.Username != "" {
		name += " (@" + user.Username + ")"
	}
	return name
}

func safeHTTPSLink(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
}

func limitUsers(users []user, limit int) []user {
	if len(users) > limit {
		return users[:limit]
	}
	return users
}

func viewItemCount(v *wire.View) int {
	n := 0
	for _, section := range v.Sections {
		n += len(section.Items)
	}
	return n
}

func commentView(r *ext.UIRequest, st pageState) *wire.View {
	t := textsFor(r.Locale)
	return &wire.View{Title: t.comment, State: ext.EncodeState(st), Sections: []wire.Section{{Items: []wire.Component{
		{Type: wire.ComponentField, Kind: "text", Name: "body", Label: t.body, Value: st.Body},
		{Type: wire.ComponentButton, Title: t.reviewComment, Action: &wire.Action{ID: "review-comment", Submit: true}},
		{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
	}}}}
}

func reviewCommentView(r *ext.UIRequest, st pageState) *wire.View {
	t := textsFor(r.Locale)
	items := textComponents(st.Body)
	items = append(items,
		wire.Component{Type: wire.ComponentButton, Title: t.confirmComment, Action: &wire.Action{ID: "confirm-comment", Confirm: t.confirmComment + "\n" + fmt.Sprintf("MR !%d", st.IID)}},
		wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "edit-comment"}},
	)
	return &wire.View{Title: t.reviewComment, State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.reviewComment, Items: items}}}
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
