package main

import "testing"

func TestCityValid(t *testing.T) {
	valid := []City{
		{Name: "上海", Latitude: 31.22, Longitude: 121.46},
		{Name: "北京", Latitude: -90, Longitude: 180},
		{Name: "x", Latitude: 90, Longitude: -180},
	}
	for _, c := range valid {
		if !cityValid(c) {
			t.Errorf("cityValid(%+v) = false", c)
		}
	}
	invalid := []City{
		{Name: "", Latitude: 31, Longitude: 121},
		{Name: "  ", Latitude: 31, Longitude: 121},
		{Name: "a", Latitude: 91, Longitude: 0},
		{Name: "a", Latitude: -91, Longitude: 0},
		{Name: "a", Latitude: 0, Longitude: 181},
		{Name: "a", Latitude: 0, Longitude: -181},
	}
	for _, c := range invalid {
		if cityValid(c) {
			t.Errorf("cityValid(%+v) = true", c)
		}
	}
}

func TestCleanCities(t *testing.T) {
	good, bad := cleanCities([]City{{Name: " 北京 ", Latitude: 39.9, Longitude: 116.4}, {Name: "上海", Latitude: 31.22, Longitude: 121.46}})
	if len(good) != 2 || good[0].Name != "北京" || good[1].Name != "上海" {
		t.Fatalf("good %+v", good)
	}
	if len(bad) != 0 {
		t.Fatalf("bad %+v", bad)
	}

	// The first usable one stays first; unusable ones are bad; duplicates are dropped.
	good, bad = cleanCities([]City{{Name: "", Latitude: 1, Longitude: 1}, {Name: "上海", Latitude: 200, Longitude: 121.46},
		{Name: "北京", Latitude: 39.9, Longitude: 116.4}, {Name: "北京", Latitude: 39.9, Longitude: 116.4}})
	if len(good) != 1 || good[0].Name != "北京" || len(bad) != 2 {
		t.Fatalf("good %+v bad %+v", good, bad)
	}

	// Nothing usable stands in as the default city.
	good, bad = cleanCities(nil)
	if len(good) != 1 || good[0] != defaultCity || len(bad) != 0 {
		t.Fatalf("empty setting: good %+v bad %+v", good, bad)
	}

	// At most citiesMost are kept.
	var many []City
	for i := 0; i < citiesMost+3; i++ {
		many = append(many, City{Name: string(rune('A' + i)), Latitude: 1, Longitude: 1})
	}
	good, _ = cleanCities(many)
	if len(good) != citiesMost {
		t.Fatalf("kept %d, want %d", len(good), citiesMost)
	}
}

func TestCityByName(t *testing.T) {
	cs := []City{{Name: "上海", Latitude: 31.22, Longitude: 121.46}, {Name: "北京", Latitude: 39.9, Longitude: 116.4}}
	if c, ok := cityByName(cs, "北京"); !ok || c.Name != "北京" {
		t.Fatalf("cityByName 北京: %+v %v", c, ok)
	}
	if c, ok := cityByName(cs, " 上海 "); !ok || c.Name != "上海" {
		t.Fatalf("cityByName trimmed: %+v %v", c, ok)
	}
	if c, ok := cityByName(cs, ""); !ok || c.Name != "上海" {
		t.Fatalf("default city: %+v %v", c, ok)
	}
	if _, ok := cityByName(cs, "广州"); ok {
		t.Fatal("an unknown name answered")
	}
	if _, ok := cityByName(nil, ""); ok {
		t.Fatal("no cities still answers")
	}
}

func TestInitReadsConfigAndFallsBack(t *testing.T) {
	f := newFakeSite(t)
	a := testApp(t, f, `{"cities": [{"name": "北京", "latitude": 39.9, "longitude": 116.4}], "temperature_unit": "fahrenheit", "wind_unit": "ms"}`)
	if len(a.cities) != 1 || a.cities[0].Name != "北京" || len(a.badCities) != 0 {
		t.Fatalf("cities %+v bad %+v", a.cities, a.badCities)
	}
	if a.cfg.tempUnit() != tempFahrenheit || a.cfg.windUnit() != windMs {
		t.Fatalf("units %v %v", a.cfg.tempUnit(), a.cfg.windUnit())
	}

	b := testApp(t, f, `{"cities": [{"name": "", "latitude": 999, "longitude": 999}]}`)
	if len(b.cities) != 1 || b.cities[0] != defaultCity || len(b.badCities) != 1 {
		t.Fatalf("fallback cities %+v bad %+v", b.cities, b.badCities)
	}
}

func TestWindDir(t *testing.T) {
	cases := map[float64]string{0: "北风", 45: "东北风", 90: "东风", 135: "东南风", 180: "南风", 225: "西南风", 270: "西风", 315: "西北风", 337.5: "北风", 22: "北风"}
	for deg, want := range cases {
		if got := windDir(deg); got != want {
			t.Errorf("windDir(%v) = %q, want %q", deg, got, want)
		}
	}
}
