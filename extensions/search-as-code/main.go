// Command search-as-code is the Persona Search-as-Code extension (docs/extensions/search-as-code.md):
// it compiles complex multi-step search requests into executable search pipelines over coding-agent session
// transcripts (Claude Code, Codex, and custom JSONL directories) and local repositories, recording step-by-step
// execution traces and reusable search recipes.
//
// The hub starts the program with no arguments and speaks the extension protocol on stdin/stdout.
// `search-as-code mcp` serves the same engine and saved recipes to the assistant as an MCP server on stdio.
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
	"strings"
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

const usage = `search-as-code: Executable Search-as-Code pipelines over coding-agent sessions and local repositories.

  search-as-code                   the extension (started by the Persona hub; protocol on stdin / stdout)
  search-as-code mcp [--data DIR]  the MCP server for the assistant on stdin / stdout (tools: sac_execute,
                                   sac_session_search, sac_recipes_list, sac_recipe_upsert, sac_recipe_delete)
  search-as-code manifest          print persona-extension.json

The MCP server finds its data in --data, else PERSONA_SEARCH_AS_CODE_DATA, else the directory the hub's
extension last used on this computer.
`

func main() {
	log.SetFlags(0)
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help", "help":
			fmt.Print(usage)
		case "manifest":
			_, _ = os.Stdout.Write(manifest)
		case "mcp":
			if err := runMCP(os.Args[2:]); err != nil {
				log.Fatal(err)
			}
		default:
			log.Fatalf("search-as-code: unknown command %q\n\n%s", os.Args[1], usage)
		}
		return
	}
	ext.Main(program(newApp("")))
}

type configSpec struct {
	SessionDirs  string `json:"session_dirs"`
	RepoDirs     string `json:"repo_dirs"`
	DefaultScope string `json:"default_scope"`
}

type app struct {
	mu    sync.Mutex
	store *store
	stop  chan struct{}
	now   func() time.Time
}

func newApp(dir string) *app {
	return &app{store: newStore(dir), now: time.Now}
}

func program(a *app) ext.Program {
	return ext.Program{
		ID: about.ID, Version: about.Version, Manifest: manifest, Init: a.init,
		UI: map[string]ext.Page{
			pageUI: {Render: a.render, Action: a.action},
			cardUI: {Render: a.card},
		},
	}
}

func (a *app) init(_ context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if dir := in.Info().DataDir; dir != "" && dir != a.store.dir {
		a.store = newStore(dir)
		rememberDataDir(dir)
	}
	var cfg configSpec
	if err := in.Config(&cfg); err == nil {
		_ = a.store.update(func(st *State, _ time.Time) error {
			if s := strings.TrimSpace(cfg.SessionDirs); s != "" {
				st.SessionDirs = splitDirs(s)
			}
			if s := strings.TrimSpace(cfg.RepoDirs); s != "" {
				st.RepoDirs = splitDirs(s)
			}
			if isValidScope(cfg.DefaultScope) {
				st.DefaultScope = cfg.DefaultScope
			}
			return nil
		})
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
	dir := fs.String("data", "", "the data directory (default: PERSONA_SEARCH_AS_CODE_DATA, else the hub extension's)")
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
