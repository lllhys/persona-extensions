package main

import "testing"

func TestTemperatureConversion(t *testing.T) {
	cases := []struct {
		c    float64
		unit temperatureUnit
		want float64
	}{
		{0, tempCelsius, 0},
		{20, tempCelsius, 20},
		{0, tempFahrenheit, 32},
		{20, tempFahrenheit, 68},
		{-40, tempFahrenheit, -40},
		{36.6, tempCelsius, 36.6},
	}
	for _, c := range cases {
		if got := c.unit.temp(c.c); got != c.want {
			t.Errorf("%s of %v = %v, want %v", c.unit, c.c, got, c.want)
		}
	}
}

func TestWindConversion(t *testing.T) {
	cases := []struct {
		kmh  float64
		unit windUnit
		want float64
	}{
		{0, windKmh, 0},
		{18, windKmh, 18},
		{36, windMs, 10},
		{18, windMs, 5},
		{17.7, windMs, 4.9},
	}
	for _, c := range cases {
		if got := c.unit.wind(c.kmh); got != c.want {
			t.Errorf("%s of %v km/h = %v, want %v", c.unit, c.kmh, got, c.want)
		}
	}
}

func TestUnitLabelsAndFormatting(t *testing.T) {
	if tempCelsius.tempLabel() != "°C" || tempFahrenheit.tempLabel() != "°F" {
		t.Fatalf("temp labels %q %q", tempCelsius.tempLabel(), tempFahrenheit.tempLabel())
	}
	if windKmh.windLabel() != "km/h" || windMs.windLabel() != "m/s" {
		t.Fatalf("wind labels %q %q", windKmh.windLabel(), windMs.windLabel())
	}
	if got := fmtTemp(11, tempCelsius); got != "11°C" {
		t.Fatalf("fmtTemp = %q", got)
	}
	if got := fmtTemp(11.5, tempCelsius); got != "11.5°C" {
		t.Fatalf("fmtTemp = %q", got)
	}
	if got := fmtTemp(11, tempFahrenheit); got != "51.8°F" {
		t.Fatalf("fmtTemp = %q", got)
	}
	if got := fmtWind(17.7, windKmh); got != "17.7 km/h" {
		t.Fatalf("fmtWind = %q", got)
	}
	if got := fmtWind(18, windMs); got != "5 m/s" {
		t.Fatalf("fmtWind = %q", got)
	}
}

func TestTrimZero(t *testing.T) {
	for in, want := range map[float64]string{0: "0", 1: "1", 1.5: "1.5", 14.3: "14.3", -2: "-2"} {
		if got := trimZero(in); got != want {
			t.Errorf("trimZero(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestConfigUnits(t *testing.T) {
	if (Config{}).tempUnit() != tempCelsius || (Config{}).windUnit() != windKmh {
		t.Fatalf("empty config units")
	}
	if (Config{TemperatureUnit: "fahrenheit"}).tempUnit() != tempFahrenheit {
		t.Fatal("fahrenheit not read")
	}
	if (Config{WindUnit: "ms"}).windUnit() != windMs {
		t.Fatal("ms not read")
	}
	if (Config{TemperatureUnit: "bogus"}).tempUnit() != tempCelsius {
		t.Fatal("bogus temperature unit should fall back")
	}
}
