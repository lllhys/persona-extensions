// Command raindrop is a Persona extension for the user's personal Raindrop library.
// Only api.raindrop.io receives the encrypted extension token; saved bookmarks retain their public links.
package main

import (
	_ "embed"
	"encoding/json"
	"log"

	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var manifest []byte

var about = func() (m struct{ ID, Version string }) {
	if err := json.Unmarshal(manifest, &m); err != nil {
		panic(err)
	}
	return m
}()

const (
	connectorID = "raindrop"
	pageUI      = "bookmarks"
)

func main() {
	log.SetFlags(0)
	ext.Main(program(newApp()))
}

func program(a *app) ext.Program {
	return ext.Program{
		ID: about.ID, Version: about.Version, Manifest: manifest,
		Init:       a.init,
		UI:         map[string]ext.Page{pageUI: {Render: a.render, Action: a.action}},
		Namespaces: map[string]ext.Handler{"connector": a.connector},
	}
}
