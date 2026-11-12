package main

import (
	"context"
	"strings"
	"testing"
)

func TestAirOutlookPreservesPollenAndForwardAir(t *testing.T) {
	q, err := parseAir([]byte(readFixture(t, "air-outlook.json")))
	if err != nil {
		t.Fatal(err)
	}
	hours := q.outlook()
	if len(hours) != 3 {
		t.Fatalf("outlook has %d hours, want 3", len(hours))
	}
	if len(hours[0].Pollen) != 6 {
		t.Fatalf("pollen species %+v", hours[0].Pollen)
	}
	if hours[0].Pollen[0].Value != 0 || hours[0].Pollen[1].Value != 18.5 {
		t.Fatalf("zero and nonzero pollen were not preserved: %+v", hours[0].Pollen)
	}
	if hours[0].AQI == nil || *hours[0].AQI != 42 || hours[0].PM25 == nil || *hours[0].PM25 != 12.5 {
		t.Fatalf("hourly air data %+v", hours[0])
	}
	if len(hours[2].Pollen) != 0 || hours[2].AQI != nil {
		t.Fatalf("unavailable values became zero: %+v", hours[2])
	}
	if got := pollenLabel("ragweed"); got != "豚草" {
		t.Fatalf("pollen label %q", got)
	}
}

func TestPollenUnavailableIsNotZero(t *testing.T) {
	q, err := parseAir([]byte(readFixture(t, "air-unavailable.json")))
	if err != nil {
		t.Fatal(err)
	}
	hours := q.outlook()
	if len(hours) != 1 || len(hours[0].Pollen) != 0 {
		t.Fatalf("unexpected pollen %+v", hours)
	}
	f := newFakeSite(t)
	sample(t, f)
	f.serve("/v1/air-quality", readFixture(t, "air-unavailable.json"))
	a := testApp(t, f, "")
	out, err := callTool(t, a, "weather_pollen", `{"hours": 2}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "没有花粉数据") || !strings.Contains(out, "无数据不等于零") {
		t.Fatalf("missing pollen was not explained: %q", out)
	}
	v := draw(t, a, 3, nil)
	says(t, v, "花粉仅在欧洲花粉季提供", "无数据不等于浓度为零")
	saysNot(t, v, "0 grains/m³")
}

func TestPollenToolAndPage(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	f.serve("/v1/air-quality", readFixture(t, "air-outlook.json"))
	a := testApp(t, f, "")
	out, err := callTool(t, a, "weather_pollen", `{"hours": 2}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"桤木 0 grains/m³", "桦木 18.5 grains/m³", "AQI 42", "CAMS ENSEMBLE", "不作医疗或用药建议"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	v := draw(t, a, 3, nil)
	says(t, v, "花粉与空气质量趋势", "桦木=18.5 grains/m³", "Open-Meteo", "CAMS ENSEMBLE", "不代表浓度为零")
}

func TestWeatherAirKeepsCurrentRequest(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, "")
	if _, err := callTool(t, a, "weather_air", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.readAir(context.Background(), a.selected(pageState{}), cacheFor); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{airURL(a.selected(pageState{})), airOutlookURL(a.selected(pageState{}))} {
		if strings.Contains(path, " ") {
			t.Fatalf("unescaped URL %q", path)
		}
	}
	if strings.Contains(airURL(a.selected(pageState{})), "hourly=") || !strings.Contains(airOutlookURL(a.selected(pageState{})), "hourly=pm2_5") {
		t.Fatalf("current and forecast requests were mixed")
	}
}
