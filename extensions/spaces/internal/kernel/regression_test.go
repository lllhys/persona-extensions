package kernel

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A property of an unknown kind in a replaced type is refused before any entity is migrated; it used to panic.
func TestPutTypeRefusesUnknownKindBeforeMigrating(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	c := typeCopy(t, s, "note")
	for i := range c.Props {
		if c.Props[i].Key == "read" {
			c.Props[i].Kind = "bogus"
		}
	}
	before := sampleBase(t)
	if err := s.PutType(c, editNow); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("PutType with an unknown kind = %v, want a refusal naming it", err)
	}
	if !sameBase(b, before) {
		t.Errorf("a refused type change changed the space")
	}
}

// A generated space id skips the ids already taken, so that one chosen id does not block every later create.
func TestPutSpaceGeneratedIDSkipsTaken(t *testing.T) {
	b := sampleBase(t)
	b.Seq = 0
	if _, err := b.PutSpace(SpaceInput{ID: "s1", Name: strp("甲")}); err != nil {
		t.Fatalf("create s1: %v", err)
	}
	s, err := b.PutSpace(SpaceInput{Name: strp("乙")})
	if err != nil {
		t.Fatalf("create without an id after s1 was taken: %v", err)
	}
	if s.ID != "s2" || b.Seq != 2 {
		t.Errorf("generated id = %q at seq %d, want s2 at seq 2", s.ID, b.Seq)
	}
}

// The built-in created and updated fields read as the day (UTC) for conditions, grouping and text; sorting still
// uses the full timestamp.
func TestBuiltinDatesAreDays(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	s.entity("t2").Created = time.Date(2026, 11, 1, 15, 30, 0, 0, time.UTC)
	w := Open(s)
	if got := w.Text(w.Entity("t2"), "created"); got != "2026-11-01" {
		t.Errorf("Text(created) = %q, want the day", got)
	}
	count := func(c Cond) int {
		t.Helper()
		res, err := w.Run(Query{Type: "task", Lens: Lens{Where: []Cond{c}}})
		if err != nil {
			t.Fatalf("Run(%+v): %v", c, err)
		}
		return res.Total
	}
	if n := count(Cond{Field: "created", Op: "eq", Value: "2026-11-01"}); n != 5 {
		t.Errorf("created eq 2026-11-01 matched %d, want 5", n)
	}
	if n := count(Cond{Field: "created", Op: "le", Value: "2026-11-01"}); n != 5 {
		t.Errorf("created le 2026-11-01 matched %d, want 5", n)
	}
	if n := count(Cond{Field: "created", Op: "gt", Value: "2026-11-01"}); n != 0 {
		t.Errorf("created gt 2026-11-01 matched %d, want 0", n)
	}

	res, err := w.Run(Query{Type: "task", Lens: Lens{Group: "created"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 1 || res.Groups[0].Key != "2026-11-01" || res.Groups[0].Name != "2026-11-01" || res.Groups[0].Count != 5 {
		t.Errorf("groups by created = %+v, want one group of 5 named by the day", res.Groups)
	}

	want := []string{"t1", "t3", "t4", "t5", "t2"}
	if got := runIDs(t, w, Query{Type: "task", Lens: Lens{Sort: []Order{{By: "created"}}}}); !reflect.DeepEqual(got, want) {
		t.Errorf("sorted by created = %v, want %v (full timestamp order)", got, want)
	}
}

// A sum or an average that overflows is undefined, not infinite: it cannot be encoded as JSON.
func TestAggregateOverflowIsUndefined(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	s.entity("t1").Values["effort"] = 1e308
	s.entity("t2").Values["effort"] = 1e308
	w := Open(s)
	res, err := w.Run(Query{Type: "task", Lens: Lens{Agg: []Agg{{Op: "sum", Of: "effort"}, {Op: "avg", Of: "effort"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Aggs {
		if c.Value != nil {
			t.Errorf("%s = %v, want undefined", c.Name, *c.Value)
		}
	}
	if _, err := json.Marshal(res); err != nil {
		t.Errorf("result does not encode: %v", err)
	}
	if v := w.Value(w.Entity("g1"), "effort"); v != nil {
		t.Errorf("rollup sum = %#v, want undefined", v)
	}
}

// A view condition is bound the way a query binds it, so an unknown operator, a value the kind refuses or an option
// the property lacks is refused by Check.
func TestViewConditionsAreBoundLikeQueries(t *testing.T) {
	cases := []struct {
		name string
		c    Cond
		ok   bool
	}{
		{"valid stage", Cond{Field: "status", Op: "eq", Value: "进行"}, true},
		{"unknown operator", Cond{Field: "status", Op: "bogus", Value: "进行"}, false},
		{"value the kind refuses", Cond{Field: "effort", Op: "eq", Value: "很多"}, false},
		{"option the property lacks", Cond{Field: "status", Op: "eq", Value: "不存在"}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			b := sampleBase(t)
			// Types[1] is task; Views[0] is its board.
			b.Spaces[0].Types[1].Views[0].Where = []Cond{tt.c}
			if err := b.Check(); (err == nil) != tt.ok {
				t.Errorf("Check = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

// A type replacement carries renamed stages and options over to the view conditions that name them, and refuses a
// view condition that names no stage or option.
func TestPutTypeMigratesViewConditions(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	c := typeCopy(t, s, "task")
	c.Flow = []Stage{{Name: "待办", Phase: Todo}, {Name: "处理", Phase: Doing}, {Name: "完成", Phase: Done}, {Name: "取消", Phase: Dropped}}
	c.Views = []View{{Name: "进行中", Kind: "list", Lens: Lens{Where: []Cond{{Field: "status", Op: "eq", Value: "进行"}}}}}
	if err := s.PutType(c, editNow); err != nil {
		t.Fatalf("stage rename: %v", err)
	}
	nt, _ := s.typeByKey("task")
	if got := nt.Views[0].Where[0].Value; got != "处理" {
		t.Errorf("view condition after rename = %v, want 处理", got)
	}
	if got := runIDs(t, Open(s), Query{Type: "task", Lens: nt.Views[0].Lens}); !reflect.DeepEqual(got, []string{"t2"}) {
		t.Errorf("view after rename = %v, want [t2]", got)
	}

	b = sampleBase(t)
	s = b.Spaces[0]
	c = typeCopy(t, s, "goal")
	for i := range c.Props {
		if c.Props[i].Key == "area" {
			c.Props[i].Options = []string{"职业", "家庭"}
		}
	}
	c.Views = []View{{Name: "工作", Kind: "list", Lens: Lens{Where: []Cond{{Field: "area", Op: "eq", Value: "工作"}}}}}
	if err := s.PutType(c, editNow); err != nil {
		t.Fatalf("option rename: %v", err)
	}
	ng, _ := s.typeByKey("goal")
	if got := ng.Views[0].Where[0].Value; got != "职业" {
		t.Errorf("view condition after option rename = %v, want 职业", got)
	}

	b = sampleBase(t)
	s = b.Spaces[0]
	before := sampleBase(t)
	c = typeCopy(t, s, "task")
	c.Views = []View{{Name: "错", Kind: "list", Lens: Lens{Where: []Cond{{Field: "status", Op: "eq", Value: "不存在"}}}}}
	if err := s.PutType(c, editNow); err == nil || !strings.Contains(err.Error(), "没有选项") {
		t.Errorf("view naming no stage = %v, want a refusal", err)
	}
	if !sameBase(b, before) {
		t.Errorf("a refused view change changed the space")
	}
}
