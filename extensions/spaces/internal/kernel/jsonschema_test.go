package kernel

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"testing"
	"unicode/utf8"
)

// jsonOf round-trips a value through JSON, so that the validator sees what a tool would see.
func jsonOf(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// validate is a small structural JSON Schema validator: the keywords the generators use.
func validate(schema map[string]any, v any, path string) error {
	if alts, ok := schema["anyOf"].([]any); ok {
		for _, a := range alts {
			if validate(a.(map[string]any), v, path) == nil {
				return nil
			}
		}
		return fmt.Errorf("%s: no alternative matches", path)
	}
	if enum, ok := schema["enum"].([]string); ok {
		s, _ := v.(string)
		if !slices.Contains(enum, s) {
			return fmt.Errorf("%s: %v is not in the enum", path, v)
		}
	}
	typ, _ := schema["type"].(string)
	switch typ {
	case "string":
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%s: want a string", path)
		}
		if max, ok := schema["maxLength"].(int); ok && utf8.RuneCountInString(s) > max {
			return fmt.Errorf("%s: longer than %d", path, max)
		}
		if p, ok := schema["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(s) {
			return fmt.Errorf("%s: does not match %s", path, p)
		}
	case "number", "integer":
		f, ok := num(v)
		if !ok {
			f, ok = v.(float64)
		}
		if !ok {
			return fmt.Errorf("%s: want a number", path)
		}
		if typ == "integer" && f != float64(int64(f)) {
			return fmt.Errorf("%s: want an integer", path)
		}
		if min, ok := num(schema["minimum"]); ok && f < min {
			return fmt.Errorf("%s: below %v", path, min)
		}
		if max, ok := num(schema["maximum"]); ok && f > max {
			return fmt.Errorf("%s: above %v", path, max)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s: want a boolean", path)
		}
	case "null":
		if v != nil {
			return fmt.Errorf("%s: want null", path)
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s: want an array", path)
		}
		if max, ok := schema["maxItems"].(int); ok && len(arr) > max {
			return fmt.Errorf("%s: more than %d items", path, max)
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for i, x := range arr {
				if err := validate(items, x, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want an object", path)
		}
		props, _ := schema["properties"].(map[string]any)
		required, _ := schema["required"].([]string)
		for _, r := range required {
			if _, ok := m[r]; !ok {
				return fmt.Errorf("%s: %s is required", path, r)
			}
		}
		for _, key := range sortedKeysAny(m) {
			sub, ok := props[key].(map[string]any)
			if !ok {
				if schema["additionalProperties"] == false {
					return fmt.Errorf("%s: %s is not a property", path, key)
				}
				continue
			}
			if err := validate(sub, m[key], path+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

func sortedKeysAny(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// closedObjects reports every object schema under v that does not refuse unknown properties.
func closedObjects(v any, path string, bad *[]string) {
	switch x := v.(type) {
	case map[string]any:
		if x["type"] == "object" && x["additionalProperties"] != false {
			*bad = append(*bad, path)
		}
		for _, k := range sortedKeysAny(x) {
			closedObjects(x[k], path+"/"+k, bad)
		}
	case []any:
		for i, y := range x {
			closedObjects(y, fmt.Sprintf("%s[%d]", path, i), bad)
		}
	}
}

func TestSchemasAreClosedObjects(t *testing.T) {
	var bad []string
	closedObjects(TypeSchema(), "type", &bad)
	closedObjects(QuerySchema(), "query", &bad)
	closedObjects(ValuesSchema(sampleSpace().Types[1]), "values", &bad)
	if len(bad) != 0 {
		t.Errorf("object schemas that accept any property: %v", bad)
	}
}

func TestTypeSchemaAcceptsEveryKernelType(t *testing.T) {
	schema := TypeSchema()
	s := sampleSpace()
	for _, typ := range s.Types {
		if err := validate(schema, jsonOf(t, typ), "type"); err != nil {
			t.Errorf("%s: %v", typ.Key, err)
		}
	}
}

func TestTypeSchemaRefusesBadTypes(t *testing.T) {
	schema := TypeSchema()
	cases := []struct {
		name string
		v    map[string]any
	}{
		{"unknown property", map[string]any{"key": "a", "name": "a", "bogus": 1}},
		{"bad key", map[string]any{"key": "Bad", "name": "a"}},
		{"unknown kind", map[string]any{"key": "a", "name": "a", "props": []any{
			map[string]any{"key": "x", "name": "x", "kind": "color"}}}},
		{"unknown phase", map[string]any{"key": "a", "name": "a", "flow": []any{
			map[string]any{"name": "x", "phase": "later"}}}},
		{"too many stages", map[string]any{"key": "a", "name": "a", "flow": make9()}},
		{"name too long", map[string]any{"key": "a", "name": "字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字字"}},
		{"unknown aggregate", map[string]any{"key": "a", "name": "a", "props": []any{
			map[string]any{"key": "x", "name": "x", "kind": "rollup", "op": "median"}}}},
	}
	for _, c := range cases {
		if err := validate(schema, c.v, "type"); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
}

// make9 is nine stages, one more than a flow may have.
func make9() []any {
	out := make([]any, 0, 9)
	for i := 0; i < 9; i++ {
		out = append(out, map[string]any{"name": fmt.Sprintf("阶段%d", i), "phase": "todo"})
	}
	return out
}

func TestQuerySchemaAcceptsQueries(t *testing.T) {
	schema := QuerySchema()
	queries := []Query{
		{},
		{Type: "task", Text: "一", Lens: Lens{
			Where: []Cond{{Field: "effort", Op: "lt", Value: 3}, {Field: "status", Op: "empty"}},
			Sort:  []Order{{By: "updated", Desc: true}},
			Group: "status",
			Agg:   []Agg{{Op: "sum", Of: "effort"}, {Op: "count"}},
		}, Limit: 20, Offset: 5},
	}
	for _, q := range queries {
		if err := validate(schema, jsonOf(t, q), "query"); err != nil {
			t.Errorf("%+v: %v", q, err)
		}
	}
	for _, bad := range []map[string]any{
		{"where": []any{map[string]any{"field": "x", "op": "like"}}},
		{"limit": 500},
		{"limit": 0},
		{"offset": -1},
		{"agg": []any{map[string]any{"op": "median"}}},
		{"unknown": true},
	} {
		if err := validate(schema, bad, "query"); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestSchemaEnumsAreTheKernelLists(t *testing.T) {
	props := func(s map[string]any) map[string]any { return s["properties"].(map[string]any) }
	typeProps := props(TypeSchema())
	item := typeProps["props"].(map[string]any)["items"].(map[string]any)
	if got := props(item)["kind"].(map[string]any)["enum"]; !reflect.DeepEqual(got, kindNames()) {
		t.Errorf("kind enum = %v", got)
	}
	if got := props(item)["op"].(map[string]any)["enum"]; !reflect.DeepEqual(got, aggNames()) {
		t.Errorf("rollup op enum = %v", got)
	}
	view := typeProps["views"].(map[string]any)["items"].(map[string]any)
	if got := props(view)["kind"].(map[string]any)["enum"]; !reflect.DeepEqual(got, viewKinds) {
		t.Errorf("view kind enum = %v", got)
	}
	flow := typeProps["flow"].(map[string]any)["items"].(map[string]any)
	if got := props(flow)["phase"].(map[string]any)["enum"]; !reflect.DeepEqual(got, phaseNames()) {
		t.Errorf("phase enum = %v", got)
	}
	q := QuerySchema()["properties"].(map[string]any)
	where := q["where"].(map[string]any)["items"].(map[string]any)
	if got := props(where)["op"].(map[string]any)["enum"]; !reflect.DeepEqual(got, opNames) {
		t.Errorf("operator enum = %v", got)
	}
	if got := q["limit"].(map[string]any)["maximum"]; got != MaxLimit {
		t.Errorf("limit maximum = %v", got)
	}
}

func TestValuesSchema(t *testing.T) {
	s := sampleBase(t).Spaces[0] // checked, so the values are canonical
	for _, typ := range s.Types {
		schema := ValuesSchema(typ)
		for _, e := range s.Entities {
			if e.Type != typ.Key {
				continue
			}
			values := e.Values
			if values == nil {
				values = map[string]any{}
			}
			if err := validate(schema, jsonOf(t, values), typ.Key); err != nil {
				t.Errorf("entity %s: %v", e.ID, err)
			}
		}
	}

	goal := s.Types[0]
	schema := ValuesSchema(goal)
	props := schema["properties"].(map[string]any)
	if _, ok := props["tasks"]; ok {
		t.Errorf("a computed rollup has a stored value schema")
	}
	if _, ok := props["summary"]; !ok {
		t.Errorf("a stored text has no schema")
	}
	bad := []map[string]any{
		{"summary": 3.0},
		{"area": "乱"},
		{"picks": []any{1.0}},
		{"nope": "x"},
	}
	for _, v := range bad {
		if err := validate(schema, v, "goal"); err == nil {
			t.Errorf("accepted %v", v)
		}
	}
	if err := validate(schema, map[string]any{"summary": nil}, "goal"); err != nil {
		t.Errorf("null refused: %v", err)
	}

	if got := ValuesSchema(nil); len(got["properties"].(map[string]any)) != 0 {
		t.Errorf("ValuesSchema(nil) has properties")
	}
}

func TestVocabulary(t *testing.T) {
	v := Vocabulary()
	kinds := v["kinds"].([]map[string]any)
	if len(kinds) != len(Kinds) || kinds[0]["name"] != KindText || kinds[0]["label"] != "文本" {
		t.Errorf("kinds = %v", kinds[0])
	}
	if got := kinds[4]["options"].([]string); !reflect.DeepEqual(got, []string{"options"}) {
		t.Errorf("enum takes options %v", got)
	}
	if got := v["view_kinds"]; !reflect.DeepEqual(got, viewKinds) {
		t.Errorf("view kinds = %v", got)
	}
	if got := v["phases"]; !reflect.DeepEqual(got, phaseNames()) {
		t.Errorf("phases = %v", got)
	}
	if got := v["ops"]; !reflect.DeepEqual(got, opNames) {
		t.Errorf("ops = %v", got)
	}
	aggs := v["aggregates"].([]map[string]any)
	if len(aggs) != len(Aggregates) || aggs[1]["name"] != "sum" || aggs[1]["operand"] != true {
		t.Errorf("aggregates = %v", aggs)
	}
}
