package kernel

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var editNow = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

func strp(s string) *string { return &s }

// copyType returns a copy of a type of the space that can be changed without touching the space. It panics when the
// type is missing, which is a bug in the fixture.
func copyType(s *Space, key string) Type {
	old, ok := s.typeByKey(key)
	if !ok {
		panic("no type " + key)
	}
	c := *old
	c.Flow = slices.Clone(old.Flow)
	c.Props = slices.Clone(old.Props)
	c.Views = slices.Clone(old.Views)
	return c
}

// typeCopy is copyType for a test, which fails when the type is missing.
func typeCopy(t *testing.T, s *Space, key string) Type {
	t.Helper()
	if _, ok := s.typeByKey(key); !ok {
		t.Fatalf("no type %s", key)
	}
	return copyType(s, key)
}

// sameBase reports whether two bases hold the same state.
func sameBase(a, b *Base) bool { return reflect.DeepEqual(a, b) }

func TestBaseSpaceResolution(t *testing.T) {
	b := sampleBase(t)
	if s, err := b.Space(""); err != nil || s.ID != "main" {
		t.Errorf("Space(\"\") with one space = %v, %v", s, err)
	}
	if s, err := b.Space("main"); err != nil || s.ID != "main" {
		t.Errorf("Space(main) = %v, %v", s, err)
	}
	if _, err := b.Space("nope"); err == nil {
		t.Errorf("Space accepted an unknown id")
	}
	b.Spaces = append(b.Spaces, &Space{ID: "two", Name: "二"})
	if _, err := b.Space(""); err == nil {
		t.Errorf("Space(\"\") resolved with two spaces")
	}
	b.Spaces = nil
	if _, err := b.Space(""); err == nil {
		t.Errorf("Space(\"\") resolved with no space")
	}
}

func TestFind(t *testing.T) {
	b := sampleBase(t)
	s, e, err := b.Find("g1")
	if err != nil || s.ID != "main" || e.Title != "目标一" {
		t.Errorf("Find(g1) = %v, %v, %v", s, e, err)
	}
	if _, _, err := b.Find("zz"); err == nil {
		t.Errorf("Find found an unknown entity")
	}
}

func TestPutSpace(t *testing.T) {
	b := sampleBase(t)
	s, err := b.PutSpace(SpaceInput{Name: strp("新空间"), About: strp("简介")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if s.ID != "sb" || b.Seq != 11 || len(b.Spaces) != 2 {
		t.Errorf("created %q at seq %d with %d spaces", s.ID, b.Seq, len(b.Spaces))
	}
	if _, err := b.PutSpace(SpaceInput{ID: "work", Name: strp("工作")}); err != nil {
		t.Errorf("create with an id: %v", err)
	}
	if b.Seq != 11 {
		t.Errorf("a given id advanced the counter to %d", b.Seq)
	}
	changed, err := b.PutSpace(SpaceInput{ID: "main", Icon: strp("star")})
	if err != nil || changed.Icon != "star" || changed.Name != "示例" {
		t.Errorf("change = %+v, %v", changed, err)
	}

	before := sampleBase(t)
	if _, err := b.PutSpace(SpaceInput{Name: nil}); err == nil {
		t.Errorf("a space was created without a name")
	}
	if _, err := b.PutSpace(SpaceInput{ID: "Bad Id", Name: strp("x")}); err == nil {
		t.Errorf("an invalid id was accepted")
	}
	_ = before
}

// TestPutSpaceRefusalKeepsBase: a refused space change leaves the base as it was.
func TestPutSpaceRefusalKeepsBase(t *testing.T) {
	b := sampleBase(t)
	ref := sampleBase(t)
	if _, err := b.PutSpace(SpaceInput{ID: "main", Name: strp("")}); err == nil {
		t.Errorf("an empty name was accepted")
	}
	if _, err := b.PutSpace(SpaceInput{ID: "main", About: strp(strings.Repeat("字", MaxAbout+1))}); err == nil {
		t.Errorf("a long about was accepted")
	}
	if !sameBase(b, ref) {
		t.Errorf("a refused space change altered the base")
	}
}

func TestPutSpaceLimit(t *testing.T) {
	b := sampleBase(t)
	for i := 1; len(b.Spaces) < MaxSpaces; i++ {
		if _, err := b.PutSpace(SpaceInput{ID: "x" + strconv.Itoa(i), Name: strp("空间")}); err != nil {
			t.Fatalf("space %d: %v", i, err)
		}
	}
	if _, err := b.PutSpace(SpaceInput{ID: "overflow", Name: strp("空间")}); err == nil {
		t.Errorf("space %d was accepted", MaxSpaces+1)
	}
}

func TestDropSpace(t *testing.T) {
	b := sampleBase(t)
	if err := b.DropSpace("nope"); err == nil {
		t.Errorf("an unknown space was dropped")
	}
	if err := b.DropSpace("main"); err != nil || len(b.Spaces) != 0 {
		t.Errorf("DropSpace = %v, spaces %d", err, len(b.Spaces))
	}
}

func TestPutTypeAddAndLimits(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	if err := s.PutType(Type{Key: "idea", Name: "点子", Flow: []Stage{{Name: "收集", Phase: Todo}, {Name: "完成", Phase: Done}}}, editNow); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, ok := s.typeByKey("idea"); !ok {
		t.Fatal("the type was not added")
	}
	for i := 4; len(s.Types) < MaxTypes; i++ {
		if err := s.PutType(Type{Key: "tp" + strconv.Itoa(i), Name: "类型"}, editNow); err != nil {
			t.Fatalf("type %d: %v", i, err)
		}
	}
	if err := s.PutType(Type{Key: "overflow", Name: "类型"}, editNow); err == nil {
		t.Errorf("type %d was accepted", MaxTypes+1)
	}
	if err := s.PutType(Type{Key: "Bad", Name: "类型"}, editNow); err == nil {
		t.Errorf("an invalid key was accepted")
	}
}

func TestPutTypeRefusals(t *testing.T) {
	tests := []struct {
		name string
		make func(s *Space) Type
	}{
		{"nine stages", func(s *Space) Type {
			c := copyType(s, "goal")
			c.Flow = nil
			for i := 0; i < 9; i++ {
				c.Flow = append(c.Flow, Stage{Name: "阶段" + strconv.Itoa(i), Phase: Todo})
			}
			return c
		}},
		{"long title of a type", func(s *Space) Type {
			c := copyType(s, "goal")
			c.Name = strings.Repeat("字", MaxName+1)
			return c
		}},
		{"too many properties", func(s *Space) Type {
			c := copyType(s, "task")
			for i := 0; len(c.Props) <= MaxProps; i++ {
				c.Props = append(c.Props, Prop{Key: "p" + strconv.Itoa(i), Name: "属性", Kind: KindText})
			}
			return c
		}},
		{"a card naming a property that does not exist", func(s *Space) Type {
			c := copyType(s, "note")
			c.Card = Card{Lead: "nope"}
			return c
		}},
		{"a progress over a text", func(s *Space) Type {
			c := copyType(s, "goal")
			c.Card = Card{Progress: &Ratio{Of: "summary"}}
			return c
		}},
		{"a relation to a missing type", func(s *Space) Type {
			c := copyType(s, "note")
			c.Props = append(c.Props, Prop{Key: "x", Name: "关", Kind: KindRelation, Target: "nope"})
			return c
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := sampleBase(t)
			before := sampleBase(t)
			s := b.Spaces[0]
			if err := s.PutType(tt.make(s), editNow); err == nil {
				t.Fatalf("the change was accepted")
			}
			if !sameBase(b, before) {
				t.Errorf("a refused change changed the space")
			}
		})
	}
}

func TestPutTypeReparseAndDroppedProperty(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]

	// read: boolean -> text. Values are reparsed, so true becomes "true".
	c := typeCopy(t, s, "note")
	for i := range c.Props {
		if c.Props[i].Key == "read" {
			c.Props[i].Kind = KindText
		}
	}
	if err := s.PutType(c, editNow); err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got := s.entity("n1").Values["read"]; got != "true" {
		t.Errorf("reparsed value = %#v", got)
	}
	if got := s.entity("n1").Updated; !got.Equal(editNow) {
		t.Errorf("a migrated entity was not touched: %v", got)
	}
	if got := s.entity("g1").Updated; got.Equal(editNow) {
		t.Errorf("an unchanged entity was touched")
	}

	// blocked_by is named by no card, lens or rollup: its values go with it.
	c = typeCopy(t, s, "task")
	c.Props = slices.DeleteFunc(c.Props, func(p Prop) bool { return p.Key == "blocked_by" })
	if err := s.PutType(c, editNow); err != nil {
		t.Fatalf("drop a property: %v", err)
	}
	if _, ok := s.entity("t2").Values["blocked_by"]; ok {
		t.Errorf("a removed property kept its values")
	}
	if _, ok := s.typeByKey("task"); !ok {
		t.Fatal("type lost")
	}
	if err := (&Base{Format: 1, Spaces: []*Space{s}}).Check(); err != nil {
		t.Errorf("space after migration: %v", err)
	}
}

func TestPutTypeRefusesWhatIsStillNamed(t *testing.T) {
	b := sampleBase(t)
	before := sampleBase(t)
	s := b.Spaces[0]
	c := typeCopy(t, s, "task")
	c.Props = slices.DeleteFunc(c.Props, func(p Prop) bool { return p.Key == "effort" })
	if err := s.PutType(c, editNow); err == nil || !strings.Contains(err.Error(), "effort") {
		t.Errorf("removing a rollup's operand = %v", err)
	}
	if !sameBase(b, before) {
		t.Errorf("the refused change changed the space")
	}
}

func TestPutTypeEnumRenames(t *testing.T) {
	// Same length: positional rename of options.
	b := sampleBase(t)
	s := b.Spaces[0]
	c := typeCopy(t, s, "goal")
	for i := range c.Props {
		if c.Props[i].Key == "area" {
			c.Props[i].Options = []string{"职业", "家庭"}
		}
	}
	if err := s.PutType(c, editNow); err != nil {
		t.Fatalf("positional rename: %v", err)
	}
	if s.entity("g1").Values["area"] != "职业" || s.entity("g2").Values["area"] != "家庭" {
		t.Errorf("options were not renamed: %v %v", s.entity("g1").Values, s.entity("g2").Values)
	}

	// Different length: 工作 has no successor and g1 uses it, so the change names g1.
	b = sampleBase(t)
	s = b.Spaces[0]
	c = typeCopy(t, s, "goal")
	for i := range c.Props {
		if c.Props[i].Key == "area" {
			c.Props[i].Options = []string{"家庭"}
		}
	}
	err := s.PutType(c, editNow)
	if err == nil || !strings.Contains(err.Error(), "目标一") {
		t.Errorf("refusal = %v, want one naming 目标一", err)
	}
}

func TestPutTypeStagesRenameAndRefusal(t *testing.T) {
	// Same length: positional rename of stages; the status of each entity follows.
	b := sampleBase(t)
	s := b.Spaces[0]
	c := typeCopy(t, s, "task")
	c.Flow = []Stage{{Name: "新", Phase: Todo}, {Name: "做", Phase: Doing}, {Name: "好", Phase: Done}, {Name: "停", Phase: Dropped}}
	if err := s.PutType(c, editNow); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if s.entity("t4").Status != "新" || s.entity("t2").Status != "做" || s.entity("t1").Status != "好" || s.entity("t3").Status != "停" {
		t.Errorf("statuses after rename: %v %v %v %v", s.entity("t4").Status, s.entity("t2").Status, s.entity("t1").Status, s.entity("t3").Status)
	}

	// Different length: 进行 has no successor and t2 uses it: refused, naming t2.
	b = sampleBase(t)
	before := sampleBase(t)
	s = b.Spaces[0]
	c = typeCopy(t, s, "task")
	c.Flow = []Stage{{Name: "待办", Phase: Todo}, {Name: "完成", Phase: Done}, {Name: "取消", Phase: Dropped}}
	err := s.PutType(c, editNow)
	if err == nil || !strings.Contains(err.Error(), "任务二") {
		t.Errorf("refusal = %v, want one naming 任务二", err)
	}
	if !sameBase(b, before) {
		t.Errorf("all-or-nothing: the refused stage change changed the space")
	}
}

func TestPutTypeFlowAddedAndRemoved(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	c := typeCopy(t, s, "note")
	c.Flow = []Stage{{Name: "记下", Phase: Todo}, {Name: "整理", Phase: Done}}
	if err := s.PutType(c, editNow); err != nil {
		t.Fatalf("add a flow: %v", err)
	}
	if got := s.entity("n1").Status; got != "记下" {
		t.Errorf("status after adding a flow = %q", got)
	}

	// Removing the flow of a type whose entities are in it is refused, naming the first such entity.
	b = sampleBase(t)
	s = b.Spaces[0]
	c = typeCopy(t, s, "goal")
	c.Flow = nil
	err := s.PutType(c, editNow)
	if err == nil || !strings.Contains(err.Error(), "目标一") {
		t.Errorf("removing a flow = %v", err)
	}
}

func TestPutTypeStoredValueOnComputedProperty(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	c := typeCopy(t, s, "goal")
	for i := range c.Props {
		if c.Props[i].Key == "area" {
			c.Props[i] = Prop{Key: "area", Name: "领域", Kind: KindRollup, Via: "task.goal", Op: "count"}
		}
	}
	err := s.PutType(c, editNow)
	if err == nil || !strings.Contains(err.Error(), "汇总不能存储值") {
		t.Errorf("a stored value on a rollup = %v", err)
	}
}

func TestPutTypeRelationTargetChange(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	c := typeCopy(t, s, "task")
	for i := range c.Props {
		if c.Props[i].Key == "goal" {
			c.Props[i].Target = "note"
		}
	}
	err := s.PutType(c, editNow)
	if err == nil || !strings.Contains(err.Error(), "任务一") {
		t.Errorf("a relation to the wrong type = %v", err)
	}
}

func TestPutTypeComputedBecomesStored(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	c := typeCopy(t, s, "goal")
	for i := range c.Props {
		if c.Props[i].Key == "effort" {
			c.Props[i] = Prop{Key: "effort", Name: "总投入", Kind: KindNumber, Unit: "小时"}
		}
	}
	if err := s.PutType(c, editNow); err != nil {
		t.Fatalf("a rollup becomes a number: %v", err)
	}
	if _, ok := s.entity("g1").Values["effort"]; ok {
		t.Errorf("a stored value appeared")
	}
}

func TestDropType(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	if err := s.DropType("nope"); err == nil {
		t.Errorf("an unknown type was dropped")
	}
	for _, key := range []string{"task", "note"} {
		if err := s.DropType(key); err == nil {
			t.Errorf("dropped %s while another type names it", key)
		}
	}
	if err := s.PutType(Type{Key: "idea", Name: "点子"}, editNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutEntity(b, EntityInput{Type: "idea", Title: strp("点子一")}, editNow); err != nil {
		t.Fatal(err)
	}
	if err := s.DropType("idea"); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, ok := s.typeByKey("idea"); ok {
		t.Errorf("the type is still there")
	}
	for _, e := range s.Entities {
		if e.Type == "idea" {
			t.Errorf("an entity of the dropped type is still there")
		}
	}
}

func TestPutEntityCreate(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	e, err := s.PutEntity(b, EntityInput{Type: "task", Title: strp("  新任务  ")}, editNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if e.ID != "eb" || b.Seq != 11 || e.Title != "新任务" || e.Status != "待办" || !e.Created.Equal(editNow) {
		t.Errorf("created %+v at seq %d", e, b.Seq)
	}
	e2, err := s.PutEntity(b, EntityInput{Type: "task", Title: strp("完成的"), Status: strp("完成"),
		Values: map[string]any{"goal": "g2", "effort": nil}}, editNow)
	if err != nil || e2.Status != "完成" || e2.Values["goal"] == nil {
		t.Errorf("create with a status and values: %+v, %v", e2, err)
	}
	if _, ok := e2.Values["effort"]; ok {
		t.Errorf("a null on create stored a value")
	}

	refused := []EntityInput{
		{Type: "nope", Title: strp("x")},
		{Type: "task"},
		{Type: "task", Title: strp("   ")},
		{Type: "task", Title: strp(strings.Repeat("字", MaxTitle+1))},
		{Type: "task", Title: strp("x"), Values: map[string]any{"nope": 1}},
		{Type: "task", Title: strp("x"), Status: strp("乱")},
		{Type: "task", Title: strp("x"), Values: map[string]any{"goal": "zz"}},
		{Type: "task", Title: strp("x"), Values: map[string]any{"goal": "n1"}},
		{Type: "task", Title: strp("x"), Values: map[string]any{"goal": "g1", "tasks": 3}},
		{Type: "task", Title: strp("x"), Values: map[string]any{"blocked_by": manyIDsX(MaxTargets + 1)}},
		{Type: "note", Title: strp("x"), Status: strp("乱")},
	}
	for i, in := range refused {
		if _, err := s.PutEntity(b, in, editNow); err == nil {
			t.Errorf("refused input %d was accepted: %+v", i, in)
		}
	}
	// Two creations succeeded (eb and the completed one); the refused ones issued no id.
	if b.Seq != 12 {
		t.Errorf("refused creations advanced the counter to %d", b.Seq)
	}
}

// manyIDsX returns n ids that name nothing.
func manyIDsX(n int) []any {
	out := make([]any, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, "x"+strconv.Itoa(i))
	}
	return out
}

func TestPutEntityNoFlowStatus(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	e, err := s.PutEntity(b, EntityInput{Type: "note", Title: strp("记")}, editNow)
	if err != nil || e.Status != "" {
		t.Errorf("a note has no status: %+v, %v", e, err)
	}
}

func TestPutEntityLimit(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	for i := len(s.Entities); i < MaxEntities; i++ {
		s.Entities = append(s.Entities, &Entity{ID: "z" + strconv.Itoa(i), Type: "note", Title: "记"})
	}
	if _, err := s.PutEntity(b, EntityInput{Type: "note", Title: strp("超出")}, editNow); err == nil {
		t.Errorf("entity %d was accepted", MaxEntities+1)
	}
	if b.Seq != 10 || len(s.Entities) != MaxEntities {
		t.Errorf("a refused entity changed the space: seq %d, %d entities", b.Seq, len(s.Entities))
	}
}

func TestPutEntityChange(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]

	e, err := s.PutEntity(b, EntityInput{ID: "t4", Title: strp("改了")}, editNow)
	if err != nil || e.Title != "改了" || !e.Updated.Equal(editNow) || e.Created.Equal(editNow) {
		t.Errorf("title change = %+v, %v", e, err)
	}
	if e, err := s.PutEntity(b, EntityInput{ID: "t4", Status: strp("进行")}, editNow); err != nil || e.Status != "进行" {
		t.Errorf("a legal move = %+v, %v", e, err)
	}
	if _, err := s.PutEntity(b, EntityInput{ID: "t5", Status: strp("完成")}, editNow); err == nil ||
		err.Error() != "「待办」不能直接变为「完成」；可以变为：进行、取消" {
		t.Errorf("an illegal move = %v", err)
	}
	if e, err := s.PutEntity(b, EntityInput{ID: "t3", Status: strp("待办")}, editNow); err != nil || e.Status != "待办" {
		t.Errorf("from a dropped stage back to the line = %+v, %v", e, err)
	}
	if e, err := s.PutEntity(b, EntityInput{ID: "t2", Status: strp("进行")}, editNow); err != nil || e.Status != "进行" {
		t.Errorf("the same status = %+v, %v", e, err)
	}
	if _, err := s.PutEntity(b, EntityInput{ID: "t2", Status: strp("乱")}, editNow); err == nil {
		t.Errorf("a status outside the flow was accepted")
	}
	if _, err := s.PutEntity(b, EntityInput{ID: "t2", Type: "goal"}, editNow); err == nil {
		t.Errorf("the type changed")
	}
	if _, err := s.PutEntity(b, EntityInput{ID: "zz", Title: strp("x")}, editNow); err == nil {
		t.Errorf("an unknown entity was changed")
	}
	if _, err := s.PutEntity(b, EntityInput{ID: "t2", Values: map[string]any{"nope": 1}}, editNow); err == nil {
		t.Errorf("an unknown key was accepted")
	}
	if _, err := s.PutEntity(b, EntityInput{ID: "t2", Values: map[string]any{"nope": nil}}, editNow); err == nil {
		t.Errorf("an unknown key was accepted with null")
	}
	if _, err := s.PutEntity(b, EntityInput{ID: "t2", Values: map[string]any{"blocked_by": []any{"t2"}}}, editNow); err == nil {
		t.Errorf("an entity was allowed to reference itself")
	}
	if _, err := s.PutEntity(b, EntityInput{ID: "n1", Status: strp("x")}, editNow); err == nil {
		t.Errorf("a type without a flow got a status")
	}
	if _, err := s.PutEntity(b, EntityInput{ID: "t2", Title: strp("")}, editNow); err == nil {
		t.Errorf("an empty title was accepted")
	}

	// null unsets; a relation value replaces the list.
	e, err = s.PutEntity(b, EntityInput{ID: "t2", Values: map[string]any{"effort": nil, "blocked_by": []any{"t3"}}}, editNow)
	if err != nil {
		t.Fatalf("null and a list: %v", err)
	}
	if _, ok := e.Values["effort"]; ok {
		t.Errorf("null did not unset")
	}
	if !reflect.DeepEqual(e.Values["blocked_by"], []string{"t3"}) {
		t.Errorf("relation value = %#v", e.Values["blocked_by"])
	}
	if e.Values["goal"] == nil {
		t.Errorf("an untouched value was lost")
	}
}

func TestDeriveInheritance(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]

	// Same type: a copy with provenance; the source is not changed.
	c, err := s.Derive(b, "t1", "task", "", editNow)
	if err != nil {
		t.Fatalf("same-type copy: %v", err)
	}
	if c.Title != "任务一" || c.Origin != "t1" || c.Status != "待办" || !reflect.DeepEqual(c.Values, map[string]any{"goal": []string{"g1"}, "effort": 3.0}) {
		t.Errorf("copy = %+v", c)
	}
	if s.entity("t1").Status != "完成" {
		t.Errorf("the source changed")
	}

	// Given title, and a blank one that falls back to the source.
	if c, _ := s.Derive(b, "t1", "task", "  新名字 ", editNow); c.Title != "新名字" {
		t.Errorf("given title = %q", c.Title)
	}
	if c, _ := s.Derive(b, "t1", "task", "   ", editNow); c.Title != "任务一" {
		t.Errorf("blank title = %q", c.Title)
	}

	// A rollup in the target is computed, so nothing is carried into it.
	g, err := s.Derive(b, "t2", "goal", "", editNow)
	if err != nil || g.Values != nil || g.Status != "规划" {
		t.Errorf("task to goal = %+v, %v", g, err)
	}

	// Nothing shared between note and goal.
	n, err := s.Derive(b, "n1", "goal", "", editNow)
	if err != nil || n.Values != nil {
		t.Errorf("note to goal = %+v, %v", n, err)
	}

	// A type without a flow gets no status.
	nn, err := s.Derive(b, "t1", "note", "", editNow)
	if err != nil || nn.Status != "" {
		t.Errorf("task to note = %+v, %v", nn, err)
	}

	for _, tt := range []struct{ id, typ string }{{"zz", "task"}, {"t1", "nope"}} {
		if _, err := s.Derive(b, tt.id, tt.typ, "", editNow); err == nil {
			t.Errorf("Derive(%s, %s) accepted", tt.id, tt.typ)
		}
	}
	// Six derivations succeeded; the refused ones issued no id.
	if b.Seq != 10+6 {
		t.Errorf("the counter is %d, want 16", b.Seq)
	}
}

// matrixValues are one sample value of each stored kind.
var matrixValues = map[string]any{
	KindText:     "12",
	KindNumber:   12.0,
	KindBoolean:  true,
	KindDate:     "2026-11-01",
	KindEnum:     "a",
	KindURL:      "https://example.com/a",
	KindRelation: []string{"t0"},
}

// TestDeriveMatrix: for every pair of kinds, a derived value is exactly what the destination kind parses from the
// source value, and absent otherwise.
func TestDeriveMatrix(t *testing.T) {
	kinds := []string{KindText, KindNumber, KindBoolean, KindDate, KindEnum, KindURL, KindRelation}
	for _, from := range kinds {
		for _, to := range kinds {
			for _, target := range []string{"tg", "other"} {
				if to != KindRelation && target == "other" {
					continue
				}
				src := Prop{Key: "x", Name: "x", Kind: from, Options: []string{"a", "b"}, Target: "tg"}
				dst := Prop{Key: "x", Name: "x", Kind: to, Options: []string{"a", "b"}, Target: target}
				if from != KindRelation {
					src.Target = ""
				}
				if to != KindRelation {
					dst.Target = ""
				}
				if from != KindEnum {
					src.Options = nil
				}
				if to != KindEnum {
					dst.Options = nil
				}
				b := &Base{Format: 1, Seq: 5, Spaces: []*Space{{ID: "m", Name: "m",
					Types: []*Type{
						{Key: "src", Name: "源", Props: []Prop{src}},
						{Key: "dst", Name: "目", Props: []Prop{dst}},
						{Key: "tg", Name: "目标"},
						{Key: "other", Name: "其他"},
					},
					Entities: []*Entity{
						{ID: "t0", Type: "tg", Title: "目标一"},
						{ID: "s0", Type: "src", Title: "源一", Values: map[string]any{"x": matrixValues[from]}},
					},
				}}}
				if err := b.Check(); err != nil {
					t.Fatalf("%s -> %s: base invalid: %v", from, to, err)
				}
				s := b.Spaces[0]
				e, err := s.Derive(b, "s0", "dst", "", editNow)
				if err != nil {
					t.Fatalf("%s -> %s: %v", from, to, err)
				}
				k, _ := KindOf(to)
				want, werr := k.Parse(&dst, matrixValues[from])
				carried := werr == nil && want != nil
				if k.Refs != nil {
					// The carried ids must name entities of the target type: only t0 is one.
					carried = carried && target == "tg" && slices.Equal(k.Refs(want), []string{"t0"})
				}
				got, ok := e.Values["x"]
				if carried != ok || (carried && !reflect.DeepEqual(got, want)) {
					t.Errorf("%s -> %s (target %s): carried %v (%#v), want %v (%#v)", from, to, target, ok, got, carried, want)
				}
			}
		}
	}
}

func TestDeriveDanglingOriginAfterDrop(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	if _, err := s.Drop("t1"); err != nil {
		t.Fatal(err)
	}
	if got := s.entity("t5").Origin; got != "t1" {
		t.Errorf("origin of a derived entity = %q, want t1 kept", got)
	}
	if err := b.Check(); err != nil {
		t.Errorf("a dangling origin is refused: %v", err)
	}
}

func TestDrop(t *testing.T) {
	b := sampleBase(t)
	s := b.Spaces[0]
	unlinked, err := s.Drop("t1")
	if err != nil {
		t.Fatal(err)
	}
	// t1 is picked by g1, and blocks t2. Its origin link in t5 is not a relation, so t5 keeps it.
	if unlinked != 2 {
		t.Errorf("unlinked = %d, want 2", unlinked)
	}
	if !reflect.DeepEqual(s.entity("g1").Values["picks"], []string{"t2"}) {
		t.Errorf("picks = %#v", s.entity("g1").Values["picks"])
	}
	if _, ok := s.entity("t2").Values["blocked_by"]; ok {
		t.Errorf("an emptied relation kept a value")
	}
	if s.entity("t1") != nil {
		t.Errorf("the entity is still there")
	}
	if s.entity("t5").Origin != "t1" {
		t.Errorf("origin was changed")
	}
	if err := b.Check(); err != nil {
		t.Errorf("after Drop: %v", err)
	}

	// A goal with three tasks and a note: four links go.
	b = sampleBase(t)
	s = b.Spaces[0]
	if n, err := s.Drop("g1"); err != nil || n != 4 {
		t.Errorf("Drop(g1) = %d, %v; want 4", n, err)
	}
	if s.entity("t1").Values["goal"] != nil {
		t.Errorf("goal was not unlinked from t1")
	}

	// An entity nothing points to.
	b = sampleBase(t)
	if n, err := b.Spaces[0].Drop("t4"); err != nil || n != 0 {
		t.Errorf("Drop(t4) = %d, %v", n, err)
	}
	if _, err := b.Spaces[0].Drop("zz"); err == nil {
		t.Errorf("an unknown entity was dropped")
	}
}

func TestBaseEditIdentityAfterRefusal(t *testing.T) {
	b := sampleBase(t)
	before := sampleBase(t)
	s := b.Spaces[0]
	if _, err := s.PutEntity(b, EntityInput{ID: "t4", Status: strp("完成")}, editNow); err == nil {
		t.Fatal("an illegal move was accepted")
	}
	if _, err := s.PutEntity(b, EntityInput{ID: "t2", Values: map[string]any{"effort": "many"}}, editNow); err == nil {
		t.Fatal("a malformed number was accepted")
	}
	if !sameBase(b, before) {
		t.Errorf("refused entity changes altered the base")
	}
}
