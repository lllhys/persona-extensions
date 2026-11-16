package ui

import (
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/store"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/usage"
)

// Card draws the overview card: today's total against the seven-day daily average, and the model that used the most.
func (a *App) Card(r *ext.UIRequest) (*wire.View, error) {
	var view *wire.View
	err := a.st.View(func(st *store.State) error {
		now, loc := a.now(), a.loc()
		today := usage.Summarize(st, now, 1, loc, usage.Filter{})
		week := usage.Summarize(st, now, 7, loc, usage.Filter{})
		average := week.Total.Total() / 7

		text, tone := formatChange(today.Total.Total(), average)
		top := "还没有用量"
		if len(today.Models) > 0 && today.Models[0].Total() > 0 {
			top = modelName(today.Models[0].Name) + " " + formatTokens(today.Models[0].Total())
		}
		view = &wire.View{
			Title: "Token 用量",
			Sections: []wire.Section{{Items: []wire.Component{
				{Type: wire.ComponentMetric, Label: "今天", Value: formatTokens(today.Total.Total()),
					Detail: "7 日均 " + formatTokens(average) + " · " + text, Tone: tone},
				{Type: wire.ComponentText, Text: "最高 " + top},
			}}},
			State: ext.EncodeState(pageState{Tab: tabOverview}),
		}
		return nil
	})
	return view, err
}
