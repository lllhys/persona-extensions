// Command token-usage is the Token 用量 extension program. The hub runs it through ext.Main: it draws the page and
// the overview card, and a loop pulls each machine's token usage into the data directory the hub gives, where it is
// kept for 400 days. The assistant runs `token-usage mcp` on the same data.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/budget"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/hub"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/mcp"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/store"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/sync"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/ui"
)

//go:embed persona-extension.json
var authorManifest []byte

var manifest = pluginkit.PackagedManifest(authorManifest)

// usage is the help text. It must name every word of the manifest's mcp_servers requires list.
const usage = `token-usage: the Token 用量 extension program.

Usage:
  token-usage mcp [--data DIR]   serve the assistant's MCP tools on stdin and stdout
  token-usage manifest           print the extension manifest
  token-usage help               show this help

The data directory is --data, then PERSONA_TOKEN_USAGE_DATA, then the directory the running extension recorded,
then the default one under the user's configuration directory.
`

var errNotInitialized = errors.New("Token 用量：扩展还没有初始化")

func main() {
	log.SetFlags(0)
	handled, err := dispatch(os.Args[1:], os.Stdin, os.Stdout)
	if err != nil {
		log.Fatal(err)
	}
	if !handled {
		ext.Main(program())
	}
}

// dispatch runs the command line when it names a command of its own (help, mcp). Anything else, including none and
// `manifest`, belongs to ext.Main, which the hub uses; dispatch reports that with false.
func dispatch(args []string, in io.Reader, out io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, err := io.WriteString(out, usage)
		return true, err
	case "mcp":
		return true, runMCP(args[1:], in, out)
	}
	return false, nil
}

// runMCP serves the tools over in and out until in ends. It never writes anything but the budget tools' changes.
func runMCP(args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("token-usage mcp", flag.ContinueOnError)
	data := fs.String("data", "", "the data directory (default: the hub's directory, see token-usage help)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("token-usage mcp: unexpected argument %q", fs.Arg(0))
	}
	dir, err := store.Resolve(*data)
	if err != nil {
		return err
	}
	return mcp.Serve(context.Background(), in, out, store.Open(dir), budget.Open(dir), store.Zone(dir), time.Now)
}

// extension holds the app that Init creates. Requests are handled concurrently, so the pointer is atomic.
type extension struct {
	app atomic.Pointer[ui.App]
}

func (x *extension) current() (*ui.App, error) {
	if a := x.app.Load(); a != nil {
		return a, nil
	}
	return nil, errNotInitialized
}

// program is the extension for ext.Main. Init opens the store in the data directory the hub gives, records that
// directory and the hub's time zone for `token-usage mcp`, and starts the pull loop.
func program() ext.Program {
	var m struct{ ID, Version string }
	_ = json.Unmarshal(manifest, &m)
	var x extension
	return ext.Program{
		ID: m.ID, Version: m.Version, Manifest: manifest,
		Init: func(ctx context.Context, in *ext.Init, h *ext.Hub) (map[string]any, error) {
			dir := in.Info().DataDir
			st := store.Open(dir)
			bf := budget.Open(dir)
			store.Remember(dir)
			loc := zoneOf(in.Hub.Timezone)
			store.RememberZone(dir, loc.String())
			env := ext.Env()
			c := hub.NewFromHub(h, env.HubURL, env.HubToken)
			a := ui.New(st, bf, c, time.Now, func() *time.Location { return loc })
			x.app.Store(a)
			go sync.Loop(ctx, c, st, bf, loc, sync.Every)
			return nil, nil
		},
		UI: map[string]ext.Page{
			ui.PageUI: {
				Render: func(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
					a, err := x.current()
					if err != nil {
						return nil, err
					}
					return a.Page(ctx, r)
				},
				Action: func(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
					a, err := x.current()
					if err != nil {
						return nil, err
					}
					return a.Act(ctx, r)
				},
			},
			ui.CardUI: {
				Render: func(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
					a, err := x.current()
					if err != nil {
						return nil, err
					}
					return a.Card(r)
				},
			},
		},
	}
}

// zoneOf loads the hub's time zone, falling back to the local one when the name is empty or unknown.
func zoneOf(name string) *time.Location {
	if name == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return loc
}
