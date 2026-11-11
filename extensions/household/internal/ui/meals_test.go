package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/ops"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// TestMealsTabHasIdeasPlansAndRecipes checks the three sections, with a row per planned meal and per recipe.
func TestMealsTabHasIdeasPlansAndRecipes(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	v := render(t, a, state{T: mealTab})
	checkView(t, "meals", v, false)

	ideas := rowsTitled(t, v, "今天吃什么")
	if want := len(rules.MealIdeas(h, testToday, 0)); len(ideas) != want || want == 0 {
		t.Fatalf("%d idea rows, want %d (> 0)", len(ideas), want)
	}
	planned := 0
	for _, m := range h.Meals {
		if m.Status == model.MealPlan {
			planned++
		}
	}
	if got := rowsTitled(t, v, "计划"); len(got) != planned {
		t.Fatalf("%d planned rows, want %d", len(got), planned)
	}
	if got := rowsTitled(t, v, "菜谱"); len(got) != len(h.Recipes) {
		t.Fatalf("%d recipe rows, want %d", len(got), len(h.Recipes))
	}
}

// TestIdeaRowsSayWhatIsMissingOrToUseFirst checks an idea lists what is missing and what to use first.
func TestIdeaRowsSayWhatIsMissingOrToUseFirst(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	ideas := rules.MealIdeas(home(t, st), testToday, 0)
	rows := rowsTitled(t, render(t, a, state{T: mealTab}), "今天吃什么")
	for i, idea := range ideas {
		if i >= len(rows) {
			t.Fatalf("%d rows for %d ideas", len(rows), len(ideas))
		}
		r := rows[i]
		if r.Title != idea.Recipe.Title {
			t.Fatalf("row %d is %q, want %q", i, r.Title, idea.Recipe.Title)
		}
		if len(idea.Missing) > 0 && !strings.Contains(r.Subtitle, "缺："+idea.Missing[0]) {
			t.Fatalf("row %q subtitle %q does not name the missing %q", r.Title, r.Subtitle, idea.Missing[0])
		}
		if len(idea.UseFirst) > 0 && !strings.Contains(r.Subtitle, "先用：") {
			t.Fatalf("row %q subtitle %q does not say what to use first", r.Title, r.Subtitle)
		}
		if _, ok := rowAction(r, "排到今晚"); !ok {
			t.Fatalf("row %q has no 排到今晚", r.Title)
		}
	}
}

// TestPlannedMealsCloseAsDoneOrCancelled checks 已做 and 取消 take a planned meal out of the plan.
func TestPlannedMealsCloseAsDoneOrCancelled(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	recipe := entityID(t, st, model.TRecipe, "蒜蓉生菜")
	act(t, a, state{}, nil, "plan:"+recipe)
	h := home(t, st)
	planned := h.Meals[len(h.Meals)-1]
	if planned.Status != model.MealPlan {
		t.Fatalf("new meal is %q, want 计划", planned.Status)
	}

	res := act(t, a, state{T: mealTab}, nil, "mealdone:"+planned.ID)
	toastHas(t, res, "已做")
	if got := mealStatus(home(t, st), planned.ID); got != model.MealDone {
		t.Fatalf("status %q, want 已做", got)
	}
	for _, r := range rowsTitled(t, res.View, "计划") {
		if formID(r.Action) == planned.ID {
			t.Fatal("a meal marked 已做 is still planned")
		}
	}

	act(t, a, state{}, nil, "plan:"+recipe)
	h = home(t, st)
	again := h.Meals[len(h.Meals)-1]
	res = act(t, a, state{T: mealTab}, nil, "mealcancel:"+again.ID)
	toastHas(t, res, "已取消")
	if got := mealStatus(home(t, st), again.ID); got != model.MealCancel {
		t.Fatalf("status %q, want 取消", got)
	}
}

// TestPlannedMealsAreOrderedByDate checks the 计划 rows are the planned meals, earliest date first.
func TestPlannedMealsAreOrderedByDate(t *testing.T) {
	st := newStore(t, true)
	err := st.Update(func(b *kernel.Base, _ time.Time) error {
		for _, d := range []string{"2026-11-20", "2026-11-12"} {
			title := "测试餐" + d
			in := kernel.EntityInput{Type: model.TMeal, Title: &title, Values: map[string]any{
				"date": d, "slot": "晚餐",
			}}
			if _, err := ops.Save(b, in, testNow, testLoc()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := newApp(st)
	h := home(t, st)
	var want []string
	for _, m := range h.Meals {
		if m.Status == model.MealPlan {
			want = append(want, m.ID)
		}
	}
	rows := rowsTitled(t, render(t, a, state{T: mealTab}), "计划")
	if len(rows) != len(want) {
		t.Fatalf("%d planned rows, want %d", len(rows), len(want))
	}
	for i := 1; i < len(rows); i++ {
		if strings.Compare(dayOfRow(h, rows[i-1]), dayOfRow(h, rows[i])) > 0 {
			t.Fatalf("planned rows out of date order at %d", i)
		}
	}
}

// dayOfRow is the date of the planned meal a row opens, as a string.
func dayOfRow(h *model.Home, r wire.Component) string {
	id := formID(r.Action)
	for _, m := range h.Meals {
		if m.ID == id {
			return string(m.Date)
		}
	}
	return ""
}

// TestMealsTabOffersAddButtons checks 添加菜谱 and 添加餐食 push their forms.
func TestMealsTabOffersAddButtons(t *testing.T) {
	a, _ := newApp(newStore(t, false))
	v := render(t, a, state{T: mealTab})
	checkView(t, "empty meals", v, false)
	for _, title := range []string{"添加菜谱", "添加餐食"} {
		b, ok := buttonTitled(v, title)
		if !ok || b.Action == nil || b.Action.Push == nil {
			t.Fatalf("%s button %+v, want a push to its form", title, b)
		}
	}
}
