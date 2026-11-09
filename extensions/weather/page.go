package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The page (ui contributions "weather", a nav_page, and "page", a plugin_page): the current weather, the next 24
// hours as a line, the 7-day forecast, and one air quality card. The state it carries is which city is chosen.

type pageState struct {
	City string `json:"c,omitempty"` // the chosen city's name; empty is the first (the default)
}

// readState reads the page's state, keeping the chosen city among the configured ones.
func (a *app) readState(r *ext.UIRequest) pageState {
	var st pageState
	if r.DecodeState(&st) != nil {
		st = pageState{}
	}
	if st.City != "" {
		if _, ok := cityByName(a.cities, st.City); !ok {
			st.City = ""
		}
	}
	return st
}

// selected is the city to draw.
func (a *app) selected(st pageState) City {
	c, _ := cityByName(a.cities, st.City)
	return c
}

func (a *app) render(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	ctx, cancel := context.WithTimeout(ctx, renderBudget)
	defer cancel()
	return a.page(ctx, r, a.readState(r), false), nil
}

func (a *app) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, renderBudget)
	defer cancel()
	st := a.readState(r)
	refresh := false
	switch r.Action {
	case "city":
		if name := r.Values["city"]; name != "" {
			if _, ok := cityByName(a.cities, name); ok {
				st.City = name
			}
		}
	case "refresh":
		refresh = true
	}
	return &wire.ActionResult{View: a.page(ctx, r, st, refresh)}, nil
}

func text(s, style string) wire.Component {
	return wire.Component{Type: wire.ComponentText, Text: s, Style: style}
}

func button(title, id string) wire.Component {
	return wire.Component{Type: wire.ComponentButton, Title: title, Action: &wire.Action{ID: id}}
}

// picker chooses the city when there are several: a segmented control, or a row each before UI version 2.
func (a *app) picker(r *ext.UIRequest, st pageState) wire.Section {
	if len(a.cities) <= 1 {
		return wire.Section{}
	}
	if r.UIVersion >= 2 {
		seg := wire.Component{Type: wire.ComponentSegmented, Name: "city", Value: a.selected(st).Name, Action: &wire.Action{ID: "city"}}
		for _, c := range a.cities {
			seg.Choices = append(seg.Choices, wire.Choice{Value: c.Name, Title: c.Name})
		}
		return wire.Section{Items: []wire.Component{seg}}
	}
	sec := wire.Section{Title: "城市"}
	for _, c := range a.cities {
		it := wire.Component{Type: wire.ComponentRow, Title: c.Name, Action: &wire.Action{ID: "city", Values: map[string]string{"city": c.Name}}}
		if c.Name == a.selected(st).Name {
			it.Badge = &wire.Badge{Text: "正在看", Tone: wire.TonePositive}
		}
		sec.Items = append(sec.Items, it)
	}
	return sec
}

// page is the weather for one city; what went wrong is drawn, with a way to try again, never as a page of zeros.
func (a *app) page(ctx context.Context, r *ext.UIRequest, st pageState, refresh bool) *wire.View {
	c := a.selected(st)
	loc := locFor(c)
	v := &wire.View{Title: "天气", State: ext.EncodeState(st), RefreshAfter: 600}
	if p := a.picker(r, st); len(p.Items) > 0 {
		v.Sections = append(v.Sections, p)
	}
	if len(a.badCities) > 0 {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{
			text(clip("这些城市没填对，没有读："+badCityNames(a.badCities)+"。要写名字、纬度（-90 到 90）、经度（-180 到 180）", 1900), wire.TextStyleSecondary)}})
	}

	u, w := a.cfg.tempUnit(), a.cfg.windUnit()
	f, rd, err := a.readForecast(ctx, c, freshness(refresh))
	if err != nil {
		v.Sections = append(v.Sections, wire.Section{Title: c.Name, Items: []wire.Component{
			text(clip(oneLine(err.Error()), 1900), wire.TextStyleSecondary), button("重试", "refresh")}})
		return v
	}

	v.Sections = append(v.Sections, a.nowSection(c, f, u, w))

	hours := f.next24()
	if len(hours) > 0 {
		v.Sections = append(v.Sections, a.hoursSection(hours, u))
	}

	if ds := f.days(7); len(ds) > 0 {
		v.Sections = append(v.Sections, a.forecastSection(ds, u))
	}

	v.Sections = append(v.Sections, a.airSection(ctx, c, freshness(refresh)))

	tail := wire.Section{}
	if !rd.at.IsZero() {
		tail.Items = append(tail.Items, text("读于 "+rd.at.In(loc).Format("15:04")+"（10 分钟内不重读）", wire.TextStyleCaption))
	}
	tail.Items = append(tail.Items, button("刷新", "refresh"))
	v.Sections = append(v.Sections, tail)
	return v
}

// nowSection is the current weather: a big temperature with its icon, and the apparent temperature, humidity, wind
// and today's high and low beside it.
func (a *app) nowSection(c City, f forecast, u temperatureUnit, w windUnit) wire.Section {
	cur := f.Current
	desc, icon := wmo(cur.WeatherCode)
	big := wire.Component{Type: wire.ComponentMetric, Label: "现在", Value: icon + " " + fmtTemp(cur.Temperature, u), Detail: desc}

	stats := []wire.Stat{
		{Label: "体感", Value: fmtTemp(cur.Apparent, u)},
		{Label: "湿度", Value: fmt.Sprintf("%.0f%%", cur.Humidity)},
	}
	if cur.WindSpeed > 0 {
		stats = append(stats, wire.Stat{Label: windDir(cur.WindDirection), Value: fmtWind(cur.WindSpeed, w)})
	} else {
		stats = append(stats, wire.Stat{Label: "风", Value: "无风"})
	}
	if hi, lo, ok := f.todayHighLow(); ok {
		stats = append(stats, wire.Stat{Label: "今天", Value: fmtTemp(lo, u) + " / " + fmtTemp(hi, u)})
	}

	sec := wire.Section{Title: c.Name, Items: []wire.Component{big}}
	sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentStatGrid, Stats: stats})
	return sec
}

// hoursSection is the next 24 hours as a temperature line.
func (a *app) hoursSection(hours []hourPoint, u temperatureUnit) wire.Section {
	pts := make([]wire.ChartPoint, 0, len(hours))
	for _, h := range hours {
		pts = append(pts, wire.ChartPoint{Label: h.at.Format("15") + "时", Value: u.temp(h.temp)})
	}
	chart := wire.Component{Type: wire.ComponentChart, Kind: wire.ChartKindLine, Unit: u.tempLabel(),
		Series: []wire.ChartSeries{{Name: "气温", Points: pts}}}
	return wire.Section{Title: "未来 24 小时", Items: []wire.Component{chart}}
}

// forecastSection is the 7-day forecast, one row a day.
func (a *app) forecastSection(ds []day, u temperatureUnit) wire.Section {
	sec := wire.Section{Title: "7 天预报"}
	for _, d := range ds {
		desc, icon := wmo(d.code)
		title := d.weekday()
		if title == "" {
			title = d.date.Format("01-02")
		}
		row := wire.Component{Type: wire.ComponentRow,
			Title:    fmt.Sprintf("%s %s %s / %s", title, icon, fmtTemp(d.max, u), fmtTemp(d.min, u)),
			Subtitle: fmt.Sprintf("%s · 降水概率 %.0f%%", desc, d.precip)}
		sec.Items = append(sec.Items, row)
	}
	if len(sec.Items) == 0 {
		sec.Items = append(sec.Items, text("没有这几天的预报", wire.TextStyleSecondary))
	}
	return sec
}

// airSection is the air quality card; a failed read is its own note, not a page of zeros.
func (a *app) airSection(ctx context.Context, c City, fresh time.Duration) wire.Section {
	q, _, err := a.readAir(ctx, c, fresh)
	if err != nil {
		return wire.Section{Title: "空气质量", Items: []wire.Component{
			text(clip(oneLine(err.Error()), 1900), wire.TextStyleSecondary)}}
	}
	var stats []wire.Stat
	if q.Current.EuropeanAQI != nil {
		stats = append(stats, wire.Stat{Label: "空气质量指数", Value: fmt.Sprintf("%.0f · %s", *q.Current.EuropeanAQI, aqiLevel(*q.Current.EuropeanAQI))})
	}
	if q.Current.PM25 != nil {
		stats = append(stats, wire.Stat{Label: "PM2.5", Value: trimZero(*q.Current.PM25) + " µg/m³", Detail: pm25Level(*q.Current.PM25)})
	}
	if q.Current.PM10 != nil {
		stats = append(stats, wire.Stat{Label: "PM10", Value: trimZero(*q.Current.PM10) + " µg/m³"})
	}
	if len(stats) == 0 {
		return wire.Section{Title: "空气质量", Items: []wire.Component{
			text("这个城市没有空气质量数据", wire.TextStyleSecondary)}}
	}
	return wire.Section{Title: "空气质量", Items: []wire.Component{{Type: wire.ComponentStatGrid, Stats: stats}}}
}

// badCityNames lists the unusable cities in words.
func badCityNames(bad []City) string {
	names := make([]string, 0, len(bad))
	for _, c := range bad {
		n := c.Name
		if strings.TrimSpace(n) == "" {
			n = "（没写名字）"
		}
		names = append(names, n)
	}
	return strings.Join(names, "、")
}
