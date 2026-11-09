package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The geocoding search (geocoding-api.open-meteo.com/v1/search): a city's name to its point and timezone.

// geoResult is one hit of a search.
type geoResult struct {
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timezone  string  `json:"timezone"`
	Country   string  `json:"country"`
	Admin1    string  `json:"admin1"`
}

// geoAnswer is a search's whole answer.
type geoAnswer struct {
	Results []geoResult `json:"results"`
}

// parseGeo reads a search answer.
func parseGeo(raw []byte) (geoAnswer, error) {
	var g geoAnswer
	if err := json.Unmarshal(raw, &g); err != nil {
		return geoAnswer{}, fmt.Errorf("%w：%v", errBadAnswer, err)
	}
	return g, nil
}

// best is the first usable hit; an answer with none says errNoCity.
func (g geoAnswer) best() (City, error) {
	for _, r := range g.Results {
		if strings.TrimSpace(r.Name) == "" || r.Latitude < -90 || r.Latitude > 90 || r.Longitude < -180 || r.Longitude > 180 {
			continue
		}
		return City{Name: r.Name, Latitude: r.Latitude, Longitude: r.Longitude, Timezone: r.Timezone}, nil
	}
	return City{}, errNoCity
}
