package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The connector's read tools, for the assistant: the hub's machine reaches Open-Meteo where the user's computers and
// phone may not.

// Tool is a tool as connector.tools.list lists it.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// cityParam is the schema of the shared city argument.
var cityParam = map[string]any{"type": "string", "description": "城市名，如「上海」；不写用扩展里设的默认城市。没存过的名字会自动搜索"}

var tools = []Tool{
	{Name: "weather_now",
		Description: "查一个城市的当前天气：温度、体感温度、天气现象、湿度、风向风速，以及今天的最高最低。用户问现在的天气、冷不冷、出门穿什么时用它。",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"city": cityParam}}},
	{Name: "weather_forecast",
		Description: "查一个城市未来几天的天气预报：每天的最高最低温、天气现象、降水概率。用户问后面几天的天气、要不要带伞、安排出行时用它。",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"city": cityParam,
			"days": map[string]any{"type": "integer", "minimum": 1, "maximum": 7, "description": "几天，默认 7"}}}},
	{Name: "weather_air",
		Description: "查一个城市的空气质量：AQI（欧洲空气质量指数）、PM2.5、PM10，带等级说明。用户问空气质量、雾霾、能不能户外活动时用它。",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"city": cityParam}}},
}

func (a *app) callTool(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	var args struct {
		City string `json:"city"`
		Days int    `json:"days"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("参数不对：%w", err)
		}
	}
	switch name {
	case "weather_now":
		return a.toolNow(ctx, args.City)
	case "weather_forecast":
		days := 7
		if args.Days > 0 {
			days = min(args.Days, 7)
		}
		return a.toolForecast(ctx, args.City, days)
	case "weather_air":
		return a.toolAir(ctx, args.City)
	}
	return "", fmt.Errorf("没有工具 %s", name)
}

// resolveCity finds the city to read: a configured one by name, the default when the name is empty, or a search.
func (a *app) resolveCity(ctx context.Context, name string) (City, error) {
	if c, ok := cityByName(a.cities, name); ok {
		return c, nil
	}
	rd, err := a.read(ctx, a.client.geo, geoURL(name), cacheFor)
	if err != nil {
		return City{}, err
	}
	g, err := parseGeo(rd.body)
	if err != nil {
		return City{}, err
	}
	return g.best()
}

// readForecast reads and parses a city's forecast.
func (a *app) readForecast(ctx context.Context, c City, fresh time.Duration) (forecast, reading, error) {
	rd, err := a.read(ctx, a.client.forecast, forecastURL(c), fresh)
	if err != nil {
		return forecast{}, reading{}, err
	}
	f, err := parseForecast(rd.body)
	return f, rd, err
}

// readAir reads and parses a city's air quality.
func (a *app) readAir(ctx context.Context, c City, fresh time.Duration) (air, reading, error) {
	rd, err := a.read(ctx, a.client.air, airURL(c), fresh)
	if err != nil {
		return air{}, reading{}, err
	}
	q, err := parseAir(rd.body)
	return q, rd, err
}

// curTime writes current.Time as "2006-01-02 15:04" in words (it is already the city's own time).
func curTime(s string) string {
	if t, err := time.Parse(hourTime, s); err == nil {
		return t.Format("01-02 15:04")
	}
	return strings.ReplaceAll(s, "T", " ")
}

func (a *app) toolNow(ctx context.Context, name string) (string, error) {
	c, err := a.resolveCity(ctx, name)
	if err != nil {
		return "", err
	}
	f, _, err := a.readForecast(ctx, c, cacheFor)
	if err != nil {
		return "", err
	}
	u, w := a.cfg.tempUnit(), a.cfg.windUnit()
	cur := f.Current
	var b strings.Builder
	fmt.Fprintf(&b, "%s 当前（%s）：%s，体感 %s，%s%s\n", c.Name, curTime(cur.Time),
		fmtTemp(cur.Temperature, u), fmtTemp(cur.Apparent, u), wmoText(cur.WeatherCode), iconOf(cur.WeatherCode))
	fmt.Fprintf(&b, "湿度 %.0f%%", cur.Humidity)
	if cur.WindSpeed > 0 {
		fmt.Fprintf(&b, "，%s %s", windDir(cur.WindDirection), fmtWind(cur.WindSpeed, w))
	} else {
		b.WriteString("，无风")
	}
	if hi, lo, ok := f.todayHighLow(); ok {
		fmt.Fprintf(&b, "\n今天：最低 %s · 最高 %s", fmtTemp(lo, u), fmtTemp(hi, u))
	}
	return b.String(), nil
}

func (a *app) toolForecast(ctx context.Context, name string, days int) (string, error) {
	c, err := a.resolveCity(ctx, name)
	if err != nil {
		return "", err
	}
	f, _, err := a.readForecast(ctx, c, cacheFor)
	if err != nil {
		return "", err
	}
	u := a.cfg.tempUnit()
	ds := f.days(days)
	if len(ds) == 0 {
		return "", errors.New("Open-Meteo 没给这几天的预报")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %d 天预报：", c.Name, len(ds))
	for _, d := range ds {
		date := ""
		if !d.date.IsZero() {
			date = d.date.Format("01-02")
		}
		fmt.Fprintf(&b, "\n- %s %s：%s，%s ~ %s，降水概率 %.0f%%", d.weekday(), date,
			wmoText(d.code), fmtTemp(d.min, u), fmtTemp(d.max, u), d.precip)
	}
	return b.String(), nil
}

func (a *app) toolAir(ctx context.Context, name string) (string, error) {
	c, err := a.resolveCity(ctx, name)
	if err != nil {
		return "", err
	}
	q, _, err := a.readAir(ctx, c, cacheFor)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s 空气质量：", c.Name)
	if q.Current.EuropeanAQI != nil {
		fmt.Fprintf(&b, "AQI %.0f（%s）", *q.Current.EuropeanAQI, aqiLevel(*q.Current.EuropeanAQI))
	} else {
		b.WriteString("AQI 无")
	}
	if q.Current.PM25 != nil {
		fmt.Fprintf(&b, "，PM2.5 %s µg/m³（%s）", trimZero(*q.Current.PM25), pm25Level(*q.Current.PM25))
	}
	if q.Current.PM10 != nil {
		fmt.Fprintf(&b, "，PM10 %s µg/m³", trimZero(*q.Current.PM10))
	}
	return b.String(), nil
}

// iconOf is the emoji for a WMO code (the description lives in wmo).
func iconOf(code int) string {
	_, icon := wmo(code)
	return icon
}
