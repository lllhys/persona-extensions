package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A watches.json that cannot be parsed is moved aside to watches.json.corrupt and logged, not read as no rules and
// then replaced by the next rule the assistant adds; a missing one is not an error.
func TestWatchesUnreadableKeptAside(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "watches.json")
	const broken = `[{"id": "w1", "name": "门开了", "entity_id": "binary_sensor.front_door"`
	if err := os.WriteFile(file, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs []string
	a := newApp(func(f string, args ...any) { logs = append(logs, fmt.Sprintf(f, args...)) })
	a.watch.load(dir)
	if n := len(a.watch.rules()); n != 0 {
		t.Fatalf("%d rules from a broken file", n)
	}
	a.watch.mu.Lock()
	a.watch.mine = append(a.watch.mine, Rule{ID: "w2", Name: "灯", Entity: "light.desk", Source: "assistant"})
	a.watch.mu.Unlock()
	if err := a.watch.save(); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(file + ".corrupt"); err != nil || string(raw) != broken {
		t.Fatalf("the unreadable file was not kept aside: %q %v", raw, err)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "watches.json") || !strings.Contains(logs[0], ".corrupt") {
		t.Errorf("logged %q, want one line naming the file and where it went", logs)
	}

	// a missing file: no rules, nothing logged, nothing moved
	logs = nil
	empty := t.TempDir()
	b := newApp(func(f string, args ...any) { logs = append(logs, fmt.Sprintf(f, args...)) })
	b.watch.load(empty)
	if len(b.watch.rules()) != 0 || len(logs) != 0 {
		t.Errorf("a missing file: rules %v, logged %q", b.watch.rules(), logs)
	}
	if _, err := os.Stat(filepath.Join(empty, "watches.json.corrupt")); !os.IsNotExist(err) {
		t.Errorf("a missing file was moved aside: %v", err)
	}
}
