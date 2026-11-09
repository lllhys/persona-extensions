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
	View    string `json:"v,omitempty"`
	Query   string `json:"q,omitempty"`
	Offset  int    `json:"o,omitempty"`
	LinkID  string `json:"i,omitempty"`
	Mode    string `json:"m,omitempty"`
	DraftID string `json:"d,omitempty"`
}

func readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || st.Offset < 0 || len(st.LinkID) > 256 || len(st.DraftID) > 128 || len(st.Query) > 250 {
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
		st.Query, st.Offset = clipText(r.Values["q"], 200), 0
		if strings.TrimSpace(st.Query) != "" {
			st.View = "search"
		} else {
			st.View = "unread"
		}
	case "view":
		if r.Values["view"] != "unread" && r.Values["view"] != "starred" {
			return nil, errors.New("invalid reading view")
		}
		st.View, st.Query, st.Offset = r.Values["view"], "", 0
	case "more":
		st.Offset += 50
	case "detail":
		if err := validLinkID(r.Values["id"]); err != nil {
			return nil, err
		}
		st.Mode, st.LinkID = "detail", r.Values["id"]
	case "back":
		st.Mode, st.LinkID = "", ""
	case "read":
		if err := validLinkID(st.LinkID); err != nil || st.DraftID == "" {
			return nil, errors.New("link and draft are required")
		}
		raw, _ := json.Marshal(readArgs{LinkID: st.LinkID})
		out, err := a.doAction(ctx, "ui-read:"+st.DraftID, "goodlinks_mark_read", raw)
		if err != nil {
			return nil, err
		}
		return &wire.ActionResult{Toast: out["text"].(string), Pop: true}, nil
	case "star":
		if err := validLinkID(st.LinkID); err != nil || st.DraftID == "" {
			return nil, errors.New("link and draft are required")
		}
		starred, err := strconv.ParseBool(r.Values["starred"])
		if err != nil {
			return nil, errors.New("invalid starred flag")
		}
		raw, _ := json.Marshal(starArgs{LinkID: st.LinkID, Starred: starred})
		out, err := a.doAction(ctx, "ui-star:"+st.DraftID, "goodlinks_toggle_star", raw)
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
	if st.Mode == "detail" {
		if err := validLinkID(st.LinkID); err != nil {
			return nil, err
		}
		item, err := a.client.link(ctx, st.LinkID)
		a.record(err)
		if err != nil {
			return nil, err
		}
		text, contentErr := a.client.content(ctx, st.LinkID)
		if contentErr != nil && !errors.Is(contentErr, errNotFound) {
			return nil, contentErr
		}
		highlights, err := a.client.highlights(ctx, st.LinkID, 0)
		if err != nil {
			return nil, err
		}
		st.DraftID = randomID()
		return detailView(r, st, item, text, highlights.Data), nil
	}
	if st.Mode != "" {
		return nil, errors.New("invalid reading view")
	}
	if st.View == "" {
		st.View = "unread"
	}
	page, err := a.client.links(ctx, st.View, st.Query, st.Offset)
	a.record(err)
	if err != nil {
		return nil, err
	}
	v := &wire.View{Title: t.reading, State: ext.EncodeState(st), Search: &wire.Search{Placeholder: t.search, Value: st.Query, Action: wire.Action{ID: "search"}}, Filter: &wire.Filter{
		Name: "view", Title: t.reading, Value: st.View, Action: wire.Action{ID: "view"},
		Choices: []wire.Choice{{Value: "unread", Title: t.unread}, {Value: "starred", Title: t.starred}},
	}}
	if st.View == "search" {
		v.Filter.Value = "unread"
	}
	section := wire.Section{Title: fmt.Sprintf(t.results, len(page.Data))}
	for _, item := range page.Data {
		if item.ID == "" || item.Title == "" {
			continue
		}
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentRow, Key: "link-" + item.ID, Title: clipText(item.Title, 1800), Subtitle: clipText(item.Author+" · "+strings.Join(item.Tags, ", "), 1800), Action: &wire.Action{ID: "detail", Values: map[string]string{"id": item.ID}}})
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentEmpty, Title: t.noLinks})
	}
	if page.HasMore {
		section.More = &wire.More{Title: t.more, Action: wire.Action{ID: "more"}}
	}
	v.Sections = []wire.Section{section}
	return v, nil
}

func detailView(r *ext.UIRequest, st pageState, item link, text string, highlights []highlight) *wire.View {
	t := textsFor(r.Locale)
	items := []wire.Component{{Type: wire.ComponentText, Text: clipText(item.Author+" · "+strings.Join(item.Tags, ", "), 1800)}}
	if link := safeArticleLink(item.URL); link != "" {
		items = append(items, wire.Component{Type: wire.ComponentLink, Title: t.open, URL: link})
	}
	if item.ReadAt == nil || *item.ReadAt == "" {
		items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.markRead, Action: &wire.Action{ID: "read", Confirm: t.markRead + "\n" + item.Title}})
	}
	starTitle := t.star
	starred := true
	if item.Starred {
		starTitle, starred = t.unstar, false
	}
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: starTitle, Action: &wire.Action{ID: "star", Values: map[string]string{"starred": strconv.FormatBool(starred)}, Confirm: starTitle + "\n" + item.Title}}, wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}})
	v := &wire.View{Title: clipText(item.Title, 1800), State: ext.EncodeState(st), Sections: []wire.Section{{Title: t.reading, Items: items}}}
	if text != "" {
		v.Sections = append(v.Sections, wire.Section{Title: t.article, Items: textComponents(clipText(text, 60000))})
	} else {
		v.Sections = append(v.Sections, wire.Section{Title: t.article, Items: []wire.Component{{Type: wire.ComponentText, Text: t.noContent, Style: wire.TextStyleSecondary}}})
	}
	section := wire.Section{Title: t.highlights}
	for _, item := range highlights {
		if item.Content == "" {
			continue
		}
		section.Items = append(section.Items, textComponents(clipText(item.Content, 12000))...)
		if item.Note != nil && *item.Note != "" {
			section.Items = append(section.Items, wire.Component{Type: wire.ComponentText, Text: clipText(*item.Note, 1800), Style: wire.TextStyleSecondary})
		}
	}
	if len(section.Items) == 0 {
		section.Items = append(section.Items, wire.Component{Type: wire.ComponentText, Text: t.noHighlights, Style: wire.TextStyleSecondary})
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
			if len(out) >= 220 {
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
