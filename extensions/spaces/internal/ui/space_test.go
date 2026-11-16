package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/spaces/internal/templates"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/store"
)

// withSamples is the first template with sample records.
func withSamples(t *testing.T) templates.Template {
	t.Helper()
	for _, tp := range templates.All() {
		if len(tp.Samples) > 0 {
			return tp
		}
	}
	t.Fatal("no template has samples")
	return templates.Template{}
}

// spaceNamed is the space of st with the name; nil when there is none.
func spaceNamed(t *testing.T, st kernel.Storage, name string) *kernel.Space {
	t.Helper()
	var out *kernel.Space
	if err := st.View(func(b *kernel.Base) error {
		for _, sp := range b.Spaces {
			if sp.Name == name {
				out = sp
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// templateRowsOf are the rows of a view that open a template's form.
func templateRowsOf(v *wire.View) []wire.Component {
	var out []wire.Component
	for _, r := range componentsOf(v, wire.ComponentRow) {
		if r.Action != nil && strings.HasPrefix(r.Action.ID, "new:") {
			out = append(out, r)
		}
	}
	return out
}

// wantTemplateRows asserts a row per template, in order: its icon, name, about and number of types.
func wantTemplateRows(t *testing.T, v *wire.View) {
	t.Helper()
	rows := templateRowsOf(v)
	all := templates.All()
	if len(rows) != len(all) {
		t.Fatalf("template rows = %d, want %d", len(rows), len(all))
	}
	for i, tp := range all {
		r := rows[i]
		value := ""
		if len(tp.Types) > 0 {
			value = fmt.Sprintf("%d 类", len(tp.Types))
		}
		if r.Action.ID != "new:"+tp.Key || r.Title != tp.Name || r.Subtitle != tp.About || r.Value != value || r.Icon != iconName(tp.Icon) {
			t.Fatalf("row of %s = %+v", tp.Key, r)
		}
	}
}

func TestOverviewOpensTheTemplateList(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	id := firstSpace(t, st)
	v := drawPage(t, a, state{S: id}, 5)
	b, ok := buttonTitled(v, "新建空间")
	if !ok || b.Action == nil || b.Action.ID != "new" || b.Action.Push != nil {
		t.Fatalf("new space button = %+v", b)
	}
	res := act(t, a, state{S: id}, b.Action.ID, nil)
	got := decode(t, res.View)
	if got != (state{S: id, C: pickSlot}) || res.View.Title != "" {
		t.Fatalf("template list state = %+v, title %q", got, res.View.Title)
	}
	if head := res.View.Sections[0].Items[0]; head.Type != wire.ComponentHeader || head.Title != "新建空间" {
		t.Fatalf("template list header = %+v", head)
	}
	if res.View.Filter != nil || res.View.Tabs != nil {
		t.Fatalf("template list has chrome: %+v / %+v", res.View.Filter, res.View.Tabs)
	}
	wantTemplateRows(t, res.View)
	cancel, ok := buttonTitled(res.View, "取消")
	if !ok || cancel.Action.ID != "cancel" {
		t.Fatalf("cancel = %+v", cancel)
	}
	res = act(t, a, got, cancel.Action.ID, nil)
	if back := decode(t, res.View); back != (state{S: id}) || res.Toast != "" {
		t.Fatalf("after cancel = %+v / %q", back, res.Toast)
	}
}

func TestWelcomeRowsOpenTheForm(t *testing.T) {
	a := newApp(storeWith(t, true))
	v := drawPage(t, a, state{}, 5)
	if head := v.Sections[0].Items[0]; head.Type != wire.ComponentHeader || head.Title != "空间" {
		t.Fatalf("welcome header = %+v", head)
	}
	wantTemplateRows(t, v)
	// The template list with no space is the welcome, and leaves no new space in the state.
	v = drawPage(t, a, state{C: pickSlot}, 5)
	if got := decode(t, v); got.C != "" || v.Sections[0].Items[0].Title != "空间" {
		t.Fatalf("template list with no space = %+v", got)
	}
	for _, tp := range templates.All() {
		res := act(t, a, state{}, "new:"+tp.Key, nil)
		if got := decode(t, res.View); got.C != tp.Key {
			t.Fatalf("new:%s = %+v", tp.Key, got)
		}
		if _, ok := buttonTitled(res.View, "创建"); !ok {
			t.Fatalf("form of %s has no create", tp.Key)
		}
	}
}

func TestCreateFormOfEveryTemplate(t *testing.T) {
	a := newApp(storeWith(t, true))
	for _, tp := range templates.All() {
		t.Run(tp.Key, func(t *testing.T) {
			v := drawPage(t, a, state{C: tp.Key}, 5)
			head := v.Sections[0].Items[0]
			if head.Type != wire.ComponentHeader || head.Title != tp.Name || head.Subtitle != tp.About {
				t.Fatalf("form header = %+v", head)
			}
			captions := textOfCaptions(v)
			var names []string
			for _, ty := range tp.Types {
				names = append(names, ty.Name)
			}
			brings := "包含：" + strings.Join(names, "、")
			if len(tp.Types) > 0 && (len(captions) != 1 || captions[0] != brings) {
				t.Fatalf("captions = %v, want %q", captions, brings)
			}
			if len(tp.Types) == 0 && len(captions) != 0 {
				t.Fatalf("captions of a template without types = %v", captions)
			}
			name := fieldNamed(t, v, "name")
			if name.Kind != wire.FieldKindText || name.Label != "名称" || name.Value != tp.Name {
				t.Fatalf("name field = %+v", name)
			}
			samples, ok := fieldOf(v, "samples")
			if ok != (len(tp.Samples) > 0) {
				t.Fatalf("samples field present = %v, template has %d samples", ok, len(tp.Samples))
			}
			if ok && (samples.Kind != wire.FieldKindToggle || samples.Value != "true" || samples.Label != "带示例记录") {
				t.Fatalf("samples field = %+v", samples)
			}
			create, ok := buttonTitled(v, "创建")
			if !ok || create.Action.ID != "create:"+tp.Key || !create.Action.Submit {
				t.Fatalf("create = %+v", create)
			}
			back, ok := buttonTitled(v, "返回")
			if !ok || back.Action.ID != "new" {
				t.Fatalf("back = %+v", back)
			}
		})
	}
}

func TestDefaultNameIsUnique(t *testing.T) {
	tp := templates.All()[0]
	st := store.Open("")
	a := newApp(st)
	for i, want := range []string{tp.Name, tp.Name + " 2", tp.Name + " 3"} {
		v := drawPage(t, a, state{C: tp.Key}, 5)
		if got := fieldNamed(t, v, "name").Value; got != want {
			t.Fatalf("default name %d = %q, want %q", i, got, want)
		}
		// An empty name is the default one.
		res := act(t, a, state{C: tp.Key}, "create:"+tp.Key, map[string]string{"name": " "})
		if res.Toast != "已创建「"+want+"」" || spaceNamed(t, st, want) == nil {
			t.Fatalf("created %d = %q", i, res.Toast)
		}
	}
}

func TestCreateFromTheForm(t *testing.T) {
	tp := withSamples(t)
	cases := []struct {
		name    string
		values  map[string]string
		samples bool
	}{
		{"custom name without samples", map[string]string{"name": " 我的空间 ", "samples": "false"}, false},
		{"with samples", map[string]string{"name": "我的空间", "samples": "true"}, true},
		{"the untouched form", nil, true},
		// A client that sends only the edited fields: the form's switch was drawn on, so the examples come.
		{"only the name sent from the form", map[string]string{"name": "我的空间"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := storeWith(t, false, "blank")
			a := newApp(st)
			before := state{S: firstSpace(t, st), C: tp.Key}
			values := tc.values
			if values == nil {
				// What a client sends for the form as drawn: every field with its value, the switch on.
				values = formOf(drawPage(t, a, before, 5), map[string]string{"name": "我的空间"})
				if values["samples"] != "true" {
					t.Fatalf("the form's samples default = %q", values["samples"])
				}
			}
			res := act(t, a, before, "create:"+tp.Key, values)
			sp := spaceNamed(t, st, "我的空间")
			if sp == nil {
				t.Fatal("no space created")
			}
			if len(sp.Types) != len(tp.Types) || (len(sp.Entities) > 0) != tc.samples {
				t.Fatalf("space has %d types, %d records", len(sp.Types), len(sp.Entities))
			}
			if res.Toast != "已创建「我的空间」" || res.Pop || res.Push != nil {
				t.Fatalf("result = %+v", res)
			}
			// The new space's overview on the root page, with the switcher between the two spaces.
			got := decode(t, res.View)
			if got != (state{S: sp.ID}) {
				t.Fatalf("landing state = %+v", got)
			}
			if head := res.View.Sections[0].Items[0]; head.Title != "我的空间" {
				t.Fatalf("landing header = %+v", head)
			}
			if res.View.Filter == nil || res.View.Filter.Value != sp.ID || len(res.View.Filter.Choices) != 2 {
				t.Fatalf("switcher = %+v", res.View.Filter)
			}
		})
	}
}

func TestCreateRefusals(t *testing.T) {
	tp := templates.All()[0]
	st := storeWith(t, false, "blank")
	a := newApp(st)
	s := state{S: firstSpace(t, st), C: tp.Key}
	cases := []struct {
		name   string
		s      state
		action string
		values map[string]string
		toast  string
	}{
		{"a name past the limit", s, "create:" + tp.Key, map[string]string{"name": strings.Repeat("长", kernel.MaxName+1)}, ""},
		{"an unknown template", s, "create:nope", nil, "没有模板「nope」"},
		{"an unknown template's form", s, "new:nope", nil, "没有模板「nope」"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := baseJSON(t, st)
			res := act(t, a, tc.s, tc.action, tc.values)
			wantRefused(t, res, tc.s)
			if tc.toast != "" && res.Toast != tc.toast {
				t.Fatalf("toast = %q, want %q", res.Toast, tc.toast)
			}
			if baseJSON(t, st) != before {
				t.Fatal("a refused create changed the base")
			}
		})
	}
	// An unknown template in the state falls through to the space.
	v := drawPage(t, a, state{S: s.S, C: "nope"}, 5)
	if got := decode(t, v); got != (state{S: s.S}) {
		t.Fatalf("stale template = %+v", got)
	}
}

// TestNewSpaceAtEveryVersion: the flow from the overview to a new space works for every app: the button, the
// template list and the form are drawn in each version's vocabulary, with no back button of a pushed page.
func TestNewSpaceAtEveryVersion(t *testing.T) {
	tp := withSamples(t)
	for version := 1; version <= 5; version++ {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			st := storeWith(t, true, "blank")
			a := newApp(st)
			do := func(s state, action string, values map[string]string) *wire.ActionResult {
				t.Helper()
				res, err := a.Act(context.Background(), request(version, s, values, action))
				if err != nil {
					t.Fatal(err)
				}
				checkView(t, action, res.View, false)
				checkLowered(t, action, res.View, version)
				if _, ok := buttonTitled(res.View, "返回"); ok && decode(t, res.View).C == pickSlot {
					t.Fatalf("v%d: the template list has a back button", version)
				}
				return res
			}
			v := drawPage(t, a, state{S: firstSpace(t, st)}, version)
			b, ok := buttonTitled(v, "新建空间")
			if !ok {
				t.Fatalf("v%d: no new space button", version)
			}
			res := do(decode(t, v), b.Action.ID, nil)
			if rows := templateRowsOf(res.View); len(rows) != len(templates.All()) {
				t.Fatalf("v%d: %d template rows", version, len(rows))
			}
			res = do(decode(t, res.View), "new:"+tp.Key, nil)
			if !fieldNamedOK(res.View, "name") || !fieldNamedOK(res.View, "samples") {
				t.Fatalf("v%d: form fields = %v", version, fieldNamesOf(res.View))
			}
			create, _ := buttonTitled(res.View, "创建")
			res = do(decode(t, res.View), create.Action.ID, formOf(res.View, map[string]string{"samples": "false"}))
			sp := spaceNamed(t, st, tp.Name)
			if sp == nil || len(sp.Entities) != 0 || decode(t, res.View) != (state{S: sp.ID}) {
				t.Fatalf("v%d: created %+v, state %+v", version, sp, decode(t, res.View))
			}
		})
	}
}

// A samples value that is neither "true" nor "false" is refused and nothing is created; a template without samples
// has no switch, so a value sent for it is not read.
func TestCreateReadsTheSamplesSwitchStrictly(t *testing.T) {
	tp := withSamples(t)
	for _, v := range []string{"", "1", "yes", "TRUE", "on"} {
		st := storeWith(t, false, "blank")
		a := newApp(st)
		before := state{S: firstSpace(t, st), C: tp.Key}
		res := act(t, a, before, "create:"+tp.Key, map[string]string{"name": "我的空间", "samples": v})
		if !strings.Contains(res.Toast, "只能是 true 或 false") || decode(t, res.View) != before {
			t.Fatalf("samples %q: toast %q, state %+v", v, res.Toast, decode(t, res.View))
		}
		if spaceNamed(t, st, "我的空间") != nil {
			t.Fatalf("samples %q: a space was created", v)
		}
	}
	blank, ok := templates.Get("blank")
	if !ok || len(blank.Samples) != 0 {
		t.Fatalf("blank template = %+v", blank)
	}
	st := storeWith(t, false, "blank")
	a := newApp(st)
	act(t, a, state{S: firstSpace(t, st), C: blank.Key}, "create:"+blank.Key, map[string]string{"name": "另一个", "samples": "yes"})
	if sp := spaceNamed(t, st, "另一个"); sp == nil || len(sp.Entities) != 0 {
		t.Fatalf("blank space = %+v", sp)
	}
}

// The flow of a new space starts from the overview and cancel returns there, also when the state it is entered or
// left with holds a type tab, a view, a search, loaded pages or an open fold.
func TestNewSpaceFlowDropsTheStateBelowTheSpace(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	id := firstSpace(t, st)
	tp := withSamples(t)
	stale := state{S: id, T: "task", V: 1, Q: "周", N: 2, F: "add"}
	for _, action := range []string{"new", "new:" + tp.Key} {
		want := state{S: id, C: pickSlot}
		if action != "new" {
			want.C = tp.Key
		}
		res := act(t, a, stale, action, nil)
		if got := decode(t, res.View); got != want {
			t.Fatalf("%s from a type tab: state = %+v, want %+v", action, got, want)
		}
		res = act(t, a, decode(t, res.View), "cancel", nil)
		if got := decode(t, res.View); got != (state{S: id}) {
			t.Fatalf("cancel after %s: state = %+v", action, got)
		}
	}
	// A cancel that arrives with lower state still set (an older page) lands on the overview too.
	withC := stale
	withC.C = pickSlot
	res := act(t, a, withC, "cancel", nil)
	if got := decode(t, res.View); got != (state{S: id}) {
		t.Fatalf("cancel with stale state = %+v", got)
	}
	if _, ok := buttonTitled(res.View, "新建空间"); !ok {
		t.Fatal("cancel did not land on the overview, which holds the button")
	}
}

// A create that does not come from a template's form (the state names no template, or another one) and sends no
// samples value creates no examples: there was no switch drawn on to stand for.
func TestDirectCreateWithoutTheSwitchHasNoSamples(t *testing.T) {
	tp := withSamples(t)
	other := ""
	for _, k := range templateKeys() {
		if k != tp.Key {
			other = k
			break
		}
	}
	for i, from := range []state{{}, {C: pickSlot}, {C: other}} {
		st := storeWith(t, false, "blank")
		a := newApp(st)
		from.S = firstSpace(t, st)
		name := fmt.Sprintf("直接创建 %d", i)
		act(t, a, from, "create:"+tp.Key, map[string]string{"name": name})
		sp := spaceNamed(t, st, name)
		if sp == nil || len(sp.Types) != len(tp.Types) || len(sp.Entities) != 0 {
			t.Fatalf("from %+v: space = %+v", from, sp)
		}
		// The explicit value still decides.
		act(t, a, from, "create:"+tp.Key, map[string]string{"name": name + " 带示例", "samples": "true"})
		if sp := spaceNamed(t, st, name+" 带示例"); sp == nil || len(sp.Entities) == 0 {
			t.Fatalf("from %+v with samples true: space = %+v", from, sp)
		}
	}
}

// A default name is never longer than a space's name may be: a base at the limit that is taken gives way to the
// suffix, and the name saves as it is.
func TestDefaultNameFitsTheLimit(t *testing.T) {
	long := strings.Repeat("长", kernel.MaxName)
	cases := []struct {
		base  string
		taken []string
		want  string
	}{
		{"项目", nil, "项目"},
		{"项目", []string{"项目", "项目 2"}, "项目 3"},
		{long, nil, long},
		{long + "超出", nil, long},
		{long, []string{long}, strings.Repeat("长", kernel.MaxName-2) + " 2"},
		{long, []string{long, strings.Repeat("长", kernel.MaxName-2) + " 2"}, strings.Repeat("长", kernel.MaxName-2) + " 3"},
	}
	for _, tc := range cases {
		taken := map[string]bool{}
		for _, n := range tc.taken {
			taken[n] = true
		}
		got := freeName(tc.base, taken)
		if got != tc.want || got == "" || taken[got] || len([]rune(got)) > kernel.MaxName {
			t.Errorf("freeName(%d runes, %d taken) = %q (%d runes), want %q", len([]rune(tc.base)), len(tc.taken), got, len([]rune(got)), tc.want)
		}
	}
	// Ten taken names push the suffix to two digits; the name still fits.
	taken := map[string]bool{long: true}
	for i := 2; i <= 10; i++ {
		taken[freeName(long, taken)] = true
	}
	if got := freeName(long, taken); len([]rune(got)) != kernel.MaxName || !strings.HasSuffix(got, " 11") {
		t.Errorf("after ten = %q (%d runes)", got, len([]rune(got)))
	}

	// End to end: a space at the limit whose name the kernel accepts, then a second one by the default name.
	st := store.Open("")
	if err := st.Update(func(b *kernel.Base, _ time.Time) error {
		name := freeName(long, nil)
		_, err := b.PutSpace(kernel.SpaceInput{Name: &name})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(b *kernel.Base, _ time.Time) error {
		taken := map[string]bool{}
		for _, sp := range b.Spaces {
			taken[sp.Name] = true
		}
		name := freeName(long, taken)
		_, err := b.PutSpace(kernel.SpaceInput{Name: &name})
		return err
	}); err != nil {
		t.Fatalf("the default name beside a name at the limit is refused: %v", err)
	}
}
