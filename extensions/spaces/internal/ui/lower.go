package ui

import (
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"
)

// lowerRefresh is how often, in seconds, an app that ignores invalidation draws the page again.
const lowerRefresh = 30

// lower rewrites a view for an app of an older UI version, so that it draws only the components of that version. The
// screens are written in the vocabulary of the newest version; the rewrites run in order while the app is below the
// version each one needs. Version 0 says nothing about the app, as the SDK reads it, and draws the view as it is.
func lower(v *wire.View, version int) *wire.View {
	if v == nil || version <= 0 {
		return v
	}
	if version < 4 {
		v = below4(v)
	}
	if version < 3 {
		v = below3(v)
	}
	if version < 2 {
		v = below2(v)
	}
	return v
}

// below4 draws the header as texts, the tabs as a leading section of rows, and drops the split layout. A pushed page
// (one with a title) gets a button back to the page below.
func below4(v *wire.View) *wire.View {
	out := *v
	out.Layout, out.Tabs = "", nil
	out.Sections = []wire.Section{}
	if v.Tabs != nil {
		out.Sections = append(out.Sections, wire.Section{Items: tabRows(v.Tabs)})
	}
	for _, s := range v.Sections {
		ns := wire.Section{Title: s.Title, Footer: s.Footer, More: s.More, Items: []wire.Component{}}
		for _, it := range s.Items {
			ns.Items = append(ns.Items, headerTexts(it)...)
		}
		out.Sections = append(out.Sections, ns)
	}
	if v.Title != "" {
		out.Sections = append(out.Sections, wire.Section{Items: []wire.Component{
			button("返回", &wire.Action{ID: "back"}, wire.ButtonRoleNormal),
		}})
	}
	return &out
}

// below3 draws tables as rows, empty states as a text and a button, and the filter as a section of rows. Search is
// dropped, and more becomes a plain button at the end of its section, so that the next page stays reachable. The page
// is drawn again every thirty seconds, since invalidation is ignored.
func below3(v *wire.View) *wire.View {
	out := *v
	out.Search, out.Filter, out.RefreshAfter = nil, nil, lowerRefresh
	out.Sections = []wire.Section{}
	if v.Filter != nil {
		out.Sections = append(out.Sections, wire.Section{Items: filterRows(v.Filter)})
	}
	for _, s := range v.Sections {
		ns := wire.Section{Title: s.Title, Footer: s.Footer, Items: []wire.Component{}}
		for _, it := range s.Items {
			ns.Items = append(ns.Items, plainComponents(it)...)
		}
		if s.More != nil {
			more := s.More.Action
			ns.Items = append(ns.Items, button(textOr(s.More.Title, "更多"), &more, wire.ButtonRoleNormal))
		}
		out.Sections = append(out.Sections, ns)
	}
	return &out
}

// below2 draws stats as metrics and a segmented control as buttons.
func below2(v *wire.View) *wire.View {
	out := *v
	out.Sections = make([]wire.Section, 0, len(v.Sections))
	for _, s := range v.Sections {
		ns := s
		ns.Items = []wire.Component{}
		for _, it := range s.Items {
			ns.Items = append(ns.Items, metricButtons(it)...)
		}
		out.Sections = append(out.Sections, ns)
	}
	return &out
}

// tabRows is the tabs as rows: each opens its tab through the tabs' action with the tab's value as argument.
func tabRows(t *wire.Tabs) []wire.Component {
	out := []wire.Component{}
	for _, tab := range t.Items {
		r := wire.Component{
			Type: wire.ComponentRow, Title: textOr(tab.Title, tab.Value), Value: tab.Badge,
			Action: &wire.Action{ID: t.Action.ID + ":" + tab.Value},
		}
		if tab.Value == t.Value {
			r.Subtitle = "当前"
		}
		out = append(out, r)
	}
	return out
}

// filterRows is the filter as rows, each choosing through the filter's action with the choice as argument.
func filterRows(f *wire.Filter) []wire.Component {
	out := []wire.Component{}
	for _, ch := range f.Choices {
		r := wire.Component{
			Type: wire.ComponentRow, Title: textOr(ch.Title, ch.Value),
			Action: &wire.Action{ID: f.Action.ID + ":" + ch.Value},
		}
		if ch.Value == f.Value {
			r.Subtitle = "当前"
		}
		out = append(out, r)
	}
	return out
}

// headerTexts is a header as texts: its title, then captions for its subtitle, badges and facts. Any other component
// stays as it is.
func headerTexts(it wire.Component) []wire.Component {
	if it.Type != wire.ComponentHeader {
		return []wire.Component{it}
	}
	out := []wire.Component{{Type: wire.ComponentText, Text: it.Title, Style: wire.TextStyleBody}}
	if it.Subtitle != "" {
		out = append(out, wire.Component{Type: wire.ComponentText, Text: it.Subtitle, Style: wire.TextStyleCaption})
	}
	if len(it.Badges) > 0 {
		var parts []string
		for _, b := range it.Badges {
			parts = append(parts, b.Text)
		}
		out = append(out, wire.Component{Type: wire.ComponentText, Text: strings.Join(parts, " · "), Style: wire.TextStyleCaption})
	}
	for _, f := range it.Facts {
		text := f.Value
		if f.Label != "" {
			text = f.Label + " " + f.Value
		}
		out = append(out, wire.Component{Type: wire.ComponentText, Text: text, Style: wire.TextStyleCaption})
	}
	return out
}

// plainComponents is a component in the vocabulary of UI version 2: a table is a row per line (its caption a text
// above), an empty state a text and a button. Any other component stays as it is.
func plainComponents(it wire.Component) []wire.Component {
	switch it.Type {
	case wire.ComponentTable:
		var out []wire.Component
		if it.Caption != "" {
			out = append(out, wire.Component{Type: wire.ComponentText, Text: it.Caption, Style: wire.TextStyleCaption})
		}
		for _, cells := range it.Rows {
			r := wire.Component{Type: wire.ComponentRow, Title: "—"}
			if len(cells) > 0 {
				r.Title = textOr(cells[0], "—")
				r.Subtitle = cut(strings.Join(cells[1:], " · "), maxText)
			}
			out = append(out, r)
		}
		return out
	case wire.ComponentEmpty:
		out := []wire.Component{{Type: wire.ComponentText, Text: it.Title, Style: wire.TextStyleBody}}
		if it.Detail != "" {
			out = append(out, wire.Component{Type: wire.ComponentText, Text: it.Detail, Style: wire.TextStyleSecondary})
		}
		if it.Button != nil {
			a := it.Button.Action
			out = append(out, wire.Component{Type: wire.ComponentButton, Title: it.Button.Title, Action: &a, Role: wire.ButtonRoleNormal})
		}
		return out
	}
	return []wire.Component{it}
}

// metricButtons is a stat grid as metrics and a segmented control as buttons. Any other component stays as it is.
func metricButtons(it wire.Component) []wire.Component {
	switch it.Type {
	case wire.ComponentStatGrid:
		out := []wire.Component{}
		for _, s := range it.Stats {
			out = append(out, wire.Component{Type: wire.ComponentMetric, Label: s.Label, Value: s.Value, Detail: s.Detail, Tone: s.Tone})
		}
		return out
	case wire.ComponentSegmented:
		out := []wire.Component{}
		if it.Action == nil {
			return out
		}
		for _, ch := range it.Choices {
			out = append(out, wire.Component{
				Type: wire.ComponentButton, Title: textOr(ch.Title, ch.Value), Role: wire.ButtonRoleNormal,
				Action: &wire.Action{ID: it.Action.ID + ":" + ch.Value},
			})
		}
		return out
	}
	return []wire.Component{it}
}
