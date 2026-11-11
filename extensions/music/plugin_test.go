package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestConformance(t *testing.T) {
	a := newApp()
	exttest.Check(t, manifest, exttest.Serve(t, program(a)), json.RawMessage(`{"iphone":true}`))
}

func TestManifestMatchesTheCode(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID, Version string
		Description string
		Tags        []string
		Runtime     struct {
			Command map[string]string
		}
		Node struct {
			Command     map[string]string
			Args        []string
			Machines    string
			Permissions struct {
				Files        bool `json:"files"`
				MacOSPrivacy bool `json:"macos_privacy"`
			}
		}
		Contributes struct {
			Connectors []struct {
				ID            string
				Name          string
				Capabilities  []string
				Hosts         []string
				ConfirmAlways []string `json:"confirm_always"`
			}
			UI      []struct{ ID, Placement, Icon string }
			Skills  []struct{ ID, Dir string }
			Prompts []struct{ ID, Kind, Title, Text string }
		}
		Engines struct {
			Persona           string `json:"persona"`
			ExtensionProtocol int    `json:"extension_protocol"`
		}
		Configuration struct {
			Properties map[string]json.RawMessage
		}
		Secrets []struct {
			Name     string
			Required bool
		}
		Permissions struct{ Network []string }
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "music" || m.ID != about.ID || m.Version != about.Version {
		t.Fatalf("id %q version %q, about %+v", m.ID, m.Version, about)
	}
	if c := m.Contributes.Connectors; len(c) != 1 || c[0].ID != appleMusicID || c[0].Name != "Apple Music" ||
		!slices.Equal(c[0].Capabilities, []string{"status", "tools", "actions"}) {
		t.Fatalf("connectors %+v", c)
	}
	if !slices.Equal(m.Contributes.Connectors[0].Hosts, []string{"music.apple.com"}) {
		t.Fatalf("hosts %v", m.Contributes.Connectors[0].Hosts)
	}
	if !slices.Equal(m.Contributes.Connectors[0].ConfirmAlways, []string{"applemusic_*_create_playlist"}) {
		t.Fatalf("confirm_always %v", m.Contributes.Connectors[0].ConfirmAlways)
	}
	// nav_page and the music icon need a hub build that knows both; older hubs must refuse the manifest instead of
	// installing a page they cannot draw. The exact floor moves with the first release that ships the icon.
	if m.Engines.ExtensionProtocol != 1 || !regexp.MustCompile(`^>=20[0-9]{10}$`).MatchString(m.Engines.Persona) {
		t.Fatalf("engines %+v", m.Engines)
	}
	if len(m.Secrets) != 0 || len(m.Permissions.Network) != 0 {
		t.Fatalf("secrets %+v network %v", m.Secrets, m.Permissions.Network)
	}
	if u := m.Contributes.UI; len(u) != 2 || u[0].ID != navUI || u[0].Placement != wire.PlacementNavPage || u[0].Icon != "music" ||
		u[1].ID != pageUI || u[1].Placement != wire.PlacementPluginPage {
		t.Fatalf("ui %+v", u)
	}
	if s := m.Contributes.Skills; len(s) != 2 ||
		s[0].ID != "music" || s[0].Dir != "skills/music" ||
		s[1].ID != "applemusic" || s[1].Dir != "skills/applemusic" {
		t.Fatalf("skills %+v", s)
	}
	for _, p := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64"} {
		if m.Runtime.Command[p] != "bin/music-"+p {
			t.Errorf("runtime.command[%s] = %q", p, m.Runtime.Command[p])
		}
	}
	if len(m.Runtime.Command) != 4 {
		t.Errorf("runtime.command %v", m.Runtime.Command)
	}
	if len(m.Node.Command) != 2 || m.Node.Command["darwin-arm64"] != "bin/music-darwin-arm64" {
		t.Errorf("node.command %v", m.Node.Command)
	}
	if !slices.Equal(m.Node.Args, []string{"node"}) || m.Node.Machines != "macs" || !m.Node.Permissions.Files || !m.Node.Permissions.MacOSPrivacy {
		t.Errorf("node %+v", m.Node)
	}
	// The extension is a generic music connector: its own description and tags never name one service.
	for _, word := range []string{"Apple Music", "Spotify", "apple music", "spotify"} {
		if strings.Contains(m.Description, word) || slices.Contains(m.Tags, word) {
			t.Errorf("the extension description or tags name %q: %q %v", word, m.Description, m.Tags)
		}
	}
	var macs struct {
		Type     string `json:"type"`
		MaxItems int    `json:"maxItems"`
		Items    struct {
			Format string   `json:"format"`
			XOS    []string `json:"x-os"`
		} `json:"items"`
	}
	if err := json.Unmarshal(m.Configuration.Properties["macs"], &macs); err != nil ||
		macs.Type != "array" || macs.MaxItems != 1 || macs.Items.Format != "machine" ||
		!slices.Equal(macs.Items.XOS, []string{"darwin"}) {
		t.Fatalf("macs schema %+v %v", macs, err)
	}
	var iphone struct {
		Type    string `json:"type"`
		Default bool   `json:"default"`
	}
	if err := json.Unmarshal(m.Configuration.Properties["iphone"], &iphone); err != nil || iphone.Type != "boolean" || iphone.Default {
		t.Fatalf("iphone schema %+v %v", iphone, err)
	}
	// The configuration's properties are exactly Config's fields.
	var keys []string
	ct := reflect.TypeOf(Config{})
	for i := range ct.NumField() {
		name, _, _ := strings.Cut(ct.Field(i).Tag.Get("json"), ",")
		keys = append(keys, name)
	}
	slices.Sort(keys)
	if got := slices.Sorted(maps.Keys(m.Configuration.Properties)); !slices.Equal(got, keys) {
		t.Fatalf("configuration %v, Config %v", got, keys)
	}
}

// appWith builds an app with one Apple Music source from providers directly, for tests that do not need the hub's
// process.
func appWith(t *testing.T, providers ...provider) *app {
	t.Helper()
	a := newApp()
	a.logf = t.Logf
	src := newSource(appleMusicID, appleMusicName, appleMusicName, "Apple", appleMusicGuide...)
	for _, p := range providers {
		if err := src.add(p); err != nil {
			t.Fatal(err)
		}
	}
	a.addSource(src)
	return a
}

func call(t *testing.T, a *app, method, params string) (map[string]any, error) {
	t.Helper()
	var raw json.RawMessage
	if params != "" {
		raw = json.RawMessage(params)
	}
	res, err := a.connector(context.Background(), method, raw)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(res)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func toolNames(t *testing.T, list map[string]any) []string {
	t.Helper()
	var names []string
	for _, x := range list["tools"].([]any) {
		tool := x.(map[string]any)
		names = append(names, tool["name"].(string))
		if tool["input_schema"].(map[string]any)["type"] != "object" || tool["description"] == "" {
			t.Fatalf("tool %v", tool)
		}
	}
	return names
}

func code(err error) string {
	var e *ext.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestConnectorRouting(t *testing.T) {
	mac := newMacProvider("m1", macFixture())
	iphone := newIPhoneProvider(openSnapshotStore(t.TempDir()))
	if _, err := iphone.Sync(context.Background(), json.RawMessage(`{
		"recent":[{"id":"r1","title":"Song","artist":"Artist"}],
		"playlists":[{"id":"p1","title":"List","track_count":2}]
	}`)); err != nil {
		t.Fatal(err)
	}
	a := appWith(t, mac, iphone)

	st, err := call(t, a, "status", "")
	if err != nil || st["state"] != "ok" || !strings.Contains(st["account"].(string), "Apple Music") {
		t.Fatalf("status: %v %v", st, err)
	}
	if _, err := call(t, a, "status", `{"contribution":"other"}`); code(err) != wire.ErrorCodeNoContribution {
		t.Fatalf("another contribution: %v", err)
	}

	tools, err := call(t, a, "tools.list", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"applemusic_mac_search", "applemusic_mac_recent", "applemusic_mac_playlists", "applemusic_mac_playlist",
		"applemusic_mac_now", "applemusic_mac_stats", "applemusic_mac_control",
		"applemusic_iphone_recent", "applemusic_iphone_playlists", "applemusic_iphone_now",
		"applemusic_iphone_stats", "applemusic_iphone_sync",
	}
	if got := toolNames(t, tools); !slices.Equal(got, want) {
		t.Fatalf("tools %v", got)
	}
	actions, err := call(t, a, "actions.list", "")
	if err != nil || !slices.Equal(toolNames(t, actions), []string{"applemusic_mac_create_playlist"}) {
		t.Fatalf("actions %v %v", actions, err)
	}

	search, err := call(t, a, "tools.call", `{"name":"applemusic_mac_search","args":{"query":"midnight"}}`)
	if err != nil || !strings.Contains(search["text"].(string), "Midnight City") {
		t.Fatalf("mac search: %v %v", search, err)
	}
	now, err := call(t, a, "tools.call", `{"name":"applemusic_mac_now","args":{}}`)
	if err != nil || !strings.Contains(now["text"].(string), "Midnight City") {
		t.Fatalf("mac now: %v %v", now, err)
	}
	recent, err := call(t, a, "tools.call", `{"name":"applemusic_iphone_recent","args":{}}`)
	if err != nil || !strings.Contains(recent["text"].(string), "Song") {
		t.Fatalf("iphone recent: %v %v", recent, err)
	}
	sync, err := call(t, a, "tools.call", `{"name":"applemusic_iphone_sync","args":{"playlists":[{"id":"p2","title":"Other"}]}}`)
	if err != nil || !strings.Contains(sync["text"].(string), "Synced") {
		t.Fatalf("iphone sync: %v %v", sync, err)
	}
	if _, err := call(t, a, "tools.call", `{"name":"applemusic_nope","args":{}}`); err == nil {
		t.Fatal("an unknown endpoint answered")
	}

	summary, err := call(t, a, "actions.describe", `{"name":"applemusic_mac_create_playlist","args":{"name":"Focus","track_ids":["m1"]}}`)
	if err != nil || !strings.Contains(summary["summary"].(string), "Focus") {
		t.Fatalf("describe: %v %v", summary, err)
	}
	create := `{"name":"applemusic_mac_create_playlist","args":{"name":"Focus","track_ids":["m1"]},"request_id":"req-1"}`
	first, err := call(t, a, "actions.do", create)
	if err != nil || !strings.Contains(first["text"].(string), "Created") {
		t.Fatalf("create: %v %v", first, err)
	}
	again, err := call(t, a, "actions.do", create)
	if err != nil || again["text"] != first["text"] {
		t.Fatalf("repeat: %v %v", again, err)
	}
	if _, err := call(t, a, "actions.do", `{"name":"applemusic_mac_create_playlist","args":{"name":"Other","track_ids":["m1"]},"request_id":"req-1"}`); err == nil {
		t.Error("a reused request_id with other arguments answered")
	}
}

// A create_playlist with play:true is one write followed by one playback request. The request must reach the Mac as
// the bare action plus the new playlist's id: the node's whitelist rejects a "play_playlist:<id>" action, and the user
// would see a failure after the playlist was already created.
func TestCreatePlaylistWithPlay(t *testing.T) {
	fn := macFixture()
	a := appWith(t, newMacProvider("m1", fn))
	res, err := call(t, a, "actions.do",
		`{"name":"applemusic_mac_create_playlist","args":{"name":"Focus","track_ids":["m1"],"play":true},"request_id":"req-play"}`)
	if err != nil || !strings.Contains(res["text"].(string), "Playing it now.") {
		t.Fatalf("create and play: %v %v", res, err)
	}
	if got := fn.controlParams(t); len(got) != 2 || got["action"] != "play_playlist" || got["id"] != "p.new" {
		t.Fatalf("playback sent %v", got)
	}
}

func TestConnectorNotConfigured(t *testing.T) {
	a := appWith(t)
	st, err := call(t, a, "status", "")
	if err != nil || st["state"] != "not_configured" {
		t.Fatalf("status: %v %v", st, err)
	}
	if tools, err := call(t, a, "tools.list", ""); err != nil || len(tools["tools"].([]any)) != 0 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	v, err := a.render(context.Background(), req(6, nil))
	if err != nil {
		t.Fatal(err)
	}
	checked(t, v)
	if !strings.Contains(viewText(v), "Connect Apple Music") {
		t.Fatalf("the fallback does not ask to connect:\n%s", viewText(v))
	}
}

func TestManifestConfigMatches(t *testing.T) {
	// The manifest's configuration keys are Config's fields (the full check is in TestManifestMatchesTheCode); this
	// small test names the two endpoints so a rename is caught here too.
	var c Config
	if c.machine() != "" || c.IPhone {
		t.Fatalf("zero config %+v", c)
	}
	_ = fmt.Sprintf("%+v", c)
}
