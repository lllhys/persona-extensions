// Showcase: every component of an extension's view, in every state, a page each (docs/extensions/showcase.md). For the people
// who write extensions to see what each looks like, and for each app (web, Android, iOS, Mac) to screenshot the same
// views: TestSnapshots writes every page to snapshots/, which the apps' screenshot tests draw.
//
// Local data only: no network, no secrets.
package main

import (
	_ "embed"
	"encoding/json"

	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var manifest []byte

func main() { ext.Main(program(newApp())) }

func program(a *app) ext.Program {
	var m struct{ ID, Version string }
	_ = json.Unmarshal(manifest, &m)
	return ext.Program{
		ID: m.ID, Version: m.Version, Manifest: manifest,
		Init: a.init,
		UI: map[string]ext.Page{
			pageUI:   {Render: a.render, Action: a.action},
			cardUI:   {Render: a.card},
			widgetUI: {Render: a.widget},
		},
		Namespaces: map[string]ext.Handler{"shortcut": a.shortcut},
	}
}

// The contributions' ids (persona-extension.json).
const (
	pageUI   = "showcase"
	cardUI   = "showcase-card"
	widgetUI = "showcase-widget"
)
