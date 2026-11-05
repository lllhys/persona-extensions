package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

var update = flag.Bool("update", false, "write snapshots/ again")

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newApp())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
}

func req(ui, size string, version int, s *state) *ext.UIRequest {
	r := &ext.UIRequest{}
	r.Contribution, r.Size, r.UIVersion = ui, size, version
	if s != nil {
		r.State = ext.EncodeState(*s)
	}
	return r
}

// snapshot is one view the apps draw for their screenshots: snapshots/<name>.json.
type snapshot struct {
	name string
	view func(t *testing.T, a *app) *wire.View
}

func pageOf(s state) func(t *testing.T, a *app) *wire.View {
	return func(t *testing.T, a *app) *wire.View {
		v, err := a.render(context.Background(), req(pageUI, wire.SizeFull, 3, &s))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func snapshots() []snapshot {
	out := []snapshot{{"home", pageOf(state{})}}
	for _, p := range pages {
		out = append(out, snapshot{p.id, pageOf(state{P: p.id})})
	}
	out = append(out,
		snapshot{"list-search", pageOf(state{P: "list", Q: "1"})},
		snapshot{"list-filter", pageOf(state{P: "list", Kind: "chat"})},
		snapshot{"list-more", pageOf(state{P: "list", Shown: 40})},
		snapshot{"list-none", pageOf(state{P: "list", Q: "没有这一条"})},
		snapshot{"detail", pageOf(state{P: "detail", ID: "3"})},
		snapshot{"edit", pageOf(state{P: "edit", ID: "3"})},
		snapshot{"segmented-30d", pageOf(state{P: "segmented", Range: "30d", Answer: "no"})},
		snapshot{"invalidate-loading", func(t *testing.T, a *app) *wire.View {
			a.asked = time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC)
			return pageOf(state{P: "invalidate"})(t, a)
		}},
		snapshot{"invalidate-done", func(t *testing.T, a *app) *wire.View {
			a.asked = time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC)
			a.came = a.asked.Add(3 * time.Second)
			return pageOf(state{P: "invalidate"})(t, a)
		}},
		snapshot{"card", func(t *testing.T, a *app) *wire.View {
			v, err := a.card(context.Background(), req(cardUI, wire.SizeCompact, 3, nil))
			if err != nil {
				t.Fatal(err)
			}
			return v
		}},
		snapshot{"shortcut-count", func(t *testing.T, a *app) *wire.View {
			res, err := a.shortcut(context.Background(), "run", json.RawMessage(`{"contribution":"showcase-count","values":{"n":"3","unit":"star"},"ui_version":3}`))
			if err != nil {
				t.Fatal(err)
			}
			return res.(wire.ShortcutResult).View
		}},
	)
	for _, f := range wire.WidgetFamilies {
		f := f
		out = append(out, snapshot{"widget-" + f, func(t *testing.T, a *app) *wire.View {
			v, err := a.widget(context.Background(), req(widgetUI, f, 3, nil))
			if err != nil {
				t.Fatal(err)
			}
			return v
		}})
	}
	return out
}

// TestSnapshots holds every page to the schema and to snapshots/; go test -run TestSnapshots -update writes them.
func TestSnapshots(t *testing.T) {
	for _, s := range snapshots() {
		v := s.view(t, newApp())
		if err := wire.Validate("View", v); err != nil {
			t.Errorf("%s: %v", s.name, err)
			continue
		}
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		b = append(b, '\n')
		path := filepath.Join("snapshots", s.name+".json")
		if *update {
			if err := os.MkdirAll("snapshots", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, b, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		old, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(old, b) {
			t.Errorf("snapshots/%s.json is not what the page draws: go test -run TestSnapshots -update", s.name)
		}
	}
	if !*update { // no snapshot of a page that is gone
		files, _ := filepath.Glob(filepath.Join("snapshots", "*.json"))
		if len(files) != len(snapshots()) {
			t.Errorf("snapshots/ has %d files, the pages %d: go test -run TestSnapshots -update (after removing snapshots/)", len(files), len(snapshots()))
		}
	}
}

// TestEveryComponent: the pages show every component type an extension can draw.
func TestEveryComponent(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range snapshots() {
		for _, sec := range s.view(t, newApp()).Sections {
			for _, c := range sec.Items {
				seen[c.Type] = true
			}
		}
	}
	for _, typ := range extensionComponents() {
		if !seen[typ] {
			t.Errorf("no page shows %s", typ)
		}
	}
}

// TestEditFlow: list → detail → edit → save (pop) → the detail drawn again shows the edit.
func TestEditFlow(t *testing.T) {
	a := newApp()
	ctx := context.Background()
	list := pageOf(state{P: "list"})(t, a)
	push := list.Sections[0].Items[2].Action.Push
	if push == nil {
		t.Fatal("a row opens its detail on top of the list")
	}
	r := req(pageUI, wire.SizeFull, 3, nil)
	r.State = push.State
	detail, _ := a.render(ctx, r)
	var edit *wire.Push
	for _, c := range detail.Sections[0].Items {
		if c.Type == wire.ComponentButton && c.Action != nil && c.Action.Push != nil {
			edit = c.Action.Push
		}
	}
	if edit == nil {
		t.Fatal("the detail's 编辑 opens the form on top")
	}
	r.State, r.Action = edit.State, "save"
	r.Values = map[string]string{"title": "改过的标题", "kind": "mail", "note": "新的备注", "done": "true"}
	res, err := a.action(ctx, r)
	if err != nil || res == nil || !res.Pop {
		t.Fatalf("save: %+v %v", res, err)
	}
	r.State, r.Action, r.Values = push.State, "", nil
	detail, _ = a.render(ctx, r)
	if detail.Title != "改过的标题" {
		t.Fatalf("after saving the detail is %q", detail.Title)
	}
	r.State, r.Action, r.Values = edit.State, "save", map[string]string{"title": " "}
	if res, _ := a.action(ctx, r); res == nil || res.Pop || res.Toast == "" {
		t.Fatalf("an empty title is refused: %+v", res)
	}
}

// TestEditBack: apps before UI version 3 draw pages in place and have no native stack back,
// so the edit page renders its own back button, exactly like detail(); v3 apps render neither.
func TestEditBack(t *testing.T) {
	a := newApp()
	ctx := context.Background()
	find := func(v *wire.View) *wire.Component {
		for _, sec := range v.Sections {
			for i := range sec.Items {
				if c := &sec.Items[i]; c.Action != nil && c.Action.ID == "back" {
					return c
				}
			}
		}
		return nil
	}
	v2, _ := a.render(ctx, req(pageUI, wire.SizeFull, 2, &state{P: "edit", ID: "3"}))
	back := find(v2)
	if back == nil {
		t.Fatal("the edit page on a pre-v3 app renders a back button")
	}
	if back.Type != wire.ComponentButton || back.Title != "回到列表" {
		t.Fatalf("the pre-v3 back button is %+v, want the same button detail() renders", back)
	}
	v3, _ := a.render(ctx, req(pageUI, wire.SizeFull, 3, &state{P: "edit", ID: "3"}))
	if find(v3) != nil {
		t.Fatal("the edit page on v3 renders no back button (the client provides native stack back)")
	}
}

// TestOldAppListContext: before UI version 3 pages are drawn in place, so the detail and edit pushes carry the
// list's search, filter and loaded page; 回到列表 then redraws the list the user left, as native pop does on v3.
func TestOldAppListContext(t *testing.T) {
	a := newApp()
	ctx := context.Background()
	was := state{P: "list", Q: "示例", Kind: "doc", Shown: 40}

	lv, _ := a.render(ctx, req(pageUI, wire.SizeFull, 2, &was))
	row := lv.Sections[1].Items[0] // sections: the old-app tools, the list, the return-to-catalog button
	if row.Action == nil || row.Action.Push == nil {
		t.Fatal("a list row opens its detail")
	}
	var ds state
	if err := json.Unmarshal([]byte(row.Action.Push.State), &ds); err != nil {
		t.Fatal(err)
	}
	if ds.Q != was.Q || ds.Kind != was.Kind || ds.Shown != was.Shown {
		t.Fatalf("the detail push carries %+v, want the list's context %+v", ds, was)
	}

	// The detail drawn in place keeps the context both in its view state and in the edit's push.
	r := req(pageUI, wire.SizeFull, 2, nil)
	r.State = row.Action.Push.State
	dv, _ := a.render(ctx, r)
	var held state
	if err := json.Unmarshal([]byte(dv.State), &held); err != nil {
		t.Fatal(err)
	}
	if held.Q != was.Q || held.Kind != was.Kind || held.Shown != was.Shown {
		t.Fatalf("the in-place detail holds %+v, want the list's context %+v", held, was)
	}
	var editPush *wire.Push
	for _, c := range dv.Sections[0].Items {
		if c.Type == wire.ComponentButton && c.Action != nil && c.Action.Push != nil {
			editPush = c.Action.Push
		}
	}
	if editPush == nil {
		t.Fatal("the detail's 编辑 opens the form")
	}
	var es state
	if err := json.Unmarshal([]byte(editPush.State), &es); err != nil {
		t.Fatal(err)
	}
	if es.Q != was.Q || es.Kind != was.Kind || es.Shown != was.Shown {
		t.Fatalf("the edit push carries %+v, want the list's context %+v", es, was)
	}

	// Back from the detail, and back from the edit, each redraws the list exactly where it was left.
	for _, from := range []string{row.Action.Push.State, editPush.State} {
		r.State, r.Action = from, "back"
		if _, err := a.action(ctx, r); err != nil {
			t.Fatal(err)
		}
		var back state
		if err := json.Unmarshal([]byte(r.State), &back); err != nil {
			t.Fatal(err)
		}
		if back.P != "list" || back.ID != "" || back.Q != was.Q || back.Kind != was.Kind || back.Shown != was.Shown {
			t.Fatalf("back lands on %+v, want the list with its context", back)
		}
		r.Action = ""
		again, _ := a.render(ctx, r)
		tools, listSec := again.Sections[0].Items, again.Sections[1].Items
		if tools[0].Value != was.Q {
			t.Fatalf("the redrawn list's search box is %q, want %q", tools[0].Value, was.Q)
		}
		if tools[1].Value != was.Kind {
			t.Fatalf("the redrawn list's kind choice is %q, want %q", tools[1].Value, was.Kind)
		}
		rows, more := 0, false
		for _, c := range listSec {
			switch {
			case c.Type == wire.ComponentRow:
				rows++
			case c.Type == wire.ComponentButton && c.Title == "加载更多":
				more = true
			}
		}
		if rows != 20 || more { // 20 of the 60 items are 文档; Shown 40 clamps to 20, so there is no second page
			t.Fatalf("the redrawn doc-filtered list shows %d rows and a load-more button %v", rows, more)
		}
	}

	// A search matching all 60 with 40 loaded keeps its loaded page and load-more button after the round trip.
	full := state{P: "list", Q: "示例", Shown: 40}
	fv, _ := a.render(ctx, req(pageUI, wire.SizeFull, 2, &full))
	r.State, r.Action = fv.Sections[1].Items[0].Action.Push.State, "back"
	if _, err := a.action(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Action = ""
	again, _ := a.render(ctx, r)
	rows, more := 0, false
	for _, c := range again.Sections[1].Items {
		switch {
		case c.Type == wire.ComponentRow:
			rows++
		case c.Type == wire.ComponentButton && c.Title == "加载更多":
			more = true
		}
	}
	if rows != 40 || !more {
		t.Fatalf("the redrawn list shows %d rows and a load-more button %v, want 40 and one", rows, more)
	}

	// v3 keeps using the native stack: its pushes carry no list context, so v3 views are unchanged.
	v3, _ := a.render(ctx, req(pageUI, wire.SizeFull, 3, &full))
	var vs state
	if err := json.Unmarshal([]byte(v3.Sections[0].Items[0].Action.Push.State), &vs); err != nil {
		t.Fatal(err)
	}
	if vs.Q != "" || vs.Kind != "" || vs.Shown != 0 {
		t.Fatalf("a v3 detail push carries list context %+v; the native stack already preserves the list", vs)
	}
}

// TestListMore: load more shows 20 more, until all 60; search and filter start over.
func TestListMore(t *testing.T) {
	a := newApp()
	ctx := context.Background()
	r := req(pageUI, wire.SizeFull, 3, &state{P: "list"})
	for _, want := range []int{40, 60} {
		r.Action = "more"
		if _, err := a.action(ctx, r); err != nil {
			t.Fatal(err)
		}
		r.Action = ""
		v, _ := a.render(ctx, r)
		if n := len(v.Sections[0].Items); n != want {
			t.Fatalf("after 加载更多: %d rows, want %d", n, want)
		}
		if (v.Sections[0].More != nil) != (want < 60) {
			t.Fatalf("加载更多 with %d of 60 shown: %v", want, v.Sections[0].More)
		}
	}
	r.Action, r.Values = "kind", map[string]string{"kind": "doc", "q": "示例"}
	_, _ = a.action(ctx, r)
	r.Action = ""
	v, _ := a.render(ctx, r)
	if n := len(v.Sections[0].Items); n != 20 || v.Filter.Value != "doc" || v.Search.Value != "示例" {
		t.Fatalf("filtered: %d rows, filter %q, search %q", n, v.Filter.Value, v.Search.Value)
	}
	// An app before UI version 3 gets a field, a choice and buttons instead.
	v, _ = a.render(ctx, req(pageUI, wire.SizeFull, 2, &state{P: "list"}))
	if v.Search != nil || v.Filter != nil || v.Sections[1].More != nil || v.Sections[0].Items[0].Type != wire.ComponentField {
		t.Fatalf("for an older app: %+v", v)
	}
}

// TestInvalidate: the request-data action answers loading, and the data comes after a.after.
func TestInvalidate(t *testing.T) {
	a := newApp()
	a.after = 10 * time.Millisecond
	ctx := context.Background()
	r := req(pageUI, wire.SizeFull, 3, &state{P: "invalidate"})
	r.Action = "ask"
	_, _ = a.action(ctx, r)
	v, _ := a.render(ctx, r)
	if v.Sections[0].Items[1].Type != wire.ComponentLoading {
		t.Fatalf("right after asking: %s", v.Sections[0].Items[1].Type)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v, _ = a.render(ctx, r); v.Sections[0].Items[1].Type == wire.ComponentMetric {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the data never came")
}

func TestShortcuts(t *testing.T) {
	a := newApp()
	ctx := context.Background()
	res, err := a.shortcut(ctx, "run", json.RawMessage(`{"contribution":"showcase-echo","values":{"text":"你好"},"ui_version":3}`))
	if err != nil || res.(wire.ShortcutResult).Text != "你说：你好" {
		t.Fatalf("echo: %+v %v", res, err)
	}
	if _, err := a.shortcut(ctx, "run", json.RawMessage(`{"contribution":"showcase-count","values":{"n":"x"},"ui_version":3}`)); err == nil {
		t.Fatal("counting to x is refused")
	}
	if err := wire.Validate("ShortcutResult", mustShortcut(t, a)); err != nil {
		t.Fatal(err)
	}
}

func mustShortcut(t *testing.T, a *app) any {
	res, err := a.shortcut(context.Background(), "run", json.RawMessage(`{"contribution":"showcase-count","values":{"n":"3","loud":"true"},"ui_version":3}`))
	if err != nil {
		t.Fatal(err)
	}
	return res
}
