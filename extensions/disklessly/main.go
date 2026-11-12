// Command disklessly is the Persona Disklessly Machine & Cold-Session Management extension
// (docs/extensions/disklessly.md): fleet disk watermarks, reclaimable developer caches, dry-run cleanup plans,
// safe execution with df-measured freed bytes, and coding-agent cold session tiering across local and SSH machines.
//
// The hub starts the program with no arguments and speaks the extension protocol to it. `disklessly mcp` serves
// the same fleet state and operations to the assistant as an MCP server on stdio.
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

	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var authorManifest []byte

var manifest = pluginkit.PackagedManifest(authorManifest)

var about = func() (m struct{ ID, Version string }) {
	if err := json.Unmarshal(manifest, &m); err != nil {
		panic(err)
	}
	return m
}()

const usage = `disklessly: Persona Disklessly machine disk watermark, cache cleanup and cold-session tiering extension.

  disklessly                   the extension (started by the Persona hub; protocol on stdin / stdout)
  disklessly mcp [--data DIR]  the MCP server for the assistant on stdin / stdout (tools:
                               disklessly_machines_status, disklessly_scan, disklessly_history,
                               disklessly_sessions_search, disklessly_sessions_status,
                               disklessly_clean, disklessly_machine_upsert, disklessly_machine_delete,
                               disklessly_sessions_offload, disklessly_sessions_restore)
  disklessly manifest          print persona-extension.json

The MCP server finds the data in --data, else PERSONA_DISKLESSLY_DATA, else the directory the hub's
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
			log.Fatalf("disklessly: unknown command %q\n\n%s", os.Args[1], usage)
		}
		return
	}
	ext.Main(program(newApp("")))
}

type app struct {
	mu     sync.Mutex
	store  *store
	runner Runner
	stop   chan struct{}
	now    func() time.Time
}

func newApp(dir string) *app {
	return &app{store: newStore(dir), runner: DefaultRunner, now: time.Now}
}

func program(a *app) ext.Program {
	return ext.Program{
		ID: about.ID, Version: about.Version, Manifest: manifest, Init: a.init,
		UI: map[string]ext.Page{
			pageUI:     {Render: a.render, Action: a.action},
			cardUI:     {Render: a.card},
			sessionsUI: {Render: a.renderSessions, Action: a.actionSessions},
		},
	}
}

func (a *app) init(_ context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if dir := in.Info().DataDir; dir != "" && dir != a.store.dir {
		home := a.store.homeDir
		a.store = newStore(dir)
		if home != "" {
			a.store.homeDir = home
		}
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

func (a *app) run() Runner {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runner == nil {
		return DefaultRunner
	}
	return a.runner
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
	dir := fs.String("data", "", "the data directory (default: PERSONA_DISKLESSLY_DATA, else the hub extension's)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	data, err := dataDir(*dir)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	return (&mcpServer{store: newStore(data), runner: DefaultRunner}).serve(ctx, os.Stdin, os.Stdout)
}
