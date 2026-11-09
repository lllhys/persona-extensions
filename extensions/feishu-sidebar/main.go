// Command feishu-sidebar is a Persona extension (extensions/feishu-sidebar) that opens the user's Persona web from
// a Feishu sidebar (docs/extensions/feishu-sidebar.md):
//
//   - ui (nav_page "feishu-sidebar"): a page that shows the Feishu applink for the hub's public address and the
//     default path from the settings, to open in Feishu or copy; when the address is not set, it says what to do.
//   - connectors (id feishu, tools): feishu_sidebar_link, for the assistant.
//
// It builds the link locally from the hub's own address. It never calls Feishu and keeps no credentials.
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
	pageUI      = "feishu-sidebar"
	connectorID = "feishu"
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
