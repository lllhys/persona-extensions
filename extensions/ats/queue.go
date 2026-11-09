package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Task states in the self-contained ATS state machine.
const (
	StateQueued     = "queued"
	StateLeased     = "leased"
	StateRunning    = "running"
	StateNeedsInput = "needs_input"
	StateSucceeded  = "succeeded"
	StateFailed     = "failed"
	StateCanceled   = "canceled"
	StateCancelled  = "cancelled"
	StateExpired    = "expired"
)

// AllTaskStates lists every state accepted by filters.
var AllTaskStates = []string{
	StateQueued,
	StateLeased,
	StateRunning,
	StateNeedsInput,
	StateSucceeded,
	StateFailed,
	StateCanceled,
	StateCancelled,
	StateExpired,
}

// Verdicts for task verification evidence.
const (
	VerdictPass       = "pass"
	VerdictFail       = "fail"
	VerdictUnverified = "unverified"
)

// Permission profiles: readonly by default; only explicit "default" enables write permissions.
const (
	PermissionReadonly     = "readonly"
	PermissionReadOnlyDash = "read-only"
	PermissionDefault      = "default"
)

// Priority levels for offline-compute-style multi-factor scheduling.
const (
	PriorityP0 = "p0"
	PriorityP1 = "p1"
	PriorityP2 = "p2"
	PriorityP3 = "p3"
)

// Difficulty levels and mapped model tiers.
const (
	DifficultyEasy    = "easy"
	DifficultyMedium  = "medium"
	DifficultyHard    = "hard"
	DifficultyExtreme = "extreme"

	TierFast     = "fast"
	TierStandard = "standard"
	TierStrong   = "strong"
)

// PersonaTodoLabel is the mandatory label linking a task to a Persona to-do.
const (
	PersonaTodoLabel = "persona_todo"
	AssistantSource  = "assistant"
	PersonaSystemID  = "persona"
)

// Errors returned by the queue and lease state machine.
var (
	ErrTaskNotFound = errors.New("task not found")
	ErrStaleAttempt = errors.New("stale attempt or mismatched worker lease fence (403)")
)

// AdmissionError is returned when queue admission limits are exceeded (HTTP 429).
type AdmissionError struct {
	Code       string `json:"code"`
	HTTPStatus int    `json:"http_status"`
	Queued     int    `json:"queued"`
	MaxQueued  int    `json:"max_queued"`
	Message    string `json:"message"`
}

func (e *AdmissionError) Error() string {
	return e.Message
}

// ExternalRef identifies the external system and conversation that originated a task.
type ExternalRef struct {
	System       string `json:"system"`
	ID           string `json:"id"`
	Conversation string `json:"conversation,omitempty"`
}

// AcceptanceSpec holds the verification command for a task.
type AcceptanceSpec struct {
	Command string `json:"command"`
}

// WorkspaceSpec holds the pinned git repository and base commit/ref.
type WorkspaceSpec struct {
	Repo    string `json:"repo"`
	BaseSHA string `json:"base_sha"`
}

// Artifact records a verification output or patch produced by an attempt.
type Artifact struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes,omitempty"`
}

// Attempt represents one execution lease of a task, guarded by fence tokens and worker IDs.
type Attempt struct {
	AttemptID      string     `json:"attempt_id"`
	TaskID         string     `json:"task_id"`
	AttemptNo      int        `json:"attempt_no"`
	FenceToken     int        `json:"fence_token"`
	WorkerID       string     `json:"worker_id"`
	Machine        string     `json:"machine,omitempty"`
	ModelTier      string     `json:"model_tier,omitempty"`
	Harness        string     `json:"harness,omitempty"`
	State          string     `json:"state"`
	LeasedAt       time.Time  `json:"leased_at"`
	LeaseExpiresAt time.Time  `json:"lease_expires_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	ExitCode       *int       `json:"exit_code,omitempty"`
	ErrorClass     string     `json:"error_class,omitempty"`
	Verdict        string     `json:"verdict,omitempty"`
	Summary        string     `json:"summary,omitempty"`
}

// EvidenceReport stores a verification report from an attempt.
type EvidenceReport struct {
	TaskID     string     `json:"task_id"`
	AttemptNo  int        `json:"attempt_no"`
	FenceToken int        `json:"fence_token"`
	WorkerID   string     `json:"worker_id"`
	ReportID   string     `json:"report_id"`
	ReceivedAt time.Time  `json:"received_at"`
	Outcome    string     `json:"outcome"`
	Verdict    string     `json:"verdict,omitempty"`
	ErrorClass string     `json:"error_class,omitempty"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	Message    string     `json:"message,omitempty"`
	Artifacts  []Artifact `json:"artifacts,omitempty"`
}

// Task is a persistent unit of work in the ATS queue.
type Task struct {
	ID                 string            `json:"id"`
	IdempotencyKey     string            `json:"idempotency_key,omitempty"`
	Title              string            `json:"title"`
	Prompt             string            `json:"prompt"`
	State              string            `json:"state"`
	Verdict            string            `json:"verdict"`
	FailureReason      string            `json:"failure_reason,omitempty"`
	QoS                string            `json:"qos"`
	Pools              []string          `json:"pools"`
	Priority           string            `json:"priority"`
	Difficulty         string            `json:"difficulty"`
	ModelTier          string            `json:"model_tier"`
	QuotaWeight        float64           `json:"quota_weight"`
	Machine            string            `json:"machine,omitempty"`
	PermissionProfile  string            `json:"permission_profile"`
	Workspace          WorkspaceSpec     `json:"workspace"`
	Acceptance         *AcceptanceSpec   `json:"acceptance,omitempty"`
	Labels             map[string]string `json:"labels"`
	Source             string            `json:"source"`
	ExternalRef        *ExternalRef      `json:"external_ref,omitempty"`
	AttemptNo          int               `json:"attempt_no"`
	FenceToken         int               `json:"fence_token"`
	MaxRetries         int               `json:"max_retries"`
	RetriesLeft        int               `json:"retries_left"`
	LostCount          int               `json:"lost_count"`
	LastFailedWorker   string            `json:"last_failed_worker,omitempty"`
	SubmittedAt        time.Time         `json:"submitted_at"`
	EnqueuedAt         time.Time         `json:"enqueued_at"`
	NotBefore          *time.Time        `json:"not_before,omitempty"`
	FinishedAt         *time.Time        `json:"finished_at,omitempty"`
	Summary            string            `json:"summary,omitempty"`
	SupersededBy       string            `json:"superseded_by,omitempty"`
	DecisionWrittenRef string            `json:"decision_written_ref,omitempty"`
	DecisionSkipReason string            `json:"decision_skip_reason,omitempty"`
	Attempts           []Attempt         `json:"attempts,omitempty"`
	Reports            []EvidenceReport  `json:"reports,omitempty"`
}

// TodoID returns the linked Persona to-do ID, if any.
func (t *Task) TodoID() string {
	if t == nil || t.Labels == nil {
		return ""
	}
	return t.Labels[PersonaTodoLabel]
}

// IsTerminal reports whether the task is in a terminal state.
func (t *Task) IsTerminal() bool {
	if t == nil {
		return false
	}
	switch t.State {
	case StateSucceeded, StateFailed, StateCanceled, StateCancelled, StateExpired:
		return true
	default:
		return false
	}
}

// QueueConfig controls admission limits, lease TTL, and retry backoff.
type QueueConfig struct {
	MaxQueued          int           `json:"max_queued"`
	LeaseTTL           time.Duration `json:"lease_ttl"`
	MaxRetries         int           `json:"max_retries"`
	MaxLost            int           `json:"max_lost"`
	RetryBaseDelay     time.Duration `json:"retry_base_delay"`
	RetryMaxDelay      time.Duration `json:"retry_max_delay"`
	ReserveHeadroomPct float64       `json:"reserve_headroom_pct"`
}

// DefaultQueueConfig returns the standard production defaults.
func DefaultQueueConfig() QueueConfig {
	return QueueConfig{
		MaxQueued:          100,
		LeaseTTL:           75 * time.Second,
		MaxRetries:         2,
		MaxLost:            5,
		RetryBaseDelay:     5 * time.Second,
		RetryMaxDelay:      30 * time.Minute,
		ReserveHeadroomPct: 30,
	}
}

// NormalizePermissionProfile defaults to "readonly"; only "default" grants write access.
func NormalizePermissionProfile(raw string) (string, error) {
	s := strings.TrimSpace(strings.ToLower(raw))
	switch s {
	case "", PermissionReadonly, PermissionReadOnlyDash:
		return PermissionReadonly, nil
	case PermissionDefault:
		return PermissionDefault, nil
	default:
		return "", fmt.Errorf("permission_profile must be one of %s, %s, %s", PermissionReadonly, PermissionReadOnlyDash, PermissionDefault)
	}
}

// NormalizePriority parses priority from an explicit value or tag/label (`p0`..`p3`, `high`/`normal`/`low`).
func NormalizePriority(raw string) (string, error) {
	s := strings.TrimSpace(strings.ToLower(raw))
	s = strings.TrimPrefix(s, "priority=")
	switch s {
	case "", PriorityP2, "normal", "standard":
		return PriorityP2, nil
	case PriorityP0, "critical", "urgent", "expedited":
		return PriorityP0, nil
	case PriorityP1, "high":
		return PriorityP1, nil
	case PriorityP3, "low", "bulk", "background":
		return PriorityP3, nil
	default:
		return "", fmt.Errorf("priority must be one of p0, p1, p2, p3 (or high, normal, low)")
	}
}

// NormalizeDifficulty parses difficulty (`easy`, `medium`, `hard`, `extreme`) or model tier (`fast`, `standard`, `strong`)
// and returns (difficulty, modelTier, quotaWeight).
func NormalizeDifficulty(rawDiff, rawTier string) (string, string, float64, error) {
	d := strings.TrimSpace(strings.ToLower(rawDiff))
	d = strings.TrimPrefix(d, "difficulty=")
	t := strings.TrimSpace(strings.ToLower(rawTier))
	t = strings.TrimPrefix(t, "tier=")

	if d == "" && t != "" {
		switch t {
		case TierFast:
			d = DifficultyEasy
		case TierStandard:
			d = DifficultyMedium
		case TierStrong:
			d = DifficultyHard
		default:
			return "", "", 0, fmt.Errorf("tier must be one of fast, standard, strong")
		}
	}

	var tier string
	var weight float64
	switch d {
	case "", DifficultyMedium:
		d = DifficultyMedium
		tier = TierStandard
		weight = 2.5
	case DifficultyEasy:
		tier = TierFast
		weight = 1.0
	case DifficultyHard:
		tier = TierStrong
		weight = 5.0
	case DifficultyExtreme:
		tier = TierStrong
		weight = 8.0
	default:
		return "", "", 0, fmt.Errorf("difficulty must be one of easy, medium, hard, extreme")
	}

	if t != "" {
		switch t {
		case TierFast, TierStandard, TierStrong:
			tier = t
		default:
			return "", "", 0, fmt.Errorf("tier must be one of fast, standard, strong")
		}
	}
	return d, tier, weight, nil
}

// ExtractSchedulingTags inspects explicit parameters and labels/tags to resolve canonical Priority, Difficulty, ModelTier, QuotaWeight, and Machine.
func ExtractSchedulingTags(explicitPriority, explicitDifficulty, explicitMachine, qos string, labels map[string]string, tags []string) (priority, difficulty, modelTier, machine string, quotaWeight float64, normLabels map[string]string, err error) {
	normLabels = make(map[string]string, len(labels)+4)
	for k, v := range labels {
		cleanKey := strings.TrimSpace(k)
		if cleanKey == "" {
			continue
		}
		normLabels[cleanKey] = strings.TrimSpace(v)
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if k, v, ok := strings.Cut(tag, "="); ok {
			k = strings.TrimSpace(strings.ToLower(k))
			v = strings.TrimSpace(v)
			if k == PersonaTodoLabel {
				continue
			}
			if _, exists := normLabels[k]; !exists {
				normLabels[k] = v
			}
			continue
		}
		low := strings.ToLower(tag)
		switch low {
		case PriorityP0, PriorityP1, PriorityP2, PriorityP3, "high", "normal", "low":
			if _, exists := normLabels["priority"]; !exists {
				normLabels["priority"] = low
			}
		case DifficultyEasy, DifficultyMedium, DifficultyHard, DifficultyExtreme:
			if _, exists := normLabels["difficulty"]; !exists {
				normLabels["difficulty"] = low
			}
		}
	}

	rawPrio := explicitPriority
	if rawPrio == "" {
		rawPrio = normLabels["priority"]
	}
	if rawPrio == "" && qos != "" {
		switch strings.ToLower(qos) {
		case "expedited":
			rawPrio = PriorityP0
		case "bulk":
			rawPrio = PriorityP3
		}
	}
	priority, err = NormalizePriority(rawPrio)
	if err != nil {
		return "", "", "", "", 0, nil, err
	}

	rawDiff := explicitDifficulty
	if rawDiff == "" {
		rawDiff = normLabels["difficulty"]
	}
	rawTier := normLabels["tier"]
	difficulty, modelTier, quotaWeight, err = NormalizeDifficulty(rawDiff, rawTier)
	if err != nil {
		return "", "", "", "", 0, nil, err
	}

	machine = strings.TrimSpace(explicitMachine)
	if machine == "" {
		machine = strings.TrimSpace(normLabels["machine"])
	}
	machine = strings.TrimPrefix(machine, "machine=")

	normLabels["priority"] = priority
	normLabels["difficulty"] = difficulty
	normLabels["tier"] = modelTier
	if machine != "" {
		normLabels["machine"] = machine
	}
	return priority, difficulty, modelTier, machine, quotaWeight, normLabels, nil
}

// ComputeRetryDelay computes deterministic exponential backoff + jitter using SHA-256(task_id, attempt_no).
func ComputeRetryDelay(taskID string, attemptNo int, baseDelay, maxDelay time.Duration) time.Duration {
	if baseDelay <= 0 {
		baseDelay = 5 * time.Second
	}
	if maxDelay <= 0 {
		maxDelay = 30 * time.Minute
	}
	if attemptNo < 1 {
		attemptNo = 1
	}
	shift := attemptNo - 1
	if shift > 16 {
		shift = 16
	}
	exp := float64(baseDelay) * math.Pow(2, float64(shift))
	if exp > float64(maxDelay) {
		exp = float64(maxDelay)
	}

	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", taskID, attemptNo)))
	u := binary.BigEndian.Uint64(sum[:8])
	unit := float64(u) / float64(math.MaxUint64)
	factor := 0.75 + 0.50*unit
	delay := time.Duration(exp * factor)
	if delay > maxDelay {
		delay = maxDelay
	}
	if delay < baseDelay/2 {
		delay = baseDelay / 2
	}
	return delay
}

// EnqueueInput holds all parameters for creating a task in the queue.
type EnqueueInput struct {
	ID                string
	IdempotencyKey    string
	Title             string
	Prompt            string
	Repo              string
	BaseSHA           string
	QoS               string
	Pool              string
	Priority          string
	Difficulty        string
	Machine           string
	AcceptanceCommand string
	PermissionProfile string
	Labels            map[string]string
	Tags              []string
	Source            string
	ExternalRef       *ExternalRef
	MaxRetries        *int
	Now               time.Time
}

// EnqueueResult reports whether a new task was created or deduplicated by idempotency key.
type EnqueueResult struct {
	Created bool  `json:"created"`
	Task    *Task `json:"task"`
}

// CompleteAttemptInput holds the parameters for finishing or reporting an attempt (fenced by worker_id and fence_token).
type CompleteAttemptInput struct {
	TaskID     string
	AttemptID  string
	AttemptNo  int
	FenceToken int
	WorkerID   string
	ReportID   string
	Outcome    string // succeeded | failed | canceled | rate_limited
	Verdict    string // pass | fail | unverified
	ErrorClass string // infra_error | timeout | task_failed | rate_limited | auth_expired
	ExitCode   *int
	Message    string
	Artifacts  []Artifact
	Retryable  *bool
	Now        time.Time
}

// TaskFilter specifies filtering criteria for ListTasks.
type TaskFilter struct {
	States     []string
	Pool       string
	Source     string
	TodoID     string
	Priority   string
	Difficulty string
	Machine    string
	Limit      int
}

type persistedQueue struct {
	Seq      int64             `json:"seq"`
	Config   *QueueConfig      `json:"config,omitempty"`
	Tasks    []*Task           `json:"tasks"`
	Machines []MachineSlot     `json:"machines,omitempty"`
	Quota    *QuotaStatus      `json:"quota,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Queue manages persistent tasks, leases, retries, and scheduling state.
type Queue struct {
	mu       sync.RWMutex
	dataFile string
	cfg      QueueConfig
	seq      int64
	tasks    map[string]*Task
	machines map[string]MachineSlot
	quota    QuotaStatus
}

// NewQueue creates or loads a persistent queue stored in dataDir.
func NewQueue(dataDir string, cfg QueueConfig) (*Queue, error) {
	if cfg.MaxQueued <= 0 {
		cfg.MaxQueued = 100
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 75 * time.Second
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 2
	}
	if cfg.MaxLost <= 0 {
		cfg.MaxLost = 5
	}
	if cfg.RetryBaseDelay <= 0 {
		cfg.RetryBaseDelay = 5 * time.Second
	}
	if cfg.RetryMaxDelay <= 0 {
		cfg.RetryMaxDelay = 30 * time.Minute
	}
	if cfg.ReserveHeadroomPct <= 0 {
		cfg.ReserveHeadroomPct = 30
	}

	q := &Queue{
		cfg:      cfg,
		tasks:    make(map[string]*Task),
		machines: make(map[string]MachineSlot),
		quota:    DefaultQuotaStatus(),
	}
	if strings.TrimSpace(dataDir) != "" {
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return nil, err
		}
		q.dataFile = filepath.Join(dataDir, "queue-state.json")
		if err := q.loadLocked(); err != nil {
			return nil, err
		}
	}
	return q, nil
}

// UpdateConfig updates the queue's runtime configuration safely and persists it so MCP processes share it.
func (q *Queue) UpdateConfig(cfg QueueConfig) {
	q.mu.Lock()
	defer q.mu.Unlock()
	_ = q.loadLocked()
	if cfg.MaxQueued > 0 {
		q.cfg.MaxQueued = cfg.MaxQueued
	}
	if cfg.LeaseTTL > 0 {
		q.cfg.LeaseTTL = cfg.LeaseTTL
	}
	if cfg.MaxRetries >= 0 {
		q.cfg.MaxRetries = cfg.MaxRetries
	}
	if cfg.ReserveHeadroomPct > 0 {
		q.cfg.ReserveHeadroomPct = cfg.ReserveHeadroomPct
	}
	_ = q.saveLocked()
}

// Config returns a copy of the current QueueConfig.
func (q *Queue) Config() QueueConfig {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.cfg
}

// Reload reloads disk state so external processes (such as MCP invocations) see fresh updates.
func (q *Queue) Reload() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.loadLocked()
}

func (q *Queue) loadLocked() error {
	if q.dataFile == "" {
		return nil
	}
	raw, err := os.ReadFile(q.dataFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var p persistedQueue
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	q.seq = p.Seq
	if p.Config != nil {
		if p.Config.MaxQueued > 0 {
			q.cfg.MaxQueued = p.Config.MaxQueued
		}
		if p.Config.LeaseTTL > 0 {
			q.cfg.LeaseTTL = p.Config.LeaseTTL
		}
		if p.Config.MaxRetries >= 0 {
			q.cfg.MaxRetries = p.Config.MaxRetries
		}
		if p.Config.ReserveHeadroomPct > 0 {
			q.cfg.ReserveHeadroomPct = p.Config.ReserveHeadroomPct
		}
	}
	q.tasks = make(map[string]*Task, len(p.Tasks))
	for _, t := range p.Tasks {
		if t != nil && t.ID != "" {
			q.tasks[t.ID] = cloneTask(t)
		}
	}
	if len(p.Machines) > 0 {
		q.machines = make(map[string]MachineSlot, len(p.Machines))
		for _, m := range p.Machines {
			q.machines[m.Name] = m
		}
	}
	if p.Quota != nil {
		q.quota = *p.Quota
	}
	return nil
}

func (q *Queue) saveLocked() error {
	if q.dataFile == "" {
		return nil
	}
	tasks := make([]*Task, 0, len(q.tasks))
	for _, t := range q.tasks {
		tasks = append(tasks, t)
	}
	sort.Slice(tasks, func(i, j int) bool {
		if !tasks[i].SubmittedAt.Equal(tasks[j].SubmittedAt) {
			return tasks[i].SubmittedAt.Before(tasks[j].SubmittedAt)
		}
		return tasks[i].ID < tasks[j].ID
	})
	machines := make([]MachineSlot, 0, len(q.machines))
	for _, m := range q.machines {
		machines = append(machines, m)
	}
	sort.Slice(machines, func(i, j int) bool {
		return machines[i].Name < machines[j].Name
	})
	quotaCopy := q.quota
	cfgCopy := q.cfg
	p := persistedQueue{
		Seq:      q.seq,
		Config:   &cfgCopy,
		Tasks:    tasks,
		Machines: machines,
		Quota:    &quotaCopy,
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", q.dataFile, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, q.dataFile)
}

func cloneTask(t *Task) *Task {
	if t == nil {
		return nil
	}
	out := *t
	if t.Pools != nil {
		out.Pools = slices.Clone(t.Pools)
	}
	if t.Labels != nil {
		out.Labels = maps.Clone(t.Labels)
	}
	if t.Acceptance != nil {
		acc := *t.Acceptance
		out.Acceptance = &acc
	}
	if t.ExternalRef != nil {
		ref := *t.ExternalRef
		out.ExternalRef = &ref
	}
	if t.NotBefore != nil {
		nb := *t.NotBefore
		out.NotBefore = &nb
	}
	if t.FinishedAt != nil {
		fa := *t.FinishedAt
		out.FinishedAt = &fa
	}
	if t.Attempts != nil {
		out.Attempts = make([]Attempt, len(t.Attempts))
		for i, a := range t.Attempts {
			att := a
			if a.StartedAt != nil {
				sa := *a.StartedAt
				att.StartedAt = &sa
			}
			if a.FinishedAt != nil {
				fa := *a.FinishedAt
				att.FinishedAt = &fa
			}
			if a.ExitCode != nil {
				ec := *a.ExitCode
				att.ExitCode = &ec
			}
			out.Attempts[i] = att
		}
	}
	if t.Reports != nil {
		out.Reports = make([]EvidenceReport, len(t.Reports))
		for i, r := range t.Reports {
			rep := r
			if r.ExitCode != nil {
				ec := *r.ExitCode
				rep.ExitCode = &ec
			}
			if r.Artifacts != nil {
				rep.Artifacts = slices.Clone(r.Artifacts)
			}
			out.Reports[i] = rep
		}
	}
	return &out
}

func (q *Queue) activeQueuedCountLocked() int {
	n := 0
	for _, t := range q.tasks {
		if t.State == StateQueued && t.SupersededBy == "" {
			n++
		}
	}
	return n
}

// Enqueue validates and adds a task to the queue, enforcing readonly defaults and max_queued admission control.
func (q *Queue) Enqueue(in EnqueueInput) (EnqueueResult, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	_ = q.loadLocked()

	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	if strings.TrimSpace(in.Prompt) == "" {
		return EnqueueResult{}, errors.New("prompt must be a non-empty string")
	}

	perm, err := NormalizePermissionProfile(in.PermissionProfile)
	if err != nil {
		return EnqueueResult{}, err
	}

	prio, diff, tier, machine, weight, labels, err := ExtractSchedulingTags(
		in.Priority,
		in.Difficulty,
		in.Machine,
		in.QoS,
		in.Labels,
		in.Tags,
	)
	if err != nil {
		return EnqueueResult{}, err
	}
	if in.ExternalRef != nil && strings.TrimSpace(in.ExternalRef.Conversation) != "" {
		labels[PersonaTodoLabel] = strings.TrimSpace(in.ExternalRef.Conversation)
	}

	// Idempotency check before max_queued cap so replays of an already-admitted task succeed.
	if key := strings.TrimSpace(in.IdempotencyKey); key != "" {
		for _, existing := range q.tasks {
			if existing.IdempotencyKey == key {
				return EnqueueResult{Created: false, Task: cloneTask(existing)}, nil
			}
		}
	}

	// Admission control on maximum queued tasks.
	queuedCount := q.activeQueuedCountLocked()
	if queuedCount >= q.cfg.MaxQueued {
		return EnqueueResult{}, &AdmissionError{
			Code:       "too_many_queued",
			HTTPStatus: 429,
			Queued:     queuedCount,
			MaxQueued:  q.cfg.MaxQueued,
			Message:    fmt.Sprintf("too_many_queued (429): queue has %d queued tasks, reaching max_queued=%d", queuedCount, q.cfg.MaxQueued),
		}
	}

	q.seq++
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = fmt.Sprintf("task_%d", q.seq)
	}
	if _, exists := q.tasks[id]; exists {
		id = fmt.Sprintf("task_%d_%d", now.UnixMilli(), q.seq)
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = firstLinePreview(in.Prompt, 60)
	}
	qos := strings.TrimSpace(in.QoS)
	if qos == "" {
		switch prio {
		case PriorityP0:
			qos = "expedited"
		case PriorityP3:
			qos = "bulk"
		default:
			qos = "standard"
		}
	}
	pool := strings.TrimSpace(in.Pool)
	if pool == "" {
		pool = "default"
	}
	repo := strings.TrimSpace(in.Repo)
	if repo == "" {
		repo = "/"
	}
	baseSHA := strings.TrimSpace(in.BaseSHA)
	if baseSHA == "" {
		baseSHA = "HEAD"
	}
	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = AssistantSource
	}
	maxRetries := q.cfg.MaxRetries
	if in.MaxRetries != nil && *in.MaxRetries >= 0 {
		maxRetries = *in.MaxRetries
	}

	var acceptance *AcceptanceSpec
	if cmd := strings.TrimSpace(in.AcceptanceCommand); cmd != "" {
		acceptance = &AcceptanceSpec{Command: cmd}
	}

	task := &Task{
		ID:                id,
		IdempotencyKey:    strings.TrimSpace(in.IdempotencyKey),
		Title:             title,
		Prompt:            in.Prompt,
		State:             StateQueued,
		Verdict:           VerdictUnverified,
		QoS:               qos,
		Pools:             []string{pool},
		Priority:          prio,
		Difficulty:        diff,
		ModelTier:         tier,
		QuotaWeight:       weight,
		Machine:           machine,
		PermissionProfile: perm,
		Workspace: WorkspaceSpec{
			Repo:    repo,
			BaseSHA: baseSHA,
		},
		Acceptance:  acceptance,
		Labels:      labels,
		Source:      source,
		ExternalRef: in.ExternalRef,
		AttemptNo:   0,
		FenceToken:  0,
		MaxRetries:  maxRetries,
		RetriesLeft: maxRetries,
		SubmittedAt: now,
		EnqueuedAt:  now,
	}

	q.tasks[task.ID] = task
	if err := q.saveLocked(); err != nil {
		return EnqueueResult{}, err
	}
	return EnqueueResult{Created: true, Task: cloneTask(task)}, nil
}

// LeaseTask claims a specific queued task for workerID on machine, issuing a worker lease and incrementing the fence token.
func (q *Queue) LeaseTask(taskID, workerID, machine string, now time.Time) (*Task, *Attempt, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	_ = q.loadLocked()

	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, nil, errors.New("worker_id must not be empty")
	}

	t, ok := q.tasks[taskID]
	if !ok {
		return nil, nil, ErrTaskNotFound
	}
	if t.State != StateQueued {
		return nil, nil, fmt.Errorf("task %s is %s, not queued", taskID, t.State)
	}
	if t.NotBefore != nil && now.Before(*t.NotBefore) {
		return nil, nil, fmt.Errorf("task %s is cooling down until %s", taskID, t.NotBefore.Format(time.RFC3339))
	}

	t.AttemptNo++
	t.FenceToken = t.AttemptNo
	t.State = StateRunning
	started := now
	att := Attempt{
		AttemptID:      fmt.Sprintf("%s-att-%d", t.ID, t.AttemptNo),
		TaskID:         t.ID,
		AttemptNo:      t.AttemptNo,
		FenceToken:     t.FenceToken,
		WorkerID:       workerID,
		Machine:        strings.TrimSpace(machine),
		ModelTier:      t.ModelTier,
		Harness:        "claude",
		State:          StateRunning,
		LeasedAt:       now,
		LeaseExpiresAt: now.Add(q.cfg.LeaseTTL),
		StartedAt:      &started,
	}
	t.Attempts = append(t.Attempts, att)

	if mName := strings.TrimSpace(machine); mName != "" {
		if m, exists := q.machines[mName]; exists {
			m.RunningSlots++
			q.machines[mName] = m
		}
	}

	if err := q.saveLocked(); err != nil {
		return nil, nil, err
	}
	attCopy := att
	return cloneTask(t), &attCopy, nil
}

// Heartbeat renews an active attempt's lease, verifying worker_id and fence_token.
func (q *Queue) Heartbeat(taskID, workerID string, fenceToken int, now time.Time) (*Attempt, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	_ = q.loadLocked()

	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	t, ok := q.tasks[taskID]
	if !ok {
		return nil, ErrTaskNotFound
	}
	if fenceToken <= 0 || (t.State != StateRunning && t.State != StateLeased) || t.FenceToken != fenceToken || len(t.Attempts) == 0 {
		return nil, ErrStaleAttempt
	}
	idx := len(t.Attempts) - 1
	att := &t.Attempts[idx]
	if att.AttemptNo != fenceToken || att.FenceToken != fenceToken || att.WorkerID != strings.TrimSpace(workerID) {
		return nil, ErrStaleAttempt
	}
	if att.State != StateRunning && att.State != StateLeased {
		return nil, ErrStaleAttempt
	}
	if now.After(att.LeaseExpiresAt) {
		return nil, ErrStaleAttempt
	}
	att.LeaseExpiresAt = now.Add(q.cfg.LeaseTTL)
	if err := q.saveLocked(); err != nil {
		return nil, err
	}
	out := *att
	return &out, nil
}

// CompleteAttempt records an attempt's outcome and evidence report, guarded by fence token, attempt ID, lease expiry, and worker ID.
func (q *Queue) CompleteAttempt(in CompleteAttemptInput) (*Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	_ = q.loadLocked()

	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	t, ok := q.tasks[in.TaskID]
	if !ok {
		return nil, ErrTaskNotFound
	}

	fence := in.FenceToken
	if fence <= 0 {
		fence = in.AttemptNo
	}
	if fence <= 0 || (t.State != StateRunning && t.State != StateLeased) || t.FenceToken != fence || len(t.Attempts) == 0 {
		return nil, ErrStaleAttempt
	}
	idx := len(t.Attempts) - 1
	att := &t.Attempts[idx]
	if att.FenceToken != fence || att.WorkerID != strings.TrimSpace(in.WorkerID) {
		return nil, ErrStaleAttempt
	}
	if in.AttemptID != "" && att.AttemptID != strings.TrimSpace(in.AttemptID) {
		return nil, ErrStaleAttempt
	}
	if att.State != StateRunning && att.State != StateLeased {
		return nil, ErrStaleAttempt
	}
	if now.After(att.LeaseExpiresAt) {
		return nil, ErrStaleAttempt
	}

	reportID := strings.TrimSpace(in.ReportID)
	if reportID == "" {
		reportID = fmt.Sprintf("rep_%s_%d", t.ID, fence)
	}
	for _, r := range t.Reports {
		if r.AttemptNo == fence && r.ReportID == reportID {
			return cloneTask(t), nil
		}
	}

	verdict := strings.TrimSpace(strings.ToLower(in.Verdict))
	switch verdict {
	case "passed", VerdictPass:
		verdict = VerdictPass
	case "failed", VerdictFail:
		verdict = VerdictFail
	default:
		verdict = VerdictUnverified
	}

	rep := EvidenceReport{
		TaskID:     t.ID,
		AttemptNo:  fence,
		FenceToken: fence,
		WorkerID:   att.WorkerID,
		ReportID:   reportID,
		ReceivedAt: now,
		Outcome:    in.Outcome,
		Verdict:    verdict,
		ErrorClass: in.ErrorClass,
		ExitCode:   in.ExitCode,
		Message:    in.Message,
		Artifacts:  slices.Clone(in.Artifacts),
	}
	t.Reports = append(t.Reports, rep)

	att.FinishedAt = &now
	att.ExitCode = in.ExitCode
	att.ErrorClass = in.ErrorClass
	att.Verdict = verdict
	att.Summary = firstLinePreview(in.Message, 200)
	t.Summary = att.Summary

	if att.Machine != "" {
		if m, exists := q.machines[att.Machine]; exists && m.RunningSlots > 0 {
			m.RunningSlots--
			q.machines[att.Machine] = m
		}
	}

	outcome := strings.TrimSpace(strings.ToLower(in.Outcome))
	switch outcome {
	case StateSucceeded:
		att.State = StateSucceeded
		t.State = StateSucceeded
		t.Verdict = verdict
		t.FailureReason = ""
		t.NotBefore = nil
		t.FinishedAt = &now
	case StateCanceled, StateCancelled:
		att.State = StateCanceled
		t.State = StateCanceled
		t.Verdict = verdict
		t.NotBefore = nil
		t.FinishedAt = &now
	default:
		att.State = StateFailed
		t.LastFailedWorker = att.WorkerID
		errClass := strings.TrimSpace(in.ErrorClass)
		if errClass == "" {
			errClass = "task_failed"
		}
		shouldRetry := t.RetriesLeft > 0 && isRetryableErrorClass(errClass)
		if errClass == "rate_limited" && t.AttemptNo <= t.MaxRetries+3 {
			shouldRetry = true
		}
		if in.Retryable != nil {
			shouldRetry = *in.Retryable && (t.RetriesLeft > 0 || (errClass == "rate_limited" && t.AttemptNo <= t.MaxRetries+3))
		}
		if shouldRetry {
			if errClass != "rate_limited" || t.AttemptNo > t.MaxRetries {
				if t.RetriesLeft > 0 {
					t.RetriesLeft--
				}
			}
			delay := ComputeRetryDelay(t.ID, t.AttemptNo, q.cfg.RetryBaseDelay, q.cfg.RetryMaxDelay)
			readyAt := now.Add(delay)
			t.State = StateQueued
			t.NotBefore = &readyAt
			t.FailureReason = errClass
		} else {
			t.State = StateFailed
			t.Verdict = verdict
			t.FailureReason = errClass
			t.NotBefore = nil
			t.FinishedAt = &now
		}
	}

	if err := q.saveLocked(); err != nil {
		return nil, err
	}
	return cloneTask(t), nil
}

func isRetryableErrorClass(errClass string) bool {
	switch errClass {
	case "infra_error", "timeout", "task_failed", "rate_limited", "lost":
		return true
	default:
		return false
	}
}

// ReapExpiredLeases scans running tasks for expired worker leases and re-queues them with backoff or fails them if max_lost is reached.
func (q *Queue) ReapExpiredLeases(now time.Time) ([]*Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	_ = q.loadLocked()

	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	var reaped []*Task
	for _, t := range q.tasks {
		if (t.State != StateRunning && t.State != StateLeased) || len(t.Attempts) == 0 {
			continue
		}
		att := &t.Attempts[len(t.Attempts)-1]
		if !now.After(att.LeaseExpiresAt) {
			continue
		}
		att.State = "lost"
		att.ErrorClass = "lease_expired"
		att.FinishedAt = &now
		if att.Machine != "" {
			if m, exists := q.machines[att.Machine]; exists && m.RunningSlots > 0 {
				m.RunningSlots--
				q.machines[att.Machine] = m
			}
		}
		t.LostCount++
		t.LastFailedWorker = att.WorkerID
		if t.LostCount >= q.cfg.MaxLost {
			t.State = StateFailed
			t.FailureReason = "too_many_lost"
			t.FinishedAt = &now
			t.NotBefore = nil
		} else {
			delay := ComputeRetryDelay(t.ID, t.AttemptNo, q.cfg.RetryBaseDelay, q.cfg.RetryMaxDelay)
			readyAt := now.Add(delay)
			t.State = StateQueued
			t.FailureReason = "lease_expired"
			t.NotBefore = &readyAt
		}
		reaped = append(reaped, cloneTask(t))
	}
	if len(reaped) > 0 {
		if err := q.saveLocked(); err != nil {
			return nil, err
		}
	}
	return reaped, nil
}

// CancelTask cancels a queued or running task.
func (q *Queue) CancelTask(taskID string, now time.Time) (*Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	_ = q.loadLocked()

	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	t, ok := q.tasks[taskID]
	if !ok {
		return nil, ErrTaskNotFound
	}
	if t.IsTerminal() {
		return cloneTask(t), nil
	}
	if len(t.Attempts) > 0 {
		att := &t.Attempts[len(t.Attempts)-1]
		if att.State == StateRunning || att.State == StateLeased {
			att.State = StateCanceled
			att.FinishedAt = &now
			if att.Machine != "" {
				if m, exists := q.machines[att.Machine]; exists && m.RunningSlots > 0 {
					m.RunningSlots--
					q.machines[att.Machine] = m
				}
			}
		}
	}
	t.State = StateCanceled
	t.NotBefore = nil
	t.FinishedAt = &now
	if err := q.saveLocked(); err != nil {
		return nil, err
	}
	return cloneTask(t), nil
}

// RetryTask resets a failed or canceled task back to the queued state so it can be scheduled again.
func (q *Queue) RetryTask(taskID string, now time.Time) (*Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	_ = q.loadLocked()

	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	t, ok := q.tasks[taskID]
	if !ok {
		return nil, ErrTaskNotFound
	}
	if t.State != StateFailed && t.State != StateCanceled && t.State != StateCancelled && t.State != StateExpired {
		return nil, fmt.Errorf("only failed or canceled tasks can be retried (current state: %s)", t.State)
	}
	if q.activeQueuedCountLocked() >= q.cfg.MaxQueued {
		return nil, &AdmissionError{
			Code:       "too_many_queued",
			HTTPStatus: 429,
			Queued:     q.activeQueuedCountLocked(),
			MaxQueued:  q.cfg.MaxQueued,
			Message:    fmt.Sprintf("too_many_queued (429): queue has reached max_queued=%d", q.cfg.MaxQueued),
		}
	}
	t.State = StateQueued
	t.FailureReason = ""
	t.EnqueuedAt = now
	t.FinishedAt = nil
	t.NotBefore = nil
	if t.RetriesLeft <= 0 {
		t.RetriesLeft = 1
	}
	t.DecisionWrittenRef = ""
	t.DecisionSkipReason = ""
	if err := q.saveLocked(); err != nil {
		return nil, err
	}
	return cloneTask(t), nil
}

// GetTask returns a task by ID.
func (q *Queue) GetTask(taskID string) (*Task, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	t, ok := q.tasks[strings.TrimSpace(taskID)]
	if !ok {
		return nil, ErrTaskNotFound
	}
	return cloneTask(t), nil
}

// ListTasks returns tasks matching the filter, newest SubmittedAt first.
func (q *Queue) ListTasks(f TaskFilter) ([]*Task, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var prioFilter string
	if strings.TrimSpace(f.Priority) != "" {
		p, err := NormalizePriority(f.Priority)
		if err != nil {
			return nil, err
		}
		prioFilter = p
	}
	var diffFilter string
	if strings.TrimSpace(f.Difficulty) != "" {
		d, _, _, err := NormalizeDifficulty(f.Difficulty, "")
		if err != nil {
			return nil, err
		}
		diffFilter = d
	}
	machineFilter := strings.TrimSpace(strings.TrimPrefix(f.Machine, "machine="))

	var out []*Task
	for _, t := range q.tasks {
		if len(f.States) > 0 && !matchesStateFilter(t.State, f.States) {
			continue
		}
		if f.Pool != "" && !slices.Contains(t.Pools, f.Pool) {
			continue
		}
		if f.Source != "" && t.Source != f.Source {
			continue
		}
		if f.TodoID != "" && t.TodoID() != f.TodoID {
			continue
		}
		if prioFilter != "" && t.Priority != prioFilter {
			continue
		}
		if diffFilter != "" && t.Difficulty != diffFilter {
			continue
		}
		if machineFilter != "" && t.Machine != machineFilter {
			continue
		}
		out = append(out, cloneTask(t))
	}

	sort.Slice(out, func(i, j int) bool {
		if !out[i].SubmittedAt.Equal(out[j].SubmittedAt) {
			return out[i].SubmittedAt.After(out[j].SubmittedAt)
		}
		return out[i].ID > out[j].ID
	})

	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func matchesStateFilter(taskState string, allowed []string) bool {
	for _, s := range allowed {
		if s == taskState {
			return true
		}
		if (s == StateCanceled || s == StateCancelled) && (taskState == StateCanceled || taskState == StateCancelled) {
			return true
		}
	}
	return false
}

func firstLinePreview(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	runes := []rune(s)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return s
}
