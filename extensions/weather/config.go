package main

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Config is the extension's configuration (the manifest's form): the cities and the units.
type Config struct {
	Cities          []cityEntry `json:"cities,omitempty"`
	TemperatureUnit string      `json:"temperature_unit,omitempty"`
	WindUnit        string      `json:"wind_unit,omitempty"`
}

// City is one configured city: its name and where it is. Timezone is optional; Open-Meteo answers in the city's own
// timezone, and the timezone here is only for writing "读于" in the page and tools. lookup: only the name was written,
// and its point comes from the geocoding search when it is first read (app.locate).
type City struct {
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timezone  string  `json:"timezone,omitempty"`
	lookup    bool
}

// cityEntry is one item of the cities setting. The form writes a string (the hub's forms take lists of strings only):
// a city's name, optionally followed by its "latitude,longitude" to pin an ambiguous name ("Springfield 39.80,-89.64").
// An object {name, latitude, longitude, timezone}, the shape before 0.3.0, is still read.
type cityEntry City

func (e *cityEntry) UnmarshalJSON(raw []byte) error {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		*e = cityEntry(cityFromText(s))
		return nil
	}
	var c struct {
		Name      string  `json:"name"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Timezone  string  `json:"timezone,omitempty"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return err
	}
	*e = cityEntry(City{Name: c.Name, Latitude: c.Latitude, Longitude: c.Longitude, Timezone: c.Timezone})
	return nil
}

// pinned is "<name> <latitude>,<longitude>".
var pinned = regexp.MustCompile(`^(.*\S)\s+(-?\d+(?:\.\d+)?)\s*,\s*(-?\d+(?:\.\d+)?)$`)

// cityFromText reads one written city: a name and its point, or a name to look up. The default city's own name needs no
// lookup.
func cityFromText(s string) City {
	s = strings.TrimSpace(s)
	if m := pinned.FindStringSubmatch(s); m != nil {
		lat, err1 := strconv.ParseFloat(m[2], 64)
		lon, err2 := strconv.ParseFloat(m[3], 64)
		if err1 == nil && err2 == nil {
			return City{Name: strings.TrimSpace(m[1]), Latitude: lat, Longitude: lon}
		}
	}
	if s == defaultCity.Name {
		return defaultCity
	}
	return City{Name: s, lookup: true}
}

// cities are the setting's cities as written.
func (c Config) cities() []City {
	out := make([]City, 0, len(c.Cities))
	for _, e := range c.Cities {
		out = append(out, City(e))
	}
	return out
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

// cityValid says whether a city can be read: a name and a point on the earth, or a name to look up.
func cityValid(c City) bool {
	if strings.TrimSpace(c.Name) == "" {
		return false
	}
	return c.lookup || c.Latitude >= -90 && c.Latitude <= 90 && c.Longitude >= -180 && c.Longitude <= 180
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
