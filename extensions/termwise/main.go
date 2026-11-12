// Command termwise is a Persona extension with one reading page and two modes.
//
// Translation mode keeps the sample article and adds a translation under each block.
// Vocabulary mode leaves that text in place and underlines vocabulary candidates; choosing
// one shows the gloss and can remember the word. The choice and the remembered words are
// stored in the extension data directory.
//
// `termwise preview` serves the same reader over HTTP so the underlines can be used in a
// browser. The hub starts the program with no arguments and speaks the extension protocol.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var authorManifest []byte

var manifest = pluginkit.PackagedManifest(authorManifest)

var about = func() (m struct{ ID, Version string }) {
	_ = json.Unmarshal(manifest, &m)
	return m
}()

const pageUI = "read"

func main() {
	log.SetFlags(0)
	if len(os.Args) > 1 && os.Args[1] == "preview" {
		if err := runPreview(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	ext.Main(program(newApp(log.Printf)))
}

func program(a *app) ext.Program {
	return ext.Program{
		ID: about.ID, Version: about.Version, Manifest: manifest,
		Init: a.init,
		UI:   map[string]ext.Page{pageUI: {Render: a.render, Action: a.action}},
	}
}

func runPreview(args []string) error {
	fs := flag.NewFlagSet("preview", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8731", "address to listen on")
	dir := fs.String("data", "", "directory for the reading state (default: a temp dir)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	data := *dir
	if data == "" {
		var err error
		data, err = os.MkdirTemp("", "termwise-preview-")
		if err != nil {
			return err
		}
	}
	a := newApp(log.Printf)
	a.store = openStore(data, log.Printf)
	mux := http.NewServeMux()
	mux.Handle("/", a.previewHandler())
	srv := &http.Server{Addr: *addr, Handler: mux}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	fmt.Fprintf(os.Stderr, "termwise preview at http://%s (state %s)\n", *addr, data)
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
