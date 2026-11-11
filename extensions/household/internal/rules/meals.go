package rules

import (
	"cmp"
	"slices"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
)

// Idea is a recipe suggestion. Have of Total ingredients are at hand. Missing lists the titles of the others ("?" for
// an ingredient whose item no longer exists); UseFirst lists the titles of the ingredients at hand that expire soon.
type Idea struct {
	Recipe   model.Recipe
	Have     int
	Total    int
	Missing  []string
	UseFirst []string
}

// MealIdeas ranks the recipes that have at least one ingredient. An ingredient is at hand when its item has not
// expired and is either untracked by quantity or has an estimate above zero. Recipes with every ingredient at hand
// come first, then those with more ingredients that expire soon, then the larger share at hand; ties go by title and
// id. A limit of zero or less returns every idea.
func MealIdeas(h *model.Home, today model.Day, limit int) []Idea {
	items := make(map[string]model.Item, len(h.Items))
	for _, it := range h.Items {
		items[it.ID] = it
	}
	var ideas []Idea
	for _, r := range h.Recipes {
		if len(r.Needs) == 0 {
			continue
		}
		idea := Idea{Recipe: r, Total: len(r.Needs)}
		for _, id := range r.Needs {
			it, ok := items[id]
			switch {
			case !ok:
				idea.Missing = append(idea.Missing, "?")
			case !atHand(it, today):
				idea.Missing = append(idea.Missing, it.Title)
			default:
				idea.Have++
				if expiresSoon(it, today) {
					idea.UseFirst = append(idea.UseFirst, it.Title)
				}
			}
		}
		ideas = append(ideas, idea)
	}
	slices.SortFunc(ideas, func(a, b Idea) int {
		return cmp.Or(
			cmp.Compare(boolInt(b.Have == b.Total), boolInt(a.Have == a.Total)),
			cmp.Compare(len(b.UseFirst), len(a.UseFirst)),
			// Share at hand, a.Have/a.Total against b.Have/b.Total, without division.
			cmp.Compare(b.Have*a.Total, a.Have*b.Total),
			cmp.Compare(a.Recipe.Title, b.Recipe.Title),
			cmp.Compare(a.Recipe.ID, b.Recipe.ID),
		)
	})
	if limit > 0 && len(ideas) > limit {
		ideas = ideas[:limit]
	}
	return ideas
}

// atHand reports whether the item can be used now: it has not expired, and it is untracked or still has stock.
func atHand(it model.Item, today model.Day) bool {
	if it.Expires != "" && it.Expires < today {
		return false
	}
	return !it.HasQty || Estimate(it, today) > 0
}

// expiresSoon reports whether the item expires within its lead, today included.
func expiresSoon(it model.Item, today model.Day) bool {
	return it.Expires != "" && it.Expires >= today && it.Expires <= today.Add(Lead(it))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
