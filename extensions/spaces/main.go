// Command spaces is the Persona Spaces extension (docs/extensions/spaces.md): spaces whose types, fields and
// relations the user or the assistant defines, kept in one store and drawn from that definition.
//
// The hub starts the program with no arguments and speaks the extension protocol to it. `spaces mcp` serves the
// same data to the assistant as an MCP server on stdio.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

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

const usage = `spaces: Persona Spaces, user-defined spaces of typed entities and relations.

  spaces                   the extension (started by the Persona hub; protocol on stdin / stdout)
  spaces mcp [--data DIR]  the MCP server for the assistant on stdin / stdout (tools: space_list,
                           space_schema_get, space_query, space_upsert, space_schema_upsert,
                           space_entity_upsert, space_entity_delete)
  spaces manifest          print persona-extension.json

The MCP server finds the data in --data, else PERSONA_SPACES_DATA, else the directory the hub's
extension last used on this computer.
`

func main() {
	log.SetFlags(0)
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help", "help":
			fmt.Print(usage)
		case "manifest":
			os.Stdout.Write(manifest)
		case "mcp":
			if err := runMCP(os.Args[2:]); err != nil {
				log.Fatal(err)
			}
		default:
			log.Fatalf("spaces: unknown command %q\n\n%s", os.Args[1], usage)
		}
		return
	}
	ext.Main(program(newApp("")))
}

type app struct {
	mu    sync.Mutex
	store *store
	stop  chan struct{}
	now   func() time.Time
}

func newApp(dir string) *app { return &app{store: newStore(dir), now: time.Now} }

func program(a *app) ext.Program {
	return ext.Program{
		ID: about.ID, Version: about.Version, Manifest: manifest, Init: a.init,
		UI: map[string]ext.Page{
			pageUI: {Render: a.render, Action: a.action},
			cardUI: {Render: a.card},
		},
	}
}

// init opens the store in the extension's data directory, tells `spaces mcp` where it is, and watches it so a
// change the assistant makes draws the pages again.
func (a *app) init(_ context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if dir := in.Info().DataDir; dir != "" && dir != a.store.dir {
		a.store = newStore(dir)
		rememberDataDir(dir)
	}
	if a.stop != nil {
		close(a.stop)
	}
	a.stop = make(chan struct{})
	go watch(a.store, hub, a.stop)
	return nil, nil
}

func (a *app) db() *store {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.store
}

func watch(s *store, hub *ext.Hub, stop chan struct{}) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			if s.changed() && hub != nil {
				_ = hub.Invalidate("")
			}
		}
	}
}

func runMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	dir := fs.String("data", "", "the data directory (default: PERSONA_SPACES_DATA, else the hub extension's)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	data, err := dataDir(*dir)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	return (&mcpServer{store: newStore(data)}).serve(ctx, os.Stdin, os.Stdout)
}
