// The GitHub extension (nwverse/persona-extensions#26): the GitHub connector Persona's hub had built in, moved here, and a
// page of what needs the user on GitHub.
//
//   - connectors (id github): the inbox (review requests, @s, assignments, comments and reviews on the user's PRs
//     and issues, failing CI), the knowledge base's archive of the PRs and issues the user is part of (chats
//     github-<owner>-<repo>-<n>), the assistant's tools github_search / github_read / github_diff / github_checks and
//     the writes github_comment / github_review (through the hub's gate: confirmed on the phone), the thread page.
//     The sign-in is a computer's gh login (the hub asks that computer: connector.credentials.gh_token) or the device
//     flow, kept in the hub's sealed connector secret: the same id, so a hub that had its own connector hands its
//     sign-in, inbox, archive and switches over as they are.
//   - ui (nav_page github): the GitHub page (internal/page).
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
	"slices"
	"syscall"
	"time"

	"github.com/nwverse/persona-extensions/extensions/github/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/github/internal/github"
	"github.com/nwverse/persona-extensions/extensions/github/internal/page"
	"github.com/nwverse/persona-extensions/extensions/github/internal/serve"
	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

//go:embed persona-extension.json
var manifest []byte

// version is the manifest's: what the program answers to initialize, which the hub holds to the manifest.
var version = func() string {
	var m struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(manifest, &m)
	return m.Version
}()

// badgeEvery is how often the page's badge (PRs waiting for review) is read while the page is on.
const badgeEvery = 10 * time.Minute

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println(version)
			return
		default:
			fmt.Fprintln(os.Stderr, "usage: github [version]: started by the Persona hub, which talks to it over stdin / stdout")
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

// app holds the connector the hub started the program with and its page.
type app struct {
	page *page.Page
}

// program is the extension: the connector namespace (internal/serve) and the page, both on the one connector.
func program(a *app) ext.Program {
	prog := serve.Program("github", version, manifest, a.build)
	connectorInit := prog.Init
	prog.Init = func(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
		contributes, err := connectorInit(ctx, in, hub)
		if err != nil {
			return nil, err
		}
		if a.page != nil && slices.Contains(in.Active["ui"], page.ID) {
			go a.page.KeepBadge(ctx, hub, badgeEvery)
		}
		return contributes, nil
	}
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
	var s github.Settings
	if len(init.Config) > 0 && string(init.Config) != "null" {
		if err := json.Unmarshal(init.Config, &s); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
	}
	c := github.New(s.Config(), "", nil).WithRules(pluginkit.TriageRules(init.Config))
	c.UseSecrets(secrets)
	if init.GhToken != nil {
		c.UseHub(func(ctx context.Context, machine, host string) (string, error) {
			tok, err := init.GhToken(ctx, machine, host)
			return tok, hubError(err)
		})
	}
	a.page = page.New(c)
	return c, nil
}

// hubError is the hub's answer to connector.credentials.gh_token as the connector's errors, so its status says what
// to do as the hub's own connector did.
func hubError(err error) error {
	var e *ext.Error
	if err == nil || !errors.As(err, &e) {
		return err
	}
	switch e.Code {
	case "no_machine":
		return github.ErrNoMachine
	case "offline":
		return github.ErrOffline
	case "node_too_old":
		return github.ErrNodeTooOld
	case "no_gh":
		return github.ErrNoGh
	case "not_signed_in":
		return github.ErrNotSignedIn
	case wire.ErrorCodePermissionDenied:
		return github.ErrNotGranted
	case wire.ErrorCodeUnknownMethod:
		return github.ErrHubTooOld
	}
	return err
}
