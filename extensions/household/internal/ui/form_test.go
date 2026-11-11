package ui

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
)

// fieldNamed is the form field with the name, anywhere on the page.
func fieldNamed(v *wire.View, name string) (wire.Component, bool) {
	for _, c := range components(v) {
		if c.Type == wire.ComponentField && c.Name == name {
			return c, true
		}
	}
	return wire.Component{}, false
}

// newFormState is the state of the form page for a new record of the type.
func newFormState(typ string) state { return state{F: "new:" + typ} }

func TestFormRendersEveryTypeAndValidates(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	for _, typ := range []string{model.TPlace, model.TItem, model.TBuy, model.TChore, model.TRecipe, model.TMeal} {
		v := render(t, a, newFormState(typ))
		checkView(t, "new "+typ, v, false)
		if _, ok := fieldNamed(v, "title"); !ok {
			t.Fatalf("new %s form has no 名称 field", typ)
		}
		if v.Title == "" {
			t.Fatalf("new %s form has no title", typ)
		}
	}
}

func TestSaveNewItemThroughTheForm(t *testing.T) {
	st := newStore(t, true)
	a, changed := newApp(st)
	placeID := entityID(t, st, model.TPlace, "厨房柜")
	res := act(t, a, newFormState(model.TItem), map[string]string{
		"title":      "蛋糕粉",
		"p_category": "=食材",
		"p_qty":      "2",
		"p_unit":     "袋",
		"p_place":    "=" + placeID,
		"p_expires":  "2026-12-01",
		"p_min":      "",
		"p_note":     "",
	}, "save")
	toastHas(t, res, "已添加")
	if !res.Pop {
		t.Fatal("saving the form does not pop back")
	}
	if *changed != 1 {
		t.Fatalf("changed called %d times, want 1", *changed)
	}
	var found bool
	for _, it := range home(t, st).Items {
		if it.Title != "蛋糕粉" {
			continue
		}
		found = true
		if it.Category != "食材" || it.Unit != "袋" || !it.HasQty || it.Qty != 2 || it.PlaceID != placeID {
			t.Fatalf("saved item %+v does not match the form", it)
		}
		if it.Expires != "2026-12-01" {
			t.Fatalf("expires %q, want 2026-12-01", it.Expires)
		}
		if it.Counted != testToday {
			t.Fatalf("counted %q, want today: a quantity given without a count date is counted today", it.Counted)
		}
	}
	if !found {
		t.Fatal("the new item is not stored")
	}
}

func TestSaveQuantityWithEmptyCountDateCountsToday(t *testing.T) {
	st := newStore(t, false)
	a, _ := newApp(st)
	act(t, a, newFormState(model.TItem), map[string]string{
		"title": "电池", "p_qty": "4", "p_counted": "",
	}, "save")
	for _, it := range home(t, st).Items {
		if it.Title == "电池" {
			if it.Counted != testToday {
				t.Fatalf("counted %q, want today", it.Counted)
			}
			return
		}
	}
	t.Fatal("the item is not stored")
}

func TestSaveRequiresATitle(t *testing.T) {
	st := newStore(t, true)
	a, changed := newApp(st)
	before := snapshot(t, st)
	res := act(t, a, newFormState(model.TItem), map[string]string{"title": "   ", "p_qty": "1"}, "save")
	toastHas(t, res, "名称")
	if res.Pop {
		t.Fatal("a refused save pops the form")
	}
	if snapshot(t, st) != before || *changed != 0 {
		t.Fatal("a refused save changed the base")
	}
}

func TestSaveRefusesANumberThatIsNot(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	before := snapshot(t, st)
	res := act(t, a, newFormState(model.TItem), map[string]string{"title": "电池", "p_qty": "很多"}, "save")
	if res.Toast == "" {
		t.Fatal("a number that is not one was saved")
	}
	if snapshot(t, st) != before {
		t.Fatal("a refused save changed the base")
	}
}

func TestEditFormIsPrefilledAndSavesChanges(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	id := entityID(t, st, model.TItem, "纯牛奶")
	f := state{F: "edit:" + id}

	v := render(t, a, f)
	checkView(t, "edit form", v, false)
	if v.Title == "" {
		t.Fatal("edit form has no title")
	}
	title, _ := fieldNamed(v, "title")
	if title.Value != "纯牛奶" {
		t.Fatalf("title field %q, want 纯牛奶", title.Value)
	}
	qty, _ := fieldNamed(v, "p_qty")
	if qty.Value == "" {
		t.Fatal("quantity field is empty for an item with a quantity")
	}
	counted, _ := fieldNamed(v, "p_counted")
	before := counted.Value

	res := act(t, a, f, map[string]string{
		"title":     "纯牛奶 1L",
		"p_qty":     "5",
		"p_counted": before,
		"p_note":    "早上喝",
	}, "save")
	toastHas(t, res, "已保存")
	if !res.Pop {
		t.Fatal("editing does not pop back")
	}
	var got model.Item
	for _, it := range home(t, st).Items {
		if it.ID == id {
			got = it
		}
	}
	if got.Title != "纯牛奶 1L" || got.Qty != 5 || got.Note != "早上喝" {
		t.Fatalf("edited item %+v", got)
	}
	if string(got.Counted) != before {
		t.Fatalf("counted changed to %q by an edit that kept it", got.Counted)
	}
}

func TestEditFormForChoreOffersDone(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	id := entityID(t, st, model.TChore, "拖地")
	v := render(t, a, state{F: "edit:" + id})
	if !hasAction(v, "chore:"+id) {
		t.Fatal("chore form has no 标记完成")
	}
	if !hasAction(v, "delete:"+id) {
		t.Fatal("edit form has no delete")
	}
}

func TestDeleteFromTheFormNeedsConfirmAndPops(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	id := entityID(t, st, model.TItem, "酱油")
	v := render(t, a, state{F: "edit:" + id})
	del, ok := actionOf(v, "delete:"+id)
	if !ok {
		t.Fatal("no delete action on the form")
	}
	if del.Confirm == "" {
		t.Fatal("delete has no confirm")
	}

	res := act(t, a, state{F: "edit:" + id}, nil, "delete:"+id)
	if !res.Pop {
		t.Fatal("deleting the record shown does not pop back")
	}
	toastHas(t, res, "已删除")
	for _, it := range home(t, st).Items {
		if it.ID == id {
			t.Fatal("the record is still stored")
		}
	}
}

func TestDeleteFromTheOverviewRedraws(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	id := entityID(t, st, model.TRecipe, "白粥")
	res := act(t, a, state{}, nil, "delete:"+id)
	if res.Pop {
		t.Fatal("a delete from a tab pops")
	}
	if res.View == nil {
		t.Fatal("a delete from a tab gave no view")
	}
	checkView(t, "after delete", res.View, false)
}

func TestDeleteMissingRecordIsAToast(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	res := act(t, a, state{}, nil, "delete:e_nothing")
	if res.Toast == "" {
		t.Fatal("delete of a missing record gave no toast")
	}
}

func TestStaleFormStateFallsBackToTheOverview(t *testing.T) {
	a, _ := newApp(newStore(t, true))
	v := render(t, a, state{F: "edit:e_gone"})
	if v.Title != "" {
		t.Fatalf("stale form still drawn with title %q", v.Title)
	}
	if v.Tabs == nil || v.Tabs.Value != "over" {
		t.Fatal("stale form did not fall back to the overview")
	}
	if got := viewState(t, v).F; got != "" {
		t.Fatalf("stale form state kept: %q", got)
	}
}

func TestFormPageHasSaveAndBack(t *testing.T) {
	a, _ := newApp(newStore(t, true))
	v := render(t, a, newFormState(model.TPlace))
	if !hasAction(v, "save") {
		t.Fatal("form has no save")
	}
	if !hasAction(v, "back") {
		t.Fatal("form has no cancel")
	}
	save, _ := actionOf(v, "save")
	if !save.Submit {
		t.Fatal("save does not submit the fields")
	}
}

func TestAddPushesTheFormPage(t *testing.T) {
	a, _ := newApp(newStore(t, true))
	res := act(t, a, state{}, nil, "add:"+model.TChore)
	if res.Push == nil {
		t.Fatal("add does not push a page")
	}
	if res.Push.UI != PageUI {
		t.Fatalf("pushed ui %q, want %q", res.Push.UI, PageUI)
	}
	if got := decodeState(t, res.Push.State).F; got != "new:chore" {
		t.Fatalf("pushed form %q, want new:chore", got)
	}
	if !strings.Contains(res.Push.Title, "家务") {
		t.Fatalf("pushed title %q does not name the type", res.Push.Title)
	}
}

func TestAddUnknownTypeIsAToast(t *testing.T) {
	a, _ := newApp(newStore(t, true))
	res := act(t, a, state{}, nil, "add:nothing")
	if res.Toast == "" || res.Push != nil {
		t.Fatalf("add of an unknown type %+v, want a toast and no push", res)
	}
}

func TestEditPushesTheRecord(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	id := entityID(t, st, model.TRecipe, "番茄炒蛋")
	res := act(t, a, state{}, nil, "edit:"+id)
	if res.Push == nil {
		t.Fatal("edit does not push")
	}
	if got := decodeState(t, res.Push.State).F; got != "edit:"+id {
		t.Fatalf("pushed form %q, want edit:%s", got, id)
	}
	if res.Push.Title != "番茄炒蛋" {
		t.Fatalf("pushed title %q, want the record's title", res.Push.Title)
	}
}

func TestEditMissingRecordIsAToast(t *testing.T) {
	a, _ := newApp(newStore(t, true))
	res := act(t, a, state{}, nil, "edit:e_missing")
	if res.Toast == "" || res.Push != nil {
		t.Fatalf("edit of a missing record %+v, want a toast and no push", res)
	}
}

func TestSaveWithNoFormIsAToast(t *testing.T) {
	a, _ := newApp(newStore(t, true))
	res := act(t, a, state{}, map[string]string{"title": "x"}, "save")
	if res.Toast == "" {
		t.Fatal("save with no form open gave no toast")
	}
}

// actionOf is the action of the first component with the action id, on the view.
func actionOf(v *wire.View, id string) (wire.Action, bool) {
	for _, c := range components(v) {
		if c.Action != nil && c.Action.ID == id {
			return *c.Action, true
		}
		if c.Button != nil && c.Button.Action.ID == id {
			return c.Button.Action, true
		}
	}
	for _, c := range components(v) {
		for _, a := range c.Actions {
			if a.Action.ID == id {
				return a.Action, true
			}
		}
	}
	return wire.Action{}, false
}
