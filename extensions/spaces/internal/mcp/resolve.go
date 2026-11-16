package mcp

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// This file resolves what an assistant naturally writes (a space or a type by its name, a related record by its
// title) to what the kernel takes (ids and keys), and states what would be accepted when it cannot. Nothing here
// changes the base.

// spaceOf resolves the space a tool names: an id first, else the one space with exactly that name. An empty ref is the
// only space. Every refusal lists the spaces there are.
func spaceOf(b *kernel.Base, ref string) (*kernel.Space, error) {
	spaces := slices.DeleteFunc(slices.Clone(b.Spaces), func(s *kernel.Space) bool { return s == nil })
	if ref == "" {
		switch len(spaces) {
		case 0:
			return nil, errors.New("还没有空间；先用 space_write 新建一个")
		case 1:
			return spaces[0], nil
		}
		return nil, fmt.Errorf("有多个空间，请用 space 指定（id 或名称）。现有空间：%s", spaceList(spaces))
	}
	if i := slices.IndexFunc(spaces, func(s *kernel.Space) bool { return s.ID == ref }); i >= 0 {
		return spaces[i], nil
	}
	named := slices.DeleteFunc(slices.Clone(spaces), func(s *kernel.Space) bool { return s.Name != ref })
	switch {
	case len(named) == 1:
		return named[0], nil
	case len(named) > 1:
		return nil, fmt.Errorf("有 %d 个空间叫「%s」，请用 id 指定：%s", len(named), ref, spaceList(named))
	case len(spaces) == 0:
		return nil, fmt.Errorf("没有空间「%s」；还没有空间，先用 space_write 新建一个", ref)
	}
	return nil, fmt.Errorf("没有空间「%s」。现有空间：%s", ref, spaceList(spaces))
}

// spaceList names spaces as 「名称（id）」, joined.
func spaceList(spaces []*kernel.Space) string {
	parts := make([]string, 0, len(spaces))
	for _, s := range spaces {
		parts = append(parts, s.Name+"（"+s.ID+"）")
	}
	return strings.Join(parts, "、")
}

// spaceFor resolves the space of a record: the one that holds the record when only its id is given, else the named
// one (or the only one).
func spaceFor(b *kernel.Base, space, id string) (*kernel.Space, error) {
	if space == "" && id != "" {
		sp, _, err := b.Find(id)
		return sp, err
	}
	return spaceOf(b, space)
}

// typeIn resolves a type of the space: a key first, else the one type with exactly that name. A refusal lists the
// space's types.
func typeIn(sp *kernel.Space, ref string) (*kernel.Type, error) {
	types := slices.DeleteFunc(slices.Clone(sp.Types), func(t *kernel.Type) bool { return t == nil })
	if t := typeByKey(sp, ref); t != nil {
		return t, nil
	}
	named := slices.DeleteFunc(slices.Clone(types), func(t *kernel.Type) bool { return t.Name != ref })
	switch {
	case len(named) == 1:
		return named[0], nil
	case len(named) > 1:
		return nil, fmt.Errorf("有 %d 个类型叫「%s」，请用 key 指定：%s", len(named), ref, typeList(named))
	case len(types) == 0:
		return nil, fmt.Errorf("没有类型「%s」；空间「%s」还没有类型，先用 type_write 定义一个", ref, sp.Name)
	}
	return nil, fmt.Errorf("没有类型「%s」。可选：%s", ref, typeList(types))
}

// typeList names types as 「key（名称）」, joined.
func typeList(types []*kernel.Type) string {
	parts := make([]string, 0, len(types))
	for _, t := range types {
		parts = append(parts, t.Key+"（"+t.Name+"）")
	}
	return strings.Join(parts, "、")
}

// newType is the type of a record to create: the one named, or the only type of the space when none is named.
func newType(sp *kernel.Space, ref string) (*kernel.Type, error) {
	if ref != "" {
		return typeIn(sp, ref)
	}
	types := slices.DeleteFunc(slices.Clone(sp.Types), func(t *kernel.Type) bool { return t == nil })
	switch len(types) {
	case 0:
		return nil, fmt.Errorf("空间「%s」还没有类型，先用 type_write 定义一个", sp.Name)
	case 1:
		return types[0], nil
	}
	return nil, fmt.Errorf("新建记录需要 type。可选：%s", typeList(types))
}

// propByKey finds a declared property of the type.
func propByKey(t *kernel.Type, key string) (kernel.Prop, bool) {
	i := slices.IndexFunc(t.Props, func(p kernel.Prop) bool { return p.Key == key })
	if i < 0 {
		return kernel.Prop{}, false
	}
	return t.Props[i], true
}

// scalarText is the text of a JSON scalar as the kernel reads an enum value; false for anything else.
func scalarText(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", false
		}
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(x), true
	}
	return "", false
}

// checkChoices refuses, before the kernel does, an enum value outside its options and a status that is not a stage,
// naming what would be accepted. Everything else is left to the kernel, whose sentences already say it.
func checkChoices(t *kernel.Type, in kernel.EntityInput) error {
	if in.Status != nil && len(t.Flow) > 0 {
		if _, ok := t.Stage(*in.Status); !ok {
			names := make([]string, 0, len(t.Flow))
			for _, st := range t.Flow {
				names = append(names, st.Name)
			}
			return fmt.Errorf("「%s」不是「%s」的阶段；阶段有：%s", *in.Status, t.Name, strings.Join(names, "、"))
		}
	}
	for _, key := range sortedKeys(in.Values) {
		p, ok := propByKey(t, key)
		if !ok || p.Kind != kernel.KindEnum {
			continue
		}
		s, ok := scalarText(in.Values[key])
		if !ok {
			continue
		}
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(p.Options, s) {
			return fmt.Errorf("属性「%s」只能是：%s", p.Name, strings.Join(p.Options, "、"))
		}
	}
	return nil
}

// sortedKeys are the keys of a value map in order, so that the first error is always the same one.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// relatedValues returns the values with every relation target given by its title replaced by its id. The map is a
// copy when anything changes; the shape of each value (one string or a list) is kept, so the kernel reads it as
// before.
func relatedValues(sp *kernel.Space, t *kernel.Type, values map[string]any) (map[string]any, error) {
	var out map[string]any
	for _, key := range sortedKeys(values) {
		p, ok := propByKey(t, key)
		if !ok || p.Kind != kernel.KindRelation {
			continue
		}
		v, err := targetIDs(sp, &p, values[key])
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = make(map[string]any, len(values))
			for k, x := range values {
				out[k] = x
			}
		}
		out[key] = v
	}
	if out == nil {
		return values, nil
	}
	return out, nil
}

// targetIDs resolves a relation operand: a string, or each string of a list, that is not the id of a record of the
// space is looked up as the exact title of a record of the property's target type. Other values are kept as they are.
func targetIDs(sp *kernel.Space, p *kernel.Prop, v any) (any, error) {
	switch x := v.(type) {
	case string:
		return targetID(sp, p, x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = item
			if s, ok := item.(string); ok {
				id, err := targetID(sp, p, s)
				if err != nil {
					return nil, err
				}
				out[i] = id
			}
		}
		return out, nil
	}
	return v, nil
}

// targetID is the id of one relation target given by its id or its title. An empty string is kept; the kernel reads it
// as nothing.
func targetID(sp *kernel.Space, p *kernel.Prop, s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || slices.ContainsFunc(sp.Entities, func(e *kernel.Entity) bool { return e != nil && e.ID == s }) {
		return s, nil
	}
	var ids []string
	for _, e := range sp.Entities {
		if e != nil && (p.Target == "" || e.Type == p.Target) && e.Title == s {
			ids = append(ids, e.ID)
		}
	}
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("属性「%s」：没有标题为「%s」的目标记录", p.Name, s)
	case 1:
		return ids[0], nil
	}
	return "", fmt.Errorf("属性「%s」：有 %d 条目标记录的标题是「%s」，请用编号：%s", p.Name, len(ids), s, strings.Join(ids, "、"))
}

// relatedConds returns the conditions with every relation operand given by a title replaced by its id, for typed and
// cross-type queries. Only "eq", "ne" and "has" with a non-empty string or a list of non-empty strings are read as titles;
// conditions on other fields, other operators and other operands (null, an empty string, a number) go to the kernel
// as they are, which accepts or refuses them by its own rules.
func relatedConds(sp *kernel.Space, t *kernel.Type, where []kernel.Cond) ([]kernel.Cond, error) {
	out := slices.Clone(where)
	for i := range out {
		c := &out[i]
		var p kernel.Prop
		ok := false
		if c.Field == "origin" {
			p = kernel.Prop{Key: "origin", Name: "源自", Kind: kernel.KindRelation}
			ok = true // built-in relation: the source may be any type, so p.Target stays empty
		} else if t != nil {
			p, ok = propByKey(t, c.Field)
		}
		if !ok || p.Kind != kernel.KindRelation || !titleOp(c.Op) || !titleOperand(c.Value) {
			continue
		}
		v, err := targetIDs(sp, &p, c.Value)
		if err != nil {
			return nil, fmt.Errorf("筛选条件：%w", err)
		}
		c.Value = v
	}
	return out, nil
}

// titleOp reports whether a condition's operator compares a relation with records, which may then be given by title.
func titleOp(op string) bool { return op == "eq" || op == "ne" || op == "has" }

// titleOperand reports whether v is a non-empty string or a non-empty list of non-empty strings.
func titleOperand(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) != ""
	case []any:
		for _, item := range x {
			if s, ok := item.(string); !ok || strings.TrimSpace(s) == "" {
				return false
			}
		}
		return len(x) > 0
	}
	return false
}
