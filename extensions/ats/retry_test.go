package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const modelErrorText = "There's an issue with the selected model (some-model). It may not exist or you may not have access to it. Run /model to pick a different model."

func TestTurnErrorClass(t *testing.T) {
	for _, tc := range []struct{ kind, text, want string }{
		{"user", modelErrorText, "config_error"},
		{"", modelErrorText, "config_error"},
		{"", "API Error: 404 model: some-model not found", "config_error"},
		{"", "invalid model name", "config_error"},
		{"user", "Not logged in · Please run /login", "auth_expired"},
		{"", "API Error: 401 authentication_error: invalid x-api-key", "auth_expired"},
		{"", "Invalid API key · Fix external API key", "auth_expired"},
		{"usage_limit", "You've hit your limit · resets 3pm", "rate_limited"},
		{"", "API Error: 429 rate_limit_error", "rate_limited"},
		{"transient", "API Error: 529 Overloaded", "rate_limited"},
		{"", "Too many requests", "rate_limited"},
		{"", "Claude usage limit reached", "rate_limited"},
		// Words that only look like the above.
		{"", "the limited edition build failed to generate", "task_failed"},
		{"", "could not read the author field of the commit", "task_failed"},
		{"", "failed to migrate the data model", "task_failed"},
		{"transient", "API Error: Connection error.", "task_failed"},
		{"user", "Prompt is too long", "task_failed"},
		{"", "", "task_failed"},
	} {
		if got := turnErrorClass(tc.kind, tc.text); got != tc.want {
			t.Errorf("turnErrorClass(%q, %q) = %s, want %s", tc.kind, tc.text, got, tc.want)
		}
	}
}

func TestErrorsARetryCannotFixEndTheTaskAfterOneAttempt(t *testing.T) {
	for name, tc := range map[string]struct {
		kind, text, class, says string
	}{
		"model":      {"user", modelErrorText, "config_error", "模型不可用"},
		"signed out": {"user", "Not logged in · Please run /login", "auth_expired", "登录失效"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			task := r.enqueue(EnqueueInput{PermissionProfile: PermissionDefault})
			r.tick()
			r.hub.set("session-1", func(s *hubSession) { s.State, s.TurnError, s.TurnErrorKind = "idle", tc.text, tc.kind })
			r.tick()
			got := r.task(task.ID)
			if got.State != StateFailed || got.FailureReason != tc.class || got.AttemptNo != 1 || got.NotBefore != nil {
				t.Fatalf("state %s reason %s attempt %d not_before %v", got.State, got.FailureReason, got.AttemptNo, got.NotBefore)
			}
			if got.RetriesLeft == 0 {
				t.Fatal("the test needs a task that had retries left")
			}
			rep := got.Reports[len(got.Reports)-1]
			if !strings.Contains(rep.Message, tc.says) || !strings.Contains(rep.Message, strings.TrimRight(tc.text, ".")) || strings.Contains(rep.Message, ".。") {
				t.Fatalf("message: %s", rep.Message)
			}
			// Later rounds start nothing more.
			r.now = r.now.Add(time.Hour)
			r.tick()
			r.tick()
			if n := len(r.hub.created); n != 1 {
				t.Fatalf("%d sessions were started", n)
			}
			if w := r.todos.written(); len(w) != 1 {
				t.Fatalf("decisions: %+v", w)
			}
		})
	}
}

// failOnce runs a writable task's first attempt: the agent leaves work (committed or not), the acceptance
// command fails, and the task waits for its second attempt.
func failOnce(t *testing.T, r *rig, task *Task, commit bool) *Task {
	t.Helper()
	r.tick()
	got := r.task(task.ID)
	if got.Exec == nil || got.Exec.Phase != PhaseWorking {
		t.Fatalf("attempt 1 did not start: %+v", got)
	}
	if err := os.WriteFile(filepath.Join(got.Exec.Worktree, "half.txt"), []byte("half\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if commit {
		runGit(t, got.Exec.Worktree, "add", ".")
		runGit(t, got.Exec.Worktree, "commit", "-q", "-m", "half of it")
	}
	r.hub.turnEnds(got.Exec.SessionID, "u1", "I wrote half.txt.\nThe second half is still missing.")
	r.tick()
	r.tick()
	r.waitAccepted(task.ID, 1)
	r.tick()
	got = r.task(task.ID)
	if got.State != StateQueued || got.NotBefore == nil {
		t.Fatalf("after the first failure: %s %v", got.State, got.NotBefore)
	}
	return got
}

func TestRetryOfAWritableTaskContinuesFromTheEarlierAttemptAndKnowsHowItEnded(t *testing.T) {
	for _, committed := range []bool{true, false} {
		name := "work left uncommitted"
		if committed {
			name = "work committed by the agent"
		}
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			base := runGit(t, r.repo, "rev-parse", "HEAD")
			task := r.enqueue(EnqueueInput{Title: "Write both halves", PermissionProfile: PermissionDefault,
				AcceptanceCommand: "echo second half missing >&2; exit 3"})
			got := failOnce(t, r, task, committed)

			rep := got.Reports[0]
			if rep.Summary != "I wrote half.txt.\nThe second half is still missing." {
				t.Fatalf("summary: %q", rep.Summary)
			}
			var commits []string
			for _, a := range rep.Artifacts {
				if a.Kind == "commit" {
					commits = append(commits, a.Path)
				}
			}
			head := runGit(t, r.repo, "rev-parse", "ats/"+task.ID+"-1")
			if len(commits) != 1 || commits[0] != head || head == base {
				t.Fatalf("commit artifacts %v, branch at %s, base %s", commits, head, base)
			}
			if kinds := artifactKinds(got); slices.Contains(kinds, "worktree") {
				t.Fatalf("the worktree was kept: %v", kinds)
			}
			if !committed {
				msg := runGit(t, r.repo, "log", "-1", "--format=%B", head)
				if !strings.HasPrefix(msg, "ATS "+task.ID+": Write both halves\n") || !strings.Contains(msg, "attempt 1") {
					t.Fatalf("commit message: %q", msg)
				}
			}

			r.now = got.NotBefore.Add(time.Second)
			r.tick()
			got = r.task(task.ID)
			if got.AttemptNo != 2 || got.Exec == nil || got.Exec.Phase != PhaseWorking {
				t.Fatalf("attempt 2 did not start: %+v", got)
			}
			if got.NotBefore != nil {
				t.Fatalf("not_before stayed on the running task: %v", got.NotBefore)
			}
			if got.Exec.BaseSHA != head || got.Exec.Branch != "ats/"+task.ID+"-2" {
				t.Fatalf("attempt 2 starts at %s on %s, the earlier work is at %s", got.Exec.BaseSHA, got.Exec.Branch, head)
			}
			if _, err := os.Stat(filepath.Join(got.Exec.Worktree, "half.txt")); err != nil {
				t.Fatalf("the earlier attempt's work is not in the new worktree: %v", err)
			}
			prompt := r.hub.created[1].Prompt
			for _, want := range []string{"This is attempt 2", "Attempt 1 did not succeed", "acceptance command failed",
				"验收命令退出码 3", "second half missing", "The second half is still missing.", "commit " + head, "continue from it"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("the retry's prompt does not have %q:\n%s", want, prompt)
				}
			}
			if first := r.hub.created[0].Prompt; strings.Contains(first, "This is attempt") {
				t.Errorf("the first attempt's prompt talks of an earlier one:\n%s", first)
			}
			v := viewTask(got, false)
			if v.FailureReason != "" || v.NotBefore != nil {
				t.Fatalf("the running attempt shows the earlier one's failure: %+v", v)
			}
		})
	}
}

func TestRetryStartsFromTheBaseWhenTheEarlierCommitIsGone(t *testing.T) {
	r := newRig(t)
	base := runGit(t, r.repo, "rev-parse", "HEAD")
	task := r.enqueue(EnqueueInput{PermissionProfile: PermissionDefault, AcceptanceCommand: "exit 3"})
	got := failOnce(t, r, task, true)

	// The commit the report names is not in the repository any more (the branch was deleted and collected).
	r.q.mu.Lock()
	for i, a := range r.q.tasks[task.ID].Reports[0].Artifacts {
		if a.Kind == "commit" {
			r.q.tasks[task.ID].Reports[0].Artifacts[i].Path = strings.Repeat("0123", 10)
		}
	}
	r.q.mu.Unlock()

	r.now = got.NotBefore.Add(time.Second)
	r.tick()
	got = r.task(task.ID)
	if got.AttemptNo != 2 || got.Exec == nil || got.Exec.Phase != PhaseWorking || got.Exec.BaseSHA != base {
		t.Fatalf("attempt 2: %+v exec %+v", got.State, got.Exec)
	}
	prompt := r.hub.created[1].Prompt
	if !strings.Contains(prompt, "starts again from the base commit") || strings.Contains(prompt, "continue from it") {
		t.Fatalf("prompt:\n%s", prompt)
	}
}

func TestRetryOnAnotherComputerDoesNotAskForTheEarlierCommit(t *testing.T) {
	task := &Task{
		Attempts: []Attempt{{AttemptNo: 1, Machine: "machine-1"}, {AttemptNo: 2, Machine: "machine-1"}, {AttemptNo: 3, Machine: "machine-2"}},
		Reports: []EvidenceReport{
			{AttemptNo: 1, Artifacts: []Artifact{{Kind: "commit", Path: "aaa"}, {Kind: "branch", Path: "ats/x-1"}}},
			{AttemptNo: 2, Artifacts: []Artifact{{Kind: "session", Path: "m/s"}}}, // added nothing to attempt 1's commit
		},
	}
	if got := resumeCommit(task, "machine-1"); got != "aaa" {
		t.Fatalf("same computer: %q", got)
	}
	if got := resumeCommit(task, "machine-2"); got != "" {
		t.Fatalf("another computer: %q", got)
	}
}

func TestRetryOfAReadOnlyTaskStartsFromTheBaseAndKnowsHowItEnded(t *testing.T) {
	r := newRig(t)
	base := runGit(t, r.repo, "rev-parse", "HEAD")
	task := r.enqueue(EnqueueInput{AcceptanceCommand: "echo nope; exit 1"})
	r.tick()
	r.hub.turnEnds("session-1", "u1", "Looked around.")
	r.tick()
	r.tick()
	r.waitAccepted(task.ID, 1)
	r.tick()
	got := r.task(task.ID)
	if slices.Contains(artifactKinds(got), "commit") {
		t.Fatalf("an attempt that committed nothing has a commit in its evidence: %v", artifactKinds(got))
	}
	r.now = got.NotBefore.Add(time.Second)
	r.tick()
	got = r.task(task.ID)
	if got.Exec == nil || got.Exec.BaseSHA != base || got.Exec.Branch != "" {
		t.Fatalf("exec: %+v", got.Exec)
	}
	prompt := r.hub.created[1].Prompt
	if !strings.Contains(prompt, "This is attempt 2") || !strings.Contains(prompt, "nope") || strings.Contains(prompt, "base commit") || strings.Contains(prompt, "continue from") {
		t.Fatalf("prompt:\n%s", prompt)
	}
}

func TestAgentsSummaryIsKeptInTheReport(t *testing.T) {
	r := newRig(t)
	long := strings.Repeat("字", 2000) + "\n最后一行"
	task := r.enqueue(EnqueueInput{})
	r.tick()
	r.hub.turnEnds("session-1", "u1", long)
	r.tick()
	r.tick()
	got := r.task(task.ID)
	if got.State != StateSucceeded || len(got.Reports) != 1 {
		t.Fatalf("state %s", got.State)
	}
	sum := got.Reports[0].Summary
	if n := len([]rune(sum)); n > summaryRunes+1 || !strings.HasPrefix(sum, "字") || !strings.HasSuffix(sum, "…") {
		t.Fatalf("summary of %d runes: %q…", n, string([]rune(sum)[:10]))
	}
}

func TestAgentSummaryTakesTheMarkedPart(t *testing.T) {
	for reply, want := range map[string]string{
		"did things\n\nSummary:\nadded Shout\ntests pass":     "added Shout\ntests pass",
		"looked\n\n**Summary:** two call sites\n- a.go:3":     "two call sites\n- a.go:3",
		"## Summary: early\nmore\nSummary: the last one wins": "the last one wins",
		"no marker here":            "no marker here",
		"an empty marker\nSummary:": "an empty marker\nSummary:",
	} {
		if got := agentSummary(reply); got != want {
			t.Errorf("agentSummary(%q) = %q, want %q", reply, got, want)
		}
	}
}

func TestPromptOfAWritableTaskSaysUncommittedWorkIsCommitted(t *testing.T) {
	r := newRig(t)
	r.enqueue(EnqueueInput{PermissionProfile: PermissionDefault})
	r.tick()
	prompt := r.hub.created[0].Prompt
	for _, want := range []string{"if you can", "committed to it for you", "Do not push", "Nobody is watching"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt does not have %q:\n%s", want, prompt)
		}
	}
}

func TestCancelDoesNotCommitTheUnfinishedWork(t *testing.T) {
	r := newRig(t)
	task := r.enqueue(EnqueueInput{PermissionProfile: PermissionDefault})
	r.tick()
	wt := r.task(task.ID).Exec.Worktree
	if err := os.WriteFile(filepath.Join(wt, "half.txt"), []byte("half\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.cancel(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if st := runGit(t, wt, "status", "--porcelain"); st == "" {
		t.Fatal("the canceled attempt's work was committed")
	}
}

func TestReleaseCommitMessageFitsALine(t *testing.T) {
	msg := releaseCommitMessage(&Task{ID: "task_12", Title: strings.Repeat("很长的标题", 30) + "\nsecond line"}, 2)
	first, rest, _ := strings.Cut(msg, "\n")
	if n := len([]rune(first)); n > 72 || !strings.HasPrefix(first, "ATS task_12: 很长的标题") || !strings.HasSuffix(first, "…") {
		t.Fatalf("first line of %d runes: %s", n, first)
	}
	if rest != "\nUncommitted work of attempt 2, committed by ATS." {
		t.Fatalf("body: %q", rest)
	}
}

func TestReleaseCommitsUncommittedWorkWhenAsked(t *testing.T) {
	// No identity from this machine's own git configuration.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, v := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
	for name, identity := range map[string]string{"repository without an identity": "", "repository's own identity": "Dev <dev@example.com>"} {
		t.Run(name, func(t *testing.T) {
			root, repo := newTestRepo(t)
			if identity != "" {
				runGit(t, repo, "config", "user.name", "Dev")
				runGit(t, repo, "config", "user.email", "dev@example.com")
			}
			w, _ := newTestWorker(t, root)
			ctx := t.Context()
			ws, err := w.prepare(ctx, prepareParams{TaskID: "task_a", Attempt: 1, Repo: repo})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(ws.Path, "new.txt"), []byte("new\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(ws.Path, "README.md"), []byte("changed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			res, err := w.release(ctx, releaseParams{TaskID: "task_a", Attempt: 1, Repo: repo, BaseSHA: ws.BaseSHA, CommitMessage: "ATS task_a: work\n\nbody"})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Removed || res.Kept != "" || res.Branch != "ats/task_a-1" || res.HeadSHA == "" || res.HeadSHA == ws.BaseSHA {
				t.Fatalf("release: %+v (base %s)", res, ws.BaseSHA)
			}
			if _, err := os.Stat(ws.Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the worktree is still there: %v", err)
			}
			if sha := runGit(t, repo, "rev-parse", "ats/task_a-1"); sha != res.HeadSHA {
				t.Fatalf("branch at %s, release said %s", sha, res.HeadSHA)
			}
			if files := runGit(t, repo, "show", "--name-only", "--format=", res.HeadSHA); files != "README.md\nnew.txt" {
				t.Fatalf("files in the commit: %q", files)
			}
			want := cmpOr(identity, "ATS <ats@localhost>")
			if who := runGit(t, repo, "log", "-1", "--format=%an <%ae>|%cn <%ce>|%s", res.HeadSHA); who != want+"|"+want+"|ATS task_a: work" {
				t.Fatalf("commit by %q, want %q", who, want)
			}
		})
	}

	t.Run("not asked", func(t *testing.T) {
		root, repo := newTestRepo(t)
		w, _ := newTestWorker(t, root)
		ws, err := w.prepare(t.Context(), prepareParams{TaskID: "task_b", Attempt: 1, Repo: repo})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws.Path, "new.txt"), []byte("new\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := w.release(t.Context(), releaseParams{TaskID: "task_b", Attempt: 1, Repo: repo, BaseSHA: ws.BaseSHA})
		if err != nil || res.Kept != ws.Path || res.Removed || res.HeadSHA != ws.BaseSHA {
			t.Fatalf("release: %+v %v", res, err)
		}
	})

	t.Run("read-only worktree has no branch to commit to", func(t *testing.T) {
		root, repo := newTestRepo(t)
		w, _ := newTestWorker(t, root)
		ws, err := w.prepare(t.Context(), prepareParams{TaskID: "task_c", Attempt: 1, Repo: repo, Readonly: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws.Path, "new.txt"), []byte("new\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := w.release(t.Context(), releaseParams{TaskID: "task_c", Attempt: 1, Repo: repo, BaseSHA: ws.BaseSHA, CommitMessage: "x"})
		if err != nil || res.Kept != ws.Path || res.HeadSHA != ws.BaseSHA {
			t.Fatalf("release: %+v %v", res, err)
		}
	})
}

func TestWaitingSaysWhatTheAgentWaitsFor(t *testing.T) {
	r := newRig(t)
	task := r.enqueue(EnqueueInput{PermissionProfile: PermissionDefault})
	r.tick()
	needs := func() {
		r.hub.set("session-1", func(s *hubSession) { s.State, s.PendingInteractions = "needs_you", 1 })
	}
	setCards := func(err error, cards ...hubInteraction) {
		r.hub.mu.Lock()
		r.hub.interactions, r.hub.interactionsErr = cards, err
		r.hub.mu.Unlock()
	}
	waiting := func() string {
		t.Helper()
		got := r.task(task.ID)
		if got.State != StateNeedsInput {
			t.Fatalf("state %s", got.State)
		}
		return got.Exec.Waiting
	}

	// Another session's card is not this task's.
	needs()
	setCards(nil, hubInteraction{SessionID: "session-9", MachineID: testMachine, Kind: "permission", ToolName: "Bash", Summary: "rm -rf x"})
	r.tick()
	if got := waiting(); got != waitingInSession {
		t.Fatalf("no card of its own: %q", got)
	}

	setCards(nil, hubInteraction{SessionID: "session-1", MachineID: testMachine, Kind: "permission", ToolName: "Bash",
		Summary: "git add -A && git commit -m work\nsecond line"})
	r.tick()
	if got := waiting(); got != "Agent 等你批准运行：git add -A && git commit -m work" {
		t.Fatalf("permission: %q", got)
	}

	// The hub cannot be asked for a round: what was known stays.
	setCards(errors.New("hub away"))
	r.tick()
	if got := waiting(); got != "Agent 等你批准运行：git add -A && git commit -m work" {
		t.Fatalf("after a failed lookup: %q", got)
	}

	setCards(nil, hubInteraction{SessionID: "session-1", MachineID: testMachine, Kind: "question", Summary: "ignored",
		Questions: []struct {
			Question string `json:"question"`
		}{{Question: "Which environment?"}}})
	r.tick()
	if got := waiting(); got != "Agent 在问你：Which environment?" {
		t.Fatalf("question: %q", got)
	}

	// The same answer round after round changes nothing in the queue.
	before := r.task(task.ID).Exec
	r.tick()
	r.tick()
	if after := r.task(task.ID).Exec; *after != *before {
		t.Fatalf("the record changed without news:\n%+v\n%+v", before, after)
	}

	r.hub.set("session-1", func(s *hubSession) { s.State, s.PendingInteractions = "working", 0 })
	r.tick()
	if got := r.task(task.ID); got.State != StateRunning || got.Exec.Waiting != "" {
		t.Fatalf("after the answer: %s %q", got.State, got.Exec.Waiting)
	}
}

func TestWaitingText(t *testing.T) {
	for _, tc := range []struct {
		ix   hubInteraction
		want string
	}{
		{hubInteraction{Kind: "permission", ToolName: "Write", Summary: "docs/a.md"}, "Agent 等你批准 Write：docs/a.md"},
		{hubInteraction{Kind: "permission", ToolName: "WebFetch"}, "Agent 等你批准使用 WebFetch"},
		{hubInteraction{Kind: "permission", Summary: strings.Repeat("x", 200)}, "Agent 等你批准运行：" + strings.Repeat("x", 120) + "…"},
		{hubInteraction{Kind: "question", Summary: "A or B?"}, "Agent 在问你：A or B?"},
		{hubInteraction{Kind: "permission"}, ""},
		{hubInteraction{Kind: "plan", Summary: "a plan"}, ""},
	} {
		if got := waitingText(tc.ix); got != tc.want {
			t.Errorf("waitingText(%+v) = %q, want %q", tc.ix, got, tc.want)
		}
	}
}

func TestTaskViewShowsTheFailureOnlyWhereItSaysSomething(t *testing.T) {
	soon := testStart.Add(time.Minute)
	for _, tc := range []struct {
		state     string
		reason    bool
		notBefore bool
	}{
		{state: StateQueued, reason: true, notBefore: true},
		{state: StateRunning, reason: false, notBefore: false},
		{state: StateNeedsInput, reason: false, notBefore: false},
		{state: StateFailed, reason: true, notBefore: false},
	} {
		v := viewTask(&Task{State: tc.state, FailureReason: "task_failed", NotBefore: &soon}, false)
		if (v.FailureReason != "") != tc.reason || (v.NotBefore != nil) != tc.notBefore {
			t.Errorf("%s: failure_reason %q not_before %v", tc.state, v.FailureReason, v.NotBefore)
		}
	}
}
