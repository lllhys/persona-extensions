package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/ops"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// Form limits. A single relation offers the newest pickLimit records of its target; a multichoice has at most
// multiMost choices, and the records it holds are always among them.
const (
	choicePrefix = "=" // a choice is sent as "=" and its value; "=" alone is unset
	unsetLabel   = "未设置"
	pickLimit    = 100
	multiMost    = 50
)

// formPage draws the form the state names: a field per property of the type, 名称 first, then 保存 and 取消. A record
// that exists also has its own buttons, and 删除 asks first. nil when the state names no record or type.
func (c *ctx) formPage() *wire.View {
	typ, e, ok := c.formTarget()
	if !ok {
		return nil
	}
	fields := append(c.fields(typ, e),
		button("保存", &wire.Action{ID: "save", Submit: true}, wire.ButtonRoleNormal),
		button("取消", &wire.Action{ID: "back"}, wire.ButtonRoleNormal),
	)
	v := &wire.View{Title: "添加" + typ.Name, Sections: []wire.Section{{Items: fields}}}
	if e != nil {
		v.Title = cut(e.Title, maxTitle)
		v.Sections = append(v.Sections, wire.Section{Items: recordButtons(e)})
	}
	return v
}

// recordButtons are the buttons of an existing record's form: 标记完成 for a chore, then 删除 with its confirm.
func recordButtons(e *kernel.Entity) []wire.Component {
	var out []wire.Component
	if e.Type == model.TChore {
		out = append(out, button("标记完成", &wire.Action{ID: "chore:" + e.ID}, wire.ButtonRoleNormal))
	}
	return append(out, button("删除", &wire.Action{
		ID: "delete:" + e.ID, Confirm: fmt.Sprintf("删除「%s」？", cut(e.Title, maxTitle)),
	}, wire.ButtonRoleDestructive))
}

// formTarget is the type of the state's form, and the record it edits: none for a new record. false when the state
// names no form, or a record or type that is not there.
func (c *ctx) formTarget() (kernel.Type, *kernel.Entity, bool) {
	kind, arg, _ := strings.Cut(c.st.F, ":")
	switch kind {
	case "new":
		typ, ok := householdType(arg)
		return typ, nil, ok
	case "edit":
		e, ok := c.record(arg)
		if !ok {
			return kernel.Type{}, nil, false
		}
		typ, ok := householdType(e.Type)
		return typ, e, ok
	}
	return kernel.Type{}, nil, false
}

// householdType is the type of the household schema with the key.
func householdType(key string) (kernel.Type, bool) {
	for _, t := range model.Types() {
		if t.Key == key {
			return t, true
		}
	}
	return kernel.Type{}, false
}

// formValues reads the submitted fields into the values of the record. A field the form did not send is left out, so
// the record keeps its value; a field sent empty is unset.
func (c *ctx) formValues(typ kernel.Type) (map[string]any, error) {
	vals := map[string]any{}
	for _, p := range typ.Props {
		k, ok := kernel.KindOf(p.Kind)
		name := "p_" + p.Key
		raw, sent := c.r.Values[name]
		if !ok || k.Input == kernel.InNone || !sent {
			continue
		}
		if k.Input == kernel.InPick && p.Many {
			if ids := c.r.Multi(name); len(ids) > 0 {
				vals[p.Key] = ids
			} else {
				vals[p.Key] = nil
			}
			continue
		}
		v, err := submitted(p, raw)
		if err != nil {
			return nil, err
		}
		vals[p.Key] = v
	}
	// A quantity whose count date is left empty is counted today, as a quantity given without one is.
	if _, sent := c.r.Values["p_counted"]; sent && vals["qty"] != nil && vals["counted"] == nil {
		vals["counted"] = string(c.today)
	}
	return vals, nil
}

// submitted reads the text a form sent for a property into its value through the kind's Parse. A choice or a relation
// is sent as "=" and the value, where "" and "=" are unset.
func submitted(p kernel.Prop, raw string) (any, error) {
	k, ok := kernel.KindOf(p.Kind)
	if !ok {
		return nil, fmt.Errorf("没有属性类型「%s」", p.Kind)
	}
	if k.Input == kernel.InChoice || k.Input == kernel.InPick {
		raw = strings.TrimPrefix(raw, choicePrefix)
	}
	if raw == "" {
		return nil, nil
	}
	v, err := k.Parse(&p, raw)
	if err != nil {
		return nil, fmt.Errorf("「%s」：%w", p.Name, err)
	}
	return v, nil
}

// actSave saves the form: a new record, or the edited one. The form closes when it is saved.
func actSave(c *ctx, _ string) (*wire.ActionResult, error) {
	typ, e, ok := c.formTarget()
	if !ok {
		return nil, errors.New("没有要保存的记录")
	}
	title := strings.TrimSpace(c.r.Values["title"])
	if title == "" {
		return nil, errors.New("请填写名称")
	}
	vals, err := c.formValues(typ)
	if err != nil {
		return nil, err
	}
	in := kernel.EntityInput{Type: typ.Key, Title: &title, Values: vals}
	toast := "已添加「" + title + "」"
	if e != nil {
		in.ID, in.Type = e.ID, ""
		toast = "已保存「" + title + "」"
	}
	if _, err := ops.Save(c.b, in, c.now, c.loc); err != nil {
		return nil, err
	}
	return &wire.ActionResult{Toast: toast, Pop: true}, nil
}

// actAdd opens the form of a new record of the type, pushed over the page.
func actAdd(c *ctx, arg string) (*wire.ActionResult, error) {
	key := c.pick(arg, "type")
	typ, ok := householdType(key)
	if !ok {
		return nil, fmt.Errorf("没有类型「%s」", key)
	}
	return &wire.ActionResult{Push: addAction(typ.Key).Push}, nil
}

// actEdit opens the form of a record, pushed over the page.
func actEdit(c *ctx, arg string) (*wire.ActionResult, error) {
	e, ok := c.record(arg)
	if !ok {
		return nil, errors.New("没有这条记录")
	}
	return &wire.ActionResult{Push: editAction(e.ID, e.Title).Push}, nil
}

// actDelete deletes a record. On the form of that record the form closes too.
func actDelete(c *ctx, arg string) (*wire.ActionResult, error) {
	e, ok := c.record(arg)
	if !ok {
		return nil, errors.New("没有这条记录")
	}
	title := cut(e.Title, maxTitle)
	if err := ops.Remove(c.b, e.ID, c.now); err != nil {
		return nil, err
	}
	return &wire.ActionResult{Toast: "已删除「" + title + "」", Pop: c.st.F == "edit:"+e.ID}, nil
}
