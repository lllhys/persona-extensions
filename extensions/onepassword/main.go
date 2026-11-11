// Command onepassword is the 1Password extension for Persona. With a 1Password service account token (the
// extension's secret, checked with 1Password before the hub saves it), the connector "onepassword" gives the
// assistant three read-only tools: the vaults the service account can read, item search by title, tag or website,
// and one item's fields with their op:// references. No tool ever returns a secret value: the official SDK's item
// answers are turned into metadata at once, and everything leaving the program is redacted.
package main

import (
	_ "embed"
	"encoding/json"
	"log"

	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var manifest []byte

var about = func() (out struct{ ID, Version string }) {
	if err := json.Unmarshal(manifest, &out); err != nil {
		panic(err)
	}
	return out
}()

func main() {
	log.SetFlags(0)
	ext.Main(program(newApp(connectSDK)))
}

// program leaves Manifest out: the hub starts it from its package, which has the manifest the hub reads.
func program(a *app) ext.Program {
	return ext.Program{ID: about.ID, Version: about.Version, Init: a.init,
		UI:         map[string]ext.Page{uiConnection: a.page()},
		Namespaces: map[string]ext.Handler{"connector": a.connector, "settings": a.settings}}
}
