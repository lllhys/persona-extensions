package main

import (
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

func TestDefaultPage(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, "")
	v := draw(t, a, 3, nil)
	says(t, v,
		"## 上海",
		"[metric]",
		"⛅ 11°C", "局部多云", "现在",
		"{体感=7°C}", "{湿度=63%}", "{西北风=17.7 km/h}", "{今天=8.2°C / 15°C}",
		"## 未来 24 小时", "series:气温[24]",
		"## 7 天预报", "周一", "15°C / 8.2°C", "降水概率 79%",
		"## 空气质量", "空气质量指数=49 · 一般", "PM2.5=14.3", "PM10=14.4",
		"刷新")
	saysNot(t, v, "segmented", "读不懂", "0°C")
}

func TestFahrenheitAndMs(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, `{"temperature_unit": "fahrenheit", "wind_unit": "ms"}`)
	v := draw(t, a, 3, nil)
	says(t, v, "⛅ 51.8°F", "{体感=44.6°F}", "{西北风=4.9 m/s}")
	saysNot(t, v, "°C", "km/h")
}

func TestMultipleCitiesSwitch(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, `{"cities": [{"name": "上海", "latitude": 31.22, "longitude": 121.46}, {"name": "北京", "latitude": 39.9, "longitude": 116.4}]}`)
	v := draw(t, a, 3, nil)
	says(t, v, "segmented", "<上海:上海>", "<北京:北京>", "## 上海")
	if st := stateOf(t, v.State); st.City != "" {
		t.Fatalf("state %+v", st)
	}
	v = tap(t, a, 3, v, "city", map[string]string{"city": "北京"})
	says(t, v, "## 北京")
	saysNot(t, v, "## 上海")
	if st := stateOf(t, v.State); st.City != "北京" {
		t.Fatalf("state %+v", st)
	}
	// A city not in the setting leaves the choice unchanged.
	v = tap(t, a, 3, v, "city", map[string]string{"city": "广州"})
	says(t, v, "## 北京")
}

// Before UI version 2 the picker is rows, not a segmented control.
func TestPickerBeforeUIVersion2(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, `{"cities": [{"name": "上海", "latitude": 31.22, "longitude": 121.46}, {"name": "北京", "latitude": 39.9, "longitude": 116.4}]}`)
	v := draw(t, a, 1, nil)
	saysNot(t, v, "segmented")
	says(t, v, "## 城市")
	if shanghai, beijing := findRow(v, "上海"), findRow(v, "北京"); shanghai == nil || shanghai.Badge == nil || beijing == nil || beijing.Badge != nil {
		t.Fatalf("the rows: %+v %+v", shanghai, beijing)
	}
}

func TestErrorIsNotZero(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, "")
	f.serve("/v1/forecast", `{"error": true}`)
	v := draw(t, a, 3, nil)
	says(t, v, "读不懂", "重试", "→refresh")
	saysNot(t, v, "未来 24 小时", "7 天预报", "11°C", "0°C")

	// An HTTP failure says the status too.
	g := newFakeSite(t)
	sample(t, g)
	g.put("/v1/forecast", answer{status: 502, body: "bad gateway"})
	b := testApp(t, g, "")
	v = draw(t, b, 3, nil)
	says(t, v, "HTTP 502", "重试")
}

func TestAirFailureKeepsForecast(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, "")
	f.serve("/v1/air-quality", `{"error": true}`)
	v := draw(t, a, 3, nil)
	says(t, v, "## 未来 24 小时", "## 7 天预报", "## 空气质量", "读不懂")
	saysNot(t, v, "空气质量指数=")
}

func TestBadCitiesNote(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, `{"cities": [{"name": "", "latitude": 999, "longitude": 999}]}`)
	v := draw(t, a, 3, nil)
	says(t, v, "这些城市没填对", "## 上海")
}

func TestRefreshRereads(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, "")
	v := draw(t, a, 3, nil)
	if n := f.hit("/v1/forecast"); n != 1 {
		t.Fatalf("first draw read forecast %d times", n)
	}
	// A second draw within 10 minutes answers from the cache.
	draw(t, a, 3, nil)
	if n := f.hit("/v1/forecast"); n != 1 {
		t.Fatalf("cached draw read forecast %d times", n)
	}
	// A refresh reads again.
	v = tap(t, a, 3, v, "refresh", nil)
	if n := f.hit("/v1/forecast"); n != 2 {
		t.Fatalf("refresh read forecast %d times", n)
	}
}

// The chart points carry the chosen unit and the next 24 hours' labels.
func TestChartPoints(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, "")
	v := draw(t, a, 3, nil)
	var chart *wire.Component
	for i := range v.Sections {
		for j := range v.Sections[i].Items {
			if c := &v.Sections[i].Items[j]; c.Type == wire.ComponentChart {
				chart = c
			}
		}
	}
	if chart == nil || chart.Kind != wire.ChartKindLine || chart.Unit != "°C" || len(chart.Series) != 1 || len(chart.Series[0].Points) != 24 {
		t.Fatalf("chart %+v", chart)
	}
	if got := chart.Series[0].Points[0]; got.Label != "17时" || got.Value != 11.3 {
		t.Fatalf("first point %+v", got)
	}
	if chart.Series[0].Points[1].Label != "18时" {
		t.Fatalf("second point label %q", chart.Series[0].Points[1].Label)
	}
}
