package model

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONObject(t *testing.T) {
	for in, want := range map[string]bool{`{"a":1}`: true, "```json\n{\"a\":1}\n```": true, "Here: {\"a\":[1,2]} done": true, "no json": false, `[1,2]`: false, `{"a":`: false} {
		if _, ok := jsonObject(in); ok != want {
			t.Errorf("%q: %v", in, ok)
		}
	}
}

// Pi asks the command once with the system prompt and the input, never gives it a tool, takes the JSON object out of
// the text, asks again when the answer is not one, and keeps answers in the cache.
func TestPiRunsTheCommand(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "pi")
	body := "#!/bin/sh\necho x >> " + log + "\nfor a in \"$@\"; do echo \"$a\" >> " + log + ".args; done\nif [ $(wc -l < " + log + ") -lt 2 ]; then echo 'sorry'; else echo '```json'; echo '{\"ok\":true}'; echo '```'; fi\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	p := Pi{Command: script, Provider: "provider-x", Models: map[Tier]string{Small: "m-small", Medium: "m-medium"}, CacheDir: filepath.Join(dir, "cache")}
	r := Request{Step: "daily", Tier: Medium, System: "SYS", User: "INPUT", Schema: []byte(`{"type":"object"}`)}
	got, err := p.Complete(context.Background(), r)
	if err != nil || got.Text != `{"ok":true}` || got.Model != "m-medium" {
		t.Fatalf("%+v %v", got, err)
	}
	if b, _ := os.ReadFile(log); strings.Count(string(b), "x") != 2 {
		t.Errorf("calls: %q", b)
	}
	args, _ := os.ReadFile(log + ".args")
	for _, want := range []string{"--no-tools", "--no-mcp", "--no-session", "--system-prompt", "SYS", "m-medium", "provider-x"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("no %q in the arguments: %s", want, args)
		}
	}
	if _, err := p.Complete(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); strings.Count(string(b), "x") != 2 {
		t.Errorf("the cache was not used: %q", b)
	}
}

func TestRateLimited(t *testing.T) {
	for out, want := range map[string]bool{
		`429 {"error":{"code":"rate_limit_error","message":"upstream account capacity is temporarily unavailable"}}`: true,
		`400 {"error":{"message":"bad request"}}`: false,
		`the answer is not JSON`:                  false,
	} {
		if got := rateLimited(out); got != want {
			t.Errorf("rateLimited(%q) = %v, want %v", out, got, want)
		}
	}
}
