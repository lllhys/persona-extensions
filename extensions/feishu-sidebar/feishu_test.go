package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

// testApp is the extension initialized with config (JSON; "" for none) and the hub's own reported URL ("" for none).
func testApp(t *testing.T, config, hubURL string) *app {
	t.Helper()
	a := newApp()
	in := &ext.Init{}
	if config != "" {
		in.InitializeParams.Config = json.RawMessage(config)
	}
	in.InitializeParams.Hub.URL = hubURL
	if _, err := a.init(context.Background(), in, nil); err != nil {
		t.Fatal(err)
	}
	return a
}

func req(version int) *ext.UIRequest {
	r := &ext.UIRequest{}
	r.Contribution, r.Placement, r.Size, r.UIVersion = pageUI, wire.PlacementNavPage, wire.SizeFull, version
	return r
}

// checked holds v to what the apps draw: the SDK's check and exttest's.
func checked(t *testing.T, v *wire.View) *wire.View {
	t.Helper()
	if err := wire.CheckView(v); err != nil {
		t.Fatalf("CheckView: %v\n%s", err, texts(v))
	}
	raw, _ := json.Marshal(v)
	if err := exttest.CheckView(raw, false); err != nil {
		t.Fatalf("exttest.CheckView: %v\n%s", err, texts(v))
	}
	return v
}

func draw(t *testing.T, a *app, version int) *wire.View {
	t.Helper()
	v, err := a.render(context.Background(), req(version))
	if err != nil {
		t.Fatal(err)
	}
	return checked(t, v)
}

// texts is a view in lines for the tests to search: [type] its words.
func texts(v *wire.View) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", v.Title)
	for _, s := range v.Sections {
		if s.Title != "" {
			fmt.Fprintf(&b, "## %s\n", s.Title)
		}
		for _, c := range s.Items {
			fmt.Fprintf(&b, "[%s]", c.Type)
			for _, part := range []string{c.Title, c.Subtitle, c.Text, c.Value, c.URL} {
				if part != "" {
					b.WriteString(" " + part)
				}
			}
			b.WriteString("\n")
		}
		if s.Footer != "" {
			fmt.Fprintf(&b, "_%s_\n", s.Footer)
		}
	}
	return b.String()
}

func says(t *testing.T, v *wire.View, words ...string) {
	t.Helper()
	s := texts(v)
	for _, w := range words {
		if !strings.Contains(s, w) {
			t.Errorf("no %q in:\n%s", w, s)
		}
	}
}

func saysNot(t *testing.T, v *wire.View, words ...string) {
	t.Helper()
	s := texts(v)
	for _, w := range words {
		if strings.Contains(s, w) {
			t.Errorf("%q in:\n%s", w, s)
		}
	}
}
