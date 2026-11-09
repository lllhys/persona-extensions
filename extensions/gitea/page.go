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
	Owner   string `json:"o,omitempty"`
	Repo    string `json:"r,omitempty"`
	Page    int    `json:"p,omitempty"`
	PRID    int64  `json:"i,omitempty"`
	Mode    string `json:"m,omitempty"`
	DraftID string `json:"d,omitempty"`
	Body    string `json:"b,omitempty"`
}

func readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || st.Page < 0 || st.PRID < 0 || len(st.Owner) > 128 || len(st.Repo) > 128 || len(st.DraftID) > 128 || len(st.Body) > 11000 {
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
	case "scope":
		if err := validRepo(r.Values["owner"], r.Values["repo"]); err != nil {
			return nil, err
		}
		st.Owner, st.Repo, st.Page, st.Mode = r.Values["owner"], r.Values["repo"], 1, "list"
	case "more":
		st.Page++
	case "detail":
		id, err := strconv.ParseInt(r.Values["id"], 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("invalid PR number")
		}
		st.PRID, st.Mode = id, "detail"
	case "back":
		st.Mode, st.PRID = "list", 0
	case "approve":
		if st.PRID <= 0 || st.DraftID == "" {
			return nil, errors.New("PR and draft are required")
		}
		raw, _ := json.Marshal(reviewArgs{Owner: st.Owner, Repo: st.Repo, Number: st.PRID})
		out, err := a.doAction(ctx, "ui-approve:"+st.DraftID, "gitea_approve", raw)
		if err != nil {
			return nil, err
		}
		return &wire.ActionResult{Toast: out["text"].(string), Pop: true}, nil
	case "comment":
		st.Mode, st.DraftID, st.Body = "comment", randomID(), ""
	case "review-comment":
		if err := validComment(r.Values["body"]); err != nil {
			return nil, err
		}
		st.Body, st.Mode = r.Values["body"], "review-comment"
	case "confirm-comment":
		raw, _ := json.Marshal(reviewArgs{Owner: st.Owner, Repo: st.Repo, Number: st.PRID, Body: st.Body})
		out, err := a.doAction(ctx, "ui-comment:"+st.DraftID, "gitea_comment", raw)
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
		if st.PRID <= 0 || validRepo(st.Owner, st.Repo) != nil {
			return nil, errors.New("repository and PR number are required")
		}
		if st.Mode == "comment" {
			return commentView(r, st), nil
		}
		if st.Mode == "review-comment" {
			return reviewCommentView(r, st), nil
		}
		item, err := a.client.pullRequest(ctx, st.Owner, st.Repo, st.PRID)
		a.record(err)
		if err != nil {
			return nil, err
		}
		comments, err := a.client.comments(ctx, st.Owner, st.Repo, st.PRID)
		if err != nil {
			return nil, err
		}
		st.DraftID = randomID()
		return detailView(r, st, item, comments), nil
	}
	if st.Mode != "" && st.Mode != "list" {
		return nil, errors.New("invalid review view")
	}
	if st.Page < 1 {
		st.Page = 1
	}
	items, err := a.client.pullRequests(ctx, st.Owner, st.Repo, st.Page)
	a.record(err)
	if err != nil {
		return nil, err
	}
	v := &wire.View{Title: t.reviews, State: ext.EncodeState(st), Search: &wire.Search{Placeholder: "owner/repo", Value: st.Owner + "/" + st.Repo, Action: wire.Action{ID: "scope"}}}
	section := wire.Section{Title: t.open}
	for _, item := range items {
		if item.Number <= 0 || item.Title == "" {
			continue
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "pr-" + strconv.FormatInt(item.Number, 10), Title: fmt.Sprintf("#%d %s", item.Number, clipText(item.Title, 1800)), Subtitle: clipText(item.User.FullName+" · "+item.Head.Ref, 1800), Action: &wire.Action{ID: "detail", Values: map[string]string{"id": strconv.FormatInt(item.Number, 10)}}})
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noPRs})
	}
	if len(items) == 50 {
		section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more"}}
	}
	v.Sections = []wire.Section{section}
	return v, nil
}

func detailView(r *ext.UIRequest, st pageState, item pullRequest, comments []comment) *wire.View {
	t := textsFor(r.Locale)
	items := []wire.Component{
		{Type: wire.ComponentText, Text: clipText(item.Title, 1800)},
		{Type: wire.ComponentText, Text: clipText(t.author+": "+item.User.FullName+" · "+item.Head.Ref+" → "+item.Base.Ref, 1800)},
	}
	if link := safePRLink(item.HTMLURL); link != "" {
		items = append(items, wire.Component{Type: wire.ComponentLink, Title: t.openPR, URL: link})
	}
	items = append(items,
		wire.Component{Type: wire.ComponentButton, Title: t.approve, Action: &wire.Action{ID: "approve", Confirm: t.approve + "\n" + item.Title}},
		wire.Component{Type: wire.ComponentButton, Title: t.comment, Action: &wire.Action{ID: "comment", Push: &wire.Push{Title: t.comment, State: ext.EncodeState(pageState{Owner: st.Owner, Repo: st.Repo, PRID: st.PRID, Mode: "comment", DraftID: randomID()})}}},
		wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
	)
	v := &wire.View{Title: fmt.Sprintf("PR #%d", item.Number), State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.reviews, Items: items}}}
	if item.Body != "" {
		v.Sections = append(v.Sections, wire.Section{Title: t.description, Items: textComponents(clipText(item.Body, 60000))})
	}
	section := wire.Section{Title: t.comments}
	for _, comment := range comments {
		if comment.Body == "" {
			continue
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentText, Text: clipText(comment.User.FullName+" (@"+comment.User.Login+")", 1800), Style: wire.TextStyleSecondary})
		section.Items = append(section.Items, textComponents(clipText(comment.Body, 12000))...)
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentText, Text: t.noComments, Style: wire.TextStyleSecondary})
	}
	v.Sections = append(v.Sections, section)
	return v
}

func commentView(r *ext.UIRequest, st pageState) *wire.View {
	t := textsFor(r.Locale)
	return &wire.View{Title: t.comment, State: ext.EncodeState(st), Sections: []wire.Section{{Items: []wire.Component{
		{Type: wire.ComponentField, Kind: "text", Name: "body", Label: t.body, Value: st.Body},
		{Type: wire.ComponentButton, Title: t.review, Action: &wire.Action{ID: "review-comment", Submit: true}},
		{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}},
	}}}}
}

func reviewCommentView(r *ext.UIRequest, st pageState) *wire.View {
	t := textsFor(r.Locale)
	items := textComponents(st.Body)
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.save, Action: &wire.Action{ID: "confirm-comment", Confirm: t.save + "\n" + fmt.Sprintf("PR #%d", st.PRID)}}, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "comment"}})
	return &wire.View{Title: t.review, State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.review, Items: items}}}
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
