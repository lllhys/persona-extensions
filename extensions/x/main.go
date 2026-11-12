// The X extension (nwverse/persona#5363), two ways in, either one: the user's own X account through the official X
// API v2, with their own developer app's keys (pay-per-use credits, a daily post budget); or Grok, the xAI CLI grok on
// the hub machine signed in with the user's SuperGrok / X Premium+ subscription, run headless to search X (no X
// account sign-in, no X keys; internal/x/grok.go).
//
//   - connectors (id x): the inbox (mentions and replies to the user), the assistant's read tools x_digest / x_feed /
//     x_following / x_post / x_search, a fields sign-in with the four keys, kept in the hub's sealed connector secret. What it
//     reads (who the user follows, a few days of the home timeline or chosen lists, the mentions) stays in the
//     extension's data directory and is deleted after retention_days, or at once when the user signs out.
//   - ui (nav_page x): the organized digest (internal/page).
//   - assistant: the skills/x skill (a daily digest with the x_* tools).
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/nwverse/persona-extensions/extensions/x/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/x/internal/page"
	"github.com/nwverse/persona-extensions/extensions/x/internal/serve"
	"github.com/nwverse/persona-extensions/extensions/x/internal/x"
	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

//go:embed persona-extension.json
var authorManifest []byte

var manifest = pluginkit.PackagedManifest(authorManifest)

// version is the manifest's: what the program answers to initialize, which the hub holds to the manifest.
var version = func() string {
	var m struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(manifest, &m)
	return m.Version
}()

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println(version)
			return
		default:
			fmt.Fprintln(os.Stderr, "usage: x [version]: started by the Persona hub, which talks to it over stdin / stdout")
			os.Exit(2)
		}
	}
	log.SetFlags(0) // the hub prefixes each line it keeps
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := ext.Serve(ctx, os.Stdin, os.Stdout, program(&app{}))
	if err != nil && ctx.Err() == nil && !errors.Is(err, io.EOF) { // EOF: the hub went away
		log.Printf("stopped: %v", err)
		os.Exit(1)
	}
}

// app holds the connector's page once the hub started the program.
type app struct {
	base string // the API's base URL ("" is X's): tests point it at a fake
	// grok runs grok and findGrok finds it (nil: the real ones); tests set fakes.
	grok     x.GrokRunner
	findGrok func(string) string
	page     *page.Page
}

// program is the extension: the connector namespace (internal/serve) and the page, both on the one connector.
func program(a *app) ext.Program {
	prog := serve.Program(x.ID, version, manifest, a.build)
	prog.UI = map[string]ext.Page{page.ID: {Render: a.render, Action: a.action}}
	return prog
}

func (a *app) render(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	if a.page == nil {
		return nil, &ext.Error{Code: wire.ErrorCodeNotInitialized, Message: "not initialized"}
	}
	return a.page.Render(ctx, r)
}

func (a *app) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	if a.page == nil {
		return nil, &ext.Error{Code: wire.ErrorCodeNotInitialized, Message: "not initialized"}
	}
	return a.page.Action(ctx, r)
}

// build is the connector for what the hub started the program with.
func (a *app) build(init serve.Init, secrets connector.Secrets) (connector.Connector, error) {
	var s x.Settings
	if len(init.Config) > 0 && string(init.Config) != "null" {
		if err := json.Unmarshal(init.Config, &s); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
	}
	dir := init.DataDir
	if dir == "" {
		dir = ext.Env().Data
	}
	c := x.New(s, x.Options{Base: a.base, DataDir: dir, Locale: init.Locale, Timezone: init.Timezone,
		Grok: a.grok, FindGrok: a.findGrok}).WithRules(pluginkit.TriageRules(init.Config))
	c.UseSecrets(secrets)
	if hub := init.Hub; hub != nil {
		c.OnInboxChange(func() {
			_ = hub.Notify(serve.InPlugin(serve.EChanged), serve.ChangedEvent{Contribution: x.ID, Scope: string(connector.ScopeInbox)})
		})
	}
	a.page = page.New(c)
	return c, nil
}
