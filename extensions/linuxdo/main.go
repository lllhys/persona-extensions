// Command linuxdo is a Persona plugin for LINUX DO (https://linux.do), a Discourse community, read only and
// without an account (docs/extensions/linuxdo.md):
//
//   - ui (plugin_page "hot"): a page of its hot topics — hot, today's / this week's top topics, latest — filtered by the user's
//     interests (the plugin's settings); a topic opens in the page as its text and its best replies, read by the
//     hub, so the phone needs no way to the site.
//   - connectors (id linuxdo, tools): linuxdo_hot and linuxdo_topic, for the assistant.
//
// The hub's machine reads the site's public RSS lists and topic JSON; the site's JSON lists answer a server's
// address with a challenge, its RSS does not.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/nwverse/persona-extensions/pluginkit"
)

//go:embed persona-extension.json
var manifest []byte

// version is the manifest's.
var version = func() string {
	var m struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(manifest, &m)
	return m.Version
}()

func main() {
	log.SetFlags(0)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	p := newPlugin(log.Printf)
	err := pluginkit.Serve(ctx, os.Stdin, os.Stdout, pluginkit.Plugin{ID: "linuxdo", Version: version, Manifest: manifest,
		Contributes: contributes(), Init: func(ip pluginkit.InitParams, _ *pluginkit.Hub) error { return p.configure(ip) },
		Handlers: p.handlers()})
	if err != nil {
		log.Printf("stopped: %v", err)
	}
}

// contributes is the manifest's contributes, answered to initialize.
func contributes() map[string]any {
	var m struct {
		Contributes map[string]any `json:"contributes"`
	}
	_ = json.Unmarshal(manifest, &m)
	return m.Contributes
}
