package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestConformance(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, "") // the site is the fake before anything is drawn
	exttest.Check(t, manifest, exttest.Serve(t, program(a)),
		json.RawMessage(`{"cities": [{"name": "上海", "latitude": 31.22, "longitude": 121.46}], "temperature_unit": "celsius", "wind_unit": "kmh"}`))
	if n := f.hit("/v1/forecast"); n != 1 {
		t.Fatalf("the page read the forecast %d times", n)
	}
}

func TestManifestMatchesTheCode(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID, Version string
		Runtime     struct {
			Command map[string]string
		}
		Contributes struct {
			Connectors []struct {
				ID                  string
				Capabilities, Hosts []string
			}
			UI []struct{ ID, Placement, Icon string }
		}
		Configuration struct {
			Properties map[string]json.RawMessage
		}
		Permissions struct{ Network []string }
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "weather" || m.ID != about.ID || m.Version != about.Version {
		t.Fatalf("id %q version %q, about %+v", m.ID, m.Version, about)
	}
	if c := m.Contributes.Connectors; len(c) != 1 || c[0].ID != connectorID ||
		!slices.Equal(c[0].Capabilities, []string{"status", "tools"}) {
		t.Fatalf("connectors %+v", c)
	}
	for _, host := range []string{"api.open-meteo.com", "air-quality-api.open-meteo.com", "geocoding-api.open-meteo.com"} {
		if !slices.Contains(m.Contributes.Connectors[0].Hosts, host) || !slices.Contains(m.Permissions.Network, host) {
			t.Fatalf("host %q not in hosts %v or network %v", host, m.Contributes.Connectors[0].Hosts, m.Permissions.Network)
		}
	}
	if u := m.Contributes.UI; len(u) != 2 || u[0].ID != navUI || u[0].Placement != wire.PlacementNavPage || u[0].Icon != "gauge" ||
		u[1].ID != pageUI || u[1].Placement != wire.PlacementPluginPage {
		t.Fatalf("ui %+v", u)
	}
	for _, p := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64"} {
		if m.Runtime.Command[p] != "bin/weather-"+p {
			t.Errorf("runtime.command[%s] = %q", p, m.Runtime.Command[p])
		}
	}
	if len(m.Runtime.Command) != 4 {
		t.Errorf("runtime.command %v", m.Runtime.Command)
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

	// The cities default is Shanghai, the first city, and the units default as the code reads them.
	var citiesSchema struct {
		Default []City `json:"default"`
	}
	if err := json.Unmarshal(m.Configuration.Properties["cities"], &citiesSchema); err != nil {
		t.Fatal(err)
	}
	if len(citiesSchema.Default) != 1 || citiesSchema.Default[0].Name != "上海" ||
		citiesSchema.Default[0].Latitude != 31.22 || citiesSchema.Default[0].Longitude != 121.46 {
		t.Fatalf("cities default %+v", citiesSchema.Default)
	}
	var tu struct{ Default string }
	_ = json.Unmarshal(m.Configuration.Properties["temperature_unit"], &tu)
	var wu struct{ Default string }
	_ = json.Unmarshal(m.Configuration.Properties["wind_unit"], &wu)
	if tu.Default != "celsius" || wu.Default != "kmh" {
		t.Fatalf("unit defaults %q %q", tu.Default, wu.Default)
	}

	// The three endpoint hosts are declared as the code reaches them.
	for _, base := range []string{forecastBase, airBase, geoBase} {
		u, err := url.Parse(base)
		if err != nil || !slices.Contains(m.Permissions.Network, u.Host) {
			t.Fatalf("endpoint %s not in network %v", base, m.Permissions.Network)
		}
	}
}

func code(err error) string {
	var e *ext.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestConnector(t *testing.T) {
	f := newFakeSite(t)
	sample(t, f)
	a := testApp(t, f, "")
	call := func(method, params string) (map[string]any, error) {
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
	for _, params := range []string{`{"contribution": "weather"}`, ""} {
		if st, err := call("status", params); err != nil || st["state"] != "ok" || st["account"] == "" {
			t.Fatalf("status %s: %v %v", params, st, err)
		}
	}
	if _, err := call("status", `{"contribution": "other"}`); code(err) != wire.ErrorCodeNoContribution {
		t.Fatalf("another contribution: %v", err)
	}
	if _, err := call("sync", `{}`); code(err) != wire.ErrorCodeUnknownMethod {
		t.Fatalf("an unknown method: %v", err)
	}
	if _, err := call("status", `[`); err == nil {
		t.Fatal("params that are not JSON")
	}

	list, err := call("tools.list", `{"contribution": "weather"}`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, x := range list["tools"].([]any) {
		tool := x.(map[string]any)
		names = append(names, tool["name"].(string))
		if tool["input_schema"].(map[string]any)["type"] != "object" || tool["description"] == "" {
			t.Fatalf("tool %v", tool)
		}
	}
	if !slices.Equal(names, []string{"weather_now", "weather_forecast", "weather_air"}) {
		t.Fatalf("tools %v", names)
	}
	res, err := call("tools.call", `{"contribution": "weather", "name": "weather_now", "args": {}}`)
	if err != nil || !strings.Contains(res["text"].(string), "上海") {
		t.Fatalf("tools.call: %v %v", res, err)
	}
	for _, params := range []string{`{"name": "nope"}`, `{"name": "weather_now", "args": "x"}`} {
		if _, err := call("tools.call", params); err == nil {
			t.Errorf("tools.call %s answered", params)
		}
	}
}
