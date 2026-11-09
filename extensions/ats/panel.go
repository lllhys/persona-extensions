package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

const queueUI = "queue"

// ExtConfig matches the JSON Schema properties in persona-extension.json.
type ExtConfig struct {
	MaxQueued          int    `json:"max_queued"`
	LeaseTTLSeconds    int    `json:"lease_ttl_seconds"`
	MaxRetries         int    `json:"max_retries"`
	QuotaURL           string `json:"quota_url"`
	ReserveHeadroomPct int    `json:"reserve_headroom_pct"`
}

type atsApp struct {
	mu         sync.Mutex
	cfg        ExtConfig
	dataDir    string
	queue      *Queue
	writer     *DecisionWriter
	hub        *ext.Hub
	httpClient *http.Client
	nowFunc    func() time.Time
}

func newATSApp(q *Queue) *atsApp {
	app := &atsApp{
		cfg: ExtConfig{
			MaxQueued:          100,
			LeaseTTLSeconds:    75,
			MaxRetries:         2,
			ReserveHeadroomPct: 30,
		},
		queue:      q,
		writer:     NewDecisionWriter("", "", nil, nil),
		httpClient: &http.Client{Timeout: 8 * time.Second},
		nowFunc:    func() time.Time { return time.Now().UTC() },
	}
	return app
}

func (a *atsApp) now() time.Time {
	if a.nowFunc != nil {
		return a.nowFunc()
	}
	return time.Now().UTC()
}

func (a *atsApp) program(id, version string) ext.Program {
	return ext.Program{
		ID:       id,
		Version:  version,
		Manifest: manifest,
		Init:     a.init,
		UI: map[string]ext.Page{
			queueUI: {
				Render: a.render,
				Action: a.action,
			},
		},
		Namespaces: map[string]ext.Handler{
			"ats": a.handleRPC,
		},
	}
}

func (a *atsApp) init(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	c := ExtConfig{
		MaxQueued:          100,
		LeaseTTLSeconds:    75,
		MaxRetries:         2,
		ReserveHeadroomPct: 30,
	}
	if err := in.Config(&c); err != nil {
		return nil, err
	}

	qCfg := DefaultQueueConfig()
	if c.MaxQueued > 0 {
		qCfg.MaxQueued = c.MaxQueued
	}
	if c.LeaseTTLSeconds > 0 {
		qCfg.LeaseTTL = time.Duration(c.LeaseTTLSeconds) * time.Second
	}
	if c.MaxRetries >= 0 {
		qCfg.MaxRetries = c.MaxRetries
	}
	if c.ReserveHeadroomPct > 0 {
		qCfg.ReserveHeadroomPct = float64(c.ReserveHeadroomPct)
	}

	info := in.Info()
	dataDir := strings.TrimSpace(info.DataDir)
	if dataDir == "" {
		dataDir = defaultDataDir()
	} else {
		recordActiveDataDir(dataDir)
	}

	a.mu.Lock()
	a.cfg = c
	a.dataDir = dataDir
	a.hub = hub
	if a.queue == nil {
		q, err := NewQueue(dataDir, qCfg)
		if err != nil {
			a.mu.Unlock()
			return nil, err
		}
		a.queue = q
	} else {
		a.queue.UpdateConfig(qCfg)
	}
	if a.writer == nil {
		a.writer = NewDecisionWriter(in.Hub.URL, in.Hub.Token, in.Hub.Scopes, a.httpClient)
	} else {
		a.writer.Configure(in.Hub.URL, in.Hub.Token, in.Hub.Scopes)
	}
	a.mu.Unlock()

	a.refreshRuntimeState(ctx)
	return nil, nil
}

func (a *atsApp) ensureQueue() (*Queue, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.queue != nil {
		return a.queue, nil
	}
	q, err := NewQueue(a.dataDir, DefaultQueueConfig())
	if err != nil {
		return nil, err
	}
	a.queue = q
	return q, nil
}

func (a *atsApp) refreshRuntimeState(ctx context.Context) {
	q, err := a.ensureQueue()
	if err != nil {
		return
	}
	_ = q.Reload()
	now := a.now()
	_, _ = q.ReapExpiredLeases(now)

	a.mu.Lock()
	quotaSource := strings.TrimSpace(a.cfg.QuotaURL)
	if quotaSource == "" {
		quotaSource = strings.TrimSpace(os.Getenv("ATS_QUOTA_URL"))
	}
	hub := a.hub
	client := a.httpClient
	writer := a.writer
	a.mu.Unlock()

	if quotaSource != "" {
		if st, err := FetchQuotaFromSource(ctx, client, quotaSource, now); err == nil {
			_ = q.SetQuotaStatus(st)
		}
	} else if hub != nil {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		var ga GenericGatewayAccounts
		if err := hub.Call(cctx, "usage.gateway_accounts", map[string]any{}, &ga); err == nil && (len(ga.Accounts) > 0 || len(ga.Sources) > 0) {
			_ = q.SetQuotaStatus(ParseGatewayAccounts(ga, now))
		}
		cancel()
	}

	if writer != nil && writer.HasTodosWriteScope() {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, _ = writer.SyncQueueOutcomes(cctx, q)
		cancel()
	}
}

func (a *atsApp) handleRPC(ctx context.Context, method string, params json.RawMessage) (any, error) {
	q, err := a.ensureQueue()
	if err != nil {
		return nil, err
	}
	switch method {
	case "overview":
		a.refreshRuntimeState(ctx)
		return q.PlanSchedule(a.now()), nil
	case "outcomes":
		a.refreshRuntimeState(ctx)
		tasks, err := q.ListTasks(TaskFilter{Limit: 100})
		if err != nil {
			return nil, err
		}
		var outcomes []map[string]any
		for _, t := range tasks {
			if payload, ok := BuildOutcomeDecision(t); ok {
				outcomes = append(outcomes, map[string]any{
					"todo_id":        t.TodoID(),
					"task_id":        t.ID,
					"title":          t.Title,
					"state":          t.State,
					"verdict":        t.Verdict,
					"failure_reason": t.FailureReason,
					"ref":            payload.Ref,
					"choice":         payload.Choice,
					"reason":         payload.Reason,
				})
			}
		}
		return map[string]any{"outcomes": outcomes}, nil
	case "lease":
		var p struct {
			TaskID   string `json:"task_id"`
			WorkerID string `json:"worker_id"`
			Machine  string `json:"machine"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		now := a.now()
		if p.TaskID != "" {
			t, att, err := q.LeaseTask(p.TaskID, p.WorkerID, p.Machine, now)
			if err != nil {
				return nil, err
			}
			return map[string]any{"task": t, "attempt": att}, nil
		}
		tasks, attempts, plan, err := q.DispatchNext(1, now)
		if err != nil {
			return nil, err
		}
		return map[string]any{"tasks": tasks, "attempts": attempts, "plan": plan}, nil
	case "heartbeat":
		var p struct {
			TaskID     string `json:"task_id"`
			WorkerID   string `json:"worker_id"`
			FenceToken int    `json:"fence_token"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		att, err := q.Heartbeat(p.TaskID, p.WorkerID, p.FenceToken, a.now())
		if err != nil {
			return nil, err
		}
		return map[string]any{"attempt": att}, nil
	case "complete":
		var p CompleteAttemptInput
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		p.Now = a.now()
		t, err := q.CompleteAttempt(p)
		if err != nil {
			return nil, err
		}
		a.mu.Lock()
		w := a.writer
		a.mu.Unlock()
		if w != nil && w.HasTodosWriteScope() && t.IsTerminal() {
			_, _ = w.WriteTaskOutcome(ctx, q, t.ID)
		}
		return map[string]any{"task": t}, nil
	default:
		return nil, ext.UnknownMethod("ats." + method)
	}
}

func (a *atsApp) render(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	if r.Contribution != "" && r.Contribution != queueUI {
		return nil, ext.NoContribution(r.Contribution)
	}
	a.refreshRuntimeState(ctx)
	q, err := a.ensureQueue()
	if err != nil {
		return nil, err
	}
	return buildQueueView(q, a.now(), r.TZ), nil
}

func (a *atsApp) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	if r.Contribution != "" && r.Contribution != queueUI {
		return nil, ext.NoContribution(r.Contribution)
	}
	q, err := a.ensureQueue()
	if err != nil {
		return nil, err
	}
	_ = q.Reload()
	now := a.now()

	act := strings.TrimSpace(r.Action)
	kind, targetID, _ := strings.Cut(act, ":")

	var toast string
	switch kind {
	case "retry":
		t, err := q.RetryTask(targetID, now)
		if err != nil {
			toast = "重试失败：" + err.Error()
		} else {
			toast = fmt.Sprintf("已重新入队：%s (%s)", t.Title, t.ID)
		}
	case "cancel":
		t, err := q.CancelTask(targetID, now)
		if err != nil {
			toast = "取消失败：" + err.Error()
		} else {
			a.mu.Lock()
			w := a.writer
			a.mu.Unlock()
			if w != nil && w.HasTodosWriteScope() {
				_, _ = w.WriteTaskOutcome(ctx, q, t.ID)
			}
			toast = fmt.Sprintf("已取消任务：%s (%s)", t.Title, t.ID)
		}
	case "dispatch":
		leased, _, plan, err := q.DispatchNext(1, now)
		switch {
		case err != nil:
			toast = "调度失败：" + err.Error()
		case len(leased) > 0:
			toast = fmt.Sprintf("已出队执行：%s → %s", leased[0].ID, leased[0].ModelTier)
		default:
			toast = fmt.Sprintf("当前无可出队任务（模式：%s）", plan.AdmissionLabel)
		}
	case "sync":
		a.refreshRuntimeState(ctx)
		toast = "已同步额度与待办验收结论"
	default:
		toast = "已刷新队列视图"
	}

	view := buildQueueView(q, now, r.TZ)
	return &wire.ActionResult{
		View:  view,
		Toast: toast,
	}, nil
}

func buildQueueView(q *Queue, now time.Time, tz string) *wire.View {
	loc := time.UTC
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}

	cfg := q.Config()
	plan := q.PlanSchedule(now)
	quota := plan.Quota

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

	quotaDetail := fmt.Sprintf("来源 %s · 保底线 %.0f%%", quota.Provider, plan.ReserveHeadroomPct)
	if quota.ResetAt != nil {
		quotaDetail = fmt.Sprintf("重置 %s · 保底线 %.0f%%", quota.ResetAt.In(loc).Format("15:04"), plan.ReserveHeadroomPct)
	}

	prioSummary := fmt.Sprintf("P0:%d P1:%d P2:%d P3:%d",
		plan.QueuedByPriority[PriorityP0],
		plan.QueuedByPriority[PriorityP1],
		plan.QueuedByPriority[PriorityP2],
		plan.QueuedByPriority[PriorityP3],
	)
	diffSummary := fmt.Sprintf("易:%d 中:%d 难:%d 极难:%d",
		plan.QueuedByDifficulty[DifficultyEasy],
		plan.QueuedByDifficulty[DifficultyMedium],
		plan.QueuedByDifficulty[DifficultyHard],
		plan.QueuedByDifficulty[DifficultyExtreme],
	)

	statsSection := wire.Section{
		Title: "离线计算队列与通用额度总览",
		Items: []wire.Component{
			{
				Type: wire.ComponentStatGrid,
				Stats: []wire.Stat{
					{
						Label:  "离线调度档位",
						Value:  plan.AdmissionLabel,
						Detail: fmt.Sprintf("准入 %d · 延后 %d", len(plan.AdmittedCandidates), len(plan.DeferredCandidates)),
						Tone:   modeTone,
					},
					{
						Label:  "通用额度水位",
						Value:  fmt.Sprintf("%.0f%%", quota.RemainingPct),
						Detail: quotaDetail,
						Tone:   quotaTone,
					},
					{
						Label:  "排队 / 准入上限",
						Value:  fmt.Sprintf("%d / %d", plan.CountsByState[StateQueued], cfg.MaxQueued),
						Detail: prioSummary,
					},
					{
						Label:  "运行 / 完成 / 失败",
						Value:  fmt.Sprintf("%d / %d / %d", plan.CountsByState[StateRunning], plan.CountsByState[StateSucceeded], plan.CountsByState[StateFailed]),
						Detail: diffSummary,
					},
				},
			},
			{
				Type:  wire.ComponentTable,
				Title: "模型档位额度水位（按任务难度映射）",
				Columns: []wire.TableColumn{
					{Title: "模型档位"},
					{Title: "对应任务难度"},
					{Title: "剩余额度", Align: "end"},
					{Title: "离线准入状态"},
				},
				Rows: [][]string{
					{
						"fast (轻量)",
						"easy (权重 1.0x)",
						fmt.Sprintf("%.0f%%", tierRemaining(quota, TierFast)),
						tierStatusText(tierRemaining(quota, TierFast), plan.ReserveHeadroomPct, quota.Exhausted),
					},
					{
						"standard (标准)",
						"medium (权重 2.5x)",
						fmt.Sprintf("%.0f%%", tierRemaining(quota, TierStandard)),
						tierStatusText(tierRemaining(quota, TierStandard), plan.ReserveHeadroomPct, quota.Exhausted),
					},
					{
						"strong (强模型)",
						"hard / extreme (权重 5.0x–8.0x)",
						fmt.Sprintf("%.0f%%", tierRemaining(quota, TierStrong)),
						tierStatusText(tierRemaining(quota, TierStrong), plan.ReserveHeadroomPct, quota.Exhausted),
					},
				},
			},
		},
	}

	machineRows := make([][]string, 0, len(plan.Machines))
	for _, m := range plan.Machines {
		stateStr := "在线"
		if !m.Online {
			stateStr = "离线"
		}
		machineRows = append(machineRows, []string{
			m.Name,
			stateStr,
			fmt.Sprintf("%d / %d", m.RunningSlots, m.MaxSlots),
			fmt.Sprintf("%.0f%%", m.LoadRatio()*100),
		})
	}
	machineSection := wire.Section{
		Title: "执行机器槽位与负载均衡",
		Items: []wire.Component{
			{
				Type: wire.ComponentTable,
				Columns: []wire.TableColumn{
					{Title: "机器节点"},
					{Title: "状态"},
					{Title: "运行槽位", Align: "end"},
					{Title: "负载率", Align: "end"},
				},
				Rows: machineRows,
			},
		},
	}

	allTasks, _ := q.ListTasks(TaskFilter{Limit: 50})
	var activeItems []wire.Component
	var terminalItems []wire.Component

	for _, t := range allTasks {
		if !t.IsTerminal() {
			activeItems = append(activeItems, buildActiveTaskRow(t, loc))
		} else {
			terminalItems = append(terminalItems, buildTerminalTaskRow(t, loc))
		}
	}

	activeSection := wire.Section{Title: "运行中与排队任务"}
	if len(activeItems) == 0 {
		activeSection.Items = []wire.Component{
			{
				Type:  wire.ComponentEmpty,
				Title: "当前没有排队或运行中的任务",
				Text:  "可通过助手的 enqueue_task 工具（每次需确认）为待办派发带优先级与难度标签的 ATS 任务。",
			},
		}
	} else {
		activeSection.Items = activeItems
	}

	terminalSection := wire.Section{Title: "近期验收证据与失败任务"}
	if len(terminalItems) == 0 {
		terminalSection.Items = []wire.Component{
			{
				Type:  wire.ComponentText,
				Style: wire.TextStyleSecondary,
				Text:  "暂无已完成或失败的任务记录。",
			},
		}
	} else {
		terminalSection.Items = terminalItems
	}

	return &wire.View{
		Title:        "ATS 队列",
		RefreshAfter: 30,
		Sections: []wire.Section{
			statsSection,
			machineSection,
			activeSection,
			terminalSection,
		},
	}
}

func tierStatusText(remPct, reservePct float64, exhausted bool) string {
	if exhausted || remPct <= 5 {
		return "冷却暂停"
	}
	if remPct < reservePct {
		return "仅 P0-P1 保底"
	}
	return "全量放行"
}

func buildActiveTaskRow(t *Task, loc *time.Location) wire.Component {
	var meta []string
	meta = append(meta, strings.ToUpper(t.Priority))
	meta = append(meta, fmt.Sprintf("%s→%s", t.Difficulty, t.ModelTier))
	if t.Machine != "" {
		meta = append(meta, "机器:"+t.Machine)
	}
	if todoID := t.TodoID(); todoID != "" {
		meta = append(meta, "待办:"+todoID)
	}
	meta = append(meta, "权限:"+t.PermissionProfile)
	if t.AttemptNo > 0 {
		meta = append(meta, fmt.Sprintf("尝试#%d (fence=%d)", t.AttemptNo, t.FenceToken))
	}
	if t.NotBefore != nil {
		meta = append(meta, "冷却至 "+t.NotBefore.In(loc).Format("15:04:05"))
	}

	badge := &wire.Badge{Text: "排队中", Tone: wire.ToneCaution}
	if t.State == StateRunning || t.State == StateLeased {
		badge = &wire.Badge{Text: "运行中", Tone: wire.TonePositive}
	} else if t.NotBefore != nil {
		badge = &wire.Badge{Text: "退避冷却", Tone: wire.ToneCaution}
	}

	return wire.Component{
		Type:     wire.ComponentRow,
		Key:      "active-" + t.ID,
		Title:    fmt.Sprintf("%s (%s)", t.Title, t.ID),
		Subtitle: strings.Join(meta, " · "),
		Badge:    badge,
		Action: &wire.Action{
			ID:      "cancel:" + t.ID,
			Confirm: fmt.Sprintf("确认取消任务 %s (%s) 吗？", t.Title, t.ID),
		},
	}
}

func buildTerminalTaskRow(t *Task, loc *time.Location) wire.Component {
	var meta []string
	meta = append(meta, strings.ToUpper(t.Priority))
	meta = append(meta, fmt.Sprintf("%s→%s", t.Difficulty, t.ModelTier))
	if todoID := t.TodoID(); todoID != "" {
		meta = append(meta, "待办:"+todoID)
	}
	if t.Acceptance != nil && t.Acceptance.Command != "" {
		meta = append(meta, "验收:"+t.Acceptance.Command)
	}
	if len(t.Reports) > 0 {
		last := t.Reports[len(t.Reports)-1]
		if last.ExitCode != nil {
			meta = append(meta, fmt.Sprintf("退出码:%d", *last.ExitCode))
		}
		if last.Message != "" {
			meta = append(meta, oneLinePreview(last.Message, 80))
		}
	} else if t.FailureReason != "" {
		meta = append(meta, "原因:"+t.FailureReason)
	}
	if t.DecisionWrittenRef != "" {
		meta = append(meta, "已写回:"+t.DecisionWrittenRef)
	}
	if t.FinishedAt != nil {
		meta = append(meta, t.FinishedAt.In(loc).Format("01-02 15:04"))
	}

	var badge *wire.Badge
	var action *wire.Action
	switch t.State {
	case StateSucceeded:
		if t.Verdict == VerdictPass || t.Verdict == "passed" {
			badge = &wire.Badge{Text: "通过验收", Tone: wire.TonePositive}
		} else {
			badge = &wire.Badge{Text: "跑完未验收", Tone: wire.ToneCaution}
		}
	case StateFailed:
		badge = &wire.Badge{Text: "失败（点此重试）", Tone: wire.ToneDanger}
		action = &wire.Action{
			ID: "retry:" + t.ID,
		}
	default:
		badge = &wire.Badge{Text: "已取消（点此重试）", Tone: wire.ToneCaution}
		action = &wire.Action{
			ID: "retry:" + t.ID,
		}
	}

	return wire.Component{
		Type:     wire.ComponentRow,
		Key:      "term-" + t.ID,
		Title:    fmt.Sprintf("%s (%s)", t.Title, t.ID),
		Subtitle: strings.Join(meta, " · "),
		Badge:    badge,
		Action:   action,
	}
}

func oneLinePreview(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return s
}

func baseStateDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "persona-ats")
	}
	return filepath.Join(os.TempDir(), "persona-ats")
}

func recordActiveDataDir(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	base := baseStateDir()
	_ = os.MkdirAll(base, 0o755)
	_ = os.WriteFile(filepath.Join(base, "active-datadir"), []byte(dir+"\n"), 0o600)
}

func defaultDataDir() string {
	if d := strings.TrimSpace(os.Getenv("PERSONA_ATS_DATA_DIR")); d != "" {
		return d
	}
	if d := strings.TrimSpace(os.Getenv("ATS_DATA_DIR")); d != "" {
		return d
	}
	base := baseStateDir()
	if raw, err := os.ReadFile(filepath.Join(base, "active-datadir")); err == nil {
		if target := strings.TrimSpace(string(raw)); target != "" {
			if st, statErr := os.Stat(target); statErr == nil && st.IsDir() {
				return target
			}
		}
	}
	return base
}
