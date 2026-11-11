package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/spaces/internal/templates"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/store"
)

var testNow = time.Date(2026, time.November, 10, 9, 30, 0, 0, time.UTC)

// storeWith is an in-memory store holding the templates with the keys, applied in order, with or without samples.
func storeWith(t *testing.T, samples bool, keys ...string) *store.Store {
	t.Helper()
	st := store.Open("")
	err := st.Update(func(b *kernel.Base, _ time.Time) error {
		for _, k := range keys {
			if _, err := templates.Apply(b, k, "", samples, testNow); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// crowded is a store with one blank space "书架" holding a list type "item" with n entities and, when table is set,
// a type "tbl" whose only view is a table, with n entities too.
func crowded(t *testing.T, n int, table bool) *store.Store {
	t.Helper()
	st := store.Open("")
	err := st.Update(func(b *kernel.Base, _ time.Time) error {
		sp, err := templates.Apply(b, "blank", "书架", false, testNow)
		if err != nil {
			return err
		}
		if err := sp.PutType(kernel.Type{Key: "item", Name: "条目"}, testNow); err != nil {
			return err
		}
		if table {
			tbl := kernel.Type{Key: "tbl", Name: "表", Views: []kernel.View{{Name: "表", Kind: "table"}}}
			if err := sp.PutType(tbl, testNow); err != nil {
				return err
			}
		}
		for i := 0; i < n; i++ {
			title := fmt.Sprintf("条目 %02d", i)
			if _, err := sp.PutEntity(b, kernel.EntityInput{Type: "item", Title: &title}, testNow); err != nil {
				return err
			}
			if table {
				row := fmt.Sprintf("表 %02d", i)
				if _, err := sp.PutEntity(b, kernel.EntityInput{Type: "tbl", Title: &row}, testNow); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// newApp is the app over st with the clock of the tests.
func newApp(st kernel.Storage) *App {
	a := New(st)
	a.Now = func() time.Time { return testNow }
	return a
}

// request is a request to the nav page at a UI version with a state, values and an action.
func request(version int, s state, values map[string]string, action string) *ext.UIRequest {
	return &ext.UIRequest{UIParams: wire.UIParams{
		Contribution: PageUI, State: ext.EncodeState(s), Action: action, Values: values,
		Placement: "nav_page", Size: "full", UIVersion: version, TZ: "Asia/Shanghai", Locale: "zh-Hans-CN", Platform: "web",
	}}
}

// checkView holds a view to the validators of the SDK: the schema and the page rules, and for a card the compact ones.
func checkView(t *testing.T, label string, v *wire.View, compact bool) {
	t.Helper()
	if err := wire.CheckView(v); err != nil {
		t.Fatalf("%s: wire.CheckView: %v", label, err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: marshal: %v", label, err)
	}
	if err := exttest.CheckView(raw, compact); err != nil {
		t.Fatalf("%s: exttest.CheckView: %v", label, err)
	}
}

// checkLowered holds a view that was lowered to version n to that version's vocabulary.
func checkLowered(t *testing.T, label string, v *wire.View, n int) {
	t.Helper()
	for _, s := range v.Sections {
		if n < 3 && s.More != nil {
			t.Fatalf("%s: more at version %d", label, n)
		}
		if n < 4 && s.Pane != "" {
			t.Fatalf("%s: pane at version %d", label, n)
		}
		for _, it := range s.Items {
			if wire.ComponentSince[it.Type] > n {
				t.Fatalf("%s: component %s needs version %d, not %d", label, it.Type, wire.ComponentSince[it.Type], n)
			}
		}
	}
	if n < 3 && (v.Search != nil || v.Filter != nil) {
		t.Fatalf("%s: search or filter at version %d", label, n)
	}
	if n < 4 && (v.Tabs != nil || v.Layout != "") {
		t.Fatalf("%s: tabs or layout at version %d", label, n)
	}
}

// decode reads the state a view carries.
func decode(t *testing.T, v *wire.View) state {
	t.Helper()
	var s state
	if v.State == "" {
		return s
	}
	if err := json.Unmarshal([]byte(v.State), &s); err != nil {
		t.Fatalf("state %q: %v", v.State, err)
	}
	return s
}

// drawPage draws the nav page at a version, checks it and returns it.
func drawPage(t *testing.T, a *App, s state, version int) *wire.View {
	t.Helper()
	v, err := a.Page(context.Background(), request(version, s, nil, ""))
	if err != nil {
		t.Fatalf("Page(%+v) at %d: %v", s, version, err)
	}
	checkView(t, fmt.Sprintf("page %+v v%d", s, version), v, false)
	return v
}

// act runs an action on the nav page and checks the view it answers, when it has one.
func act(t *testing.T, a *App, s state, action string, values map[string]string) *wire.ActionResult {
	t.Helper()
	res, err := a.Act(context.Background(), request(5, s, values, action))
	if err != nil {
		t.Fatalf("Act(%s): %v", action, err)
	}
	if res.View != nil {
		checkView(t, "act "+action, res.View, false)
	}
	return res
}

// firstSpace is the id of the first space of st.
func firstSpace(t *testing.T, st kernel.Storage) string {
	t.Helper()
	var id string
	if err := st.View(func(b *kernel.Base) error {
		if len(b.Spaces) > 0 {
			id = b.Spaces[0].ID
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// allScreens are the states that show every screen of a base: the welcome, each space's overview and settings fold,
// each type's tab with each view, searched, extra pages loaded, with the quick-add fold open, and the schema editor.
func allScreens(t *testing.T, st kernel.Storage) []state {
	t.Helper()
	out := []state{{}}
	err := st.View(func(b *kernel.Base) error {
		for _, sp := range b.Spaces {
			out = append(out, state{S: sp.ID}, state{S: sp.ID, F: "space"})
			for _, ty := range sp.Types {
				for v := range ty.ViewsOrDefault() {
					for _, q := range []string{"", "a"} {
						for _, f := range []string{"", "new"} {
							out = append(out, state{S: sp.ID, T: ty.Key, V: v, Q: q, F: f})
						}
					}
					out = append(out, state{S: sp.ID, T: ty.Key, V: v, N: 1})
				}
				out = append(out, state{S: sp.ID, D: ty.Key})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEveryScreenAtEveryVersion(t *testing.T) {
	cases := []struct {
		name  string
		store *store.Store
	}{
		{"empty store", storeWith(t, true)},
		{"blank", storeWith(t, true, "blank")},
		{"blank without samples", storeWith(t, false, "blank")},
		{"project", storeWith(t, true, "project")},
		{"reading", storeWith(t, true, "reading")},
		{"inventory", storeWith(t, true, "inventory")},
		{"project without samples", storeWith(t, false, "project")},
		{"all templates", storeWith(t, true, "project", "reading", "inventory", "blank")},
		{"crowded", crowded(t, 25, true)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newApp(tc.store)
			for _, s := range allScreens(t, tc.store) {
				for v := 1; v <= 5; v++ {
					checkLowered(t, fmt.Sprintf("%+v v%d", s, v), drawPage(t, a, s, v), v)
				}
			}
			for v := 1; v <= 5; v++ {
				card, err := a.Card(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{
					Contribution: CardUI, Placement: "overview_card", Size: "compact", UIVersion: v, TZ: "Asia/Shanghai",
				}})
				if err != nil {
					t.Fatalf("Card at %d: %v", v, err)
				}
				checkView(t, fmt.Sprintf("card v%d", v), card, true)
				checkLowered(t, fmt.Sprintf("card v%d", v), card, v)
			}
		})
	}
}

func TestEmptyStoreShowsWelcomeAndCard(t *testing.T) {
	a := newApp(storeWith(t, true))
	v := drawPage(t, a, state{}, 5)
	if len(v.Sections) != 1 {
		t.Fatalf("welcome sections = %d", len(v.Sections))
	}
	rows := 0
	for _, it := range v.Sections[0].Items {
		if it.Type == wire.ComponentRow {
			rows++
			if it.Action == nil || !strings.HasPrefix(it.Action.ID, "create:") {
				t.Fatalf("template row action = %+v", it.Action)
			}
		}
	}
	if rows != len(templates.All()) {
		t.Fatalf("welcome rows = %d, want %d", rows, len(templates.All()))
	}
	card, err := a.Card(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{UIVersion: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if card.Sections[0].Items[0].Type != wire.ComponentEmpty {
		t.Fatalf("empty card = %+v", card.Sections[0].Items[0])
	}
}

func TestRoutingFallsThroughStaleIds(t *testing.T) {
	st := storeWith(t, true, "project", "reading")
	a := newApp(st)
	var projectSpace, idea, goal string
	_ = st.View(func(b *kernel.Base) error {
		projectSpace = b.Spaces[0].ID
		for _, e := range b.Spaces[0].Entities {
			if e.Type == "idea" && idea == "" {
				idea = e.ID
			}
			if e.Type == "goal" && goal == "" {
				goal = e.ID
			}
		}
		return nil
	})
	cases := []struct {
		name  string
		s     state
		check func(t *testing.T, v *wire.View, got state)
	}{
		{"first space when none is named", state{}, func(t *testing.T, v *wire.View, got state) {
			if got.S != projectSpace {
				t.Fatalf("S = %q, want %q", got.S, projectSpace)
			}
			if v.Filter == nil || len(v.Filter.Choices) != 2 {
				t.Fatalf("filter with two spaces = %+v", v.Filter)
			}
		}},
		{"stale space falls to the first", state{S: "nope"}, func(t *testing.T, _ *wire.View, got state) {
			if got.S != projectSpace {
				t.Fatalf("S = %q", got.S)
			}
		}},
		{"stale type falls to the overview", state{S: projectSpace, T: "nope", V: 2, N: 3}, func(t *testing.T, v *wire.View, got state) {
			if got.T != "" || got.V != 0 || got.N != 0 {
				t.Fatalf("state after stale type = %+v", got)
			}
			if v.Tabs == nil || v.Tabs.Value != overTab {
				t.Fatalf("tabs = %+v", v.Tabs)
			}
		}},
		{"entity page opens with its header", state{S: projectSpace, E: idea}, func(t *testing.T, v *wire.View, got state) {
			if got.E != idea || v.Sections[0].Items[0].Type != wire.ComponentHeader || v.Title == "" {
				t.Fatalf("entity route = %+v / %+v", got, v.Sections[0].Items[0])
			}
		}},
		{"stale entity falls through to its type", state{S: projectSpace, E: "nope", T: "idea"}, func(t *testing.T, v *wire.View, got state) {
			if got.E != "" || got.T != "idea" || v.Tabs == nil || v.Tabs.Value != "idea" {
				t.Fatalf("stale entity = %+v, tabs %+v", got, v.Tabs)
			}
		}},
		{"entity of another space shows its space", state{S: "nope", E: goal}, func(t *testing.T, _ *wire.View, got state) {
			if got.S != projectSpace {
				t.Fatalf("S = %q, want the entity's space %q", got.S, projectSpace)
			}
		}},
		{"editor of a type opens with its header", state{S: projectSpace, D: "idea"}, func(t *testing.T, v *wire.View, got state) {
			if got.D != "idea" || v.Sections[0].Items[0].Type != wire.ComponentHeader || v.Sections[0].Items[0].Title != "点子" {
				t.Fatalf("editor route = %+v", got)
			}
		}},
		{"editor for a new type", state{S: projectSpace, D: "+"}, func(t *testing.T, v *wire.View, got state) {
			if got.D != "+" || v.Title != "新建类型" {
				t.Fatalf("new type editor = %+v", got)
			}
		}},
		{"stale editor type falls to the overview", state{S: projectSpace, D: "nope"}, func(t *testing.T, _ *wire.View, got state) {
			if got.D != "" {
				t.Fatalf("D after stale = %q", got.D)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := drawPage(t, a, tc.s, 5)
			tc.check(t, v, decode(t, v))
		})
	}
}

func TestActions(t *testing.T) {
	st := storeWith(t, true, "project", "reading")
	a := newApp(st)
	projectSpace := firstSpace(t, st)
	var readingSpace string
	_ = st.View(func(b *kernel.Base) error { readingSpace = b.Spaces[1].ID; return nil })
	project := state{S: projectSpace}
	cases := []struct {
		name   string
		s      state
		action string
		values map[string]string
		check  func(t *testing.T, res *wire.ActionResult, got state)
	}{
		{"tab by argument", project, "tab:goal", nil, func(t *testing.T, res *wire.ActionResult, got state) {
			if got.T != "goal" || res.View == nil {
				t.Fatalf("tab = %+v", got)
			}
		}},
		{"tab by value", project, "tab", map[string]string{"tab": "task"}, func(t *testing.T, _ *wire.ActionResult, got state) {
			if got.T != "task" {
				t.Fatalf("tab = %+v", got)
			}
		}},
		{"tab back to the overview", state{S: projectSpace, T: "idea"}, "tab:/", nil, func(t *testing.T, _ *wire.ActionResult, got state) {
			if got.T != "" {
				t.Fatalf("overview tab = %+v", got)
			}
		}},
		{"stale tab is refused", project, "tab:nope", nil, func(t *testing.T, res *wire.ActionResult, got state) {
			if res.Toast != "没有类型「nope」" || got.T != "" {
				t.Fatalf("refused tab = %q / %+v", res.Toast, got)
			}
		}},
		{"view by value", state{S: projectSpace, T: "goal"}, "view", map[string]string{"view": "1"}, func(t *testing.T, _ *wire.ActionResult, got state) {
			if got.V != 1 {
				t.Fatalf("view = %+v", got)
			}
		}},
		{"view by argument resets the pages", state{S: projectSpace, T: "goal", N: 2}, "view:0", nil, func(t *testing.T, _ *wire.ActionResult, got state) {
			if got.V != 0 || got.N != 0 {
				t.Fatalf("view argument = %+v", got)
			}
		}},
		{"view out of range is refused", state{S: projectSpace, T: "goal"}, "view:9", nil, func(t *testing.T, res *wire.ActionResult, _ state) {
			if res.Toast == "" {
				t.Fatal("no refusal")
			}
		}},
		{"view without a type is refused", project, "view:1", nil, func(t *testing.T, res *wire.ActionResult, _ state) {
			if res.Toast == "" {
				t.Fatal("no refusal")
			}
		}},
		{"search", state{S: projectSpace, T: "idea", N: 3}, "search", map[string]string{"q": " 复盘 "}, func(t *testing.T, _ *wire.ActionResult, got state) {
			if got.Q != "复盘" || got.N != 0 {
				t.Fatalf("search = %+v", got)
			}
		}},
		{"more", state{S: projectSpace, T: "idea"}, "more", nil, func(t *testing.T, _ *wire.ActionResult, got state) {
			if got.N != 1 {
				t.Fatalf("more = %+v", got)
			}
		}},
		{"more is bounded", state{S: projectSpace, T: "idea", N: maxMore}, "more", nil, func(t *testing.T, _ *wire.ActionResult, got state) {
			if got.N != maxMore {
				t.Fatalf("more past the bound = %+v", got)
			}
		}},
		{"fold opens", project, "fold:new", nil, func(t *testing.T, _ *wire.ActionResult, got state) {
			if got.F != "new" {
				t.Fatalf("fold = %+v", got)
			}
		}},
		{"fold closes when open", state{S: projectSpace, F: "space"}, "fold:space", nil, func(t *testing.T, _ *wire.ActionResult, got state) {
			if got.F != "" {
				t.Fatalf("fold toggled = %+v", got)
			}
		}},
		{"switch space", project, "space:" + readingSpace, nil, func(t *testing.T, _ *wire.ActionResult, got state) {
			if got.S != readingSpace || got.T != "" {
				t.Fatalf("space = %+v", got)
			}
		}},
		{"switch to a missing space is refused", project, "space:nope", nil, func(t *testing.T, res *wire.ActionResult, got state) {
			if res.Toast == "" || got.S != projectSpace {
				t.Fatalf("refused space = %q / %+v", res.Toast, got)
			}
		}},
		{"add an entity", state{S: projectSpace, T: "idea", F: "new"}, "add", map[string]string{"title": "新的点子"}, func(t *testing.T, res *wire.ActionResult, got state) {
			if got.F != "" || res.Toast != "已创建「新的点子」" {
				t.Fatalf("added = %q / %+v", res.Toast, got)
			}
		}},
		{"add without a title is refused and keeps the fold", state{S: projectSpace, T: "idea", F: "new"}, "add", map[string]string{"title": " "}, func(t *testing.T, res *wire.ActionResult, got state) {
			if got.F != "new" || res.Toast == "" {
				t.Fatalf("refused add = %q / %+v", res.Toast, got)
			}
		}},
		{"add without a type is refused", project, "add", map[string]string{"title": "x"}, func(t *testing.T, res *wire.ActionResult, _ state) {
			if res.Toast != "先选择一个类型" {
				t.Fatalf("add without type = %q", res.Toast)
			}
		}},
		{"save the space", state{S: projectSpace, F: "space"}, "save", map[string]string{"name": "新名字", "about": "关于"}, func(t *testing.T, res *wire.ActionResult, got state) {
			if got.F != "" || res.Toast != "已保存" {
				t.Fatalf("saved = %q / %+v", res.Toast, got)
			}
		}},
		{"save with an empty name is refused", state{S: projectSpace, F: "space"}, "save", map[string]string{"name": ""}, func(t *testing.T, res *wire.ActionResult, got state) {
			if got.F != "space" || res.Toast == "" {
				t.Fatalf("refused save = %q / %+v", res.Toast, got)
			}
		}},
		{"create from a template", state{}, "create:inventory", nil, func(t *testing.T, res *wire.ActionResult, got state) {
			if got.S == "" || !strings.HasPrefix(res.Toast, "已创建") {
				t.Fatalf("created = %q / %+v", res.Toast, got)
			}
		}},
		{"create with an unknown template is refused", state{}, "create:nope", nil, func(t *testing.T, res *wire.ActionResult, _ state) {
			if res.Toast == "" {
				t.Fatal("no refusal")
			}
		}},
		{"back", project, "back", nil, func(t *testing.T, res *wire.ActionResult, _ state) {
			if !res.Pop || res.View != nil {
				t.Fatalf("back = %+v", res)
			}
		}},
		{"unknown action draws the page", project, "nothing:here", nil, func(t *testing.T, res *wire.ActionResult, _ state) {
			if res.View == nil {
				t.Fatal("no view")
			}
		}},
		{"stale entity in state is dropped", state{S: projectSpace, E: "nope", T: "idea"}, "more", nil, func(t *testing.T, res *wire.ActionResult, got state) {
			if res.View == nil || got.E != "" {
				t.Fatalf("stale entity = %+v", got)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := act(t, a, tc.s, tc.action, tc.values)
			var got state
			if res.View != nil {
				got = decode(t, res.View)
			}
			tc.check(t, res, got)
		})
	}
}

// TestActionsWithoutSpace: the actions that need a space refuse, with the sentence the user reads.
func TestActionsWithoutSpace(t *testing.T) {
	a := newApp(storeWith(t, true))
	cases := []struct {
		action string
		toast  string
	}{
		{"save", "还没有空间"},
		{"drop", "还没有空间"},
		{"tab:x", "没有类型「x」"},
		{"add", "先选择一个类型"},
	}
	for _, tc := range cases {
		res := act(t, a, state{}, tc.action, map[string]string{"title": "x"})
		if res.Toast != tc.toast {
			t.Fatalf("%s: toast %q, want %q", tc.action, res.Toast, tc.toast)
		}
	}
}

func TestDropSpaceInvalidatesTheCard(t *testing.T) {
	st := storeWith(t, true, "project", "reading")
	hub := &fakeHub{}
	a := newApp(st)
	a.SetHub(hub)
	first := firstSpace(t, st)
	res := act(t, a, state{S: first, F: "space"}, "drop", nil)
	if res.Toast != "已删除「项目与目标」" {
		t.Fatalf("drop = %q", res.Toast)
	}
	if got := decode(t, res.View); got.S == first || got.S == "" {
		t.Fatalf("state after drop = %+v", got)
	}
	if !slices.Contains(hub.calls, CardUI) {
		t.Fatalf("hub calls = %v, want the card drawn again", hub.calls)
	}
	// A navigation changes nothing, so the hub is not told.
	hub.calls = nil
	act(t, a, state{}, "tab:x", nil)
	act(t, a, state{S: firstSpace(t, st)}, "more", nil)
	if len(hub.calls) != 0 {
		t.Fatalf("navigation told the hub: %v", hub.calls)
	}
}

// fakeHub records the contributions it was asked to draw again.
type fakeHub struct{ calls []string }

func (h *fakeHub) Invalidate(contribution string) error {
	h.calls = append(h.calls, contribution)
	return nil
}

// broken is a storage that cannot be read or written.
type broken struct{}

func (broken) View(func(b *kernel.Base) error) error            { return fmt.Errorf("读取失败") }
func (broken) Update(func(*kernel.Base, time.Time) error) error { return fmt.Errorf("写入失败") }
func (broken) Changed() bool                                    { return false }

func TestStorageErrorIsReturned(t *testing.T) {
	a := newApp(broken{})
	if _, err := a.Page(context.Background(), request(5, state{}, nil, "")); err == nil {
		t.Fatal("page over a broken store: no error")
	}
	if _, err := a.Act(context.Background(), request(5, state{}, nil, "more")); err == nil {
		t.Fatal("act over a broken store: no error")
	}
	if _, err := a.Card(context.Background(), request(5, state{}, nil, "")); err == nil {
		t.Fatal("card over a broken store: no error")
	}
}

func TestClockAndLocation(t *testing.T) {
	a := New(storeWith(t, true, "blank"))
	if a.clock().IsZero() {
		t.Fatal("default clock is zero")
	}
	a.Now = nil
	if a.clock().IsZero() {
		t.Fatal("nil clock is not the real one")
	}
	if got := location("Nowhere/Land"); got != time.UTC {
		t.Fatalf("unknown zone = %v", got)
	}
	if got := location(""); got != time.UTC {
		t.Fatalf("empty zone = %v", got)
	}
	if got := location("Asia/Shanghai"); got.String() != "Asia/Shanghai" {
		t.Fatalf("zone = %v", got)
	}
}

func TestCorruptStateIsIgnored(t *testing.T) {
	a := newApp(storeWith(t, true, "project"))
	v, err := a.Page(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{State: "{not json", UIVersion: 5}})
	if err != nil {
		t.Fatal(err)
	}
	checkView(t, "corrupt state", v, false)
}

func TestSpaceOverviewShowsEveryPart(t *testing.T) {
	st := storeWith(t, true, "project", "reading")
	a := newApp(st)
	v := drawPage(t, a, state{S: firstSpace(t, st)}, 5)
	if v.Filter == nil || v.Tabs == nil {
		t.Fatalf("overview chrome: filter %v tabs %v", v.Filter, v.Tabs)
	}
	// The overview and the four types of the project.
	if v.Tabs.Value != overTab || len(v.Tabs.Items) != 5 {
		t.Fatalf("tabs = %+v", v.Tabs)
	}
	head := v.Sections[0].Items[0]
	if head.Type != wire.ComponentHeader || head.Title != "项目与目标" {
		t.Fatalf("header = %+v", head)
	}
	types := v.Sections[1]
	if types.Title != "类型" || len(types.Items) != 4 {
		t.Fatalf("type rows = %+v", types)
	}
	if types.Items[0].Action == nil || types.Items[0].Action.ID != "tab:idea" {
		t.Fatalf("type row action = %+v", types.Items[0].Action)
	}
	if !strings.Contains(types.Items[0].Subtitle, "看板") {
		t.Fatalf("idea subtitle = %q", types.Items[0].Subtitle)
	}
	last := v.Sections[len(v.Sections)-1]
	if len(last.Items) != 2 || last.Items[1].Action.ID != "fold:space" {
		t.Fatalf("button row = %+v", last.Items)
	}
}

func TestSpaceFoldAndDeleteConfirmation(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	v := drawPage(t, a, state{S: firstSpace(t, st), F: "space"}, 5)
	var fields, destructive int
	for _, s := range v.Sections {
		for _, it := range s.Items {
			if it.Type == wire.ComponentField {
				fields++
			}
			if it.Role == wire.ButtonRoleDestructive {
				destructive++
				if !strings.Contains(it.Action.Confirm, "删除空间") {
					t.Fatalf("confirm = %q", it.Action.Confirm)
				}
			}
		}
	}
	if fields != 2 || destructive != 1 {
		t.Fatalf("fold: %d fields, %d destructive", fields, destructive)
	}
}

func TestTypeTabShowsBoardFeedProgressTableAndQuickAdd(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	id := firstSpace(t, st)

	// The idea board: a column per stage of the flow, the empty one with a dash.
	v := drawPage(t, a, state{S: id, T: "idea"}, 5)
	if v.Search == nil || v.Search.Placeholder != "搜索点子" {
		t.Fatalf("search = %+v", v.Search)
	}
	if v.Sections[0].Items[0].Type != wire.ComponentSegmented {
		t.Fatalf("segmented missing: %+v", v.Sections[0].Items[0])
	}
	stats := v.Sections[0].Items[1].Stats
	if len(stats) != 4 || stats[0].Label != "数量" {
		t.Fatalf("default stats = %+v", stats)
	}
	var titles []string
	for _, s := range v.Sections[1:] {
		titles = append(titles, s.Title)
	}
	for _, want := range []string{"收集 · 1", "探索 · 1", "已孵化 · 1", "搁置 · 0"} {
		if !slices.Contains(titles, want) {
			t.Fatalf("board columns %v lack %q", titles, want)
		}
	}
	// The dropped stage is the last column, before the buttons.
	empty := v.Sections[len(v.Sections)-2].Items[0]
	if empty.Type != wire.ComponentText || empty.Text != "—" {
		t.Fatalf("empty column = %+v", empty)
	}

	// The feed of ideas, newest first, today's day first in the request's zone.
	v = drawPage(t, a, state{S: id, T: "idea", V: 1}, 5)
	if v.Sections[1].Title != "今天" {
		t.Fatalf("feed first day = %q", v.Sections[1].Title)
	}

	// The goals' progress: a row and a bar per goal, with a ratio.
	v = drawPage(t, a, state{S: id, T: "goal"}, 5)
	bars := 0
	for _, it := range v.Sections[1].Items {
		if it.Type == wire.ComponentProgress {
			bars++
			if it.Ratio == nil || it.Detail == "" {
				t.Fatalf("progress = %+v", it)
			}
		}
	}
	if bars == 0 {
		t.Fatal("no progress bars for goals")
	}

	// The task table: the columns and cells, the aggregates as caption, then the same entities as rows.
	v = drawPage(t, a, state{S: id, T: "task", V: 1}, 5)
	table := v.Sections[1].Items[0]
	if table.Type != wire.ComponentTable || len(table.Rows) == 0 || table.Caption == "" {
		t.Fatalf("table = %+v", table)
	}
	if v.Sections[2].Title != "打开" {
		t.Fatalf("index section = %q", v.Sections[2].Title)
	}

	// Quick add: opened, it shows one title and a primary create, just above the buttons.
	v = drawPage(t, a, state{S: id, T: "idea", F: "new"}, 5)
	fold := v.Sections[len(v.Sections)-2]
	if len(fold.Items) != 2 || fold.Items[0].Name != "title" || !fold.Items[1].Action.Submit {
		t.Fatalf("quick add = %+v", fold.Items)
	}
}

func TestMorePagingAndTable(t *testing.T) {
	st := crowded(t, 60, true)
	a := newApp(st)
	id := firstSpace(t, st)

	// A list: a page of 20, then 40, then all 60 with no more.
	v := drawPage(t, a, state{S: id, T: "item"}, 5)
	if rows := len(v.Sections[1].Items); rows != pageRows || v.Sections[1].More == nil {
		t.Fatalf("first page rows = %d, more = %v", rows, v.Sections[1].More)
	}
	v = drawPage(t, a, state{S: id, T: "item", N: 1}, 5)
	if rows := len(v.Sections[1].Items); rows != 2*pageRows || v.Sections[1].More == nil {
		t.Fatalf("second page rows = %d", rows)
	}
	v = drawPage(t, a, state{S: id, T: "item", N: 2}, 5)
	if rows := len(v.Sections[1].Items); rows != 60 || v.Sections[1].More != nil {
		t.Fatalf("last page rows = %d, more = %v", rows, v.Sections[1].More)
	}

	// A table: fifty rows and more on the index; a hundred at most.
	v = drawPage(t, a, state{S: id, T: "tbl"}, 5)
	if rows := len(v.Sections[1].Items[0].Rows); rows != tableRows || v.Sections[2].More == nil {
		t.Fatalf("table rows = %d, more on index = %v", rows, v.Sections[2].More)
	}
	v = drawPage(t, a, state{S: id, T: "tbl", N: 1}, 5)
	if rows := len(v.Sections[1].Items[0].Rows); rows != 60 || v.Sections[2].More != nil {
		t.Fatalf("table second page rows = %d", rows)
	}

	// A search that finds nothing says so.
	v = drawPage(t, a, state{S: id, T: "item", Q: "没有这个"}, 5)
	if v.Sections[1].Items[0].Type != wire.ComponentEmpty || !strings.HasPrefix(v.Sections[1].Items[0].Title, "没有找到") {
		t.Fatalf("empty search = %+v", v.Sections[1].Items[0])
	}
}

func TestEmptyTypeOffersTheFirstEntity(t *testing.T) {
	st := storeWith(t, false, "project")
	a := newApp(st)
	v := drawPage(t, a, state{S: firstSpace(t, st), T: "note"}, 5)
	empty := v.Sections[1].Items[0]
	if empty.Type != wire.ComponentEmpty || empty.Title != "还没有记录" || empty.Button == nil {
		t.Fatalf("empty type = %+v", empty)
	}
	if empty.Button.Action.ID != "fold:new" {
		t.Fatalf("empty button = %+v", empty.Button.Action)
	}
}

func TestSpaceWithoutTypesOffersATypeAndNoTabs(t *testing.T) {
	st := storeWith(t, true, "blank")
	a := newApp(st)
	v := drawPage(t, a, state{S: firstSpace(t, st)}, 5)
	if v.Tabs != nil {
		t.Fatalf("tabs in a space with no types: %+v", v.Tabs)
	}
	empty := v.Sections[1].Items[0]
	if empty.Type != wire.ComponentEmpty || empty.Button == nil || empty.Button.Title != "新建类型" {
		t.Fatalf("no types = %+v", empty)
	}
}

func TestCardListsEntitiesInProgress(t *testing.T) {
	st := storeWith(t, true, "project", "reading")
	a := newApp(st)
	card, err := a.Card(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{Size: "compact", UIVersion: 5}})
	if err != nil {
		t.Fatal(err)
	}
	checkView(t, "card", card, true)
	if len(card.Sections) != 2 || card.Sections[1].Title != "在办" {
		t.Fatalf("card sections = %+v", card.Sections)
	}
	rows := card.Sections[1].Items
	if len(rows) == 0 || len(rows) > cardRows {
		t.Fatalf("card rows = %d", len(rows))
	}
	for _, r := range rows {
		if r.Action == nil || r.Action.Push == nil || r.Action.Push.UI != PageUI {
			t.Fatalf("card row does not push to the page: %+v", r.Action)
		}
		if r.Badge == nil || r.Badge.Tone != wire.ToneCaution {
			t.Fatalf("card row badge = %+v", r.Badge)
		}
	}
}

// sampleBase is a base holding one space of the project template, with no samples, and its space.
func sampleBase(t *testing.T) (*kernel.Base, *kernel.Space) {
	t.Helper()
	b := &kernel.Base{Format: 1}
	sp, err := templates.Apply(b, "project", "", false, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return b, sp
}

func TestRelationChoices(t *testing.T) {
	b, sp := sampleBase(t)
	task := typeIn(sp, "task")
	var goal kernel.Prop
	for _, p := range task.Props {
		if p.Key == "goal" {
			goal = p
		}
	}
	c := &ctx{b: b, sp: sp, w: kernel.Open(sp), now: testNow, loc: time.UTC}

	// No goal yet: the choice is only the unset one.
	f, ok := c.field(goal, "p_goal", nil)
	if !ok || f.Kind != wire.FieldKindChoice || f.Value != "=" || len(f.Choices) != 1 {
		t.Fatalf("unset relation field = %+v", f)
	}

	// Goals are offered newest first; the one held is among them.
	var ids []string
	for i := 0; i < pickLimit+1; i++ {
		title := fmt.Sprintf("目标 %03d", i)
		e, err := sp.PutEntity(b, kernel.EntityInput{Type: "goal", Title: &title}, testNow.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID)
	}
	c.w = kernel.Open(sp)
	held := ids[0] // the oldest: not among the newest hundred
	f, _ = c.field(goal, "p_goal", []string{held})
	if f.Value != "="+held {
		t.Fatalf("held value = %q", f.Value)
	}
	if got := f.Choices[1].Value; got != "="+held {
		t.Fatalf("held relation is not offered first: %q", got)
	}
	if len(f.Choices) != pickLimit+2 {
		t.Fatalf("choices = %d, want %d", len(f.Choices), pickLimit+2)
	}
	// A held entity that no longer exists is not offered.
	f, _ = c.field(goal, "p_goal", []string{"gone"})
	if len(f.Choices) != pickLimit+1 {
		t.Fatalf("choices with a dangling value = %d", len(f.Choices))
	}
}

func TestFormFieldsAndSubmittedValues(t *testing.T) {
	c := &ctx{}
	cases := []struct {
		name string
		p    kernel.Prop
		v    any
		kind string
		val  string
	}{
		{"text", kernel.Prop{Key: "t", Name: "文本", Kind: kernel.KindText}, "内容", wire.FieldKindText, "内容"},
		{"number", kernel.Prop{Key: "n", Name: "数", Kind: kernel.KindNumber, Unit: "页"}, 12.5, wire.FieldKindNumber, "12.5"},
		{"number unset", kernel.Prop{Key: "n", Name: "数", Kind: kernel.KindNumber}, nil, wire.FieldKindNumber, ""},
		{"toggle", kernel.Prop{Key: "b", Name: "是否", Kind: kernel.KindBoolean}, true, wire.FieldKindToggle, "true"},
		{"toggle unset", kernel.Prop{Key: "b", Name: "是否", Kind: kernel.KindBoolean}, nil, wire.FieldKindToggle, "false"},
		{"date", kernel.Prop{Key: "d", Name: "日期", Kind: kernel.KindDate}, "2026-01-02", wire.FieldKindText, "2026-01-02"},
		{"choice", kernel.Prop{Key: "e", Name: "选项", Kind: kernel.KindEnum, Options: []string{"高", "低"}}, "低", wire.FieldKindChoice, "=低"},
		{"choice unset", kernel.Prop{Key: "e", Name: "选项", Kind: kernel.KindEnum, Options: []string{"高"}}, nil, wire.FieldKindChoice, "="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, ok := c.field(tc.p, "p_"+tc.p.Key, tc.v)
			if !ok {
				t.Fatal("no field")
			}
			if f.Kind != tc.kind || f.Value != tc.val || f.Label != tc.p.Name {
				t.Fatalf("field = kind %q value %q label %q", f.Kind, f.Value, f.Label)
			}
		})
	}
	if _, ok := c.field(kernel.Prop{Key: "r", Name: "汇总", Kind: kernel.KindRollup, Op: "count"}, "r", nil); ok {
		t.Fatal("a computed property has a field")
	}
	if _, ok := c.field(kernel.Prop{Key: "x", Name: "未知", Kind: "nope"}, "x", nil); ok {
		t.Fatal("an unknown kind has a field")
	}
	if f, _ := c.field(kernel.Prop{Key: "u", Name: "链接", Kind: kernel.KindURL}, "u", nil); f.Placeholder != "https://" {
		t.Fatalf("url placeholder = %q", f.Placeholder)
	}
	rel, _ := kernel.KindOf(kernel.KindRelation)
	if got := fieldValue(rel, nil); got != "=" {
		t.Fatalf("empty relation value = %q", got)
	}
	if got := fieldValue(rel, []string{"e9", "e8"}); got != "=e9" {
		t.Fatalf("relation value = %q", got)
	}
	num, _ := kernel.KindOf(kernel.KindNumber)
	if got := fieldValue(num, "x"); got != "" {
		t.Fatalf("number of a string = %q", got)
	}
	choice, _ := kernel.KindOf(kernel.KindEnum)
	if got := fieldValue(choice, nil); got != "=" {
		t.Fatalf("choice of nil = %q", got)
	}

	submit := []struct {
		name string
		p    kernel.Prop
		raw  string
		want any
		err  bool
	}{
		{"text", kernel.Prop{Key: "t", Name: "文本", Kind: kernel.KindText}, " 你好 ", "你好", false},
		{"empty is unset", kernel.Prop{Key: "t", Name: "文本", Kind: kernel.KindText}, "", nil, false},
		{"choice", kernel.Prop{Key: "e", Name: "选项", Kind: kernel.KindEnum, Options: []string{"高"}}, "=高", "高", false},
		{"choice unset", kernel.Prop{Key: "e", Name: "选项", Kind: kernel.KindEnum, Options: []string{"高"}}, "=", nil, false},
		{"choice not an option", kernel.Prop{Key: "e", Name: "选项", Kind: kernel.KindEnum, Options: []string{"高"}}, "=低", nil, true},
		{"number", kernel.Prop{Key: "n", Name: "数", Kind: kernel.KindNumber}, "12", 12.0, false},
		{"number malformed", kernel.Prop{Key: "n", Name: "数", Kind: kernel.KindNumber}, "十二", nil, true},
		{"toggle", kernel.Prop{Key: "b", Name: "是否", Kind: kernel.KindBoolean}, "true", true, false},
		{"date malformed", kernel.Prop{Key: "d", Name: "日期", Kind: kernel.KindDate}, "2026-13-01", nil, true},
		{"relation", kernel.Prop{Key: "r", Name: "关联", Kind: kernel.KindRelation, Target: "x"}, "=e1", []string{"e1"}, false},
		{"computed", kernel.Prop{Key: "r", Name: "汇总", Kind: kernel.KindRollup, Op: "count"}, "3", nil, true},
		{"unknown kind", kernel.Prop{Key: "x", Name: "未知", Kind: "nope"}, "a", nil, true},
	}
	for _, tc := range submit {
		t.Run("submit "+tc.name, func(t *testing.T) {
			got, err := submitted(tc.p, tc.raw)
			if (err != nil) != tc.err {
				t.Fatalf("err = %v, want error %v", err, tc.err)
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("value = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOptionChoicesAndDayTitles(t *testing.T) {
	got := optionChoices([]string{"甲", "乙"})
	if len(got) != 3 || got[0].Title != unsetLabel || got[2].Value != "=乙" {
		t.Fatalf("option choices = %+v", got)
	}
	cases := []struct{ day, today, yesterday, want string }{
		{"", "2026-11-10", "2026-11-09", unsetLabel},
		{"2026-11-10", "2026-11-10", "2026-11-09", "今天"},
		{"2026-11-09", "2026-11-10", "2026-11-09", "昨天"},
		{"2026-10-01", "2026-11-10", "2026-11-09", "2026-10-01"},
	}
	for _, tc := range cases {
		if got := dayTitle(tc.day, tc.today, tc.yesterday); got != tc.want {
			t.Fatalf("dayTitle(%q) = %q, want %q", tc.day, got, tc.want)
		}
	}
}

func TestLowerRewritesEachOlderVocabulary(t *testing.T) {
	full := &wire.View{
		Title:  "条目",
		Search: &wire.Search{Placeholder: "搜", Action: wire.Action{ID: "search"}},
		Filter: &wire.Filter{Name: "space", Choices: []wire.Choice{{Value: "a", Title: "甲"}, {Value: "b"}}, Value: "a", Action: wire.Action{ID: "space"}},
		Tabs:   &wire.Tabs{Name: "tab", Value: "b", Items: []wire.Tab{{Value: "a", Title: "概览", Badge: "2"}, {Value: "b"}}, Action: wire.Action{ID: "tab"}},
		Layout: wire.LayoutSplit,
		Sections: []wire.Section{
			{Title: "头", Items: []wire.Component{
				{Type: wire.ComponentHeader, Title: "标题", Subtitle: "副", Badges: []wire.Badge{{Text: "进行"}}, Facts: []wire.Fact{{Label: "作者", Value: "甲"}, {Value: "无标签"}}},
				{Type: wire.ComponentStatGrid, Stats: []wire.Stat{{Label: "数", Value: "3", Detail: "d", Tone: wire.TonePositive}}},
				{Type: wire.ComponentSegmented, Name: "view", Value: "1", Action: &wire.Action{ID: "view"}, Choices: []wire.Choice{{Value: "0", Title: "列"}, {Value: "1"}}},
				{Type: wire.ComponentTable, Columns: []wire.TableColumn{{Title: "a"}, {Title: "b"}}, Rows: [][]string{{"甲", "乙"}, {"丙", "丁"}}, Caption: "合计"},
				{Type: wire.ComponentEmpty, Title: "空", Detail: "细", Button: &wire.EmptyButton{Title: "做", Action: wire.Action{ID: "fold:new"}}},
				{Type: wire.ComponentEmpty, Title: "空"},
				{Type: wire.ComponentTable, Columns: []wire.TableColumn{{Title: "a"}}, Rows: [][]string{{"x"}}},
				{Type: wire.ComponentText, Text: "保留"},
			}, More: &wire.More{Action: wire.Action{ID: "more"}}, Pane: wire.PaneMain},
		},
	}

	// Version 1 keeps only version 1 components: the back button closes a pushed page.
	v1 := lower(full, 1)
	checkLowered(t, "v1", v1, 1)
	if v1.Search != nil || v1.Filter != nil || v1.Tabs != nil || v1.Layout != "" || v1.RefreshAfter != lowerRefresh {
		t.Fatalf("v1 chrome not lowered: %+v", v1)
	}
	last := v1.Sections[len(v1.Sections)-1]
	if last.Items[0].Action == nil || last.Items[0].Action.ID != "back" {
		t.Fatalf("back button = %+v", last)
	}
	var metrics, buttons int
	for _, s := range v1.Sections {
		for _, it := range s.Items {
			switch it.Type {
			case wire.ComponentMetric:
				metrics++
			case wire.ComponentButton:
				buttons++
			}
		}
	}
	if metrics != 1 || buttons < 3 {
		t.Fatalf("metrics %d, buttons %d", metrics, buttons)
	}
	checkView(t, "v1 lowered", v1, false)

	// Version 2 keeps the stats and the segmented control, and lowers the newer components.
	v2 := lower(full, 2)
	checkLowered(t, "v2", v2, 2)
	if v2.Tabs != nil || v2.Search != nil {
		t.Fatal("v2 chrome not lowered")
	}
	checkView(t, "v2 lowered", v2, false)

	// Version 3 keeps the table, the empty state, the search and the filter; it lowers the header and the tabs.
	v3 := lower(full, 3)
	checkLowered(t, "v3", v3, 3)
	if v3.Tabs != nil || v3.Search == nil || v3.Filter == nil {
		t.Fatalf("v3 chrome: tabs %v search %v filter %v", v3.Tabs, v3.Search, v3.Filter)
	}
	checkView(t, "v3 lowered", v3, false)

	// Version 0 (unknown), 4 and later draw the view as it is.
	for _, n := range []int{0, 5} {
		if got := lower(full, n); got != full {
			t.Fatalf("lower at %d changed the view", n)
		}
	}
	if got := lower(nil, 1); got != nil {
		t.Fatal("lower of nil")
	}
	checkLowered(t, "v4", lower(full, 4), 4)
}

func TestLowerTabsAndFilterAsRows(t *testing.T) {
	v := &wire.View{
		Filter: &wire.Filter{Name: "space", Choices: []wire.Choice{{Value: "a", Title: "甲"}, {Value: "b", Title: "乙"}}, Value: "b", Action: wire.Action{ID: "space"}},
		Tabs:   &wire.Tabs{Name: "tab", Value: "a", Items: []wire.Tab{{Value: "a", Title: "概览"}, {Value: "x", Badge: "3"}}, Action: wire.Action{ID: "tab"}},
	}
	low := lower(v, 2)
	// The filter's section comes first, the tabs' section after it.
	tabs := low.Sections[1].Items
	if tabs[0].Action.ID != "tab:a" || tabs[0].Subtitle != "当前" || tabs[0].Title != "概览" {
		t.Fatalf("tab row = %+v", tabs[0])
	}
	if tabs[1].Title != "x" || tabs[1].Value != "3" {
		t.Fatalf("tab row without title = %+v", tabs[1])
	}
	filter := low.Sections[0].Items
	if filter[1].Action.ID != "space:b" || filter[1].Subtitle != "当前" {
		t.Fatalf("filter row = %+v", filter[1])
	}
	if filter[0].Title != "甲" {
		t.Fatalf("filter title = %+v", filter[0])
	}
}

// variantStore is a space with types that exercise the rarer shapes: a board grouped by an enumerated property, a
// board with one column, a board grouped by a relation, a feed ordered by a date, and a progress from two numbers.
func variantStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	st := store.Open("")
	var id string
	err := st.Update(func(b *kernel.Base, _ time.Time) error {
		sp, err := templates.Apply(b, "blank", "变体", false, testNow)
		if err != nil {
			return err
		}
		id = sp.ID
		types := []kernel.Type{
			{Key: "plain", Name: "普通", Props: []kernel.Prop{{Key: "tag", Name: "标签", Kind: kernel.KindEnum, Options: []string{"甲", "乙"}}},
				Views: []kernel.View{{Name: "看板", Kind: "board"}}},
			{Key: "bare", Name: "裸", Props: []kernel.Prop{{Key: "note", Name: "备注", Kind: kernel.KindText}},
				Views: []kernel.View{{Name: "看板", Kind: "board"}}},
			{Key: "loc", Name: "位置", Props: []kernel.Prop{{Key: "parent", Name: "上级", Kind: kernel.KindRelation, Target: "loc"}},
				Views: []kernel.View{{Name: "看板", Kind: "board", Lens: kernel.Lens{Group: "parent"}}}},
			{Key: "due", Name: "期限", Props: []kernel.Prop{{Key: "day", Name: "日", Kind: kernel.KindDate}},
				Views: []kernel.View{{Name: "动态", Kind: "feed", Lens: kernel.Lens{Sort: []kernel.Order{{By: "day"}}}}}},
			{Key: "book", Name: "书", Props: []kernel.Prop{
				{Key: "pages", Name: "页数", Kind: kernel.KindNumber, Unit: "页"},
				{Key: "read", Name: "已读", Kind: kernel.KindNumber, Unit: "页"}},
				Card:  kernel.Card{Progress: &kernel.Ratio{Of: "read", Over: "pages"}},
				Views: []kernel.View{{Name: "进度", Kind: "progress"}}},
		}
		for i := range types {
			if err := sp.PutType(types[i], testNow); err != nil {
				return err
			}
		}
		put := func(typ, title string, values map[string]any) (*kernel.Entity, error) {
			return sp.PutEntity(b, kernel.EntityInput{Type: typ, Title: &title, Values: values}, testNow)
		}
		if _, err := put("plain", "有标签", map[string]any{"tag": "甲"}); err != nil {
			return err
		}
		if _, err := put("plain", "没标签", nil); err != nil {
			return err
		}
		if _, err := put("bare", "裸条目", nil); err != nil {
			return err
		}
		root, err := put("loc", "客厅", nil)
		if err != nil {
			return err
		}
		if _, err := put("loc", "沙发", map[string]any{"parent": []string{root.ID}}); err != nil {
			return err
		}
		for _, d := range []struct{ title, day string }{{"很早", "2026-10-01"}, {"昨天的", "2026-11-09"}, {"今天的", "2026-11-10"}, {"无日期", ""}} {
			var values map[string]any
			if d.day != "" {
				values = map[string]any{"day": d.day}
			}
			if _, err := put("due", d.title, values); err != nil {
				return err
			}
		}
		_, err = put("book", "一本书", map[string]any{"pages": 320.0, "read": 96.0})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, id
}

func TestBoardsFeedsAndProgressVariants(t *testing.T) {
	st, id := variantStore(t)
	a := newApp(st)

	// A board grouped by an enumerated property: the options in order, then the unset group.
	v := drawPage(t, a, state{S: id, T: "plain"}, 5)
	titles := sectionTitles(v)
	for _, want := range []string{"甲 · 1", "乙 · 0", "未设置 · 1"} {
		if !slices.Contains(titles, want) {
			t.Fatalf("enum board columns %v lack %q", titles, want)
		}
	}

	// A board of a type with no flow and no enumerated property is one column, without a title.
	// The sections are the head, the one column and the buttons.
	v = drawPage(t, a, state{S: id, T: "bare"}, 5)
	if len(v.Sections) != 3 || v.Sections[1].Title != "" || len(v.Sections[1].Items) != 1 {
		t.Fatalf("one-column board = %+v", v.Sections)
	}

	// A board grouped by a relation: the groups are named by the targets, the unset group comes last of them.
	v = drawPage(t, a, state{S: id, T: "loc"}, 5)
	titles = sectionTitles(v)
	if !slices.Contains(titles, "客厅 · 1") || titles[len(titles)-2] != "未设置 · 1" {
		t.Fatalf("relation board columns %v", titles)
	}

	// A feed ordered by a date: newest day first, days named in the zone, an undated entry last. The head and the
	// buttons of the page have no title; the days are between them.
	v = drawPage(t, a, state{S: id, T: "due"}, 5)
	titles = sectionTitles(v)
	want := []string{"今天", "昨天", "2026-10-01", "未设置"}
	if got := titles[1 : len(titles)-1]; !slices.Equal(got, want) {
		t.Fatalf("feed days = %v, want %v", got, want)
	}

	// A progress from two numbers: the label is both values with their units, the detail the share.
	v = drawPage(t, a, state{S: id, T: "book"}, 5)
	bar := v.Sections[1].Items[1]
	if bar.Type != wire.ComponentProgress || bar.Label != "96 页 / 320 页" || bar.Detail != "30%" {
		t.Fatalf("progress from two numbers = %+v", bar)
	}

	// The view index past the views of the type shows the first view again.
	v = drawPage(t, a, state{S: id, T: "book", V: 9}, 5)
	if decode(t, v).V != 0 {
		t.Fatalf("view index not reset: %+v", decode(t, v))
	}
}

// sectionTitles are the titles of the sections of a view, in order.
func sectionTitles(v *wire.View) []string {
	var out []string
	for _, s := range v.Sections {
		out = append(out, s.Title)
	}
	return out
}

func TestRelativeTimes(t *testing.T) {
	c := &ctx{now: testNow, loc: time.UTC}
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{-time.Minute, "刚刚"},
		{30 * time.Second, "刚刚"},
		{5 * time.Minute, "5 分钟前"},
		{3 * time.Hour, "3 小时前"},
		{48 * time.Hour, "2026-11-08"},
	}
	for _, tc := range cases {
		if got := c.ago(testNow.Add(-tc.ago)); got != tc.want {
			t.Fatalf("ago(%v) = %q, want %q", tc.ago, got, tc.want)
		}
	}
}

func TestToneAndCut(t *testing.T) {
	ty := kernel.Type{Key: "x", Name: "x", Flow: []kernel.Stage{
		{Name: "甲", Phase: kernel.Todo}, {Name: "乙", Phase: kernel.Doing},
		{Name: "丙", Phase: kernel.Done}, {Name: "丁", Phase: kernel.Dropped}}}
	cases := map[string]string{
		"甲": wire.ToneNeutral, "乙": wire.ToneCaution, "丙": wire.TonePositive, "丁": wire.ToneNeutral, "戊": wire.ToneNeutral,
	}
	for status, want := range cases {
		if got := toneOf(&ty, status); got != want {
			t.Fatalf("toneOf(%q) = %q, want %q", status, got, want)
		}
	}
	if got := cut("一二三四", 2); got != "一二" {
		t.Fatalf("cut = %q", got)
	}
	if got := cut("一二", 5); got != "一二" {
		t.Fatalf("cut short = %q", got)
	}
	if got := iconName("nope"); got != "" {
		t.Fatalf("unknown icon = %q", got)
	}
	if got := iconName("star"); got != "star" {
		t.Fatalf("icon = %q", got)
	}
}

func TestStateStaysSmall(t *testing.T) {
	st := storeWith(t, true, "project", "reading", "inventory", "blank")
	a := newApp(st)
	v := drawPage(t, a, state{S: firstSpace(t, st), T: "task", Q: strings.Repeat("字", 200), F: "new"}, 5)
	if len(v.State) > 16<<10 {
		t.Fatalf("state is %d bytes", len(v.State))
	}
}

// openedRows counts the rows of a view that open an entity page; the rows of the tabs and the filter do not.
func openedRows(v *wire.View) int {
	n := 0
	for _, r := range componentsOf(v, wire.ComponentRow) {
		if r.Action != nil && r.Action.Push != nil {
			n++
		}
	}
	return n
}

// busyStore is a blank space with n goals, each with a progress from two numbers, and n notes whose lead is longer than
// a feed row shows under the row: a progress and a feed each put two components on the page for every entity.
func busyStore(t *testing.T, n int) (*store.Store, string) {
	t.Helper()
	st := store.Open("")
	var id string
	err := st.Update(func(b *kernel.Base, _ time.Time) error {
		sp, err := templates.Apply(b, "blank", "书架", false, testNow)
		if err != nil {
			return err
		}
		id = sp.ID
		types := []kernel.Type{
			{Key: "goal", Name: "目标", Props: []kernel.Prop{
				{Key: "total", Name: "总数", Kind: kernel.KindNumber},
				{Key: "done", Name: "完成", Kind: kernel.KindNumber}},
				Card:  kernel.Card{Progress: &kernel.Ratio{Of: "done", Over: "total"}},
				Views: []kernel.View{{Name: "进度", Kind: "progress"}}},
			{Key: "note", Name: "笔记", Props: []kernel.Prop{{Key: "body", Name: "正文", Kind: kernel.KindText}},
				Card:  kernel.Card{Lead: "body"},
				Views: []kernel.View{{Name: "动态", Kind: "feed"}}},
		}
		for i := range types {
			if err := sp.PutType(types[i], testNow); err != nil {
				return err
			}
		}
		long := strings.Repeat("长", 70)
		for i := 0; i < n; i++ {
			goal := fmt.Sprintf("目标 %03d", i)
			values := map[string]any{"total": 10.0, "done": 5.0}
			if _, err := sp.PutEntity(b, kernel.EntityInput{Type: "goal", Title: &goal, Values: values}, testNow); err != nil {
				return err
			}
			note := fmt.Sprintf("笔记 %03d", i)
			if _, err := sp.PutEntity(b, kernel.EntityInput{Type: "note", Title: &note, Values: map[string]any{"body": long}}, testNow); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, id
}

// TestPagesOfTwoComponentsStayUnderTheItemLimit: a progress or a feed page holds at most pairMost entities, so that the
// most extra pages still draw under wire.ItemsMost with the header, the quick add and the buttons.
func TestPagesOfTwoComponentsStayUnderTheItemLimit(t *testing.T) {
	st, id := busyStore(t, 160)
	a := newApp(st)
	for _, typ := range []string{"goal", "note"} {
		v := drawPage(t, a, state{S: id, T: typ, N: maxMore}, 5)
		if n := countComponents(v); n > wire.ItemsMost {
			t.Fatalf("%s at the most pages: %d components, more than %d", typ, n, wire.ItemsMost)
		}
		if rows := openedRows(v); rows != pairMost {
			t.Fatalf("%s at the most pages: %d rows, want %d", typ, rows, pairMost)
		}
	}
}

// TestOlderAppsPageThroughEntitiesWithAButton: an app below UI version 3 has no More, so the page keeps it as a plain
// button that loads the next page, and the button works from the app's own version.
func TestOlderAppsPageThroughEntitiesWithAButton(t *testing.T) {
	const total = 35
	st := crowded(t, total, false)
	a := newApp(st)
	id := firstSpace(t, st)
	for _, version := range []int{1, 2} {
		s := state{S: id, T: "item"}
		v := drawPage(t, a, s, version)
		checkLowered(t, fmt.Sprintf("first page v%d", version), v, version)
		if rows := openedRows(v); rows != pageRows {
			t.Fatalf("v%d first page rows = %d, want %d", version, rows, pageRows)
		}
		more, ok := buttonTitled(v, "更多")
		if !ok || more.Action == nil || more.Action.ID != "more" {
			t.Fatalf("v%d: no plain more button: %+v", version, componentsOf(v, wire.ComponentButton))
		}
		res, err := a.Act(context.Background(), request(version, s, nil, more.Action.ID))
		if err != nil {
			t.Fatalf("v%d more: %v", version, err)
		}
		if rows := openedRows(res.View); rows != min(2*pageRows, total) {
			t.Fatalf("v%d second page rows = %d, want %d", version, rows, min(2*pageRows, total))
		}
	}
}

// TestLowerCutsTheSubtitleOfATableRow: a table row for an older app joins its cells into the subtitle, which is kept to
// the length of a text.
func TestLowerCutsTheSubtitleOfATableRow(t *testing.T) {
	long := strings.Repeat("长", maxText)
	cells := []string{"甲", long, long, long, long, long, long}
	v := &wire.View{Sections: []wire.Section{{Items: []wire.Component{{
		Type: wire.ComponentTable, Columns: make([]wire.TableColumn, len(cells)), Rows: [][]string{cells},
	}}}}}
	low := lower(v, 2)
	rows := componentsOf(low, wire.ComponentRow)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if n := len([]rune(rows[0].Subtitle)); n > maxText {
		t.Fatalf("subtitle is %d runes, more than %d", n, maxText)
	}
	checkView(t, "lowered table", low, false)
}
