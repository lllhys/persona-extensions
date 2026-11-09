package wechat

import (
	"context"
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func manifestJSON(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(Manifest("1.0.0", "wechat"))
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.ValidateJSON("Manifest", raw); err != nil {
		t.Fatalf("the manifest does not hold to schema/extension: %v", err)
	}
	return raw
}

// Exercise the connector and channel against the official SDK's synthetic hub.
func TestProgramAgainstTheSDKsHub(t *testing.T) {
	m := manifestJSON(t)
	exttest.CheckConnectors(t, m, exttest.Serve(t, Program("1.0.0")))
	h := exttest.CheckChannels(t, m, exttest.Serve(t, Program("1.0.0")))
	var items []wire.ConnectorItem
	_ = json.Unmarshal(h.Init.Contributes["connectors"], &items)
	if len(items) != 1 || items[0].ID != ID || !slices.Equal(items[0].Capabilities, Capabilities) {
		t.Fatalf("initialize contributes %s", h.Init.Contributes["connectors"])
	}
	var st wire.ConnectorStatus
	if err := h.Call("connector.status", wire.ConnectorParams{Contribution: ID}, &st); err != nil ||
		st.State != wire.ConnectorStateNotSignedIn || st.Error != t2(defaultLang, "wechat.not_bound") {
		t.Fatalf("status %+v %v", st, err)
	}
	var tools wire.ConnectorTools
	if err := h.Call("connector.tools.list", wire.ConnectorParams{Contribution: ID}, &tools); err != nil || len(tools.Tools) != 0 {
		t.Fatalf("tools.list %+v %v", tools, err)
	}
	if err := h.Call("connector.archive.changes", wire.ConnectorChangesParams{Contribution: ID}, nil); err == nil || !strings.Contains(err.Error(), "has no") {
		t.Fatalf("an undeclared capability: %v", err)
	}
}

func TestProgramWritesInTheHubsLanguage(t *testing.T) {
	p := Program("1.0.0")
	init := p.Init
	p.Init = func(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
		in.Hub.Locale = langEn
		return init(ctx, in, hub)
	}
	h := exttest.Start(t, manifestJSON(t), exttest.Serve(t, p))
	var st wire.ConnectorStatus
	if err := h.Call("connector.status", wire.ConnectorParams{Contribution: ID}, &st); err != nil || st.Error != "WeChat isn't bound yet" {
		t.Fatalf("status %+v %v", st, err)
	}
}

func t2(l, key string) string { return t(l, key) }

var tr = t

func TestLocalesHaveTheSameKeys(t *testing.T) {
	want := slices.Sorted(maps.Keys(catalogs[langZhHans]))
	for _, l := range []string{langEn, langJa} {
		if got := slices.Sorted(maps.Keys(catalogs[l])); !slices.Equal(got, want) {
			t.Errorf("%s: keys %v, zh-Hans has %v", l, got, want)
		}
	}
	if langOf("") != defaultLang || langOf("ko") != langEn || langOf(langJa) != langJa {
		t.Error("langOf")
	}
	ph := regexp.MustCompile(`\{\w+\}`)
	for key, zh := range catalogs[langZhHans] {
		want := slices.Sorted(slices.Values(ph.FindAllString(zh, -1)))
		for _, l := range []string{langEn, langJa} {
			if got := slices.Sorted(slices.Values(ph.FindAllString(catalogs[l][key], -1))); !slices.Equal(got, want) {
				t.Errorf("%s %s: placeholders %v, zh-Hans has %v", l, key, got, want)
			}
		}
	}
	if got := t2(langEn, "no.such.key"); got != "no.such.key" {
		t.Errorf("a missing key: %q", got)
	}
	if got := tr(langEn, "wechat.account", "id", "123456"); got != "WeChat user 123456" {
		t.Errorf("args: %q", got)
	}
}
