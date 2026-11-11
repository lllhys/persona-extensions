// Package ops holds the transactions of the household extension: save, use, restock, purchase, chore done, meal close,
// remove and the sample set. Each works on a kernel base inside one Storage.Update; a failed call leaves the base as it
// was, so the caller returns its error from Update and nothing is saved.
package ops

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// typeNames are the Chinese names of the types, for the error messages.
var typeNames = map[string]string{
	model.TPlace:  "位置",
	model.TItem:   "物品",
	model.TBuy:    "采购",
	model.TChore:  "家务",
	model.TRecipe: "菜谱",
	model.TMeal:   "餐食",
}

// tx runs f on a copy of b, after making sure the household space and its types exist. The copy becomes the base only
// when f succeeds. f gets the copy, its household space and today's date in loc.
func tx(b *kernel.Base, now time.Time, loc *time.Location, f func(nb *kernel.Base, sp *kernel.Space, today model.Day) error) error {
	nb := *b
	nb.Spaces = slices.Clone(b.Spaces)
	sp, err := model.Ensure(&nb, now)
	if err != nil {
		return err
	}
	if err := f(&nb, sp, model.DayOf(now, loc)); err != nil {
		return err
	}
	*b = nb
	return nil
}

// find returns the record of the household space with the id, when it is of the type.
func find(nb *kernel.Base, id, typ string) (*kernel.Entity, error) {
	sp, e, err := nb.Find(id)
	if err != nil {
		return nil, err
	}
	if sp.ID != model.SpaceID {
		return nil, fmt.Errorf("没有记录「%s」", id)
	}
	if e.Type != typ {
		return nil, fmt.Errorf("「%s」不是%s", e.Title, typeNames[typ])
	}
	return e, nil
}

// loadItem reads the item with the id, which must exist.
func loadItem(nb *kernel.Base, sp *kernel.Space, id string, loc *time.Location) (model.Item, error) {
	if _, err := find(nb, id, model.TItem); err != nil {
		return model.Item{}, err
	}
	it, _ := model.Load(sp, loc).Item(id)
	return it, nil
}

func strPtr(s string) *string { return &s }

// Save creates or changes one record of the household space through the kernel's PutEntity. Three defaults are the
// household's: an item given a quantity without a count date is counted today; a new chore without a next date is due
// today; a new purchase without a reason is 手动. The values given are not changed.
func Save(b *kernel.Base, in kernel.EntityInput, now time.Time, loc *time.Location) (*kernel.Entity, error) {
	var out *kernel.Entity
	err := tx(b, now, loc, func(nb *kernel.Base, sp *kernel.Space, today model.Day) error {
		var err error
		out, err = save(sp, nb, in, now, today)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// save is Save inside a transaction.
func save(sp *kernel.Space, nb *kernel.Base, in kernel.EntityInput, now time.Time, today model.Day) (*kernel.Entity, error) {
	typ := in.Type
	if in.ID != "" {
		_, e, err := nb.Find(in.ID)
		if err != nil {
			return nil, err
		}
		typ = e.Type
	}
	vals := make(map[string]any, len(in.Values)+1)
	maps.Copy(vals, in.Values)
	switch typ {
	case model.TItem:
		if vals["qty"] != nil {
			if _, ok := vals["counted"]; !ok {
				vals["counted"] = string(today)
			}
		}
	case model.TChore:
		if in.ID == "" && vals["next"] == nil {
			vals["next"] = string(today)
		}
	case model.TBuy:
		if in.ID == "" && vals["reason"] == nil {
			vals["reason"] = model.ReasonManual
		}
	}
	in.Values = vals
	return sp.PutEntity(nb, in, now)
}

// Use takes n off the estimated quantity of an item and counts it today. The item must have a quantity.
func Use(b *kernel.Base, itemID string, n float64, now time.Time, loc *time.Location) (*kernel.Entity, error) {
	if !(n > 0) {
		return nil, errors.New("用掉的数量需要大于 0")
	}
	var out *kernel.Entity
	err := tx(b, now, loc, func(nb *kernel.Base, sp *kernel.Space, today model.Day) error {
		it, err := loadItem(nb, sp, itemID, loc)
		if err != nil {
			return err
		}
		if !it.HasQty {
			return fmt.Errorf("「%s」没有数量，不能用掉", it.Title)
		}
		left := max(0, rules.Estimate(it, today)-n)
		out, err = sp.PutEntity(nb, kernel.EntityInput{ID: itemID,
			Values: map[string]any{"qty": left, "counted": string(today)}}, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Restock creates one 待买 purchase for every finding that calls for one and has no open purchase yet. Each is derived
// from its item (the kernel's Derive: title, unit and category copied, origin recorded), with the item, the amount
// (the item's refill, or 1) and the reason of the finding. A second call creates nothing.
func Restock(b *kernel.Base, now time.Time, loc *time.Location) ([]*kernel.Entity, error) {
	var out []*kernel.Entity
	err := tx(b, now, loc, func(nb *kernel.Base, sp *kernel.Space, today model.Day) error {
		for _, f := range rules.Restock(model.Load(sp, loc), today) {
			d, err := sp.Derive(nb, f.Item.ID, model.TBuy, "", now)
			if err != nil {
				return err
			}
			amount := f.Item.Refill
			if amount <= 0 {
				amount = 1
			}
			// The note of the item is where it is kept, not a note about the purchase, so it is not carried over.
			e, err := sp.PutEntity(nb, kernel.EntityInput{ID: d.ID, Values: map[string]any{
				"item":   []string{f.Item.ID},
				"amount": amount,
				"reason": f.Kind.Reason(),
				"note":   nil,
			}}, now)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Purchase closes a 待买 purchase. Bought, it becomes 已买 and, when the purchase has an item, the item is stocked (see
// stock). Not bought, it becomes 不买 and nothing else changes.
//
// Stocking: an item with a quantity gets the amount added to its estimate and is counted today. When its expiry date is
// already past, the old batch is taken as thrown away instead: the quantity is the amount alone and the date is cleared.
// The amount is the purchase's, or the item's refill when that is 0, or 1 when the refill is unset. An item without a
// quantity stays untracked: only a past expiry date is cleared.
func Purchase(b *kernel.Base, buyID string, bought bool, now time.Time, loc *time.Location) (*kernel.Entity, error) {
	var out *kernel.Entity
	err := tx(b, now, loc, func(nb *kernel.Base, sp *kernel.Space, today model.Day) error {
		e, err := find(nb, buyID, model.TBuy)
		if err != nil {
			return err
		}
		if e.Status != model.BuyTodo {
			return fmt.Errorf("「%s」已经是「%s」，只能关闭待买的采购", e.Title, e.Status)
		}
		status := model.BuySkip
		if bought {
			if err := stock(nb, sp, buyID, now, loc, today); err != nil {
				return err
			}
			status = model.BuyDone
		}
		out, err = sp.PutEntity(nb, kernel.EntityInput{ID: buyID, Status: strPtr(status)}, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// stock adds the amount of a bought purchase to its item, when it has one.
func stock(nb *kernel.Base, sp *kernel.Space, buyID string, now time.Time, loc *time.Location, today model.Day) error {
	h := model.Load(sp, loc)
	i := slices.IndexFunc(h.Buys, func(b model.Buy) bool { return b.ID == buyID })
	if i < 0 {
		return fmt.Errorf("没有记录「%s」", buyID)
	}
	buy := h.Buys[i]
	if buy.ItemID == "" {
		return nil
	}
	it, ok := h.Item(buy.ItemID)
	if !ok {
		return fmt.Errorf("没有记录「%s」", buy.ItemID)
	}
	expired := it.Expires != "" && it.Expires < today
	vals := map[string]any{}
	if expired {
		vals["expires"] = nil
	}
	if it.HasQty {
		amount := buy.Amount
		if amount == 0 {
			amount = it.Refill
		}
		if amount == 0 {
			amount = 1
		}
		if expired {
			vals["qty"] = amount
		} else {
			vals["qty"] = rules.Estimate(it, today) + amount
		}
		vals["counted"] = string(today)
	}
	if len(vals) == 0 {
		return nil
	}
	_, err := sp.PutEntity(nb, kernel.EntityInput{ID: it.ID, Values: vals}, now)
	return err
}

// ChoreDone completes a chore: last is today, and next is today plus the period rounded up to whole days (at least
// one day), or cleared when the chore has no period (a one-off).
func ChoreDone(b *kernel.Base, choreID string, now time.Time, loc *time.Location) (*kernel.Entity, error) {
	var out *kernel.Entity
	err := tx(b, now, loc, func(nb *kernel.Base, sp *kernel.Space, today model.Day) error {
		if _, err := find(nb, choreID, model.TChore); err != nil {
			return err
		}
		h := model.Load(sp, loc)
		i := slices.IndexFunc(h.Chores, func(c model.Chore) bool { return c.ID == choreID })
		if i < 0 {
			return fmt.Errorf("没有记录「%s」", choreID)
		}
		vals := map[string]any{"last": string(today), "next": nil}
		if every := h.Chores[i].Every; every > 0 {
			vals["next"] = string(today.Add(int(math.Ceil(every))))
		}
		var err error
		out, err = sp.PutEntity(nb, kernel.EntityInput{ID: choreID, Values: vals}, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MealClose closes a planned meal: done becomes 已做, otherwise 取消. Only a 计划 meal can be closed.
func MealClose(b *kernel.Base, mealID string, done bool, now time.Time, loc *time.Location) (*kernel.Entity, error) {
	var out *kernel.Entity
	err := tx(b, now, loc, func(nb *kernel.Base, sp *kernel.Space, _ model.Day) error {
		e, err := find(nb, mealID, model.TMeal)
		if err != nil {
			return err
		}
		if e.Status != model.MealPlan {
			return fmt.Errorf("「%s」已经是「%s」，只能关闭计划中的餐食", e.Title, e.Status)
		}
		status := model.MealCancel
		if done {
			status = model.MealDone
		}
		out, err = sp.PutEntity(nb, kernel.EntityInput{ID: mealID, Status: strPtr(status)}, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Remove deletes a record of the household space. Relations that pointed to it lose the id.
func Remove(b *kernel.Base, id string, now time.Time) error {
	return tx(b, now, nil, func(_ *kernel.Base, sp *kernel.Space, _ model.Day) error {
		_, err := sp.Drop(id)
		return err
	})
}
