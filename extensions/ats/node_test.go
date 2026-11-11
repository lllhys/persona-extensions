package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestWorker(t *testing.T, repos ...string) (*nodeWorker, chan acceptResult) {
	t.Helper()
	done := make(chan acceptResult, 4)
	w := newNodeWorker(t.TempDir(), repos, func(event string, params any) {
		if res, ok := params.(acceptResult); ok && event == "ats.accepted" {
			done <- res
		}
	})
	return w, done
}

func waitResult(t *testing.T, done chan acceptResult) acceptResult {
	t.Helper()
	select {
	case res := <-done:
		return res
	case <-time.After(20 * time.Second):
		t.Fatal("the acceptance command did not report")
		return acceptResult{}
	}
}

func TestPrepareChecksTheRepositoryOutAtTheRef(t *testing.T) {
	root, repo := newTestRepo(t)
	w, _ := newTestWorker(t, root)
	first := runGit(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "next.txt"), []byte("next\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "second")

	res, err := w.prepare(t.Context(), prepareParams{TaskID: "task_a", Attempt: 1, Repo: repo, Ref: first})
	if err != nil {
		t.Fatal(err)
	}
	if res.BaseSHA != first || res.Branch != "ats/task_a-1" || res.Path != filepath.Join(w.data, "worktrees", "task_a-1") {
		t.Fatalf("prepare: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(res.Path, "next.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the worktree is not at the ref the task named")
	}
	if got := runGit(t, res.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != "ats/task_a-1" {
		t.Fatalf("the worktree is on %s", got)
	}
	// Asked again (the hub program restarted in between): the same worktree, not an error.
	again, err := w.prepare(t.Context(), prepareParams{TaskID: "task_a", Attempt: 1, Repo: repo, Ref: first})
	if err != nil || again != res {
		t.Fatalf("prepare again: %+v %v", again, err)
	}

	ro, err := w.prepare(t.Context(), prepareParams{TaskID: "task_b", Attempt: 2, Repo: repo, Readonly: true})
	if err != nil {
		t.Fatal(err)
	}
	if ro.Branch != "" || ro.BaseSHA != runGit(t, repo, "rev-parse", "HEAD") {
		t.Fatalf("read-only prepare: %+v", ro)
	}
}

func TestPrepareRefusesWhatTheAllowlistDoesNotCover(t *testing.T) {
	root, repo := newTestRepo(t)
	_, outside := newTestRepo(t)
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	w, _ := newTestWorker(t, root)
	for name, tc := range map[string]struct {
		p    prepareParams
		want string
	}{
		"another directory":     {prepareParams{TaskID: "t", Attempt: 1, Repo: outside}, "repo_not_allowed"},
		"a link leading out":    {prepareParams{TaskID: "t", Attempt: 1, Repo: link}, "repo_not_allowed"},
		"a relative path":       {prepareParams{TaskID: "t", Attempt: 1, Repo: "app"}, "repo:"},
		"a missing repository":  {prepareParams{TaskID: "t", Attempt: 1, Repo: filepath.Join(root, "none")}, "repo:"},
		"an unknown ref":        {prepareParams{TaskID: "t", Attempt: 1, Repo: repo, Ref: "no-such-branch"}, "ref_not_found"},
		"a ref that is a flag":  {prepareParams{TaskID: "t", Attempt: 1, Repo: repo, Ref: "--upload-pack=x"}, "ref must be"},
		"a task id with a path": {prepareParams{TaskID: "../t", Attempt: 1, Repo: repo}, "not valid"},
		"no attempt":            {prepareParams{TaskID: "t", Repo: repo}, "not valid"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := w.prepare(t.Context(), tc.p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
	if entries, _ := os.ReadDir(filepath.Join(w.data, "worktrees")); len(entries) != 0 {
		t.Fatalf("a refused request left %d worktrees", len(entries))
	}

	empty, _ := newTestWorker(t)
	if _, err := empty.prepare(t.Context(), prepareParams{TaskID: "t", Attempt: 1, Repo: repo}); err == nil || !strings.Contains(err.Error(), "repo_not_allowed") {
		t.Fatalf("an empty allowlist allowed a repository: %v", err)
	}
}

func TestAcceptRunsTheCommandInTheWorktreeWithoutTheHubsEnvironment(t *testing.T) {
	t.Setenv("PERSONA_EXAMPLE_TOKEN", "not-for-the-command")
	root, repo := newTestRepo(t)
	w, done := newTestWorker(t, root)
	if err := w.accept(acceptParams{TaskID: "task_a", Attempt: 1, Command: "true"}); err == nil || !strings.Contains(err.Error(), "worktree_missing") {
		t.Fatalf("acceptance without a worktree: %v", err)
	}
	res, err := w.prepare(t.Context(), prepareParams{TaskID: "task_a", Attempt: 1, Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.accept(acceptParams{TaskID: "task_a", Attempt: 1, Command: " "}); err == nil {
		t.Fatal("an empty command was taken")
	}
	cmd := `echo "token=[${PERSONA_EXAMPLE_TOKEN:-}]"; pwd; echo dirty > note.txt; exit 7`
	if err := w.accept(acceptParams{TaskID: "task_a", Attempt: 1, Command: cmd}); err != nil {
		t.Fatal(err)
	}
	got := waitResult(t, done)
	if !got.Done || got.ExitCode != 7 || got.TimedOut || !got.Dirty || got.HeadSHA != res.BaseSHA {
		t.Fatalf("result: %+v", got)
	}
	if !strings.Contains(got.OutputTail, "token=[]") || !strings.Contains(got.OutputTail, res.Path) {
		t.Fatalf("output: %s", got.OutputTail)
	}
	// The result is kept for a hub program that missed the event.
	status, err := w.acceptStatus("task_a", 1)
	if err != nil || status != got {
		t.Fatalf("status: %+v %v", status, err)
	}
	// An attempt this program knows nothing of: its command did not survive.
	lost, err := w.acceptStatus("task_zz", 1)
	if err != nil || !lost.Done || lost.ExitCode != -1 || !strings.Contains(lost.Error, "lost") {
		t.Fatalf("status of an unknown attempt: %+v %v", lost, err)
	}
}

func TestAcceptTimeoutKillsEverythingTheCommandStarted(t *testing.T) {
	root, repo := newTestRepo(t)
	w, done := newTestWorker(t, root)
	res, err := w.prepare(t.Context(), prepareParams{TaskID: "task_a", Attempt: 1, Repo: repo, Readonly: true})
	if err != nil {
		t.Fatal(err)
	}
	// A child that would write a file after the timeout, were it left alive.
	cmd := `(sleep 3; echo late > late.txt) & echo started; sleep 30`
	start := time.Now()
	if err := w.accept(acceptParams{TaskID: "task_a", Attempt: 1, Command: cmd, TimeoutSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	if st, err := w.acceptStatus("task_a", 1); err != nil || st.Done {
		t.Fatalf("status while running: %+v %v", st, err)
	}
	got := waitResult(t, done)
	if !got.TimedOut || got.ExitCode == 0 || time.Since(start) > 10*time.Second || !strings.Contains(got.OutputTail, "started") {
		t.Fatalf("result after %s: %+v", time.Since(start), got)
	}
	time.Sleep(3500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(res.Path, "late.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a process the command started outlived the timeout")
	}
}

func TestReleaseKeepsWorkAndDropsWhatIsEmpty(t *testing.T) {
	root, repo := newTestRepo(t)
	w, _ := newTestWorker(t, root)
	ctx := context.Background()
	prep := func(id string) prepareResult {
		res, err := w.prepare(ctx, prepareParams{TaskID: id, Attempt: 1, Repo: repo})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	branches := func() string { return runGit(t, repo, "branch", "--list", "ats/*", "--format=%(refname:short)") }

	// Nothing done: the worktree and the branch go.
	a := prep("task_a")
	res, err := w.release(ctx, releaseParams{TaskID: "task_a", Attempt: 1, Repo: repo, BaseSHA: a.BaseSHA})
	if err != nil || !res.Removed || res.Branch != "" || branches() != "" {
		t.Fatalf("release of an untouched attempt: %+v %v, branches %q", res, err, branches())
	}

	// A commit: the worktree goes, the branch stays.
	b := prep("task_b")
	if err := os.WriteFile(filepath.Join(b.Path, "work.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, b.Path, "add", ".")
	runGit(t, b.Path, "commit", "-q", "-m", "work")
	head := runGit(t, b.Path, "rev-parse", "HEAD")
	res, err = w.release(ctx, releaseParams{TaskID: "task_b", Attempt: 1, Repo: repo, BaseSHA: b.BaseSHA})
	if err != nil || !res.Removed || res.Branch != "ats/task_b-1" || res.HeadSHA != head || branches() != "ats/task_b-1" {
		t.Fatalf("release of an attempt with a commit: %+v %v, branches %q", res, err, branches())
	}
	if _, err := os.Stat(b.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the worktree is still there")
	}

	// Work that is not committed: everything stays where it is.
	c := prep("task_c")
	if err := os.WriteFile(filepath.Join(c.Path, "draft.txt"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = w.release(ctx, releaseParams{TaskID: "task_c", Attempt: 1, Repo: repo, BaseSHA: c.BaseSHA})
	if err != nil || res.Removed || res.Kept != c.Path {
		t.Fatalf("release of a dirty worktree: %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(c.Path, "draft.txt")); err != nil {
		t.Fatal("uncommitted work was removed")
	}

	// Released twice, or never prepared: nothing to do.
	if res, err := w.release(ctx, releaseParams{TaskID: "task_a", Attempt: 1, Repo: repo}); err != nil || !res.Removed {
		t.Fatalf("second release: %+v %v", res, err)
	}
}

func TestNodeRPCNamesItsMethods(t *testing.T) {
	root, repo := newTestRepo(t)
	w, _ := newTestWorker(t, root)
	out, err := w.rpc(t.Context(), "prepare", []byte(`{"task_id":"task_a","attempt":1,"repo":"`+repo+`","readonly":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if res, ok := out.(prepareResult); !ok || res.Path == "" {
		t.Fatalf("prepare over rpc: %#v", out)
	}
	if _, err := w.rpc(t.Context(), "run_anything", []byte(`{}`)); err == nil {
		t.Fatal("an unknown method was answered")
	}
}
