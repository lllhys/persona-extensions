package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/pluginkit"
)

// alerts.json and watchlist.json that cannot be parsed are moved aside to <file>.corrupt and logged, not read as
// empty (or as the configured watchlist) and then replaced by the next alert or watchlist change; missing ones are
// not an error.
func TestUnreadableStateKeptAside(t *testing.T) {
	dir := t.TempDir()
	const brokenAlerts = `[{"id": "a1", "ref": "cn:600519.SH", "kind": "above", "value": 1450`
	const brokenWatch = `["cn:600519.SH", "us:AAPL"`
	for name, body := range map[string]string{"alerts.json": brokenAlerts, "watchlist.json": brokenWatch} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var logs []string
	p := newPlugin(func(f string, args ...any) { logs = append(logs, fmt.Sprintf(f, args...)) })
	var ip pluginkit.InitParams
	ip.Plugin.DataDir = dir
	if err := p.configure(ip); err != nil {
		t.Fatal(err)
	}
	if n := len(p.alerts.all()); n != 0 {
		t.Fatalf("%d alerts from a broken file", n)
	}
	if err := p.alerts.add(Alert{ID: "a2", Ref: "us:AAPL", Kind: "below", Value: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.addWatch(Ref{Source: "us", Symbol: "MSFT"}); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"alerts.json": brokenAlerts, "watchlist.json": brokenWatch} {
		if raw, err := os.ReadFile(filepath.Join(dir, name+".corrupt")); err != nil || string(raw) != body {
			t.Errorf("%s was not kept aside: %q %v", name, raw, err)
		}
		if !strings.Contains(strings.Join(logs, "\n"), name+".corrupt") {
			t.Errorf("nothing logged for %s: %q", name, logs)
		}
	}

	// missing files: nothing logged, nothing moved
	logs = nil
	empty := t.TempDir()
	q := newPlugin(func(f string, args ...any) { logs = append(logs, fmt.Sprintf(f, args...)) })
	ip.Plugin.DataDir = empty
	if err := q.configure(ip); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 0 {
		t.Errorf("missing files logged %q", logs)
	}
	if m, _ := filepath.Glob(filepath.Join(empty, "*.corrupt")); len(m) != 0 {
		t.Errorf("missing files moved aside: %v", m)
	}
}
