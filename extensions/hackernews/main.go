// Command hackernews is a Persona extension for Hacker News (https://news.ycombinator.com), read only and without an
// account (docs/extensions/hackernews.md):
//
//   - ui (plugin_page "stories"): the Top, New, Best, Ask and Show lists, filtered by the user's interests; a story
//     opens with its text, a link to the article and its top-level comments, read by the hub. The page's own text
//     follows the locale the hub passes (zh-Hans, en, ja).
//   - connectors (id hackernews, tools): hn_stories and hn_item, for the assistant.
//
// The hub's machine reads the public Firebase API (https://hacker-news.firebaseio.com/v0).
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
	pageUI      = "stories"
	connectorID = "hackernews"
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
