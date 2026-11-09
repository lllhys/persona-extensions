package main

import (
	"slices"
	"strings"
)

// Config is the extension's configuration (the manifest's form): the cities and the units.
type Config struct {
	Cities          []City `json:"cities,omitempty"`
	TemperatureUnit string `json:"temperature_unit,omitempty"`
	WindUnit        string `json:"wind_unit,omitempty"`
}

// City is one configured city: its name and where it is. Timezone is optional; Open-Meteo answers in the city's own
// timezone, and the timezone here is only for writing "读于" in the page and tools.
type City struct {
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timezone  string  `json:"timezone,omitempty"`
}

// defaultCity stands in when the setting is empty or every city in it is unusable.
var defaultCity = City{Name: "上海", Latitude: 31.22, Longitude: 121.46, Timezone: "Asia/Shanghai"}

// tempUnit reads the temperature unit setting, "celsius" when it is unset or unknown.
func (c Config) tempUnit() temperatureUnit {
	if c.TemperatureUnit == string(tempFahrenheit) {
		return tempFahrenheit
	}
	return tempCelsius
}

// windUnit reads the wind unit setting, "kmh" when it is unset or unknown.
func (c Config) windUnit() windUnit {
	if c.WindUnit == string(windMs) {
		return windMs
	}
	return windKmh
}

// cityValid says whether a city can be read: a name and a point on the earth.
func cityValid(c City) bool {
	return strings.TrimSpace(c.Name) != "" && c.Latitude >= -90 && c.Latitude <= 90 && c.Longitude >= -180 && c.Longitude <= 180
}

// cleanCities turns the setting into the cities to read: the valid ones (the first stays first, at most citiesMost,
// each name once) and those that cannot be. When nothing usable remains, the default city stands in.
func cleanCities(cs []City) (good, bad []City) {
	for _, c := range cs {
		c.Name = strings.TrimSpace(c.Name)
		switch {
		case !cityValid(c):
			bad = append(bad, c)
		case len(good) < citiesMost && !slices.ContainsFunc(good, func(g City) bool { return g.Name == c.Name }):
			good = append(good, c)
		}
	}
	if len(good) == 0 {
		good = []City{defaultCity}
	}
	return good, bad
}

// cityByName finds a city by name (exactly); the empty name is the default (the first).
func cityByName(cs []City, name string) (City, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		if len(cs) > 0 {
			return cs[0], true
		}
		return City{}, false
	}
	for _, c := range cs {
		if c.Name == name {
			return c, true
		}
	}
	return City{}, false
}
