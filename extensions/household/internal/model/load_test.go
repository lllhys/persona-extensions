package model

import (
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// put creates one entity of the household space and fails the test on error.
func put(t *testing.T, b *kernel.Base, sp *kernel.Space, now time.Time, in kernel.EntityInput) *kernel.Entity {
	t.Helper()
	e, err := sp.PutEntity(b, in, now)
	if err != nil {
		t.Fatalf("PutEntity(%s): %v", in.Type, err)
	}
	return e
}

// strPtr returns a pointer to s, for EntityInput titles and statuses.
func strPtr(s string) *string { return &s }

// handSpace is a space built without the kernel's checks, to hold values the kernel would refuse.
func handSpace(entities ...*kernel.Entity) *kernel.Space {
	return &kernel.Space{ID: SpaceID, Name: SpaceName, Entities: entities}
}

func TestLoadNilSpaceIsEmpty(t *testing.T) {
	h := Load(nil, time.UTC)
	if h == nil {
		t.Fatal("Load(nil) = nil, want an empty Home")
	}
	if len(h.Places)+len(h.Items)+len(h.Buys)+len(h.Chores)+len(h.Recipes)+len(h.Meals) != 0 {
		t.Errorf("Load(nil) holds records: %+v", h)
	}
}

func TestLoadEmptySpace(t *testing.T) {
	_, sp := ensured(t)
	h := Load(sp, time.UTC)
	if len(h.Places)+len(h.Items)+len(h.Buys)+len(h.Chores)+len(h.Recipes)+len(h.Meals) != 0 {
		t.Errorf("Load of an empty space holds records: %+v", h)
	}
}

func TestLoadEveryType(t *testing.T) {
	b, sp := ensured(t)
	loc := time.UTC
	place := put(t, b, sp, testNow, kernel.EntityInput{Type: TPlace, Title: strPtr("厨房冰箱"),
		Values: map[string]any{"note": "冷藏食材"}})
	milk := put(t, b, sp, testNow, kernel.EntityInput{Type: TItem, Title: strPtr("纯牛奶"), Values: map[string]any{
		"category": "食材", "place": []string{place.ID}, "qty": 2, "unit": "盒", "min": 1,
		"expires": "2026-11-20", "daily": 0.5, "counted": "2026-11-09", "lead": 5, "refill": 3,
		"note": "低脂",
	}})
	// A purchase derived from the item copies unit and category, and records its origin.
	buy, err := sp.Derive(b, milk.ID, TBuy, "", testNow)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if _, err := sp.PutEntity(b, kernel.EntityInput{ID: buy.ID, Values: map[string]any{
		"item": []string{milk.ID}, "amount": 2, "reason": ReasonLow, "note": "周末",
	}}, testNow); err != nil {
		t.Fatalf("PutEntity buy: %v", err)
	}
	put(t, b, sp, testNow, kernel.EntityInput{Type: TChore, Title: strPtr("整理冰箱"), Values: map[string]any{
		"every": 7, "last": "2026-11-03", "next": "2026-11-10", "who": "家人甲", "place": []string{place.ID},
		"minutes": 15, "note": "先清过期食品",
	}})
	recipe := put(t, b, sp, testNow, kernel.EntityInput{Type: TRecipe, Title: strPtr("牛奶布丁"), Values: map[string]any{
		"needs": []string{milk.ID}, "minutes": 20, "url": "https://example.com/recipes/milk-pudding", "note": "冷藏后吃",
	}})
	put(t, b, sp, testNow, kernel.EntityInput{Type: TMeal, Title: strPtr("牛奶布丁晚餐"), Status: strPtr(MealPlan),
		Values: map[string]any{"date": "2026-11-11", "slot": "晚餐", "recipe": recipe.ID, "note": "加一把草莓"}})

	h := Load(sp, loc)

	if len(h.Places) != 1 || h.Places[0] != (Place{ID: place.ID, Title: "厨房冰箱", Note: "冷藏食材", Items: 1}) {
		t.Errorf("Places = %+v", h.Places)
	}
	wantMilk := Item{ID: milk.ID, Title: "纯牛奶", Category: "食材", PlaceID: place.ID, Unit: "盒", Note: "低脂",
		Qty: 2, HasQty: true, Min: 1, HasMin: true, Daily: 0.5, Lead: 5, Refill: 3,
		Expires: "2026-11-20", Counted: "2026-11-09", Updated: "2026-11-10"}
	if len(h.Items) != 1 || h.Items[0] != wantMilk {
		t.Errorf("Items = %+v, want %+v", h.Items, wantMilk)
	}
	if len(h.Buys) != 1 {
		t.Fatalf("Buys = %+v, want one", h.Buys)
	}
	wantBuy := Buy{ID: buy.ID, Title: "纯牛奶", Status: BuyTodo, ItemID: milk.ID, Unit: "盒", Category: "食材",
		Reason: ReasonLow, Note: "周末", Origin: milk.ID, Amount: 2, Updated: testNow}
	if h.Buys[0] != wantBuy {
		t.Errorf("Buys[0] = %+v, want %+v", h.Buys[0], wantBuy)
	}
	wantChore := Chore{ID: h.Chores[0].ID, Title: "整理冰箱", Who: "家人甲", PlaceID: place.ID, Note: "先清过期食品",
		Every: 7, Minutes: 15, Last: "2026-11-03", Next: "2026-11-10"}
	if len(h.Chores) != 1 || h.Chores[0] != wantChore {
		t.Errorf("Chores = %+v, want %+v", h.Chores, wantChore)
	}
	wantRecipe := Recipe{ID: recipe.ID, Title: "牛奶布丁", URL: "https://example.com/recipes/milk-pudding",
		Note: "冷藏后吃", Needs: []string{milk.ID}, Minutes: 20}
	if len(h.Recipes) != 1 || !slices.Equal(h.Recipes[0].Needs, wantRecipe.Needs) ||
		h.Recipes[0].ID != wantRecipe.ID || h.Recipes[0].Title != wantRecipe.Title ||
		h.Recipes[0].URL != wantRecipe.URL || h.Recipes[0].Note != wantRecipe.Note ||
		h.Recipes[0].Minutes != wantRecipe.Minutes {
		t.Errorf("Recipes = %+v, want %+v", h.Recipes, wantRecipe)
	}
	if len(h.Meals) != 1 || h.Meals[0].Status != MealPlan || h.Meals[0].Date != "2026-11-11" ||
		h.Meals[0].Slot != "晚餐" || h.Meals[0].RecipeID != recipe.ID || h.Meals[0].Note != "加一把草莓" {
		t.Errorf("Meals = %+v", h.Meals)
	}
}

func TestLoadMissingValuesAreUnset(t *testing.T) {
	sp := handSpace(
		&kernel.Entity{ID: "e1", Type: TItem, Title: "空白物品"},
		&kernel.Entity{ID: "e2", Type: TPlace, Title: "空白位置"},
		&kernel.Entity{ID: "e3", Type: TBuy, Title: "空白采购", Status: BuyTodo},
		&kernel.Entity{ID: "e4", Type: TChore, Title: "空白家务"},
		&kernel.Entity{ID: "e5", Type: TRecipe, Title: "空白菜谱"},
		&kernel.Entity{ID: "e6", Type: TMeal, Title: "空白餐食", Status: MealPlan},
	)
	h := Load(sp, time.UTC)
	if it := h.Items[0]; it.HasQty || it.HasMin || it.Qty != 0 || it.Min != 0 || it.Expires != "" ||
		it.Counted != "" || it.Daily != 0 || it.Lead != 0 || it.Refill != 0 || it.PlaceID != "" || it.Category != "" {
		t.Errorf("unset item = %+v, want every value unset", it)
	}
	if p := h.Places[0]; p.Note != "" || p.Items != 0 {
		t.Errorf("unset place = %+v", p)
	}
	if b := h.Buys[0]; b.Amount != 0 || b.ItemID != "" || b.Origin != "" || b.Status != BuyTodo {
		t.Errorf("unset buy = %+v", b)
	}
	if c := h.Chores[0]; c.Every != 0 || c.Minutes != 0 || c.Last != "" || c.Next != "" || c.Who != "" {
		t.Errorf("unset chore = %+v", c)
	}
	if r := h.Recipes[0]; r.Needs != nil || r.Minutes != 0 || r.URL != "" {
		t.Errorf("unset recipe = %+v", r)
	}
	if m := h.Meals[0]; m.Date != "" || m.Slot != "" || m.RecipeID != "" || m.Status != MealPlan {
		t.Errorf("unset meal = %+v", m)
	}
}

func TestLoadWrongShapedValuesAreUnset(t *testing.T) {
	sp := handSpace(
		&kernel.Entity{ID: "e1", Type: TItem, Title: "错形物品", Updated: testNow, Values: map[string]any{
			"category": 7, "place": "e2", "qty": "3", "unit": 3.5, "min": true, "expires": 20261120,
			"daily": []any{1}, "counted": "not a date", "lead": "5", "refill": map[string]any{}, "note": []string{"x"},
		}},
		&kernel.Entity{ID: "e2", Type: TPlace, Title: "错形位置", Values: map[string]any{"note": 12}},
		&kernel.Entity{ID: "e3", Type: TBuy, Title: "错形采购", Status: BuyTodo, Values: map[string]any{
			"item": 1, "amount": "2", "unit": false, "category": []string{"食材"}, "reason": nil, "note": 9,
		}},
		&kernel.Entity{ID: "e4", Type: TChore, Title: "错形家务", Values: map[string]any{
			"every": "7", "last": 1, "next": []string{"2026-11-10"}, "who": 2, "place": 3, "minutes": true,
		}},
		&kernel.Entity{ID: "e5", Type: TRecipe, Title: "错形菜谱", Values: map[string]any{
			"needs": "e1", "minutes": "20", "url": 5, "note": false,
		}},
		&kernel.Entity{ID: "e6", Type: TMeal, Title: "错形餐食", Status: MealDone, Values: map[string]any{
			"date": 20261111, "slot": 1, "recipe": []any{"e5"}, "note": []int{1},
		}},
	)
	h := Load(sp, time.UTC)
	if len(h.Items) != 1 {
		t.Fatalf("Items = %d, want 1: a wrong shape must not drop the record", len(h.Items))
	}
	it := h.Items[0]
	if it.Title != "错形物品" || it.ID != "e1" {
		t.Errorf("record identity lost: %+v", it)
	}
	if it.Category != "" || it.PlaceID != "" || it.HasQty || it.Qty != 0 || it.Unit != "" || it.HasMin ||
		it.Min != 0 || it.Expires != "" || it.Daily != 0 || it.Counted != "" || it.Lead != 0 ||
		it.Refill != 0 || it.Note != "" {
		t.Errorf("wrong-shaped item values were not unset: %+v", it)
	}
	if it.Updated != "2026-11-10" {
		t.Errorf("Updated = %q, want 2026-11-10 (the entity's own time is still a day)", it.Updated)
	}
	if h.Places[0].Note != "" {
		t.Errorf("wrong-shaped place note = %q, want unset", h.Places[0].Note)
	}
	if b := h.Buys[0]; b.Amount != 0 || b.ItemID != "" || b.Unit != "" || b.Category != "" ||
		b.Reason != "" || b.Note != "" || b.Status != BuyTodo {
		t.Errorf("wrong-shaped buy = %+v", b)
	}
	if c := h.Chores[0]; c.Every != 0 || c.Last != "" || c.Next != "" || c.Who != "" || c.PlaceID != "" || c.Minutes != 0 {
		t.Errorf("wrong-shaped chore = %+v", c)
	}
	if r := h.Recipes[0]; r.Needs != nil || r.Minutes != 0 || r.URL != "" || r.Note != "" {
		t.Errorf("wrong-shaped recipe = %+v", r)
	}
	if m := h.Meals[0]; m.Date != "" || m.Slot != "" || m.RecipeID != "" || m.Note != "" || m.Status != MealDone {
		t.Errorf("wrong-shaped meal = %+v", m)
	}
}

func TestLoadInvalidDateStringIsUnset(t *testing.T) {
	sp := handSpace(&kernel.Entity{ID: "e1", Type: TItem, Title: "日期错误", Values: map[string]any{
		"expires": "2026-02-30", "counted": "2026-1-5",
	}})
	it := Load(sp, time.UTC).Items[0]
	if it.Expires != "" || it.Counted != "" {
		t.Errorf("invalid dates = %q %q, want unset", it.Expires, it.Counted)
	}
}

func TestLoadUpdatedUsesLocation(t *testing.T) {
	shanghai := time.FixedZone("UTC+8", 8*3600)
	b, sp := ensured(t)
	// 23:00 UTC is already the next day in UTC+8.
	at := time.Date(2026, 11, 10, 23, 0, 0, 0, time.UTC)
	put(t, b, sp, at, kernel.EntityInput{Type: TItem, Title: strPtr("鸡蛋")})
	if got := Load(sp, shanghai).Items[0].Updated; got != "2026-11-11" {
		t.Errorf("Updated in UTC+8 = %q, want 2026-11-11", got)
	}
	if got := Load(sp, time.UTC).Items[0].Updated; got != "2026-11-10" {
		t.Errorf("Updated in UTC = %q, want 2026-11-10", got)
	}
}

func TestLoadPlaceItemCountFromRelations(t *testing.T) {
	b, sp := ensured(t)
	kitchen := put(t, b, sp, testNow, kernel.EntityInput{Type: TPlace, Title: strPtr("厨房柜")})
	put(t, b, sp, testNow, kernel.EntityInput{Type: TPlace, Title: strPtr("杂物柜")})
	put(t, b, sp, testNow, kernel.EntityInput{Type: TItem, Title: strPtr("大米"), Values: map[string]any{"place": []string{kitchen.ID}}})
	put(t, b, sp, testNow, kernel.EntityInput{Type: TItem, Title: strPtr("食用油"), Values: map[string]any{"place": []string{kitchen.ID}}})
	put(t, b, sp, testNow, kernel.EntityInput{Type: TItem, Title: strPtr("创可贴")})
	h := Load(sp, time.UTC)
	counts := map[string]int{}
	for _, p := range h.Places {
		counts[p.Title] = p.Items
	}
	if counts["厨房柜"] != 2 || counts["杂物柜"] != 0 {
		t.Errorf("place item counts = %v, want 厨房柜 2 and 杂物柜 0", counts)
	}
}

func TestLoadKeepsEntityOrder(t *testing.T) {
	b, sp := ensured(t)
	put(t, b, sp, testNow, kernel.EntityInput{Type: TItem, Title: strPtr("第一")})
	put(t, b, sp, testNow, kernel.EntityInput{Type: TPlace, Title: strPtr("位置")})
	put(t, b, sp, testNow, kernel.EntityInput{Type: TItem, Title: strPtr("第二")})
	h := Load(sp, time.UTC)
	if len(h.Items) != 2 || h.Items[0].Title != "第一" || h.Items[1].Title != "第二" {
		t.Errorf("Items order = %+v, want 第一 then 第二", h.Items)
	}
}

func TestHomeLookups(t *testing.T) {
	h := &Home{
		Places:  []Place{{ID: "e1", Title: "厨房柜"}},
		Items:   []Item{{ID: "e2", Title: "大米"}},
		Recipes: []Recipe{{ID: "e3", Title: "白粥"}},
	}
	if it, ok := h.Item("e2"); !ok || it.Title != "大米" {
		t.Errorf("Item(e2) = %+v, %v", it, ok)
	}
	if _, ok := h.Item("e1"); ok {
		t.Error("Item found a place")
	}
	if p, ok := h.Place("e1"); !ok || p.Title != "厨房柜" {
		t.Errorf("Place(e1) = %+v, %v", p, ok)
	}
	if r, ok := h.Recipe("e3"); !ok || r.Title != "白粥" {
		t.Errorf("Recipe(e3) = %+v, %v", r, ok)
	}
	if _, ok := h.Recipe("missing"); ok {
		t.Error("Recipe found a missing id")
	}
}

// TestLoadBuyUpdatedIsTheRecordTime: a purchase keeps the instant its record last changed, whatever the location.
func TestLoadBuyUpdatedIsTheRecordTime(t *testing.T) {
	shanghai := time.FixedZone("UTC+8", 8*3600)
	b, sp := ensured(t)
	// 23:00 UTC is already the next day in UTC+8.
	at := time.Date(2026, 11, 10, 23, 0, 0, 0, time.UTC)
	put(t, b, sp, at, kernel.EntityInput{Type: TBuy, Title: strPtr("鸡蛋")})
	for _, loc := range []*time.Location{shanghai, time.UTC} {
		if got := Load(sp, loc).Buys[0].Updated; !got.Equal(at) {
			t.Errorf("Buy.Updated in %v = %v, want %v", loc, got, at)
		}
	}
}
