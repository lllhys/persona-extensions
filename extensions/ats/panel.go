package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

const queueUI = "queue"

// ExtConfig is the extension's settings (configuration in persona-extension.json). Both programs read it: the
// hub program all of it, the program on a computer the repository allowlist.
type ExtConfig struct {
	Machines             []string `json:"machines"`
	Repos                []string `json:"repos"`
	ParallelPerMachine   int      `json:"parallel_per_machine"`
	WritePermissionMode  string   `json:"write_permission_mode"`
	TaskTimeoutMinutes   int      `json:"task_timeout_minutes"`
	AcceptTimeoutMinutes int      `json:"acceptance_timeout_minutes"`
	MaxQueued            int      `json:"max_queued"`
	MaxRetries           int      `json:"max_retries"`
	ReserveHeadroomPct   int      `json:"reserve_headroom_pct"`
	ModelFast            string   `json:"model_fast"`
	ModelStandard        string   `json:"model_standard"`
	ModelStrong          string   `json:"model_strong"`
}

func defaultExtConfig() ExtConfig {
	return ExtConfig{
		ParallelPerMachine: 1, WritePermissionMode: "acceptEdits", TaskTimeoutMinutes: 120, AcceptTimeoutMinutes: 10,
		MaxQueued: 100, MaxRetries: 2, ReserveHeadroomPct: 30,
	}
}

func (c ExtConfig) parallel() int { return min(max(c.ParallelPerMachine, 1), 4) }

// writeMode is the permission mode of the session of a task that may write. Read-only tasks always run in plan.
func (c ExtConfig) writeMode() string {
	switch c.WritePermissionMode {
	case "default", "acceptEdits", "auto", "bypassPermissions":
		return c.WritePermissionMode
	}
	return "acceptEdits"
}

func (c ExtConfig) taskTimeout() time.Duration {
	m := c.TaskTimeoutMinutes
	if m <= 0 {
		m = 120
	}
	return time.Duration(min(m, 24*60)) * time.Minute
}

func (c ExtConfig) acceptTimeout() time.Duration {
	m := c.AcceptTimeoutMinutes
	if m <= 0 {
		return defaultAcceptTime
	}
	return min(time.Duration(m)*time.Minute, maxAcceptTime)
}

// model is the model a session of this tier asks for; empty leaves the agent's own default.
func (c ExtConfig) model(tier string) string {
	switch tier {
	case TierFast:
		return strings.TrimSpace(c.ModelFast)
	case TierStrong:
		return strings.TrimSpace(c.ModelStrong)
	}
	return strings.TrimSpace(c.ModelStandard)
}

func (c ExtConfig) queueConfig() QueueConfig {
	q := DefaultQueueConfig()
	if c.MaxQueued > 0 {
		q.MaxQueued = c.MaxQueued
	}
	if c.MaxRetries >= 0 {
		q.MaxRetries = c.MaxRetries
	}
	if c.ReserveHeadroomPct > 0 {
		q.ReserveHeadroomPct = float64(c.ReserveHeadroomPct)
	}
	return q
}

// atsApp is the hub program: the queue, its dispatcher, the queue page and the HTTP API the assistant calls.
type atsApp struct {
	mu      sync.Mutex
	id      string
	cfg     ExtConfig
	queue   *Queue
	writer  *DecisionWriter
	disp    *dispatcher
	hub     *ext.Hub
	hubURL  string
	token   string
	nowFunc func() time.Time
	// api and nodes are set by tests; init makes the real ones.
	api   hubAPI
	nodes nodeCaller
}

func newATSApp(q *Queue) *atsApp {
	return &atsApp{id: "ats", cfg: defaultExtConfig(), queue: q, nowFunc: func() time.Time { return time.Now().UTC() }}
}

func (a *atsApp) now() time.Time { return a.nowFunc() }

func (a *atsApp) config() ExtConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

func (a *atsApp) program(id, version string) ext.Program {
	a.id = id
	return ext.Program{
		ID:       id,
		Version:  version,
		Manifest: manifest,
		Init:     a.init,
		HTTP:     a.handler(),
		UI:       map[string]ext.Page{queueUI: {Render: a.render, Action: a.action}},
		Namespaces: map[string]ext.Handler{
			"assistant": a.assistant,
			"node":      a.nodeEvent,
		},
	}
}

func (a *atsApp) init(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	c := defaultExtConfig()
	if err := in.Config(&c); err != nil {
		return nil, err
	}
	dataDir := strings.TrimSpace(in.Info().DataDir)
	if dataDir == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		dataDir = filepath.Join(dir, "persona-ats")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg, a.hub, a.hubURL, a.token = c, hub, strings.TrimRight(in.Hub.URL, "/"), in.Hub.Token
	if a.queue == nil {
		q, err := NewQueue(dataDir, c.queueConfig())
		if err != nil {
			return nil, err
		}
		a.queue = q
	} else {
		a.queue.UpdateConfig(c.queueConfig())
	}
	if a.writer == nil {
		a.writer = NewDecisionWriter(in.Hub.URL, in.Hub.Token, in.Hub.Scopes, &http.Client{Timeout: 10 * time.Second})
	} else {
		a.writer.Configure(in.Hub.URL, in.Hub.Token, in.Hub.Scopes)
	}
	if a.disp != nil {
		a.disp.poke()
		return nil, nil
	}
	if a.api == nil {
		a.api = newHubClient(in.Hub.URL, in.Hub.Token)
	}
	if a.nodes == nil {
		a.nodes = rpcNodes{hub: hub}
	}
	a.disp = a.newDispatcher()
	go a.disp.run(context.Background()) // ends with the process: the hub stops the program by closing stdin
	return nil, nil
}

func (a *atsApp) newDispatcher() *dispatcher {
	d := newDispatcher(a.queue, a.api, a.nodes, a.writer, a.config, a.now)
	d.refresh = a.refreshQuota
	d.changed = func() {
		a.mu.Lock()
		hub := a.hub
		a.mu.Unlock()
		if hub != nil {
			_ = hub.Invalidate(queueUI)
		}
	}
	return d
}

// refreshQuota reads the usage windows of the hub's gateway accounts, which decide what the scheduler admits.
func (a *atsApp) refreshQuota(ctx context.Context) {
	a.mu.Lock()
	hub, q := a.hub, a.queue
	a.mu.Unlock()
	if hub == nil || q == nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var ga GenericGatewayAccounts
	if err := hub.Call(cctx, "usage.gateway_accounts", map[string]any{}, &ga); err == nil && (len(ga.Accounts) > 0 || len(ga.Sources) > 0) {
		_ = q.SetQuotaStatus(ParseGatewayAccounts(ga, a.now()))
	}
}

// assistant answers assistant.credentials: what the assistant's gated command needs to reach this program's HTTP
// API. The token is the extension's own, whose scopes include calling this extension and nothing wider of the
// API; the gate keeps it out of the agent's sight.
func (a *atsApp) assistant(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method != "credentials" {
		return nil, ext.UnknownMethod("assistant." + method)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.hubURL == "" || a.token == "" {
		return nil, errors.New("the hub gave no API address or token (permissions.hub not granted)")
	}
	return map[string]any{"env": map[string]string{
		"ATS_BASE":  a.hubURL + "/api/v1/extensions/" + a.id,
		"ATS_TOKEN": a.token,
	}}, nil
}

// nodeEvent takes what the program on a computer sends: the result of an acceptance command.
func (a *atsApp) nodeEvent(ctx context.Context, method string, params json.RawMessage) (any, error) {
	var ev struct {
		Event  string          `json:"event"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return nil, err
	}
	a.mu.Lock()
	d := a.disp
	a.mu.Unlock()
	if ev.Event == "ats.accepted" && d != nil {
		var res acceptResult
		if err := json.Unmarshal(ev.Params, &res); err == nil {
			d.onAccepted(res)
		}
	}
	return map[string]any{}, nil
}

func (a *atsApp) render(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	if r.Contribution != "" && r.Contribution != queueUI {
		return nil, ext.NoContribution(r.Contribution)
	}
	q := a.q()
	if q == nil {
		return nil, errors.New("the queue is not ready")
	}
	if id := pageTask(r); id != "" {
		return buildTaskView(q, id, a.now(), r.TZ), nil
	}
	return buildQueueView(q, a.config(), a.now(), r.TZ), nil
}

func (a *atsApp) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	if r.Contribution != "" && r.Contribution != queueUI {
		return nil, ext.NoContribution(r.Contribution)
	}
	q := a.q()
	if q == nil {
		return nil, errors.New("the queue is not ready")
	}
	a.mu.Lock()
	d := a.disp
	a.mu.Unlock()
	now := a.now()
	kind, target, _ := strings.Cut(strings.TrimSpace(r.Action), ":")

	var toast string
	switch kind {
	case "open":
		// An app that does not open pages itself asks for the task's page this way.
		title := "任务"
		if t, err := q.GetTask(target); err == nil {
			title = t.Title
		}
		return &wire.ActionResult{Push: taskPush(target, title)}, nil
	case "retry":
		if t, err := q.RetryTask(target, now); err != nil {
			toast = "重试失败：" + err.Error()
		} else {
			toast = "已重新排队：" + t.Title
		}
	case "cancel":
		var t *Task
		var err error
		if d != nil {
			t, err = d.cancel(ctx, target)
		} else {
			t, err = q.CancelTask(target, now)
		}
		if err != nil {
			toast = "取消失败：" + err.Error()
		} else {
			toast = "已取消：" + t.Title
		}
	}
	if d != nil {
		d.poke()
	}
	// The action came from a task's own page: draw that page again, not the queue.
	if id := pageTask(r); id != "" {
		return &wire.ActionResult{View: buildTaskView(q, id, now, r.TZ), Toast: toast}, nil
	}
	return &wire.ActionResult{View: buildQueueView(q, a.config(), now, r.TZ), Toast: toast}, nil
}

func viewLocation(tz string) *time.Location {
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			return l
		}
	}
	return time.UTC
}

// buildQueueView is the queue page. What the user acts on comes first: tasks that wait for them, then what is
// queued and running, then what ended; the scheduler's numbers and the computers are below.
func buildQueueView(q *Queue, cfg ExtConfig, now time.Time, tz string) *wire.View {
	loc := viewLocation(tz)
	plan := q.PlanSchedule(now)
	quota := plan.Quota
	view := &wire.View{Title: "ATS 队列", RefreshAfter: 30}

	var setup []string
	if len(cfg.Machines) == 0 {
		setup = append(setup, "还没有选电脑：在这个扩展的设置里选至少一台，任务才会开始跑。")
	}
	if len(cfg.Repos) == 0 {
		setup = append(setup, "还没有填允许的仓库目录：填了以后助手才能给这些目录下的仓库派任务。")
	}
	if len(setup) > 0 {
		view.Sections = append(view.Sections, wire.Section{Title: "还差一步", Items: []wire.Component{
			{Type: wire.ComponentText, Text: strings.Join(setup, "\n")},
		}})
	}

	names := map[string]string{}
	for _, m := range plan.Machines {
		if m.Title != "" {
			names[m.Name] = m.Title
		}
	}
	tasks, _ := q.ListTasks(TaskFilter{Limit: 60})
	var needs, open, done []wire.Component
	for _, t := range tasks {
		switch {
		case t.IsTerminal():
			done = append(done, terminalRow(t, loc))
		case t.State == StateNeedsInput:
			needs = append(needs, activeRow(t, names, loc))
		default:
			open = append(open, activeRow(t, names, loc))
		}
	}
	if len(needs) > 0 {
		view.Sections = append(view.Sections, wire.Section{Title: "等你处理", Items: needs,
			Footer: "到任务的 Agent 会话里回答或批准，任务会接着跑。"})
	}
	if len(open) == 0 && len(needs) == 0 {
		open = []wire.Component{{Type: wire.ComponentEmpty, Title: "没有排队或在跑的任务",
			Text: "跟助手说「把这个待办交给 ATS 做」，确认后任务会出现在这里。"}}
	}
	if len(open) > 0 { // with tasks waiting for the user above, an empty placeholder only pushes the rest down
		view.Sections = append(view.Sections, wire.Section{Title: "排队和在跑", Items: open})
	}
	if len(done) == 0 {
		done = []wire.Component{{Type: wire.ComponentText, Style: wire.TextStyleSecondary, Text: "还没有跑完的任务。"}}
	}
	view.Sections = append(view.Sections, wire.Section{Title: "最近结束", Items: done})

	modeTone := wire.TonePositive
	switch plan.AdmissionMode {
	case AdmissionPriorityOnly:
		modeTone = wire.ToneCaution
	case AdmissionPausedQuota:
		modeTone = wire.ToneDanger
	}
	quotaTone := wire.TonePositive
	if quota.Exhausted || quota.RemainingPct <= 10 {
		quotaTone = wire.ToneDanger
	} else if quota.RemainingPct < plan.ReserveHeadroomPct {
		quotaTone = wire.ToneCaution
	}
	quotaDetail := fmt.Sprintf("保留线 %.0f%%", plan.ReserveHeadroomPct)
	if quota.ResetAt != nil {
		quotaDetail = fmt.Sprintf("%s 重置 · 保留线 %.0f%%", quota.ResetAt.In(loc).Format("15:04"), plan.ReserveHeadroomPct)
	}
	active := plan.CountsByState[StateRunning]
	overview := []wire.Component{
		{Type: wire.ComponentStatGrid, Stats: []wire.Stat{
			{Label: "调度档位", Value: plan.AdmissionLabel, Detail: fmt.Sprintf("可出队 %d · 暂缓 %d", len(plan.AdmittedCandidates), len(plan.DeferredCandidates)), Tone: modeTone},
			{Label: "剩余额度", Value: fmt.Sprintf("%.0f%%", quota.RemainingPct), Detail: quotaDetail, Tone: quotaTone},
			{Label: "排队 / 上限", Value: fmt.Sprintf("%d / %d", plan.CountsByState[StateQueued], q.Config().MaxQueued),
				Detail: fmt.Sprintf("P0:%d P1:%d P2:%d P3:%d", plan.QueuedByPriority[PriorityP0], plan.QueuedByPriority[PriorityP1], plan.QueuedByPriority[PriorityP2], plan.QueuedByPriority[PriorityP3])},
			{Label: "在跑 / 等你 / 完成 / 失败", Value: fmt.Sprintf("%d / %d / %d / %d", active, plan.CountsByState[StateNeedsInput], plan.CountsByState[StateSucceeded], plan.CountsByState[StateFailed])},
		}},
	}
	// The table of tiers says something only when a tier is held back: with every tier admitted in full it
	// repeats the line above three times.
	tiers := [][]string{
		tierRow("fast", "easy", quota, TierFast, plan.ReserveHeadroomPct),
		tierRow("standard", "medium", quota, TierStandard, plan.ReserveHeadroomPct),
		tierRow("strong", "hard / extreme", quota, TierStrong, plan.ReserveHeadroomPct),
	}
	if slices.ContainsFunc(tiers, func(r []string) bool { return r[3] != tierAdmitted }) {
		overview = append(overview, wire.Component{Type: wire.ComponentTable, Title: "各模型档位的额度", Columns: []wire.TableColumn{
			{Title: "模型档位"}, {Title: "任务难度"}, {Title: "剩余额度", Align: "end"}, {Title: "准入"},
		}, Rows: tiers})
	}
	view.Sections = append(view.Sections, wire.Section{Title: "总览", Items: overview})

	if len(plan.Machines) > 0 {
		rows := make([][]string, 0, len(plan.Machines))
		for _, m := range plan.Machines {
			state := "在线"
			if !m.Online {
				state = "离线"
			}
			name := m.Title
			if name == "" {
				name = m.Name
			}
			rows = append(rows, []string{name, state, fmt.Sprintf("%d / %d", m.RunningSlots, m.MaxSlots)})
		}
		view.Sections = append(view.Sections, wire.Section{Title: "电脑", Items: []wire.Component{
			{Type: wire.ComponentTable, Columns: []wire.TableColumn{{Title: "电脑"}, {Title: "状态"}, {Title: "在跑 / 可同时跑", Align: "end"}}, Rows: rows},
		}})
	}
	return view
}

const tierAdmitted = "全部放行"

func tierRow(tier, difficulty string, quota QuotaStatus, key string, reserve float64) []string {
	rem := tierRemaining(quota, key)
	status := tierAdmitted
	switch {
	case quota.Exhausted || rem <= 5:
		status = "暂停"
	case rem < reserve:
		status = "只放行 P0、P1"
	}
	return []string{tier, difficulty, fmt.Sprintf("%.0f%%", rem), status}
}

// taskMeta is what every row says of its task, in a few words: how urgent, whether it may write, which
// repository. The to-do's id and the model tier are on the task's page.
func taskMeta(t *Task) []string {
	meta := []string{priorityWords(t.Priority), permissionWords(t.PermissionProfile)}
	if name := repoName(t.Workspace.Repo); name != "" {
		meta = append(meta, name)
	}
	return meta
}

// openTask is a row's tap: the task's page, on top of the queue. Canceling and retrying are on that page and in
// the row's menu, so that a tap never changes a task.
func openTask(t *Task) *wire.Action {
	return &wire.Action{ID: "open:" + t.ID, Push: taskPush(t.ID, t.Title)}
}

func cancelAction(t *Task) wire.Action {
	return wire.Action{ID: "cancel:" + t.ID, Confirm: fmt.Sprintf("取消任务「%s」？", t.Title)}
}

func retryAction(t *Task) wire.Action {
	return wire.Action{ID: "retry:" + t.ID, Confirm: fmt.Sprintf("重新排队「%s」？", t.Title)}
}

func activeRow(t *Task, machines map[string]string, loc *time.Location) wire.Component {
	meta := taskMeta(t)
	badge := &wire.Badge{Text: "排队中", Tone: wire.ToneCaution}
	if t.NotBefore != nil && t.State == StateQueued {
		badge = &wire.Badge{Text: "等待重试", Tone: wire.ToneCaution}
		meta = append(meta, fmt.Sprintf("第 %d 次失败（%s），%s 后重试", max(t.AttemptNo, 1), failureWords(lastErrorClass(t), lastVerdict(t)), t.NotBefore.In(loc).Format("15:04")))
	}
	subtitle := ""
	if e := t.Exec; e != nil && isActiveState(t.State) {
		name := machines[e.MachineID]
		if name == "" {
			name = e.MachineID
		}
		meta = append(meta, name, attemptCount(t.AttemptNo))
		switch {
		case t.State == StateNeedsInput:
			badge = &wire.Badge{Text: "等你处理", Tone: wire.ToneDanger}
			// What it waits for is the line the user reads; the rest is on the task's page.
			subtitle = cmpOr(strings.TrimSpace(e.Waiting), waitingInSession)
		case e.Phase == PhaseAccepting:
			badge = &wire.Badge{Text: "验收中", Tone: wire.TonePositive}
		case e.Phase == PhaseWorking:
			badge = &wire.Badge{Text: "Agent 在做", Tone: wire.TonePositive}
		default:
			badge = &wire.Badge{Text: "准备中", Tone: wire.TonePositive}
		}
	}
	if subtitle == "" {
		subtitle = strings.Join(meta, " · ")
	}
	return wire.Component{
		Type: wire.ComponentRow, Key: "active-" + t.ID, Title: t.Title, Subtitle: subtitle, Badge: badge,
		Action:  openTask(t),
		Actions: []wire.RowAction{{Title: "取消任务", Role: wire.ButtonRoleDestructive, Swipe: true, Action: cancelAction(t)}},
	}
}

// lastVerdict is the verdict of the task's newest report (the task's own verdict is set only when it ends).
func lastVerdict(t *Task) string {
	if r := lastReport(t); r != nil {
		return r.Verdict
	}
	return t.Verdict
}

func terminalRow(t *Task, loc *time.Location) wire.Component {
	meta := taskMeta(t)
	var badge *wire.Badge
	var actions []wire.RowAction
	switch t.State {
	case StateSucceeded:
		if t.Verdict == VerdictPass {
			badge = &wire.Badge{Text: "通过验收", Tone: wire.TonePositive}
		} else {
			badge = &wire.Badge{Text: "跑完未验收", Tone: wire.ToneCaution}
		}
	case StateFailed:
		badge = &wire.Badge{Text: "失败", Tone: wire.ToneDanger}
		meta = append(meta, fmt.Sprintf("%s，试了 %d 次", failureWords(lastErrorClass(t), t.Verdict), max(t.AttemptNo, 1)))
		actions = []wire.RowAction{{Title: "重试", Swipe: true, Action: retryAction(t)}}
	default:
		badge = &wire.Badge{Text: outcomeWords(t.State, t.Verdict, ""), Tone: wire.ToneCaution}
		actions = []wire.RowAction{{Title: "重试", Swipe: true, Action: retryAction(t)}}
	}
	if t.FinishedAt != nil {
		meta = append(meta, t.FinishedAt.In(loc).Format("01-02 15:04"))
	}
	return wire.Component{Type: wire.ComponentRow, Key: "term-" + t.ID, Title: t.Title, Subtitle: strings.Join(meta, " · "), Badge: badge,
		Action: openTask(t), Actions: actions}
}
