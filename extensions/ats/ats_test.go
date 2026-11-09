package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestConformance(t *testing.T) {
	q, err := NewQueue(t.TempDir(), DefaultQueueConfig())
	if err != nil {
		t.Fatal(err)
	}
	app := newATSApp(q)
	exttest.Check(t, manifest, exttest.Serve(t, app.program("ats", "0.3.0")))
}

func TestManifestSecurityGate(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatalf("packaged manifest invalid: %v", err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(authorManifest, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if len(m.Contributes.McpServers) != 1 {
		t.Fatalf("expected 1 mcp_server, got %d", len(m.Contributes.McpServers))
	}
	srv := m.Contributes.McpServers[0]
	wantAllow := []string{"list_tasks", "get_task", "get_evidence"}
	if !slices.Equal(srv.Allow, wantAllow) {
		t.Fatalf("mcp_servers[0].allow = %v, want %v", srv.Allow, wantAllow)
	}
	if slices.Contains(srv.Allow, "enqueue_task") {
		t.Fatal("enqueue_task must NEVER be in mcp_servers[0].allow")
	}
	if !slices.Contains(m.Permissions.Hub, "todos.write") {
		t.Fatalf("permissions.hub = %v, must include todos.write", m.Permissions.Hub)
	}
	if !m.Permissions.UsageGateways {
		t.Fatal("permissions.usage_gateways must be true")
	}
}

func TestQueueStateMachineAndR1ReadonlyDefault(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	q, err := NewQueue(dir, DefaultQueueConfig())
	if err != nil {
		t.Fatal(err)
	}

	// R1: omitting permission_profile forces "readonly".
	res, err := q.Enqueue(EnqueueInput{
		Title:  "Inspect build logs",
		Prompt: "Check recent CI logs",
		Repo:   "/tmp/repo",
		Labels: map[string]string{PersonaTodoLabel: "todo_1"},
		Now:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Task.PermissionProfile != PermissionReadonly {
		t.Fatalf("expected readonly default, got %+v", res.Task)
	}

	// Explicit "default" is permitted when requested.
	resWrite, err := q.Enqueue(EnqueueInput{
		Title:             "Apply patch",
		Prompt:            "Fix the bug",
		Repo:              "/tmp/repo",
		PermissionProfile: PermissionDefault,
		Now:               now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resWrite.Task.PermissionProfile != PermissionDefault {
		t.Fatalf("expected default permission profile, got %s", resWrite.Task.PermissionProfile)
	}

	// Lease and complete the first task as succeeded with pass verdict.
	leased, att, err := q.LeaseTask(res.Task.ID, "worker-a", "node-1", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if leased.State != StateRunning || att.FenceToken != 1 || att.WorkerID != "worker-a" {
		t.Fatalf("unexpected leased state: task=%+v att=%+v", leased, att)
	}

	exitZero := 0
	doneTask, err := q.CompleteAttempt(CompleteAttemptInput{
		TaskID:     res.Task.ID,
		FenceToken: att.FenceToken,
		WorkerID:   "worker-a",
		Outcome:    StateSucceeded,
		Verdict:    VerdictPass,
		ExitCode:   &exitZero,
		Message:    "all unit tests passed",
		Artifacts:  []Artifact{{Kind: "report", Path: "out/report.json", Bytes: 512}},
		Now:        now.Add(10 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if doneTask.State != StateSucceeded || doneTask.Verdict != VerdictPass || len(doneTask.Reports) != 1 {
		t.Fatalf("unexpected completed task: %+v", doneTask)
	}

	// Reload from disk and verify persistence.
	q2, err := NewQueue(dir, DefaultQueueConfig())
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := q2.GetTask(res.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State != StateSucceeded || reloaded.Verdict != VerdictPass || len(reloaded.Reports) != 1 {
		t.Fatalf("reloaded task mismatch: %+v", reloaded)
	}
}

func TestR2WorkerLeaseAndFenceProtection(t *testing.T) {
	now := time.Date(2026, 11, 9, 11, 0, 0, 0, time.UTC)
	cfg := DefaultQueueConfig()
	cfg.LeaseTTL = 30 * time.Second
	cfg.RetryBaseDelay = 10 * time.Second
	q, err := NewQueue(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	enq, err := q.Enqueue(EnqueueInput{
		Prompt: "Run fenced job",
		Repo:   "/tmp/repo",
		Now:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := enq.Task.ID

	// Attempt 1 leased by worker-a (fence = 1).
	_, att1, err := q.LeaseTask(taskID, "worker-a", "node-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if att1.FenceToken != 1 {
		t.Fatalf("expected fenceToken=1, got %d", att1.FenceToken)
	}

	// Worker-b impersonating worker-a on fence=1 must be rejected.
	if _, err := q.Heartbeat(taskID, "worker-b", att1.FenceToken, now.Add(5*time.Second)); !errors.Is(err, ErrStaleAttempt) {
		t.Fatalf("expected ErrStaleAttempt for wrong worker heartbeat, got %v", err)
	}
	if _, err := q.CompleteAttempt(CompleteAttemptInput{
		TaskID:     taskID,
		FenceToken: att1.FenceToken,
		WorkerID:   "worker-b",
		Outcome:    StateSucceeded,
		Verdict:    VerdictPass,
		Now:        now.Add(5 * time.Second),
	}); !errors.Is(err, ErrStaleAttempt) {
		t.Fatalf("expected ErrStaleAttempt for wrong worker completion, got %v", err)
	}

	// Expire lease 1 and reap it -> task returns to queued with R6 backoff.
	reaped, err := q.ReapExpiredLeases(now.Add(35 * time.Second))
	if err != nil || len(reaped) != 1 {
		t.Fatalf("expected 1 reaped task, got %d (%v)", len(reaped), err)
	}

	// Wait past not_before and lease Attempt 2 to worker-b (fence = 2).
	lease2Time := now.Add(2 * time.Minute)
	_, att2, err := q.LeaseTask(taskID, "worker-b", "node-2", lease2Time)
	if err != nil {
		t.Fatal(err)
	}
	if att2.FenceToken != 2 {
		t.Fatalf("expected fenceToken=2, got %d", att2.FenceToken)
	}

	// Stale worker-a with old fenceToken=1 tries to overwrite state -> rejected with ErrStaleAttempt.
	if _, err := q.CompleteAttempt(CompleteAttemptInput{
		TaskID:     taskID,
		FenceToken: att1.FenceToken,
		WorkerID:   "worker-a",
		Outcome:    StateFailed,
		ErrorClass: "task_failed",
		Now:        lease2Time.Add(time.Second),
	}); !errors.Is(err, ErrStaleAttempt) {
		t.Fatalf("expected ErrStaleAttempt for stale fenceToken=1, got %v", err)
	}

	// Mismatched AttemptID or expired lease on CompleteAttempt must also be rejected with ErrStaleAttempt.
	if _, err := q.CompleteAttempt(CompleteAttemptInput{
		TaskID:     taskID,
		AttemptID:  "wrong-attempt-id",
		FenceToken: att2.FenceToken,
		WorkerID:   "worker-b",
		Outcome:    StateSucceeded,
		Verdict:    VerdictPass,
		Now:        lease2Time.Add(2 * time.Second),
	}); !errors.Is(err, ErrStaleAttempt) {
		t.Fatalf("expected ErrStaleAttempt for mismatched AttemptID, got %v", err)
	}
	if _, err := q.CompleteAttempt(CompleteAttemptInput{
		TaskID:     taskID,
		FenceToken: att2.FenceToken,
		WorkerID:   "worker-b",
		Outcome:    StateSucceeded,
		Verdict:    VerdictPass,
		Now:        lease2Time.Add(60 * time.Second),
	}); !errors.Is(err, ErrStaleAttempt) {
		t.Fatalf("expected ErrStaleAttempt after lease expiry, got %v", err)
	}

	// Current worker-b with fenceToken=2 succeeds.
	finalTask, err := q.CompleteAttempt(CompleteAttemptInput{
		TaskID:     taskID,
		FenceToken: att2.FenceToken,
		WorkerID:   "worker-b",
		Outcome:    StateSucceeded,
		Verdict:    VerdictPass,
		Message:    "completed by worker-b",
		Now:        lease2Time.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if finalTask.State != StateSucceeded || finalTask.FenceToken != 2 {
		t.Fatalf("expected succeeded at fence=2, got %+v", finalTask)
	}
}

func TestR6DeterministicBackoffJitterAndAntiAffinity(t *testing.T) {
	base := 5 * time.Second
	maxD := 10 * time.Minute

	d1a := ComputeRetryDelay("task_42", 1, base, maxD)
	d1b := ComputeRetryDelay("task_42", 1, base, maxD)
	if d1a != d1b {
		t.Fatalf("ComputeRetryDelay is not deterministic: %v != %v", d1a, d1b)
	}
	d2 := ComputeRetryDelay("task_42", 2, base, maxD)
	d3 := ComputeRetryDelay("task_42", 3, base, maxD)
	if d2 <= d1a || d3 <= d2 {
		t.Fatalf("expected exponential growth across attempts: d1=%v d2=%v d3=%v", d1a, d2, d3)
	}

	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	cfg := DefaultQueueConfig()
	cfg.RetryBaseDelay = 10 * time.Second
	cfg.MaxRetries = 2
	q, err := NewQueue(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = q.SetMachines([]MachineSlot{
		{Name: "node-a", Online: true, MaxSlots: 2, RunningSlots: 0},
		{Name: "node-b", Online: true, MaxSlots: 2, RunningSlots: 0},
	})

	enq, err := q.Enqueue(EnqueueInput{
		Prompt: "Flaky network task",
		Repo:   "/tmp/repo",
		Now:    now,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, att1, err := q.LeaseTask(enq.Task.ID, "node-a:worker-standard", "node-a", now)
	if err != nil {
		t.Fatal(err)
	}

	// Fail attempt 1 with retryable infra_error.
	afterFail, err := q.CompleteAttempt(CompleteAttemptInput{
		TaskID:     enq.Task.ID,
		FenceToken: att1.FenceToken,
		WorkerID:   "node-a:worker-standard",
		Outcome:    StateFailed,
		ErrorClass: "infra_error",
		Message:    "connection reset",
		Now:        now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if afterFail.State != StateQueued || afterFail.RetriesLeft != 1 || afterFail.NotBefore == nil {
		t.Fatalf("expected task to re-queue with not_before, got %+v", afterFail)
	}

	// Leasing before NotBefore must fail.
	if _, _, err := q.LeaseTask(enq.Task.ID, "node-b:worker-standard", "node-b", now.Add(2*time.Second)); err == nil {
		t.Fatal("expected LeaseTask before NotBefore to fail")
	}

	// After NotBefore, PlanSchedule must admit the task and prefer node-b due to R6 worker anti-affinity against node-a.
	readyTime := afterFail.NotBefore.Add(time.Second)
	plan := q.PlanSchedule(readyTime)
	if len(plan.AdmittedCandidates) != 1 {
		t.Fatalf("expected 1 admitted candidate after NotBefore, got %d", len(plan.AdmittedCandidates))
	}
	if plan.AdmittedCandidates[0].TargetMachine != "node-b" {
		t.Fatalf("expected worker anti-affinity to pick node-b over node-a, got %s", plan.AdmittedCandidates[0].TargetMachine)
	}
}

func TestR8QueueAdmissionCap(t *testing.T) {
	cfg := DefaultQueueConfig()
	cfg.MaxQueued = 2
	q, err := NewQueue(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := q.Enqueue(EnqueueInput{Prompt: "task 1", IdempotencyKey: "k1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(EnqueueInput{Prompt: "task 2", IdempotencyKey: "k2"}); err != nil {
		t.Fatal(err)
	}

	// Idempotent replay of k1 still succeeds at cap.
	replay, err := q.Enqueue(EnqueueInput{Prompt: "task 1", IdempotencyKey: "k1"})
	if err != nil || replay.Created {
		t.Fatalf("expected idempotent replay to succeed with Created=false, got %+v, err=%v", replay, err)
	}

	// Third distinct queued task must be rejected with 429 too_many_queued.
	_, err = q.Enqueue(EnqueueInput{Prompt: "task 3"})
	var admErr *AdmissionError
	if !errors.As(err, &admErr) || admErr.Code != "too_many_queued" || admErr.HTTPStatus != 429 {
		t.Fatalf("expected AdmissionError(too_many_queued, 429), got %v", err)
	}
}

func TestTagDrivenPriorityDifficultyAndOfflineScheduler(t *testing.T) {
	now := time.Date(2026, 11, 9, 14, 0, 0, 0, time.UTC)
	cfg := DefaultQueueConfig()
	cfg.ReserveHeadroomPct = 30
	q, err := NewQueue(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	_ = q.SetMachines([]MachineSlot{
		{Name: "gpu-box", Online: true, MaxSlots: 2, RunningSlots: 1}, // 50% load
		{Name: "cpu-box", Online: true, MaxSlots: 4, RunningSlots: 0}, // 0% load
		{Name: "offline-box", Online: false, MaxSlots: 4, RunningSlots: 0},
	})

	// Enqueue tasks with tag-driven priority, difficulty, and machine affinity.
	tP3Hard, err := q.Enqueue(EnqueueInput{
		Title:  "Batch refactor",
		Prompt: "Large repo cleanup",
		Tags:   []string{"priority=p3", "difficulty=extreme"},
		Now:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tP3Hard.Task.Priority != PriorityP3 || tP3Hard.Task.Difficulty != DifficultyExtreme || tP3Hard.Task.ModelTier != TierStrong {
		t.Fatalf("unexpected tag extraction for P3 extreme task: %+v", tP3Hard.Task)
	}

	tP0Med, err := q.Enqueue(EnqueueInput{
		Title:  "Hotfix prod",
		Prompt: "Urgent fix",
		Tags:   []string{"p0", "difficulty=medium"},
		Now:    now,
	})
	if err != nil {
		t.Fatal(err)
	}

	tP1Pinned, err := q.Enqueue(EnqueueInput{
		Title:  "GPU verification",
		Prompt: "Verify on gpu-box",
		Labels: map[string]string{"priority": "high", "difficulty": "easy", "machine": "gpu-box"},
		Now:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tP1Pinned.Task.Priority != PriorityP1 || tP1Pinned.Task.ModelTier != TierFast || tP1Pinned.Task.Machine != "gpu-box" {
		t.Fatalf("unexpected label extraction: %+v", tP1Pinned.Task)
	}

	// 1. High quota headroom (85%) -> Full admission mode (full), P0 ordered first, least-loaded machine chosen.
	_ = q.SetQuotaStatus(QuotaStatus{
		Provider:     "quota_gateway",
		RemainingPct: 85,
		TierRemainingPct: map[string]float64{
			TierFast:     90,
			TierStandard: 85,
			TierStrong:   80,
		},
		CheckedAt: now,
	})
	planFull := q.PlanSchedule(now)
	if planFull.AdmissionMode != AdmissionFull || planFull.AdmissionLabel != "全量放行" {
		t.Fatalf("expected full admission mode, got %s (%s)", planFull.AdmissionMode, planFull.AdmissionLabel)
	}
	if len(planFull.AdmittedCandidates) != 3 {
		t.Fatalf("expected all 3 tasks admitted in full mode, got %d", len(planFull.AdmittedCandidates))
	}
	if planFull.AdmittedCandidates[0].TaskID != tP0Med.Task.ID || planFull.AdmittedCandidates[0].TargetMachine != "cpu-box" {
		t.Fatalf("expected P0 task first on least-loaded cpu-box, got %+v", planFull.AdmittedCandidates[0])
	}
	if planFull.AdmittedCandidates[1].TaskID != tP1Pinned.Task.ID || planFull.AdmittedCandidates[1].TargetMachine != "gpu-box" {
		t.Fatalf("expected P1 pinned task second on gpu-box, got %+v", planFull.AdmittedCandidates[1])
	}

	// 2. Low quota headroom (20% < 30% reserve) -> Priority-only mode (priority_only), P3 extreme task is deferred!
	_ = q.SetQuotaStatus(QuotaStatus{
		Provider:     "quota_gateway",
		RemainingPct: 20,
		TierRemainingPct: map[string]float64{
			TierFast:     25,
			TierStandard: 20,
			TierStrong:   20,
		},
		CheckedAt: now,
	})
	planReserve := q.PlanSchedule(now)
	if planReserve.AdmissionMode != AdmissionPriorityOnly || planReserve.AdmissionLabel != "仅 P0-P1 保底" {
		t.Fatalf("expected priority_only mode, got %s (%s)", planReserve.AdmissionMode, planReserve.AdmissionLabel)
	}
	if len(planReserve.AdmittedCandidates) != 2 || len(planReserve.DeferredCandidates) != 1 {
		t.Fatalf("expected 2 admitted (P0, P1) and 1 deferred (P3), got admitted=%d deferred=%d",
			len(planReserve.AdmittedCandidates), len(planReserve.DeferredCandidates))
	}
	if planReserve.DeferredCandidates[0].TaskID != tP3Hard.Task.ID {
		t.Fatalf("expected P3 extreme task to be deferred, got %+v", planReserve.DeferredCandidates[0])
	}

	// 3. Anti-starvation Aging: a P2 task waiting 150 minutes gains +60 aging boost and promotes to P1 eligibility.
	tAgedP2, err := q.Enqueue(EnqueueInput{
		Title:      "Aged P2 task",
		Prompt:     "Waited 150m",
		Priority:   PriorityP2,
		Difficulty: DifficultyEasy,
		Now:        now.Add(-150 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	planAged := q.PlanSchedule(now)
	foundAged := false
	for _, c := range planAged.AdmittedCandidates {
		if c.TaskID == tAgedP2.Task.ID && c.AgingBoost >= 60 {
			foundAged = true
		}
	}
	if !foundAged {
		t.Fatalf("expected aged P2 task (150m wait) to be admitted with aging boost=60, got %+v", planAged.AdmittedCandidates)
	}

	// 4. Exhausted / Cooldown quota -> Paused mode (paused_quota), 0 tasks admitted.
	resetAt := now.Add(30 * time.Minute)
	_ = q.SetQuotaStatus(QuotaStatus{
		Provider:      "quota_gateway",
		RemainingPct:  0,
		Exhausted:     true,
		CooldownUntil: &resetAt,
		ResetAt:       &resetAt,
		CheckedAt:     now,
	})
	planPaused := q.PlanSchedule(now)
	if planPaused.AdmissionMode != AdmissionPausedQuota || len(planPaused.AdmittedCandidates) != 0 {
		t.Fatalf("expected paused_quota with 0 admitted tasks, got mode=%s admitted=%d", planPaused.AdmissionMode, len(planPaused.AdmittedCandidates))
	}
}

func TestMCPStdioProtocolAndEnqueueSecurity(t *testing.T) {
	q, err := NewQueue(t.TempDir(), DefaultQueueConfig())
	if err != nil {
		t.Fatal(err)
	}
	opts := MCPOptions{
		AllowEnqueue: true,
		Version:      "0.3.0",
		PinWorkspace: func(repo, ref string) (WorkspaceSpec, error) {
			if ref == "" {
				ref = "HEAD"
			}
			return WorkspaceSpec{Repo: repo, BaseSHA: "sha-" + ref}, nil
		},
	}

	// 1. Read-only mode (AllowEnqueue: false) hides enqueue_task and rejects calling it.
	roList := HandleMCPMessage(q, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`), MCPOptions{AllowEnqueue: false})
	roBytes, _ := json.Marshal(roList.Result)
	if strings.Contains(string(roBytes), "enqueue_task") {
		t.Fatalf("read-only MCP server must not list enqueue_task: %s", roBytes)
	}

	// 2. Caller attempting to override labels.persona_todo (including padded with whitespace) must be rejected.
	badLabelCall := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"enqueue_task","arguments":{"todo_id":"todo_real","prompt":"run check","repo":"/tmp/repo","labels":{"persona_todo":"todo_spoof"}}}}`
	badResp := HandleMCPMessage(q, []byte(badLabelCall), opts)
	badMap := badResp.Result.(map[string]any)
	if badMap["isError"] != true {
		t.Fatalf("expected caller setting labels.persona_todo to fail, got %+v", badMap)
	}
	paddedLabelCall := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"enqueue_task","arguments":{"todo_id":"todo_real","prompt":"run check","repo":"/tmp/repo","labels":{" persona_todo ":"todo_spoof"}}}}`
	paddedResp := HandleMCPMessage(q, []byte(paddedLabelCall), opts)
	if paddedResp.Result.(map[string]any)["isError"] != true {
		t.Fatalf("expected caller setting whitespace-padded persona_todo label to fail, got %+v", paddedResp.Result)
	}

	// 3. Valid enqueue_task via stdio stream forces persona_todo=<todo_id>, source="assistant", readonly default, and tags.
	inputLines := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"enqueue_task","arguments":{"todo_id":"todo_101","prompt":"verify build","repo":"/tmp/repo","priority":"p1","difficulty":"hard","machine":"build-node-1","acceptance":"go test ./..."}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_tasks","arguments":{"todo_id":"todo_101","priority":"p1","difficulty":"hard"}}}`,
	}, "\n") + "\n"

	var outBuf bytes.Buffer
	if err := ServeMCP(q, strings.NewReader(inputLines), &outBuf, opts); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 JSON-RPC responses (notification produces none), got %d: %s", len(lines), outBuf.String())
	}

	var enqRPC struct {
		Result struct {
			IsError           bool `json:"isError"`
			StructuredContent struct {
				Created           bool           `json:"created"`
				PermissionProfile string         `json:"permissionProfile"`
				Task              map[string]any `json:"task"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &enqRPC); err != nil {
		t.Fatal(err)
	}
	if enqRPC.Result.IsError || !enqRPC.Result.StructuredContent.Created {
		t.Fatalf("enqueue_task failed: %s", lines[1])
	}
	if enqRPC.Result.StructuredContent.PermissionProfile != PermissionReadonly {
		t.Fatalf("expected readonly profile, got %s", enqRPC.Result.StructuredContent.PermissionProfile)
	}
	taskID := enqRPC.Result.StructuredContent.Task["id"].(string)

	stored, err := q.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.TodoID() != "todo_101" || stored.Source != AssistantSource ||
		stored.ExternalRef == nil || stored.ExternalRef.System != AssistantSource ||
		stored.ExternalRef.ID != PersonaSystemID || stored.ExternalRef.Conversation != "todo_101" {
		t.Fatalf("unexpected provenance on stored task: %+v, externalRef=%+v", stored, stored.ExternalRef)
	}
	if stored.Priority != PriorityP1 || stored.Difficulty != DifficultyHard || stored.ModelTier != TierStrong || stored.Machine != "build-node-1" {
		t.Fatalf("unexpected scheduling metadata on stored task: %+v", stored)
	}

	// 4. Test get_task and get_evidence tools.
	getTaskOut, err := CallMCPTool(q, "get_task", map[string]any{"id": taskID}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if getTaskOut.(map[string]any)["repo"] != "/tmp/repo" {
		t.Fatalf("unexpected get_task output: %+v", getTaskOut)
	}

	evOut, err := CallMCPTool(q, "get_evidence", map[string]any{"id": taskID}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if evOut.(map[string]any)["id"] != taskID {
		t.Fatalf("unexpected get_evidence output: %+v", evOut)
	}
}

func TestS3TodoOutcomeDecisionWriteback(t *testing.T) {
	now := time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC)
	q, err := NewQueue(t.TempDir(), DefaultQueueConfig())
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var received []OutcomeDecisionPayload
	var authHeaders []string
	seenRefs := map[string]bool{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		if strings.HasSuffix(r.URL.Path, "/todo_missing/decisions") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		var body OutcomeDecisionPayload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received = append(received, body)
		inserted := !seenRefs[body.Ref]
		seenRefs[body.Ref] = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OutcomeDecisionResponse{
			Inserted: inserted,
			Decision: json.RawMessage(`{"id":"dec_1","source":"outcome"}`),
		})
	}))
	defer srv.Close()

	// Create and complete a task linked to todo_ok.
	enq, err := q.Enqueue(EnqueueInput{
		ID:                "task_s3_pass",
		Title:             "Verify release",
		Prompt:            "Run acceptance suite",
		Repo:              "/tmp/repo",
		AcceptanceCommand: "go test ./...",
		Labels:            map[string]string{PersonaTodoLabel: "todo_ok"},
		Now:               now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, att, err := q.LeaseTask(enq.Task.ID, "worker-1", "local", now)
	if err != nil {
		t.Fatal(err)
	}
	code0 := 0
	_, err = q.CompleteAttempt(CompleteAttemptInput{
		TaskID:     enq.Task.ID,
		FenceToken: att.FenceToken,
		WorkerID:   "worker-1",
		Outcome:    StateSucceeded,
		Verdict:    VerdictPass,
		ExitCode:   &code0,
		Message:    "all 24 checks green",
		Artifacts:  []Artifact{{Kind: "log", Path: "logs/test.log"}},
		Now:        now.Add(5 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Also create a task linked to todo_missing (returns 404).
	enq404, _ := q.Enqueue(EnqueueInput{
		ID:         "task_s3_404",
		Prompt:     "Missing todo task",
		Labels:     map[string]string{PersonaTodoLabel: "todo_missing"},
		MaxRetries: new(int),
		Now:        now,
	})
	_, att404, _ := q.LeaseTask(enq404.Task.ID, "worker-1", "local", now)
	_, _ = q.CompleteAttempt(CompleteAttemptInput{
		TaskID:     enq404.Task.ID,
		FenceToken: att404.FenceToken,
		WorkerID:   "worker-1",
		Outcome:    StateFailed,
		ErrorClass: "task_failed",
		Message:    "failed check",
		Now:        now.Add(5 * time.Second),
	})

	writer := NewDecisionWriter(srv.URL, "ext-scoped-token", []string{"todos.write"}, srv.Client())
	results, err := writer.SyncQueueOutcomes(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 sync results, got %+v", results)
	}

	mu.Lock()
	if len(received) != 1 {
		t.Fatalf("expected 1 valid decision POST to todo_ok, got %d", len(received))
	}
	gotPayload := received[0]
	gotAuth := authHeaders[0]
	mu.Unlock()

	if gotAuth != "Bearer ext-scoped-token" {
		t.Fatalf("unexpected Authorization header: %s", gotAuth)
	}
	if gotPayload.Choice != "通过验收" || gotPayload.Ref != "ats:task_s3_pass:succeeded:pass" {
		t.Fatalf("unexpected outcome decision payload: %+v", gotPayload)
	}
	if !strings.Contains(gotPayload.Reason, "go test ./...") || !strings.Contains(gotPayload.Reason, "logs/test.log") {
		t.Fatalf("expected reason to include acceptance command and artifact, got %s", gotPayload.Reason)
	}

	// Second sync must be a no-op (both already written or 404-skipped).
	results2, err := writer.SyncQueueOutcomes(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(results2) != 0 {
		t.Fatalf("expected 0 writes on second idempotent sync, got %+v", results2)
	}
}

func TestGenericQuotaProviderAndPanelView(t *testing.T) {
	now := time.Date(2026, 11, 9, 16, 0, 0, 0, time.UTC)
	resetAt := now.Add(45 * time.Minute)
	uFast := 0.20
	uStrong := 0.85

	rawGatewayAccounts, _ := json.Marshal(GenericGatewayAccounts{
		Sources: []GenericGatewaySource{{ID: "s1", Name: "QuotaPool", Type: "relay"}},
		Accounts: []GenericGatewayAccount{
			{
				Source:    "s1",
				AccountID: "acc_1",
				Provider:  "claude",
				Status:    "active",
				Windows: []GenericGatewayWindow{
					{Kind: "five_hour", Scope: "model", ScopedModel: "claude-haiku-5-5", UsedRatio: &uFast, ResetsAt: &resetAt},
					{Kind: "five_hour", Scope: "model", ScopedModel: "claude-opus-5-5", UsedRatio: &uStrong, ResetsAt: &resetAt},
				},
			},
		},
	})

	quotaFile := filepath.Join(t.TempDir(), "quota.json")
	q, err := NewQueue(t.TempDir(), DefaultQueueConfig())
	if err != nil {
		t.Fatal(err)
	}
	st, err := ParseQuotaSnapshot(rawGatewayAccounts, now)
	if err != nil {
		t.Fatal(err)
	}
	if st.Provider != "QuotaPool" || st.TierRemainingPct[TierFast] != 80 || st.TierRemainingPct[TierStrong] != 15 {
		t.Fatalf("unexpected parsed quota status: %+v", st)
	}
	_ = q.SetQuotaStatus(st)
	_ = quotaFile

	// Add one queued task and one failed task, then render and exercise panel actions.
	enqQueued, _ := q.Enqueue(EnqueueInput{
		ID:         "task_queued_1",
		Title:      "Queued P1 job",
		Prompt:     "Check status",
		Priority:   PriorityP1,
		Difficulty: DifficultyEasy,
		Labels:     map[string]string{PersonaTodoLabel: "todo_ui"},
		Now:        now,
	})
	enqFailed, _ := q.Enqueue(EnqueueInput{
		ID:         "task_failed_1",
		Title:      "Failed P2 job",
		Prompt:     "Run integration",
		Priority:   PriorityP2,
		Difficulty: DifficultyHard,
		MaxRetries: new(int),
		Now:        now,
	})
	_, attF, _ := q.LeaseTask(enqFailed.Task.ID, "local:worker-strong", "local", now)
	_, _ = q.CompleteAttempt(CompleteAttemptInput{
		TaskID:     enqFailed.Task.ID,
		FenceToken: attF.FenceToken,
		WorkerID:   "local:worker-strong",
		Outcome:    StateFailed,
		ErrorClass: "task_failed",
		Message:    "exit status 1",
		Now:        now.Add(time.Second),
	})

	app := newATSApp(q)
	app.nowFunc = func() time.Time { return now.Add(2 * time.Second) }

	view, err := app.render(context.Background(), &ext.UIRequest{
		UIParams: wire.UIParams{Contribution: queueUI, Size: wire.SizeFull, UIVersion: 4, TZ: "Asia/Shanghai"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(view); err != nil {
		t.Fatalf("rendered panel view failed wire.CheckView: %v", err)
	}

	// Action: retry the failed task.
	retryRes, err := app.action(context.Background(), &ext.UIRequest{
		UIParams: wire.UIParams{Contribution: queueUI, Action: "retry:" + enqFailed.Task.ID, TZ: "Asia/Shanghai"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(retryRes.View); err != nil {
		t.Fatalf("retry action view failed wire.CheckView: %v", err)
	}
	retriedTask, _ := q.GetTask(enqFailed.Task.ID)
	if retriedTask.State != StateQueued {
		t.Fatalf("expected failed task to be queued after retry action, got %s", retriedTask.State)
	}

	// Action: cancel the queued task.
	cancelRes, err := app.action(context.Background(), &ext.UIRequest{
		UIParams: wire.UIParams{Contribution: queueUI, Action: "cancel:" + enqQueued.Task.ID, TZ: "Asia/Shanghai"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(cancelRes.View); err != nil {
		t.Fatalf("cancel action view failed wire.CheckView: %v", err)
	}
	canceledTask, _ := q.GetTask(enqQueued.Task.ID)
	if canceledTask.State != StateCanceled {
		t.Fatalf("expected queued task to be canceled after cancel action, got %s", canceledTask.State)
	}
}

func TestGatewayAccountsPartialCooldownDoesNotPauseQueue(t *testing.T) {
	now := time.Date(2026, 11, 9, 17, 0, 0, 0, time.UTC)
	cooldown10m := now.Add(10 * time.Minute)
	cooldown25m := now.Add(25 * time.Minute)

	q, err := NewQueue(t.TempDir(), DefaultQueueConfig())
	if err != nil {
		t.Fatal(err)
	}

	p0Res, err := q.Enqueue(EnqueueInput{
		ID:         "task_p0_partial_cd",
		Title:      "Urgent P0 task",
		Prompt:     "Run P0 check",
		Priority:   PriorityP0,
		Difficulty: DifficultyMedium,
		Now:        now,
	})
	if err != nil {
		t.Fatal(err)
	}
	p2Res, err := q.Enqueue(EnqueueInput{
		ID:         "task_p2_partial_cd",
		Title:      "Normal P2 task",
		Prompt:     "Run P2 check",
		Priority:   PriorityP2,
		Difficulty: DifficultyMedium,
		Now:        now,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. 4 accounts: 3 active, 1 cooling down for 10m -> RemainingPct=75, ActiveAccounts=3, CooldownUntil=nil, mode=full.
	stPartial := ParseGatewayAccounts(GenericGatewayAccounts{
		Sources: []GenericGatewaySource{{ID: "pool", Name: "AccountPool", Type: "relay"}},
		Accounts: []GenericGatewayAccount{
			{Source: "pool", AccountID: "acc_1", Status: "active"},
			{Source: "pool", AccountID: "acc_2", Status: "active"},
			{Source: "pool", AccountID: "acc_3", Status: "active"},
			{Source: "pool", AccountID: "acc_4", Status: "cooldown", CooldownUntil: &cooldown10m},
		},
	}, now)
	if stPartial.ActiveAccounts != 3 || stPartial.TotalAccounts != 4 {
		t.Fatalf("expected 3/4 active accounts, got %d/%d", stPartial.ActiveAccounts, stPartial.TotalAccounts)
	}
	if stPartial.RemainingPct != 75 || stPartial.Exhausted {
		t.Fatalf("expected RemainingPct=75 and Exhausted=false, got rem=%v exhausted=%v", stPartial.RemainingPct, stPartial.Exhausted)
	}
	if stPartial.CooldownUntil != nil {
		t.Fatalf("expected global CooldownUntil to be nil when activeAccounts > 0, got %v", stPartial.CooldownUntil)
	}

	_ = q.SetQuotaStatus(stPartial)
	planPartial := q.PlanSchedule(now)
	if planPartial.AdmissionMode != AdmissionFull {
		t.Fatalf("expected full admission mode with 3/4 active accounts, got %s", planPartial.AdmissionMode)
	}
	if len(planPartial.AdmittedCandidates) != 2 {
		t.Fatalf("expected both P0 and P2 tasks admitted, got %d", len(planPartial.AdmittedCandidates))
	}

	leased, attempts, _, err := q.DispatchNext(1, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(leased) != 1 || len(attempts) != 1 || leased[0].ID != p0Res.Task.ID {
		t.Fatalf("expected DispatchNext to lease P0 task %s, got %+v", p0Res.Task.ID, leased)
	}

	// Re-enqueue another P0 task to test priority_only and all-cooldown transitions.
	p0Res2, err := q.Enqueue(EnqueueInput{
		ID:         "task_p0_reserve_cd",
		Title:      "Second P0 task",
		Prompt:     "Run P0 under reserve",
		Priority:   PriorityP0,
		Difficulty: DifficultyMedium,
		Now:        now,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. 4 accounts: 1 active, 3 cooling down -> RemainingPct=25 (<30 reserve), CooldownUntil=nil, mode=priority_only.
	stReserve := ParseGatewayAccounts(GenericGatewayAccounts{
		Sources: []GenericGatewaySource{{ID: "pool", Name: "AccountPool", Type: "relay"}},
		Accounts: []GenericGatewayAccount{
			{Source: "pool", AccountID: "acc_1", Status: "active"},
			{Source: "pool", AccountID: "acc_2", Status: "cooldown", CooldownUntil: &cooldown10m},
			{Source: "pool", AccountID: "acc_3", Status: "cooldown", CooldownUntil: &cooldown25m},
			{Source: "pool", AccountID: "acc_4", Status: "cooldown", CooldownUntil: &cooldown25m},
		},
	}, now)
	if stReserve.ActiveAccounts != 1 || stReserve.RemainingPct != 25 || stReserve.Exhausted || stReserve.CooldownUntil != nil {
		t.Fatalf("unexpected reserve quota status: %+v", stReserve)
	}
	_ = q.SetQuotaStatus(stReserve)
	planReserve := q.PlanSchedule(now)
	if planReserve.AdmissionMode != AdmissionPriorityOnly {
		t.Fatalf("expected priority_only mode when 1/4 accounts active (25%%), got %s", planReserve.AdmissionMode)
	}
	if len(planReserve.AdmittedCandidates) != 1 || planReserve.AdmittedCandidates[0].TaskID != p0Res2.Task.ID {
		t.Fatalf("expected only P0 task admitted in priority_only mode, got %+v", planReserve.AdmittedCandidates)
	}
	if len(planReserve.DeferredCandidates) != 1 || planReserve.DeferredCandidates[0].TaskID != p2Res.Task.ID {
		t.Fatalf("expected P2 task deferred in priority_only mode, got %+v", planReserve.DeferredCandidates)
	}

	// 3. All schedulable accounts in cooldown (activeAccounts == 0) -> Exhausted=true, CooldownUntil=earliest cooldown, mode=paused_quota.
	notSchedulable := false
	stAllCooling := ParseGatewayAccounts(GenericGatewayAccounts{
		Sources: []GenericGatewaySource{{ID: "pool", Name: "AccountPool", Type: "relay"}},
		Accounts: []GenericGatewayAccount{
			{Source: "pool", AccountID: "acc_1", Status: "cooldown", CooldownUntil: &cooldown25m},
			{Source: "pool", AccountID: "acc_2", Status: "cooldown", CooldownUntil: &cooldown10m},
			{Source: "pool", AccountID: "acc_3", Status: "disabled", Schedulable: &notSchedulable},
		},
	}, now)
	if stAllCooling.ActiveAccounts != 0 || !stAllCooling.Exhausted || stAllCooling.RemainingPct != 0 {
		t.Fatalf("expected activeAccounts=0, Exhausted=true, RemainingPct=0 when all accounts cooling/disabled, got %+v", stAllCooling)
	}
	if stAllCooling.CooldownUntil == nil || !stAllCooling.CooldownUntil.Equal(cooldown10m) {
		t.Fatalf("expected global CooldownUntil=%v (earliest cooldown), got %v", cooldown10m, stAllCooling.CooldownUntil)
	}
	_ = q.SetQuotaStatus(stAllCooling)
	planPaused := q.PlanSchedule(now)
	if planPaused.AdmissionMode != AdmissionPausedQuota || len(planPaused.AdmittedCandidates) != 0 {
		t.Fatalf("expected paused_quota with 0 admitted when all accounts cooling, got mode=%s admitted=%d",
			planPaused.AdmissionMode, len(planPaused.AdmittedCandidates))
	}
	leasedPaused, _, _, err := q.DispatchNext(1, now)
	if err != nil || len(leasedPaused) != 0 {
		t.Fatalf("expected DispatchNext to lease 0 tasks in paused_quota, got %d (err=%v)", len(leasedPaused), err)
	}
}
