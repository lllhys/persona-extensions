package ops

import (
	"testing"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
)

func TestLoadSamplesCreatesTheSampleSet(t *testing.T) {
	b := newBase()
	n, err := LoadSamples(b, now, shanghai)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(model.Samples()) {
		t.Errorf("LoadSamples created %d records, want %d", n, len(model.Samples()))
	}
	h := homeOf(t, b)
	counts := []struct {
		name      string
		got, want int
	}{
		{"places", len(h.Places), 3},
		{"items", len(h.Items), 10},
		{"chores", len(h.Chores), 3},
		{"recipes", len(h.Recipes), 3},
		{"meals", len(h.Meals), 2},
		{"purchases", len(h.Buys), 1},
	}
	for _, c := range counts {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestLoadSamplesFindingsCoverEveryKind(t *testing.T) {
	b := newBase()
	if _, err := LoadSamples(b, now, shanghai); err != nil {
		t.Fatal(err)
	}
	h := homeOf(t, b)
	seen := map[rules.Kind]bool{}
	byTitle := map[string]rules.Kind{}
	for _, f := range rules.Findings(h, today) {
		seen[f.Kind] = true
		byTitle[f.Item.Title] = f.Kind
	}
	for _, k := range []rules.Kind{rules.Expired, rules.Out, rules.Low, rules.Runout, rules.Expiring} {
		if !seen[k] {
			t.Errorf("no finding of kind %s in the samples", k)
		}
	}
	want := map[string]rules.Kind{
		"纯牛奶": rules.Expired,
		"鸡蛋":  rules.Out,
		"酱油":  rules.Low,
		"纸尿裤": rules.Runout,
		"酸奶":  rules.Expiring,
		"生菜":  rules.Expiring,
	}
	for title, kind := range want {
		if byTitle[title] != kind {
			t.Errorf("%s: finding %q, want %q", title, byTitle[title], kind)
		}
	}
	for _, it := range h.Items {
		if _, ok := want[it.Title]; !ok {
			if _, has := byTitle[it.Title]; has {
				t.Errorf("%s has a finding %q, want none", it.Title, byTitle[it.Title])
			}
		}
	}
}

func TestLoadSamplesRelationsAndDates(t *testing.T) {
	b := newBase()
	if _, err := LoadSamples(b, now, shanghai); err != nil {
		t.Fatal(err)
	}
	h := homeOf(t, b)
	cabinet := placeByTitle(t, b, "杂物柜")
	if got := itemByTitle(t, h, "纸尿裤").PlaceID; got != cabinet {
		t.Errorf("diapers in %q, want the cabinet %q", got, cabinet)
	}
	rice := itemByTitle(t, h, "大米").ID
	porridge, ok := recipeByTitle(h, "白粥")
	if !ok || len(porridge.Needs) != 1 || porridge.Needs[0] != rice {
		t.Errorf("porridge needs %v, want [%s]", porridge.Needs, rice)
	}
	dates := []struct {
		name string
		got  model.Day
		want model.Day
	}{
		{"milk expiry", itemByTitle(t, h, "纯牛奶").Expires, "2026-11-08"},
		{"diaper count", itemByTitle(t, h, "纸尿裤").Counted, "2026-11-07"},
		{"yogurt expiry", itemByTitle(t, h, "酸奶").Expires, "2026-11-12"},
		{"dust next", choreByTitle(h, "除尘").Next, "2026-12-05"},
	}
	for _, d := range dates {
		if d.got != d.want {
			t.Errorf("%s = %q, want %q", d.name, d.got, d.want)
		}
	}
	for _, m := range h.Meals {
		if m.Date == "" {
			t.Errorf("meal %q has no date", m.Title)
		}
		if m.Status == model.MealPlan && (m.Date != today || m.RecipeID == "") {
			t.Errorf("planned meal %q on %q for %q, want today with a recipe", m.Title, m.Date, m.RecipeID)
		}
	}
}

func TestLoadSamplesRefusesNonEmptySpace(t *testing.T) {
	b := newBase()
	create(t, b, model.TPlace, "厨房冰箱", nil)
	before := snapshot(t, b)
	if n, err := LoadSamples(b, now, shanghai); err == nil || n != 0 {
		t.Fatalf("LoadSamples into a used space = %d, %v; want an error", n, err)
	}
	if snapshot(t, b) != before {
		t.Error("a refused LoadSamples changed the base")
	}
}

func TestLoadSamplesTwiceRefused(t *testing.T) {
	b := samplesBase(t)
	before := snapshot(t, b)
	if _, err := LoadSamples(b, now, shanghai); err == nil {
		t.Fatal("second LoadSamples succeeded, want an error")
	}
	if snapshot(t, b) != before {
		t.Error("a refused LoadSamples changed the base")
	}
}

func recipeByTitle(h *model.Home, title string) (model.Recipe, bool) {
	for _, r := range h.Recipes {
		if r.Title == title {
			return r, true
		}
	}
	return model.Recipe{}, false
}

func choreByTitle(h *model.Home, title string) model.Chore {
	for _, c := range h.Chores {
		if c.Title == title {
			return c
		}
	}
	return model.Chore{}
}
