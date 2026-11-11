package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/ops"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/store"
)

func TestItemFormFieldsFollowTheKinds(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	v := render(t, a, newFormState(model.TItem))
	want := map[string]string{
		"p_category": wire.FieldKindChoice,
		"p_place":    wire.FieldKindChoice,
		"p_qty":      wire.FieldKindNumber,
		"p_unit":     wire.FieldKindText,
		"p_min":      wire.FieldKindNumber,
		"p_expires":  wire.FieldKindDate,
		"p_counted":  wire.FieldKindDate,
		"p_daily":    wire.FieldKindNumber,
		"p_lead":     wire.FieldKindNumber,
		"p_refill":   wire.FieldKindNumber,
		"p_note":     wire.FieldKindText,
	}
	for name, kind := range want {
		f, ok := fieldNamed(v, name)
		if !ok {
			t.Fatalf("item form has no %s", name)
		}
		if f.Kind != kind {
			t.Fatalf("%s kind %q, want %q", name, f.Kind, kind)
		}
	}
	if _, ok := fieldNamed(v, "p_items"); ok {
		t.Fatal("the rollup 'items' is on the form")
	}
	cat, _ := fieldNamed(v, "p_category")
	if len(cat.Choices) != len(model.Categories)+1 || cat.Choices[0].Value != "=" {
		t.Fatalf("category choices %+v, want unset then the seven categories", cat.Choices)
	}
}

func TestRecipeFormOffersItemsAsAMultichoice(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	v := render(t, a, newFormState(model.TRecipe))
	needs, ok := fieldNamed(v, "p_needs")
	if !ok {
		t.Fatal("recipe form has no needs field")
	}
	if needs.Kind != wire.FieldKindMultichoice {
		t.Fatalf("needs kind %q, want multichoice", needs.Kind)
	}
	if len(needs.Choices) == 0 {
		t.Fatal("needs has no choices although there are items")
	}
}

func TestFormForAnItemWithNoPlaceChoicesOnlyUnset(t *testing.T) {
	a, _ := newApp(newStore(t, false))
	v := render(t, a, newFormState(model.TItem))
	checkView(t, "item form without records", v, false)
	f, ok := fieldNamed(v, "p_place")
	if !ok {
		t.Fatal("no place field")
	}
	if len(f.Choices) != 1 || f.Value != "=" {
		t.Fatalf("place choices %+v value %q, want only 未设置", f.Choices, f.Value)
	}
}

func TestMultichoiceWithNoTargetsIsLeftOut(t *testing.T) {
	a, _ := newApp(newStore(t, false))
	v := render(t, a, newFormState(model.TRecipe))
	checkView(t, "recipe form without items", v, false)
	if _, ok := fieldNamed(v, "p_needs"); ok {
		t.Fatal("a multichoice with no choices is on the form")
	}
}

func TestEditFormShowsTheRecordsRelationAsItsChoice(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	id := entityID(t, st, model.TItem, "纯牛奶")
	v := render(t, a, state{F: "edit:" + id})
	place, _ := fieldNamed(v, "p_place")
	if place.Value == "=" {
		t.Fatal("place field is unset for a record that has a place")
	}
	found := false
	for _, ch := range place.Choices {
		if ch.Value == place.Value {
			found = true
		}
	}
	if !found {
		t.Fatalf("place value %q is not one of its choices", place.Value)
	}
}

// TestRecipeWithMoreNeedsThanTheFieldShowsLeavesTheFieldOff checks a recipe holding more items than a multichoice can
// offer has no needs field, and saving its form leaves the needs as they are.
func TestRecipeWithMoreNeedsThanTheFieldShowsLeavesTheFieldOff(t *testing.T) {
	st := store.Open("")
	var recipe string
	var needs []string
	err := st.Update(func(b *kernel.Base, _ time.Time) error {
		for i := range multiMost + 1 {
			title := fmt.Sprintf("食材%03d", i)
			e, err := ops.Save(b, kernel.EntityInput{Type: model.TItem, Title: &title}, testNow, testLoc())
			if err != nil {
				return err
			}
			needs = append(needs, e.ID)
		}
		title := "多材料菜"
		e, err := ops.Save(b, kernel.EntityInput{Type: model.TRecipe, Title: &title,
			Values: map[string]any{"needs": needs}}, testNow, testLoc())
		if err != nil {
			return err
		}
		recipe = e.ID
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := newApp(st)
	v := render(t, a, state{F: "edit:" + recipe})
	checkView(t, "recipe with many needs", v, false)
	if _, ok := fieldNamed(v, "p_needs"); ok {
		t.Fatal("needs field is on the form although it cannot show every held item")
	}

	act(t, a, state{F: "edit:" + recipe}, map[string]string{"title": "多材料菜改名"}, "save")
	h := home(t, st)
	r, ok := h.Recipe(recipe)
	if !ok {
		t.Fatal("recipe is gone")
	}
	if r.Title != "多材料菜改名" {
		t.Fatalf("title %q, want the saved one", r.Title)
	}
	if len(r.Needs) != len(needs) {
		t.Fatalf("needs %d after saving the form, want %d untouched", len(r.Needs), len(needs))
	}
}
