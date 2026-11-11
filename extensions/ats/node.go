package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

// The program on a computer (the manifest's node section). It does the three things an attempt needs on the
// computer where it runs, and nothing else:
//
//	ats.prepare  check the repository out into a worktree of its own, at the commit the ref names now
//	ats.accept   run the task's acceptance command there (started here, reported with the event ats.accepted)
//	ats.release  remove the worktree again, unless it holds work that is not committed
//
// The agent itself is a Persona session the hub program starts in the worktree; this program never runs one.
// Repositories outside the allowlist in the settings are refused here too, whatever the hub program asked for.

const (
	outputTailBytes   = 4 << 10
	defaultAcceptTime = 10 * time.Minute
	maxAcceptTime     = 60 * time.Minute
)

var safeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)

type nodeWorker struct {
	data   string
	notify func(event string, params any)

	mu      sync.Mutex
	repos   []string
	running map[string]context.CancelFunc
}

type prepareParams struct {
	TaskID   string `json:"task_id"`
	Attempt  int    `json:"attempt"`
	Repo     string `json:"repo"`
	Ref      string `json:"ref"`
	Readonly bool   `json:"readonly"`
}

type prepareResult struct {
	Path    string `json:"path"`
	BaseSHA string `json:"base_sha"`
	Branch  string `json:"branch,omitempty"`
}

type acceptParams struct {
	TaskID         string `json:"task_id"`
	Attempt        int    `json:"attempt"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// acceptResult is what the acceptance command left: the evidence of the attempt.
type acceptResult struct {
	TaskID     string `json:"task_id"`
	Attempt    int    `json:"attempt"`
	Done       bool   `json:"done"`
	ExitCode   int    `json:"exit_code"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	Error      string `json:"error,omitempty"`
	OutputTail string `json:"output_tail,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	HeadSHA    string `json:"head_sha,omitempty"`
	Dirty      bool   `json:"dirty,omitempty"`
}

type releaseParams struct {
	TaskID  string `json:"task_id"`
	Attempt int    `json:"attempt"`
	Repo    string `json:"repo"`
	BaseSHA string `json:"base_sha"`
}

type releaseResult struct {
	Removed bool   `json:"removed"`
	Kept    string `json:"kept,omitempty"` // the worktree, when it was left because of uncommitted work
	Branch  string `json:"branch,omitempty"`
	HeadSHA string `json:"head_sha,omitempty"`
}

func newNodeWorker(data string, repos []string, notify func(string, any)) *nodeWorker {
	return &nodeWorker{data: data, repos: repos, notify: notify, running: map[string]context.CancelFunc{}}
}

func attemptKey(taskID string, attempt int) (string, error) {
	if !safeIDPattern.MatchString(taskID) || attempt <= 0 {
		return "", errors.New("task_id or attempt is not valid")
	}
	return fmt.Sprintf("%s-%d", taskID, attempt), nil
}

func (n *nodeWorker) worktree(key string) string { return filepath.Join(n.data, "worktrees", key) }
func (n *nodeWorker) resultFile(key string) string {
	return filepath.Join(n.data, "results", key+".json")
}

// expandHome turns ~ and ~/x into paths under this computer's home directory.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// realPath is the path with ~ expanded and symlinks followed, so an allowlisted directory cannot be left through
// a link.
func realPath(p string) (string, error) {
	p = expandHome(strings.TrimSpace(p))
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%s is not an absolute path", p)
	}
	return filepath.EvalSymlinks(filepath.Clean(p))
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// allowedRepo resolves repo and checks it against the allowlist: the repository is an allowlisted directory or
// lies inside one. An empty allowlist allows nothing.
func (n *nodeWorker) allowedRepo(repo string) (string, error) {
	real, err := realPath(repo)
	if err != nil {
		return "", fmt.Errorf("repo: %w", err)
	}
	n.mu.Lock()
	roots := append([]string(nil), n.repos...)
	n.mu.Unlock()
	for _, r := range roots {
		root, err := realPath(r)
		if err == nil && within(root, real) {
			return real, nil
		}
	}
	return "", errors.New("repo_not_allowed: the repository is not under a directory in the extension's \"repos\" setting")
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(cleanEnv(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("git %s: %s", args[0], firstLinePreview(s, 300))
	}
	return s, nil
}

// cleanEnv is this program's environment without what the hub put there for it.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PERSONA_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func (n *nodeWorker) prepare(ctx context.Context, p prepareParams) (prepareResult, error) {
	key, err := attemptKey(p.TaskID, p.Attempt)
	if err != nil {
		return prepareResult{}, err
	}
	repo, err := n.allowedRepo(p.Repo)
	if err != nil {
		return prepareResult{}, err
	}
	ref := strings.TrimSpace(p.Ref)
	if ref == "" {
		ref = "HEAD"
	}
	if strings.HasPrefix(ref, "-") {
		return prepareResult{}, errors.New("ref must be a commit-ish that does not start with -")
	}
	sha, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil || sha == "" {
		return prepareResult{}, fmt.Errorf("ref_not_found: %s names no commit in the repository", ref)
	}
	path := n.worktree(key)
	res := prepareResult{Path: path, BaseSHA: sha}
	if !p.Readonly {
		res.Branch = "ats/" + key
	}
	if _, err := os.Stat(path); err == nil { // prepared before the hub program restarted
		return res, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return prepareResult{}, err
	}
	args := []string{"worktree", "add", "--detach", path, sha}
	if res.Branch != "" {
		args = []string{"worktree", "add", "-b", res.Branch, path, sha}
	}
	if _, err := git(ctx, repo, args...); err != nil {
		return prepareResult{}, err
	}
	return res, nil
}

// accept starts the acceptance command and returns; the result is written beside the worktrees and sent to the
// hub program as the event ats.accepted (acceptStatus answers it too, for a hub program that missed the event).
func (n *nodeWorker) accept(p acceptParams) error {
	key, err := attemptKey(p.TaskID, p.Attempt)
	if err != nil {
		return err
	}
	if strings.TrimSpace(p.Command) == "" {
		return errors.New("command must not be empty")
	}
	dir := n.worktree(key)
	if _, err := os.Stat(dir); err != nil {
		return errors.New("worktree_missing: the attempt has no worktree on this computer")
	}
	timeout := time.Duration(p.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultAcceptTime
	}
	if timeout > maxAcceptTime {
		timeout = maxAcceptTime
	}
	n.mu.Lock()
	if _, busy := n.running[key]; busy {
		n.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	n.running[key] = cancel
	n.mu.Unlock()
	_ = os.Remove(n.resultFile(key))

	go func() {
		defer cancel()
		res := runAcceptance(ctx, dir, p.Command)
		res.TaskID, res.Attempt, res.Done = p.TaskID, p.Attempt, true
		gctx, gcancel := context.WithTimeout(context.Background(), 10*time.Second)
		if sha, err := git(gctx, dir, "rev-parse", "HEAD"); err == nil {
			res.HeadSHA = sha
		}
		if st, err := git(gctx, dir, "status", "--porcelain"); err == nil {
			res.Dirty = st != ""
		}
		gcancel()
		if raw, err := json.Marshal(res); err == nil {
			_ = os.MkdirAll(filepath.Dir(n.resultFile(key)), 0o755)
			_ = os.WriteFile(n.resultFile(key), raw, 0o600)
		}
		n.mu.Lock()
		delete(n.running, key)
		n.mu.Unlock()
		if n.notify != nil {
			n.notify("ats.accepted", res)
		}
	}()
	return nil
}

// runAcceptance runs command with sh -c in dir, in a process group of its own so a timeout kills everything it
// started, and keeps the tail of what it printed.
func runAcceptance(ctx context.Context, dir, command string) acceptResult {
	start := time.Now()
	tail := &tailBuffer{max: outputTailBytes}
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = cleanEnv()
	cmd.Stdout, cmd.Stderr = tail, tail
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	res := acceptResult{ExitCode: -1}
	if err := cmd.Start(); err != nil {
		res.Error = err.Error()
		return res
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		err = <-done
		res.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		if !res.TimedOut {
			res.Error = "canceled"
		}
	}
	res.DurationMS = time.Since(start).Milliseconds()
	res.OutputTail = tail.String()
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil && res.Error == "" && !res.TimedOut && res.ExitCode <= 0 {
		res.Error = err.Error()
	}
	return res
}

func (n *nodeWorker) acceptStatus(taskID string, attempt int) (acceptResult, error) {
	key, err := attemptKey(taskID, attempt)
	if err != nil {
		return acceptResult{}, err
	}
	n.mu.Lock()
	_, busy := n.running[key]
	n.mu.Unlock()
	res := acceptResult{TaskID: taskID, Attempt: attempt}
	if busy {
		return res, nil
	}
	raw, err := os.ReadFile(n.resultFile(key))
	if err != nil {
		// Neither running nor finished: this program restarted under the command.
		res.Done, res.ExitCode, res.Error = true, -1, "lost: the acceptance command did not survive a restart of the computer's program"
		return res, nil
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return acceptResult{}, err
	}
	return res, nil
}

// release stops the attempt's acceptance command if it still runs and removes its worktree. A worktree with
// uncommitted changes stays, and so does a branch with commits: the work is the user's to look at.
func (n *nodeWorker) release(ctx context.Context, p releaseParams) (releaseResult, error) {
	key, err := attemptKey(p.TaskID, p.Attempt)
	if err != nil {
		return releaseResult{}, err
	}
	n.mu.Lock()
	if cancel, busy := n.running[key]; busy {
		cancel()
	}
	n.mu.Unlock()
	_ = os.Remove(n.resultFile(key))
	path := n.worktree(key)
	if _, err := os.Stat(path); err != nil {
		return releaseResult{Removed: true}, nil
	}
	res := releaseResult{}
	if sha, err := git(ctx, path, "rev-parse", "HEAD"); err == nil {
		res.HeadSHA = sha
	}
	if b, err := git(ctx, path, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		res.Branch = b
	}
	if st, err := git(ctx, path, "status", "--porcelain"); err == nil && st != "" {
		res.Kept = path
		return res, nil
	}
	repo, err := n.allowedRepo(p.Repo)
	if err != nil {
		return res, err
	}
	if _, err := git(ctx, repo, "worktree", "remove", "--force", path); err != nil {
		return res, err
	}
	res.Removed = true
	if res.Branch != "" {
		// A branch that never moved carries nothing: drop it.
		if p.BaseSHA != "" && p.BaseSHA == res.HeadSHA && strings.HasPrefix(res.Branch, "ats/") {
			if _, err := git(ctx, repo, "branch", "-D", res.Branch); err == nil {
				res.Branch = ""
			}
		}
	}
	return res, nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.max:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.ToValidUTF8(string(b.buf), "")
}

// nodeProgram is the program of the node role.
func nodeProgram(id, version string) ext.Program {
	var w *nodeWorker
	return ext.Program{
		ID: id, Version: version, Manifest: manifest,
		Init: func(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
			var c ExtConfig
			if err := in.Config(&c); err != nil {
				return nil, err
			}
			data := strings.TrimSpace(in.Info().DataDir)
			if data == "" {
				dir, err := os.UserCacheDir()
				if err != nil {
					return nil, err
				}
				data = filepath.Join(dir, "persona-ats")
			}
			w = newNodeWorker(data, c.Repos, func(event string, params any) { _ = hub.Notify(event, params) })
			return nil, nil
		},
		Namespaces: map[string]ext.Handler{
			"ats": func(ctx context.Context, method string, params json.RawMessage) (any, error) {
				if w == nil {
					return nil, errors.New("not initialized")
				}
				return w.rpc(ctx, method, params)
			},
		},
	}
}

// rpc answers the hub program's calls (ats.<method> over nodes.call).
func (n *nodeWorker) rpc(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "prepare":
		var p prepareParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return n.prepare(ctx, p)
	case "accept":
		var p acceptParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if err := n.accept(p); err != nil {
			return nil, err
		}
		return map[string]any{"started": true}, nil
	case "accept_status":
		var p acceptParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return n.acceptStatus(p.TaskID, p.Attempt)
	case "release":
		var p releaseParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return n.release(ctx, p)
	}
	return nil, ext.UnknownMethod("ats." + method)
}
