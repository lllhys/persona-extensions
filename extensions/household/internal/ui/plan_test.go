package ui

import (
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
)

func TestPlanTonightAddsAPlannedDinner(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	recipe := entityID(t, st, model.TRecipe, "蒜蓉生菜")
	before := len(home(t, st).Meals)

	res := act(t, a, state{}, nil, "plan:"+recipe)
	toastHas(t, res, "已排到今晚")
	h := home(t, st)
	if len(h.Meals) != before+1 {
		t.Fatalf("%d meals, want %d", len(h.Meals), before+1)
	}
	m := h.Meals[len(h.Meals)-1]
	if m.Date != testToday || m.Slot != "晚餐" || m.RecipeID != recipe || m.Status != model.MealPlan {
		t.Fatalf("planned meal %+v, want a 计划 dinner today for the recipe", m)
	}
	if m.Title != "蒜蓉生菜" {
		t.Fatalf("meal title %q, want the recipe's", m.Title)
	}
}

func TestPlanUnknownRecipeIsAToast(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	before := snapshot(t, st)
	res := act(t, a, state{}, nil, "plan:e_none")
	if res.Toast == "" || snapshot(t, st) != before {
		t.Fatal("planning an unknown recipe must be refused without changes")
	}
}

func TestMealDoneAndCancelCloseAPlannedMeal(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	recipe := entityID(t, st, model.TRecipe, "蒜蓉生菜")
	act(t, a, state{}, nil, "plan:"+recipe)
	h := home(t, st)
	planned := h.Meals[len(h.Meals)-1]

	res := act(t, a, state{}, nil, "mealcancel:"+planned.ID)
	toastHas(t, res, "已取消")
	if got := mealStatus(home(t, st), planned.ID); got != model.MealCancel {
		t.Fatalf("status %q, want 取消", got)
	}

	act(t, a, state{}, nil, "plan:"+recipe)
	h = home(t, st)
	next := h.Meals[len(h.Meals)-1]
	act(t, a, state{}, nil, "mealdone:"+next.ID)
	if got := mealStatus(home(t, st), next.ID); got != model.MealDone {
		t.Fatalf("status %q, want 已做", got)
	}
}

func mealStatus(h *model.Home, id string) string {
	for _, m := range h.Meals {
		if m.ID == id {
			return m.Status
		}
	}
	return ""
}

func TestLoadSamplesIntoAnEmptyHousehold(t *testing.T) {
	st := newStore(t, false)
	a, changed := newApp(st)
	res := act(t, a, state{}, nil, "load")
	toastHas(t, res, "已载入")
	if got, want := entityCount(t, st), len(model.Samples()); got != want {
		t.Fatalf("%d records after 载入示例, want %d", got, want)
	}
	if *changed != 1 {
		t.Fatalf("changed called %d times, want 1", *changed)
	}
	if !strings.Contains(res.Toast, "条示例") {
		t.Fatalf("toast %q does not count the samples", res.Toast)
	}
}

func TestLoadSamplesRefusesANonEmptyHousehold(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	before := snapshot(t, st)
	res := act(t, a, state{}, nil, "load")
	toastHas(t, res, "已有记录")
	if snapshot(t, st) != before {
		t.Fatal("a refused 载入示例 changed the base")
	}
}
