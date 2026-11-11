package ui

import (
	"encoding/json"
	"slices"
	"strconv"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// fields are the form's fields: 名称, then one per property the kind of which has an input, filled with the record's
// values when it is an edit.
func (c *ctx) fields(typ kernel.Type, e *kernel.Entity) []wire.Component {
	out := []wire.Component{{Type: wire.ComponentField, Name: "title", Label: "名称", Kind: wire.FieldKindText,
		Value: titleOf(e), Placeholder: "写下名称"}}
	for _, p := range typ.Props {
		var cur any
		if e != nil {
			cur = e.Values[p.Key]
		}
		if f, ok := c.field(p, cur); ok {
			out = append(out, f)
		}
	}
	return out
}

// titleOf is the record's title, "" for a new record.
func titleOf(e *kernel.Entity) string {
	if e == nil {
		return ""
	}
	return e.Title
}

// field is the form field of one property holding cur. A computed property has none. A relation with many targets is
// a multichoice, and is left out when its target has no records to choose from, since it needs a choice, or when it
// holds more records than a multichoice can offer.
func (c *ctx) field(p kernel.Prop, cur any) (wire.Component, bool) {
	k, ok := kernel.KindOf(p.Kind)
	if !ok || k.Input == kernel.InNone {
		return wire.Component{}, false
	}
	f := wire.Component{Type: wire.ComponentField, Name: "p_" + p.Key, Label: p.Name}
	switch {
	case p.Kind == kernel.KindDate:
		f.Kind, f.Value = wire.FieldKindDate, textOf(cur)
	case k.Input == kernel.InLine:
		f.Kind, f.Value, f.Placeholder = wire.FieldKindText, textOf(cur), k.Hint
	case k.Input == kernel.InNumber:
		f.Kind, f.Value = wire.FieldKindNumber, numberOf(cur)
	case k.Input == kernel.InToggle:
		on, _ := cur.(bool)
		f.Kind, f.Value = wire.FieldKindToggle, strconv.FormatBool(on)
	case k.Input == kernel.InChoice:
		f.Kind, f.Value = wire.FieldKindChoice, choicePrefix+textOf(cur)
		f.Choices = optionChoices(k.Enum(&p))
	case k.Input == kernel.InPick && p.Many:
		held := k.Refs(cur)
		// The field offers every held record, so it cannot hold more than a multichoice has choices: a property that
		// holds more is left off the form, and saving the form then leaves it as it is.
		if len(held) > multiMost {
			return wire.Component{}, false
		}
		picked := c.targetRecords(p.Target, held, multiMost)
		if len(picked) == 0 {
			return wire.Component{}, false
		}
		f.Kind, f.Choices, f.Value = wire.FieldKindMultichoice, recordChoices(picked, ""), heldJSON(picked, held)
	case k.Input == kernel.InPick:
		held := k.Refs(cur)
		picked := c.targetRecords(p.Target, held, pickLimit)
		f.Kind, f.Choices, f.Value = wire.FieldKindChoice, recordChoices(picked, choicePrefix), choicePrefix
		f.Choices = append([]wire.Choice{{Value: choicePrefix, Title: unsetLabel}}, f.Choices...)
		if len(held) > 0 && slices.ContainsFunc(picked, func(e *kernel.Entity) bool { return e.ID == held[0] }) {
			f.Value = choicePrefix + held[0]
		}
	default:
		return wire.Component{}, false
	}
	return f, true
}

// textOf is a text value, "" for none.
func textOf(v any) string {
	s, _ := v.(string)
	return s
}

// numberOf is a number value as the field writes it, "" for none.
func numberOf(v any) string {
	if f, ok := v.(float64); ok {
		return num(f)
	}
	return ""
}

// optionChoices offers the unset choice and then the options, in order.
func optionChoices(options []string) []wire.Choice {
	out := []wire.Choice{{Value: choicePrefix, Title: unsetLabel}}
	for _, o := range options {
		out = append(out, wire.Choice{Value: choicePrefix + o, Title: o})
	}
	return out
}

// targetRecords are the records of the target type a relation offers: the held ones first, then the newest, up to
// limit in all.
func (c *ctx) targetRecords(target string, held []string, limit int) []*kernel.Entity {
	var out []*kernel.Entity
	have := map[string]bool{}
	add := func(e *kernel.Entity) {
		if !have[e.ID] {
			have[e.ID] = true
			out = append(out, e)
		}
	}
	for _, id := range held {
		if e, ok := c.record(id); ok && e.Type == target {
			add(e)
		}
	}
	if c.sp == nil {
		return out
	}
	var newest []*kernel.Entity
	for _, e := range c.sp.Entities {
		if e != nil && e.Type == target {
			newest = append(newest, e)
		}
	}
	slices.SortStableFunc(newest, func(a, b *kernel.Entity) int { return b.Updated.Compare(a.Updated) })
	for _, e := range newest {
		if len(out) >= limit {
			break
		}
		add(e)
	}
	return out
}

// recordChoices offers the records by title, each value prefixed by prefix.
func recordChoices(picked []*kernel.Entity, prefix string) []wire.Choice {
	out := make([]wire.Choice, 0, len(picked))
	for _, e := range picked {
		out = append(out, wire.Choice{Value: prefix + e.ID, Title: cut(e.Title, maxTitle)})
	}
	return out
}

// heldJSON is the multichoice value: the JSON array of the held records that are offered, "" for none.
func heldJSON(picked []*kernel.Entity, held []string) string {
	var ids []string
	for _, id := range held {
		if slices.ContainsFunc(picked, func(e *kernel.Entity) bool { return e.ID == id }) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	raw, _ := json.Marshal(ids)
	return string(raw)
}
