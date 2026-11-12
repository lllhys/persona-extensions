// Command onepassword is the 1Password extension for Persona. With a 1Password service account token (the
// extension's secret, checked with 1Password before the hub saves it), or with op on one of the user's computers
// picked in its settings (node.go, run there as "onepassword node"), the connector "onepassword" gives the assistant
// three read-only tools: the vaults it can read, item search by title, tag or website, and one item's fields with
// their op:// references. No tool ever returns a secret value: item answers are turned into metadata at once (on that
// computer, for op), and everything leaving the program is redacted. As a secret provider (secrets.go) it gives
// Persona's manager, and only it, the value of an op:// reference a setting holds or the user approved for one Agent
// run.
package main

import (
	_ "embed"
	"encoding/json"
	"log"
	"os"

	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var authorManifest []byte

var manifest = pluginkit.PackagedManifest(authorManifest)

var about = func() (out struct{ ID, Version string }) {
	if err := json.Unmarshal(manifest, &out); err != nil {
		panic(err)
	}
	return out
}()

func main() {
	log.SetFlags(0)
	if len(os.Args) > 1 && os.Args[1] == "node" { // on the computer picked in the settings
		ext.Main(nodeProgram(newNodeApp()))
		return
	}
	ext.Main(program(newApp(connectSDK)))
}

// program leaves Manifest out: the hub starts it from its package, which has the manifest the hub reads.
func program(a *app) ext.Program {
	return ext.Program{ID: about.ID, Version: about.Version, Init: a.init,
		UI:         map[string]ext.Page{uiConnection: a.page()},
		Namespaces: map[string]ext.Handler{"connector": a.connector, "settings": a.settings, "secrets": a.secrets}}
}
