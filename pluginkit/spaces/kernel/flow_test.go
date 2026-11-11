package kernel

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// flowA is a flow with a dropped stage at the end: 收集 → 探索 → 已孵化, and 搁置 may be reached from any stage.
var flowA = &Type{Key: "idea", Name: "点子", Flow: []Stage{
	{Name: "收集", Phase: Todo},
	{Name: "探索", Phase: Doing},
	{Name: "已孵化", Phase: Done},
	{Name: "搁置", Phase: Dropped},
}}

// flowB has a dropped stage in the middle of the flow.
var flowB = &Type{Key: "item", Name: "物品", Flow: []Stage{
	{Name: "缺货", Phase: Todo},
	{Name: "停用", Phase: Dropped},
	{Name: "待补", Phase: Doing},
	{Name: "充足", Phase: Done},
}}

// flowC has no dropped stage.
var flowC = &Type{Key: "task", Name: "任务", Flow: []Stage{
	{Name: "待办", Phase: Todo},
	{Name: "进行", Phase: Doing},
	{Name: "完成", Phase: Done},
}}

func TestMoves(t *testing.T) {
	tests := []struct {
		name string
		t    *Type
		from string
		want []string
	}{
		{"first stage", flowA, "收集", []string{"探索", "搁置"}},
		{"middle stage", flowA, "探索", []string{"收集", "已孵化", "搁置"}},
		{"last main stage", flowA, "已孵化", []string{"探索", "搁置"}},
		{"dropped stage", flowA, "搁置", []string{"收集", "探索", "已孵化"}},
		{"unknown stage", flowA, "没有", nil},
		{"no flow", &Type{Key: "n", Name: "n"}, "收集", nil},
		{"dropped in the middle, first main", flowB, "缺货", []string{"停用", "待补"}},
		{"dropped in the middle, between", flowB, "待补", []string{"缺货", "停用", "充足"}},
		{"dropped in the middle, last", flowB, "充足", []string{"停用", "待补"}},
		{"dropped in the middle, from dropped", flowB, "停用", []string{"缺货", "待补", "充足"}},
		{"no dropped stage", flowC, "进行", []string{"待办", "完成"}},
		{"no dropped stage, first", flowC, "待办", []string{"进行"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.t.Moves(tt.from); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Moves(%q) = %v, want %v", tt.from, got, tt.want)
			}
		})
	}
}

// TestMovesSymmetry: a move from a to b is allowed exactly when the move back from b to a is, except between two
// dropped stages, which can never move.
func TestMovesSymmetry(t *testing.T) {
	for _, ty := range []*Type{flowA, flowB, flowC} {
		for _, a := range ty.Flow {
			for _, b := range ty.Flow {
				if a.Name == b.Name {
					continue
				}
				ab := slices.Contains(ty.Moves(a.Name), b.Name)
				ba := slices.Contains(ty.Moves(b.Name), a.Name)
				if ab != ba && !(a.Phase == Dropped && b.Phase == Dropped) {
					t.Errorf("%s: %s→%s is %v but %s→%s is %v", ty.Key, a.Name, b.Name, ab, b.Name, a.Name, ba)
				}
				if a.Phase == Dropped && b.Phase == Dropped && (ab || ba) {
					t.Errorf("%s: two dropped stages move to each other", ty.Key)
				}
			}
		}
	}
}

func TestNextChainsToTheEnd(t *testing.T) {
	cur := "收集"
	var chain []string
	for {
		next, ok := flowA.Next(cur)
		if !ok {
			break
		}
		chain = append(chain, next)
		cur = next
	}
	if want := []string{"探索", "已孵化"}; !reflect.DeepEqual(chain, want) {
		t.Errorf("Next chain = %v, want %v", chain, want)
	}
	if cur != "已孵化" {
		t.Errorf("the chain ends at %q, want the last main-line stage", cur)
	}
	tests := []struct {
		t    *Type
		from string
		want string
		ok   bool
	}{
		{flowA, "搁置", "", false},
		{flowA, "没有", "", false},
		{flowA, "已孵化", "", false},
		{flowB, "缺货", "待补", true},
		{flowB, "停用", "", false},
		{flowB, "待补", "充足", true},
		{flowB, "充足", "", false},
	}
	for _, tt := range tests {
		got, ok := tt.t.Next(tt.from)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Next(%q) = %q, %v; want %q, %v", tt.from, got, ok, tt.want, tt.ok)
		}
	}
}

func TestPosition(t *testing.T) {
	tests := []struct {
		t    *Type
		from string
		want float64
		ok   bool
	}{
		{flowA, "收集", 0, true},
		{flowA, "探索", 0.5, true},
		{flowA, "已孵化", 1, true},
		{flowA, "搁置", 0, false},
		{flowA, "没有", 0, false},
		{flowB, "待补", 0.5, true},
		{flowB, "充足", 1, true},
		{flowB, "缺货", 0, true},
		{&Type{Key: "n", Name: "n"}, "收集", 0, false},
		{&Type{Key: "one", Name: "one", Flow: []Stage{{Name: "a", Phase: Todo}, {Name: "b", Phase: Dropped}}}, "a", 0, false},
	}
	for _, tt := range tests {
		got, ok := tt.t.Position(tt.from)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Position(%q) = %v, %v; want %v, %v", tt.from, got, ok, tt.want, tt.ok)
		}
	}
}

func TestStageLookup(t *testing.T) {
	if s, ok := flowA.Stage("探索"); !ok || s.Phase != Doing {
		t.Errorf("Stage(探索) = %+v, %v", s, ok)
	}
	if _, ok := flowA.Stage("没有"); ok {
		t.Errorf("Stage found an unknown name")
	}
}

// TestCheckFlow refuses each invalid flow for one rule, and accepts the valid ones.
func TestCheckFlow(t *testing.T) {
	stages := func(names ...string) []Stage {
		var out []Stage
		for _, n := range names {
			out = append(out, Stage{Name: n, Phase: Todo})
		}
		return out
	}
	tests := []struct {
		name  string
		flow  []Stage
		valid bool
	}{
		{"no flow", nil, true},
		{"two stages", []Stage{{Name: "a", Phase: Todo}, {Name: "b", Phase: Done}}, true},
		{"dropped in the middle", []Stage{{Name: "a", Phase: Todo}, {Name: "x", Phase: Dropped}, {Name: "b", Phase: Done}}, true},
		{"same phase twice", []Stage{{Name: "a", Phase: Doing}, {Name: "b", Phase: Doing}}, true},
		{"one stage", []Stage{{Name: "a", Phase: Todo}}, false},
		{"nine stages", stages("a", "b", "c", "d", "e", "f", "g", "h", "i"), false},
		{"name too long", []Stage{{Name: strings.Repeat("字", MaxShort+1), Phase: Todo}, {Name: "b", Phase: Done}}, false},
		{"empty name", []Stage{{Name: "", Phase: Todo}, {Name: "b", Phase: Done}}, false},
		{"duplicate name", []Stage{{Name: "a", Phase: Todo}, {Name: "a", Phase: Done}}, false},
		{"unknown phase", []Stage{{Name: "a", Phase: "later"}, {Name: "b", Phase: Done}}, false},
		{"first stage dropped", []Stage{{Name: "a", Phase: Dropped}, {Name: "b", Phase: Todo}, {Name: "c", Phase: Done}}, false},
		{"main line of one stage", []Stage{{Name: "a", Phase: Todo}, {Name: "b", Phase: Dropped}}, false},
		{"phases go backwards", []Stage{{Name: "a", Phase: Done}, {Name: "b", Phase: Doing}}, false},
		{"phases go backwards across a dropped stage", []Stage{{Name: "a", Phase: Doing}, {Name: "x", Phase: Dropped}, {Name: "b", Phase: Todo}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ty := &Type{Key: "k", Name: "类型", Flow: tt.flow}
			err := ty.checkFlow()
			if (err == nil) != tt.valid {
				t.Fatalf("checkFlow = %v, want valid = %v", err, tt.valid)
			}
		})
	}
}

func TestPhaseRank(t *testing.T) {
	if phaseRank(Todo) >= phaseRank(Doing) || phaseRank(Doing) >= phaseRank(Done) {
		t.Errorf("phases are not ordered todo < doing < done")
	}
	if phaseRank(Dropped) != -1 || phaseRank("other") != -1 {
		t.Errorf("dropped and unknown phases have no rank on the main line")
	}
}
