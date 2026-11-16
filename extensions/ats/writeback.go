package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const decisionRefMaxRunes = 200

// OutcomeDecisionPayload is the body posted to POST /api/v1/todos/{id}/decisions under scope todos.write (#1235).
type OutcomeDecisionPayload struct {
	Question string `json:"question"`
	Choice   string `json:"choice"`
	Reason   string `json:"reason,omitempty"`
	Ref      string `json:"ref"`
}

// OutcomeDecisionResponse is the Hub's response to POST /api/v1/todos/{id}/decisions.
type OutcomeDecisionResponse struct {
	Inserted bool            `json:"inserted"`
	Decision json.RawMessage `json:"decision,omitempty"`
}

// DecisionWriteResult records the result of syncing one terminal task's outcome decision to the Hub.
type DecisionWriteResult struct {
	TaskID   string `json:"task_id"`
	TodoID   string `json:"todo_id"`
	Ref      string `json:"ref"`
	Choice   string `json:"choice"`
	Inserted bool   `json:"inserted"`
	Skipped  bool   `json:"skipped"`
	Reason   string `json:"reason,omitempty"`
}

// BuildDecisionRef constructs a deterministic idempotent ref for a task's terminal outcome (<= 200 runes, no '#').
func BuildDecisionRef(taskID string, attemptNo int, state, verdict string) string {
	cleanID := strings.ReplaceAll(strings.TrimSpace(taskID), "#", "_")
	cleanState := strings.ReplaceAll(strings.TrimSpace(state), "#", "_")
	cleanVerdict := strings.ReplaceAll(strings.TrimSpace(verdict), "#", "_")
	if cleanState == StateCancelled {
		cleanState = StateCanceled
	}
	prefix := fmt.Sprintf("ats:%s", cleanID)
	if attemptNo > 1 {
		prefix = fmt.Sprintf("ats:%s:%d", cleanID, attemptNo)
	}
	ref := fmt.Sprintf("%s:%s", prefix, cleanState)
	if cleanState == StateSucceeded && cleanVerdict != "" {
		ref = fmt.Sprintf("%s:%s:%s", prefix, cleanState, cleanVerdict)
	}
	if utf8.RuneCountInString(ref) > decisionRefMaxRunes {
		runes := []rune(ref)
		ref = string(runes[:decisionRefMaxRunes])
	}
	return ref
}

// decisionReasonMost bounds a decision's reason, in characters: the to-do shows it whole.
const decisionReasonMost = 1000

// BuildOutcomeDecision maps a terminal task with a persona_todo label into an OutcomeDecisionPayload. The to-do's
// reader has not seen the queue: the question names the task by its title, the choice says the outcome in plain
// words, and the reason carries what the agent said it did, what the acceptance command answered (only when it
// ran), what was left behind and the task's id.
func BuildOutcomeDecision(t *Task) (OutcomeDecisionPayload, bool) {
	if t == nil || !t.IsTerminal() || t.SupersededBy != "" {
		return OutcomeDecisionPayload{}, false
	}
	todoID := strings.TrimSpace(t.TodoID())
	if todoID == "" {
		return OutcomeDecisionPayload{}, false
	}

	question := fmt.Sprintf("ATS 任务「%s」的结果", cmpOr(firstLinePreview(t.Title, 40), t.ID))
	var choice string
	switch t.State {
	case StateSucceeded, StateCanceled, StateCancelled, StateExpired:
		choice = outcomeWords(t.State, t.Verdict, "")
	case StateFailed:
		choice = fmt.Sprintf("失败：%s（试了 %d 次）", failureWords(lastErrorClass(t), t.Verdict), max(t.AttemptNo, 1))
	default:
		return OutcomeDecisionPayload{}, false
	}

	var reasonParts []string
	if last := lastReport(t); last != nil {
		if s := oneLine(last.Summary, 400); s != "" {
			reasonParts = append(reasonParts, "Agent 的总结: "+s)
		}
		command := ""
		if t.Acceptance != nil {
			command = oneLine(t.Acceptance.Command, 200)
		}
		switch {
		case last.ExitCode != nil && command != "":
			reasonParts = append(reasonParts, fmt.Sprintf("验收: `%s` 退出码 %d", command, *last.ExitCode))
		case last.ExitCode != nil:
			reasonParts = append(reasonParts, fmt.Sprintf("验收: 退出码 %d", *last.ExitCode))
		case t.State == StateFailed:
			// The acceptance command never ran: say what stopped the task instead.
			if msg := firstLinePreview(last.Message, 300); msg != "" {
				reasonParts = append(reasonParts, "原因: "+msg)
			}
		}
		// The branch before its commit, whatever order the report lists them in.
		arts := slices.Clone(last.Artifacts)
		slices.SortStableFunc(arts, func(a, b Artifact) int { return artifactRank(a.Kind) - artifactRank(b.Kind) })
		var left []string
		for _, a := range arts {
			p := strings.TrimSpace(a.Path)
			switch {
			case p == "" || len(left) >= 6:
			case a.Kind == "branch":
				left = append(left, "分支 "+p)
			case a.Kind == "commit":
				left = append(left, "提交 "+shortSHA(p))
			case a.Kind == "worktree":
				left = append(left, "保留的工作目录 "+p)
			case a.Kind == "session":
				// The session is already attached to the to-do; its id adds nothing here.
			default:
				left = append(left, p)
			}
		}
		if len(left) > 0 {
			reasonParts = append(reasonParts, "产物: "+strings.Join(left, " · "))
		}
	} else if s := oneLine(t.Summary, 400); s != "" && t.State == StateFailed {
		reasonParts = append(reasonParts, "原因: "+s)
	}
	reasonParts = append(reasonParts, "任务 "+t.ID)

	ref := BuildDecisionRef(t.ID, t.AttemptNo, t.State, t.Verdict)
	return OutcomeDecisionPayload{
		Question: question,
		Choice:   choice,
		Reason:   truncateRunes(strings.Join(reasonParts, "；"), decisionReasonMost),
		Ref:      ref,
	}, true
}

func truncateRunes(s string, max int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}

// DecisionWriter posts idempotent outcome decisions to the Persona Hub using the `todos.write` scope.
type DecisionWriter struct {
	mu          sync.Mutex
	hubURL      string
	hubToken    string
	scopes      []string
	client      *http.Client
	writtenRefs map[string]bool
}

// NewDecisionWriter creates a DecisionWriter for the Hub.
func NewDecisionWriter(hubURL, hubToken string, scopes []string, client *http.Client) *DecisionWriter {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &DecisionWriter{
		hubURL:      strings.TrimRight(strings.TrimSpace(hubURL), "/"),
		hubToken:    strings.TrimSpace(hubToken),
		scopes:      slices.Clone(scopes),
		client:      client,
		writtenRefs: make(map[string]bool),
	}
}

// Configure updates the Hub credentials and granted scopes.
func (w *DecisionWriter) Configure(hubURL, hubToken string, scopes []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.hubURL = strings.TrimRight(strings.TrimSpace(hubURL), "/")
	w.hubToken = strings.TrimSpace(hubToken)
	w.scopes = slices.Clone(scopes)
}

// HasTodosWriteScope reports whether the Hub granted `todos.write` (or legacy full token when scopes list is empty).
func (w *DecisionWriter) HasTodosWriteScope() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.hubURL == "" || w.hubToken == "" {
		return false
	}
	if len(w.scopes) == 0 {
		return true
	}
	return slices.Contains(w.scopes, "todos.write")
}

// SyncQueueOutcomes scans terminal tasks in the queue that carry a `persona_todo` label and writes any unwritten outcome decisions to the Hub.
func (w *DecisionWriter) SyncQueueOutcomes(ctx context.Context, q *Queue) ([]DecisionWriteResult, error) {
	if q == nil {
		return nil, nil
	}
	tasks, err := q.ListTasks(TaskFilter{Limit: 100})
	if err != nil {
		return nil, err
	}

	var results []DecisionWriteResult
	var firstErr error
	for _, t := range tasks {
		if !t.IsTerminal() || t.SupersededBy != "" || t.TodoID() == "" {
			continue
		}
		payload, ok := BuildOutcomeDecision(t)
		if !ok {
			continue
		}
		if t.DecisionWrittenRef == payload.Ref || t.DecisionSkipReason != "" {
			continue
		}

		res, err := w.WriteTaskOutcome(ctx, q, t.ID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		results = append(results, res)
	}
	return results, firstErr
}

// WriteTaskOutcome posts a single task's outcome decision to the Hub idempotently and records the ref in the queue.
func (w *DecisionWriter) WriteTaskOutcome(ctx context.Context, q *Queue, taskID string) (DecisionWriteResult, error) {
	t, err := q.GetTask(taskID)
	if err != nil {
		return DecisionWriteResult{}, err
	}
	todoID := t.TodoID()
	payload, ok := BuildOutcomeDecision(t)
	if !ok {
		return DecisionWriteResult{
			TaskID:  t.ID,
			TodoID:  todoID,
			Skipped: true,
			Reason:  "not_terminal_or_no_todo",
		}, nil
	}

	w.mu.Lock()
	alreadyInMem := w.writtenRefs[payload.Ref]
	hubURL := w.hubURL
	hubToken := w.hubToken
	scopes := slices.Clone(w.scopes)
	client := w.client
	w.mu.Unlock()

	if t.DecisionWrittenRef == payload.Ref || alreadyInMem {
		return DecisionWriteResult{
			TaskID:   t.ID,
			TodoID:   todoID,
			Ref:      payload.Ref,
			Choice:   payload.Choice,
			Inserted: false,
			Skipped:  true,
			Reason:   "already_written",
		}, nil
	}
	if t.DecisionSkipReason != "" {
		return DecisionWriteResult{
			TaskID:  t.ID,
			TodoID:  todoID,
			Ref:     payload.Ref,
			Choice:  payload.Choice,
			Skipped: true,
			Reason:  t.DecisionSkipReason,
		}, nil
	}

	if hubURL == "" || hubToken == "" || (len(scopes) > 0 && !slices.Contains(scopes, "todos.write")) {
		return DecisionWriteResult{
			TaskID:  t.ID,
			TodoID:  todoID,
			Ref:     payload.Ref,
			Choice:  payload.Choice,
			Skipped: true,
			Reason:  "missing_todos_write_scope_or_hub_token",
		}, nil
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return DecisionWriteResult{}, err
	}
	endpoint := fmt.Sprintf("%s/api/v1/todos/%s/decisions", hubURL, url.PathEscape(todoID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return DecisionWriteResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+hubToken)

	resp, err := client.Do(req)
	if err != nil {
		return DecisionWriteResult{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	// 404 or 403: mark skipped so we never retry in a loop.
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		skipReason := fmt.Sprintf("http_%d", resp.StatusCode)
		_ = q.markDecisionSkipped(t.ID, skipReason)
		w.mu.Lock()
		w.writtenRefs[payload.Ref] = true
		w.mu.Unlock()
		return DecisionWriteResult{
			TaskID:  t.ID,
			TodoID:  todoID,
			Ref:     payload.Ref,
			Choice:  payload.Choice,
			Skipped: true,
			Reason:  skipReason,
		}, nil
	}

	if resp.StatusCode/100 != 2 {
		return DecisionWriteResult{}, fmt.Errorf("POST /api/v1/todos/%s/decisions returned HTTP %d: %s", todoID, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var out OutcomeDecisionResponse
	_ = json.Unmarshal(respBody, &out)

	_ = q.markDecisionWritten(t.ID, payload.Ref)
	w.mu.Lock()
	w.writtenRefs[payload.Ref] = true
	w.mu.Unlock()

	return DecisionWriteResult{
		TaskID:   t.ID,
		TodoID:   todoID,
		Ref:      payload.Ref,
		Choice:   payload.Choice,
		Inserted: out.Inserted,
	}, nil
}

func (q *Queue) markDecisionWritten(taskID, ref string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	t.DecisionWrittenRef = ref
	return q.saveLocked()
}

func (q *Queue) markDecisionSkipped(taskID, reason string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	t.DecisionSkipReason = reason
	return q.saveLocked()
}

// artifactRank orders what an attempt left behind for the to-do's line: branch, commit, then the rest.
func artifactRank(kind string) int {
	switch kind {
	case "branch":
		return 0
	case "commit":
		return 1
	}
	return 2
}
