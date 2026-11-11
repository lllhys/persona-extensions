package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// unsetValue is the choice that stands for "no value". A choice is sent as "=" and the value; "=" alone is unset,
// so no option can collide with the unset marker.
const (
	choicePrefix = "="
	unsetLabel   = "未设置"
	pickLimit    = 100 // the newest entities offered by a relation's choice
)

// fieldKinds maps the abstract control of a kind one to one to the field kind the apps draw. A computed kind has no
// control and is not in the table.
var fieldKinds = map[kernel.Input]string{
	kernel.InLine:   wire.FieldKindText,
	kernel.InNumber: wire.FieldKindNumber,
	kernel.InToggle: wire.FieldKindToggle,
	kernel.InChoice: wire.FieldKindChoice,
	kernel.InPick:   wire.FieldKindChoice,
}

// field is the form field of one stored property, named name, holding v. ok is false for a computed property, which
// has no input.
func (c *ctx) field(p kernel.Prop, name string, v any) (wire.Component, bool) {
	k, ok := kernel.KindOf(p.Kind)
	if !ok {
		return wire.Component{}, false
	}
	kind, ok := fieldKinds[k.Input]
	if !ok {
		return wire.Component{}, false
	}
	f := wire.Component{
		Type: wire.ComponentField, Name: name, Label: p.Name, Kind: kind,
		Value: fieldValue(k, v),
	}
	switch k.Input {
	case kernel.InLine:
		f.Placeholder = k.Hint
	case kernel.InChoice:
		f.Choices = optionChoices(k.Enum(&p))
	case kernel.InPick:
		f.Choices = c.targetChoices(p, v)
	}
	return f, true
}

// fieldValue is the text a field starts with: a choice is sent as "=" and its value, a number as its plain decimal, a
// toggle as true or false, a relation as its first id.
func fieldValue(k *kernel.Kind, v any) string {
	switch k.Input {
	case kernel.InChoice:
		s, _ := v.(string)
		return choicePrefix + s
	case kernel.InPick:
		ids := k.Refs(v)
		if len(ids) == 0 {
			return choicePrefix
		}
		return choicePrefix + ids[0]
	case kernel.InNumber:
		if f, ok := v.(float64); ok {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
		return ""
	case kernel.InToggle:
		b, _ := v.(bool)
		return strconv.FormatBool(b)
	}
	s, _ := v.(string)
	return s
}

// optionChoices offers the unset choice and then the options, in order.
func optionChoices(options []string) []wire.Choice {
	out := []wire.Choice{{Value: choicePrefix, Title: unsetLabel}}
	for _, o := range options {
		out = append(out, wire.Choice{Value: choicePrefix + o, Title: o})
	}
	return out
}

// targetChoices offers the unset choice and the newest entities of the relation's target type. The value the field
// holds is always offered, even when it is older than the newest ones.
func (c *ctx) targetChoices(p kernel.Prop, v any) []wire.Choice {
	out := []wire.Choice{{Value: choicePrefix, Title: unsetLabel}}
	if c.sp == nil {
		return out
	}
	k, _ := kernel.KindOf(p.Kind)
	held := ""
	if ids := k.Refs(v); len(ids) > 0 {
		held = ids[0]
	}
	var set []*kernel.Entity
	for _, e := range c.sp.Entities {
		if e != nil && e.Type == p.Target {
			set = append(set, e)
		}
	}
	slices.SortStableFunc(set, func(a, b *kernel.Entity) int { return b.Updated.Compare(a.Updated) })
	if len(set) > pickLimit {
		set = set[:pickLimit]
	}
	if held != "" && !slices.ContainsFunc(set, func(e *kernel.Entity) bool { return e.ID == held }) {
		if e := c.w.Entity(held); e != nil {
			set = append([]*kernel.Entity{e}, set...)
		}
	}
	for _, e := range set {
		out = append(out, wire.Choice{Value: choicePrefix + e.ID, Title: cut(e.Title, maxTitle)})
	}
	return out
}

// submitted reads the text a form sent for a property into its canonical value through the kind's Parse. A choice or
// a relation is sent as "=" and the value; "" and "=" are unset. A computed property cannot be submitted.
func submitted(p kernel.Prop, raw string) (any, error) {
	k, ok := kernel.KindOf(p.Kind)
	if !ok {
		return nil, fmt.Errorf("没有属性类型「%s」", p.Kind)
	}
	if k.Input == kernel.InNone {
		return nil, fmt.Errorf("「%s」由计算得出，不能填写", p.Name)
	}
	if k.Input == kernel.InChoice || k.Input == kernel.InPick {
		raw = strings.TrimPrefix(raw, choicePrefix)
	}
	if raw == "" {
		return nil, nil
	}
	return k.Parse(&p, raw)
}
