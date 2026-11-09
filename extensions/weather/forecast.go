package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// The forecast (api.open-meteo.com/v1/forecast), as its JSON answers it. The API sends temperatures in °C and wind in
// km/h; units.go turns them into the user's.

// forecast is the parts of the answer the page and tools read.
type forecast struct {
	Timezone string  `json:"timezone"`
	Current  current `json:"current"`
	Hourly   hourly  `json:"hourly"`
	Daily    daily   `json:"daily"`
}

type current struct {
	Time          string  `json:"time"`
	Temperature   float64 `json:"temperature_2m"`
	Apparent      float64 `json:"apparent_temperature"`
	Humidity      float64 `json:"relative_humidity_2m"`
	WeatherCode   int     `json:"weather_code"`
	WindSpeed     float64 `json:"wind_speed_10m"`
	WindDirection float64 `json:"wind_direction_10m"`
}

type hourly struct {
	Time   []string  `json:"time"`
	Temp   []float64 `json:"temperature_2m"`
	Code   []int     `json:"weather_code"`
	Precip []float64 `json:"precipitation_probability"`
}

type daily struct {
	Time    []string  `json:"time"`
	Code    []int     `json:"weather_code"`
	Max     []float64 `json:"temperature_2m_max"`
	Min     []float64 `json:"temperature_2m_min"`
	Precip  []float64 `json:"precipitation_probability_max"`
	Sunrise []string  `json:"sunrise"`
	Sunset  []string  `json:"sunset"`
}

// hourTime is the hourly time format Open-Meteo sends (city-local, no seconds).
const hourTime = "2006-01-02T15:04"

// dayTime is the daily time format.
const dayTime = "2006-01-02"

// parseForecast reads an answer; a change of shape is errBadAnswer, not a page of zeros.
func parseForecast(raw []byte) (forecast, error) {
	var f forecast
	if err := json.Unmarshal(raw, &f); err != nil {
		return forecast{}, fmt.Errorf("%w：%v", errBadAnswer, err)
	}
	return f, nil
}

// nowHour is the hour of the current reading, floored, so the next 24 hours start at it.
func (f forecast) nowHour() time.Time {
	t, err := time.Parse(hourTime, f.Current.Time)
	if err != nil {
		return time.Now().Truncate(time.Hour)
	}
	return t.Truncate(time.Hour)
}

// hourPoint is one point of the next-24-hours line.
type hourPoint struct {
	at   time.Time
	temp float64
}

// next24 is the 24 hours starting at the current hour: label and temperature.
func (f forecast) next24() []hourPoint {
	from := f.nowHour()
	out := make([]hourPoint, 0, 24)
	for i, s := range f.Hourly.Time {
		if i >= len(f.Hourly.Temp) {
			break
		}
		t, err := time.Parse(hourTime, s)
		if err != nil || t.Before(from) {
			continue
		}
		out = append(out, hourPoint{at: t, temp: f.Hourly.Temp[i]})
		if len(out) == 24 {
			break
		}
	}
	return out
}

// day is one row of the 7-day forecast.
type day struct {
	date   time.Time
	code   int
	max    float64
	min    float64
	precip float64
}

// days is the daily forecast, at most n days, from today on.
func (f forecast) days(n int) []day {
	var out []day
	for i, s := range f.Daily.Time {
		if len(out) >= n {
			break
		}
		d := day{}
		if t, err := time.Parse(dayTime, s); err == nil {
			d.date = t
		}
		if i < len(f.Daily.Code) {
			d.code = f.Daily.Code[i]
		}
		if i < len(f.Daily.Max) {
			d.max = f.Daily.Max[i]
		}
		if i < len(f.Daily.Min) {
			d.min = f.Daily.Min[i]
		}
		if i < len(f.Daily.Precip) {
			d.precip = f.Daily.Precip[i]
		}
		out = append(out, d)
	}
	return out
}

// todayHighLow is today's high and low (daily[0]), for the current weather and the now tool.
func (f forecast) todayHighLow() (high, low float64, ok bool) {
	if len(f.Daily.Max) == 0 || len(f.Daily.Min) == 0 {
		return 0, 0, false
	}
	return f.Daily.Max[0], f.Daily.Min[0], true
}

// weekdays are the Chinese names, Sunday first (time.Weekday is Sunday=0).
var weekdays = [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

// weekday is the day's Chinese weekday name ("" when its date is unknown).
func (d day) weekday() string {
	if d.date.IsZero() {
		return ""
	}
	return weekdays[d.date.Weekday()]
}
