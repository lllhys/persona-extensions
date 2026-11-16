package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// taskPage is the state of a task's page: the page the queue pushes when a row is tapped.
type taskPage struct {
	Task string `json:"task,omitempty"`
}

func taskPush(id, title string) *wire.Push {
	return &wire.Push{State: ext.EncodeState(taskPage{Task: id}), Title: firstLinePreview(title, 40)}
}

// pageTask is the task whose page a render or an action belongs to; empty on the queue page.
func pageTask(r *ext.UIRequest) string {
	var st taskPage
	if err := r.DecodeState(&st); err != nil || len(st.Task) > 128 {
		return ""
	}
	return strings.TrimSpace(st.Task)
}

const (
	detailTextMost = 2000 // a text on the page, in characters
	detailCodeMost = 6000 // the output tail on the page, in characters: its end is what tells
)

// buildTaskView is one task's page: what it is, what it is doing or how it ended, the evidence (the acceptance
// command with its exit code and output, what the agent said it did, what it left behind), every attempt, and the
// buttons that change it.
func buildTaskView(q *Queue, id string, now time.Time, tz string) *wire.View {
	loc := viewLocation(tz)
	state := ext.EncodeState(taskPage{Task: id})
	t, err := q.GetTask(id)
	if err != nil || t == nil {
		return &wire.View{Title: "任务", State: state, Sections: []wire.Section{{Items: []wire.Component{
			{Type: wire.ComponentEmpty, Title: "找不到这个任务", Text: "它可能已经从队列里清掉了。回到队列页看看现在有什么。"},
		}}}}
	}
	view := &wire.View{Title: firstLinePreview(t.Title, 40), State: state}
	if !t.IsTerminal() {
		view.RefreshAfter = 30
	}
	machines := map[string]string{}
	for _, m := range q.PlanSchedule(now).Machines {
		if m.Title != "" {
			machines[m.Name] = m.Title
		}
	}
	machineName := func(id string) string { return cmpOr(machines[id], id) }
	clock := func(at time.Time) string { return at.In(loc).Format("01-02 15:04") }

	// The header: what the task is.
	facts := []wire.Fact{
		{Label: "优先级", Value: priorityWords(t.Priority)},
		{Label: "难度 · 模型档位", Value: t.Difficulty + " · " + t.ModelTier},
		{Label: "权限", Value: permissionWords(t.PermissionProfile)},
	}
	if t.Workspace.Repo != "" {
		facts = append(facts, wire.Fact{Label: "仓库", Value: t.Workspace.Repo + " @ " + cmpOr(t.Workspace.BaseSHA, "HEAD")})
	}
	machine := t.Machine
	if t.Exec != nil && t.Exec.MachineID != "" {
		machine = t.Exec.MachineID
	} else if n := len(t.Attempts); n > 0 && t.Attempts[n-1].Machine != "" {
		machine = t.Attempts[n-1].Machine
	}
	if machine != "" {
		facts = append(facts, wire.Fact{Label: "电脑", Value: machineName(machine)})
	}
	// A header holds eight facts at most, so the two times share one.
	switch {
	case t.FinishedAt != nil && !t.SubmittedAt.IsZero():
		facts = append(facts, wire.Fact{Label: "提交 – 结束", Value: clock(t.SubmittedAt) + " – " + clock(*t.FinishedAt)})
	case t.FinishedAt != nil:
		facts = append(facts, wire.Fact{Label: "结束", Value: clock(*t.FinishedAt)})
	case !t.SubmittedAt.IsZero():
		facts = append(facts, wire.Fact{Label: "提交", Value: clock(t.SubmittedAt)})
	}
	if todo := t.TodoID(); todo != "" {
		facts = append(facts, wire.Fact{Label: "待办", Value: todo})
	}
	facts = append(facts, wire.Fact{Label: "任务", Value: t.ID})
	head := []wire.Component{{Type: wire.ComponentHeader, Key: "task", Title: t.Title, Badges: taskBadges(t), Facts: facts}}
	switch {
	case !t.IsTerminal():
		a := cancelAction(t)
		head = append(head, wire.Component{Type: wire.ComponentButton, Key: "cancel", Title: "取消任务", Role: wire.ButtonRoleDestructive, Action: &a})
	case t.State != StateSucceeded:
		a := retryAction(t)
		head = append(head, wire.Component{Type: wire.ComponentButton, Key: "retry", Title: "重试", Action: &a})
	}
	view.Sections = append(view.Sections, wire.Section{Items: head})

	// Now: what a task that has not ended is doing, or waiting for.
	rep := lastReport(t)
	if !t.IsTerminal() {
		var lines []string
		tone := ""
		switch e := t.Exec; {
		case t.State == StateNeedsInput:
			waiting := waitingInSession
			if e != nil && strings.TrimSpace(e.Waiting) != "" {
				waiting = strings.TrimSpace(e.Waiting)
			}
			lines = append(lines, waiting, "到这个任务的 Agent 会话里回答或批准，任务会接着跑。")
			tone = wire.ToneDanger
		case e != nil && isActiveState(t.State):
			line := attemptCount(t.AttemptNo) + "：" + phaseWords(e.Phase)
			if e.MachineID != "" {
				line += "（" + machineName(e.MachineID) + "）"
			}
			if !e.PhaseSince.IsZero() {
				line += "，从 " + e.PhaseSince.In(loc).Format("15:04") + " 起"
			}
			lines = append(lines, line)
		case t.NotBefore != nil:
			lines = append(lines, fmt.Sprintf("第 %d 次失败（%s），%s 后重试，还能再试 %d 次。", max(t.AttemptNo, 1),
				failureWords(lastErrorClass(t), lastVerdict(t)), t.NotBefore.In(loc).Format("15:04"), t.RetriesLeft))
		default:
			lines = append(lines, "排队中：等空位和额度。")
		}
		view.Sections = append(view.Sections, wire.Section{Title: "现在", Items: []wire.Component{
			{Type: wire.ComponentText, Key: "now", Text: strings.Join(lines, "\n"), Tone: tone},
		}})
	}

	// How the last attempt ended, in words; the acceptance command's own output is in the next section.
	if rep != nil {
		title := "结果"
		if !t.IsTerminal() {
			title = "上一次尝试"
		}
		text := attemptCount(rep.AttemptNo) + "：" + outcomeWords(rep.Outcome, rep.Verdict, rep.ErrorClass)
		if rep.ExitCode == nil && rep.Outcome != StateSucceeded {
			if msg := strings.TrimSpace(rep.Message); msg != "" {
				text += "\n" + truncateRunes(msg, detailTextMost)
			}
		}
		if t.IsTerminal() {
			switch {
			case t.DecisionWrittenRef != "":
				text += "\n结论已写回待办。"
			case t.DecisionSkipReason != "":
				text += "\n结论没有写回待办（待办已不在，或没有权限）。"
			}
		}
		view.Sections = append(view.Sections, wire.Section{Title: title, Items: []wire.Component{
			{Type: wire.ComponentText, Key: "result", Text: text},
		}})
	}

	// Acceptance: the command, and what it answered when it ran.
	var accept []wire.Component
	if t.Acceptance != nil && strings.TrimSpace(t.Acceptance.Command) != "" {
		accept = append(accept, wire.Component{Type: wire.ComponentCode, Key: "accept-command", Language: "sh", Text: strings.TrimSpace(t.Acceptance.Command)})
		switch {
		case rep != nil && rep.ExitCode != nil:
			headLine, tail := acceptanceOutput(rep.Message)
			line := fmt.Sprintf("%s跑的结果：退出码 %d", attemptCount(rep.AttemptNo), *rep.ExitCode)
			if rep.ErrorClass == "timeout" && headLine != "" {
				line += "（" + headLine + "）"
			}
			tone := wire.TonePositive
			if *rep.ExitCode != 0 || rep.Verdict != VerdictPass {
				tone = wire.ToneDanger
			}
			accept = append(accept, wire.Component{Type: wire.ComponentText, Key: "accept-exit", Text: line, Tone: tone})
			if tail != "" {
				accept = append(accept, wire.Component{Type: wire.ComponentCode, Key: "accept-output", Text: tailRunes(tail, detailCodeMost)})
			}
		case t.IsTerminal():
			accept = append(accept, wire.Component{Type: wire.ComponentText, Key: "accept-exit", Style: wire.TextStyleSecondary, Text: "验收命令没有跑：任务在那之前就结束了。"})
		default:
			accept = append(accept, wire.Component{Type: wire.ComponentText, Key: "accept-exit", Style: wire.TextStyleSecondary, Text: "Agent 做完后在任务的工作目录里跑，退出码 0 算通过。"})
		}
	} else {
		accept = append(accept, wire.Component{Type: wire.ComponentText, Key: "accept-none", Style: wire.TextStyleSecondary, Text: "这个任务没有验收命令：跑完只能算「跑完了」，不算通过验收。"})
	}
	view.Sections = append(view.Sections, wire.Section{Title: "验收", Items: accept})

	if rep != nil && strings.TrimSpace(rep.Summary) != "" {
		view.Sections = append(view.Sections, wire.Section{Title: "Agent 的总结", Items: []wire.Component{
			{Type: wire.ComponentText, Key: "summary", Text: truncateRunes(rep.Summary, detailTextMost)},
		}})
	}

	if rows := artifactRows(t, rep); len(rows) > 0 {
		view.Sections = append(view.Sections, wire.Section{Title: "产物", Items: []wire.Component{
			{Type: wire.ComponentTable, Key: "artifacts", Columns: []wire.TableColumn{{Title: "留下了什么"}, {Title: "在哪"}}, Rows: rows},
		}, Footer: "分支和提交在跑任务的那台电脑的仓库里，没有 push。"})
	}

	if prompt := strings.TrimSpace(t.Prompt); prompt != "" {
		view.Sections = append(view.Sections, wire.Section{Title: "任务说明", Items: []wire.Component{
			{Type: wire.ComponentText, Key: "prompt", Text: truncateRunes(prompt, detailTextMost)},
		}})
	}

	if len(t.Attempts) > 0 {
		rows := make([][]string, 0, len(t.Attempts))
		for _, a := range t.Attempts {
			at := ""
			switch {
			case a.FinishedAt != nil:
				at = clock(*a.FinishedAt)
			case a.StartedAt != nil:
				at = clock(*a.StartedAt) + " 开始"
			}
			state := a.State
			if isActiveState(state) && a.AttemptNo == t.AttemptNo {
				state = t.State // the task knows that its attempt waits for the user
			}
			rows = append(rows, []string{attemptCount(a.AttemptNo), outcomeWords(state, a.Verdict, a.ErrorClass), machineName(a.Machine), at})
		}
		view.Sections = append(view.Sections, wire.Section{Title: "历次尝试", Items: []wire.Component{
			{Type: wire.ComponentTable, Key: "attempts", Columns: []wire.TableColumn{{Title: "尝试"}, {Title: "结果"}, {Title: "电脑"}, {Title: "时间", Align: "end"}}, Rows: rows},
		}})
	}
	return view
}

func taskBadges(t *Task) []wire.Badge {
	switch t.State {
	case StateSucceeded:
		if t.Verdict == VerdictPass {
			return []wire.Badge{{Text: "通过验收", Tone: wire.TonePositive}}
		}
		return []wire.Badge{{Text: "跑完未验收", Tone: wire.ToneCaution}}
	case StateFailed:
		return []wire.Badge{{Text: "失败", Tone: wire.ToneDanger}, {Text: failureWords(lastErrorClass(t), t.Verdict), Tone: wire.ToneDanger}}
	case StateNeedsInput:
		return []wire.Badge{{Text: "等你处理", Tone: wire.ToneDanger}}
	case StateQueued:
		if t.NotBefore != nil {
			return []wire.Badge{{Text: "等待重试", Tone: wire.ToneCaution}}
		}
		return []wire.Badge{{Text: "排队中", Tone: wire.ToneCaution}}
	case StateRunning, StateLeased:
		phase := ""
		if t.Exec != nil {
			phase = t.Exec.Phase
		}
		return []wire.Badge{{Text: phaseWords(phase), Tone: wire.TonePositive}}
	}
	return []wire.Badge{{Text: outcomeWords(t.State, t.Verdict, ""), Tone: wire.ToneCaution}}
}

// artifactRows lists what the task left on its computer: of a finished attempt what its report names, of a
// running one the branch and worktree it works in.
func artifactRows(t *Task, rep *EvidenceReport) [][]string {
	var rows [][]string
	if e := t.Exec; e != nil && isActiveState(t.State) {
		if e.Branch != "" {
			rows = append(rows, []string{"分支（在用）", e.Branch})
		}
		if e.Worktree != "" {
			rows = append(rows, []string{"工作目录（在用）", e.Worktree})
		}
		return rows
	}
	if rep == nil {
		return nil
	}
	for _, a := range rep.Artifacts {
		p := strings.TrimSpace(a.Path)
		if p == "" {
			continue
		}
		switch a.Kind {
		case "branch":
			rows = append(rows, []string{"分支", p})
		case "commit":
			rows = append(rows, []string{"提交", shortSHA(p)})
		case "worktree":
			rows = append(rows, []string{"保留的工作目录（有没提交的改动）", p})
		case "session":
			rows = append(rows, []string{"Agent 会话", "挂在这个任务的待办上"})
		default:
			rows = append(rows, []string{a.Kind, p})
		}
	}
	return rows
}
