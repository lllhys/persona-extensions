package main

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// Admission modes for the offline-compute-style multi-factor scheduler.
const (
	AdmissionFull         = "full"
	AdmissionPriorityOnly = "priority_only"
	AdmissionPausedQuota  = "paused_quota"
)

// MachineSlot represents an execution node's online state and slot capacity.
type MachineSlot struct {
	Name         string   `json:"name"`            // the computer's id on the hub
	Title        string   `json:"title,omitempty"` // its display name
	Online       bool     `json:"online"`
	MaxSlots     int      `json:"max_slots"`
	RunningSlots int      `json:"running_slots"`
	Labels       []string `json:"labels,omitempty"`
}

// FreeSlots returns the number of unoccupied slots on an online machine.
func (m MachineSlot) FreeSlots() int {
	if !m.Online || m.MaxSlots <= m.RunningSlots {
		return 0
	}
	return m.MaxSlots - m.RunningSlots
}

// LoadRatio returns the current utilization ratio [0.0, 1.0].
func (m MachineSlot) LoadRatio() float64 {
	if m.MaxSlots <= 0 {
		return 1.0
	}
	r := float64(m.RunningSlots) / float64(m.MaxSlots)
	if r < 0 {
		return 0
	}
	if r > 1 {
		return 1
	}
	return r
}

// ScheduledCandidate represents one task evaluated by the multi-factor scheduler.
type ScheduledCandidate struct {
	TaskID        string  `json:"task_id"`
	Title         string  `json:"title"`
	Priority      string  `json:"priority"`
	Difficulty    string  `json:"difficulty"`
	RequestedTier string  `json:"requested_tier"`
	EffectiveTier string  `json:"effective_tier"`
	TargetMachine string  `json:"target_machine,omitempty"`
	Score         float64 `json:"score"`
	PriorityScore float64 `json:"priority_score"`
	AgingBoost    float64 `json:"aging_boost"`
	QuotaFitBonus float64 `json:"quota_fit_bonus"`
	WaitMinutes   float64 `json:"wait_minutes"`
	Admitted      bool    `json:"admitted"`
	HoldReason    string  `json:"hold_reason,omitempty"`
}

// SchedulePlan summarizes the current queue state, quota admission mode, machine slots, and dispatch decisions.
type SchedulePlan struct {
	AdmissionMode      string               `json:"admission_mode"`
	AdmissionLabel     string               `json:"admission_label"`
	ReserveHeadroomPct float64              `json:"reserve_headroom_pct"`
	Quota              QuotaStatus          `json:"quota"`
	Machines           []MachineSlot        `json:"machines"`
	QueuedByPriority   map[string]int       `json:"queued_by_priority"`
	QueuedByDifficulty map[string]int       `json:"queued_by_difficulty"`
	CountsByState      map[string]int       `json:"counts_by_state"`
	AdmittedCandidates []ScheduledCandidate `json:"admitted_candidates"`
	DeferredCandidates []ScheduledCandidate `json:"deferred_candidates"`
	CoolingRetryCount  int                  `json:"cooling_retry_count"`
}

// SetMachines updates the registered machine slot table in the queue.
func (q *Queue) SetMachines(machines []MachineSlot) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.machines = make(map[string]MachineSlot, len(machines))
	for _, m := range machines {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			continue
		}
		if m.MaxSlots <= 0 {
			m.MaxSlots = 1
		}
		m.Name = name
		q.machines[name] = m
	}
	return q.saveLocked()
}

// Machines returns a sorted snapshot of the registered machines, with the slots the active attempts occupy.
func (q *Queue) Machines() []MachineSlot {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.machinesLocked()
}

func (q *Queue) machinesLocked() []MachineSlot {
	running := map[string]int{}
	for _, t := range q.tasks {
		if isActiveState(t.State) && len(t.Attempts) > 0 {
			running[t.Attempts[len(t.Attempts)-1].Machine]++
		}
	}
	out := make([]MachineSlot, 0, len(q.machines))
	for _, m := range q.machines {
		m.RunningSlots = running[m.Name]
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// AdmissionModeLabel returns the human-readable Chinese label for an admission mode.
func AdmissionModeLabel(mode string) string {
	switch mode {
	case AdmissionFull:
		return "全量放行"
	case AdmissionPriorityOnly:
		return "仅 P0-P1 保底"
	case AdmissionPausedQuota:
		return "额度冷却暂停"
	default:
		return mode
	}
}

func priorityBaseScore(priority string) float64 {
	switch priority {
	case PriorityP0:
		return 100
	case PriorityP1:
		return 75
	case PriorityP2:
		return 50
	case PriorityP3:
		return 25
	default:
		return 50
	}
}

// PlanSchedule computes the offline-compute-style multi-factor schedule plan without mutating task state.
func (q *Queue) PlanSchedule(now time.Time) SchedulePlan {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.planScheduleLocked(now)
}

func (q *Queue) planScheduleLocked(now time.Time) SchedulePlan {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	reserve := q.cfg.ReserveHeadroomPct
	if reserve <= 0 {
		reserve = 30
	}
	quota := q.quota
	if quota.Provider == "" {
		quota = DefaultQuotaStatus()
	}
	machines := q.machinesLocked()

	mode := determineAdmissionMode(quota, reserve, now)
	queuedByPrio := map[string]int{
		PriorityP0: 0,
		PriorityP1: 0,
		PriorityP2: 0,
		PriorityP3: 0,
	}
	queuedByDiff := map[string]int{
		DifficultyEasy:    0,
		DifficultyMedium:  0,
		DifficultyHard:    0,
		DifficultyExtreme: 0,
	}
	countsByState := map[string]int{
		StateQueued:     0,
		StateRunning:    0,
		StateNeedsInput: 0,
		StateSucceeded:  0,
		StateFailed:     0,
		StateCanceled:   0,
	}

	var queuedTasks []*Task
	coolingCount := 0
	for _, t := range q.tasks {
		if t.SupersededBy != "" {
			continue
		}
		switch t.State {
		case StateQueued:
			countsByState[StateQueued]++
			queuedByPrio[t.Priority]++
			queuedByDiff[t.Difficulty]++
			if t.NotBefore != nil && now.Before(*t.NotBefore) {
				coolingCount++
				continue
			}
			queuedTasks = append(queuedTasks, t)
		case StateRunning, StateLeased:
			countsByState[StateRunning]++
		case StateNeedsInput:
			countsByState[StateNeedsInput]++
		case StateSucceeded:
			countsByState[StateSucceeded]++
		case StateFailed:
			countsByState[StateFailed]++
		case StateCanceled, StateCancelled, StateExpired:
			countsByState[StateCanceled]++
		}
	}

	// Score every ready queued task using priority + anti-starvation aging + model tier quota fit.
	type scoredTask struct {
		task *Task
		cand ScheduledCandidate
	}
	scored := make([]scoredTask, 0, len(queuedTasks))
	for _, t := range queuedTasks {
		waitMin := math.Max(0, now.Sub(t.EnqueuedAt).Minutes())
		pScore := priorityBaseScore(t.Priority)
		// Aging: +0.5 score per minute waited, capped at +60 so long-waiting tasks gradually catch up.
		aging := math.Min(60.0, waitMin*0.5)
		tierRem := tierRemaining(quota, t.ModelTier)
		weight := t.QuotaWeight
		if weight <= 0 {
			weight = 2.5
		}
		quotaBonus := (tierRem / 100.0) * (20.0 / weight)
		total := math.Round((pScore+aging+quotaBonus)*100) / 100

		scored = append(scored, scoredTask{
			task: t,
			cand: ScheduledCandidate{
				TaskID:        t.ID,
				Title:         t.Title,
				Priority:      t.Priority,
				Difficulty:    t.Difficulty,
				RequestedTier: t.ModelTier,
				EffectiveTier: t.ModelTier,
				Score:         total,
				PriorityScore: pScore,
				AgingBoost:    math.Round(aging*100) / 100,
				QuotaFitBonus: math.Round(quotaBonus*100) / 100,
				WaitMinutes:   math.Round(waitMin*10) / 10,
			},
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].cand.Score != scored[j].cand.Score {
			return scored[i].cand.Score > scored[j].cand.Score
		}
		if !scored[i].task.EnqueuedAt.Equal(scored[j].task.EnqueuedAt) {
			return scored[i].task.EnqueuedAt.Before(scored[j].task.EnqueuedAt)
		}
		return scored[i].task.ID < scored[j].task.ID
	})

	simMachines := slices.Clone(machines)
	var admitted, deferred []ScheduledCandidate

	for _, item := range scored {
		t := item.task
		c := item.cand

		if mode == AdmissionPausedQuota {
			c.Admitted = false
			if quota.ResetAt != nil {
				c.HoldReason = fmt.Sprintf("quota_cooldown_until_%s", quota.ResetAt.Format(time.RFC3339))
			} else {
				c.HoldReason = "quota_exhausted"
			}
			deferred = append(deferred, c)
			continue
		}

		// Evaluate priority + difficulty + model tier quota headroom & fallback.
		effTier, okTier, holdWhy := evaluateTierAdmission(t, c.WaitMinutes, mode, quota, reserve)
		if !okTier {
			c.Admitted = false
			c.HoldReason = holdWhy
			deferred = append(deferred, c)
			continue
		}
		c.EffectiveTier = effTier

		// Select best online machine matching affinity with lowest load ratio and anti-affinity to LastFailedWorker.
		mIdx, mWhy := pickMachineSlot(simMachines, t.Machine, t.LastFailedWorker)
		if mIdx < 0 {
			c.Admitted = false
			c.HoldReason = mWhy
			deferred = append(deferred, c)
			continue
		}

		c.Admitted = true
		c.TargetMachine = simMachines[mIdx].Name
		simMachines[mIdx].RunningSlots++
		admitted = append(admitted, c)
	}

	return SchedulePlan{
		AdmissionMode:      mode,
		AdmissionLabel:     AdmissionModeLabel(mode),
		ReserveHeadroomPct: reserve,
		Quota:              quota,
		Machines:           machines,
		QueuedByPriority:   queuedByPrio,
		QueuedByDifficulty: queuedByDiff,
		CountsByState:      countsByState,
		AdmittedCandidates: admitted,
		DeferredCandidates: deferred,
		CoolingRetryCount:  coolingCount,
	}
}

func determineAdmissionMode(quota QuotaStatus, reservePct float64, now time.Time) string {
	if quota.Exhausted || quota.RemainingPct <= 5.0 {
		return AdmissionPausedQuota
	}
	if quota.CooldownUntil != nil && quota.CooldownUntil.After(now) {
		return AdmissionPausedQuota
	}
	if quota.RemainingPct < reservePct {
		return AdmissionPriorityOnly
	}
	return AdmissionFull
}

func tierRemaining(quota QuotaStatus, tier string) float64 {
	if quota.TierRemainingPct != nil {
		if v, ok := quota.TierRemainingPct[tier]; ok {
			return v
		}
	}
	return quota.RemainingPct
}

func evaluateTierAdmission(t *Task, waitMinutes float64, mode string, quota QuotaStatus, reservePct float64) (effectiveTier string, allowed bool, holdReason string) {
	reqTier := t.ModelTier
	if reqTier == "" {
		reqTier = TierStandard
	}
	reqRem := tierRemaining(quota, reqTier)

	// Aging promotion: a P2 task waiting >= 120 minutes is treated as P1 for quota admission.
	effPriority := t.Priority
	if effPriority == PriorityP2 && waitMinutes >= 120 {
		effPriority = PriorityP1
	} else if effPriority == PriorityP3 && waitMinutes >= 240 {
		effPriority = PriorityP2
	}

	if mode == AdmissionPriorityOnly {
		// In priority_only mode, hold P2/P3 tasks unless their target tier is fast and has abundant headroom (>= reservePct*1.5).
		if effPriority != PriorityP0 && effPriority != PriorityP1 {
			if reqTier == TierFast && reqRem >= math.Min(90, reservePct*1.5) {
				return TierFast, true, ""
			}
			return reqTier, false, "held_for_p0_p1_headroom_reserve"
		}
		// High-difficulty tasks in priority_only mode: P0 can use remaining strong quota (>5%); P1 requires strong tier >= 15% or falls back to standard if medium/hard allows.
		if (t.Difficulty == DifficultyExtreme || t.Difficulty == DifficultyHard) && reqRem < 15.0 && effPriority != PriorityP0 {
			if stdRem := tierRemaining(quota, TierStandard); stdRem >= reservePct && t.Difficulty == DifficultyHard {
				return TierStandard, true, ""
			}
			return reqTier, false, "insufficient_strong_tier_quota"
		}
	}

	// Check tier-specific quota even in full mode: if strong tier is low (< reservePct), hold P2/P3 hard/extreme tasks or route medium/easy tasks to fast/standard.
	if reqRem <= 5.0 {
		// Try fallback tier for non-extreme tasks if a lighter tier has ample headroom.
		if reqTier == TierStrong && t.Difficulty == DifficultyHard && tierRemaining(quota, TierStandard) >= reservePct {
			return TierStandard, true, ""
		}
		if reqTier == TierStandard && t.Difficulty == DifficultyEasy && tierRemaining(quota, TierFast) >= reservePct {
			return TierFast, true, ""
		}
		return reqTier, false, fmt.Sprintf("tier_%s_quota_exhausted", reqTier)
	}
	if reqTier == TierStrong && reqRem < reservePct && effPriority != PriorityP0 && effPriority != PriorityP1 {
		return reqTier, false, "strong_tier_reserved_for_high_priority"
	}

	return reqTier, true, ""
}

func pickMachineSlot(machines []MachineSlot, requiredMachine, lastFailedWorker string) (int, string) {
	reqName := strings.TrimSpace(requiredMachine)
	bestIdx := -1
	bestPenalty := math.MaxFloat64

	foundMachine := false
	foundOnline := false

	for i, m := range machines {
		if reqName != "" && !strings.EqualFold(m.Name, reqName) && !strings.EqualFold(m.Title, reqName) {
			continue
		}
		foundMachine = true
		if !m.Online {
			continue
		}
		foundOnline = true
		if m.FreeSlots() <= 0 {
			continue
		}
		// Penalty is load ratio [0, 1) plus a soft anti-affinity penalty (+0.35) if this machine was the last failed worker.
		penalty := m.LoadRatio()
		if lastFailedWorker != "" && (strings.EqualFold(m.Name, lastFailedWorker) || strings.HasPrefix(strings.ToLower(lastFailedWorker), strings.ToLower(m.Name)+":")) {
			penalty += 0.35
		}
		if penalty < bestPenalty {
			bestPenalty = penalty
			bestIdx = i
		}
	}

	if bestIdx >= 0 {
		return bestIdx, ""
	}
	if reqName != "" && !foundMachine {
		return -1, fmt.Sprintf("machine_%s_not_registered", reqName)
	}
	if reqName != "" && !foundOnline {
		return -1, fmt.Sprintf("machine_%s_offline", reqName)
	}
	if !foundOnline {
		return -1, "no_online_machine"
	}
	return -1, "no_free_machine_slots"
}

// DispatchNext plans and leases up to maxCount ready tasks according to the multi-factor schedule plan.
func (q *Queue) DispatchNext(maxCount int, now time.Time) ([]*Task, []*Attempt, SchedulePlan, error) {
	if maxCount <= 0 {
		maxCount = 1
	}
	plan := q.PlanSchedule(now)
	if plan.AdmissionMode == AdmissionPausedQuota || len(plan.AdmittedCandidates) == 0 {
		return nil, nil, plan, nil
	}

	var leasedTasks []*Task
	var leasedAttempts []*Attempt
	for _, cand := range plan.AdmittedCandidates {
		if len(leasedTasks) >= maxCount {
			break
		}
		workerID := fmt.Sprintf("%s:worker-%s", cand.TargetMachine, cand.EffectiveTier)
		t, att, err := q.LeaseTask(cand.TaskID, workerID, cand.TargetMachine, now)
		if err != nil {
			continue
		}
		leasedTasks = append(leasedTasks, t)
		leasedAttempts = append(leasedAttempts, att)
	}
	updatedPlan := q.PlanSchedule(now)
	return leasedTasks, leasedAttempts, updatedPlan, nil
}
