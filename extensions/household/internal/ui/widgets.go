package ui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// Limits and labels shared by the screens. A row's title holds at most maxTitle runes.
const (
	maxTitle  = 200
	maxSearch = 200
	unsetText = "未放置" // the place of an item with none
)

// button is a button with a role.
func button(title string, a *wire.Action, role string) wire.Component {
	return wire.Component{Type: wire.ComponentButton, Title: title, Action: a, Role: role}
}

// statGrid is a row of stats.
func statGrid(stats []wire.Stat) wire.Component {
	return wire.Component{Type: wire.ComponentStatGrid, Stats: stats}
}

// quiet is one text line in the secondary style, for a section with nothing to show.
func quiet(text string) wire.Component {
	return wire.Component{Type: wire.ComponentText, Text: text, Style: wire.TextStyleSecondary}
}

// orQuiet is the items, or the one quiet line when there are none.
func orQuiet(items []wire.Component, text string) []wire.Component {
	if len(items) == 0 {
		return []wire.Component{quiet(text)}
	}
	return items
}

// editAction opens the form of the record with the id, pushed over the page.
func editAction(id, title string) *wire.Action {
	return ext.Open("edit:"+id, PageUI, state{F: "edit:" + id}, cut(title, maxTitle))
}

// addAction opens the form that adds a record of the type, pushed over the page.
func addAction(typ string) *wire.Action {
	return ext.Open("add:"+typ, PageUI, state{F: "new:" + typ}, "添加"+typeName(typ))
}

// typeName is the name of a type of the household space, for the words on a screen.
func typeName(key string) string {
	if t, ok := householdType(key); ok {
		return t.Name
	}
	return key
}

// cut keeps at most n runes of s.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// join is the non-empty parts joined with " · ".
func join(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, " · ")
}

// num is a number as the screens write it: no trailing zeros.
func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// shortDay is a day as MM-DD, "" for an unset day.
func shortDay(d model.Day) string {
	if len(d) != len(model.Day("2006-01-02")) {
		return ""
	}
	return string(d)[5:]
}

// kindTone tones a finding: what is gone or used up is a danger, what runs low a caution.
func kindTone(k rules.Kind) string {
	switch k {
	case rules.Expired, rules.Out:
		return wire.ToneDanger
	}
	return wire.ToneCaution
}

// itemSubtitle is the line under an item: its estimated quantity with the unit, then its place.
func (c *ctx) itemSubtitle(it model.Item, est float64) string {
	qty := ""
	if it.HasQty {
		qty = fmt.Sprintf("约 %s%s", num(est), it.Unit)
	}
	return join(qty, c.placeName(it.PlaceID))
}

// placeName is the title of a place, or 未放置 for none or a place that is gone.
func (c *ctx) placeName(id string) string {
	if p, ok := c.home.Place(id); ok {
		return p.Title
	}
	return unsetText
}

// choreLine is the line under a chore: its period, who does it and when it is next.
func choreLine(ch model.Chore) string {
	period := "一次性"
	if ch.Every > 0 {
		period = fmt.Sprintf("每 %s 天", num(ch.Every))
	}
	next := ""
	if ch.Next != "" {
		next = "下次 " + shortDay(ch.Next)
	}
	return join(period, ch.Who, next)
}

// householdSpace is the household space of the base; nil until the first write creates it.
func householdSpace(b *kernel.Base) *kernel.Space {
	for _, sp := range b.Spaces {
		if sp != nil && sp.ID == model.SpaceID {
			return sp
		}
	}
	return nil
}

// slicesOf is the first n of the items, or all of them when there are fewer.
func slicesOf[T any](xs []T, n int) []T {
	return xs[:min(n, len(xs))]
}

// editRow is the row action that opens the form of the record.
func editRow(id, title string) wire.RowAction {
	return wire.RowAction{Title: "编辑", Action: *editAction(id, title)}
}

// deleteRow is the row action that deletes the record, after the user confirms it.
func deleteRow(id, title string) wire.RowAction {
	return wire.RowAction{Title: "删除", Role: wire.ButtonRoleDestructive, Action: wire.Action{
		ID: "delete:" + id, Confirm: fmt.Sprintf("删除「%s」？", cut(title, maxTitle)),
	}}
}

// countOf is "n 样食材"-style text: the number and the noun, "" for none.
func countOf(n int, noun string) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d %s", n, noun)
}
