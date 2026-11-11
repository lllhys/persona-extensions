package model

import (
	"slices"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// Place is a 位置. Items counts the 物品 stored here (the place.items rollup, computed by Load).
type Place struct {
	ID, Title, Note string
	Items           int
}

// Item is a 物品. HasQty and HasMin tell an unset quantity or threshold from zero; Daily, Lead and Refill use 0 for
// unset. Updated is the day the record last changed.
type Item struct {
	ID, Title, Category, PlaceID, Unit, Note string
	Qty                                      float64
	HasQty                                   bool
	Min                                      float64
	HasMin                                   bool
	Daily, Lead, Refill                      float64
	Expires, Counted, Updated                Day
}

// Buy is a 采购. Origin is the id of the record it was derived from, if any. Updated is when the record last changed;
// for a bought purchase, that is when it was bought.
type Buy struct {
	ID, Title, Status, ItemID, Unit, Category, Reason, Note, Origin string
	Amount                                                          float64
	Updated                                                         time.Time
}

// Chore is a 家务. Every and Minutes use 0 for unset.
type Chore struct {
	ID, Title, Who, PlaceID, Note string
	Every, Minutes                float64
	Last, Next                    Day
}

// Recipe is a 菜谱 with the ids of the items it needs.
type Recipe struct {
	ID, Title, URL, Note string
	Needs                []string
	Minutes              float64
}

// Meal is a 餐食 on a date and slot, for a recipe.
type Meal struct {
	ID, Title, Status, Slot, RecipeID, Note string
	Date                                    Day
}

// Home is the household space read into typed records, each kind in the entity order of the space.
type Home struct {
	Places  []Place
	Items   []Item
	Buys    []Buy
	Chores  []Chore
	Recipes []Recipe
	Meals   []Meal
}

// Load reads the space into typed records. A value of the wrong shape is treated as unset, and so is a missing one.
// A nil space gives an empty Home. loc turns the update time of an item into a Day; a purchase keeps its time.
func Load(sp *kernel.Space, loc *time.Location) *Home {
	h := &Home{}
	if sp == nil {
		return h
	}
	for _, e := range sp.Entities {
		if e == nil {
			continue
		}
		switch e.Type {
		case TPlace:
			h.Places = append(h.Places, Place{ID: e.ID, Title: e.Title, Note: text(e, "note")})
		case TItem:
			h.Items = append(h.Items, loadItem(e, loc))
		case TBuy:
			h.Buys = append(h.Buys, Buy{
				ID: e.ID, Title: e.Title, Status: e.Status, Origin: e.Origin,
				ItemID: one(e, "item"), Amount: num(e, "amount"), Unit: text(e, "unit"),
				Category: text(e, "category"), Reason: text(e, "reason"), Note: text(e, "note"),
				Updated: e.Updated,
			})
		case TChore:
			h.Chores = append(h.Chores, Chore{
				ID: e.ID, Title: e.Title, Who: text(e, "who"), PlaceID: one(e, "place"), Note: text(e, "note"),
				Every: num(e, "every"), Minutes: num(e, "minutes"), Last: day(e, "last"), Next: day(e, "next"),
			})
		case TRecipe:
			h.Recipes = append(h.Recipes, Recipe{
				ID: e.ID, Title: e.Title, URL: text(e, "url"), Note: text(e, "note"),
				Needs: many(e, "needs"), Minutes: num(e, "minutes"),
			})
		case TMeal:
			h.Meals = append(h.Meals, Meal{
				ID: e.ID, Title: e.Title, Status: e.Status, Slot: text(e, "slot"), RecipeID: one(e, "recipe"),
				Note: text(e, "note"), Date: day(e, "date"),
			})
		}
	}
	counts := map[string]int{}
	for _, it := range h.Items {
		if it.PlaceID != "" {
			counts[it.PlaceID]++
		}
	}
	for i := range h.Places {
		h.Places[i].Items = counts[h.Places[i].ID]
	}
	return h
}

func loadItem(e *kernel.Entity, loc *time.Location) Item {
	it := Item{
		ID: e.ID, Title: e.Title, Category: text(e, "category"), PlaceID: one(e, "place"),
		Unit: text(e, "unit"), Note: text(e, "note"),
		Min: num(e, "min"), Daily: num(e, "daily"), Lead: num(e, "lead"), Refill: num(e, "refill"),
		Expires: day(e, "expires"), Counted: day(e, "counted"), Updated: DayOf(e.Updated, loc),
	}
	it.Qty, it.HasQty = number(e, "qty")
	_, it.HasMin = number(e, "min")
	return it
}

// Item returns the item with the id.
func (h *Home) Item(id string) (Item, bool) {
	i := slices.IndexFunc(h.Items, func(it Item) bool { return it.ID == id })
	if i < 0 {
		return Item{}, false
	}
	return h.Items[i], true
}

// Place returns the place with the id.
func (h *Home) Place(id string) (Place, bool) {
	i := slices.IndexFunc(h.Places, func(p Place) bool { return p.ID == id })
	if i < 0 {
		return Place{}, false
	}
	return h.Places[i], true
}

// Recipe returns the recipe with the id.
func (h *Home) Recipe(id string) (Recipe, bool) {
	i := slices.IndexFunc(h.Recipes, func(r Recipe) bool { return r.ID == id })
	if i < 0 {
		return Recipe{}, false
	}
	return h.Recipes[i], true
}

// The readers below take a stored value of the kind the schema declares; anything else reads as unset.

// text is a text, enum or stored status value.
func text(e *kernel.Entity, key string) string {
	s, _ := e.Values[key].(string)
	return s
}

// number is a number value, and whether one is set.
func number(e *kernel.Entity, key string) (float64, bool) {
	f, ok := e.Values[key].(float64)
	return f, ok
}

// num is a number value, or 0 when unset.
func num(e *kernel.Entity, key string) float64 {
	f, _ := number(e, key)
	return f
}

// day is a date value, or the unset Day when the value is not a valid date.
func day(e *kernel.Entity, key string) Day {
	s, ok := e.Values[key].(string)
	if !ok {
		return ""
	}
	d, _ := ParseDay(s)
	return d
}

// one is the first target of a relation, or "" when unset.
func one(e *kernel.Entity, key string) string {
	ids, _ := e.Values[key].([]string)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// many is every target of a relation.
func many(e *kernel.Entity, key string) []string {
	ids, _ := e.Values[key].([]string)
	return slices.Clone(ids)
}
