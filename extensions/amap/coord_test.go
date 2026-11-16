package main

import (
	"testing"
)

// Public landmarks whose WGS-84 position is well known, with the point AMap's geocoding gave for them on
// 2026-11-15 (GCJ-02).
var landmarks = []struct {
	name      string
	wgs, amap point
}{
	{"Taipei 101", point{25.033976, 121.564472}, point{25.030945, 121.568161}},
	{"Star Ferry pier, Tsim Sha Tsui", point{22.29375, 114.16846}, point{22.290810, 114.173827}},
}

func TestTheShiftMatchesAMap(t *testing.T) {
	for _, l := range landmarks {
		if d := metres(toGCJ(l.wgs), l.amap); d > 60 {
			t.Errorf("%s: converted %.0f m from AMap's point", l.name, d)
		}
		if d := metres(l.wgs, l.amap); d < 400 {
			t.Errorf("%s: unconverted only %.0f m from AMap's point, the test proves nothing", l.name, d)
		}
		if d := metres(toWGS(l.amap), l.wgs); d > 60 {
			t.Errorf("%s: AMap's point back in WGS-84 is %.0f m off", l.name, d)
		}
	}
}

func TestBackAndForthIsExact(t *testing.T) {
	for lat := 18.5; lat < 53; lat += 2.5 {
		for lng := 74.0; lng < 134; lng += 3 {
			p := point{lat, lng}
			if back := toWGS(toGCJ(p)); metres(back, p) > 0.05 {
				t.Errorf("%v: back at %v (%.3f m)", p, back, metres(back, p))
			}
		}
	}
}

func TestWhereAMapShifts(t *testing.T) {
	in := map[string]point{
		"Beijing": {39.9042, 116.4074}, "Urumqi": {43.8256, 87.6168}, "Lhasa": {29.6520, 91.1721},
		"Harbin": {45.8038, 126.5350}, "Haikou": {20.0440, 110.1999}, "Kashgar": {39.4704, 75.9898},
		"Hong Kong": {22.3193, 114.1694}, "Macau": {22.1987, 113.5439}, "Taipei": {25.0330, 121.5654},
		"Kaohsiung": {22.6273, 120.3014}, "Sanya": {18.2528, 109.5119},
	}
	for name, p := range in {
		if !shifted(p) {
			t.Errorf("%s is not shifted", name)
		}
	}
	out := map[string]point{
		"Tokyo": {35.6762, 139.6503}, "Seoul": {37.5665, 126.9780}, "Pyongyang": {39.0392, 125.7625},
		"Hanoi": {21.0278, 105.8342}, "Ulaanbaatar": {47.8864, 106.9057}, "Vladivostok": {43.1155, 131.8855},
		"New York": {40.7128, -74.0060}, "Sydney": {-33.8688, 151.2093}, "Singapore": {1.3521, 103.8198},
	}
	for name, p := range out {
		if shifted(p) || toGCJ(p) != p || toWGS(p) != p {
			t.Errorf("%s is shifted", name)
		}
	}
}

func TestAMapLocationIsLngLatInGCJ(t *testing.T) {
	p := landmarks[0].wgs
	s := amapLocation(p)
	g, ok := parseLngLat(s)
	if !ok || metres(g, toGCJ(p)) > 0.2 {
		t.Fatalf("amapLocation %q", s)
	}
	back, ok := parseAMapLocation(s)
	if !ok || metres(back, p) > 0.2 {
		t.Fatalf("parseAMapLocation(%q) = %v", s, back)
	}
	for _, bad := range []string{"", "116.4", "x,y", "200,10", "116.4,95", "0,0", "[]"} {
		if _, ok := parseAMapLocation(bad); ok {
			t.Errorf("%q read as a point", bad)
		}
	}
	if got := (point{39.9, 116.39}).String(); got != "39.900000,116.390000" {
		t.Errorf("String %q", got)
	}
}
