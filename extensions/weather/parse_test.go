package main

import (
	"strings"
	"testing"
)

// The parsing of Open-Meteo's real answers (captured under testdata/), so a change of the API's shape breaks here
// rather than as a page of zeros.

func TestParseForecast(t *testing.T) {
	f, err := parseForecast([]byte(readFixture(t, "forecast.json")))
	if err != nil {
		t.Fatal(err)
	}
	if f.Timezone != "Asia/Shanghai" {
		t.Fatalf("timezone %q", f.Timezone)
	}
	if f.Current.Temperature <= 0 || f.Current.Humidity <= 0 || f.Current.WeatherCode < 0 || f.Current.WindSpeed <= 0 {
		t.Fatalf("current %+v", f.Current)
	}
	if len(f.Hourly.Time) == 0 || len(f.Hourly.Time) != len(f.Hourly.Temp) {
		t.Fatalf("hourly %d times %d temps", len(f.Hourly.Time), len(f.Hourly.Temp))
	}
	if len(f.Daily.Time) != 7 || len(f.Daily.Time) != len(f.Daily.Max) || len(f.Daily.Time) != len(f.Daily.Min) {
		t.Fatalf("daily %d times %d max %d min", len(f.Daily.Time), len(f.Daily.Max), len(f.Daily.Min))
	}

	// The next 24 hours start at the current hour and carry 24 points.
	hours := f.next24()
	if len(hours) != 24 {
		t.Fatalf("next24 gave %d hours", len(hours))
	}
	if hours[0].at.Hour() != f.nowHour().Hour() {
		t.Fatalf("first hour %v, want %v", hours[0].at, f.nowHour())
	}
	for _, h := range hours {
		if h.at.Before(f.nowHour()) {
			t.Fatalf("a past hour %v", h.at)
		}
	}

	// The 7-day forecast has today first and a name for each day.
	ds := f.days(7)
	if len(ds) != 7 || ds[0].weekday() == "" || ds[6].weekday() == "" {
		t.Fatalf("days %d, weekdays %q %q", len(ds), ds[0].weekday(), ds[6].weekday())
	}
	if hi, lo, ok := f.todayHighLow(); !ok || hi < lo {
		t.Fatalf("today high/low %v %v %v", hi, lo, ok)
	}
}

func TestParseAir(t *testing.T) {
	q, err := parseAir([]byte(readFixture(t, "air.json")))
	if err != nil {
		t.Fatal(err)
	}
	if q.Current.EuropeanAQI == nil || q.Current.PM25 == nil || q.Current.PM10 == nil {
		t.Fatalf("air current %+v", q.Current)
	}
	if got := aqiLevel(*q.Current.EuropeanAQI); got == "" {
		t.Fatalf("empty AQI level for %v", *q.Current.EuropeanAQI)
	}
	if got := pm25Level(*q.Current.PM25); got == "" {
		t.Fatalf("empty PM2.5 level for %v", *q.Current.PM25)
	}
}

func TestParseAirMissingPollutants(t *testing.T) {
	q, err := parseAir([]byte(`{"timezone":"Asia/Shanghai","current":{"time":"2026-11-09T17:00","pm2_5":null,"pm10":null,"european_aqi":null}}`))
	if err != nil {
		t.Fatal(err)
	}
	if q.Current.EuropeanAQI != nil || q.Current.PM25 != nil || q.Current.PM10 != nil {
		t.Fatalf("expected nil pollutants, got %+v", q.Current)
	}
}

func TestParseGeo(t *testing.T) {
	g, err := parseGeo([]byte(readFixture(t, "geo.json")))
	if err != nil {
		t.Fatal(err)
	}
	c, err := g.best()
	if err != nil {
		t.Fatal(err)
	}
	if c.Name == "" || c.Latitude == 0 || c.Longitude == 0 || c.Timezone == "" {
		t.Fatalf("best %+v", c)
	}
}

func TestParseGeoEmpty(t *testing.T) {
	g, err := parseGeo([]byte(`{"results":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.best(); err != errNoCity {
		t.Fatalf("empty results gave %v", err)
	}
}

func TestParseBadAnswer(t *testing.T) {
	for _, raw := range []string{`[1,2,3]`, `{"current":`} {
		if _, err := parseForecast([]byte(raw)); err == nil || !strings.Contains(err.Error(), "读不懂") {
			t.Fatalf("parseForecast(%q) gave %v", raw, err)
		}
	}
	if _, err := parseAir([]byte(`not json`)); err == nil {
		t.Fatal("parseAir accepted non-JSON")
	}
}
