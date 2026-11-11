package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func artifactKinds(t *Task) []string {
	var kinds []string
	if len(t.Reports) == 0 {
		return nil
	}
	for _, a := range t.Reports[len(t.Reports)-1].Artifacts {
		kinds = append(kinds, a.Kind)
	}
	return kinds
}

func TestWritingTaskRunsInItsOwnWorktreeAndPassesAcceptance(t *testing.T) {
	r := newRig(t)
	task := r.enqueue(EnqueueInput{Title: "Add the marker", PermissionProfile: PermissionDefault, AcceptanceCommand: "test -f done.txt && echo checked"})

	r.tick()
	got := r.task(task.ID)
	if got.State != StateRunning || got.Exec == nil || got.Exec.Phase != PhaseWorking {
		t.Fatalf("after the first round: state %s exec %+v", got.State, got.Exec)
	}
	if len(r.hub.created) != 1 {
		t.Fatalf("sessions started: %d", len(r.hub.created))
	}
	req := r.hub.created[0]
	wt := got.Exec.Worktree
	if req.Cwd != wt || req.MachineID != testMachine || req.PermissionMode != "acceptEdits" || !req.Async || req.AgentType != "claude" {
		t.Fatalf("session request: %+v", req)
	}
	if !strings.HasPrefix(wt, r.worker.data) || got.Exec.Branch != "ats/"+task.ID+"-1" {
		t.Fatalf("worktree %s branch %s", wt, got.Exec.Branch)
	}
	for _, want := range []string{"Do the thing", got.Exec.Branch, "Do not push", "test -f done.txt", blockedMarker} {
		if !strings.Contains(req.Prompt, want) {
			t.Errorf("the prompt does not have %q:\n%s", want, req.Prompt)
		}
	}
	if !slices.Equal(r.hub.attached, []string{"todo_1/session-1"}) {
		t.Fatalf("attached: %v", r.hub.attached)
	}

	// The agent works: a commit on the task's branch.
	if err := os.WriteFile(filepath.Join(wt, "done.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, wt, "add", ".")
	runGit(t, wt, "commit", "-q", "-m", "work")
	head := runGit(t, wt, "rev-parse", "HEAD")

	// The hub shows a finished turn for a moment between the agent's steps: one sighting is not the end.
	r.hub.turnEnds("session-1", "u1", "half way")
	r.tick()
	r.hub.set("session-1", func(s *hubSession) { s.State = "working" })
	r.tick()
	if got := r.task(task.ID); got.Exec.Phase != PhaseWorking || got.State != StateRunning {
		t.Fatalf("a flicker ended the turn: %+v", got.Exec)
	}

	r.hub.turnEnds("session-1", "u2", "Added done.txt and committed.")
	r.tick()
	if got := r.task(task.ID); got.Exec.Phase != PhaseWorking {
		t.Fatalf("one sighting of the finished turn was taken: %+v", got.Exec)
	}
	r.tick()
	if got := r.task(task.ID); got.Exec == nil || got.Exec.Phase != PhaseAccepting {
		t.Fatalf("acceptance did not start: %+v", got)
	}
	r.waitAccepted(task.ID, 1)
	r.tick()

	got = r.task(task.ID)
	if got.State != StateSucceeded || got.Verdict != VerdictPass || got.Exec != nil {
		t.Fatalf("final: state %s verdict %s exec %+v", got.State, got.Verdict, got.Exec)
	}
	rep := got.Reports[len(got.Reports)-1]
	if rep.ExitCode == nil || *rep.ExitCode != 0 || !strings.Contains(rep.Message, "checked") {
		t.Fatalf("report: %+v", rep)
	}
	kinds := artifactKinds(got)
	for _, want := range []string{"commit", "branch", "session"} {
		if !slices.Contains(kinds, want) {
			t.Errorf("artifacts %v lack %s", kinds, want)
		}
	}
	if _, err := os.Stat(wt); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the worktree is still there: %v", err)
	}
	if sha := runGit(t, r.repo, "rev-parse", "ats/"+task.ID+"-1"); sha != head {
		t.Fatalf("the task's branch is at %s, the work at %s", sha, head)
	}
	if sha := runGit(t, r.repo, "rev-parse", "main"); sha == head {
		t.Fatal("the task moved the repository's own branch")
	}

	written := r.todos.written()
	if len(written) != 1 || written[0].Choice != "通过验收" || written[0].Ref != "ats:"+task.ID+":succeeded:pass" {
		t.Fatalf("decisions: %+v", written)
	}
	if r.todos.paths[0] != "/api/v1/todos/todo_1/decisions" {
		t.Fatalf("decision path %s", r.todos.paths[0])
	}
	r.tick()
	if n := len(r.todos.written()); n != 1 {
		t.Fatalf("the outcome was written %d times", n)
	}
}

func TestReadonlyTaskRunsInPlanModeAndEndsUnverifiedWithoutAcceptance(t *testing.T) {
	r := newRig(t)
	r.cfg.ModelStandard = "example-model"
	task := r.enqueue(EnqueueInput{Title: "Look into it"})
	r.tick()
	req := r.hub.created[0]
	if req.PermissionMode != "plan" || req.Model != "example-model" || !strings.Contains(req.Prompt, "read-only") {
		t.Fatalf("session request: %+v", req)
	}
	wt := r.task(task.ID).Exec.Worktree
	if branch := runGit(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); branch != "HEAD" {
		t.Fatalf("a read-only task got branch %s", branch)
	}
	r.hub.turnEnds("session-1", "u1", "Found the cause.")
	r.tick()
	r.tick()
	got := r.task(task.ID)
	if got.State != StateSucceeded || got.Verdict != VerdictUnverified {
		t.Fatalf("final: %s %s", got.State, got.Verdict)
	}
	if w := r.todos.written(); len(w) != 1 || w[0].Choice != "跑完但没有验收" {
		t.Fatalf("decisions: %+v", w)
	}
	if out := runGit(t, r.repo, "branch", "--list", "ats/*"); out != "" {
		t.Fatalf("branches left: %s", out)
	}
}

func TestSessionWaitingForTheUserIsNeedsInput(t *testing.T) {
	r := newRig(t)
	task := r.enqueue(EnqueueInput{})
	r.tick()

	r.hub.set("session-1", func(s *hubSession) { s.State, s.PendingInteractions = "needs_you", 1 })
	r.tick()
	if got := r.task(task.ID); got.State != StateNeedsInput || got.Exec.Waiting == "" {
		t.Fatalf("a session waiting for approval: %s %+v", got.State, got.Exec)
	}
	if c := r.q.PlanSchedule(r.now).CountsByState; c[StateNeedsInput] != 1 || c[StateRunning] != 0 {
		t.Fatalf("a task waiting for the user is counted as waiting, not running: %v", c)
	}
	// Needing the user is not running late: the lease is kept, the task is not given to another attempt.
	r.now = r.now.Add(3 * time.Hour)
	r.tick()
	if got := r.task(task.ID); got.State != StateNeedsInput || got.AttemptNo != 1 {
		t.Fatalf("after a long wait: %s attempt %d", got.State, got.AttemptNo)
	}

	r.hub.set("session-1", func(s *hubSession) { s.State, s.PendingInteractions = "working", 0 })
	r.now = r.now.Add(-3 * time.Hour) // the agent's own time has not run out
	r.tick()
	if got := r.task(task.ID); got.State != StateRunning || got.Exec.Waiting != "" {
		t.Fatalf("after the user answered: %s %+v", got.State, got.Exec)
	}

	// The agent ends its turn saying it is blocked: needs input, judged once.
	r.hub.turnEnds("session-1", "u5", "I looked around.\n\nATS_BLOCKED: which environment should I check?")
	r.tick()
	r.tick()
	got := r.task(task.ID)
	if got.State != StateNeedsInput || !strings.Contains(got.Exec.Waiting, "which environment") {
		t.Fatalf("a blocked reply: %s %+v", got.State, got.Exec)
	}
	r.tick()
	r.tick()
	if got := r.task(task.ID); got.State != StateNeedsInput {
		t.Fatalf("the blocked turn was judged again: %s", got.State)
	}

	// The user answers in the session; the agent goes on and finishes.
	r.hub.set("session-1", func(s *hubSession) { s.State, s.UpdatedAt = "working", "u6" })
	r.tick()
	r.hub.turnEnds("session-1", "u7", "Checked staging. All fine.")
	r.tick()
	r.tick()
	if got := r.task(task.ID); got.State != StateSucceeded {
		t.Fatalf("final: %s", got.State)
	}
}

func TestFailedAcceptanceIsRetriedAsANewAttemptThenFails(t *testing.T) {
	r := newRig(t)
	one := 1
	task := r.enqueue(EnqueueInput{AcceptanceCommand: "echo broken >&2; exit 3", MaxRetries: &one})

	for attempt := 1; attempt <= 2; attempt++ {
		r.tick()
		got := r.task(task.ID)
		if got.AttemptNo != attempt || got.Exec == nil || got.Exec.Phase != PhaseWorking {
			t.Fatalf("attempt %d did not start: state %s attempt %d", attempt, got.State, got.AttemptNo)
		}
		sid := got.Exec.SessionID
		r.hub.turnEnds(sid, "u1", "done")
		r.tick()
		r.tick()
		r.waitAccepted(task.ID, attempt)
		r.tick()
		got = r.task(task.ID)
		if attempt == 1 {
			if got.State != StateQueued || got.NotBefore == nil || got.FailureReason != "task_failed" {
				t.Fatalf("after the first failure: %s not_before %v reason %s", got.State, got.NotBefore, got.FailureReason)
			}
			if len(r.todos.written()) != 0 {
				t.Fatal("an attempt that will be retried was written to the to-do")
			}
			r.now = got.NotBefore.Add(time.Second)
		}
	}
	got := r.task(task.ID)
	if got.State != StateFailed || got.Verdict != VerdictFail || len(got.Attempts) != 2 {
		t.Fatalf("final: %s %s attempts %d", got.State, got.Verdict, len(got.Attempts))
	}
	rep := got.Reports[len(got.Reports)-1]
	if rep.ExitCode == nil || *rep.ExitCode != 3 || !strings.Contains(rep.Message, "broken") {
		t.Fatalf("report: %+v", rep)
	}
	if len(r.hub.created) != 2 || r.hub.created[0].Cwd == r.hub.created[1].Cwd {
		t.Fatalf("attempts shared a worktree: %+v", r.hub.created)
	}
	if w := r.todos.written(); len(w) != 1 || w[0].Choice != "失败：task_failed" {
		t.Fatalf("decisions: %+v", w)
	}
}

func TestRepositoryOutsideTheAllowlistFailsWithoutRetry(t *testing.T) {
	r := newRig(t)
	_, other := newTestRepo(t)
	task := r.enqueue(EnqueueInput{Repo: other})
	r.tick()
	got := r.task(task.ID)
	if got.State != StateFailed || got.FailureReason != "rejected" || got.RetriesLeft != got.MaxRetries {
		t.Fatalf("state %s reason %s retries left %d", got.State, got.FailureReason, got.RetriesLeft)
	}
	if len(r.hub.created) != 0 {
		t.Fatal("a session was started for a repository that is not allowed")
	}
	if !strings.Contains(got.Reports[0].Message, "repo_not_allowed") {
		t.Fatalf("report: %s", got.Reports[0].Message)
	}
}

func TestRestartPicksTheAttemptUpWhereItWas(t *testing.T) {
	r := newRig(t)
	task := r.enqueue(EnqueueInput{AcceptanceCommand: "true"})
	r.tick()
	dir, nodeData := filepath.Dir(r.q.dataFile), r.worker.data

	// Both programs restart, long after the lease would have run out.
	q, err := NewQueue(dir, r.cfg.queueConfig())
	if err != nil {
		t.Fatal(err)
	}
	r.q = q
	r.start(nodeData)
	r.now = r.now.Add(2 * time.Hour)
	r.cfg.TaskTimeoutMinutes = 600
	r.tick()
	got := r.task(task.ID)
	if got.State != StateRunning || got.AttemptNo != 1 || got.Exec.SessionID != "session-1" {
		t.Fatalf("after the restart: %s attempt %d exec %+v", got.State, got.AttemptNo, got.Exec)
	}
	if len(r.hub.created) != 1 {
		t.Fatalf("the restart started %d sessions", len(r.hub.created))
	}
	r.hub.turnEnds("session-1", "u1", "done")
	r.tick()
	r.tick()
	r.waitAccepted(task.ID, 1)
	r.tick()
	if got := r.task(task.ID); got.State != StateSucceeded || got.Verdict != VerdictPass {
		t.Fatalf("final: %s %s", got.State, got.Verdict)
	}
}

func TestCancelInterruptsTheSessionAndRemovesTheWorktree(t *testing.T) {
	r := newRig(t)
	task := r.enqueue(EnqueueInput{})
	r.tick()
	wt := r.task(task.ID).Exec.Worktree
	got, err := r.d.cancel(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateCanceled || !slices.Equal(r.hub.interrupts, []string{"session-1"}) {
		t.Fatalf("state %s interrupts %v", got.State, r.hub.interrupts)
	}
	if _, err := os.Stat(wt); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the worktree is still there: %v", err)
	}
	if w := r.todos.written(); len(w) != 1 || w[0].Choice != "已取消" {
		t.Fatalf("decisions: %+v", w)
	}
	r.tick()
	if len(r.hub.created) != 1 {
		t.Fatal("a canceled task ran again")
	}
}

func TestAgentRunningTooLongIsInterruptedAndTimesOut(t *testing.T) {
	r := newRig(t)
	zero := 0
	r.cfg.TaskTimeoutMinutes = 30
	task := r.enqueue(EnqueueInput{MaxRetries: &zero})
	r.tick()
	r.now = r.now.Add(31 * time.Minute)
	r.tick()
	got := r.task(task.ID)
	if got.State != StateFailed || got.FailureReason != "timeout" || len(r.hub.interrupts) != 1 {
		t.Fatalf("state %s reason %s interrupts %v", got.State, got.FailureReason, r.hub.interrupts)
	}
}

func TestSessionErrorsEndTheAttempt(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(s *hubSession)
		reason string
	}{
		"start error": {func(s *hubSession) { s.StartError = "no agent installed" }, "infra_error"},
		"rate limit":  {func(s *hubSession) { s.TurnError, s.TurnErrorKind = "usage limit reached", "rate_limit" }, "rate_limited"},
		"signed out":  {func(s *hubSession) { s.TurnError, s.TurnErrorKind = "please log in", "auth" }, "auth_expired"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			task := r.enqueue(EnqueueInput{})
			r.tick()
			r.hub.set("session-1", tc.change)
			r.tick()
			if got := r.task(task.ID); got.FailureReason != tc.reason || got.Exec != nil {
				t.Fatalf("state %s reason %s", got.State, got.FailureReason)
			}
		})
	}
}

func TestNothingRunsWithoutAComputerOrOverItsSlots(t *testing.T) {
	r := newRig(t)
	a := r.enqueue(EnqueueInput{Title: "first", Priority: PriorityP0})
	b := r.enqueue(EnqueueInput{Title: "second"})
	r.tick()
	if ta, tb := r.task(a.ID), r.task(b.ID); ta.State != StateRunning || tb.State != StateQueued {
		t.Fatalf("one slot: first %s second %s", ta.State, tb.State)
	}

	r2 := newRig(t)
	r2.cfg.Machines = nil
	c := r2.enqueue(EnqueueInput{})
	r2.now = r2.now.Add(2 * refreshInterval)
	r2.tick()
	if got := r2.task(c.ID); got.State != StateQueued || len(r2.hub.created) != 0 {
		t.Fatalf("with no computer selected: %s", got.State)
	}
}

func TestBlockedReason(t *testing.T) {
	for reply, want := range map[string]string{
		"All done.": "",
		"Work.\nATS_BLOCKED: need the staging URL":    "need the staging URL",
		"Work.\n\n`ATS_BLOCKED: need access`\n":       "need access",
		"ATS_BLOCKED: early\n1\n2\n3\n4\nfinished ok": "",
	} {
		got, blocked := blockedReason(reply)
		if got != want || blocked != (want != "") {
			t.Errorf("%q: %q %v", reply, got, blocked)
		}
	}
}
