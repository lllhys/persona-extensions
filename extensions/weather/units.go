package main

import (
	"fmt"
	"math"
)

// Units: Open-Meteo answers temperatures in °C and wind in km/h; the settings say what the page and tools show.

// temperatureUnit is the setting's value and what it stands for.
type temperatureUnit string

const (
	tempCelsius    temperatureUnit = "celsius"
	tempFahrenheit temperatureUnit = "fahrenheit"
)

// windUnit is the setting's value and what it stands for.
type windUnit string

const (
	windKmh windUnit = "kmh"
	windMs  windUnit = "ms"
)

// temp turns a temperature in °C into the unit of u, rounded to one decimal.
func (u temperatureUnit) temp(c float64) float64 {
	if u == tempFahrenheit {
		return round1(c*9/5 + 32)
	}
	return round1(c)
}

// tempLabel is the unit's short label ("°C").
func (u temperatureUnit) tempLabel() string {
	if u == tempFahrenheit {
		return "°F"
	}
	return "°C"
}

// wind turns a wind speed in km/h into the unit of u, rounded to one decimal.
func (u windUnit) wind(kmh float64) float64 {
	if u == windMs {
		return round1(kmh / 3.6)
	}
	return round1(kmh)
}

// windLabel is the unit's short label ("km/h").
func (u windUnit) windLabel() string {
	if u == windMs {
		return "m/s"
	}
	return "km/h"
}

// round1 rounds to one decimal place (what a weather number needs).
func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

// fmtTemp writes a temperature with its unit label, converted to it ("11°C", "51.8°F").
func fmtTemp(v float64, u temperatureUnit) string {
	return fmt.Sprintf("%s%s", trimZero(u.temp(v)), u.tempLabel())
}

// fmtWind writes a wind speed with its unit label, converted to it ("17.7 km/h", "4.9 m/s").
func fmtWind(v float64, u windUnit) string {
	return fmt.Sprintf("%s %s", trimZero(u.wind(v)), u.windLabel())
}

// trimZero writes a number without a pointless trailing ".0".
func trimZero(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}
