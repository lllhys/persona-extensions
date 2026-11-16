package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestDegradedConfigurations: design 9.1, the four configurations a new user may have: no hand-written memory, a
// kb.owner with the name only, a single message source, no agents. Each runs the days and the week without an
// error, the sources card says what is missing instead of failing, and nothing is made up to fill the gap.
func TestDegradedConfigurations(t *testing.T) {
	cmd := fakeCLI(t)
	cases := []struct {
		name  string
		setup func(t *testing.T, dir, kb string) []string
		check func(t *testing.T, st *store.State)
	}{
		{"no hand-written memory", func(t *testing.T, dir, kb string) []string {
			empty := filepath.Join(dir, "memory")
			if err := os.MkdirAll(empty, 0o700); err != nil {
				t.Fatal(err)
			}
			return []string{"-memory-dir", empty}
		}, func(t *testing.T, st *store.State) {
			for _, e := range st.Entries {
				if e.Origin == entry.Handwritten {
					t.Errorf("a hand-written entry from an empty directory: %+v", e)
				}
			}
		}},
		{"kb.owner with the name only", func(t *testing.T, dir, kb string) []string {
			if err := os.WriteFile(filepath.Join(kb, "owner.json"), []byte(`{"name":"Sam Rivera"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			return nil
		}, nil},
		{"one message source", func(t *testing.T, dir, kb string) []string {
			ents, _ := os.ReadDir(filepath.Join(kb, "chats"))
			for _, e := range ents {
				if !strings.HasPrefix(e.Name(), "lark-") {
					if err := os.RemoveAll(filepath.Join(kb, "chats", e.Name())); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, k := range []string{"events", "meetings"} {
				if err := os.RemoveAll(filepath.Join(kb, k)); err != nil {
					t.Fatal(err)
				}
			}
			return nil
		}, func(t *testing.T, st *store.State) {
			for _, e := range st.Entries {
				for _, ev := range e.Evidence {
					if c := connector(ev.Ref); c != "" && c != "lark" {
						t.Errorf("evidence from a source not connected: %+v", ev)
					}
				}
			}
		}},
		{"no agents", func(t *testing.T, dir, kb string) []string { return nil }, func(t *testing.T, st *store.State) {
			for k, s := range st.Sources {
				if strings.HasPrefix(k, entry.SrcAgent+"/") && s.Units > 0 {
					t.Errorf("agent units read with no agents: %+v", s)
				}
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			kb := filepath.Join(dir, "kb")
			copyTree(t, "testdata/sample", kb)
			extra := c.setup(t, dir, kb)
			state, out := filepath.Join(dir, "state"), filepath.Join(dir, "out")
			args := append([]string{"-kb", kb, "-from", "2026-11-03", "-to", "2026-11-04", "-tz", "UTC", "-state", state, "-out", out,
				"-model-cmd", cmd, "-model", "model-x", "-workers", "1", "-week"}, extra...)
			if err := offline(args); err != nil {
				t.Fatal(err)
			}
			st, err := store.Open(state)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range st.S.Runs {
				for _, n := range r.Notes {
					if strings.Contains(n, "没跑完") || strings.Contains(n, "没写完") {
						t.Errorf("%s %s: %s", r.Kind, r.Period, n)
					}
				}
			}
			if len(st.S.Readings) == 0 {
				t.Error("no readings")
			}
			var src map[string]any
			raw, err := os.ReadFile(filepath.Join(out, "sources.json"))
			if err != nil || json.Unmarshal(raw, &src) != nil {
				t.Fatalf("sources.json: %v", err)
			}
			if c.check != nil {
				c.check(t, st.S)
			}
		})
	}
}
