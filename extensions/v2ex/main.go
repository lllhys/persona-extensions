// Command v2ex is a Persona extension for V2EX (https://www.v2ex.com), read only and without an account
// (docs/extensions/v2ex.md):
//
//   - ui (plugin_page "topics"): a page of its hot topics, latest topics and the user's nodes, filtered by the user's
//     interests; a topic opens as its text and its latest replies, read by the hub, so the phone needs no way to the site.
//   - connectors (id v2ex, tools): v2ex_hot and v2ex_topic, for the assistant.
//
// The hub's machine reads the site's public API v1.
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
	pageUI      = "topics"
	connectorID = "v2ex"
)

func main() { ext.Main(program(newApp())) }

func program(a *app) ext.Program {
	return ext.Program{
		ID: about.ID, Version: about.Version, Manifest: manifest,
		Init:       a.init,
		UI:         map[string]ext.Page{pageUI: {Render: a.render, Action: a.action}},
		Namespaces: map[string]ext.Handler{"connector": a.connector},
	}
}
