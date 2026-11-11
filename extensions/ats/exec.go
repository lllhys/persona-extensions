package main

import (
	"sort"
	"time"
)

// What the dispatcher needs from the queue beyond leasing and completing: the execution record of a running
// attempt, leases that survive a restart, and pruning of old finished tasks.

// UpdateExec changes the execution record of the task's current attempt (fence), and with it the task's state
// between running and needs_input. It fails with ErrStaleAttempt when the attempt is no longer the current one.
func (q *Queue) UpdateExec(taskID string, fence int, now time.Time, fn func(e *Execution)) (*Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.tasks[taskID]
	if !ok {
		return nil, ErrTaskNotFound
	}
	if !isActiveState(t.State) || t.FenceToken != fence || len(t.Attempts) == 0 {
		return nil, ErrStaleAttempt
	}
	if t.Exec == nil {
		t.Exec = &Execution{PhaseSince: now.UTC()}
	}
	phase := t.Exec.Phase
	fn(t.Exec)
	if t.Exec.Phase != phase {
		t.Exec.PhaseSince = now.UTC()
	}
	state := StateRunning
	if t.Exec.Waiting != "" {
		state = StateNeedsInput
	}
	t.State = state
	t.Attempts[len(t.Attempts)-1].State = state
	if err := q.saveLocked(); err != nil {
		return nil, err
	}
	return cloneTask(t), nil
}

// RenewLeases extends the lease of every active attempt. The dispatcher is the only lease holder, so it does this
// when it starts (an attempt whose session outlived a restart is picked up again, not run twice) and on every
// round.
func (q *Queue) RenewLeases(now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	changed := false
	for _, t := range q.tasks {
		if !isActiveState(t.State) || len(t.Attempts) == 0 {
			continue
		}
		t.Attempts[len(t.Attempts)-1].LeaseExpiresAt = now.UTC().Add(q.cfg.LeaseTTL)
		changed = true
	}
	if !changed {
		return nil
	}
	return q.saveLocked()
}

// ActiveTasks returns the tasks that hold a lease, oldest first.
func (q *Queue) ActiveTasks() []*Task {
	q.mu.RLock()
	defer q.mu.RUnlock()
	var out []*Task
	for _, t := range q.tasks {
		if isActiveState(t.State) {
			out = append(out, cloneTask(t))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Prune drops finished tasks older than maxAge, and the oldest finished ones beyond keep. A finished task whose
// outcome still waits to be written to its to-do stays.
func (q *Queue) Prune(now time.Time, maxAge time.Duration, keep int) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var done []*Task
	for _, t := range q.tasks {
		if t.IsTerminal() && t.FinishedAt != nil && (t.TodoID() == "" || t.DecisionWrittenRef != "" || t.DecisionSkipReason != "") {
			done = append(done, t)
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].FinishedAt.After(*done[j].FinishedAt) })
	dropped := 0
	for i, t := range done {
		if i >= keep || now.Sub(*t.FinishedAt) > maxAge {
			delete(q.tasks, t.ID)
			dropped++
		}
	}
	if dropped == 0 {
		return 0, nil
	}
	return dropped, q.saveLocked()
}
