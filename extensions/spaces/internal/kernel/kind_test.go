package kernel

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestKindTable(t *testing.T) {
	want := []string{KindText, KindNumber, KindBoolean, KindDate, KindEnum, KindURL, KindRelation, KindRollup}
	if len(Kinds) != len(want) {
		t.Fatalf("%d rows, want %d", len(Kinds), len(want))
	}
	inputs := map[Input]bool{InLine: true, InNumber: true, InToggle: true, InChoice: true, InPick: true, InNone: true}
	for i, k := range Kinds {
		if k.Name != want[i] {
			t.Errorf("row %d is %q, want %q", i, k.Name, want[i])
		}
		if k.Label == "" || k.Icon == "" {
			t.Errorf("%s: label or icon is empty", k.Name)
		}
		if !inputs[k.Input] {
			t.Errorf("%s: unknown input %q", k.Name, k.Input)
		}
		if k.Check == nil || k.Parse == nil || k.Text == nil || k.Cmp == nil || k.Schema == nil {
			t.Errorf("%s: a required function is nil", k.Name)
		}
		computed := k.Name == KindRollup
		if computed != (k.Eval != nil) {
			t.Errorf("%s: Eval set = %v, want %v (only rollups are computed)", k.Name, k.Eval != nil, computed)
		}
		if !computed && k.Name != KindNumber && k.Name != KindRelation && k.Name != KindBoolean && k.Num != nil {
			t.Errorf("%s: unexpected numeric reading", k.Name)
		}
		if k.Refs != nil && k.Name != KindRelation {
			t.Errorf("%s: unexpected Refs", k.Name)
		}
		if k.Enum != nil && k.Name != KindEnum {
			t.Errorf("%s: unexpected Enum", k.Name)
		}
	}
	for _, name := range want {
		if k, ok := KindOf(name); !ok || k.Name != name {
			t.Errorf("KindOf(%q) = %v, %v", name, k, ok)
		}
	}
	if _, ok := KindOf("color"); ok {
		t.Errorf("KindOf accepted an unknown kind")
	}
}

func TestParseAccepts(t *testing.T) {
	tests := []struct {
		name string
		kind string
		prop Prop
		raw  any
		want any
	}{
		{"text trims", KindText, Prop{Name: "n"}, "  hi  ", "hi"},
		{"text blank is unset", KindText, Prop{Name: "n"}, "   ", nil},
		{"text nil is unset", KindText, Prop{Name: "n"}, nil, nil},
		{"text from number", KindText, Prop{Name: "n"}, 12.5, "12.5"},
		{"text from bool", KindText, Prop{Name: "n"}, true, "true"},
		{"text from int", KindText, Prop{Name: "n"}, 7, "7"},
		{"number from float", KindNumber, Prop{Name: "n"}, 12.0, 12.0},
		{"number from string", KindNumber, Prop{Name: "n"}, " 3.5 ", 3.5},
		{"number from int", KindNumber, Prop{Name: "n"}, 4, 4.0},
		{"number from int64", KindNumber, Prop{Name: "n"}, int64(9), 9.0},
		{"number negative zero", KindNumber, Prop{Name: "n"}, math.Copysign(0, -1), 0.0},
		{"number blank is unset", KindNumber, Prop{Name: "n"}, "", nil},
		{"number nil is unset", KindNumber, Prop{Name: "n"}, nil, nil},
		{"boolean", KindBoolean, Prop{Name: "n"}, true, true},
		{"boolean from string", KindBoolean, Prop{Name: "n"}, " false ", false},
		{"boolean blank is unset", KindBoolean, Prop{Name: "n"}, "", nil},
		{"boolean nil is unset", KindBoolean, Prop{Name: "n"}, nil, nil},
		{"date", KindDate, Prop{Name: "n"}, "2026-11-10", "2026-11-10"},
		{"date trims", KindDate, Prop{Name: "n"}, " 2026-11-10 ", "2026-11-10"},
		{"date blank is unset", KindDate, Prop{Name: "n"}, " ", nil},
		{"enum", KindEnum, Prop{Name: "n", Options: []string{"工作", "生活"}}, " 生活 ", "生活"},
		{"enum blank is unset", KindEnum, Prop{Name: "n", Options: []string{"a"}}, "", nil},
		{"url", KindURL, Prop{Name: "n"}, "https://example.com/a", "https://example.com/a"},
		{"url http", KindURL, Prop{Name: "n"}, "http://example.com", "http://example.com"},
		{"url blank is unset", KindURL, Prop{Name: "n"}, " ", nil},
		{"relation one", KindRelation, Prop{Name: "n"}, "t1", []string{"t1"}},
		{"relation many, distinct", KindRelation, Prop{Name: "n", Many: true}, []any{"t1", " t2 ", "t1", ""}, []string{"t1", "t2"}},
		{"relation from string slice", KindRelation, Prop{Name: "n", Many: true}, []string{" a "}, []string{"a"}},
		{"relation empty list is unset", KindRelation, Prop{Name: "n", Many: true}, []any{}, nil},
		{"relation blank is unset", KindRelation, Prop{Name: "n"}, "", nil},
		{"relation nil is unset", KindRelation, Prop{Name: "n"}, nil, nil},
		{"rollup is a number", KindRollup, Prop{Name: "n"}, "2", 2.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, _ := KindOf(tt.kind)
			p := tt.prop
			p.Kind = tt.kind
			got, err := k.Parse(&p, tt.raw)
			if err != nil {
				t.Fatalf("Parse(%#v) error: %v", tt.raw, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%#v) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseRefuses(t *testing.T) {
	tests := []struct {
		name string
		kind string
		prop Prop
		raw  any
	}{
		{"text of a list", KindText, Prop{Name: "n"}, []any{"x"}},
		{"text over the limit", KindText, Prop{Name: "n"}, strings.Repeat("字", MaxText+1)},
		{"text of NaN", KindText, Prop{Name: "n"}, math.NaN()},
		{"text of a map", KindText, Prop{Name: "n"}, map[string]any{}},
		{"number of a word", KindNumber, Prop{Name: "n"}, "很多"},
		{"number of NaN text", KindNumber, Prop{Name: "n"}, "NaN"},
		{"number of infinity", KindNumber, Prop{Name: "n"}, "Inf"},
		{"number of overflow", KindNumber, Prop{Name: "n"}, "1e400"},
		{"number of a bool", KindNumber, Prop{Name: "n"}, true},
		{"number of a list", KindNumber, Prop{Name: "n"}, []any{1.0}},
		{"number infinite float", KindNumber, Prop{Name: "n"}, math.Inf(1)},
		{"boolean of a word", KindBoolean, Prop{Name: "n"}, "yes"},
		{"boolean of a number", KindBoolean, Prop{Name: "n"}, 1.0},
		{"date of a number", KindDate, Prop{Name: "n"}, 2026.0},
		{"date not in the format", KindDate, Prop{Name: "n"}, "2026-1-5"},
		{"date that does not exist", KindDate, Prop{Name: "n"}, "2026-02-30"},
		{"enum not an option", KindEnum, Prop{Name: "n", Options: []string{"工作"}}, "别的"},
		{"enum of a list", KindEnum, Prop{Name: "n", Options: []string{"工作"}}, []any{"工作"}},
		{"url without scheme", KindURL, Prop{Name: "n"}, "example.com"},
		{"url with another scheme", KindURL, Prop{Name: "n"}, "ftp://example.com"},
		{"url without host", KindURL, Prop{Name: "n"}, "https://"},
		{"url unparseable", KindURL, Prop{Name: "n"}, "https://[::1"},
		{"url over the limit", KindURL, Prop{Name: "n"}, "https://example.com/" + strings.Repeat("字", MaxText)},
		{"url of a number", KindURL, Prop{Name: "n"}, 3.0},
		{"relation of a number", KindRelation, Prop{Name: "n"}, 3.0},
		{"relation of a map", KindRelation, Prop{Name: "n", Many: true}, []any{map[string]any{}}},
		{"relation of a nested list", KindRelation, Prop{Name: "n", Many: true}, []any{[]any{"x"}}},
		{"relation of several to one", KindRelation, Prop{Name: "n"}, []any{"t1", "t2"}},
		{"relation over the limit", KindRelation, Prop{Name: "n", Many: true}, manyIDs(MaxTargets + 1)},
		{"number of a struct", KindNumber, Prop{Name: "n"}, struct{}{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, _ := KindOf(tt.kind)
			p := tt.prop
			p.Kind = tt.kind
			if got, err := k.Parse(&p, tt.raw); err == nil {
				t.Fatalf("Parse(%#v) accepted it as %#v", tt.raw, got)
			}
		})
	}
}

func manyIDs(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = "t" + strconv.Itoa(i)
	}
	return out
}

// TestParseIdempotent: Parse(Parse(x)) == Parse(x) for every kind on a spread of inputs.
func TestParseIdempotent(t *testing.T) {
	inputs := []any{nil, "", " 工作 ", "12.5", "  true ", "2026-11-10", "https://example.com/a", "t1", []any{"t1", "t2"}, 3.0, true}
	for _, k := range Kinds {
		p := &Prop{Key: "p", Name: "p", Kind: k.Name, Options: []string{"工作"}, Many: true, Target: "task"}
		for _, in := range inputs {
			v, err := k.Parse(p, in)
			if err != nil {
				continue
			}
			again, err := k.Parse(p, v)
			if err != nil || !reflect.DeepEqual(again, v) {
				t.Errorf("%s: Parse(%#v) = %#v, then %#v (%v): not idempotent", k.Name, in, v, again, err)
			}
		}
	}
}

// TestTextRoundTrip: for the kinds whose text is their value, Parse(Text(v)) gives v back.
func TestTextRoundTrip(t *testing.T) {
	cases := []struct {
		kind string
		prop Prop
		v    any
	}{
		{KindText, Prop{Name: "n"}, "一段文字"},
		{KindNumber, Prop{Name: "n"}, 0.25},
		{KindNumber, Prop{Name: "n"}, 320.0},
		{KindDate, Prop{Name: "n"}, "2026-11-10"},
		{KindEnum, Prop{Name: "n", Options: []string{"工作"}}, "工作"},
	}
	for _, c := range cases {
		k, _ := KindOf(c.kind)
		p := c.prop
		p.Kind = c.kind
		text := k.Text(&p, c.v, nil)
		back, err := k.Parse(&p, text)
		if err != nil || !reflect.DeepEqual(back, c.v) {
			t.Errorf("%s: %#v -> %q -> %#v (%v)", c.kind, c.v, text, back, err)
		}
	}
}

func TestDisplayText(t *testing.T) {
	names := func(id string) string {
		return map[string]string{"t1": "任务一", "t2": "任务二"}[id]
	}
	tests := []struct {
		name string
		kind string
		prop Prop
		v    any
		want string
	}{
		{"text", KindText, Prop{}, "x", "x"},
		{"number with unit", KindNumber, Prop{Unit: "页"}, 320.0, "320 页"},
		{"number without unit", KindNumber, Prop{}, 0.5, "0.5"},
		{"boolean yes", KindBoolean, Prop{}, true, "是"},
		{"boolean no", KindBoolean, Prop{}, false, "否"},
		{"date of a timestamp", KindDate, Prop{}, "2026-11-10T09:00:00Z", "2026-11-10"},
		{"date", KindDate, Prop{}, "2026-11-10", "2026-11-10"},
		{"enum", KindEnum, Prop{}, "工作", "工作"},
		{"url host and path", KindURL, Prop{}, "https://example.com/a/b", "example.com/a/b"},
		{"url root", KindURL, Prop{}, "https://example.com/", "example.com"},
		{"url unparseable", KindURL, Prop{}, "https://[::1", "https://[::1"},
		{"relation names", KindRelation, Prop{}, []string{"t1", "t2"}, "任务一、任务二"},
		{"relation skips dangling", KindRelation, Prop{}, []string{"t1", "gone"}, "任务一"},
		{"relation of nothing", KindRelation, Prop{}, nil, ""},
		{"rollup number", KindRollup, Prop{Op: "sum", Unit: "小时"}, 10.0, "10 小时"},
		{"rollup percent", KindRollup, Prop{Op: "percent_done"}, 0.5, "50%"},
		{"rollup percent rounds", KindRollup, Prop{Op: "percent_done"}, 1.0 / 3, "33%"},
		{"rollup of nothing", KindRollup, Prop{Op: "sum"}, "x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, _ := KindOf(tt.kind)
			p := tt.prop
			p.Kind = tt.kind
			if got := k.Text(&p, tt.v, names); got != tt.want {
				t.Errorf("Text = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCompareNumericAndNilFirst(t *testing.T) {
	tests := []struct {
		kind string
		a, b any
		want int
	}{
		{KindText, "a", "b", -1},
		{KindText, "b", "a", 1},
		{KindText, "a", "a", 0},
		{KindText, nil, "a", -1},
		{KindText, "a", nil, 1},
		{KindText, nil, nil, 0},
		{KindNumber, 1.0, 2.0, -1},
		{KindRollup, 2.0, 1.0, 1},
		{KindBoolean, false, true, -1},
		{KindBoolean, true, false, 1},
		{KindBoolean, true, true, 0},
		{KindBoolean, false, false, 0},
		{KindBoolean, nil, false, -1},
		{KindBoolean, true, nil, 1},
		{KindBoolean, nil, nil, 0},
		{KindDate, "2026-01-02", "2026-01-10", -1},
		{KindEnum, "b", "a", 1},
		{KindURL, "https://a", "https://b", -1},
		{KindRelation, []string{"a"}, []string{"a", "b"}, -1},
		{KindRelation, []string{"b"}, []string{"a", "z"}, 1},
		{KindRelation, nil, []string{"a"}, -1},
	}
	for _, tt := range tests {
		k, _ := KindOf(tt.kind)
		if got := k.Cmp(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: Cmp(%#v, %#v) = %d, want %d", tt.kind, tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNumericReadingsRefsAndEnum(t *testing.T) {
	num := func(kind string, v any) (float64, bool) {
		k, _ := KindOf(kind)
		if k.Num == nil {
			return 0, false
		}
		return k.Num(v)
	}
	tests := []struct {
		kind string
		v    any
		want float64
		ok   bool
	}{
		{KindNumber, 2.5, 2.5, true},
		{KindNumber, "2.5", 0, false},
		{KindNumber, nil, 0, false},
		{KindBoolean, true, 1, true},
		{KindBoolean, false, 0, true},
		{KindBoolean, "true", 0, false},
		{KindRelation, []string{"a", "b"}, 2, true},
		{KindRelation, "a", 0, false},
		{KindRollup, 0.25, 0.25, true},
		{KindRollup, nil, 0, false},
		{KindText, "1", 0, false},
		{KindDate, "2026-01-01", 0, false},
		{KindEnum, "1", 0, false},
		{KindURL, "https://x", 0, false},
	}
	for _, tt := range tests {
		got, ok := num(tt.kind, tt.v)
		if got != tt.want || ok != tt.ok {
			t.Errorf("%s Num(%#v) = %v, %v; want %v, %v", tt.kind, tt.v, got, ok, tt.want, tt.ok)
		}
	}
	rel, _ := KindOf(KindRelation)
	if got := rel.Refs([]string{"a"}); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("relation Refs = %v", got)
	}
	if got := rel.Refs(nil); got != nil {
		t.Errorf("relation Refs of nothing = %v", got)
	}
	enum, _ := KindOf(KindEnum)
	p := &Prop{Options: []string{"b", "a"}}
	got := enum.Enum(p)
	if !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Errorf("enum values = %v, want the options in order", got)
	}
	got[0] = "changed"
	if p.Options[0] != "b" {
		t.Errorf("enum values share the option slice")
	}
	if enum.Enum(&Prop{}) != nil {
		t.Errorf("an enum without options is open")
	}
	text, _ := KindOf(KindText)
	if text.Enum != nil {
		t.Errorf("text is not enumerable")
	}
}

func TestSchemasAreWellFormed(t *testing.T) {
	for _, k := range Kinds {
		p := &Prop{Name: "n", Options: []string{"工作", "生活"}, Many: true}
		s := k.Schema(p)
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("%s: %v", k.Name, err)
		}
		var back map[string]any
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("%s: %v", k.Name, err)
		}
		if _, ok := back["type"]; !ok {
			t.Errorf("%s: schema has no type: %s", k.Name, raw)
		}
	}
	enum, _ := KindOf(KindEnum)
	if got := enum.Schema(&Prop{Options: []string{"a"}})["enum"]; !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("enum schema = %v", got)
	}
	rel, _ := KindOf(KindRelation)
	if got := rel.Schema(&Prop{})["maxItems"]; got != 1 {
		t.Errorf("single relation maxItems = %v, want 1", got)
	}
	if got := rel.Schema(&Prop{Many: true})["maxItems"]; got != MaxTargets {
		t.Errorf("many relation maxItems = %v, want %d", got, MaxTargets)
	}
}

func TestRowChecks(t *testing.T) {
	s := sampleSpace()
	goal, task := s.Types[0], s.Types[1]
	tests := []struct {
		name string
		kind string
		prop Prop
		t    *Type
		ok   bool
	}{
		{"enum with options", KindEnum, Prop{Name: "n", Options: []string{"a", "b"}}, goal, true},
		{"enum without options", KindEnum, Prop{Name: "n"}, goal, false},
		{"enum over the option limit", KindEnum, Prop{Name: "n", Options: manyOptions(MaxOptions + 1)}, goal, false},
		{"enum with an empty option", KindEnum, Prop{Name: "n", Options: []string{""}}, goal, false},
		{"enum with padded option", KindEnum, Prop{Name: "n", Options: []string{" a"}}, goal, false},
		{"enum with a long option", KindEnum, Prop{Name: "n", Options: []string{strings.Repeat("字", MaxName+1)}}, goal, false},
		{"enum with a repeated option", KindEnum, Prop{Name: "n", Options: []string{"a", "a"}}, goal, false},
		{"number with unit", KindNumber, Prop{Name: "n", Unit: "页"}, goal, true},
		{"number with a long unit", KindNumber, Prop{Name: "n", Unit: strings.Repeat("字", MaxShort+1)}, goal, false},
		{"relation to a type", KindRelation, Prop{Name: "n", Target: "task", Back: "目标"}, goal, true},
		{"relation without target", KindRelation, Prop{Name: "n"}, goal, false},
		{"relation to a missing type", KindRelation, Prop{Name: "n", Target: "nope"}, goal, false},
		{"relation with a long back name", KindRelation, Prop{Name: "n", Target: "task", Back: strings.Repeat("字", MaxShort+1)}, goal, false},
		{"text is always fine", KindText, Prop{Name: "n"}, goal, true},
		{"boolean is always fine", KindBoolean, Prop{Name: "n"}, goal, true},
		{"date is always fine", KindDate, Prop{Name: "n"}, goal, true},
		{"url is always fine", KindURL, Prop{Name: "n"}, goal, true},
		{"count of tasks", KindRollup, Prop{Name: "n", Via: "task.goal", Op: "count"}, goal, true},
		{"count of tasks by own relation", KindRollup, Prop{Name: "n", Via: "picks", Op: "count"}, goal, true},
		{"unknown aggregate", KindRollup, Prop{Name: "n", Via: "task.goal", Op: "median"}, goal, false},
		{"count with an operand", KindRollup, Prop{Name: "n", Via: "task.goal", Op: "count", Of: "effort"}, goal, false},
		{"sum without an operand", KindRollup, Prop{Name: "n", Via: "task.goal", Op: "sum"}, goal, false},
		{"sum of a number", KindRollup, Prop{Name: "n", Via: "task.goal", Op: "sum", Of: "effort"}, goal, true},
		{"sum of a relation counts", KindRollup, Prop{Name: "n", Via: "task.goal", Op: "sum", Of: "goal"}, goal, true},
		{"sum of a missing property", KindRollup, Prop{Name: "n", Via: "task.goal", Op: "sum", Of: "nope"}, goal, false},
		{"sum of text", KindRollup, Prop{Name: "n", Via: "task.goal", Op: "sum", Of: "title"}, goal, false},
		{"sum of a property the related type lacks", KindRollup, Prop{Name: "n", Via: "task.goal", Op: "sum", Of: "tasks"}, goal, false},
		{"via a property that is not a relation", KindRollup, Prop{Name: "n", Via: "summary", Op: "count"}, goal, false},
		{"via a rollup", KindRollup, Prop{Name: "n", Via: "tasks", Op: "count"}, goal, false},
		{"via a missing property", KindRollup, Prop{Name: "n", Via: "nope", Op: "count"}, goal, false},
		{"via origin, which has no target", KindRollup, Prop{Name: "n", Via: "origin", Op: "count"}, task, false},
		{"via a missing type", KindRollup, Prop{Name: "n", Via: "nope.goal", Op: "count"}, goal, false},
		{"backlinks of a missing property", KindRollup, Prop{Name: "n", Via: "task.nope", Op: "count"}, goal, false},
		{"backlinks of a relation to another type", KindRollup, Prop{Name: "n", Via: "note.about", Op: "count"}, task, false},
		{"backlinks that point at the type", KindRollup, Prop{Name: "n", Via: "task.blocked_by", Op: "count"}, task, true},
		{"rollup with a long unit", KindRollup, Prop{Name: "n", Via: "task.goal", Op: "count", Unit: strings.Repeat("字", MaxShort+1)}, goal, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, _ := KindOf(tt.kind)
			p := tt.prop
			p.Kind = tt.kind
			err := k.Check(&p, tt.t, s)
			if (err == nil) != tt.ok {
				t.Fatalf("Check = %v, want ok = %v", err, tt.ok)
			}
		})
	}
}

// TestRollupOfRollupRefused: a rollup may not read a rollup, so evaluation terminates in one step.
func TestRollupOfRollupRefused(t *testing.T) {
	s := &Space{ID: "s", Name: "s", Types: []*Type{
		{Key: "a", Name: "a", Props: []Prop{
			{Key: "bs", Name: "bs", Kind: KindRelation, Target: "b", Many: true},
			{Key: "x", Name: "x", Kind: KindRollup, Via: "b.a", Op: "sum", Of: "r"},
		}},
		{Key: "b", Name: "b", Props: []Prop{
			{Key: "a", Name: "a", Kind: KindRelation, Target: "a"},
			{Key: "r", Name: "r", Kind: KindRollup, Via: "a", Op: "count"},
		}},
	}}
	k, _ := KindOf(KindRollup)
	p := s.Types[0].Props[1]
	if err := k.Check(&p, s.Types[0], s); err == nil {
		t.Fatalf("a rollup over a rollup was accepted")
	}
	s.Types[1].Props[1].Kind = KindNumber
	s.Types[1].Props[1].Via = ""
	s.Types[1].Props[1].Op = ""
	if err := k.Check(&p, s.Types[0], s); err != nil {
		t.Fatalf("a rollup over a stored number is refused: %v", err)
	}
}

func manyOptions(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "o" + strconv.Itoa(i)
	}
	return out
}

func TestRollupEvalIsNilWhenUndefined(t *testing.T) {
	k, _ := KindOf(KindRollup)
	b := sampleBase(t)
	sp := b.Spaces[0]
	w := Open(sp)
	p := sp.Types[0].Props[5] // progress, percent_done over task.goal
	if got := k.Eval(w, w.Entity("g2"), &p); got != nil {
		t.Errorf("undefined percent_done = %#v, want nil", got)
	}
}

func TestKindRowsAreSorted(t *testing.T) {
	// The row order is the order the vocabulary lists kinds in; it must not drift.
	names := make([]string, 0, len(Kinds))
	for _, k := range Kinds {
		names = append(names, k.Name)
	}
	if !slices.Equal(names, []string{KindText, KindNumber, KindBoolean, KindDate, KindEnum, KindURL, KindRelation, KindRollup}) {
		t.Errorf("row order = %v", names)
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"", " ", "12", "-0", "true", "2026-11-10", "https://example.com", "工作", "t1", "NaN", "1e400"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, k := range Kinds {
			p := &Prop{Key: "p", Name: "p", Kind: k.Name, Options: []string{"工作"}, Many: true, Target: "task"}
			v, err := k.Parse(p, s)
			if err != nil {
				continue
			}
			again, err := k.Parse(p, v)
			if err != nil || !reflect.DeepEqual(again, v) {
				t.Fatalf("%s: Parse(%q) = %#v, then %#v (%v)", k.Name, s, v, again, err)
			}
		}
	})
}
