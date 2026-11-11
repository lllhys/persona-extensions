package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The connection page: contributes.ui "connection", an extension_page under the extension's details (Settings ›
// Extensions › 1Password › 连接). Whether the token is set and 1Password takes it, the vaults the service account can
// read, and what the assistant can and cannot do with them. Opening it signs in if nothing has yet, so it is also
// where a user checks a new token. Only components of UI version 3, which every app draws.

const mostVaultRows = 100

func (a *app) page() ext.Page {
	return ext.Page{
		Render: func(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
			return a.view(ctx, r), nil
		},
		Action: func(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
			if r.Action != "refresh" {
				return nil, fmt.Errorf("unknown page action %q", r.Action)
			}
			return &wire.ActionResult{View: a.view(ctx, r)}, nil
		},
	}
}

func (a *app) view(ctx context.Context, r *ext.UIRequest) *wire.View {
	t := textsFor(r.Locale)
	v := &wire.View{Title: t.connection}
	a.mu.Lock()
	configured := a.token != ""
	a.mu.Unlock()
	if !configured {
		v.Sections = []wire.Section{
			{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: t.notSetUp, Detail: t.notSetUpDetail, Icon: "settings"}}},
			{Title: t.setUpTitle, Items: steps(t.setUpSteps)},
			{Title: t.canTitle, Items: steps(t.can)},
		}
		return v
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	vaults, err := a.listVaults(ctx)
	if err != nil {
		title, guide, says := t.unreachable, t.unreachableGuide, fmt.Sprintf(t.says, clip(err.Error(), 300))
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			guide, says = t.timedOut+" "+t.unreachableGuide, ""
		case kindOf(err) == errAuth:
			title, guide = t.refused, t.refusedGuide
		case kindOf(err) == errRate:
			title, guide = t.rateLimited, t.rateLimitedGuide
		}
		items := []wire.Component{
			{Type: wire.ComponentRow, Title: title, Icon: "warning", Tone: "danger"},
			{Type: wire.ComponentText, Text: guide},
		}
		if says != "" {
			items = append(items, wire.Component{Type: wire.ComponentText, Text: says, Style: wire.TextStyleSecondary})
		}
		items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.checkAgain, Action: &wire.Action{ID: "refresh"}})
		v.Sections = []wire.Section{
			{Items: items},
			{Title: t.canTitle, Items: steps(t.can)},
		}
		return v
	}

	conn := wire.Section{Items: []wire.Component{
		{Type: wire.ComponentRow, Title: t.connected, Subtitle: fmt.Sprintf(t.connectedDetail, len(vaults)), Icon: "check", Tone: "positive"},
		{Type: wire.ComponentButton, Title: t.checkAgain, Action: &wire.Action{ID: "refresh"}},
	}}
	list := wire.Section{Title: t.vaultsTitle, Footer: t.vaultsFooter}
	for i, vault := range vaults {
		if i == mostVaultRows {
			list.Items = append(list.Items, wire.Component{Type: wire.ComponentText, Text: fmt.Sprintf(t.moreVaults, len(vaults)-mostVaultRows), Style: wire.TextStyleSecondary})
			break
		}
		list.Items = append(list.Items, wire.Component{Type: wire.ComponentRow, Title: clip(redact(vault.Title, ""), 200),
			Subtitle: fmt.Sprintf(t.items, vault.Items), Icon: "folder"})
	}
	if len(vaults) == 0 {
		list.Items = []wire.Component{{Type: wire.ComponentEmpty, Title: t.noVaults, Detail: t.noVaultsDetail, Icon: "folder"}}
		list.Footer = ""
	}
	v.Sections = []wire.Section{conn, list, {Title: t.canTitle, Items: steps(t.can)}}
	return v
}

func steps(lines []string) []wire.Component {
	out := make([]wire.Component, 0, len(lines))
	for _, l := range lines {
		out = append(out, wire.Component{Type: wire.ComponentText, Text: l})
	}
	return out
}
