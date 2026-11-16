package model

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CLI runs each request through a local Claude Code command line in print mode, for offline reruns on a copy of the
// data: no tools, no MCP servers, no session kept, in a fresh empty working directory, the answer held to the
// request's schema. It is never used by the extension the hub runs.
type CLI struct {
	Command  string          // default "claude"
	Models   map[Tier]string // the model of each tier; a tier not in it uses Small's, else the command's default
	Effort   string          // --effort, if set
	CacheDir string          // if set, answers are kept here by request, so an unchanged rerun calls nothing
	Timeout  time.Duration   // one try; default 10 minutes
}

// cliResult is the part of the command's JSON output read.
type cliResult struct {
	IsError          bool            `json:"is_error"`
	Subtype          string          `json:"subtype"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	Usage            struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// cached is an answer kept on disk.
type cached struct {
	Text  string `json:"text"`
	Model string `json:"model"`
}

func (c CLI) model(t Tier) string {
	if m := c.Models[t]; m != "" {
		return m
	}
	return c.Models[Small]
}

// key is the cache key of a request on a model.
func key(model string, r Request) string {
	h := sha256.New()
	for _, p := range []string{model, r.System, r.User, string(r.Schema)} {
		fmt.Fprintf(h, "%d:%s\x00", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Complete answers a request, from the cache when it holds the same request (no tokens then), else by running the
// command, once more if the first try fails.
func (c CLI) Complete(ctx context.Context, r Request) (Response, error) {
	mdl := c.model(r.Tier)
	var path string
	if c.CacheDir != "" {
		path = filepath.Join(c.CacheDir, key(mdl, r)+".json")
		if b, err := os.ReadFile(path); err == nil {
			var a cached
			if json.Unmarshal(b, &a) == nil && a.Text != "" {
				return Response{Text: a.Text, Model: a.Model}, nil
			}
		}
	}
	resp, err := c.run(ctx, mdl, r)
	if err != nil && ctx.Err() == nil {
		resp, err = c.run(ctx, mdl, r)
	}
	if err != nil {
		return Response{}, err
	}
	if path != "" {
		if err := os.MkdirAll(c.CacheDir, 0o700); err != nil {
			return resp, err
		}
		b, _ := json.Marshal(cached{Text: resp.Text, Model: resp.Model})
		if err := writeFile(path, b); err != nil {
			return resp, err
		}
	}
	return resp, nil
}

func (c CLI) run(ctx context.Context, mdl string, r Request) (Response, error) {
	cmd := c.Command
	if cmd == "" {
		cmd = "claude"
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"-p"}
	if mdl != "" {
		args = append(args, "--model", mdl)
	}
	if c.Effort != "" {
		args = append(args, "--effort", c.Effort)
	}
	args = append(args, "--system-prompt", r.System, "--tools", "", "--strict-mcp-config", "--no-session-persistence",
		"--output-format", "json")
	if len(r.Schema) > 0 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, r.Schema); err != nil {
			return Response{}, fmt.Errorf("schema: %w", err)
		}
		args = append(args, "--json-schema", compact.String())
	}
	wd, err := os.MkdirTemp("", "enkeep-memory-model-")
	if err != nil {
		return Response{}, err
	}
	defer os.RemoveAll(wd)
	x := exec.CommandContext(ctx, cmd, args...)
	x.Dir = wd
	x.Stdin = strings.NewReader(r.User)
	var stdout, stderr bytes.Buffer
	x.Stdout, x.Stderr = &stdout, &stderr
	if err := x.Run(); err != nil {
		return Response{}, fmt.Errorf("%s (%s): %w: %s", cmd, r.Step, err, tail(stderr.String()+stdout.String(), 400))
	}
	var out cliResult
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return Response{}, fmt.Errorf("%s (%s): output is not JSON: %s", cmd, r.Step, tail(stdout.String(), 400))
	}
	if out.IsError {
		return Response{}, fmt.Errorf("%s (%s): %s %s", cmd, r.Step, out.Subtype, tail(out.Result, 400))
	}
	text := out.Result
	if len(out.StructuredOutput) > 0 && string(out.StructuredOutput) != "null" {
		var v any
		if err := json.Unmarshal(out.StructuredOutput, &v); err != nil {
			return Response{}, fmt.Errorf("%s (%s): structured_output: %w", cmd, r.Step, err)
		}
		b, _ := json.Marshal(v)
		text = string(b)
	}
	if strings.TrimSpace(text) == "" {
		return Response{}, errors.New(cmd + " (" + r.Step + "): empty answer")
	}
	u := out.Usage
	return Response{Text: text, Model: mdl, InTokens: u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
		OutTokens: u.OutputTokens}, nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n:])
	}
	return s
}

func writeFile(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
