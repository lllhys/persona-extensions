// Command weather is a Persona extension for Open-Meteo's public weather, air-quality and geocoding APIs, read only and
// without an account (docs/extensions/weather.md):
//
//   - ui (nav_page "weather" and plugin_page "page"): the current weather (temperature, apparent temperature, wind,
//     humidity and today's high and low), the next 24 hours as a temperature line, the 7-day forecast, and one air
//     quality card; more than one configured city switches at the top, and a button refreshes.
//   - connectors (id weather, tools): weather_now, weather_forecast and weather_air, for the assistant.
//
// The hub's machine reads Open-Meteo (https://open-meteo.com), which needs no key.
package main

import (
	_ "embed"
	"encoding/json"

	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var manifest []byte

// about is the manifest's id and version.
var about = func() (m struct{ ID, Version string }) {
	_ = json.Unmarshal(manifest, &m)
	return m
}()

// The contributions' ids (persona-extension.json).
const (
	navUI       = "weather"
	pageUI      = "page"
	connectorID = "weather"
)

func main() { ext.Main(program(newApp())) }

func program(a *app) ext.Program {
	return ext.Program{
		ID: about.ID, Version: about.Version, Manifest: manifest,
		Init:       a.init,
		UI:         map[string]ext.Page{navUI: {Render: a.render, Action: a.action}, pageUI: {Render: a.render, Action: a.action}},
		Namespaces: map[string]ext.Handler{"connector": a.connector},
	}
}
