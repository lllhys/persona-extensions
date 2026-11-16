// Command amap is a Persona extension for AMap's (高德地图) Web Service API, read only, with the user's own key
// (docs/extensions/amap.md): a connector whose tools let the assistant search places in China, see what is nearby,
// turn addresses into coordinates and back, plan routes (driving, walking, cycling, e-bike, public transport) with a
// taxi fare estimate, and read a Chinese city's weather.
//
// The tools take and give WGS-84 coordinates, like Persona's phone tools and saved places; AMap's GCJ-02 is
// converted at its edge (coord.go). The key is the extension secret api_key, checked before it is saved
// (settings.check) and once when the program starts.
package main

import (
	_ "embed"
	"encoding/json"

	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var authorManifest []byte

var manifest = pluginkit.PackagedManifest(authorManifest)

// about is the manifest's id and version.
var about = func() (m struct{ ID, Version string }) {
	_ = json.Unmarshal(manifest, &m)
	return m
}()

// The contributions' and the secret's ids (persona-extension.json).
const (
	connectorID = "amap"
	keySecret   = "api_key"
)

func main() { ext.Main(program(newApp())) }

func program(a *app) ext.Program {
	return ext.Program{
		ID: about.ID, Version: about.Version, Manifest: manifest,
		Init:       a.init,
		Namespaces: map[string]ext.Handler{"connector": a.connector, "settings": a.settings},
	}
}
