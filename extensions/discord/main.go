// Command discord is a Persona extension: Discord as a connector, through the user's own bot
// (docs/extensions/discord.md; the plan is docs/plans/plan-discord.md in nwverse/persona, tracked in
// nwverse/persona#917). It signs in with the bot's token, which the hub keeps encrypted; archives the channels,
// threads and forum posts the bot can read into the knowledge base over Discord's REST API (history_days back the
// first time, then what is new at each of the hub's polls); and gives the assistant read-only tools. It sends
// nothing.
//
// The hub starts it and talks the process protocol to it over stdin / stdout through Persona's official Go SDK.
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var manifest []byte

// version is the manifest's: what the program answers to initialize, which the hub holds to the manifest.
var version = func() string {
	var m struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(manifest, &m)
	return m.Version
}()

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println(version)
			return
		case "manifest": // ext.Main answers it
		default:
			fmt.Fprintln(os.Stderr, "usage: discord [version]: started by the Persona hub, which talks to it over stdin / stdout")
			os.Exit(2)
		}
	}
	log.SetFlags(0) // the hub prefixes each line it keeps
	ext.Main(newConn(discordEndpoints, log.Printf).program(version))
}
