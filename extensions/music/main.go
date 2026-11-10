// Command music is a Persona extension for music services. It contributes one connector per source and one page that
// aggregates them:
//
//   - connectors: one per service (Apple Music today), each with its endpoints: the account provider (catalog,
//     library, playlists, history, writes), the local Mac provider (Music.app through a program on the selected Mac)
//     and the iPhone provider (a snapshot the assistant reads through the phone's tools).
//   - ui (nav_page "music" and plugin_page "page"): all sources together, or one at a time: now playing, recently
//     played, library playlists, stats, search and playback controls where the source has them.
//
// The program is a provider framework: a source implements the capabilities it has, and the page and tools adapt. The
// same binary runs as the program on a Mac (args "node") and answers the hub's nodes.call there.
package main

import (
	_ "embed"
	"encoding/json"
	"log"
	"os"

	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var manifest []byte

// about is the manifest's id and version.
var about = func() (m struct{ ID, Version string }) {
	if err := json.Unmarshal(manifest, &m); err != nil {
		panic(err)
	}
	return m
}()

// The contribution and endpoint ids. One connector is Apple Music; its endpoints are the Mac and the iPhone.
const (
	navUI          = "music"
	pageUI         = "page"
	appleMusicID   = "applemusic"
	appleMusicName = "Apple Music"
	macID          = "mac"
	iphoneID       = "iphone"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) > 1 && os.Args[1] == "node" {
		ext.Main(macProgram())
		return
	}
	ext.Main(program(newApp()))
}

func program(a *app) ext.Program {
	return ext.Program{
		ID: about.ID, Version: about.Version, Manifest: manifest,
		Init: a.init,
		UI: map[string]ext.Page{
			navUI:  {Render: a.render, Action: a.action, Image: a.image},
			pageUI: {Render: a.render, Action: a.action, Image: a.image},
		},
		Namespaces: map[string]ext.Handler{"connector": a.connector},
	}
}
