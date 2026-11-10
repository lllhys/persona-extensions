// Command spaces is the spaces extension program. The hub runs it through ext.Main: it draws the navigation page and
// the overview card over the data directory the hub gives. The assistant runs `spaces mcp` on the same data.
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

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/spaces/internal/kernel"
	"github.com/nwverse/persona-extensions/extensions/spaces/internal/mcp"
	"github.com/nwverse/persona-extensions/extensions/spaces/internal/store"
	"github.com/nwverse/persona-extensions/extensions/spaces/internal/ui"
)

//go:embed persona-extension.json
var manifest []byte

// watchEvery is how often the program asks the store whether another process changed the data file.
const watchEvery = 2 * time.Second

// usage is the help text. It must name every word of the manifest's mcp_servers requires list.
const usage = `spaces: the spaces extension program.

Usage:
  spaces mcp [--data DIR]   serve the assistant's MCP tools on stdin and stdout
  spaces manifest           print the extension manifest
  spaces help               show this help

The data directory is --data, then PERSONA_SPACES_DATA, then the directory the running extension recorded,
then the default one under the user's configuration directory.
`

// errNotInitialized answers a UI request that arrives before the hub initialized the program.
var errNotInitialized = errors.New("空间：扩展还没有初始化")

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

// dispatch runs the command line of the program when it names a command of its own (help, mcp). Any other command
// line, including none and `manifest`, belongs to ext.Main, which the hub uses; dispatch reports that with false.
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

// runMCP serves the MCP tools over in and out until in ends. It reads the data directory from the --data flag or
// the usual place, and it never writes anything but the answers.
func runMCP(args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("spaces mcp", flag.ContinueOnError)
	data := fs.String("data", "", "the data directory (default: the hub's directory, see spaces help)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("spaces mcp: unexpected argument %q", fs.Arg(0))
	}
	dir, err := store.DataDir(*data)
	if err != nil {
		return err
	}
	return mcp.Serve(context.Background(), in, out, store.Open(dir), time.Now)
}

// extension holds the app that Init creates. Requests are handled concurrently, so the pointer is atomic.
type extension struct {
	app atomic.Pointer[ui.App]
}

// current returns the app, or an error when Init has not run.
func (x *extension) current() (*ui.App, error) {
	if a := x.app.Load(); a != nil {
		return a, nil
	}
	return nil, errNotInitialized
}

// program is the extension for ext.Main. Init opens the store in the data directory the hub gives, records that
// directory for `spaces mcp`, and starts the watch loop; the UI handlers draw through the app of Init.
func program() ext.Program {
	var m struct{ ID, Version string }
	_ = json.Unmarshal(manifest, &m)
	var x extension
	return ext.Program{
		ID: m.ID, Version: m.Version, Manifest: manifest,
		Init: func(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
			dir := in.Info().DataDir
			st := store.Open(dir)
			store.Remember(dir)
			a := ui.New(st)
			a.SetHub(hub)
			x.app.Store(a)
			go watch(ctx, st, hub, watchEvery)
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
					return a.Card(ctx, r)
				},
			},
		},
	}
}

// watch asks st every interval whether another process changed the stored data and, when it did, asks the hub to
// draw the contributions again. It returns when ctx ends, so the program exits as soon as the hub ends it.
func watch(ctx context.Context, st kernel.Storage, hub ui.Notifier, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if st.Changed() {
				_ = hub.Invalidate("")
			}
		}
	}
}
