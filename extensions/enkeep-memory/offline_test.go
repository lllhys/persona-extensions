package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestOfflineRerun: the offline subcommand runs the daily passes over a copy of a knowledge base plus a synthetic
// conversation transcript with a stand-in model command, writes the reports and export.json, admits the Persona
// conversation, and leaves both input directories as they were.
func TestOfflineRerun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	dir := t.TempDir()
	cmd := filepath.Join(dir, "fake-claude")
	script := "#!/bin/sh\ncat > /dev/null\necho '{\"is_error\":false,\"structured_output\":{},\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n"
	if err := os.WriteFile(cmd, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	convs := filepath.Join(dir, "convs")
	rec := func(typ, at, text string) string {
		b, _ := json.Marshal(map[string]any{"type": typ, "timestamp": at, "sessionId": "abcdef0123456789",
			"message": map[string]any{"role": typ, "content": text}})
		return string(b)
	}
	transcript := rec("user", "2026-11-03T18:00:00Z", "以后写周报先写结论，再写过程") + "\n" + rec("assistant", "2026-11-03T18:00:10Z", "明白") + "\n"
	if err := os.MkdirAll(convs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convs, "abcdef0123456789.jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "origins.json"), []byte(`{"abcdef0123456789":"user"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	kb := "testdata/sample"
	before := listing(t, kb) + listing(t, convs)
	out := filepath.Join(dir, "out")
	err := offline([]string{"-kb", kb, "-conversations", convs, "-conversation-origins", filepath.Join(dir, "origins.json"), "-from", "2026-11-03", "-to", "2026-11-04", "-tz", "UTC",
		"-state", filepath.Join(dir, "state"), "-out", out, "-model-cmd", cmd, "-model", "model-x", "-workers", "3", "-week"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"daily-2026-11-03.md", "daily-2026-11-04.md", "weekly-2026-W45.md", "export.json"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(out, "export.json"))
	if err != nil {
		t.Fatal(err)
	}
	var x struct {
		Funnel map[string]struct {
			Chats   map[string]int `json:"chats"`
			Sources map[string]int `json:"sources"`
		} `json:"funnel"`
		Items   []json.RawMessage `json:"items"`
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(raw, &x); err != nil {
		t.Fatal(err)
	}
	if f := x.Funnel["2026-11-03"]; f.Sources["persona/assistant"] != 1 || f.Chats["C1_persona_conversation→include"] != 1 {
		t.Errorf("the Persona conversation was not admitted: %+v", f)
	}
	if after := listing(t, kb) + listing(t, convs); after != before {
		t.Errorf("an input directory changed")
	}
}

func listing(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			b.WriteString(p + " " + info.ModTime().String() + "\n")
		}
		return nil
	})
	return b.String()
}
