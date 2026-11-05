package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The overview card, the widget (every family) and the shortcuts: the places outside the extension's own page.

// card is the overview's compact card: a stat grid and a small chart (at most 2 sections, 12 components, no fields).
func (a *app) card(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	ch := series("用量", "", 3, 5, 2, 8, 6, 4, 7)
	return &wire.View{Sections: []wire.Section{{Items: items(
		wire.Component{Type: wire.ComponentStatGrid, Stats: []wire.Stat{
			{Label: "组件", Value: strconv.Itoa(len(wire.ComponentTypes))},
			{Label: "页面", Value: strconv.Itoa(len(pages))},
			{Label: "状态", Value: "正常", Tone: wire.TonePositive},
		}},
		wire.Component{Type: wire.ComponentChart, Kind: wire.ChartKindBar, Series: []wire.ChartSeries{ch}},
	)}}}, nil
}

// widget draws for the family the app asks (context.size); the hub fits it to what the family can draw.
func (a *app) widget(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	switch r.Size {
	case wire.WidgetFamilyAccessoryCircular:
		return &wire.View{Sections: []wire.Section{{Items: items(
			wire.Component{Type: wire.ComponentProgress, Label: "完成", Ratio: ratio(0.72)})}}}, nil
	case wire.WidgetFamilyAccessoryRectangular:
		return &wire.View{Sections: []wire.Section{{Items: items(
			wire.Component{Type: wire.ComponentMetric, Label: "组件展示", Value: strconv.Itoa(len(pages)) + " 页"})}}}, nil
	case wire.WidgetFamilyAccessoryInline:
		return &wire.View{Sections: []wire.Section{{Items: items(
			wire.Component{Type: wire.ComponentText, Text: fmt.Sprintf("组件展示 · %d 页", len(pages))})}}}, nil
	case wire.WidgetFamilySmall:
		return &wire.View{Sections: []wire.Section{{Items: items(
			wire.Component{Type: wire.ComponentMetric, Label: "页面", Value: strconv.Itoa(len(pages)), Detail: "组件展示", Tone: wire.TonePositive},
			wire.Component{Type: wire.ComponentProgress, Label: "完成", Ratio: ratio(0.72)},
		)}}}, nil
	}
	// medium, large
	stats := []wire.Stat{
		{Label: "组件", Value: strconv.Itoa(len(wire.ComponentTypes))},
		{Label: "页面", Value: strconv.Itoa(len(pages))},
		{Label: "状态", Value: "正常", Tone: wire.TonePositive},
	}
	c := items(wire.Component{Type: wire.ComponentStatGrid, Stats: stats})
	if r.Size == wire.WidgetFamilyLarge {
		c = append(c,
			wire.Component{Type: wire.ComponentChart, Kind: wire.ChartKindBar, Series: []wire.ChartSeries{series("用量", "", 3, 5, 2, 8, 6, 4, 7)}},
			wire.Component{Type: wire.ComponentRow, Title: "列表", Value: "60 条"},
			wire.Component{Type: wire.ComponentRow, Title: "图表", Value: "4 种"},
		)
	}
	return &wire.View{Sections: []wire.Section{{Items: c}}}, nil
}

// shortcut answers shortcut.run: showcase-echo says the text back, showcase-count counts to n.
func (a *app) shortcut(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	if method != "run" {
		return nil, ext.UnknownMethod("shortcut." + method)
	}
	var p wire.ShortcutRunParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &ext.Error{Code: "invalid", Message: err.Error()}
	}
	switch p.Contribution {
	case "showcase-echo":
		return wire.ShortcutResult{Text: "你说：" + p.Values["text"]}, nil
	case "showcase-count":
		n, err := strconv.Atoi(strings.TrimSpace(p.Values["n"]))
		if err != nil || n < 0 || n > 1000 {
			return nil, &ext.Error{Code: "invalid", Message: "数到几：0 到 1000 的整数"}
		}
		unit := map[string]string{"apple": "个苹果", "star": "颗星星"}[p.Values["unit"]]
		if unit == "" {
			unit = "个"
		}
		text := fmt.Sprintf("数到了 %d %s", n, unit)
		if p.Values["loud"] == "true" {
			text += "！"
		}
		return wire.ShortcutResult{Text: text, View: &wire.View{Sections: []wire.Section{{Items: items(
			wire.Component{Type: wire.ComponentMetric, Label: "数到", Value: strconv.Itoa(n), Detail: strings.TrimPrefix(unit, "个")},
		)}}}}, nil
	}
	return nil, ext.NoContribution(p.Contribution)
}
