package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

// pageRig is a queue with tasks put into the states the pages show, without a dispatcher: each helper moves a
// task the way the dispatcher's calls would.
type pageRig struct {
	t *testing.T
	q *Queue
	a *atsApp
}

func newPageRig(t *testing.T) *pageRig {
	t.Helper()
	a := newTestApp(t)
	return &pageRig{t: t, q: a.queue, a: a}
}

func (p *pageRig) enqueue(in EnqueueInput) *Task {
	p.t.Helper()
	if in.Labels == nil {
		in.Labels = map[string]string{PersonaTodoLabel: "todo_1"}
	}
	return mustEnqueue(p.t, p.q, in)
}

// lease starts the task's next attempt in the given phase; waiting makes it wait for the user.
func (p *pageRig) lease(id, phase, waiting string) *Attempt {
	p.t.Helper()
	_, att, err := p.q.LeaseTask(id, "dispatcher", testMachine, testStart)
	if err != nil {
		p.t.Fatal(err)
	}
	if _, err := p.q.UpdateExec(id, att.FenceToken, testStart, func(e *Execution) {
		e.MachineID, e.Phase, e.Waiting = testMachine, phase, waiting
		e.Branch, e.Worktree, e.SessionID = "ats/"+id+"-1", "/data/worktrees/"+id+"-1", "session-1"
	}); err != nil {
		p.t.Fatal(err)
	}
	return att
}

func (p *pageRig) complete(id string, att *Attempt, done CompleteAttemptInput) *Task {
	p.t.Helper()
	done.TaskID, done.FenceToken, done.WorkerID, done.Now = id, att.FenceToken, "dispatcher", testStart.Add(time.Minute)
	task, err := p.q.CompleteAttempt(done)
	if err != nil {
		p.t.Fatal(err)
	}
	return task
}

// checked marshals a view after holding it to the SDK's rules for a page.
func checked(t *testing.T, view *wire.View) string {
	t.Helper()
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if err := exttest.CheckView(raw, false); err != nil {
		t.Fatalf("not a valid view: %v\n%s", err, raw)
	}
	return string(raw)
}

func sectionTitles(v *wire.View) []string {
	var out []string
	for _, s := range v.Sections {
		out = append(out, s.Title)
	}
	return out
}

func findRow(t *testing.T, v *wire.View, key string) wire.Component {
	t.Helper()
	for _, s := range v.Sections {
		for _, c := range s.Items {
			if c.Key == key {
				return c
			}
		}
	}
	t.Fatalf("the view has no component %q", key)
	return wire.Component{}
}

func sectionOf(v *wire.View, key string) string {
	for _, s := range v.Sections {
		for _, c := range s.Items {
			if c.Key == key {
				return s.Title
			}
		}
	}
	return ""
}

func TestQueuePagePutsWhatNeedsTheUserFirstAndATapNeverChangesATask(t *testing.T) {
	p := newPageRig(t)
	zero := 0
	none := 0
	one := 1

	waiting := p.enqueue(EnqueueInput{Title: "Waiting task", Repo: "/srv/code/app", PermissionProfile: PermissionDefault, Priority: PriorityP0})
	p.lease(waiting.ID, PhaseWorking, "Agent 等你批准运行：go test ./...")
	queued := p.enqueue(EnqueueInput{Title: "Queued task", Repo: "/srv/code/site"})
	failed := p.enqueue(EnqueueInput{Title: "Failed task", Repo: "/srv/code/app", MaxRetries: &none})
	p.q.SetMachines([]MachineSlot{{Name: testMachine, Title: "Studio", Online: true, MaxSlots: 4}})
	p.complete(failed.ID, p.lease(failed.ID, PhaseWorking, ""), CompleteAttemptInput{Outcome: StateFailed, ErrorClass: "config_error",
		Message: "Agent 这一轮出错：There's an issue with the selected model (example-model). It may not exist or you may not have access to it."})
	retrying := p.enqueue(EnqueueInput{Title: "Retrying task", Repo: "/srv/code/app", AcceptanceCommand: "go test ./...", MaxRetries: &one})
	p.complete(retrying.ID, p.lease(retrying.ID, PhaseAccepting, ""), CompleteAttemptInput{Outcome: StateFailed, Verdict: VerdictFail, ErrorClass: "task_failed", ExitCode: &one, Message: "验收命令退出码 1\nFAIL"})
	passed := p.enqueue(EnqueueInput{Title: "Passed task", Repo: "/srv/code/app", AcceptanceCommand: "go test ./..."})
	p.complete(passed.ID, p.lease(passed.ID, PhaseAccepting, ""), CompleteAttemptInput{Outcome: StateSucceeded, Verdict: VerdictPass, ExitCode: &zero, Message: "验收命令退出码 0\nok"})

	view := buildQueueView(p.q, p.a.config(), testStart.Add(2*time.Minute), "UTC")
	page := checked(t, view)

	// Tasks first, the one that waits for the user before all; the numbers and the computers below.
	if got, want := strings.Join(sectionTitles(view), "|"), "等你处理|排队和在跑|最近结束|总览|电脑"; got != want {
		t.Fatalf("sections: %s, want %s", got, want)
	}
	if got := sectionOf(view, "active-"+waiting.ID); got != "等你处理" {
		t.Errorf("the task that waits for the user is under %q", got)
	}
	if got := sectionOf(view, "active-"+queued.ID); got != "排队和在跑" {
		t.Errorf("the queued task is under %q", got)
	}
	if got := sectionOf(view, "active-"+retrying.ID); got != "排队和在跑" {
		t.Errorf("the task that waits for its retry is under %q", got)
	}
	if row := findRow(t, view, "active-"+waiting.ID); row.Subtitle != "Agent 等你批准运行：go test ./..." {
		t.Errorf("the row of the waiting task does not say what it waits for: %q", row.Subtitle)
	}

	// A tap opens the task's page; canceling and retrying are in the row's menu, each asking first.
	for _, task := range []*Task{waiting, queued, retrying, failed, passed} {
		key := "active-" + task.ID
		if task.ID == failed.ID || task.ID == passed.ID {
			key = "term-" + task.ID
		}
		row := findRow(t, view, key)
		if row.Action == nil || row.Action.ID != "open:"+task.ID || row.Action.Confirm != "" || row.Action.Push == nil {
			t.Fatalf("a tap on %q does not just open its page: %+v", task.Title, row.Action)
		}
		var st taskPage
		if err := json.Unmarshal([]byte(row.Action.Push.State), &st); err != nil || st.Task != task.ID {
			t.Errorf("the pushed page of %q is for %q (%v)", task.Title, st.Task, err)
		}
		for _, ra := range row.Actions {
			if ra.Action.Confirm == "" {
				t.Errorf("%q: the row action %q does not ask first", task.Title, ra.Title)
			}
		}
	}
	if acts := findRow(t, view, "active-"+waiting.ID).Actions; len(acts) != 1 || acts[0].Action.ID != "cancel:"+waiting.ID || acts[0].Role != wire.ButtonRoleDestructive {
		t.Errorf("the running row's menu: %+v", acts)
	}
	if acts := findRow(t, view, "term-"+failed.ID).Actions; len(acts) != 1 || acts[0].Action.ID != "retry:"+failed.ID {
		t.Errorf("the failed row's menu: %+v", acts)
	}
	if acts := findRow(t, view, "term-"+passed.ID).Actions; len(acts) != 0 {
		t.Errorf("a passed task offers %+v", acts)
	}

	// Rows speak plainly: no to-do id, no tier arrow, no raw error class, and the reason of a failure whole.
	for _, bad := range []string{"todo_1", "→", "config_error", "task_failed", "点按"} {
		if strings.Contains(page, bad) {
			t.Errorf("the page has %q", bad)
		}
	}
	if sub := findRow(t, view, "term-"+failed.ID).Subtitle; !strings.Contains(sub, "模型或配置不可用，试了 1 次") || !strings.Contains(sub, "app") {
		t.Errorf("the failed row: %q", sub)
	}
	if sub := findRow(t, view, "active-"+retrying.ID).Subtitle; !strings.Contains(sub, "第 1 次失败（没通过验收），") || !strings.Contains(sub, "后重试") {
		t.Errorf("the row of the task that waits for its retry: %q", sub)
	}
	if sub := findRow(t, view, "active-"+queued.ID).Subtitle; sub != "P2 · 只读 · site" {
		t.Errorf("the queued row: %q", sub)
	}

	// With every tier admitted in full the table of tiers is left out; it is back when one is held.
	if strings.Contains(page, "各模型档位的额度") {
		t.Error("the table of tiers is shown with nothing held back")
	}
	if err := p.q.SetQuotaStatus(QuotaStatus{Provider: "test", RemainingPct: 20, CheckedAt: testStart}); err != nil {
		t.Fatal(err)
	}
	if held := checked(t, buildQueueView(p.q, p.a.config(), testStart.Add(2*time.Minute), "UTC")); !strings.Contains(held, "各模型档位的额度") || !strings.Contains(held, "只放行 P0、P1") {
		t.Error("the table of tiers is missing when quota holds tiers back")
	}
}

func TestQueuePageWithNoTasksKeepsItsSectionsInOrder(t *testing.T) {
	a := newATSApp(newTestQueue(t, DefaultQueueConfig()))
	view := buildQueueView(a.queue, a.config(), testStart, "")
	checked(t, view)
	if got, want := strings.Join(sectionTitles(view), "|"), "还差一步|排队和在跑|最近结束|总览|电脑"; got != want {
		t.Fatalf("sections: %s, want %s", got, want)
	}
}

func TestTaskPageShowsWhatATaskIsDoingAndItsEvidence(t *testing.T) {
	p := newPageRig(t)
	zero, one, none := 0, 1, 0
	if err := p.q.SetMachines([]MachineSlot{{Name: testMachine, Title: "Studio", Online: true, MaxSlots: 4}}); err != nil {
		t.Fatal(err)
	}
	at := testStart.Add(2 * time.Minute)
	page := func(id string) (*wire.View, string) {
		t.Helper()
		v := buildTaskView(p.q, id, at, "UTC")
		return v, checked(t, v)
	}
	has := func(what, raw string, wants ...string) {
		t.Helper()
		for _, w := range wants {
			if !strings.Contains(raw, w) {
				t.Errorf("%s: the page does not have %q", what, w)
			}
		}
	}
	button := func(v *wire.View, key string) *wire.Action {
		for _, s := range v.Sections {
			for _, c := range s.Items {
				if c.Key == key && c.Type == wire.ComponentButton {
					return c.Action
				}
			}
		}
		return nil
	}

	// Running: what it is doing, on which computer; it can be canceled, after asking; the page draws again.
	running := p.enqueue(EnqueueInput{Title: "Running task", Prompt: "Add the function", Repo: "/srv/code/app", AcceptanceCommand: "go test ./...", PermissionProfile: PermissionDefault, Priority: PriorityP1})
	p.lease(running.ID, PhaseWorking, "")
	v, raw := page(running.ID)
	has("running", raw, "Running task", "现在", "第 1 次：Agent 在做（Studio）", "Add the function", "go test ./...", "ats/"+running.ID+"-1", "todo_1", running.ID, "可写", "P1", "/srv/code/app @ ")
	if v.RefreshAfter == 0 {
		t.Error("the page of a running task is not drawn again")
	}
	if a := button(v, "cancel"); a == nil || a.ID != "cancel:"+running.ID || a.Confirm == "" {
		t.Errorf("the cancel button of a running task: %+v", a)
	}
	if button(v, "retry") != nil {
		t.Error("a running task offers a retry")
	}
	if pageTask(&ext.UIRequest{UIParams: wire.UIParams{State: v.State}}) != running.ID {
		t.Errorf("the page does not keep its task in its state: %q", v.State)
	}

	// Needs input: what it waits for, and where to answer.
	waiting := p.enqueue(EnqueueInput{Title: "Waiting task", Repo: "/srv/code/app"})
	p.lease(waiting.ID, PhaseWorking, "Agent 等你批准运行：go test ./...")
	_, raw = page(waiting.ID)
	has("needs input", raw, "等你处理", "Agent 等你批准运行：go test ./...", "Agent 会话里回答或批准")

	// Failed acceptance: the exit code and the end of the output, the agent's summary, what is left, the attempts.
	failed := p.enqueue(EnqueueInput{Title: "Failed task", Repo: "/srv/code/app", AcceptanceCommand: "go test ./...", PermissionProfile: PermissionDefault, MaxRetries: &none})
	p.complete(failed.ID, p.lease(failed.ID, PhaseAccepting, ""), CompleteAttemptInput{Outcome: StateFailed, Verdict: VerdictFail, ErrorClass: "task_failed", ExitCode: &one,
		Message: "验收命令退出码 1\n--- FAIL: TestGreet\nFAIL example.com/demo", Summary: "I changed greet.go.\nThe test still fails.",
		Artifacts: []Artifact{{Kind: "commit", Path: "0123456789abcdef0123456789abcdef01234567"}, {Kind: "branch", Path: "ats/example-1"}, {Kind: "session", Path: "machine-1/session-1"}}})
	v, raw = page(failed.ID)
	has("failed", raw, "失败", "没通过验收", "退出码 1", `--- FAIL: TestGreet\nFAIL example.com/demo`, "Agent 的总结", `I changed greet.go.\nThe test still fails.`,
		"ats/example-1", "0123456", "历次尝试", "第 1 次")
	for _, bad := range []string{"0123456789abcdef", "task_failed", "session-1", "现在"} {
		if strings.Contains(raw, bad) {
			t.Errorf("failed: the page has %q", bad)
		}
	}
	if v.RefreshAfter != 0 {
		t.Error("the page of a finished task is drawn again")
	}
	if a := button(v, "retry"); a == nil || a.ID != "retry:"+failed.ID {
		t.Errorf("the retry button of a failed task: %+v", a)
	}
	if button(v, "cancel") != nil {
		t.Error("a failed task offers a cancel")
	}
	out := findRow(t, v, "accept-output")
	if out.Type != wire.ComponentCode || out.Text != "--- FAIL: TestGreet\nFAIL example.com/demo" {
		t.Errorf("the acceptance output: %+v", out)
	}

	// Failed before acceptance: the message that stopped it, whole, and no exit code made up.
	broken := p.enqueue(EnqueueInput{Title: "Broken task", Repo: "/srv/code/app", AcceptanceCommand: "go test ./...", MaxRetries: &none})
	long := "Agent 这一轮出错：There's an issue with the selected model (example-model). It may not exist or you may not have access to it. Run /model to pick a different model."
	p.complete(broken.ID, p.lease(broken.ID, PhaseWorking, ""), CompleteAttemptInput{Outcome: StateFailed, ErrorClass: "config_error", Message: long})
	_, raw = page(broken.ID)
	has("config error", raw, "模型或配置不可用", "Run /model to pick a different model.", "验收命令没有跑")
	if strings.Contains(raw, "退出码") {
		t.Error("config error: the page shows an exit code of a command that never ran")
	}

	// Passed: nothing to press.
	passed := p.enqueue(EnqueueInput{Title: "Passed task", Repo: "/srv/code/app", AcceptanceCommand: "go test ./..."})
	p.complete(passed.ID, p.lease(passed.ID, PhaseAccepting, ""), CompleteAttemptInput{Outcome: StateSucceeded, Verdict: VerdictPass, ExitCode: &zero, Message: "验收命令退出码 0\nok  example.com/demo", Summary: "Added Farewell and its test."})
	v, raw = page(passed.ID)
	has("passed", raw, "通过验收", "退出码 0", "ok  example.com/demo", "Added Farewell and its test.")
	if button(v, "retry") != nil || button(v, "cancel") != nil {
		t.Error("a passed task has buttons")
	}

	// No acceptance command: the page says what that means.
	plain := p.enqueue(EnqueueInput{Title: "Plain task", Repo: "/srv/code/app"})
	_, raw = page(plain.ID)
	has("no acceptance", raw, "没有验收命令", "排队中")

	// A task that is gone: an empty page, not an error.
	v, raw = page("task_gone")
	has("unknown", raw, "找不到这个任务")
	if len(v.Sections) != 1 || v.Sections[0].Items[0].Type != wire.ComponentEmpty {
		t.Errorf("the page of an unknown task: %s", raw)
	}
}

func TestTaskPageButtonsCancelAndRetryAndStayOnThePage(t *testing.T) {
	r := newRig(t)
	a := newATSApp(r.q)
	a.cfg, a.disp = r.cfg, r.d
	a.nowFunc = func() time.Time { return r.now }

	task := r.enqueue(EnqueueInput{Title: "Working task"})
	r.tick()
	state := ext.EncodeState(taskPage{Task: task.ID})
	req := func(action string) *ext.UIRequest {
		return &ext.UIRequest{UIParams: wire.UIParams{Contribution: queueUI, Action: action, State: state}}
	}

	// The queue's rows open the page: render with the pushed state draws the task, not the queue.
	view, err := a.render(t.Context(), req(""))
	if err != nil || view.State != state || !strings.Contains(checked(t, view), "取消任务") {
		t.Fatalf("the task's page: %+v %v", view, err)
	}
	// An app that does not push pages itself asks with the row's action id.
	res, err := a.action(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{Contribution: queueUI, Action: "open:" + task.ID}})
	if err != nil || res.Push == nil || res.Push.State != state || res.View != nil {
		t.Fatalf("open: %+v %v", res, err)
	}
	if got := r.task(task.ID); got.State != StateRunning {
		t.Fatalf("opening the page changed the task: %s", got.State)
	}

	// Cancel from the page goes through the dispatcher and draws the page again, now with a retry.
	res, err = a.action(context.Background(), req("cancel:"+task.ID))
	if err != nil || !strings.Contains(res.Toast, "已取消") || len(r.hub.interrupts) != 1 || r.task(task.ID).State != StateCanceled {
		t.Fatalf("cancel: %+v %v, interrupts %v", res, err, r.hub.interrupts)
	}
	if res.View == nil || res.View.State != state {
		t.Fatalf("after cancel the answer is not the task's page: %+v", res.View)
	}
	raw := checked(t, res.View)
	if !strings.Contains(raw, "retry:"+task.ID) || strings.Contains(raw, "cancel:"+task.ID) || strings.Contains(raw, "排队和在跑") {
		t.Errorf("the page after cancel: %s", raw)
	}

	res, err = a.action(context.Background(), req("retry:"+task.ID))
	if err != nil || !strings.Contains(res.Toast, "已重新排队") || r.task(task.ID).State != StateQueued {
		t.Fatalf("retry: %+v %v", res, err)
	}
	if raw := checked(t, res.View); !strings.Contains(raw, "cancel:"+task.ID) || !strings.Contains(raw, "排队中") {
		t.Errorf("the page after retry: %s", raw)
	}
}

func TestFailureWords(t *testing.T) {
	for _, c := range []struct{ class, verdict, want string }{
		{"config_error", "", "模型或配置不可用"},
		{"auth_expired", "", "Agent 登录失效"},
		{"rate_limited", "", "额度或限流"},
		{"infra_error", "", "电脑或环境出错"},
		{"timeout", "", "超时"},
		{"timeout", VerdictFail, "验收命令超时"},
		{"task_failed", VerdictFail, "没通过验收"},
		{"task_failed", VerdictUnverified, "Agent 出错"},
		{"", "", "Agent 出错"},
		{"lost", "", "会话丢失"},
		{"rejected", "", "任务本身有问题（仓库或 ref 不对）"},
		{"something_new", "", "出错（something_new）"},
	} {
		if got := failureWords(c.class, c.verdict); got != c.want {
			t.Errorf("failureWords(%q, %q) = %q, want %q", c.class, c.verdict, got, c.want)
		}
	}
	if got := repoName("/srv/code/app/"); got != "app" {
		t.Errorf("repoName: %q", got)
	}
	if got := oneLine(" a\n\n b\tc ", 10); got != "a b c" {
		t.Errorf("oneLine: %q", got)
	}
	if head, tail := acceptanceOutput("验收命令退出码 1\nline 1\nline 2\n"); head != "验收命令退出码 1" || tail != "line 1\nline 2" {
		t.Errorf("acceptanceOutput: %q %q", head, tail)
	}
}

func TestOutcomeDecisionSaysWhatHappenedInPlainWords(t *testing.T) {
	p := newPageRig(t)
	zero, one, none := 0, 1, 0
	if err := p.q.SetMachines([]MachineSlot{{Name: testMachine, Title: "Studio", Online: true, MaxSlots: 8}}); err != nil {
		t.Fatal(err)
	}
	decision := func(id string) OutcomeDecisionPayload {
		t.Helper()
		task, err := p.q.GetTask(id)
		if err != nil {
			t.Fatal(err)
		}
		d, ok := BuildOutcomeDecision(task)
		if !ok {
			t.Fatalf("no decision for %s (%s)", id, task.State)
		}
		return d
	}

	passed := p.enqueue(EnqueueInput{Title: "Add Farewell and its test", Repo: "/srv/code/app", AcceptanceCommand: "go test ./...", PermissionProfile: PermissionDefault})
	p.complete(passed.ID, p.lease(passed.ID, PhaseAccepting, ""), CompleteAttemptInput{Outcome: StateSucceeded, Verdict: VerdictPass, ExitCode: &zero,
		Message: "验收命令退出码 0\nok", Summary: "Added Farewell\nand a test for it.",
		Artifacts: []Artifact{{Kind: "commit", Path: "0123456789abcdef0123456789abcdef01234567"}, {Kind: "branch", Path: "ats/example-1"}, {Kind: "session", Path: "machine-1/session-1"}}})
	d := decision(passed.ID)
	if d.Question != "ATS 任务「Add Farewell and its test」的结果" || d.Choice != "通过验收" || d.Ref != "ats:"+passed.ID+":succeeded:pass" {
		t.Errorf("passed: %+v", d)
	}
	if want := "Agent 的总结: Added Farewell and a test for it.；验收: `go test ./...` 退出码 0；产物: 分支 ats/example-1 · 提交 0123456；任务 " + passed.ID; d.Reason != want {
		t.Errorf("passed reason:\n got %s\nwant %s", d.Reason, want)
	}

	// Failed before the acceptance command ran: the command is not listed as if it had; the message is.
	broken := p.enqueue(EnqueueInput{Title: strings.Repeat("长", 50), Repo: "/srv/code/app", AcceptanceCommand: "go test ./...", MaxRetries: &none})
	p.complete(broken.ID, p.lease(broken.ID, PhaseWorking, ""), CompleteAttemptInput{Outcome: StateFailed, ErrorClass: "config_error", Message: "Agent 这一轮出错：the model is not available\nmore"})
	d = decision(broken.ID)
	if d.Choice != "失败：模型或配置不可用（试了 1 次）" || d.Ref != "ats:"+broken.ID+":failed" {
		t.Errorf("config error: %+v", d)
	}
	if d.Question != "ATS 任务「"+strings.Repeat("长", 40)+"…」的结果" {
		t.Errorf("a long title is not cut: %s", d.Question)
	}
	if want := "原因: Agent 这一轮出错：the model is not available；任务 " + broken.ID; d.Reason != want {
		t.Errorf("config error reason:\n got %s\nwant %s", d.Reason, want)
	}

	// Failed acceptance after a retry: the attempts are counted, the ref keeps its attempt.
	twice := p.enqueue(EnqueueInput{Title: "Fix the test", Repo: "/srv/code/app", AcceptanceCommand: "go test ./...", MaxRetries: &one})
	p.complete(twice.ID, p.lease(twice.ID, PhaseAccepting, ""), CompleteAttemptInput{Outcome: StateFailed, Verdict: VerdictFail, ErrorClass: "task_failed", ExitCode: &one, Message: "验收命令退出码 1\nFAIL"})
	if _, ok := BuildOutcomeDecision(mustTask(t, p.q, twice.ID)); ok {
		t.Fatal("a task that waits for its retry has a decision")
	}
	_, att, err := p.q.LeaseTask(twice.ID, "dispatcher", testMachine, testStart.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.q.CompleteAttempt(CompleteAttemptInput{TaskID: twice.ID, FenceToken: att.FenceToken, WorkerID: "dispatcher", Now: testStart.Add(time.Hour + time.Minute),
		Outcome: StateFailed, Verdict: VerdictFail, ErrorClass: "task_failed", ExitCode: &one, Message: "验收命令退出码 1\nFAIL"}); err != nil {
		t.Fatal(err)
	}
	d = decision(twice.ID)
	if d.Choice != "失败：没通过验收（试了 2 次）" || d.Ref != "ats:"+twice.ID+":2:failed" || !strings.HasPrefix(d.Reason, "验收: `go test ./...` 退出码 1；") {
		t.Errorf("failed twice: %+v", d)
	}

	// No acceptance command, canceled, and a long summary held to the cap with whole characters.
	plain := p.enqueue(EnqueueInput{Title: "Look around", Repo: "/srv/code/app"})
	p.complete(plain.ID, p.lease(plain.ID, PhaseWorking, ""), CompleteAttemptInput{Outcome: StateSucceeded, Verdict: VerdictUnverified, Message: "the reply", Summary: strings.Repeat("字", 3000)})
	d = decision(plain.ID)
	if d.Choice != "跑完了，没有验收命令" || strings.Contains(d.Reason, "验收") {
		t.Errorf("unverified: %+v", d)
	}
	if n := len([]rune(d.Reason)); n > decisionReasonMost+1 || !strings.HasSuffix(d.Reason, "任务 "+plain.ID) || !strings.Contains(d.Reason, strings.Repeat("字", 400)+"…") {
		t.Errorf("a long summary: %d characters, ends %q", n, d.Reason[max(0, len(d.Reason)-40):])
	}
	canceled := p.enqueue(EnqueueInput{Title: "Never mind", Repo: "/srv/code/app"})
	if _, err := p.q.CancelTask(canceled.ID, testStart); err != nil {
		t.Fatal(err)
	}
	if d = decision(canceled.ID); d.Choice != "已取消" || d.Reason != "任务 "+canceled.ID {
		t.Errorf("canceled: %+v", d)
	}
}

func mustTask(t *testing.T, q *Queue, id string) *Task {
	t.Helper()
	task, err := q.GetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestGateCardSaysWhatApprovingDoes(t *testing.T) {
	var gate struct {
		Actions []struct{ Pattern, What string } `json:"actions"`
	}
	if err := json.Unmarshal(mustRead(t, "assistant/gate.json"), &gate); err != nil {
		t.Fatal(err)
	}
	if len(gate.Actions) != 1 || !strings.Contains(gate.Actions[0].What, "无人值守") || !strings.Contains(gate.Actions[0].What, "你的电脑") {
		t.Errorf("the card's wording: %+v", gate.Actions)
	}
	skill := string(mustRead(t, "skills/ats-todo/SKILL.md"))
	for _, want := range []string{"确认卡上只有这条命令的原文", "config_error", "接着上一次尝试的提交"} {
		if !strings.Contains(skill, want) {
			t.Errorf("the skill does not have %q", want)
		}
	}
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
