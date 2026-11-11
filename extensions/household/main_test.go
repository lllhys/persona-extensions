package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/datadir"
	"github.com/nwverse/persona-extensions/extensions/household/internal/mcp"
	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/ops"
	"github.com/nwverse/persona-extensions/extensions/household/internal/remind"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/store"
)

// TestMain keeps the locator files, which Init and the MCP server read, out of the real configuration directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "household-main-test")
	if err != nil {
		log.Fatal(err)
	}
	store.Locator = func() string { return filepath.Join(dir, "config", "data-dir") }
	datadir.Locator = func() string { return filepath.Join(dir, "config", "data-dir") }
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// manifestDoc is the part of the manifest the tests compare with the program.
type manifestDoc struct {
	Contributes struct {
		MCP []struct {
			ID       string   `json:"id"`
			Requires []string `json:"requires"`
			Allow    []string `json:"allow"`
		} `json:"mcp_servers"`
	} `json:"contributes"`
}

func parseManifest(t *testing.T) manifestDoc {
	t.Helper()
	var d manifestDoc
	if err := json.Unmarshal(manifest, &d); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	return d
}

func TestConformance(t *testing.T) {
	// exttest gives Init a temporary data directory, so the program runs on its own records.
	exttest.Check(t, manifest, exttest.Serve(t, program()))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlyToolsAreAllowed(t *testing.T) {
	d := parseManifest(t)
	if len(d.Contributes.MCP) != 1 {
		t.Fatalf("want one mcp server, got %d", len(d.Contributes.MCP))
	}
	var read []string
	for _, tool := range mcp.Tools() {
		if tool.Read {
			read = append(read, tool.Name)
		}
	}
	allow := slices.Clone(d.Contributes.MCP[0].Allow)
	slices.Sort(read)
	slices.Sort(allow)
	if !slices.Equal(read, allow) {
		t.Fatalf("read-only tools %v, manifest allow %v", read, allow)
	}
}

func TestUsageNamesEveryRequirement(t *testing.T) {
	d := parseManifest(t)
	for _, s := range d.Contributes.MCP {
		if len(s.Requires) == 0 {
			t.Fatalf("server %q has no requires words", s.ID)
		}
		for _, word := range s.Requires {
			if !strings.Contains(usage, word) {
				t.Errorf("usage does not contain %q", word)
			}
		}
	}
}

func TestDispatch(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		handled   bool
		wantErr   bool
		wantUsage bool
	}{
		{name: "no command", args: nil},
		{name: "manifest belongs to ext.Main", args: []string{"manifest"}},
		{name: "unknown command belongs to ext.Main", args: []string{"nope"}},
		{name: "help", args: []string{"help"}, handled: true, wantUsage: true},
		{name: "dash h", args: []string{"-h"}, handled: true, wantUsage: true},
		{name: "long help", args: []string{"--help"}, handled: true, wantUsage: true},
		{name: "mcp error", args: []string{"mcp", "--nope"}, handled: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			handled, err := dispatch(tt.args, strings.NewReader(""), &out)
			if handled != tt.handled {
				t.Fatalf("handled = %v, want %v", handled, tt.handled)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
			if tt.wantUsage && out.String() != usage {
				t.Fatalf("output %q, want the usage text", out.String())
			}
		})
	}
}

func TestRunMCP(t *testing.T) {
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
	tests := []struct {
		name    string
		args    []string
		env     string
		wantErr bool
	}{
		{name: "flag", args: []string{"--data", t.TempDir()}},
		{name: "environment", env: t.TempDir()},
		{name: "bad flag", args: []string{"--nope"}, wantErr: true},
		{name: "extra argument", args: []string{"--data", t.TempDir(), "extra"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv("PERSONA_HOUSEHOLD_DATA", tt.env)
			}
			var out bytes.Buffer
			err := runMCP(tt.args, strings.NewReader(ping), &out)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var reply struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
				t.Fatalf("reply %q: %v", out.String(), err)
			}
			if reply.ID != 1 || reply.Result == nil {
				t.Fatalf("ping reply %q", out.String())
			}
		})
	}
}

// fakeAPI is the hub's API as the reminder sees it: the scopes it is given, and what it was asked to send.
type fakeAPI struct {
	scopes  []string
	mu      sync.Mutex
	notices []remind.Notice
}

func (f *fakeAPI) Can(scope string) bool { return slices.Contains(f.scopes, scope) }

func (f *fakeAPI) Notify(_ context.Context, n remind.Notice) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, n)
	return nil
}

func (f *fakeAPI) CreateTodo(context.Context, remind.Todo) (string, error) { return "todo", nil }
func (f *fakeAPI) DoneTodo(context.Context, string) error                  { return nil }

func (f *fakeAPI) sent() []remind.Notice {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.notices)
}

// fakePlacer records the badges and the redraws it is asked for.
type fakePlacer struct {
	mu          sync.Mutex
	badges      []string // "contribution|text|tone"
	invalidates []string
}

func (p *fakePlacer) Badge(contribution, text, tone string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.badges = append(p.badges, contribution+"|"+text+"|"+tone)
	return nil
}

func (p *fakePlacer) Invalidate(contribution string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.invalidates = append(p.invalidates, contribution)
	return nil
}

func (p *fakePlacer) counts() (badges, invalidates int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.badges), len(p.invalidates)
}

// loaded returns a store in a new directory with the sample records.
func loaded(t *testing.T) *store.Store {
	t.Helper()
	st := store.Open(t.TempDir())
	if err := st.Update(func(b *kernel.Base, at time.Time) error {
		_, err := ops.LoadSamples(b, at, time.UTC)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRefreshBadge(t *testing.T) {
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		samples bool
	}{
		{name: "no records: badge cleared", samples: false},
		{name: "samples: the count of alerts and its tone", samples: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := store.Open(t.TempDir())
			if tt.samples {
				st = loaded(t)
			}
			var h *model.Home
			if err := st.View(func(b *kernel.Base) error {
				sp, _ := b.Space(model.SpaceID)
				h = model.Load(sp, time.UTC)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			sum := rules.Summarize(h, model.DayOf(now, time.UTC))
			pl := &fakePlacer{}
			rem := &remind.Reminder{Hub: &fakeAPI{}, Path: filepath.Join(t.TempDir(), "remind.json"),
				Config: remind.Config{Mode: "off"}}
			if err := refresh(context.Background(), st, pl, rem, time.UTC, now); err != nil {
				t.Fatal(err)
			}
			// The badge is "contribution|text|tone": a cleared badge has empty text and tone.
			want := pageUI + "||"
			if sum.Alerts > 0 {
				want = pageUI + "|" + strconv.Itoa(sum.Alerts) + "|" + sum.Tone
			}
			if len(pl.badges) != 1 || pl.badges[0] != want {
				t.Fatalf("badges %q, want [%q]", pl.badges, want)
			}
			if tt.samples && (sum.Alerts == 0 || sum.Tone == "") {
				t.Fatalf("samples should need attention: alerts %d tone %q", sum.Alerts, sum.Tone)
			}
		})
	}
}

func TestRefreshReminder(t *testing.T) {
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		scopes    []string
		mode      string
		wantNotes int
	}{
		{name: "notify scope and records: one notification", scopes: []string{remind.ScopeNotify}, wantNotes: 1},
		{name: "reminder off: none", scopes: []string{remind.ScopeNotify}, mode: "off"},
		{name: "scope not granted: none", scopes: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := loaded(t)
			api := &fakeAPI{scopes: tt.scopes}
			rem := &remind.Reminder{Hub: api, Path: filepath.Join(t.TempDir(), "remind.json"),
				Config: remind.Config{Mode: tt.mode, At: "00:00"}}
			pl := &fakePlacer{}
			if err := refresh(context.Background(), st, pl, rem, time.UTC, now); err != nil {
				t.Fatal(err)
			}
			got := api.sent()
			if len(got) != tt.wantNotes {
				t.Fatalf("notifications %d, want %d", len(got), tt.wantNotes)
			}
			if tt.wantNotes > 0 && got[0].ID != "household-2026-11-10" {
				t.Fatalf("notification id %q", got[0].ID)
			}
		})
	}
}

func TestRunRedraws(t *testing.T) {
	tests := []struct {
		name           string
		change         bool // another store writes the file
		kick           bool // this process signals a change
		wantInvalidate bool
	}{
		{name: "nothing changes", wantInvalidate: false},
		{name: "another store changes the file", change: true, wantInvalidate: true},
		{name: "this process signals a change", kick: true, wantInvalidate: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			mine := store.Open(dir)
			pl := &fakePlacer{}
			rem := &remind.Reminder{Hub: &fakeAPI{}, Path: filepath.Join(dir, "remind.json"), Config: remind.Config{Mode: "off"}}
			kick := make(chan struct{}, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				run(ctx, mine, pl, rem, time.UTC, kick, time.Millisecond, time.Millisecond, time.Hour)
				close(done)
			}()
			// The first round sets the badge at start.
			waitFor(t, "the first badge", func() bool { b, _ := pl.counts(); return b == 1 })
			if tt.change {
				other := store.Open(dir)
				if err := other.Update(func(b *kernel.Base, at time.Time) error {
					_, err := ops.LoadSamples(b, at, time.UTC)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			if tt.kick {
				kick <- struct{}{}
			}
			if tt.wantInvalidate {
				waitFor(t, "a redraw of the placements", func() bool { _, i := pl.counts(); return i > 0 })
			} else {
				time.Sleep(20 * time.Millisecond)
				if _, i := pl.counts(); i != 0 {
					t.Fatalf("redraws %d without a change", i)
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("run did not end with its context")
			}
		})
	}
}

// TestRunWaitsBeforeFirstRound checks that the first round does not run until the given delay: the hub must have
// answered the initialize request before any badge is sent.
func TestRunWaitsBeforeFirstRound(t *testing.T) {
	dir := t.TempDir()
	st := loaded(t)
	pl := &fakePlacer{}
	rem := &remind.Reminder{Hub: &fakeAPI{}, Path: filepath.Join(dir, "remind.json"), Config: remind.Config{Mode: "off"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		run(ctx, st, pl, rem, time.UTC, make(chan struct{}), 300*time.Millisecond, time.Millisecond, time.Hour)
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	if b, _ := pl.counts(); b != 0 {
		t.Fatalf("%d badges before the first round delay has passed", b)
	}
	waitFor(t, "the first badge", func() bool { b, _ := pl.counts(); return b > 0 })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not end with its context")
	}
}

// waitFor polls cond for a few seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestHubLocation(t *testing.T) {
	tests := []struct {
		name string
		zone string
		want string
	}{
		{name: "empty: this machine", zone: "", want: time.Local.String()},
		{name: "unknown zone: this machine", zone: "Not/AZone", want: time.Local.String()},
		{name: "hub zone", zone: "Asia/Shanghai", want: "Asia/Shanghai"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hubLocation(tt.zone).String(); got != tt.want {
				t.Fatalf("location %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProgramBeforeInit(t *testing.T) {
	var x extension
	if _, err := x.current(); err != errNotInitialized {
		t.Fatalf("current before init: %v", err)
	}
	p := program()
	r := &ext.UIRequest{}
	r.Contribution, r.Size, r.UIVersion = pageUI, wire.SizeFull, 4
	if _, err := p.UI[pageUI].Render(context.Background(), r); err != errNotInitialized {
		t.Fatalf("render before init: %v", err)
	}
	if _, err := p.UI[pageUI].Action(context.Background(), r); err != errNotInitialized {
		t.Fatalf("action before init: %v", err)
	}
	if _, err := p.UI[cardUI].Render(context.Background(), r); err != errNotInitialized {
		t.Fatalf("card before init: %v", err)
	}
}

// findBadge returns the last badge the program sent to the hub, if any.
func findBadge(events []exttest.Event) (wire.BadgeEvent, bool) {
	var out wire.BadgeEvent
	found := false
	for _, e := range events {
		if e.Name != "ui.badge" {
			continue
		}
		if err := json.Unmarshal(e.Params, &out); err == nil {
			found = true
		}
	}
	return out, found
}

// TestHostStartDrawsThroughTheProtocol runs the program as the hub does: initialize, the badge, and the page and the
// card rendered, every answer held to the protocol's schemas.
func TestHostStartDrawsThroughTheProtocol(t *testing.T) {
	h := exttest.Start(t, manifest, exttest.Serve(t, program()))
	// The first round sets the badge: an empty home has nothing to show, so the badge is cleared (empty text).
	waitFor(t, "the first badge", func() bool { _, ok := findBadge(h.Events()); return ok })
	b, _ := findBadge(h.Events())
	if b.Contribution != pageUI || b.Text != "" {
		t.Fatalf("badge %+v, want the household page with no text", b)
	}

	for _, c := range []struct{ contribution, size string }{{pageUI, wire.SizeFull}, {cardUI, wire.SizeCompact}} {
		var v wire.View
		if err := h.Call("ui.render", wire.UIParams{Contribution: c.contribution, State: "", Size: c.size,
			Placement: "nav_page", UIVersion: 4}, &v); err != nil {
			t.Fatalf("render %s: %v", c.contribution, err)
		}
	}
}
