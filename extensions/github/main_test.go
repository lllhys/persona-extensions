package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/github/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/github/internal/github"
	"github.com/nwverse/persona-extensions/extensions/github/internal/serve"
	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The hub reads the connector from persona-extension.json before the program runs (a switched-off connector can
// still be signed in to) and uses it over what the program says: the two must be the same, with the inbox kinds
// described, the standard triage rules setting and a fallback (GitHub's items never wait for the triage model).
func TestManifestIsWhatTheProgramDeclares(t *testing.T) {
	raw, err := os.ReadFile("persona-extension.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID          string                     `json:"id"`
		Contributes map[string]json.RawMessage `json:"contributes"`
		Runtime     struct {
			Command map[string]string `json:"command"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var items []wire.ConnectorItem
	if err := json.Unmarshal(m.Contributes["connectors"], &items); err != nil || len(items) != 1 {
		t.Fatalf("connectors: %s (%v)", m.Contributes["connectors"], err)
	}
	want := serve.ItemOf(serve.ManifestOf(github.New(github.Config{}, "", nil)))
	if !reflect.DeepEqual(items[0], want) {
		t.Fatalf("persona-extension.json declares\n%+v\nthe program\n%+v", items[0], want)
	}
	if m.ID != "github" || want.ID != "github" {
		t.Fatalf("id %s / %s: the hub's connector id, so its state carries over", m.ID, want.ID)
	}
	if in, err := pluginkit.CheckManifest(raw); err != nil || !in.Fallback("github") {
		t.Fatalf("inbox rules: %v (fallback %v)", err, in.Fallback("github"))
	}
	if err := pluginkit.CheckStandardSettings(raw); err != nil {
		t.Fatal(err)
	}
	if len(m.Runtime.Command) != 4 {
		t.Fatalf("platforms: %v", m.Runtime.Command)
	}
	var ui []wire.UIItem
	if err := json.Unmarshal(m.Contributes["ui"], &ui); err != nil || len(ui) != 1 || ui[0].ID != "github" || ui[0].Placement != wire.PlacementNavPage {
		t.Fatalf("ui: %s (%v)", m.Contributes["ui"], err)
	}
}

// The program holds to the process protocol as the SDK's own check has it. Signed out, so nothing dials GitHub.
func TestConformance(t *testing.T) {
	init := exttest.Check(t, manifest, exttest.Serve(t, program(&app{})))
	var items []wire.ConnectorItem
	if err := json.Unmarshal(init.Contributes["connectors"], &items); err != nil || len(items) != 1 || items[0].ID != "github" {
		t.Fatalf("contributes %s (%v)", init.Contributes["connectors"], err)
	}
}

// The hub's answers to connector.credentials.gh_token become the connector's errors, so its status tells the user
// what to do as the hub's own connector did; the token itself goes nowhere else.
func TestMachineSignIn(t *testing.T) {
	for code, want := range map[string]error{
		"no_machine": github.ErrNoMachine, "offline": github.ErrOffline, "node_too_old": github.ErrNodeTooOld,
		"no_gh": github.ErrNoGh, "not_signed_in": github.ErrNotSignedIn,
		wire.ErrorCodePermissionDenied: github.ErrNotGranted, wire.ErrorCodeUnknownMethod: github.ErrHubTooOld,
	} {
		if got := hubError(&ext.Error{Code: code, Message: "x"}); !errors.Is(got, want) {
			t.Errorf("%s: %v", code, got)
		}
	}

	var asked []string
	a := &app{}
	c, err := a.build(serve.Init{GhToken: func(_ context.Context, machine, host string) (string, error) {
		asked = append(asked, machine+" "+host)
		return "", &ext.Error{Code: "no_gh", Message: "gh is not installed"}
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gh := c.(*github.Connector)
	st, err := gh.SetAuth(context.Background(), connector.AuthChoice{Source: github.SourceMachine, Machine: "m1", MachineName: "Laptop"})
	if err != nil || st.Source != github.SourceMachine || st.Machine != "m1" {
		t.Fatalf("auth.set: %+v %v", st, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go gh.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(gh.Status(ctx).Error, "Laptop 上没有 gh") {
		if time.Now().After(deadline) {
			t.Fatalf("status: %+v (asked %v)", gh.Status(ctx), asked)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(asked) == 0 || asked[0] != "m1 github.com" {
		t.Fatalf("asked %v", asked)
	}
	if st, _ := gh.SetAuth(context.Background(), connector.AuthChoice{Source: github.SourceDevice}); st.Source != github.SourceDevice || st.Machine != "" {
		t.Fatalf("device: %+v", st)
	}
}
