package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Pi runs each request through the pi coding agent's print mode on a model gateway it is already signed in to, for
// an offline rerun on a copy of the data: no tools, no MCP servers, no extensions or skills, no session kept, in a fresh
// empty working directory. The answer is asked for as one JSON object and read back from the text. The model of each
// tier is the one the hub would map the tier to. It never touches a credential: the command has its own sign-in. It is
// never used by the extension the hub runs.
type Pi struct {
	Command  string          // default "pi"
	Provider string          // --provider; empty: the command's own default
	Models   map[Tier]string // the model of each tier; a tier not in it uses Small's
	Thinking string          // --thinking (default "off")
	CacheDir string          // if set, answers are kept here by request, so an unchanged rerun calls nothing
	Timeout  time.Duration   // one try; default 10 minutes
	Tries    int             // default 3: a try whose answer is not one JSON object is asked again
}

func (p Pi) model(t Tier) string {
	if m := p.Models[t]; m != "" {
		return m
	}
	return p.Models[Small]
}

// Complete answers a request, from the cache when it holds the same request, else by running the command.
func (p Pi) Complete(ctx context.Context, r Request) (Response, error) {
	mdl := p.model(r.Tier)
	var path string
	if p.CacheDir != "" {
		path = filepath.Join(p.CacheDir, key(mdl, r)+".json")
		if b, err := os.ReadFile(path); err == nil {
			var a cached
			if json.Unmarshal(b, &a) == nil && a.Text != "" {
				return Response{Text: a.Text, Model: a.Model}, nil
			}
		}
	}
	tries := p.Tries
	if tries <= 0 {
		tries = 3
	}
	var resp Response
	var err error
	for i := 0; i < tries; i++ {
		if resp, err = p.run(ctx, mdl, r); err == nil || ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		return Response{}, err
	}
	if path != "" {
		if err := os.MkdirAll(p.CacheDir, 0o700); err != nil {
			return resp, err
		}
		b, _ := json.Marshal(cached{Text: resp.Text, Model: resp.Model})
		if err := writeFile(path, b); err != nil {
			return resp, err
		}
	}
	return resp, nil
}

func (p Pi) run(ctx context.Context, mdl string, r Request) (Response, error) {
	cmd := p.Command
	if cmd == "" {
		cmd = "pi"
	}
	provider, thinking := p.Provider, p.Thinking
	if thinking == "" {
		thinking = "off"
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	msg := r.User
	if len(r.Schema) > 0 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, r.Schema); err != nil {
			return Response{}, fmt.Errorf("schema: %w", err)
		}
		msg += "\n\nAnswer with one JSON object only: no code fence, no commentary. It must match this JSON schema, every required key present (empty arrays when there is nothing):\n" + compact.String()
	}
	args := []string{"-p", "--thinking", thinking, "--no-tools", "--no-session", "--no-extensions", "--no-context-files",
		"--no-skills", "--no-prompt-templates", "--no-mcp", "--system-prompt", r.System}
	if provider != "" {
		args = append(args, "--provider", provider)
	}
	if mdl != "" {
		args = append(args, "--model", mdl)
	}
	args = append(args, "--", msg)
	wd, err := os.MkdirTemp("", "enkeep-memory-model-")
	if err != nil {
		return Response{}, err
	}
	defer os.RemoveAll(wd)
	x := exec.CommandContext(ctx, cmd, args...)
	x.Dir = wd
	var stdout, stderr bytes.Buffer
	x.Stdout, x.Stderr = &stdout, &stderr
	if err := x.Run(); err != nil {
		out := tail(stderr.String()+stdout.String(), 400)
		if rateLimited(out) {
			// The gateway has no capacity now: the model is unavailable, as the hub says when model.complete is busy,
			// so a replay stops on the day instead of reading it without the model.
			return Response{}, fmt.Errorf("%w: %s (%s): %s", ErrUnavailable, cmd, r.Step, out)
		}
		return Response{}, fmt.Errorf("%s (%s): %w: %s", cmd, r.Step, err, out)
	}
	text, ok := jsonObject(stdout.String())
	if !ok {
		return Response{}, errors.New(cmd + " (" + r.Step + "): the answer is not one JSON object: " + tail(stdout.String(), 300))
	}
	return Response{Text: text, Model: mdl, InTokens: (len(r.System) + len(msg)) / 3, OutTokens: len(text) / 3}, nil
}

// jsonObject finds the JSON object in a model's text: the whole text, or what is between the first "{" and the last
// "}" (a code fence or a word of introduction around it).
func jsonObject(s string) (string, bool) {
	s = strings.TrimSpace(s)
	valid := func(t string) bool {
		var m map[string]any
		return json.Unmarshal([]byte(t), &m) == nil
	}
	if valid(s) {
		return s, true
	}
	a, b := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if a >= 0 && b > a && valid(s[a:b+1]) {
		return s[a : b+1], true
	}
	return "", false
}

// rateLimited: the command's output says the gateway refused for capacity (HTTP 429, a rate limit), not the request.
func rateLimited(out string) bool {
	return strings.Contains(out, " 429 ") || strings.HasPrefix(strings.TrimSpace(out), "429 ") || strings.Contains(out, "rate_limit_error")
}
