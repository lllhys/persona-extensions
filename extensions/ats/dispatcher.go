package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The dispatcher runs the queue. Every round it takes the ready tasks the scheduler admits, and moves each
// running attempt one step along:
//
//	preparing  the computer's program checks the repository out into a worktree (ats.prepare)
//	working    a Persona agent session works in the worktree; the task is needs_input while the session waits
//	           for the user (a question, an approval) or ended its turn saying it is blocked
//	accepting  the computer's program runs the acceptance command (ats.accept); its exit code is the verdict
//
// and then records the outcome, removes the worktree and writes the outcome to the task's to-do. All of its
// state is in the queue (Task.Exec), so a restart of the program picks every attempt up where it was.

const (
	dispatchInterval = 15 * time.Second
	refreshInterval  = 60 * time.Second
	stablePolls      = 2
	pruneMaxAge      = 30 * 24 * time.Hour
	pruneKeep        = 500
	lostSessionAfter = 2 * time.Minute
	blockedMarker    = "ATS_BLOCKED:"
	waitingInSession = "Agent 在会话里等你回答或批准"
	nodeCallTimeout  = 25 * time.Second
)

type dispatcher struct {
	q      *Queue
	api    hubAPI
	nodes  nodeCaller
	writer *DecisionWriter
	cfg    func() ExtConfig
	now    func() time.Time
	// refresh reads what the hub knows that the scheduler needs (quota); called once a minute.
	refresh func(ctx context.Context)
	// changed tells the apps the queue page's data changed.
	changed func()

	mu          sync.Mutex
	accepted    map[string]acceptResult // acceptance results the node program reported, by attempt key
	lastRefresh time.Time
	wake        chan struct{}
}

func newDispatcher(q *Queue, api hubAPI, nodes nodeCaller, writer *DecisionWriter, cfg func() ExtConfig, now func() time.Time) *dispatcher {
	return &dispatcher{q: q, api: api, nodes: nodes, writer: writer, cfg: cfg, now: now,
		accepted: map[string]acceptResult{}, wake: make(chan struct{}, 1)}
}

// run is the background loop; it ends with ctx.
func (d *dispatcher) run(ctx context.Context) {
	t := time.NewTicker(dispatchInterval)
	defer t.Stop()
	for {
		d.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-d.wake:
		}
	}
}

// poke asks for a round now (a task was queued, an acceptance command finished).
func (d *dispatcher) poke() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// onAccepted takes an acceptance result the node program sent.
func (d *dispatcher) onAccepted(res acceptResult) {
	key, err := attemptKey(res.TaskID, res.Attempt)
	if err != nil {
		return
	}
	res.Done = true
	d.mu.Lock()
	d.accepted[key] = res
	d.mu.Unlock()
	d.poke()
}

func (d *dispatcher) tick(ctx context.Context) {
	now := d.now()
	_ = d.q.RenewLeases(now)

	d.mu.Lock()
	slow := now.Sub(d.lastRefresh) >= refreshInterval
	if slow {
		d.lastRefresh = now
	}
	d.mu.Unlock()
	if slow {
		d.refreshMachines(ctx)
		if d.refresh != nil {
			d.refresh(ctx)
		}
		_, _ = d.q.Prune(now, pruneMaxAge, pruneKeep)
	}

	moved := false
	for _, t := range d.q.ActiveTasks() {
		if d.advance(ctx, t) {
			moved = true
		}
	}
	leased, _, _, err := d.q.DispatchNext(8, d.now())
	if err == nil {
		for _, t := range leased {
			d.advance(ctx, t)
			moved = true
		}
	}
	if d.writer != nil && d.writer.HasTodosWriteScope() {
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, _ = d.writer.SyncQueueOutcomes(wctx, d.q)
		cancel()
	}
	if moved && d.changed != nil {
		d.changed()
	}
}

// refreshMachines sets the scheduler's slots from the hub's list of computers: the ones the extension's program
// is switched on for, each with the configured number of tasks at a time.
func (d *dispatcher) refreshMachines(ctx context.Context) {
	cfg := d.cfg()
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	machines, err := d.api.Machines(cctx)
	if err != nil {
		return // keep the last known slots: a hub that is briefly unreachable must not look like no computers
	}
	on := map[string]bool{}
	for _, id := range cfg.Machines {
		on[id] = true
	}
	var slots []MachineSlot
	for _, m := range machines {
		if on[m.ID] {
			slots = append(slots, MachineSlot{Name: m.ID, Title: m.Name, Online: m.Online, MaxSlots: cfg.parallel()})
		}
	}
	_ = d.q.SetMachines(slots)
}

// advance moves one running attempt a step along; it reports whether the task changed.
func (d *dispatcher) advance(ctx context.Context, t *Task) bool {
	if len(t.Attempts) == 0 {
		return false
	}
	att := t.Attempts[len(t.Attempts)-1]
	e := t.Exec
	if e == nil || e.Phase == "" {
		var err error
		t, err = d.q.UpdateExec(t.ID, t.FenceToken, d.now(), func(e *Execution) {
			e.MachineID, e.Phase = att.Machine, PhasePreparing
		})
		if err != nil {
			return false
		}
		e = t.Exec
	}
	switch e.Phase {
	case PhasePreparing:
		return d.prepare(ctx, t, att)
	case PhaseWorking:
		return d.watch(ctx, t, att)
	case PhaseAccepting:
		return d.collect(ctx, t, att)
	}
	return false
}

func (d *dispatcher) nodeCall(ctx context.Context, machine, method string, params, out any) error {
	cctx, cancel := context.WithTimeout(ctx, nodeCallTimeout)
	defer cancel()
	return d.nodes.Call(cctx, machine, method, params, out)
}

// prepare checks the worktree out and starts the agent session in it.
func (d *dispatcher) prepare(ctx context.Context, t *Task, att Attempt) bool {
	e := t.Exec
	cfg := d.cfg()
	readonly := t.PermissionProfile != PermissionDefault
	var ws prepareResult
	err := d.nodeCall(ctx, e.MachineID, "ats.prepare", prepareParams{
		TaskID: t.ID, Attempt: att.AttemptNo, Repo: t.Workspace.Repo, Ref: t.Workspace.BaseSHA, Readonly: readonly,
	}, &ws)
	if err != nil {
		// What the task asks for is wrong (not what the computer is doing right now): another attempt would
		// fail the same way.
		msg := err.Error()
		permanent := strings.Contains(msg, "repo_not_allowed") || strings.Contains(msg, "ref_not_found") || strings.Contains(msg, "repo:")
		d.finish(ctx, t, att, outcome{state: StateFailed, errClass: classIf(permanent, "rejected", "infra_error"), message: "准备工作目录失败：" + msg})
		return true
	}
	mode := "plan"
	if !readonly {
		mode = cfg.writeMode()
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	sid, err := d.api.CreateSession(cctx, sessionRequest{
		MachineID: e.MachineID, Cwd: ws.Path, AgentType: "claude", Prompt: buildPrompt(t, ws, readonly),
		PermissionMode: mode, Model: cfg.model(cmpOr(att.ModelTier, t.ModelTier)), TabName: "ATS · " + firstLinePreview(t.Title, 40), Async: true,
	})
	cancel()
	if err != nil {
		t.Exec.Worktree = ws.Path
		d.finish(ctx, t, att, outcome{state: StateFailed, errClass: "infra_error", message: "启动 Agent 会话失败：" + err.Error()})
		return true
	}
	if _, err := d.q.UpdateExec(t.ID, t.FenceToken, d.now(), func(e *Execution) {
		e.Phase, e.Worktree, e.Branch, e.BaseSHA, e.SessionID = PhaseWorking, ws.Path, ws.Branch, ws.BaseSHA, sid
	}); err != nil {
		return false
	}
	if todo := t.TodoID(); todo != "" {
		actx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_ = d.api.AttachTodo(actx, todo, e.MachineID, sid) // the to-do may be finished or gone; the task runs anyway
		cancel()
	}
	return true
}

// buildPrompt is what the agent is told: the task, and the rules of running unattended.
func buildPrompt(t *Task, ws prepareResult, readonly bool) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(t.Prompt))
	b.WriteString("\n\n---\n")
	fmt.Fprintf(&b, "You are running unattended as queued task %s, in a git worktree of its own checked out at commit %s.\n", t.ID, ws.BaseSHA)
	if readonly {
		b.WriteString("- This task is read-only: do not change, create or delete files, and do not run commands that do. Investigate and report.\n")
	} else {
		fmt.Fprintf(&b, "- Work only inside this worktree. Commit your work on the current branch (%s). Do not push, and do not touch other branches or worktrees.\n", ws.Branch)
	}
	if t.Acceptance != nil && strings.TrimSpace(t.Acceptance.Command) != "" {
		fmt.Fprintf(&b, "- When you finish, this command is run in the worktree and must exit 0 for the task to pass: %s\n", strings.TrimSpace(t.Acceptance.Command))
	}
	fmt.Fprintf(&b, "- If you cannot go on without the user, end your reply with one line: %s <what you need>\n", blockedMarker)
	b.WriteString("- End with a short summary of what you did and found.\n")
	return b.String()
}

// watch reads the session's state: waiting for the user, still working, or done with its turn.
func (d *dispatcher) watch(ctx context.Context, t *Task, att Attempt) bool {
	e := t.Exec
	now := d.now()
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	s, err := d.api.Session(cctx, e.MachineID, e.SessionID)
	cancel()
	if err != nil {
		return false // the hub is briefly unreachable: look again next round
	}
	if s == nil {
		if now.Sub(e.PhaseSince) > lostSessionAfter {
			d.finish(ctx, t, att, outcome{state: StateFailed, errClass: "lost", message: "Agent 会话不见了（被关闭或电脑重装）"})
			return true
		}
		return false
	}
	switch {
	case s.StartError != "":
		d.finish(ctx, t, att, outcome{state: StateFailed, errClass: "infra_error", message: "Agent 会话没有启动：" + s.StartError})
		return true
	case s.Starting:
		return false
	case s.TurnError != "":
		d.finish(ctx, t, att, outcome{state: StateFailed, errClass: turnErrorClass(s.TurnErrorKind, s.TurnError), message: "Agent 这一轮出错：" + s.TurnError})
		return true
	case s.State == "needs_you" || s.PendingInteractions > 0:
		return d.setWaiting(t, waitingInSession, "")
	case s.State == "working" || s.BackgroundWorking:
		if timeout := d.cfg().taskTimeout(); now.Sub(e.PhaseSince) > timeout {
			ictx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_ = d.api.Interrupt(ictx, e.MachineID, e.SessionID)
			cancel()
			d.finish(ctx, t, att, outcome{state: StateFailed, errClass: "timeout", message: fmt.Sprintf("Agent 超过 %d 分钟没有做完，已打断", int(timeout.Minutes()))})
			return true
		}
		if e.Waiting != "" || e.Stable != 0 {
			_, err := d.q.UpdateExec(t.ID, t.FenceToken, now, func(e *Execution) { e.Waiting, e.Stable, e.SeenUpdate = "", 0, "" })
			return err == nil
		}
		return false
	case s.State == "unread" || s.State == "idle":
		if s.UpdatedAt == e.JudgedUpdate && e.JudgedUpdate != "" {
			return false // the turn that said it is blocked: nothing new since
		}
		// The hub shows a finished turn for a moment between an agent's steps: take it only when two rounds
		// in a row see the same one.
		stable := 1
		if s.UpdatedAt == e.SeenUpdate {
			stable = e.Stable + 1
		}
		if stable < stablePolls {
			_, _ = d.q.UpdateExec(t.ID, t.FenceToken, now, func(e *Execution) { e.SeenUpdate, e.Stable = s.UpdatedAt, stable })
			return false
		}
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		reply, err := d.api.LastReply(rctx, e.MachineID, e.SessionID)
		cancel()
		if err != nil {
			return false
		}
		if need, blocked := blockedReason(reply); blocked {
			return d.setWaiting(t, "Agent 说它卡住了："+need, s.UpdatedAt)
		}
		return d.startAcceptance(ctx, t, att, reply)
	}
	return false // offline, unknown: the computer is away; the session is still there when it is back
}

// blockedReason finds the line the agent ends a reply with when it cannot go on.
func blockedReason(reply string) (string, bool) {
	lines := strings.Split(strings.TrimSpace(reply), "\n")
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-3; i-- {
		line := strings.Trim(strings.TrimSpace(lines[i]), "`*")
		if rest, ok := strings.CutPrefix(line, blockedMarker); ok {
			return firstLinePreview(strings.TrimSpace(rest), 200), true
		}
	}
	return "", false
}

func turnErrorClass(kind, text string) string {
	s := strings.ToLower(kind + " " + text)
	switch {
	case strings.Contains(s, "rate") || strings.Contains(s, "limit") || strings.Contains(s, "quota") || strings.Contains(s, "overload"):
		return "rate_limited"
	case strings.Contains(s, "auth") || strings.Contains(s, "login") || strings.Contains(s, "credential"):
		return "auth_expired"
	}
	return "task_failed"
}

func (d *dispatcher) setWaiting(t *Task, why, judged string) bool {
	if t.Exec.Waiting == why && t.Exec.JudgedUpdate == judged {
		return false
	}
	_, err := d.q.UpdateExec(t.ID, t.FenceToken, d.now(), func(e *Execution) {
		e.Waiting, e.JudgedUpdate, e.Stable = why, judged, 0
	})
	return err == nil
}

// startAcceptance: the agent is done. With no acceptance command the task ends unverified; with one, the
// computer's program runs it.
func (d *dispatcher) startAcceptance(ctx context.Context, t *Task, att Attempt, reply string) bool {
	if t.Acceptance == nil || strings.TrimSpace(t.Acceptance.Command) == "" {
		d.finish(ctx, t, att, outcome{state: StateSucceeded, verdict: VerdictUnverified, message: reply})
		return true
	}
	key, _ := attemptKey(t.ID, att.AttemptNo)
	d.mu.Lock()
	delete(d.accepted, key)
	d.mu.Unlock()
	err := d.nodeCall(ctx, t.Exec.MachineID, "ats.accept", acceptParams{
		TaskID: t.ID, Attempt: att.AttemptNo, Command: t.Acceptance.Command, TimeoutSeconds: int(d.cfg().acceptTimeout().Seconds()),
	}, nil)
	if err != nil {
		d.finish(ctx, t, att, outcome{state: StateFailed, errClass: "infra_error", message: "验收命令没有跑起来：" + err.Error()})
		return true
	}
	_, err = d.q.UpdateExec(t.ID, t.FenceToken, d.now(), func(e *Execution) {
		e.Phase, e.Waiting, e.LastReply = PhaseAccepting, "", firstLinePreview(reply, 400)
	})
	return err == nil
}

// collect takes the acceptance command's result: the event the node program sent, or its answer when asked.
func (d *dispatcher) collect(ctx context.Context, t *Task, att Attempt) bool {
	e := t.Exec
	key, _ := attemptKey(t.ID, att.AttemptNo)
	d.mu.Lock()
	res, ok := d.accepted[key]
	delete(d.accepted, key)
	d.mu.Unlock()
	if !ok {
		if err := d.nodeCall(ctx, e.MachineID, "ats.accept_status", acceptParams{TaskID: t.ID, Attempt: att.AttemptNo}, &res); err != nil || !res.Done {
			if d.now().Sub(e.PhaseSince) > d.cfg().acceptTimeout()+5*time.Minute {
				d.finish(ctx, t, att, outcome{state: StateFailed, errClass: "timeout", message: "等不到验收命令的结果（电脑离线或程序重启）"})
				return true
			}
			return false
		}
	}
	code := res.ExitCode
	out := outcome{exitCode: &code}
	tail := strings.TrimSpace(res.OutputTail)
	switch {
	case res.TimedOut:
		out.state, out.verdict, out.errClass = StateFailed, VerdictFail, "timeout"
		out.message = fmt.Sprintf("验收命令超时（%d 秒）", res.DurationMS/1000)
	case res.Error != "":
		out.state, out.verdict, out.errClass = StateFailed, VerdictUnverified, "infra_error"
		out.message = "验收命令没有跑完：" + res.Error
	case res.ExitCode == 0:
		out.state, out.verdict = StateSucceeded, VerdictPass
		out.message = "验收命令退出码 0"
	default:
		out.state, out.verdict, out.errClass = StateFailed, VerdictFail, "task_failed"
		out.message = fmt.Sprintf("验收命令退出码 %d", res.ExitCode)
	}
	if tail != "" {
		out.message += "\n" + tail
	}
	if res.HeadSHA != "" {
		out.artifacts = append(out.artifacts, Artifact{Kind: "commit", Path: res.HeadSHA})
	}
	d.finish(ctx, t, att, out)
	return true
}

type outcome struct {
	state     string // succeeded | failed
	verdict   string
	errClass  string
	exitCode  *int
	message   string
	artifacts []Artifact
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func classIf(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// finish ends the attempt: releases what it holds on the computer, records the outcome (the queue decides
// between another attempt and a final state) and writes a final outcome to the to-do.
func (d *dispatcher) finish(ctx context.Context, t *Task, att Attempt, out outcome) {
	arts := d.cleanup(ctx, t, att.AttemptNo, false)
	if t.Exec != nil && t.Exec.SessionID != "" {
		arts = append(arts, Artifact{Kind: "session", Path: t.Exec.MachineID + "/" + t.Exec.SessionID})
	}
	if out.verdict == "" {
		out.verdict = VerdictUnverified
	}
	var retryable *bool
	if out.errClass == "rejected" {
		no := false
		retryable = &no
	}
	now := d.now()
	_ = d.q.RenewLeases(now)
	done, err := d.q.CompleteAttempt(CompleteAttemptInput{
		TaskID: t.ID, AttemptNo: att.AttemptNo, FenceToken: att.FenceToken, WorkerID: att.WorkerID,
		Outcome: out.state, Verdict: out.verdict, ErrorClass: out.errClass, ExitCode: out.exitCode,
		Message: out.message, Artifacts: append(out.artifacts, arts...), Retryable: retryable, Now: now,
	})
	if err != nil || !done.IsTerminal() || d.writer == nil || !d.writer.HasTodosWriteScope() {
		return
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, _ = d.writer.WriteTaskOutcome(wctx, d.q, done.ID)
	cancel()
}

// cleanup releases what an attempt holds on its computer: the worktree (kept when it has uncommitted work) and,
// when the attempt is being canceled, the agent's turn. It returns what stays behind as evidence.
func (d *dispatcher) cleanup(ctx context.Context, t *Task, attempt int, interrupt bool) []Artifact {
	e := t.Exec
	if e == nil || e.MachineID == "" {
		return nil
	}
	if interrupt && e.SessionID != "" {
		ictx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_ = d.api.Interrupt(ictx, e.MachineID, e.SessionID)
		cancel()
	}
	if e.Worktree == "" {
		return nil
	}
	var rel releaseResult
	if err := d.nodeCall(ctx, e.MachineID, "ats.release", releaseParams{
		TaskID: t.ID, Attempt: attempt, Repo: t.Workspace.Repo, BaseSHA: e.BaseSHA,
	}, &rel); err != nil {
		return []Artifact{{Kind: "worktree", Path: e.Worktree}}
	}
	var arts []Artifact
	if rel.Branch != "" {
		arts = append(arts, Artifact{Kind: "branch", Path: rel.Branch})
	}
	if rel.Kept != "" {
		arts = append(arts, Artifact{Kind: "worktree", Path: rel.Kept})
	}
	return arts
}

// cancel cancels a task from the queue page: the queue first, then what its attempt holds.
func (d *dispatcher) cancel(ctx context.Context, taskID string) (*Task, error) {
	before, err := d.q.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	t, err := d.q.CancelTask(taskID, d.now())
	if err != nil {
		return nil, err
	}
	if before.Exec != nil && len(before.Attempts) > 0 {
		d.cleanup(ctx, before, before.Attempts[len(before.Attempts)-1].AttemptNo, true)
	}
	if d.writer != nil && d.writer.HasTodosWriteScope() {
		_, _ = d.writer.WriteTaskOutcome(ctx, d.q, t.ID)
	}
	return t, nil
}
