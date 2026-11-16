package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Coordinates. AMap reads and writes GCJ-02 inside China; Persona (the phone's own fix, Apple Maps, saved places) uses
// WGS-84 everywhere, mainland China included. The tools take and give WGS-84, and the program converts at AMap's
// edge, here, without asking AMap's own conversion endpoint (which spends quota and converts one way only).
//
// Checked against AMap on 2026-11-15: its points in mainland China, Hong Kong and Taiwan are all GCJ-02 (converted
// public landmarks land 14–47 m from AMap's point, unconverted ones 500–640 m away), so the shift applies to all of
// them. Outside China AMap gives plain WGS-84.

// point is a latitude and longitude in degrees.
type point struct{ Lat, Lng float64 }

// The Krasovsky 1940 ellipsoid the GCJ-02 shift is defined on.
const (
	gcjA  = 6378245.0
	gcjEE = 0.00669342162296594323
)

// box is a latitude/longitude rectangle.
type box struct{ minLng, minLat, maxLng, maxLat float64 }

func (b box) has(p point) bool {
	return p.Lng >= b.minLng && p.Lng <= b.maxLng && p.Lat >= b.minLat && p.Lat <= b.maxLat
}

// chinaBoxes cover mainland China, Hong Kong, Macau and Taiwan (where AMap shifts), coarsely; notChinaBoxes cut out
// the neighbours they overlap most (northern Vietnam and Laos, Mongolia and the Russian Far East).
var (
	chinaBoxes = []box{
		{79.4462, 42.8899, 96.33, 49.2204},
		{109.6872, 39.3742, 135.0002, 54.1415},
		{73.1246, 29.5297, 124.143255, 42.8899},
		{82.9684, 26.7186, 97.0352, 29.5297},
		{97.0253, 20.4986, 124.367395, 29.5297},
		{107.975793, 17.871542, 111.744104, 20.4986},
		{119.3, 21.8, 122.1, 25.4}, // Taiwan and Penghu
	}
	notChinaBoxes = []box{
		{101.865234, 20.098899, 106.665039, 22.284305},
		{106.452637, 20.487448, 108.051758, 21.542627},
		{109.032715, 50.325122, 119.127197, 55.817310},
		{127.456055, 49.557533, 137.022705, 55.817310},
		{131.266479, 42.569264, 137.022705, 44.892162},
	}
)

// shifted reports whether AMap uses GCJ-02 at p.
func shifted(p point) bool {
	for _, b := range notChinaBoxes {
		if b.has(p) {
			return false
		}
	}
	for _, b := range chinaBoxes {
		if b.has(p) {
			return true
		}
	}
	return false
}

func gcjLat(x, y float64) float64 {
	r := -100.0 + 2.0*x + 3.0*y + 0.2*y*y + 0.1*x*y + 0.2*math.Sqrt(math.Abs(x))
	r += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	r += (20.0*math.Sin(y*math.Pi) + 40.0*math.Sin(y/3.0*math.Pi)) * 2.0 / 3.0
	r += (160.0*math.Sin(y/12.0*math.Pi) + 320*math.Sin(y*math.Pi/30.0)) * 2.0 / 3.0
	return r
}

func gcjLng(x, y float64) float64 {
	r := 300.0 + x + 2.0*y + 0.1*x*x + 0.1*x*y + 0.1*math.Sqrt(math.Abs(x))
	r += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	r += (20.0*math.Sin(x*math.Pi) + 40.0*math.Sin(x/3.0*math.Pi)) * 2.0 / 3.0
	r += (150.0*math.Sin(x/12.0*math.Pi) + 300.0*math.Sin(x/30.0*math.Pi)) * 2.0 / 3.0
	return r
}

// offset is GCJ-02 minus WGS-84 at the WGS-84 point p.
func offset(p point) point {
	dLat := gcjLat(p.Lng-105.0, p.Lat-35.0)
	dLng := gcjLng(p.Lng-105.0, p.Lat-35.0)
	rad := p.Lat / 180.0 * math.Pi
	m := math.Sin(rad)
	m = 1 - gcjEE*m*m
	s := math.Sqrt(m)
	dLat = dLat * 180.0 / ((gcjA * (1 - gcjEE)) / (m * s) * math.Pi)
	dLng = dLng * 180.0 / (gcjA / s * math.Cos(rad) * math.Pi)
	return point{dLat, dLng}
}

// toGCJ is the WGS-84 point p as AMap reads it.
func toGCJ(p point) point {
	if !shifted(p) {
		return p
	}
	d := offset(p)
	return point{p.Lat + d.Lat, p.Lng + d.Lng}
}

// toWGS is AMap's point g in WGS-84: the shift has no closed inverse, so the WGS-84 point whose shift lands on g is
// found by iteration (well under a metre after a few rounds).
func toWGS(g point) point {
	if !shifted(g) {
		return g
	}
	w := g
	for range 10 {
		s := toGCJ(w)
		dLat, dLng := g.Lat-s.Lat, g.Lng-s.Lng
		w = point{w.Lat + dLat, w.Lng + dLng}
		if math.Abs(dLat) < 1e-8 && math.Abs(dLng) < 1e-8 {
			break
		}
	}
	return w
}

// amapLocation writes the WGS-84 point p as AMap's "lng,lat" in GCJ-02, with AMap's six decimals.
func amapLocation(p point) string {
	g := toGCJ(p)
	return strconv.FormatFloat(g.Lng, 'f', 6, 64) + "," + strconv.FormatFloat(g.Lat, 'f', 6, 64)
}

// parseAMapLocation reads AMap's "lng,lat" (GCJ-02) as a WGS-84 point.
func parseAMapLocation(s string) (point, bool) {
	g, ok := parseLngLat(s)
	if !ok {
		return point{}, false
	}
	return toWGS(g), true
}

// parseLngLat reads "lng,lat" as it is, without conversion.
func parseLngLat(s string) (point, bool) {
	lng, lat, ok := strings.Cut(strings.TrimSpace(s), ",")
	if !ok {
		return point{}, false
	}
	x, err1 := strconv.ParseFloat(strings.TrimSpace(lng), 64)
	y, err2 := strconv.ParseFloat(strings.TrimSpace(lat), 64)
	p := point{y, x}
	if err1 != nil || err2 != nil || !p.valid() {
		return point{}, false
	}
	return p, true
}

func (p point) valid() bool {
	return !math.IsNaN(p.Lat) && !math.IsNaN(p.Lng) && p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180 &&
		!(p.Lat == 0 && p.Lng == 0)
}

// String is how the tools write a point: "纬度,经度" with six decimals (WGS-84).
func (p point) String() string { return fmt.Sprintf("%.6f,%.6f", p.Lat, p.Lng) }

// metres is the great-circle distance between two points.
func metres(a, b point) float64 {
	const r = 6371000.0
	f1, f2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	df, dl := f2-f1, (b.Lng-a.Lng)*math.Pi/180
	h := math.Sin(df/2)*math.Sin(df/2) + math.Cos(f1)*math.Cos(f2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(h)))
}
