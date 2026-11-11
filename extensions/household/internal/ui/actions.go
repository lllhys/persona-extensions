package ui

import (
	"errors"
	"fmt"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/ops"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// The write actions. Each runs one transaction of the ops package on the base; an error from it is the toast.

// actRestock creates a purchase for every finding that needs one and has none open.
func actRestock(c *ctx, _ string) (*wire.ActionResult, error) {
	made, err := ops.Restock(c.b, c.now, c.loc)
	if err != nil {
		return nil, err
	}
	if len(made) == 0 {
		return &wire.ActionResult{Toast: "没有需要补的货"}, nil
	}
	return &wire.ActionResult{Toast: fmt.Sprintf("已加入 %d 条采购", len(made))}, nil
}

// actBuy marks a purchase bought, which stocks its item.
func actBuy(c *ctx, arg string) (*wire.ActionResult, error) {
	e, err := ops.Purchase(c.b, arg, true, c.now, c.loc)
	if err != nil {
		return nil, err
	}
	return &wire.ActionResult{Toast: "已买「" + cut(e.Title, maxTitle) + "」"}, nil
}

// actSkip marks a purchase 不买: nothing is stocked.
func actSkip(c *ctx, arg string) (*wire.ActionResult, error) {
	e, err := ops.Purchase(c.b, arg, false, c.now, c.loc)
	if err != nil {
		return nil, err
	}
	return &wire.ActionResult{Toast: "已不买「" + cut(e.Title, maxTitle) + "」"}, nil
}

// actChore completes a chore and schedules its next round.
func actChore(c *ctx, arg string) (*wire.ActionResult, error) {
	e, err := ops.ChoreDone(c.b, arg, c.now, c.loc)
	if err != nil {
		return nil, err
	}
	return &wire.ActionResult{Toast: "已完成「" + cut(e.Title, maxTitle) + "」"}, nil
}

// actUse takes one unit off an item's estimated quantity.
func actUse(c *ctx, arg string) (*wire.ActionResult, error) {
	e, err := ops.Use(c.b, arg, 1, c.now, c.loc)
	if err != nil {
		return nil, err
	}
	return &wire.ActionResult{Toast: "已用掉 1，「" + cut(e.Title, maxTitle) + "」"}, nil
}

// actPlan plans a dinner for today with the recipe.
func actPlan(c *ctx, arg string) (*wire.ActionResult, error) {
	rec, ok := c.home.Recipe(arg)
	if !ok {
		return nil, errors.New("没有这道菜")
	}
	title := cut(rec.Title, maxTitle)
	in := kernel.EntityInput{Type: model.TMeal, Title: &title, Values: map[string]any{
		"date":   string(c.today),
		"slot":   "晚餐",
		"recipe": []string{rec.ID},
	}}
	if _, err := ops.Save(c.b, in, c.now, c.loc); err != nil {
		return nil, err
	}
	return &wire.ActionResult{Toast: "已排到今晚「" + title + "」"}, nil
}

// actMealDone marks a planned meal 已做.
func actMealDone(c *ctx, arg string) (*wire.ActionResult, error) {
	return closeMeal(c, arg, true)
}

// actMealCancel marks a planned meal 取消.
func actMealCancel(c *ctx, arg string) (*wire.ActionResult, error) {
	return closeMeal(c, arg, false)
}

// closeMeal closes a planned meal, done or cancelled.
func closeMeal(c *ctx, id string, done bool) (*wire.ActionResult, error) {
	e, err := ops.MealClose(c.b, id, done, c.now, c.loc)
	if err != nil {
		return nil, err
	}
	word := "已取消"
	if done {
		word = "已做"
	}
	return &wire.ActionResult{Toast: word + "「" + cut(e.Title, maxTitle) + "」"}, nil
}

// actLoad creates the sample records, in a household with none.
func actLoad(c *ctx, _ string) (*wire.ActionResult, error) {
	n, err := ops.LoadSamples(c.b, c.now, c.loc)
	if err != nil {
		return nil, err
	}
	return &wire.ActionResult{Toast: fmt.Sprintf("已载入 %d 条示例", n)}, nil
}
