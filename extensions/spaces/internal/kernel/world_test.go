package kernel

import (
	"math/rand/v2"
	"reflect"
	"strconv"
	"testing"
)

// sampleWorld is the index of sampleSpace, checked first so the world is built from a canonical space.
func sampleWorld(t *testing.T) (*World, *Space) {
	t.Helper()
	b := sampleBase(t)
	return Open(b.Spaces[0]), b.Spaces[0]
}

func TestOutAndIn(t *testing.T) {
	w, _ := sampleWorld(t)
	tests := []struct {
		name string
		e    string
		out  []Link
		in   []Link
	}{
		{"task with a goal", "t1",
			[]Link{{From: "t1", Prop: "goal", To: "g1"}},
			[]Link{{From: "g1", Prop: "picks", To: "t1"}, {From: "t2", Prop: "blocked_by", To: "t1"}, {From: "t5", Prop: "origin", To: "t1"}}},
		{"task with two relations", "t2",
			[]Link{{From: "t2", Prop: "goal", To: "g1"}, {From: "t2", Prop: "blocked_by", To: "t1"}},
			[]Link{{From: "g1", Prop: "picks", To: "t2"}}},
		{"derived task", "t5",
			[]Link{{From: "t5", Prop: "origin", To: "t1"}}, nil},
		{"goal with picks and backlinks grouped by source type", "g1",
			[]Link{{From: "g1", Prop: "picks", To: "t1"}, {From: "g1", Prop: "picks", To: "t2"}},
			[]Link{{From: "n1", Prop: "about", To: "g1"}, {From: "t1", Prop: "goal", To: "g1"},
				{From: "t2", Prop: "goal", To: "g1"}, {From: "t3", Prop: "goal", To: "g1"}}},
		{"task without a goal", "t4", nil, nil},
		{"note", "n1", []Link{{From: "n1", Prop: "about", To: "g1"}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := w.Entity(tt.e)
			if got := w.Out(e); !reflect.DeepEqual(got, tt.out) {
				t.Errorf("Out = %v, want %v", got, tt.out)
			}
			if got := w.In(e); !reflect.DeepEqual(got, tt.in) {
				t.Errorf("In = %v, want %v", got, tt.in)
			}
		})
	}
	if w.Out(nil) != nil || w.In(nil) != nil {
		t.Errorf("a nil entity has links")
	}
	if w.Entity("zz") != nil {
		t.Errorf("Entity found an unknown id")
	}
}

// TestOutCopies: the slices returned by Out and In may be changed without changing the index.
func TestOutCopies(t *testing.T) {
	w, _ := sampleWorld(t)
	out := w.Out(w.Entity("t2"))
	out[0].To = "changed"
	if w.Out(w.Entity("t2"))[0].To != "g1" {
		t.Errorf("Out shares its slice with the index")
	}
	in := w.In(w.Entity("g1"))
	in[0].To = "changed"
	if w.In(w.Entity("g1"))[0].To != "g1" {
		t.Errorf("In shares its slice with the index")
	}
}

func TestRelated(t *testing.T) {
	w, _ := sampleWorld(t)
	tests := []struct {
		name string
		e    string
		via  string
		want []string
	}{
		{"own relation with several targets", "g1", "picks", []string{"t1", "t2"}},
		{"own relation with one target", "t1", "goal", []string{"g1"}},
		{"own relation unset", "t4", "goal", nil},
		{"origin", "t5", "origin", []string{"t1"}},
		{"origin unset", "t1", "origin", nil},
		{"backlinks of a type", "g1", "task.goal", []string{"t1", "t2", "t3"}},
		{"backlinks of another type", "g1", "note.about", []string{"n1"}},
		{"backlinks of a type with none", "t1", "note.about", nil},
		{"backlinks of a type that does not hold the prop", "g1", "task.nope", nil},
		{"backlinks of an unknown type", "g1", "nope.goal", nil},
		{"a value that is not a relation", "g1", "effort", nil},
		{"an unknown property", "g1", "nope", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, e := range w.Related(w.Entity(tt.e), tt.via) {
				got = append(got, e.ID)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Related(%s, %s) = %v, want %v", tt.e, tt.via, got, tt.want)
			}
		})
	}
	if got := w.Related(nil, "goal"); got != nil {
		t.Errorf("Related of a nil entity = %v", got)
	}
}

func TestDanglingIdsAreSkipped(t *testing.T) {
	s := &Space{ID: "s", Name: "s",
		Types: []*Type{
			{Key: "goal", Name: "目标"},
			{Key: "task", Name: "任务", Props: []Prop{{Key: "goal", Name: "目标", Kind: KindRelation, Target: "goal", Many: true}}},
		},
		Entities: []*Entity{
			{ID: "t1", Type: "task", Title: "任务", Values: map[string]any{"goal": []string{"gone", "g1"}}},
			{ID: "g1", Type: "goal", Title: "目标"},
		},
	}
	w := Open(s)
	if got := w.Related(w.Entity("t1"), "goal"); len(got) != 1 || got[0].ID != "g1" {
		t.Errorf("Related skipped nothing or kept a dangling id: %v", got)
	}
	if got := w.Out(w.Entity("t1")); len(got) != 2 {
		t.Errorf("Out lists every stored id, got %v", got)
	}
	if got := w.In(w.Entity("g1")); len(got) != 1 {
		t.Errorf("In = %v, want one backlink", got)
	}
	if got := w.Text(w.Entity("t1"), "goal"); got != "目标" {
		t.Errorf("Text of a partly dangling relation = %q", got)
	}
}

func TestValueAndText(t *testing.T) {
	w, _ := sampleWorld(t)
	valueTests := []struct {
		e, key string
		want   any
	}{
		{"g1", "tasks", 3.0},
		{"g1", "progress", 0.5},
		{"g1", "effort", 10.0},
		{"g1", "notes", 1.0},
		{"g1", "picked", 4.0},
		{"g2", "picked", nil},
		{"g2", "progress", nil},
		{"g2", "effort", 0.0},
		{"g1", "title", "目标一"},
		{"g1", "status", "进行"},
		{"g1", "phase", "doing"},
		{"g1", "picks", []string{"t1", "t2"}},
		{"t1", "phase", "done"},
		{"t1", "status", "完成"},
		{"t1", "type", "task"},
		{"t1", "origin", nil},
		{"t1", "effort", 3.0},
		{"t4", "effort", nil},
		{"t4", "status", "待办"},
		{"t5", "origin", []string{"t1"}},
		{"t1", "nope", nil},
		{"n1", "status", nil},
		{"n1", "phase", nil},
		{"n1", "read", true},
		{"n1", "link", "https://example.com/a"},
		{"zz", "title", nil},
	}
	for _, tt := range valueTests {
		if got := w.Value(w.Entity(tt.e), tt.key); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Value(%s, %s) = %#v, want %#v", tt.e, tt.key, got, tt.want)
		}
	}
	// The built-in created field reads as the day (UTC); the full timestamp is kept for sorting only.
	created, ok := w.Value(w.Entity("t1"), "created").(string)
	if !ok || created != "2026-11-01" {
		t.Errorf("created = %#v, want the day 2026-11-01", created)
	}
	if got := w.Value(nil, "title"); got != nil {
		t.Errorf("Value of a nil entity = %#v", got)
	}
	if got := w.Value(&Entity{ID: "x", Type: "nope", Title: "x"}, "title"); got != nil {
		t.Errorf("Value of an entity of an unknown type = %#v", got)
	}

	textTests := []struct {
		e, key, want string
	}{
		{"g1", "progress", "50%"},
		{"g1", "effort", "10 小时"},
		{"g1", "tasks", "3"},
		{"g1", "picks", "任务一、任务二"},
		{"g1", "due", "2026-12-31"},
		{"g1", "area", "工作"},
		{"t1", "goal", "目标一"},
		{"t1", "effort", "3 小时"},
		{"t1", "phase", "done"},
		{"t4", "goal", ""},
		{"t5", "origin", "任务一"},
		{"t1", "created", "2026-11-01"},
		{"n1", "read", "是"},
		{"n1", "link", "example.com/a"},
		{"t1", "nope", ""},
		{"g2", "progress", ""},
	}
	for _, tt := range textTests {
		if got := w.Text(w.Entity(tt.e), tt.key); got != tt.want {
			t.Errorf("Text(%s, %s) = %q, want %q", tt.e, tt.key, got, tt.want)
		}
	}
	if got := w.Text(nil, "title"); got != "" {
		t.Errorf("Text of a nil entity = %q", got)
	}
}

func TestRatioPrecedence(t *testing.T) {
	w, _ := sampleWorld(t)
	tests := []struct {
		e    string
		want float64
		ok   bool
	}{
		{"g1", 0.5, true}, // the card's progress (the percent_done rollup)
		{"g2", 0, false},  // the card's progress is undefined: no fallback to the flow
		{"t1", 1, true},   // no progress on the card: the flow position of 完成
		{"t2", 0.5, true}, // 进行 is halfway along the main line
		{"t4", 0, true},   // 待办 is the start
		{"t3", 0, false},  // 取消 is off the main line
		{"n1", 0, false},  // no flow and no progress
		{"zz", 0, false},  // unknown entity
	}
	for _, tt := range tests {
		got, ok := w.Ratio(w.Entity(tt.e))
		if got != tt.want || ok != tt.ok {
			t.Errorf("Ratio(%s) = %v, %v; want %v, %v", tt.e, got, ok, tt.want, tt.ok)
		}
	}
	if _, ok := w.Ratio(nil); ok {
		t.Errorf("Ratio of a nil entity is defined")
	}
}

// TestRatioOverAndOf covers a card progress with an Over operand and one without.
func TestRatioOverAndOf(t *testing.T) {
	s := &Space{ID: "s", Name: "s", Types: []*Type{{
		Key: "p", Name: "p",
		Props: []Prop{
			{Key: "a", Name: "a", Kind: KindNumber},
			{Key: "b", Name: "b", Kind: KindNumber},
		},
		Card: Card{Progress: &Ratio{Of: "a", Over: "b"}},
	}, {
		Key: "q", Name: "q",
		Props: []Prop{{Key: "a", Name: "a", Kind: KindNumber}},
		Card:  Card{Progress: &Ratio{Of: "a"}},
	}}}
	cases := []struct {
		name   string
		t      string
		values map[string]any
		want   float64
		ok     bool
	}{
		{"ratio", "p", map[string]any{"a": 3.0, "b": 4.0}, 0.75, true},
		{"over is zero", "p", map[string]any{"a": 3.0, "b": 0.0}, 0, false},
		{"over is unset", "p", map[string]any{"a": 3.0}, 0, false},
		{"of is unset", "p", map[string]any{"b": 4.0}, 0, false},
		{"above one is clamped", "p", map[string]any{"a": 5.0, "b": 4.0}, 1, true},
		{"below zero is clamped", "p", map[string]any{"a": -2.0, "b": 4.0}, 0, true},
		{"of alone is a ratio", "q", map[string]any{"a": 0.25}, 0.25, true},
		{"of alone above one", "q", map[string]any{"a": 2.0}, 1, true},
		{"of alone unset", "q", map[string]any{}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s.Entities = []*Entity{{ID: "e", Type: c.t, Title: "e", Values: c.values}}
			w := Open(s)
			got, ok := w.Ratio(w.Entity("e"))
			if got != c.want || ok != c.ok {
				t.Errorf("Ratio = %v, %v; want %v, %v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestAggregatesTable(t *testing.T) {
	names := []string{"count", "sum", "avg", "min", "max", "percent_done"}
	if len(Aggregates) != len(names) {
		t.Fatalf("%d aggregates, want %d", len(Aggregates), len(names))
	}
	for i, a := range Aggregates {
		if a.Name != names[i] || a.Label == "" || a.Fn == nil {
			t.Errorf("aggregate %d = %+v", i, a)
		}
		wantOperand := a.Name == "sum" || a.Name == "avg" || a.Name == "min" || a.Name == "max"
		if a.Operand != wantOperand {
			t.Errorf("%s: Operand = %v", a.Name, a.Operand)
		}
	}
	if _, ok := aggOf("median"); ok {
		t.Errorf("aggOf found an unknown aggregate")
	}
	if a, ok := aggOf("avg"); !ok || a.Label != "平均" {
		t.Errorf("aggOf(avg) = %+v, %v", a, ok)
	}
}

func TestAggregateFunctions(t *testing.T) {
	w, _ := sampleWorld(t)
	ent := func(ids ...string) []*Entity {
		var out []*Entity
		for _, id := range ids {
			out = append(out, w.Entity(id))
		}
		return out
	}
	tests := []struct {
		name string
		agg  string
		set  []*Entity
		of   string
		want float64
		ok   bool
	}{
		{"count of three", "count", ent("t1", "t2", "t3"), "", 3, true},
		{"count of nothing", "count", nil, "", 0, true},
		{"sum", "sum", ent("t1", "t2", "t3", "t4"), "effort", 10, true},
		{"sum of nothing is zero", "sum", nil, "effort", 0, true},
		{"sum of unset values", "sum", ent("t4"), "effort", 0, true},
		{"sum of an unknown property", "sum", ent("t1"), "nope", 0, true},
		{"sum of text", "sum", ent("t1"), "title", 0, true},
		{"avg", "avg", ent("t1", "t2"), "effort", 4, true},
		{"avg of nothing", "avg", nil, "effort", 0, false},
		{"avg of unset values", "avg", ent("t4"), "effort", 0, false},
		{"min", "min", ent("t1", "t2", "t3"), "effort", 2, true},
		{"min of nothing", "min", nil, "effort", 0, false},
		{"max", "max", ent("t1", "t2", "t3"), "effort", 5, true},
		{"max of nothing", "max", nil, "effort", 0, false},
		{"percent_done", "percent_done", ent("t1", "t2", "t3"), "", 0.5, true},
		{"percent_done ignores untyped entities", "percent_done", ent("t1", "n1"), "", 1, true},
		{"percent_done of dropped only", "percent_done", ent("t3"), "", 0, false},
		{"percent_done of nothing", "percent_done", nil, "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, ok := aggOf(tt.agg)
			if !ok {
				t.Fatalf("no aggregate %s", tt.agg)
			}
			got, ok := a.Fn(w, tt.set, tt.of)
			if got != tt.want || ok != tt.ok {
				t.Errorf("%s = %v, %v; want %v, %v", tt.agg, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestRollupsAreAggregatesOverRelations(t *testing.T) {
	w, _ := sampleWorld(t)
	g1 := w.Entity("g1")
	for _, tt := range []struct {
		prop string
		want any
	}{
		{"tasks", 3.0},
		{"progress", 0.5},
		{"effort", 10.0},
		{"picked", 4.0},
		{"notes", 1.0},
	} {
		if got := w.Value(g1, tt.prop); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("g1.%s = %#v, want %#v", tt.prop, got, tt.want)
		}
	}
	if got := w.Value(w.Entity("g2"), "progress"); got != nil {
		t.Errorf("an undefined rollup is %#v, want nil", got)
	}
}

func TestOpenToleratesNilsAndUnknownTypes(t *testing.T) {
	s := &Space{ID: "s", Name: "s",
		Types: []*Type{nil, {Key: "goal", Name: "目标", Props: []Prop{{Key: "x", Name: "x", Kind: KindRelation, Target: "goal"}}}},
		Entities: []*Entity{
			nil,
			{ID: "a", Type: "nope", Title: "无类型", Origin: "b"},
			{ID: "b", Type: "goal", Title: "目标", Values: map[string]any{"x": []string{"a"}}},
		},
	}
	w := Open(s)
	if got := w.Out(w.Entity("a")); !reflect.DeepEqual(got, []Link{{From: "a", Prop: "origin", To: "b"}}) {
		t.Errorf("Out of an entity of an unknown type = %v", got)
	}
	if got := w.Value(w.Entity("a"), "title"); got != nil {
		t.Errorf("Value of an unknown type = %#v", got)
	}
	if got := w.Related(w.Entity("b"), "x"); len(got) != 1 || got[0].ID != "a" {
		t.Errorf("Related = %v", got)
	}
	if got := w.Related(w.Entity("b"), "goal.x"); len(got) != 0 {
		t.Errorf("backlinks of an unknown source type = %v", got)
	}
}

// TestInIsTheInverseOfOut: over generated spaces, a link is in Out of its source exactly when it is in In of its
// target.
func TestInIsTheInverseOfOut(t *testing.T) {
	for seed := uint64(1); seed <= 25; seed++ {
		s := generatedSpace(seed)
		b := &Base{Format: 1, Spaces: []*Space{s}}
		if err := b.Check(); err != nil {
			t.Fatalf("seed %d: generated space is invalid: %v", seed, err)
		}
		w := Open(s)
		total := 0
		for _, e := range s.Entities {
			for _, l := range w.Out(e) {
				total++
				if !slicesContainsLink(w.In(w.Entity(l.To)), l) {
					t.Fatalf("seed %d: %v is in Out but not in In", seed, l)
				}
			}
			for _, l := range w.In(e) {
				if !slicesContainsLink(w.Out(w.Entity(l.From)), l) {
					t.Fatalf("seed %d: %v is in In but not in Out", seed, l)
				}
			}
		}
		back := 0
		for _, e := range s.Entities {
			back += len(w.In(e))
		}
		if back != total {
			t.Fatalf("seed %d: %d links out, %d links in", seed, total, back)
		}
	}
}

func slicesContainsLink(links []Link, l Link) bool {
	for _, x := range links {
		if x == l {
			return true
		}
	}
	return false
}

// generatedSpace builds a space of goals, tasks and notes with random relations, derivations and statuses.
func generatedSpace(seed uint64) *Space {
	r := rand.New(rand.NewPCG(seed, seed*7+1))
	s := sampleSpace()
	s.Entities = nil
	const goals, tasks, notes = 4, 14, 5
	gid := func(i int) string { return "g" + strconv.Itoa(i) }
	tid := func(i int) string { return "t" + strconv.Itoa(i) }
	goalStatus := []string{"规划", "进行", "达成", "放弃"}
	taskStatus := []string{"待办", "进行", "完成", "取消"}
	for i := 0; i < goals; i++ {
		e := &Entity{ID: gid(i), Type: "goal", Title: "目标" + strconv.Itoa(i), Status: goalStatus[r.IntN(4)]}
		e.Values = map[string]any{}
		var picks []string
		for j := 0; j < tasks; j++ {
			if r.IntN(4) == 0 {
				picks = append(picks, tid(j))
			}
		}
		if len(picks) > 0 {
			e.Values["picks"] = picks
		}
		s.Entities = append(s.Entities, e)
	}
	for i := 0; i < tasks; i++ {
		e := &Entity{ID: tid(i), Type: "task", Title: "任务" + strconv.Itoa(i), Status: taskStatus[r.IntN(4)]}
		e.Values = map[string]any{}
		if r.IntN(3) > 0 {
			e.Values["goal"] = gid(r.IntN(goals))
		}
		if i > 0 && r.IntN(2) == 0 {
			e.Values["blocked_by"] = []string{tid(r.IntN(i))}
		}
		if i > 0 && r.IntN(4) == 0 {
			e.Origin = tid(r.IntN(i))
		}
		s.Entities = append(s.Entities, e)
	}
	for i := 0; i < notes; i++ {
		e := &Entity{ID: "n" + strconv.Itoa(i), Type: "note", Title: "记录" + strconv.Itoa(i)}
		if r.IntN(2) == 0 {
			e.Values = map[string]any{"about": gid(r.IntN(goals))}
		}
		s.Entities = append(s.Entities, e)
	}
	return s
}

func TestOpenPropertiesOnFixture(t *testing.T) {
	w, s := sampleWorld(t)
	if w.s != s || len(w.types) != 3 || len(w.byID) != len(s.Entities) {
		t.Errorf("the index does not hold the space it was built from")
	}
	if got := w.typeOf(nil); got != nil {
		t.Errorf("typeOf(nil) = %v", got)
	}
	if _, ok := w.prop(nil, "title"); ok {
		t.Errorf("prop of a nil entity is defined")
	}
	if got := w.title("zz"); got != "" {
		t.Errorf("title of an unknown id = %q", got)
	}
	if _, ok := w.number(w.Entity("t4"), "effort"); ok {
		t.Errorf("number of an unset value is defined")
	}
	if _, ok := w.number(w.Entity("t1"), "title"); ok {
		t.Errorf("number of a text is defined")
	}
	if _, ok := w.number(w.Entity("t1"), "nope"); ok {
		t.Errorf("number of an unknown property is defined")
	}
	if _, ok := w.number(w.Entity("g1"), "summary"); ok {
		t.Errorf("number of a text property is defined")
	}
}

func TestOpenTypeWithoutNumericReadingOrRefs(t *testing.T) {
	// An entity whose value is not a number, and the rollup helpers on a type that holds nothing numeric.
	s := &Space{ID: "s", Name: "s",
		Types:    []*Type{{Key: "a", Name: "a", Props: []Prop{{Key: "d", Name: "d", Kind: KindDate}}}},
		Entities: []*Entity{{ID: "x", Type: "a", Title: "x", Values: map[string]any{"d": "2026-01-01"}}},
	}
	w := Open(s)
	if _, ok := w.number(w.Entity("x"), "d"); ok {
		t.Errorf("number of a date is defined")
	}
	if got := w.Related(w.Entity("x"), "d"); got != nil {
		t.Errorf("Related over a date = %v", got)
	}
}
