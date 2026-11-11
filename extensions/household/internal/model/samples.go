package model

// Sample is one made-up record of the sample set. In Values, a string "@ref" (or a []string of them) is the entity
// created for that Ref, which must come earlier in Samples, and "+3d" or "-2d" is a date relative to today.
type Sample struct {
	Ref, Type, Title, Status string
	Values                   map[string]any
}

// Samples is the sample set behind 载入示例: three places, ten items covering every finding (one of them a baby
// item that runs out on its daily use), three chores (one overdue, one due today, one later), three recipes (one with
// everything at hand, one using an expiring item, one missing an ingredient), two meals and one manual purchase.
func Samples() []Sample {
	return []Sample{
		{Ref: "place_fridge", Type: TPlace, Title: "厨房冰箱",
			Values: map[string]any{"note": "冷藏和冷冻食材。"}},
		{Ref: "place_pantry", Type: TPlace, Title: "厨房柜",
			Values: map[string]any{"note": "调味料和干货。"}},
		{Ref: "place_cabinet", Type: TPlace, Title: "杂物柜",
			Values: map[string]any{"note": "日用品、药品和母婴用品。"}},

		{Ref: "item_milk", Type: TItem, Title: "纯牛奶",
			Values: map[string]any{"category": "食材", "place": "@place_fridge", "qty": 2, "unit": "盒", "expires": "-2d"}},
		{Ref: "item_egg", Type: TItem, Title: "鸡蛋",
			Values: map[string]any{"category": "食材", "place": "@place_fridge", "qty": 0, "unit": "个"}},
		{Ref: "item_soy", Type: TItem, Title: "酱油",
			Values: map[string]any{"category": "调味", "place": "@place_pantry", "qty": 1, "min": 1, "unit": "瓶"}},
		{Ref: "item_diaper", Type: TItem, Title: "纸尿裤",
			Values: map[string]any{"category": "母婴", "place": "@place_cabinet", "qty": 40, "daily": 5,
				"counted": "-3d", "refill": 40, "unit": "片"}},
		{Ref: "item_yogurt", Type: TItem, Title: "酸奶",
			Values: map[string]any{"category": "食材", "place": "@place_fridge", "qty": 3, "unit": "杯", "expires": "+2d"}},
		{Ref: "item_lettuce", Type: TItem, Title: "生菜",
			Values: map[string]any{"category": "食材", "place": "@place_fridge", "qty": 1, "unit": "棵", "expires": "+1d"}},
		{Ref: "item_rice", Type: TItem, Title: "大米",
			Values: map[string]any{"category": "食材", "place": "@place_pantry", "qty": 5, "min": 2, "unit": "千克", "expires": "+200d"}},
		{Ref: "item_tomato", Type: TItem, Title: "番茄",
			Values: map[string]any{"category": "食材", "place": "@place_fridge", "qty": 4, "unit": "个"}},
		{Ref: "item_oil", Type: TItem, Title: "食用油",
			Values: map[string]any{"category": "调味", "place": "@place_pantry", "qty": 1, "unit": "瓶"}},
		{Ref: "item_bandage", Type: TItem, Title: "创可贴",
			Values: map[string]any{"category": "药品", "place": "@place_cabinet", "expires": "+400d"}},

		{Ref: "chore_mop", Type: TChore, Title: "拖地",
			Values: map[string]any{"every": 7, "last": "-9d", "next": "-2d", "who": "家人甲", "place": "@place_pantry", "minutes": 20}},
		{Ref: "chore_fridge", Type: TChore, Title: "整理冰箱",
			Values: map[string]any{"every": 14, "last": "-14d", "next": "+0d", "who": "家人乙", "place": "@place_fridge", "minutes": 30}},
		{Ref: "chore_dust", Type: TChore, Title: "除尘",
			Values: map[string]any{"every": 30, "last": "-5d", "next": "+25d", "who": "家人甲", "place": "@place_cabinet", "minutes": 40}},

		{Ref: "recipe_porridge", Type: TRecipe, Title: "白粥",
			Values: map[string]any{"needs": []string{"@item_rice"}, "minutes": 40, "note": "大米加水慢煮。"}},
		{Ref: "recipe_lettuce", Type: TRecipe, Title: "蒜蓉生菜",
			Values: map[string]any{"needs": []string{"@item_lettuce", "@item_soy"}, "minutes": 10,
				"url": "https://example.com/recipes/garlic-lettuce", "note": "生菜要尽快吃完。"}},
		{Ref: "recipe_tomato_egg", Type: TRecipe, Title: "番茄炒蛋",
			Values: map[string]any{"needs": []string{"@item_tomato", "@item_egg"}, "minutes": 15}},

		{Ref: "meal_tonight", Type: TMeal, Title: "番茄炒蛋", Status: MealPlan,
			Values: map[string]any{"date": "+0d", "slot": "晚餐", "recipe": "@recipe_tomato_egg"}},
		{Ref: "meal_breakfast", Type: TMeal, Title: "白粥", Status: MealDone,
			Values: map[string]any{"date": "-1d", "slot": "早餐", "recipe": "@recipe_porridge"}},

		{Ref: "buy_detergent", Type: TBuy, Title: "洗衣液", Status: BuyTodo,
			Values: map[string]any{"category": "日用", "reason": ReasonManual, "amount": 1, "unit": "瓶"}},
	}
}
