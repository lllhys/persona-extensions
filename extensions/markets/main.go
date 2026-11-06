// Command markets is a Persona extension for market prices, read from sources behind one interface (source.go):
// OKX's public market API for cryptocurrencies, without an account, today; stock markets would be more sources
// (docs/extensions/markets.md):
//
//   - ui (nav_page "watchlist"): an entry on the apps' main navigation; the page lists the user's watchlist with last
//     price and change, searches every source, and opens an instrument as its figures, a price chart over a bar the
//     source offers and its volume. The watchlist (refs, "source:symbol") is kept in the extension's data directory
//     and edited in the page; the configuration only seeds it.
//   - connectors (id markets, tools): markets_quote, markets_search and markets_candles, for the assistant.
//
// The hub's machine reads the sources; the phone needs no way to them.
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
	err := pluginkit.Serve(ctx, os.Stdin, os.Stdout, pluginkit.Plugin{ID: "markets", Version: version, Manifest: manifest,
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
