package templates

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

var testNow = time.Date(2026, time.November, 10, 9, 30, 0, 0, time.UTC)

// applied creates a base holding the template with its samples and returns the base, its only space and its world.
func applied(t *testing.T, key string, samples bool) (*kernel.Base, *kernel.Space, *kernel.World) {
	t.Helper()
	b := &kernel.Base{Format: 1}
	sp, err := Apply(b, key, "", samples, testNow)
	if err != nil {
		t.Fatalf("Apply(%q, samples=%v): %v", key, samples, err)
	}
	return b, sp, kernel.Open(sp)
}

// entityTitled returns the entity with the title, failing the test when there is none.
func entityTitled(t *testing.T, sp *kernel.Space, title string) *kernel.Entity {
	t.Helper()
	for _, e := range sp.Entities {
		if e.Title == title {
			return e
		}
	}
	t.Fatalf("no entity titled %q", title)
	return nil
}

// number is the float value of a field, failing the test when the field is not a number.
func number(t *testing.T, w *kernel.World, e *kernel.Entity, key string) float64 {
	t.Helper()
	f, ok := w.Value(e, key).(float64)
	if !ok {
		t.Fatalf("%s.%s = %#v, want a number", e.Title, key, w.Value(e, key))
	}
	return f
}

func TestTemplatesInOrder(t *testing.T) {
	var keys []string
	for _, tpl := range All() {
		keys = append(keys, tpl.Key)
	}
	if want := []string{"project", "reading", "inventory", "blank"}; !slices.Equal(keys, want) {
		t.Fatalf("All() keys = %v, want %v", keys, want)
	}
	for _, key := range keys {
		tpl, ok := Get(key)
		if !ok || tpl.Key != key {
			t.Errorf("Get(%q) = %q, %v", key, tpl.Key, ok)
		}
		if tpl.Name == "" || tpl.Icon == "" || tpl.About == "" {
			t.Errorf("template %q has an empty name, icon or about", key)
		}
	}
	if _, ok := Get("nope"); ok {
		t.Error("Get of an unknown key found a template")
	}
}

func TestAllReturnsACopy(t *testing.T) {
	ts := All()
	ts[0] = Template{}
	if All()[0].Key != "project" {
		t.Error("All shares its slice with the caller")
	}
}

func TestEveryTemplateAppliesWithAndWithoutSamples(t *testing.T) {
	for _, tpl := range All() {
		for _, samples := range []bool{false, true} {
			b, sp, _ := applied(t, tpl.Key, samples)
			if err := b.Check(); err != nil {
				t.Errorf("%s samples=%v: Check: %v", tpl.Key, samples, err)
			}
			if sp.Name != tpl.Name || sp.Icon != tpl.Icon || sp.About != tpl.About {
				t.Errorf("%s: space = %q %q %q, want the template's name, icon and about", tpl.Key, sp.Name, sp.Icon, sp.About)
			}
			if len(sp.Types) != len(tpl.Types) {
				t.Errorf("%s: %d types, want %d", tpl.Key, len(sp.Types), len(tpl.Types))
			}
			want := 0
			if samples {
				want = len(tpl.Samples)
			}
			if len(sp.Entities) != want {
				t.Errorf("%s samples=%v: %d entities, want %d", tpl.Key, samples, len(sp.Entities), want)
			}
		}
	}
}

func TestUnionUsesEveryFeature(t *testing.T) {
	kinds := map[string]bool{}
	views := map[string]bool{}
	var viaProp, viaBacklink, derived bool
	for _, tpl := range All() {
		for _, typ := range tpl.Types {
			for _, p := range typ.Props {
				kinds[p.Kind] = true
				if p.Kind == kernel.KindRollup {
					if strings.Contains(p.Via, ".") {
						viaBacklink = true
					} else {
						viaProp = true
					}
				}
			}
			for _, v := range typ.Views {
				views[v.Kind] = true
			}
		}
		for _, s := range tpl.Samples {
			if s.From != "" {
				derived = true
			}
		}
	}
	for _, k := range kernel.Kinds {
		if !kinds[k.Name] {
			t.Errorf("no template uses the property kind %q", k.Name)
		}
	}
	for _, v := range []string{"list", "board", "table", "progress", "feed"} {
		if !views[v] {
			t.Errorf("no template uses the view kind %q", v)
		}
	}
	if !viaProp {
		t.Error("no rollup counts its own relation (Via without a dot)")
	}
	if !viaBacklink {
		t.Error("no rollup counts the backlinks of a relation (Via with a dot)")
	}
	if !derived {
		t.Error("no sample is derived")
	}
}

func TestRollupValues(t *testing.T) {
	cases := []struct {
		key, title, prop string
		want             float64
	}{
		{"project", "连续写满 12 篇周复盘", "progress", 0.5},
		{"project", "连续写满 12 篇周复盘", "tasks", 4},
		{"project", "连续写满 12 篇周复盘", "effort", 7},
		{"project", "完成一次半程跑", "progress", 0},
		{"project", "完成一次半程跑", "tasks", 2},
		{"reading", "怎样读才留得住", "sources", 3},
		{"reading", "《慢读的方法》", "notes", 2},
		{"reading", "《阳台种菜手记》", "notes", 1},
		{"inventory", "周末露营", "ready", 0.75},
		{"inventory", "储物柜", "items", 4},
		{"inventory", "储物柜", "worth", 209},
		{"inventory", "书桌", "worth", 129},
	}
	for _, c := range cases {
		_, sp, w := applied(t, c.key, true)
		e := entityTitled(t, sp, c.title)
		if got := number(t, w, e, c.prop); got != c.want {
			t.Errorf("%s %q.%s = %v, want %v", c.key, c.title, c.prop, got, c.want)
		}
	}
}

func TestSampleValuesAndProvenance(t *testing.T) {
	_, sp, w := applied(t, "project", true)
	idea := entityTitled(t, sp, "每周写一篇复盘")
	goal := entityTitled(t, sp, "连续写满 12 篇周复盘")
	if goal.Origin != idea.ID {
		t.Errorf("derived goal origin = %q, want the idea %q", goal.Origin, idea.ID)
	}
	if goal.Values["summary"] != idea.Values["summary"] || goal.Values["area"] != "学习" {
		t.Errorf("derived goal did not inherit summary and area: %v", goal.Values)
	}
	if goal.Status != "进行中" {
		t.Errorf("derived goal status = %q, want 进行中", goal.Status)
	}
	if got := w.Value(goal, "due"); got != "2027-01-09" {
		t.Errorf("due +60d = %v, want 2027-01-09", got)
	}
	if got := w.Value(entityTitled(t, sp, "完成一次半程跑"), "due"); got != "2027-03-10" {
		t.Errorf("due +120d = %v, want 2027-03-10", got)
	}
	if got := w.Value(entityTitled(t, sp, "写第 2 篇"), "due"); got != "2026-11-17" {
		t.Errorf("due +7d = %v, want 2026-11-17", got)
	}
	friend := entityTitled(t, sp, "约一位朋友互相督促")
	blocker := entityTitled(t, sp, "写第 2 篇")
	if got, _ := w.Value(friend, "blocked_by").([]string); !slices.Equal(got, []string{blocker.ID}) {
		t.Errorf("blocked_by = %v, want [%s]", got, blocker.ID)
	}
	if last := sp.Entities[len(sp.Entities)-1]; !last.Updated.Equal(testNow) || !last.Created.Equal(testNow) {
		t.Errorf("last sample stamped %v, want %v", last.Updated, testNow)
	}
	if first := sp.Entities[0]; !first.Created.Equal(testNow.Add(-time.Duration(len(sp.Entities)-1) * time.Second)) {
		t.Errorf("first sample stamped %v", first.Created)
	}

	_, sp, w = applied(t, "reading", true)
	clip := entityTitled(t, sp, "间隔复习的做法")
	note := entityTitled(t, sp, "间隔复习的要点")
	if note.Origin != clip.ID || note.Values["gist"] != clip.Values["gist"] {
		t.Errorf("derived note: origin %q gist %v, want clip %q and its gist", note.Origin, note.Values["gist"], clip.ID)
	}
	if got := w.Value(entityTitled(t, sp, "《阳台种菜手记》"), "finished"); got != "2026-10-21" {
		t.Errorf("finished -20d = %v, want 2026-10-21", got)
	}

	_, sp, w = applied(t, "inventory", true)
	var wish, chair *kernel.Entity
	for _, e := range sp.Entities {
		switch {
		case e.Type == "wish" && e.Title == "折叠椅":
			wish = e
		case e.Type == "item" && e.Title == "折叠椅":
			chair = e
		}
	}
	if wish == nil || chair == nil || chair.Origin != wish.ID {
		t.Fatalf("derived item = %v, want an item derived from the wish %q", chair, wish.ID)
	}
	if chair.Status != "充足" || chair.Values["category"] != "工具" || chair.Values["price"] != 150.0 {
		t.Errorf("derived item status %q values %v, want 充足 with the wish's category and price", chair.Status, chair.Values)
	}
	if got := w.Value(entityTitled(t, sp, "大米"), "expires"); got != "2027-05-29" {
		t.Errorf("expires +200d = %v, want 2027-05-29", got)
	}
}

func TestNameDefaultsAndGiven(t *testing.T) {
	b := &kernel.Base{Format: 1}
	sp, err := Apply(b, "reading", "我的书架", false, testNow)
	if err != nil || sp.Name != "我的书架" {
		t.Fatalf("Apply with a name: %v, %q", err, sp.Name)
	}
	b = &kernel.Base{Format: 1}
	sp, err = Apply(b, "reading", "", false, testNow)
	if err != nil || sp.Name != "阅读与知识" {
		t.Fatalf("Apply without a name: %v, %q", err, sp.Name)
	}
}

func TestApplyUnknownKey(t *testing.T) {
	b := &kernel.Base{Format: 1}
	if _, err := Apply(b, "nope", "", true, testNow); err == nil {
		t.Fatal("Apply of an unknown template succeeded")
	}
	if len(b.Spaces) != 0 {
		t.Error("a failed Apply changed the base")
	}
}

func TestRelativeDate(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"+30d", "2026-12-10", true},
		{"-20d", "2026-10-21", true},
		{"+0d", "2026-11-10", true},
		{"-0d", "2026-11-10", true},
		{"+1d", "2026-11-11", true},
		{"30d", "", false},
		{"+d", "", false},
		{"+3x", "", false},
		{"+x3d", "", false},
		{"+1-2d", "", false},
		{"+99999999999999999999d", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := relativeDate(c.in, testNow)
		if got != c.want || ok != c.ok {
			t.Errorf("relativeDate(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// Custom bundles for the error paths and the two-pass rule.

var (
	noteT = kernel.Type{Key: "note", Name: "笔记", Props: []kernel.Prop{
		{Key: "gist", Name: "要点", Kind: kernel.KindText},
		{Key: "page", Name: "页码", Kind: kernel.KindNumber},
		{Key: "due", Name: "截止", Kind: kernel.KindDate},
	}}
	taskT = kernel.Type{Key: "task", Name: "任务",
		Flow: []kernel.Stage{
			{Name: "待办", Phase: kernel.Todo},
			{Name: "完成", Phase: kernel.Done},
			{Name: "取消", Phase: kernel.Dropped},
		},
		Props: []kernel.Prop{
			{Key: "gist", Name: "要点", Kind: kernel.KindText},
			{Key: "to", Name: "到", Kind: kernel.KindRelation, Target: "note", Many: true},
		},
	}
)

func custom(samples ...Sample) Template {
	return Template{Key: "custom", Name: "自定义", Icon: "list", About: "测试", Types: []kernel.Type{noteT, taskT}, Samples: samples}
}

func TestApplyFailureLeavesBaseUnchanged(t *testing.T) {
	cases := map[string]Template{
		"duplicate ref": custom(Sample{Ref: "a", Type: "note", Title: "一"}, Sample{Ref: "a", Type: "note", Title: "二"}),
		"empty ref":     custom(Sample{Type: "note", Title: "一"}),
		"unknown type":  custom(Sample{Ref: "a", Type: "nope", Title: "一"}),
		"unknown property": custom(Sample{Ref: "a", Type: "note", Title: "一",
			Values: map[string]any{"missing": "x"}}),
		"date not a date": custom(Sample{Ref: "a", Type: "note", Title: "一",
			Values: map[string]any{"due": 5.0}}),
		"bad relative date": custom(Sample{Ref: "a", Type: "note", Title: "一",
			Values: map[string]any{"due": "+xd"}}),
		"unknown ref in a relation": custom(Sample{Ref: "a", Type: "task", Title: "一",
			Values: map[string]any{"to": "@missing"}}),
		"unknown ref in a relation list": custom(Sample{Ref: "a", Type: "task", Title: "一",
			Values: map[string]any{"to": []any{"@missing"}}}),
		"derive from a sample not yet created": custom(Sample{Ref: "a", Type: "task", From: "later", Title: "一"}),
		"derive into an unknown type":          custom(Sample{Ref: "a", Type: "note", Title: "一"}, Sample{Ref: "b", Type: "nope", From: "a", Title: "二"}),
		"derive to an unreachable stage":       custom(Sample{Ref: "a", Type: "note", Title: "一"}, Sample{Ref: "b", Type: "task", From: "a", Status: "不存在", Title: "二"}),
		"non-string relation item": {Key: "custom", Name: "自定义", Types: []kernel.Type{
			{Key: "a", Name: "甲", Props: []kernel.Prop{{Key: "link", Name: "链接", Kind: kernel.KindRelation, Target: "a"}}},
		}, Samples: []Sample{{Ref: "x", Type: "a", Title: "一", Values: map[string]any{"link": []any{5.0}}}}},
		"relation not a list": {Key: "custom", Name: "自定义", Types: []kernel.Type{
			{Key: "a", Name: "甲", Props: []kernel.Prop{{Key: "link", Name: "链接", Kind: kernel.KindRelation, Target: "a"}}},
		}, Samples: []Sample{{Ref: "x", Type: "a", Title: "一", Values: map[string]any{"link": 5.0}}}},
		"bad type key":  {Key: "custom", Name: "自定义", Types: []kernel.Type{{Key: "BAD", Name: "坏"}}},
		"name too long": {Key: "custom", Name: "自定义", Types: []kernel.Type{noteT}, Samples: nil},
	}
	for name, tpl := range cases {
		b := &kernel.Base{Format: 1}
		before, _ := json.Marshal(b)
		var err error
		if name == "name too long" {
			long := strings.Repeat("长", 41)
			_, err = apply(b, tpl, long, true, testNow)
		} else {
			_, err = apply(b, tpl, "", true, testNow)
		}
		if err == nil {
			t.Errorf("%s: apply succeeded, want an error", name)
			continue
		}
		after, _ := json.Marshal(b)
		if string(before) != string(after) {
			t.Errorf("%s: a failed apply changed the base", name)
		}
	}
}

func TestDerivedStatusWalksThroughTheFlow(t *testing.T) {
	// A note derived into a task starts at the first stage; 完成 is one move away.
	tpl := custom(
		Sample{Ref: "n", Type: "note", Title: "笔记", Values: map[string]any{"gist": "要点"}},
		Sample{Ref: "t", Type: "task", From: "n", Status: "完成", Title: "任务"},
	)
	b := &kernel.Base{Format: 1}
	sp, err := apply(b, tpl, "", true, testNow)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	task := entityTitled(t, sp, "任务")
	if task.Status != "完成" || task.Values["gist"] != "要点" {
		t.Errorf("derived task status %q values %v", task.Status, task.Values)
	}
}

func TestDerivedValuesAreApplied(t *testing.T) {
	// The derived sample's own values are applied after the derivation: a date is resolved, and a text value that
	// looks like a relative date stays text.
	tpl := custom(
		Sample{Ref: "n", Type: "note", Title: "笔记"},
		Sample{Ref: "m", Type: "note", From: "n", Title: "副本", Values: map[string]any{"gist": "+5d", "due": "+1d"}},
	)
	b := &kernel.Base{Format: 1}
	sp, err := apply(b, tpl, "", true, testNow)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	dup := entityTitled(t, sp, "副本")
	if dup.Values["gist"] != "+5d" || dup.Values["due"] != "2026-11-11" {
		t.Errorf("derived values %v", dup.Values)
	}
}

func TestRelationReferencesAreResolved(t *testing.T) {
	tpl := custom(
		Sample{Ref: "a", Type: "note", Title: "一"},
		Sample{Ref: "b", Type: "task", Title: "二", Values: map[string]any{"to": "@a", "gist": "+5d"}},
		Sample{Ref: "c", Type: "task", Title: "三", Values: map[string]any{"to": []any{"@a"}}},
	)
	b := &kernel.Base{Format: 1}
	sp, err := apply(b, tpl, "", true, testNow)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	note := entityTitled(t, sp, "一")
	for _, title := range []string{"二", "三"} {
		e := entityTitled(t, sp, title)
		if got, _ := e.Values["to"].([]string); !slices.Equal(got, []string{note.ID}) {
			t.Errorf("%s: to = %v, want [%s]", title, e.Values["to"], note.ID)
		}
	}
	// a text value is never resolved as a reference or a date
	if got := entityTitled(t, sp, "二").Values["gist"]; got != "+5d" {
		t.Errorf("text value = %v, want +5d", got)
	}
}

// TestSkeletonDefersLaterRelations builds types that refer to each other: the first type points to a type declared
// after it, and the second points back.
func TestSkeletonDefersLaterRelations(t *testing.T) {
	a := kernel.Type{Key: "a", Name: "甲", Props: []kernel.Prop{
		{Key: "to_b", Name: "到乙", Kind: kernel.KindRelation, Target: "b"},
		{Key: "self", Name: "自己", Kind: kernel.KindRelation, Target: "a"},
		{Key: "n", Name: "数", Kind: kernel.KindRollup, Via: "b.to_a", Op: "count"},
	}, Card: kernel.Card{Lead: "self"}, Views: []kernel.View{{Name: "列表", Kind: "list"}}}
	b := kernel.Type{Key: "b", Name: "乙", Props: []kernel.Prop{
		{Key: "to_a", Name: "到甲", Kind: kernel.KindRelation, Target: "a", Back: "乙"},
	}}
	tpl := Template{Key: "mutual", Name: "互相", Types: []kernel.Type{a, b}}
	base := &kernel.Base{Format: 1}
	sp, err := apply(base, tpl, "", false, testNow)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(sp.Types) != 2 || len(sp.Types[0].Props) != 3 || sp.Types[0].Views == nil {
		t.Errorf("types after two passes = %+v", sp.Types)
	}

	// The skeleton of a type leaves out what the first pass cannot resolve.
	have := map[string]bool{}
	s := skeleton(a, have)
	if len(s.Props) != 1 || s.Props[0].Key != "self" || s.Card.Lead != "" || s.Views != nil {
		t.Errorf("skeleton of a with nothing added = %+v", s)
	}
	have["b"] = true
	if s := skeleton(a, have); len(s.Props) != 2 {
		t.Errorf("skeleton of a with b added keeps %d props, want 2", len(s.Props))
	}
}

func TestLoadRefusesBadBundles(t *testing.T) {
	good := `{"key":"x","name":"甲","icon":"i","about":"a","types":[]}`
	cases := map[string]fstest.MapFS{
		"missing file":  {},
		"not json":      {"x.json": {Data: []byte("{")}},
		"unknown field": {"x.json": {Data: []byte(`{"key":"x","nmae":"甲","types":[]}`)}},
		"wrong key":     {"x.json": {Data: []byte(`{"key":"y","name":"甲","types":[]}`)}},
	}
	for name, fsys := range cases {
		if _, err := load(fsys, []string{"x"}); err == nil {
			t.Errorf("%s: load succeeded", name)
		}
	}
	ts, err := load(fstest.MapFS{"x.json": {Data: []byte(good)}}, []string{"x"})
	if err != nil || len(ts) != 1 || ts[0].Name != "甲" {
		t.Fatalf("load of a good bundle = %v, %v", ts, err)
	}
}

func TestMustLoadPanicsOnBadBundle(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("mustLoad of a missing bundle did not panic")
		}
	}()
	mustLoad(fstest.MapFS{}, []string{"missing"})
}

func TestEmbeddedBundlesDecode(t *testing.T) {
	ts, err := load(bundles, order)
	if err != nil {
		t.Fatalf("embedded bundles: %v", err)
	}
	if len(ts) != len(order) {
		t.Fatalf("%d embedded bundles, want %d", len(ts), len(order))
	}
}
