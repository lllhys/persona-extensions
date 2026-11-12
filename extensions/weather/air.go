package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// The air quality (air-quality-api.open-meteo.com/v1/air-quality). The European AQI runs 0 to >100; the pollutants are
// in µg/m³.

// air is the current air quality the page and the air tool read.
type air struct {
	Timezone string     `json:"timezone"`
	Current  airCurrent `json:"current"`
	Hourly   airHourly  `json:"hourly"`
}

type airCurrent struct {
	Time        string   `json:"time"`
	PM25        *float64 `json:"pm2_5"`
	PM10        *float64 `json:"pm10"`
	EuropeanAQI *float64 `json:"european_aqi"`
}

// airHourly keeps forward observations distinct from the current card. Null pollen is unavailable, never zero.
type airHourly struct {
	Time    []string   `json:"time"`
	PM25    []*float64 `json:"pm2_5"`
	PM10    []*float64 `json:"pm10"`
	AQI     []*float64 `json:"european_aqi"`
	Alder   []*float64 `json:"alder_pollen"`
	Birch   []*float64 `json:"birch_pollen"`
	Grass   []*float64 `json:"grass_pollen"`
	Mugwort []*float64 `json:"mugwort_pollen"`
	Olive   []*float64 `json:"olive_pollen"`
	Ragweed []*float64 `json:"ragweed_pollen"`
}

type pollenReading struct {
	Species string
	Value   float64
}

type airOutlook struct {
	At     time.Time
	AQI    *float64
	PM25   *float64
	PM10   *float64
	Pollen []pollenReading
}

var pollenSeries = []struct {
	name  string
	label string
	read  func(airHourly) []*float64
}{
	{name: "alder", label: "桤木", read: func(h airHourly) []*float64 { return h.Alder }},
	{name: "birch", label: "桦木", read: func(h airHourly) []*float64 { return h.Birch }},
	{name: "grass", label: "禾草", read: func(h airHourly) []*float64 { return h.Grass }},
	{name: "mugwort", label: "艾蒿", read: func(h airHourly) []*float64 { return h.Mugwort }},
	{name: "olive", label: "橄榄", read: func(h airHourly) []*float64 { return h.Olive }},
	{name: "ragweed", label: "豚草", read: func(h airHourly) []*float64 { return h.Ragweed }},
}

func (a air) outlook() []airOutlook {
	now := a.nowHour()
	out := make([]airOutlook, 0, 24)
	for i, raw := range a.Hourly.Time {
		at, err := time.Parse(hourTime, raw)
		if err != nil || at.Before(now) || len(out) >= 24 {
			continue
		}
		point := airOutlook{At: at, AQI: numberAt(a.Hourly.AQI, i), PM25: numberAt(a.Hourly.PM25, i), PM10: numberAt(a.Hourly.PM10, i)}
		for _, species := range pollenSeries {
			if value := numberAt(species.read(a.Hourly), i); value != nil {
				point.Pollen = append(point.Pollen, pollenReading{Species: species.name, Value: *value})
			}
		}
		out = append(out, point)
	}
	return out
}

func (a air) nowHour() time.Time {
	at, err := time.Parse(hourTime, a.Current.Time)
	if err != nil {
		return time.Time{}
	}
	return at.Truncate(time.Hour)
}

func numberAt(values []*float64, i int) *float64 {
	if i < 0 || i >= len(values) || values[i] == nil {
		return nil
	}
	value := *values[i]
	return &value
}

func pollenLabel(species string) string {
	for _, item := range pollenSeries {
		if item.name == species {
			return item.label
		}
	}
	return "花粉"
}

// parseAir reads an answer.
func parseAir(raw []byte) (air, error) {
	var a air
	if err := json.Unmarshal(raw, &a); err != nil {
		return air{}, fmt.Errorf("%w：%v", errBadAnswer, err)
	}
	return a, nil
}

// aqiLevel names the European AQI bands (0-20 good … >100 extremely poor).
func aqiLevel(aqi float64) string {
	switch {
	case aqi < 20:
		return "优"
	case aqi < 40:
		return "良"
	case aqi < 60:
		return "一般"
	case aqi < 80:
		return "较差"
	case aqi < 100:
		return "很差"
	}
	return "极差"
}

// pm25Level names the PM2.5 concentration bands in µg/m³ (the Chinese standard, familiar to the audience).
func pm25Level(pm float64) string {
	switch {
	case pm < 35:
		return "优"
	case pm < 75:
		return "良"
	case pm < 115:
		return "轻度污染"
	case pm < 150:
		return "中度污染"
	case pm < 250:
		return "重度污染"
	}
	return "严重污染"
}
