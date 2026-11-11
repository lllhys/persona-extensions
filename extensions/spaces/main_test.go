package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/spaces/internal/mcp"
	"github.com/nwverse/persona-extensions/extensions/spaces/internal/ui"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/store"
)

// TestMain keeps the locator file, which Init writes, out of the real configuration directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "spaces-main-test")
	if err != nil {
		log.Fatal(err)
	}
	store.Locator = func() string { return filepath.Join(dir, "config", "data-dir") }
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
	// exttest gives Init a temporary data directory, so the app runs on its own store.
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
				t.Setenv("PERSONA_SPACES_DATA", tt.env)
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

// fakeHub records the contributions that were asked to be drawn again.
type fakeHub struct{ got chan string }

func (h *fakeHub) Invalidate(contribution string) error {
	select {
	case h.got <- contribution:
	default:
	}
	return nil
}

func TestWatch(t *testing.T) {
	name := "空间"
	tests := []struct {
		name   string
		change bool
	}{
		{name: "another store changes the file", change: true},
		{name: "nothing changes", change: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			mine := store.Open(dir)
			other := store.Open(dir)
			hub := &fakeHub{got: make(chan string, 16)}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				watch(ctx, mine, hub, time.Millisecond)
				close(done)
			}()
			if tt.change {
				err := other.Update(func(b *kernel.Base, _ time.Time) error {
					_, err := b.PutSpace(kernel.SpaceInput{Name: &name})
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				select {
				case c := <-hub.got:
					if c != "" {
						t.Fatalf("invalidated %q, want all contributions", c)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("no invalidation after another store changed the file")
				}
			} else {
				select {
				case c := <-hub.got:
					t.Fatalf("invalidated %q without a change", c)
				case <-time.After(20 * time.Millisecond):
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("watch did not end with its context")
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
	r.Contribution, r.Size, r.UIVersion = ui.PageUI, wire.SizeFull, 4
	if _, err := p.UI[ui.PageUI].Render(context.Background(), r); err != errNotInitialized {
		t.Fatalf("render before init: %v", err)
	}
	if _, err := p.UI[ui.PageUI].Action(context.Background(), r); err != errNotInitialized {
		t.Fatalf("action before init: %v", err)
	}
	if _, err := p.UI[ui.CardUI].Render(context.Background(), r); err != errNotInitialized {
		t.Fatalf("card before init: %v", err)
	}
}

func TestProgramInitAndUI(t *testing.T) {
	p := program()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	info := &wire.ExtensionInfo{DataDir: t.TempDir()}
	in := &ext.Init{InitializeParams: wire.InitializeParams{Extension: info}}
	if _, err := p.Init(ctx, in, &ext.Hub{}); err != nil {
		t.Fatal(err)
	}
	r := &ext.UIRequest{}
	r.Contribution, r.Size, r.UIVersion = ui.PageUI, wire.SizeFull, 4
	if _, err := p.UI[ui.PageUI].Render(ctx, r); err != nil {
		t.Fatalf("page render: %v", err)
	}
	r.Contribution, r.Size = ui.CardUI, wire.SizeCompact
	if _, err := p.UI[ui.CardUI].Render(ctx, r); err != nil {
		t.Fatalf("card render: %v", err)
	}
	r.Contribution, r.Size = ui.PageUI, wire.SizeFull
	r.Action = "unknown:x"
	res, err := p.UI[ui.PageUI].Action(ctx, r)
	if err != nil {
		t.Fatalf("action: %v", err)
	}
	if res == nil || res.View == nil {
		t.Fatalf("action without a change should answer the view, got %+v", res)
	}
}
