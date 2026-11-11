// Command household is the household extension program (docs/extensions/household.md). The hub runs it through ext.Main:
// it draws the 家庭管家 page and the overview card, keeps the badge on the navigation entry up to date and sends the
// daily reminder, over the data directory the hub gives. The assistant runs `household mcp` on the same data.
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
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/datadir"
	"github.com/nwverse/persona-extensions/extensions/household/internal/mcp"
	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/remind"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
	"github.com/nwverse/persona-extensions/extensions/household/internal/ui"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/store"
)

//go:embed persona-extension.json
var manifest []byte

const (
	pageUI = "household"      // the nav_page contribution, which also carries the badge
	cardUI = "household-card" // the overview card

	// firstRoundAfter is the delay before the first round. The hub answers the initialize request after Init returns,
	// so a badge sent earlier could arrive before the hub is ready for events.
	firstRoundAfter = time.Second
	// checkEvery is how often the program asks the store whether another process changed the records.
	checkEvery = 2 * time.Second
	// remindEvery is how often the badge is set and the reminder checked, besides the changes.
	remindEvery = 10 * time.Minute
)

// usage is the help text. It must name every word of the manifest's mcp_servers requires list.
const usage = `household: the 家庭管家 extension program.

Usage:
  household mcp [--data DIR]   serve the assistant's MCP tools on stdin and stdout
  household manifest           print the extension manifest
  household help               show this help

The data directory is --data, then PERSONA_HOUSEHOLD_DATA, then the directory the running extension recorded,
then the default one under the user's configuration directory.
`

// errNotInitialized answers a UI request that arrives before the hub initialized the program.
var errNotInitialized = errors.New("家庭管家：扩展还没有初始化")

// config is the extension's configuration (the manifest's form, defaults filled in by the hub).
type config struct {
	Remind     string `json:"remind"`
	RemindTime string `json:"remind_time"`
}

func main() {
	log.SetFlags(0) // to stderr: stdout is the protocol
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

// runMCP serves the MCP tools over in and out until in ends. It finds the data directory from the --data flag or the
// usual place, and it never writes anything but the answers. "Today" is in the hub's time zone, which the running
// extension recorded in the data directory; without that record it is the zone of this machine.
func runMCP(args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("household mcp", flag.ContinueOnError)
	data := fs.String("data", "", "the data directory (default: the hub's directory, see household help)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("household mcp: unexpected argument %q", fs.Arg(0))
	}
	dir, err := datadir.Resolve(*data)
	if err != nil {
		return err
	}
	return mcp.Serve(context.Background(), in, out, store.Open(dir), datadir.Zone(dir))
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

func (x *extension) renderPage(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	a, err := x.current()
	if err != nil {
		return nil, err
	}
	return a.Page().Render(ctx, r)
}

func (x *extension) actPage(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	a, err := x.current()
	if err != nil {
		return nil, err
	}
	return a.Page().Action(ctx, r)
}

func (x *extension) renderCard(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	a, err := x.current()
	if err != nil {
		return nil, err
	}
	return a.Card().Render(ctx, r)
}

// program is the extension for ext.Main. Init opens the store in the data directory the hub gives, records that
// directory for `household mcp`, and starts the background work; the UI handlers draw through the app of Init.
func program() ext.Program {
	var m struct{ ID, Version string }
	_ = json.Unmarshal(manifest, &m)
	var x extension
	return ext.Program{
		ID: m.ID, Version: m.Version, Manifest: manifest,
		Init: x.start,
		UI: map[string]ext.Page{
			pageUI: {Render: x.renderPage, Action: x.actPage},
			cardUI: {Render: x.renderCard},
		},
	}
}

// start opens the records, makes sure the schema is there, and starts the background work bound to ctx. It runs
// before any UI request, and it does not block on the hub.
func (x *extension) start(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	var cfg config
	if err := in.Config(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	dir := in.Info().DataDir
	datadir.Remember(dir)
	st := store.Open(dir)
	if err := st.Update(func(b *kernel.Base, now time.Time) error {
		_, err := model.Ensure(b, now)
		return err
	}); err != nil {
		return nil, err
	}
	if name := in.Hub.Timezone; name != "" {
		if _, err := time.LoadLocation(name); err == nil {
			datadir.RememberZone(dir, name)
		}
	}
	loc := hubLocation(in.Hub.Timezone)
	rem := &remind.Reminder{
		Hub:    remind.NewHub(in.Hub.URL, in.Hub.Token, in.Hub.Scopes),
		Path:   filepath.Join(dir, "remind.json"),
		Config: remind.Config{Mode: cfg.Remind, At: cfg.RemindTime},
	}
	// A change made by the page or a tool of this process wakes the background loop, which redraws the placements.
	// The channel holds one wake-up: several changes in a row are one redraw.
	kick := make(chan struct{}, 1)
	changed := func() {
		select {
		case kick <- struct{}{}:
		default:
		}
	}
	x.app.Store(ui.New(st, func() *time.Location { return loc }, changed))
	go run(ctx, st, hub, rem, loc, kick, firstRoundAfter, checkEvery, remindEvery)
	return nil, nil
}

// hubLocation is the hub's time zone, else this machine's.
func hubLocation(name string) *time.Location {
	if name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return time.Local
}

// placer is what the background loop draws with: the badge on the navigation entry, and the redraw of the pages and
// cards. *ext.Hub is one.
type placer interface {
	Badge(contribution, text, tone string) error
	Invalidate(contribution string) error
}

// run keeps the badge and the reminders current until ctx ends. It does one round after firstAfter, one every
// remindEvery, one when the stored records change (found by asking the store every checkEvery, or woken by kick
// after a change of this process), and each change also redraws the placements. It returns when ctx ends.
func run(ctx context.Context, st kernel.Storage, pl placer, rem *remind.Reminder, loc *time.Location,
	kick <-chan struct{}, firstAfter, checkEvery, remindEvery time.Duration) {
	first := time.NewTimer(firstAfter)
	defer first.Stop()
	check := time.NewTicker(checkEvery)
	defer check.Stop()
	periodic := time.NewTicker(remindEvery)
	defer periodic.Stop()
	round := func() {
		if err := refresh(ctx, st, pl, rem, loc, time.Now()); err != nil && ctx.Err() == nil {
			log.Printf("家庭管家：%v", err)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			round()
		case <-check.C:
			if st.Changed() {
				_ = pl.Invalidate("")
				round()
			}
		case <-kick:
			_ = pl.Invalidate("")
			round()
		case <-periodic.C:
			round()
		}
	}
}

// refresh loads the records, sets the badge (the number of things that need a purchase or a chore, with its tone;
// cleared at zero) and runs the reminder. A failure of one does not stop the other; both errors are returned.
func refresh(ctx context.Context, st kernel.Storage, pl placer, rem *remind.Reminder, loc *time.Location, now time.Time) error {
	var h *model.Home
	if err := st.View(func(b *kernel.Base) error {
		sp, err := b.Space(model.SpaceID)
		if err != nil {
			sp = nil // no records yet: an empty home
		}
		h = model.Load(sp, loc)
		return nil
	}); err != nil {
		return err
	}
	sum := rules.Summarize(h, model.DayOf(now, loc))
	text := ""
	if sum.Alerts > 0 {
		text = strconv.Itoa(sum.Alerts)
	}
	badgeErr := pl.Badge(pageUI, text, sum.Tone)
	return errors.Join(badgeErr, rem.Run(ctx, h, now, loc))
}
