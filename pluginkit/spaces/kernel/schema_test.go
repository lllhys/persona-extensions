package kernel

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sampleSpace is a small space that uses every kind, both rollup directions and every built-in field kind. Its
// entities are the same in every test that uses it.
func sampleSpace() *Space {
	created := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	return &Space{
		ID:   "main",
		Name: "示例",
		Types: []*Type{
			{
				Key: "goal", Name: "目标",
				Flow: []Stage{{Name: "规划", Phase: Todo}, {Name: "进行", Phase: Doing}, {Name: "达成", Phase: Done}, {Name: "放弃", Phase: Dropped}},
				Props: []Prop{
					{Key: "summary", Name: "简介", Kind: KindText},
					{Key: "area", Name: "领域", Kind: KindEnum, Options: []string{"工作", "生活"}},
					{Key: "due", Name: "截止", Kind: KindDate},
					{Key: "picks", Name: "挑选", Kind: KindRelation, Target: "task", Many: true, Back: "目标"},
					{Key: "tasks", Name: "任务数", Kind: KindRollup, Via: "task.goal", Op: "count"},
					{Key: "progress", Name: "完成度", Kind: KindRollup, Via: "task.goal", Op: "percent_done"},
					{Key: "effort", Name: "总投入", Kind: KindRollup, Via: "task.goal", Op: "sum", Of: "effort", Unit: "小时"},
					{Key: "notes", Name: "记录数", Kind: KindRollup, Via: "note.about", Op: "count"},
					{Key: "picked", Name: "挑选平均", Kind: KindRollup, Via: "picks", Op: "avg", Of: "effort", Unit: "小时"},
				},
				Card:  Card{Lead: "summary", Pills: []string{"area", "due"}, Progress: &Ratio{Of: "progress"}},
				Views: []View{{Name: "列表", Kind: "list", Lens: Lens{Group: "area"}}},
			},
			{
				Key: "task", Name: "任务",
				Flow: []Stage{{Name: "待办", Phase: Todo}, {Name: "进行", Phase: Doing}, {Name: "完成", Phase: Done}, {Name: "取消", Phase: Dropped}},
				Props: []Prop{
					{Key: "goal", Name: "目标", Kind: KindRelation, Target: "goal", Back: "任务"},
					{Key: "effort", Name: "预估", Kind: KindNumber, Unit: "小时"},
					{Key: "blocked_by", Name: "被阻塞", Kind: KindRelation, Target: "task", Many: true, Back: "阻塞了"},
				},
				Views: []View{{Name: "看板", Kind: "board"}},
			},
			{
				Key: "note", Name: "记录",
				Props: []Prop{
					{Key: "about", Name: "关于", Kind: KindRelation, Target: "goal", Back: "记录"},
					{Key: "body", Name: "内容", Kind: KindText},
					{Key: "link", Name: "链接", Kind: KindURL},
					{Key: "read", Name: "已读", Kind: KindBoolean},
				},
			},
		},
		Entities: []*Entity{
			{ID: "g1", Type: "goal", Title: "目标一", Status: "进行", Created: created, Updated: updated,
				Values: map[string]any{"summary": "示例简介", "area": "工作", "due": "2026-12-31", "picks": []string{"t1", "t2"}}},
			{ID: "g2", Type: "goal", Title: "目标二", Status: "规划", Created: created, Updated: updated,
				Values: map[string]any{"area": "生活"}},
			{ID: "t1", Type: "task", Title: "任务一", Status: "完成", Created: created, Updated: updated,
				Values: map[string]any{"goal": "g1", "effort": 3.0}},
			{ID: "t2", Type: "task", Title: "任务二", Status: "进行", Created: created, Updated: updated,
				Values: map[string]any{"goal": "g1", "effort": 5.0, "blocked_by": []any{"t1"}}},
			{ID: "t3", Type: "task", Title: "任务三", Status: "取消", Created: created, Updated: updated,
				Values: map[string]any{"goal": "g1", "effort": 2.0}},
			{ID: "t4", Type: "task", Title: "任务四", Status: "待办", Created: created, Updated: updated},
			{ID: "n1", Type: "note", Title: "记录一", Created: created, Updated: updated,
				Values: map[string]any{"about": "g1", "body": "内容", "link": "https://example.com/a", "read": true}},
			{ID: "t5", Type: "task", Title: "衍生任务", Status: "待办", Origin: "t1", Created: created, Updated: updated},
		},
	}
}

// sampleBase wraps sampleSpace and checks it, so every test starts from a canonical, valid Base.
func sampleBase(t *testing.T) *Base {
	t.Helper()
	b := &Base{Format: 1, Seq: 10, Spaces: []*Space{sampleSpace()}}
	if err := b.Check(); err != nil {
		t.Fatalf("sample base is invalid: %v", err)
	}
	return b
}

func TestBaseCheckSample(t *testing.T) {
	b := sampleBase(t)
	if err := b.Check(); err != nil {
		t.Fatalf("second check: %v", err)
	}
}

func TestBaseCheckRefusals(t *testing.T) {
	big := make([]*Entity, MaxEntities+1)
	for i := range big {
		big[i] = &Entity{ID: "x" + strconv.Itoa(i), Type: "note", Title: "t"}
	}
	long := strings.Repeat("字", MaxTitle+1)
	tests := []struct {
		name   string
		mutate func(b *Base)
	}{
		{"format", func(b *Base) { b.Format = 2 }},
		{"too many spaces", func(b *Base) {
			b.Spaces = nil
			for i := 0; i <= MaxSpaces; i++ {
				b.Spaces = append(b.Spaces, &Space{ID: "s" + strconv.Itoa(i), Name: "n"})
			}
		}},
		{"nil space", func(b *Base) { b.Spaces = append(b.Spaces, nil) }},
		{"bad space id", func(b *Base) { b.Spaces[0].ID = "Bad_ID" }},
		{"duplicate space id", func(b *Base) { b.Spaces = append(b.Spaces, &Space{ID: "main", Name: "n"}) }},
		{"space name empty", func(b *Base) { b.Spaces[0].Name = "" }},
		{"space about too long", func(b *Base) { b.Spaces[0].About = strings.Repeat("字", MaxAbout+1) }},
		{"too many types", func(b *Base) {
			for i := 0; i < MaxTypes; i++ {
				b.Spaces[0].Types = append(b.Spaces[0].Types, &Type{Key: "k" + strconv.Itoa(i), Name: "n"})
			}
		}},
		{"nil type", func(b *Base) { b.Spaces[0].Types = append(b.Spaces[0].Types, nil) }},
		{"bad type key", func(b *Base) { b.Spaces[0].Types[0].Key = "Goal" }},
		{"duplicate type key", func(b *Base) { b.Spaces[0].Types[1].Key = "goal" }},
		{"type name empty", func(b *Base) { b.Spaces[0].Types[0].Name = "" }},
		{"flow with one stage", func(b *Base) { b.Spaces[0].Types[0].Flow = []Stage{{Name: "a", Phase: Todo}} }},
		{"too many props", func(b *Base) {
			for i := 0; i < MaxProps; i++ {
				b.Spaces[0].Types[2].Props = append(b.Spaces[0].Types[2].Props, Prop{Key: "p" + strconv.Itoa(i), Name: "n", Kind: KindText})
			}
		}},
		{"bad prop key", func(b *Base) { b.Spaces[0].Types[2].Props[0].Key = "Body" }},
		{"prop shadows a built-in field", func(b *Base) { b.Spaces[0].Types[2].Props[1].Key = "title" }},
		{"duplicate prop key", func(b *Base) { b.Spaces[0].Types[2].Props[1].Key = "about" }},
		{"prop name empty", func(b *Base) { b.Spaces[0].Types[2].Props[0].Name = "" }},
		{"unknown kind", func(b *Base) { b.Spaces[0].Types[2].Props[0].Kind = "color" }},
		{"option outside the kind's options", func(b *Base) { b.Spaces[0].Types[2].Props[1].Unit = "页" }},
		{"enum without options", func(b *Base) { b.Spaces[0].Types[0].Props[1].Options = nil }},
		{"relation without target", func(b *Base) { b.Spaces[0].Types[2].Props[0].Target = "" }},
		{"relation to a missing type", func(b *Base) { b.Spaces[0].Types[2].Props[0].Target = "nope" }},
		{"card lead does not resolve", func(b *Base) { b.Spaces[0].Types[0].Card.Lead = "nope" }},
		{"too many pills", func(b *Base) {
			b.Spaces[0].Types[0].Card.Pills = []string{"area", "due", "tasks", "notes", "effort"}
		}},
		{"pill does not resolve", func(b *Base) { b.Spaces[0].Types[0].Card.Pills = []string{"nope"} }},
		{"progress does not resolve", func(b *Base) { b.Spaces[0].Types[0].Card.Progress = &Ratio{Of: "nope"} }},
		{"progress is not numeric", func(b *Base) { b.Spaces[0].Types[0].Card.Progress = &Ratio{Of: "summary"} }},
		{"progress over is not numeric", func(b *Base) {
			b.Spaces[0].Types[0].Card.Progress = &Ratio{Of: "progress", Over: "area"}
		}},
		{"too many views", func(b *Base) {
			for i := 0; i < MaxViews; i++ {
				b.Spaces[0].Types[1].Views = append(b.Spaces[0].Types[1].Views, View{Name: "v" + strconv.Itoa(i), Kind: "list"})
			}
		}},
		{"view name too long", func(b *Base) { b.Spaces[0].Types[1].Views[0].Name = strings.Repeat("字", MaxShort+1) }},
		{"duplicate view name", func(b *Base) {
			b.Spaces[0].Types[1].Views = append(b.Spaces[0].Types[1].Views, View{Name: "看板", Kind: "list"})
		}},
		{"unknown view kind", func(b *Base) { b.Spaces[0].Types[1].Views[0].Kind = "map" }},
		{"where does not resolve", func(b *Base) {
			b.Spaces[0].Types[1].Views[0].Where = []Cond{{Field: "nope", Op: "eq"}}
		}},
		{"too many where conditions", func(b *Base) {
			for i := 0; i <= MaxWhere; i++ {
				b.Spaces[0].Types[1].Views[0].Where = append(b.Spaces[0].Types[1].Views[0].Where, Cond{Field: "title", Op: "has"})
			}
		}},
		{"too many sort keys", func(b *Base) {
			for i := 0; i <= MaxSort; i++ {
				b.Spaces[0].Types[1].Views[0].Sort = append(b.Spaces[0].Types[1].Views[0].Sort, Order{By: "title"})
			}
		}},
		{"sort does not resolve", func(b *Base) { b.Spaces[0].Types[1].Views[0].Sort = []Order{{By: "nope"}} }},
		{"group does not resolve", func(b *Base) { b.Spaces[0].Types[1].Views[0].Group = "nope" }},
		{"too many aggregates", func(b *Base) {
			for i := 0; i <= MaxAgg; i++ {
				b.Spaces[0].Types[1].Views[0].Agg = append(b.Spaces[0].Types[1].Views[0].Agg, Agg{Op: "count"})
			}
		}},
		{"unknown aggregate", func(b *Base) { b.Spaces[0].Types[1].Views[0].Agg = []Agg{{Op: "median"}} }},
		{"operand aggregate on a text", func(b *Base) {
			b.Spaces[0].Types[1].Views[0].Agg = []Agg{{Op: "sum", Of: "title"}}
		}},
		{"operand aggregate without operand", func(b *Base) { b.Spaces[0].Types[1].Views[0].Agg = []Agg{{Op: "sum"}} }},
		{"count with an operand", func(b *Base) { b.Spaces[0].Types[1].Views[0].Agg = []Agg{{Op: "count", Of: "effort"}} }},
		{"too many entities", func(b *Base) { b.Spaces[0].Entities = big }},
		{"nil entity", func(b *Base) { b.Spaces[0].Entities = append(b.Spaces[0].Entities, nil) }},
		{"empty entity id", func(b *Base) { b.Spaces[0].Entities[0].ID = "" }},
		{"duplicate entity id", func(b *Base) { b.Spaces[0].Entities[1].ID = "g1" }},
		{"entity of an unknown type", func(b *Base) { b.Spaces[0].Entities[0].Type = "nope" }},
		{"entity title empty", func(b *Base) { b.Spaces[0].Entities[0].Title = "" }},
		{"entity title has surrounding space", func(b *Base) { b.Spaces[0].Entities[0].Title = " 目标 " }},
		{"entity title too long", func(b *Base) { b.Spaces[0].Entities[0].Title = long }},
		{"status on a type without flow", func(b *Base) { b.Spaces[0].Entities[6].Status = "进行" }},
		{"status missing on a type with flow", func(b *Base) { b.Spaces[0].Entities[0].Status = "" }},
		{"status not in the flow", func(b *Base) { b.Spaces[0].Entities[0].Status = "没有" }},
		{"origin is the entity itself", func(b *Base) { b.Spaces[0].Entities[0].Origin = "g1" }},
		{"unknown value key", func(b *Base) { b.Spaces[0].Entities[0].Values["nope"] = "x" }},
		{"stored value of a rollup", func(b *Base) { b.Spaces[0].Entities[0].Values["tasks"] = 3.0 }},
		{"value that does not parse", func(b *Base) { b.Spaces[0].Entities[2].Values["effort"] = "很多" }},
		{"relation to a missing entity", func(b *Base) { b.Spaces[0].Entities[2].Values["goal"] = "zzz" }},
		{"relation to an entity of another type", func(b *Base) { b.Spaces[0].Entities[2].Values["goal"] = "t1" }},
		{"relation to itself", func(b *Base) { b.Spaces[0].Entities[2].Values["blocked_by"] = []any{"t1"} }},
		{"relation to an entity of another space", func(b *Base) {
			other := &Space{ID: "other", Name: "其他",
				Types:    []*Type{{Key: "goal", Name: "目标"}},
				Entities: []*Entity{{ID: "x1", Type: "goal", Title: "别处"}}}
			b.Spaces = append(b.Spaces, other)
			b.Spaces[0].Entities[2].Values["goal"] = "x1"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := sampleBase(t)
			tt.mutate(b)
			if err := b.Check(); err == nil {
				t.Fatalf("Check accepted an invalid base")
			} else if err.Error() == "" {
				t.Fatalf("Check returned an empty message")
			}
		})
	}
}

func TestBaseCheckAllowsDanglingOrigin(t *testing.T) {
	b := sampleBase(t)
	b.Spaces[0].Entities[len(b.Spaces[0].Entities)-1].Origin = "gone"
	if err := b.Check(); err != nil {
		t.Fatalf("dangling origin refused: %v", err)
	}
}

func TestBaseCheckNormalizesValues(t *testing.T) {
	b := sampleBase(t)
	e := b.Spaces[0].Entities[0]
	e.Values["picks"] = []any{"t1", "t1", " t2 ", ""}
	e.Values["area"] = "  工作 "
	e.Values["summary"] = "   "
	e.Values["due"] = nil
	if err := b.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got, want := e.Values["picks"], []string{"t1", "t2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("picks = %#v, want %#v", got, want)
	}
	if got := e.Values["area"]; got != "工作" {
		t.Errorf("area = %#v, want 工作", got)
	}
	if _, ok := e.Values["summary"]; ok {
		t.Errorf("a blank text is unset, but summary is still stored")
	}
	if _, ok := e.Values["due"]; ok {
		t.Errorf("a nil value is unset, but due is still stored")
	}
}

func TestBaseJSONRoundTrip(t *testing.T) {
	b := sampleBase(t)
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var back Base
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if err := back.Check(); err != nil {
		t.Fatalf("decoded base is invalid: %v", err)
	}
	if !reflect.DeepEqual(b, &back) {
		t.Errorf("round trip changed the base")
	}
}

func TestSpaceField(t *testing.T) {
	s := sampleSpace()
	goal, task := s.Types[0], s.Types[1]
	tests := []struct {
		name     string
		t        *Type
		key      string
		wantOK   bool
		wantKind string
	}{
		{"title", goal, "title", true, KindText},
		{"status", goal, "status", true, KindEnum},
		{"phase", goal, "phase", true, KindEnum},
		{"type", goal, "type", true, KindEnum},
		{"origin", goal, "origin", true, KindRelation},
		{"created", goal, "created", true, KindDate},
		{"updated", task, "updated", true, KindDate},
		{"declared", goal, "summary", true, KindText},
		{"declared rollup", goal, "tasks", true, KindRollup},
		{"unknown", goal, "nope", false, ""},
		{"nil type", nil, "title", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := s.Field(tt.t, tt.key)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && p.Kind != tt.wantKind {
				t.Errorf("kind = %q, want %q", p.Kind, tt.wantKind)
			}
		})
	}
	status, _ := s.Field(goal, "status")
	if !reflect.DeepEqual(status.Options, []string{"规划", "进行", "达成", "放弃"}) {
		t.Errorf("status options = %v, want the stages in flow order", status.Options)
	}
	typ, _ := s.Field(goal, "type")
	if !reflect.DeepEqual(typ.Options, []string{"goal", "task", "note"}) {
		t.Errorf("type options = %v", typ.Options)
	}
	phase, _ := s.Field(task, "phase")
	if !reflect.DeepEqual(phase.Options, []string{"todo", "doing", "done", "dropped"}) {
		t.Errorf("phase options = %v", phase.Options)
	}
	noFlow, _ := s.Field(s.Types[2], "status")
	if len(noFlow.Options) != 0 {
		t.Errorf("status of a type without flow has options %v", noFlow.Options)
	}
}

func TestCardOrDefault(t *testing.T) {
	s := sampleSpace()
	goal := s.Types[0]
	if got := goal.CardOrDefault(); !reflect.DeepEqual(got, goal.Card) {
		t.Errorf("an explicit card changed: %+v", got)
	}
	task := s.Types[1]
	got := task.CardOrDefault()
	if got.Lead != "" {
		t.Errorf("task has no text property, lead = %q", got.Lead)
	}
	if !reflect.DeepEqual(got.Pills, []string{"goal", "effort", "blocked_by"}) {
		t.Errorf("pills = %v", got.Pills)
	}
	if got.Progress != nil {
		t.Errorf("task has no percent rollup, progress = %+v", got.Progress)
	}
	empty := &Type{Key: "x", Name: "x"}
	if c := empty.CardOrDefault(); c.Lead != "" || len(c.Pills) != 0 || c.Progress != nil {
		t.Errorf("empty type card = %+v", c)
	}
	withText := &Type{Key: "x", Name: "x", Props: []Prop{
		{Key: "a", Name: "a", Kind: KindNumber},
		{Key: "b", Name: "b", Kind: KindText},
		{Key: "c", Name: "c", Kind: KindEnum, Options: []string{"o"}},
		{Key: "d", Name: "d", Kind: KindDate},
		{Key: "e", Name: "e", Kind: KindBoolean},
		{Key: "f", Name: "f", Kind: KindURL},
		{Key: "g", Name: "g", Kind: KindBoolean},
		{Key: "h", Name: "h", Kind: KindRollup, Op: "percent_done", Via: "a"},
	}}
	c := withText.CardOrDefault()
	if c.Lead != "b" {
		t.Errorf("lead = %q, want b", c.Lead)
	}
	if !reflect.DeepEqual(c.Pills, []string{"a", "c", "d"}) {
		t.Errorf("pills = %v, want the first three non-text properties", c.Pills)
	}
	if c.Progress == nil || c.Progress.Of != "h" {
		t.Errorf("progress = %+v, want the percent rollup h", c.Progress)
	}
}

func TestViewsOrDefaultAndColumns(t *testing.T) {
	empty := &Type{Key: "x", Name: "x"}
	if v := empty.ViewsOrDefault(); len(v) != 1 || v[0].Kind != "list" || v[0].Name != "列表" {
		t.Errorf("default views = %+v", v)
	}
	task := sampleSpace().Types[1]
	if v := task.ViewsOrDefault(); !reflect.DeepEqual(v, task.Views) {
		t.Errorf("declared views changed")
	}
	if got, want := task.Columns(), []string{"title", "status", "goal", "effort", "blocked_by"}; !reflect.DeepEqual(got, want) {
		t.Errorf("columns = %v, want %v", got, want)
	}
	if got := empty.Columns(); !reflect.DeepEqual(got, []string{"title"}) {
		t.Errorf("columns of an empty type = %v", got)
	}
	wide := &Type{Key: "w", Name: "w"}
	for i := 0; i < 12; i++ {
		wide.Props = append(wide.Props, Prop{Key: "p" + strconv.Itoa(i), Name: "n", Kind: KindText})
	}
	if got := wide.Columns(); len(got) != 8 {
		t.Errorf("wide type has %d columns, want 8", len(got))
	}
	flowOnly := &Type{Key: "f", Name: "f", Flow: []Stage{{Name: "a", Phase: Todo}, {Name: "b", Phase: Done}}}
	if got := flowOnly.Columns(); !reflect.DeepEqual(got, []string{"title", "status"}) {
		t.Errorf("columns with flow = %v", got)
	}
}

func TestKeyAndSpaceIDRules(t *testing.T) {
	tests := []struct {
		s         string
		key, slug bool
	}{
		{"a", true, true},
		{"goal_1", true, false},
		{"g-1", false, true},
		{"", false, false},
		{"1a", false, true},
		{"-a", false, false},
		{"A", false, false},
		{"a b", false, false},
		{"a" + strings.Repeat("b", 31), true, true},
		{"a" + strings.Repeat("b", 32), false, false},
		{strings.Repeat("c", 32), true, true},
		{"x" + strings.Repeat("y", 32), false, false},
	}
	for _, tt := range tests {
		if got := validKey(tt.s); got != tt.key {
			t.Errorf("validKey(%q) = %v, want %v", tt.s, got, tt.key)
		}
		if got := validSpaceID(tt.s); got != tt.slug {
			t.Errorf("validSpaceID(%q) = %v, want %v", tt.s, got, tt.slug)
		}
	}
}

func TestInRunes(t *testing.T) {
	tests := []struct {
		s      string
		lo, hi int
		want   bool
	}{
		{"", 1, 3, false},
		{"a", 1, 3, true},
		{"字字字", 1, 3, true},
		{"字字字字", 1, 3, false},
		{"ab", 2, 2, true},
	}
	for _, tt := range tests {
		if got := inRunes(tt.s, tt.lo, tt.hi); got != tt.want {
			t.Errorf("inRunes(%q, %d, %d) = %v, want %v", tt.s, tt.lo, tt.hi, got, tt.want)
		}
	}
	if got := runeLen("字a"); got != 2 {
		t.Errorf("runeLen = %d, want 2", got)
	}
}
