package main

import (
	"strings"
	"testing"
)

const nullForecast = `{"current":{"time":"2026-11-16T12:00","temperature_2m":null,"apparent_temperature":null,"relative_humidity_2m":null,"weather_code":null,"wind_speed_10m":null},"daily":{"time":["2026-11-16"],"temperature_2m_max":[null],"temperature_2m_min":[null],"weather_code":[null],"precipitation_probability_max":[null]}}`

func TestNullWeatherReadingsAreUnknown(t *testing.T) {
	f := newFakeSite(t)
	f.serve("/v1/forecast", nullForecast)
	a := testApp(t, f, "")
	for _, name := range []string{"weather_now", "weather_forecast"} {
		out, err := callTool(t, a, name, "")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "0°C") || strings.Contains(out, "晴朗") || strings.Contains(out, "0%") {
			t.Fatalf("%s turned missing measurements into zero/clear: %s", name, out)
		}
		if !strings.Contains(out, "未知") {
			t.Fatalf("%s does not name unavailable readings: %s", name, out)
		}
	}
}

func TestZeroWeatherReadingsRemainZero(t *testing.T) {
	f := newFakeSite(t)
	f.serve("/v1/forecast", `{"current":{"time":"2026-11-16T12:00","temperature_2m":0,"apparent_temperature":0,"relative_humidity_2m":0,"weather_code":0,"wind_speed_10m":0},"daily":{"time":["2026-11-16"],"temperature_2m_max":[0],"temperature_2m_min":[0],"weather_code":[0],"precipitation_probability_max":[0]}}`)
	a := testApp(t, f, "")
	now, err := callTool(t, a, "weather_now", "")
	if err != nil {
		t.Fatal(err)
	}
	forecast, err := callTool(t, a, "weather_forecast", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{now, forecast} {
		if !strings.Contains(out, "0°C") || !strings.Contains(out, "晴朗") || !strings.Contains(out, "0%") {
			t.Fatalf("genuine zero was not preserved: %s", out)
		}
	}
}
