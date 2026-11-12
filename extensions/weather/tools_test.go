package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func callTool(t *testing.T, a *app, name, args string) (string, error) {
	t.Helper()
	var raw json.RawMessage
	if args != "" {
		raw = json.RawMessage(args)
	}
	return a.callTool(context.Background(), name, raw)
}

func TestToolList(t *testing.T) {
	if len(tools) != 4 {
		t.Fatalf("%d tools", len(tools))
	}
	names := []string{}
	for _, tool := range tools {
		names = append(names, tool.Name)
		if tool.InputSchema["type"] != "object" || tool.Description == "" {
			t.Fatalf("tool %+v", tool)
		}
	}
	if names[0] != "weather_now" || names[1] != "weather_forecast" || names[2] != "weather_air" || names[3] != "weather_pollen" {
		t.Fatalf("tools %v", names)
	}
}

func TestToolNowDefaultCity(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, "")
	out, err := callTool(t, a, "weather_now", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"上海", "当前", "体感", "湿度", "今天"} {
		if !strings.Contains(out, w) {
			t.Errorf("no %q in %q", w, out)
		}
	}
}

func TestToolForecastDays(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, "")
	for _, c := range []struct {
		days string
		want int
	}{{"", 7}, {`{"days": 1}`, 1}, {`{"days": 3}`, 3}, {`{"days": 99}`, 7}, {`{"days": -2}`, 7}} {
		out, err := callTool(t, a, "weather_forecast", c.days)
		if err != nil {
			t.Fatalf("%s: %v", c.days, err)
		}
		if got := strings.Count(out, "降水概率"); got != c.want {
			t.Errorf("%s: %d days, want %d in %q", c.days, got, c.want, out)
		}
	}
}

func TestToolAir(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, "")
	out, err := callTool(t, a, "weather_air", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"上海", "空气质量", "AQI", "PM2.5", "PM10"} {
		if !strings.Contains(out, w) {
			t.Errorf("no %q in %q", w, out)
		}
	}
}

// An unknown but searchable city is geocoded and read.
func TestToolUnknownCityGeocoded(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, "")
	out, err := callTool(t, a, "weather_now", `{"city": "北京"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "北京") {
		t.Fatalf("geocoded name missing in %q", out)
	}
}

// An unknown name with no hits says so, in words.
func TestToolCityNotFound(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	f.serve("/v1/search", `{"results":[]}`)
	a := testApp(t, f, "")
	if _, err := callTool(t, a, "weather_now", `{"city": "不存在的城市"}`); err == nil || !strings.Contains(err.Error(), "没搜到") {
		t.Fatalf("not found gave %v", err)
	}
}

// A configured city answers without a search.
func TestToolConfiguredCityNoGeocode(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, `{"cities": [{"name": "上海", "latitude": 31.22, "longitude": 121.46}, {"name": "北京", "latitude": 39.9, "longitude": 116.4}]}`)
	if _, err := callTool(t, a, "weather_now", `{"city": "北京"}`); err != nil {
		t.Fatal(err)
	}
	if n := f.hit("/v1/search"); n != 0 {
		t.Fatalf("a configured city still searched %d times", n)
	}
}

func TestToolBoundaries(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, "")
	if _, err := callTool(t, a, "weather_nope", ""); err == nil || !strings.Contains(err.Error(), "没有工具") {
		t.Fatalf("unknown tool gave %v", err)
	}
	if _, err := callTool(t, a, "weather_now", `[`); err == nil || !strings.Contains(err.Error(), "参数不对") {
		t.Fatalf("bad args gave %v", err)
	}
	if _, err := callTool(t, a, "weather_now", `{"city": 12}`); err == nil {
		t.Fatal("a non-string city answered")
	}
}

func TestToolFailureIsNotZero(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, "")
	f.serve("/v1/forecast", `{"error": true}`)
	out, err := callTool(t, a, "weather_now", "")
	if err == nil || out != "" {
		t.Fatalf("a failed read gave %q %v", out, err)
	}
	if !strings.Contains(err.Error(), "读不懂") {
		t.Fatalf("error %v", err)
	}
}
