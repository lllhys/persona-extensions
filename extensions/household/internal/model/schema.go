// Package model holds the household schema, the sample records and the typed view of a space. The schema is the
// one of docs/extensions/household.md: Ensure writes it into a kernel base, Load reads a space back as typed records.
package model

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// SpaceID, SpaceName and SpaceIcon identify the household space in its base.
const (
	SpaceID   = "household"
	SpaceName = "家庭管家"
	SpaceIcon = "home"
)

// Type keys of the household space.
const (
	TPlace  = "place"
	TItem   = "item"
	TBuy    = "buy"
	TChore  = "chore"
	TRecipe = "recipe"
	TMeal   = "meal"
)

// Stage names, stored as the status of purchases and meals.
const (
	BuyTodo = "待买"
	BuyDone = "已买"
	BuySkip = "不买"

	MealPlan   = "计划"
	MealDone   = "已做"
	MealCancel = "取消"
)

// Reasons a purchase was made for.
const (
	ReasonLow     = "低库存"
	ReasonOut     = "已用完"
	ReasonExpired = "已过期"
	ReasonRunout  = "用量预警"
	ReasonManual  = "手动"
)

// CategoryBaby is the category of baby supplies, which get a longer warning lead by default.
const CategoryBaby = "母婴"

// Categories are the options of the item category; Slots are the meal times.
var (
	Categories = []string{"食材", "调味", "日用", "清洁", "药品", "母婴", "其他"}
	Slots      = []string{"早餐", "午餐", "晚餐", "加餐"}
)

// Types is the schema of the household space: the six types with their properties, stages, cards and views. Each call
// returns fresh values, so a caller may change the result.
func Types() []kernel.Type {
	categories := func() []string { return slices.Clone(Categories) }
	return []kernel.Type{
		{
			Key: TPlace, Name: "位置",
			Props: []kernel.Prop{
				{Key: "note", Name: "备注", Kind: kernel.KindText},
				{Key: "items", Name: "物品数", Kind: kernel.KindRollup, Via: TItem + ".place", Op: "count"},
			},
			Card:  kernel.Card{Lead: "note", Pills: []string{"items"}},
			Views: []kernel.View{{Name: "列表", Kind: "list"}},
		},
		{
			Key: TItem, Name: "物品",
			Props: []kernel.Prop{
				{Key: "category", Name: "分类", Kind: kernel.KindEnum, Options: categories()},
				{Key: "place", Name: "位置", Kind: kernel.KindRelation, Target: TPlace, Back: "存放物品"},
				{Key: "qty", Name: "数量", Kind: kernel.KindNumber},
				{Key: "unit", Name: "单位", Kind: kernel.KindText},
				{Key: "min", Name: "低库存线", Kind: kernel.KindNumber},
				{Key: "expires", Name: "保质期", Kind: kernel.KindDate},
				{Key: "daily", Name: "日均用量", Kind: kernel.KindNumber},
				{Key: "counted", Name: "盘点日", Kind: kernel.KindDate},
				{Key: "lead", Name: "提前提醒", Kind: kernel.KindNumber, Unit: "天"},
				{Key: "refill", Name: "补货量", Kind: kernel.KindNumber},
				{Key: "note", Name: "备注", Kind: kernel.KindText},
			},
			Card: kernel.Card{Lead: "note", Pills: []string{"category", "place", "qty", "expires"}},
			Views: []kernel.View{
				{Name: "列表", Kind: "list"},
				{Name: "按位置", Kind: "list", Lens: kernel.Lens{Group: "place"}},
				{Name: "表格", Kind: "table", Lens: kernel.Lens{
					Sort: []kernel.Order{{By: "expires"}},
					Agg:  []kernel.Agg{{Op: "count"}},
				}},
			},
		},
		{
			Key: TBuy, Name: "采购",
			Flow: []kernel.Stage{
				{Name: BuyTodo, Phase: kernel.Todo},
				{Name: BuyDone, Phase: kernel.Done},
				{Name: BuySkip, Phase: kernel.Dropped},
			},
			Props: []kernel.Prop{
				{Key: "item", Name: "物品", Kind: kernel.KindRelation, Target: TItem, Back: "采购记录"},
				{Key: "amount", Name: "采购量", Kind: kernel.KindNumber},
				{Key: "unit", Name: "单位", Kind: kernel.KindText},
				{Key: "category", Name: "分类", Kind: kernel.KindEnum, Options: categories()},
				{Key: "reason", Name: "原因", Kind: kernel.KindEnum,
					Options: []string{ReasonLow, ReasonOut, ReasonExpired, ReasonRunout, ReasonManual}},
				{Key: "note", Name: "备注", Kind: kernel.KindText},
			},
			Card:  kernel.Card{Lead: "note", Pills: []string{"category", "reason", "amount"}},
			Views: []kernel.View{{Name: "看板", Kind: "board"}, {Name: "表格", Kind: "table"}},
		},
		{
			Key: TChore, Name: "家务",
			Props: []kernel.Prop{
				{Key: "every", Name: "周期", Kind: kernel.KindNumber, Unit: "天"},
				{Key: "last", Name: "上次完成", Kind: kernel.KindDate},
				{Key: "next", Name: "下次到期", Kind: kernel.KindDate},
				{Key: "who", Name: "负责人", Kind: kernel.KindText},
				{Key: "place", Name: "位置", Kind: kernel.KindRelation, Target: TPlace, Back: "家务"},
				{Key: "minutes", Name: "用时", Kind: kernel.KindNumber, Unit: "分钟"},
				{Key: "note", Name: "备注", Kind: kernel.KindText},
			},
			Card: kernel.Card{Lead: "note", Pills: []string{"every", "next", "who"}},
			Views: []kernel.View{
				{Name: "列表", Kind: "list", Lens: kernel.Lens{Sort: []kernel.Order{{By: "next"}}}},
				{Name: "表格", Kind: "table"},
			},
		},
		{
			Key: TRecipe, Name: "菜谱",
			Props: []kernel.Prop{
				{Key: "needs", Name: "需要", Kind: kernel.KindRelation, Target: TItem, Many: true, Back: "可做的菜"},
				{Key: "minutes", Name: "用时", Kind: kernel.KindNumber, Unit: "分钟"},
				{Key: "url", Name: "链接", Kind: kernel.KindURL},
				{Key: "note", Name: "备注", Kind: kernel.KindText},
			},
			Card:  kernel.Card{Lead: "note", Pills: []string{"minutes"}},
			Views: []kernel.View{{Name: "列表", Kind: "list"}},
		},
		{
			Key: TMeal, Name: "餐食",
			Flow: []kernel.Stage{
				{Name: MealPlan, Phase: kernel.Todo},
				{Name: MealDone, Phase: kernel.Done},
				{Name: MealCancel, Phase: kernel.Dropped},
			},
			Props: []kernel.Prop{
				{Key: "date", Name: "日期", Kind: kernel.KindDate},
				{Key: "slot", Name: "时段", Kind: kernel.KindEnum, Options: slices.Clone(Slots)},
				{Key: "recipe", Name: "菜谱", Kind: kernel.KindRelation, Target: TRecipe, Back: "排期"},
				{Key: "note", Name: "备注", Kind: kernel.KindText},
			},
			Card: kernel.Card{Lead: "note", Pills: []string{"date", "slot", "recipe"}},
			Views: []kernel.View{
				{Name: "列表", Kind: "list", Lens: kernel.Lens{Sort: []kernel.Order{{By: "date"}}}},
				{Name: "看板", Kind: "board"},
			},
		},
	}
}

// Ensure makes sure the household space exists in b and that its types match Types. Types the space lacks are put
// first without relations, rollups, cards and views, so that types may refer to each other; then every type that
// differs from Types is put in full. A type that differs is replaced as a whole, which migrates its entities: values
// of properties the new type lacks are dropped. A type the space already had is replaced only when its entities fit
// the new type; when one does not (a hand-edited value, say, that the design does not accept), the stored type is kept
// as it is, its records unchanged, and Ensure goes on with the other types. A type this call created cannot fail that
// way: a failure for it is an error. A type that matches is not written, so a second call over a matching space leaves
// the base as it is. The base changes only when every step succeeded.
func Ensure(b *kernel.Base, now time.Time) (*kernel.Space, error) {
	work := *b
	work.Spaces = slices.Clone(b.Spaces)
	var sp *kernel.Space
	if i := slices.IndexFunc(work.Spaces, func(s *kernel.Space) bool { return s != nil && s.ID == SpaceID }); i >= 0 {
		// A copy of the space, so that a failing step leaves the space of b as it was.
		cp := *work.Spaces[i]
		work.Spaces[i] = &cp
		sp = &cp
	} else {
		name, icon := SpaceName, SpaceIcon
		created, err := work.PutSpace(kernel.SpaceInput{ID: SpaceID, Name: &name, Icon: &icon})
		if err != nil {
			return nil, err
		}
		sp = created
	}
	if err := putTypes(sp, Types(), now); err != nil {
		return nil, err
	}
	*b = work
	return sp, nil
}

// putTypes writes the types in two passes. The types the space lacks come first, as skeletons without relations,
// rollups, cards and views. Then every type that differs from its design is put in full; a rollup reads the relation
// of another type, so the types with rollups come last.
// A type the space had before the call that cannot be replaced, because an entity of it does not fit the design, is kept
// as it is; PutType leaves the space unchanged when it fails, so nothing else needs undoing.
func putTypes(sp *kernel.Space, types []kernel.Type, now time.Time) error {
	had := map[string]bool{}
	for _, ty := range sp.Types {
		had[ty.Key] = true
	}
	have := maps.Clone(had)
	for _, ty := range types {
		if have[ty.Key] {
			continue
		}
		if err := sp.PutType(skeleton(ty), now); err != nil {
			return err
		}
		have[ty.Key] = true
	}
	for _, rollups := range []bool{false, true} {
		for _, ty := range types {
			if hasRollup(ty) != rollups || sameType(sp, ty) {
				continue
			}
			if err := sp.PutType(ty, now); err != nil {
				if had[ty.Key] {
					continue
				}
				return err
			}
		}
	}
	return nil
}

// skeleton is the first-pass form of a type: its key, name and stages, with the plain properties only.
func skeleton(ty kernel.Type) kernel.Type {
	out := ty
	out.Card = kernel.Card{}
	out.Views = nil
	out.Props = nil
	for _, p := range ty.Props {
		if p.Kind == kernel.KindRelation || p.Kind == kernel.KindRollup {
			continue
		}
		out.Props = append(out.Props, p)
	}
	return out
}

// sameType reports whether the space stores a type with the key of ty and the same JSON encoding.
func sameType(sp *kernel.Space, ty kernel.Type) bool {
	i := slices.IndexFunc(sp.Types, func(t *kernel.Type) bool { return t != nil && t.Key == ty.Key })
	if i < 0 {
		return false
	}
	stored, err := json.Marshal(sp.Types[i])
	if err != nil {
		return false
	}
	want, err := json.Marshal(ty)
	if err != nil {
		return false
	}
	return bytes.Equal(stored, want)
}

func hasRollup(ty kernel.Type) bool {
	for _, p := range ty.Props {
		if p.Kind == kernel.KindRollup {
			return true
		}
	}
	return false
}
