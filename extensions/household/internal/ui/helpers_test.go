package ui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/ops"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/store"
)

// testNow is a morning in November 2026; the test day is its date in testLoc.
var testNow = time.Date(2026, time.November, 10, 9, 30, 0, 0, time.UTC)

const testToday = model.Day("2026-11-10")

// testLoc is the time zone of the tests.
func testLoc() *time.Location { return time.UTC }

// newStore is an in-memory store, holding the sample records when samples is set.
func newStore(t *testing.T, samples bool) *store.Store {
	t.Helper()
	st := store.Open("")
	if samples {
		err := st.Update(func(b *kernel.Base, _ time.Time) error {
			_, err := ops.LoadSamples(b, testNow, testLoc())
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// newApp is the app over st with the clock of the tests. The returned counter counts the calls of the changed hook.
func newApp(st kernel.Storage) (*App, *int) {
	n := 0
	a := New(st, testLoc, func() { n++ })
	a.now = func() time.Time { return testNow }
	return a, &n
}

// request is a request to the nav page in state s, with the values and the action sent.
func request(s state, values map[string]string, action string) *ext.UIRequest {
	return &ext.UIRequest{UIParams: wire.UIParams{
		Contribution: PageUI, State: ext.EncodeState(s), Action: action, Values: values,
		Placement: "nav_page", Size: "full", UIVersion: 6, TZ: "Asia/Shanghai", Locale: "zh-Hans-CN", Platform: "web",
	}}
}

// render draws the nav page in state s.
func render(t *testing.T, a *App, s state) *wire.View {
	t.Helper()
	v, err := a.Page().Render(context.Background(), request(s, nil, ""))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// act sends an action to the nav page in state s.
func act(t *testing.T, a *App, s state, values map[string]string, action string) *wire.ActionResult {
	t.Helper()
	res, err := a.Page().Action(context.Background(), request(s, values, action))
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	return res
}

// viewState reads the state a view carries.
func viewState(t *testing.T, v *wire.View) state {
	t.Helper()
	var s state
	if v.State == "" {
		return s
	}
	if err := json.Unmarshal([]byte(v.State), &s); err != nil {
		t.Fatalf("view state: %v", err)
	}
	return s
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

// components lists every component of a view, section by section.
func components(v *wire.View) []wire.Component {
	var out []wire.Component
	for _, s := range v.Sections {
		out = append(out, s.Items...)
	}
	return out
}

// sectionTitled is the section with the title.
func sectionTitled(v *wire.View, title string) (wire.Section, bool) {
	for _, s := range v.Sections {
		if s.Title == title {
			return s, true
		}
	}
	return wire.Section{}, false
}

// actionIDs lists the ids of every action on the view: tap actions, button actions and row actions.
func actionIDs(v *wire.View) []string {
	var ids []string
	for _, c := range components(v) {
		if c.Action != nil {
			ids = append(ids, c.Action.ID)
		}
		if c.Button != nil {
			ids = append(ids, c.Button.Action.ID)
		}
		for _, a := range c.Actions {
			ids = append(ids, a.Action.ID)
		}
	}
	for _, s := range v.Sections {
		if s.More != nil {
			ids = append(ids, s.More.Action.ID)
		}
	}
	if v.Tabs != nil {
		ids = append(ids, v.Tabs.Action.ID)
	}
	return ids
}

// hasAction reports whether the view has an action with the id, or with the prefix id followed by a colon.
func hasAction(v *wire.View, id string) bool {
	for _, got := range actionIDs(v) {
		if got == id || strings.HasPrefix(got, id+":") {
			return true
		}
	}
	return false
}

// home reads the household records of the store.
func home(t *testing.T, st kernel.Storage) *model.Home {
	t.Helper()
	var h *model.Home
	err := st.View(func(b *kernel.Base) error {
		h = model.Load(householdSpace(b), testLoc())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// entityID is the id of the record of the household space with the type and title, failing the test when there is none.
func entityID(t *testing.T, st kernel.Storage, typ, title string) string {
	t.Helper()
	var id string
	err := st.View(func(b *kernel.Base) error {
		sp := householdSpace(b)
		if sp == nil {
			return nil
		}
		for _, e := range sp.Entities {
			if e.Type == typ && e.Title == title {
				id = e.ID
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatalf("no %s titled %q", typ, title)
	}
	return id
}

// snapshot is the stored base as JSON, to tell whether an action changed anything.
func snapshot(t *testing.T, st kernel.Storage) string {
	t.Helper()
	var out string
	err := st.View(func(b *kernel.Base) error {
		raw, err := json.Marshal(b)
		out = string(raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// entityCount is the number of records in the household space.
func entityCount(t *testing.T, st kernel.Storage) int {
	t.Helper()
	h := home(t, st)
	return len(h.Items) + len(h.Places) + len(h.Buys) + len(h.Chores) + len(h.Recipes) + len(h.Meals)
}

// toastHas fails the test unless the result is a toast that contains the words.
func toastHas(t *testing.T, res *wire.ActionResult, words string) {
	t.Helper()
	if !strings.Contains(res.Toast, words) {
		t.Fatalf("toast %q, want it to contain %q", res.Toast, words)
	}
}

// decodeState reads a state given as the JSON of ext.EncodeState, as a pushed page carries it.
func decodeState(t *testing.T, raw string) state {
	t.Helper()
	var s state
	if raw == "" {
		return s
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("state %q: %v", raw, err)
	}
	return s
}
