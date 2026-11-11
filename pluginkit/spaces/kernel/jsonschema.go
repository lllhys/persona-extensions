package kernel

import (
	"slices"
	"strings"
)

// keyPattern is the shape of a type or property key, as Check enforces it.
const keyPattern = "^[a-z][a-z0-9_]{0,31}$"

// object is a JSON Schema object that refuses every property it does not list.
func object(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "additionalProperties": false, "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

// boundedArray is a JSON Schema array of at most max items.
func boundedArray(max int, items map[string]any) map[string]any {
	return map[string]any{"type": "array", "maxItems": max, "items": items}
}

// choice is a JSON Schema string restricted to the given values. The enum row builds it.
func choice(vals []string, desc string) map[string]any {
	k, _ := KindOf(KindEnum)
	m := k.Schema(&Prop{Options: slices.Clone(vals)})
	if desc != "" {
		m["description"] = desc
	}
	return m
}

// booleanSchema is the JSON Schema of a boolean, as the boolean row gives it.
func booleanSchema(desc string) map[string]any {
	k, _ := KindOf(KindBoolean)
	m := k.Schema(&Prop{})
	if desc != "" {
		m["description"] = desc
	}
	return m
}

// textOf is a JSON Schema string of at most max runes.
func textOf(max int, desc string) map[string]any {
	m := map[string]any{"type": "string", "maxLength": max}
	if desc != "" {
		m["description"] = desc
	}
	return m
}

// kindNames are the property kinds, in table order.
func kindNames() []string {
	out := make([]string, 0, len(Kinds))
	for _, k := range Kinds {
		out = append(out, k.Name)
	}
	return out
}

// kindLabels is a Chinese list of the property kinds, for descriptions.
func kindLabels() string {
	labels := make([]string, 0, len(Kinds))
	for _, k := range Kinds {
		labels = append(labels, k.Label)
	}
	return strings.Join(labels, "、")
}

// aggNames are the aggregates, in table order.
func aggNames() []string {
	out := make([]string, 0, len(Aggregates))
	for _, a := range Aggregates {
		out = append(out, a.Name)
	}
	return out
}

// phaseNames are the phases, in their order.
func phaseNames() []string {
	return []string{string(Todo), string(Doing), string(Done), string(Dropped)}
}

// condSchema is the schema of one condition of a Lens or a Query.
func condSchema() map[string]any {
	return object(map[string]any{
		"field": textOf(MaxName, ""),
		"op":    choice(opNames, "运算：eq 等于、ne 不等于、lt/le/gt/ge 比较、has 包含、empty 是否未设置"),
		"value": map[string]any{},
	}, "field", "op")
}

// orderSchema is the schema of one sort key.
func orderSchema() map[string]any {
	return object(map[string]any{
		"by":   textOf(MaxName, "属性或内置字段"),
		"desc": booleanSchema(""),
	}, "by")
}

// aggSchema is the schema of one aggregate.
func aggSchema() map[string]any {
	return object(map[string]any{
		"op": choice(aggNames(), "汇总方式"),
		"of": textOf(MaxName, "数字属性；只有求和、平均、最小、最大需要"),
	}, "op")
}

// propSchema is the schema of one property definition.
func propSchema() map[string]any {
	return object(map[string]any{
		"key":     map[string]any{"type": "string", "pattern": keyPattern},
		"name":    textOf(MaxName, ""),
		"kind":    choice(kindNames(), "属性种类："+kindLabels()),
		"unit":    textOf(MaxShort, "数字和汇总的单位"),
		"options": boundedArray(MaxOptions, textOf(MaxName, "选项")),
		"target":  map[string]any{"type": "string", "description": "关联的类型标识"},
		"many":    booleanSchema("关联可以指向多条记录"),
		"back":    textOf(MaxShort, "反向名称"),
		"via":     map[string]any{"type": "string", "description": "汇总的来源：本类型的关联名，或 类型.关联名（那个类型指向这里的关联）"},
		"op":      choice(aggNames(), "汇总方式"),
		"of":      map[string]any{"type": "string", "description": "汇总的数字属性"},
	}, "key", "name", "kind")
}

// TypeSchema is the JSON Schema of a Type, for a tool that writes one.
func TypeSchema() map[string]any {
	return object(map[string]any{
		"key":  map[string]any{"type": "string", "pattern": keyPattern},
		"name": textOf(MaxName, ""),
		"icon": map[string]any{"type": "string"},
		"flow": boundedArray(MaxStages, object(map[string]any{
			"name":  textOf(MaxShort, ""),
			"phase": choice(phaseNames(), "阶段"),
		}, "name", "phase")),
		"props": boundedArray(MaxProps, propSchema()),
		"card": object(map[string]any{
			"lead":  map[string]any{"type": "string"},
			"pills": boundedArray(MaxPills, map[string]any{"type": "string"}),
			"progress": object(map[string]any{
				"of":   map[string]any{"type": "string"},
				"over": map[string]any{"type": "string"},
			}, "of"),
		}),
		"views": boundedArray(MaxViews, object(map[string]any{
			"name":  textOf(MaxShort, ""),
			"kind":  choice(viewKinds, "视图种类"),
			"where": boundedArray(MaxWhere, condSchema()),
			"sort":  boundedArray(MaxSort, orderSchema()),
			"group": map[string]any{"type": "string"},
			"agg":   boundedArray(MaxAgg, aggSchema()),
		}, "name", "kind")),
	}, "key", "name")
}

// QuerySchema is the JSON Schema of the query parts of a Query. A tool adds its own fields (space, id) to it.
func QuerySchema() map[string]any {
	return object(map[string]any{
		"type":   map[string]any{"type": "string", "description": "类型标识；不填则跨类型，只能用内置字段"},
		"text":   map[string]any{"type": "string"},
		"where":  boundedArray(MaxWhere, condSchema()),
		"sort":   boundedArray(MaxSort, orderSchema()),
		"group":  map[string]any{"type": "string"},
		"agg":    boundedArray(MaxAgg, aggSchema()),
		"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": MaxLimit},
		"offset": map[string]any{"type": "integer", "minimum": 0},
	})
}

// ValuesSchema is the JSON Schema of the values of one entity of a type: every stored property, each by its kind's
// schema. A null value unsets the property, so null is allowed. Computed properties are not stored and are left out.
func ValuesSchema(t *Type) map[string]any {
	props := map[string]any{}
	if t == nil {
		return object(props)
	}
	for i := range t.Props {
		p := &t.Props[i]
		k, ok := KindOf(p.Kind)
		if !ok || k.Eval != nil {
			continue
		}
		s := k.Schema(p)
		s["title"] = p.Name
		if k.Hint != "" {
			s["description"] = k.Hint
		}
		props[p.Key] = map[string]any{"anyOf": []any{s, map[string]any{"type": "null"}}}
	}
	return object(props)
}

// Vocabulary lists the words a tool may use: the property kinds with their label, hint and options, the view kinds,
// the phases, the operators and the aggregates. Template keys are added by the caller, which knows the templates.
func Vocabulary() map[string]any {
	kinds := make([]map[string]any, 0, len(Kinds))
	for _, k := range Kinds {
		kinds = append(kinds, map[string]any{
			"name":    k.Name,
			"label":   k.Label,
			"hint":    k.Hint,
			"icon":    k.Icon,
			"input":   string(k.Input),
			"options": append([]string{}, k.Opts...),
		})
	}
	aggs := make([]map[string]any, 0, len(Aggregates))
	for _, a := range Aggregates {
		aggs = append(aggs, map[string]any{"name": a.Name, "label": a.Label, "operand": a.Operand})
	}
	return map[string]any{
		"kinds":      kinds,
		"view_kinds": slices.Clone(viewKinds),
		"phases":     phaseNames(),
		"ops":        slices.Clone(opNames),
		"aggregates": aggs,
	}
}
