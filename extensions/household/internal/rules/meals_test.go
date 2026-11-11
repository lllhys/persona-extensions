package rules

import (
	"reflect"
	"slices"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
)

// mealHome is a home with a fixed set of items and recipes for the meal ideas:
//
//	milk    纯牛奶  qty 1, expires tomorrow (expiring, at hand)
//	egg     鸡蛋    qty 10 (at hand)
//	soy     酱油    no quantity, untracked (at hand)
//	tomato  番茄    qty 3, expires in ten days (at hand)
//	rice    大米    qty 0 (out, not at hand)
//	old     老豆腐  qty 1, expired yesterday (not at hand)
func mealHome() *model.Home {
	return &model.Home{
		Items: []model.Item{
			{ID: "milk", Title: "纯牛奶", Qty: 1, HasQty: true, Expires: today.Add(1)},
			{ID: "egg", Title: "鸡蛋", Qty: 10, HasQty: true},
			{ID: "soy", Title: "酱油"},
			{ID: "tomato", Title: "番茄", Qty: 3, HasQty: true, Expires: today.Add(10)},
			{ID: "rice", Title: "大米", Qty: 0, HasQty: true},
			{ID: "old", Title: "老豆腐", Qty: 1, HasQty: true, Expires: today.Add(-1)},
		},
		Recipes: []model.Recipe{
			{ID: "r_eggtom", Title: "番茄炒蛋", Needs: []string{"tomato", "egg"}},
			{ID: "r_milkegg", Title: "煎蛋", Needs: []string{"egg", "milk"}},
			{ID: "r_porridge", Title: "牛奶粥", Needs: []string{"milk", "rice"}},
			{ID: "r_soy", Title: "酱油饭", Needs: []string{"soy", "ghost"}},
			{ID: "r_none", Title: "空菜谱"},
			{ID: "r_rice", Title: "白饭", Needs: []string{"rice"}},
			{ID: "r_expold", Title: "豆腐汤", Needs: []string{"old", "tomato"}},
			{ID: "r_share", Title: "三样菜", Needs: []string{"egg", "tomato", "ghost"}},
		},
	}
}

func ideaTitles(ideas []Idea) []string {
	var titles []string
	for _, i := range ideas {
		titles = append(titles, i.Recipe.Title)
	}
	return titles
}

func TestMealIdeasRanking(t *testing.T) {
	got := MealIdeas(mealHome(), today, 0)
	want := []string{
		"煎蛋",   // all at hand, one expiring ingredient
		"番茄炒蛋", // all at hand
		"牛奶粥",  // 1 of 2, one expiring
		"三样菜",  // no expiring, 2 of 3 at hand
		"豆腐汤",  // no expiring, 1 of 2 at hand, title before 酱油饭
		"酱油饭",  // no expiring, 1 of 2 at hand
		"白饭",   // nothing at hand
	}
	if titles := ideaTitles(got); !reflect.DeepEqual(titles, want) {
		t.Errorf("MealIdeas order = %v, want %v", titles, want)
	}
}

func TestMealIdeasDetails(t *testing.T) {
	ideas := MealIdeas(mealHome(), today, 0)
	byID := map[string]Idea{}
	for _, i := range ideas {
		byID[i.Recipe.ID] = i
	}
	tests := []struct {
		recipe   string
		have     int
		total    int
		missing  []string
		useFirst []string
	}{
		{"r_eggtom", 2, 2, nil, nil},
		{"r_milkegg", 2, 2, nil, []string{"纯牛奶"}},
		{"r_porridge", 1, 2, []string{"大米"}, []string{"纯牛奶"}},
		{"r_soy", 1, 2, []string{"?"}, nil},
		{"r_rice", 0, 1, []string{"大米"}, nil},
		{"r_expold", 1, 2, []string{"老豆腐"}, nil},
		{"r_share", 2, 3, []string{"?"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.recipe, func(t *testing.T) {
			i, ok := byID[tt.recipe]
			if !ok {
				t.Fatalf("recipe %s not in ideas", tt.recipe)
			}
			if i.Have != tt.have || i.Total != tt.total {
				t.Errorf("Have/Total = %d/%d, want %d/%d", i.Have, i.Total, tt.have, tt.total)
			}
			if !slices.Equal(i.Missing, tt.missing) {
				t.Errorf("Missing = %v, want %v", i.Missing, tt.missing)
			}
			if !slices.Equal(i.UseFirst, tt.useFirst) {
				t.Errorf("UseFirst = %v, want %v", i.UseFirst, tt.useFirst)
			}
		})
	}
}

func TestMealIdeasExcludesRecipesWithoutNeeds(t *testing.T) {
	for _, i := range MealIdeas(mealHome(), today, 0) {
		if i.Recipe.ID == "r_none" {
			t.Errorf("recipe without needs is suggested: %+v", i)
		}
	}
}

func TestMealIdeasLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"limit two", 2, 2},
		{"limit one", 1, 1},
		{"zero means all", 0, 7},
		{"negative means all", -1, 7},
		{"limit above count", 100, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MealIdeas(mealHome(), today, tt.limit); len(got) != tt.want {
				t.Errorf("len(MealIdeas(limit %d)) = %d, want %d", tt.limit, len(got), tt.want)
			}
		})
	}
	top := ideaTitles(MealIdeas(mealHome(), today, 2))
	if want := []string{"煎蛋", "番茄炒蛋"}; !reflect.DeepEqual(top, want) {
		t.Errorf("limit 2 = %v, want %v", top, want)
	}
}

func TestMealIdeasExpiringMeansExpiringNotFinding(t *testing.T) {
	// The milk is low on stock, so its finding is 低库存, but it still expires tomorrow and must be used first.
	h := mealHome()
	h.Items[0].Min = 5
	h.Items[0].HasMin = true
	ideas := MealIdeas(h, today, 0)
	for _, i := range ideas {
		if i.Recipe.ID == "r_milkegg" {
			if !slices.Equal(i.UseFirst, []string{"纯牛奶"}) {
				t.Errorf("UseFirst = %v, want [纯牛奶]", i.UseFirst)
			}
			return
		}
	}
	t.Fatal("r_milkegg not suggested")
}

func TestMealIdeasExpiresTodayIsAtHandAndExpiring(t *testing.T) {
	h := &model.Home{
		Items:   []model.Item{{ID: "yogurt", Title: "酸奶", Qty: 2, HasQty: true, Expires: today}},
		Recipes: []model.Recipe{{ID: "r", Title: "酸奶碗", Needs: []string{"yogurt"}}},
	}
	ideas := MealIdeas(h, today, 0)
	if len(ideas) != 1 {
		t.Fatalf("MealIdeas() = %+v, want one idea", ideas)
	}
	i := ideas[0]
	if i.Have != 1 || i.Total != 1 || len(i.Missing) != 0 || !slices.Equal(i.UseFirst, []string{"酸奶"}) {
		t.Errorf("idea = %+v, want have 1 of 1, nothing missing, 酸奶 used first", i)
	}
}

func TestMealIdeasExpiredIsNotAtHand(t *testing.T) {
	h := &model.Home{
		Items:   []model.Item{{ID: "old", Title: "老豆腐", Expires: today.Add(-1)}},
		Recipes: []model.Recipe{{ID: "r", Title: "豆腐汤", Needs: []string{"old"}}},
	}
	i := MealIdeas(h, today, 0)[0]
	if i.Have != 0 || !slices.Equal(i.Missing, []string{"老豆腐"}) || len(i.UseFirst) != 0 {
		t.Errorf("idea = %+v, want the expired item missing and not used first", i)
	}
}

func TestMealIdeasEmpty(t *testing.T) {
	if got := MealIdeas(&model.Home{}, today, 0); len(got) != 0 {
		t.Errorf("MealIdeas(empty) = %+v, want none", got)
	}
}
