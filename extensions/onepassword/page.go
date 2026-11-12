package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The connection page: contributes.ui "connection", an extension_page under the extension's details (Settings ›
// Extensions › 1Password › 连接). Whether the token is set and 1Password takes it, the vaults the service account can
// read, the last uses of its op:// references by Persona, and what the assistant can and cannot do with them. Opening it signs in if nothing has yet, so it is also
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
			if limited, _ := a.limited(); !limited { // Check again asks 1Password anew, unless its limit holds
				a.forgetMeta()
			}
			return &wire.ActionResult{View: a.view(ctx, r)}, nil
		},
	}
}

func (a *app) view(ctx context.Context, r *ext.UIRequest) *wire.View {
	t := textsFor(r.Locale)
	v := &wire.View{Title: t.connection}
	a.mu.Lock()
	configured := a.token != "" || a.machine != ""
	machine, nodes := a.machine, a.nodes
	a.mu.Unlock()
	if !configured {
		v.Sections = []wire.Section{
			{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: t.notSetUp, Detail: t.notSetUpDetail, Icon: "settings"}}},
			{Title: t.setUpTitle, Items: steps(t.setUpSteps)},
			{Title: t.opSetUpTitle, Items: steps(t.opSetUpSteps)},
			{Title: t.canTitle, Items: steps(t.can)},
		}
		return v
	}
	// The words that differ between the service account and op on a computer.
	unreachableGuide, rateGuide, connectedDetail, vaultsFooter, noVaultsDetail := t.unreachableGuide, t.rateLimitedGuide,
		t.connectedDetail, t.vaultsFooter, t.noVaultsDetail
	if machine != "" {
		unreachableGuide, rateGuide, connectedDetail, vaultsFooter, noVaultsDetail = t.opUnreachableGuide, t.opRateLimitedGuide,
			t.opConnectedDetail, t.opVaultsFooter, t.opNoVaultsDetail
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var vaults []vaultInfo
	err := a.opReady(ctx, machine, nodes)
	if err == nil {
		vaults, err = a.readVaults(ctx, true) // the page asks afresh (cache.go), unless the rate limit holds
	}
	if err != nil {
		title, guide, says := t.unreachable, unreachableGuide, fmt.Sprintf(t.says, clip(err.Error(), 300))
		opTitle, opGuide, onOp := opWords(t, err)
		switch {
		case errors.Is(err, context.DeadlineExceeded) && machine != "": // most likely op waiting for the user to approve it
			title, guide, says = t.opLocked, t.timedOut+" "+t.opLockedGuide, ""
		case errors.Is(err, context.DeadlineExceeded):
			guide, says = t.timedOut+" "+unreachableGuide, ""
		case onOp:
			title, guide = opTitle, opGuide
		case kindOf(err) == errAuth && machine == "":
			title, guide = t.refused, t.refusedGuide
		case kindOf(err) == errRate:
			title, guide = t.rateLimited, rateGuide
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
			a.usesSection(t, r.TZ),
			{Title: t.canTitle, Items: steps(t.can)},
		}
		return v
	}

	conn := wire.Section{Items: []wire.Component{
		{Type: wire.ComponentRow, Title: t.connected, Subtitle: fmt.Sprintf(connectedDetail, len(vaults)), Icon: "check", Tone: "positive"},
		{Type: wire.ComponentButton, Title: t.checkAgain, Action: &wire.Action{ID: "refresh"}},
	}}
	if limited, _ := a.limited(); limited { // the vaults below are what was kept: 1Password is not asked meanwhile
		conn.Items = []wire.Component{
			{Type: wire.ComponentRow, Title: t.rateLimited, Icon: "warning", Tone: "danger"},
			{Type: wire.ComponentText, Text: rateGuide},
			{Type: wire.ComponentButton, Title: t.checkAgain, Action: &wire.Action{ID: "refresh"}},
		}
	}
	list := wire.Section{Title: t.vaultsTitle, Footer: vaultsFooter}
	for i, vault := range vaults {
		if i == mostVaultRows {
			list.Items = append(list.Items, wire.Component{Type: wire.ComponentText, Text: fmt.Sprintf(t.moreVaults, len(vaults)-mostVaultRows), Style: wire.TextStyleSecondary})
			break
		}
		row := wire.Component{Type: wire.ComponentRow, Title: clip(redact(vault.Title, ""), 200), Icon: "folder"}
		if vault.Items >= 0 { // op on a computer may not say
			row.Subtitle = fmt.Sprintf(t.items, vault.Items)
		}
		list.Items = append(list.Items, row)
	}
	if len(vaults) == 0 {
		list.Items = []wire.Component{{Type: wire.ComponentEmpty, Title: t.noVaults, Detail: noVaultsDetail, Icon: "folder"}}
		list.Footer = ""
	}
	v.Sections = []wire.Section{conn, list, a.usesSection(t, r.TZ), {Title: t.canTitle, Items: steps(t.can)}}
	return v
}

// usesSection lists the uses Persona told of (secrets.used), newest first: the reference, what it was for and who
// approved it, and when, in the viewer's time zone. Never a value: the program never has one to show.
func (a *app) usesSection(t *texts, tz string) wire.Section {
	sec := wire.Section{Title: t.usesTitle, Footer: t.usesFooter}
	loc, err := time.LoadLocation(tz)
	if tz == "" || err != nil {
		loc = time.UTC
	}
	a.mu.Lock()
	token := a.token
	a.mu.Unlock()
	for _, u := range a.recentUses() {
		parts := []string{purposeText(t, u.Purpose)}
		if by := approvedText(t, u.ApprovedBy); by != "" {
			parts = append(parts, by)
		}
		parts = append(parts, u.At.In(loc).Format("2006-01-02 15:04"))
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentRow, Title: clip(redact(u.Reference, token), 120),
			Subtitle: strings.Join(parts, " · "), Icon: "lock"})
	}
	if len(sec.Items) == 0 {
		sec.Items = []wire.Component{{Type: wire.ComponentEmpty, Title: t.noUses, Detail: t.noUsesDetail, Icon: "lock"}}
		sec.Footer = ""
	}
	return sec
}

func purposeText(t *texts, p wire.SecretPurpose) string {
	var s string
	switch p.Kind {
	case "extension":
		s = fmt.Sprintf(t.useExtension, p.ID)
	case "launcher":
		s = fmt.Sprintf(t.useLauncher, p.ID)
	case "agent_run":
		s = fmt.Sprintf(t.useAgent, p.ID)
	default:
		s = p.Kind
	}
	if p.Machine != "" {
		s += fmt.Sprintf(t.useOn, p.Machine)
	}
	return s
}

// approvedText: settings, rule or device:<id>, as Persona sends them; anything else is left out rather than guessed.
func approvedText(t *texts, by string) string {
	switch {
	case by == "settings":
		return t.bySettings
	case by == "rule":
		return t.byRule
	case strings.HasPrefix(by, "device:"):
		return t.byDevice
	}
	return ""
}

func steps(lines []string) []wire.Component {
	out := make([]wire.Component, 0, len(lines))
	for _, l := range lines {
		out = append(out, wire.Component{Type: wire.ComponentText, Text: l})
	}
	return out
}

// opReady asks the computer's program whether op is installed there before the page lists vaults, which asks the
// 1Password app: a missing op is told without a request that can only fail. nil for the service account.
func (a *app) opReady(ctx context.Context, machine string, nodes nodeCaller) error {
	if machine == "" || nodes == nil {
		return nil
	}
	st, err := nodeAPI{call: nodes, machine: machine}.opStatus(ctx)
	if err != nil {
		return a.record(err)
	}
	if !st.Installed {
		return a.record(&ext.Error{Code: codeMissing, Message: "op (the 1Password CLI) is not installed on that computer"})
	}
	return nil
}
