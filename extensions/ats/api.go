package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The extension's HTTP API, which the hub serves at /api/v1/extensions/<id>/…:
//
//	GET  /tasks?state=&todo_id=&priority=&limit=   the queue, newest first, with what the scheduler is doing
//	GET  /tasks/{id}                               one task
//	GET  /tasks/{id}/evidence                      what shows whether the task is really done
//	POST /tasks                                    queue a task for a to-do
//
// The assistant reaches it through the gated command of assistant/gate.json: reads pass, POST /tasks asks the
// user every time, everything else is refused before it gets here.

var todoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,120}$`)

const (
	maxPromptBytes     = 32 << 10
	maxAcceptanceBytes = 2 << 10
	maxBodyBytes       = 128 << 10
)

type apiError struct {
	Status int    `json:"-"`
	Code   string `json:"code"`
	Msg    string `json:"error"`
}

func (e *apiError) Error() string { return e.Msg }

func badRequest(code, format string, args ...any) *apiError {
	return &apiError{Status: http.StatusBadRequest, Code: code, Msg: fmt.Sprintf(format, args...)}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	var ae *apiError
	var adm *AdmissionError
	switch {
	case errors.As(err, &ae):
		writeJSON(w, ae.Status, ae)
	case errors.As(err, &adm):
		writeJSON(w, http.StatusTooManyRequests, &apiError{Code: adm.Code, Msg: adm.Message})
	case errors.Is(err, ErrTaskNotFound):
		writeJSON(w, http.StatusNotFound, &apiError{Code: "not_found", Msg: "no such task"})
	default:
		writeJSON(w, http.StatusBadRequest, &apiError{Code: "invalid", Msg: err.Error()})
	}
}

func (a *atsApp) q() *Queue {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.queue
}

func (a *atsApp) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tasks", a.listTasks)
	mux.HandleFunc("GET /tasks/{id}", a.getTask)
	mux.HandleFunc("GET /tasks/{id}/evidence", a.getEvidence)
	mux.HandleFunc("POST /tasks", a.postTask)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.q() == nil {
			writeJSON(w, http.StatusServiceUnavailable, &apiError{Code: "not_ready", Msg: "the queue is not ready"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// taskView is a task as the API shows it.
type taskView struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	State         string     `json:"state"`
	Verdict       string     `json:"verdict"`
	FailureReason string     `json:"failure_reason,omitempty"`
	TodoID        string     `json:"todo_id,omitempty"`
	Priority      string     `json:"priority"`
	Difficulty    string     `json:"difficulty"`
	ModelTier     string     `json:"model_tier"`
	Machine       string     `json:"machine,omitempty"`
	Permission    string     `json:"permission_profile"`
	Repo          string     `json:"repo"`
	Ref           string     `json:"ref"`
	Acceptance    string     `json:"acceptance,omitempty"`
	AttemptNo     int        `json:"attempt_no"`
	RetriesLeft   int        `json:"retries_left"`
	Phase         string     `json:"phase,omitempty"`
	RunsOn        string     `json:"runs_on,omitempty"`
	SessionID     string     `json:"session_id,omitempty"`
	Waiting       string     `json:"waiting,omitempty"`
	NotBefore     *time.Time `json:"not_before,omitempty"`
	SubmittedAt   time.Time  `json:"submitted_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Prompt        string     `json:"prompt,omitempty"`
}

func viewTask(t *Task, withPrompt bool) taskView {
	v := taskView{
		ID: t.ID, Title: t.Title, State: t.State, Verdict: t.Verdict, FailureReason: t.FailureReason, TodoID: t.TodoID(),
		Priority: t.Priority, Difficulty: t.Difficulty, ModelTier: t.ModelTier, Machine: t.Machine,
		Permission: t.PermissionProfile, Repo: t.Workspace.Repo, Ref: t.Workspace.BaseSHA,
		AttemptNo: t.AttemptNo, RetriesLeft: t.RetriesLeft, NotBefore: t.NotBefore, SubmittedAt: t.SubmittedAt, FinishedAt: t.FinishedAt,
	}
	if t.Acceptance != nil {
		v.Acceptance = t.Acceptance.Command
	}
	if e := t.Exec; e != nil {
		v.Phase, v.RunsOn, v.SessionID, v.Waiting = e.Phase, e.MachineID, e.SessionID, e.Waiting
	}
	if withPrompt {
		v.Prompt = t.Prompt
	}
	return v
}

func (a *atsApp) listTasks(w http.ResponseWriter, r *http.Request) {
	q := a.q()
	query := r.URL.Query()
	f := TaskFilter{TodoID: strings.TrimSpace(query.Get("todo_id")), Priority: query.Get("priority"), Limit: 20}
	if s := strings.TrimSpace(query.Get("state")); s != "" {
		for _, st := range strings.Split(s, ",") {
			st = strings.TrimSpace(st)
			if !matchesStateFilter(st, AllTaskStates) {
				writeError(w, badRequest("invalid_state", "state must be one of %s", strings.Join(AllTaskStates, ", ")))
				return
			}
			f.States = append(f.States, st)
		}
	}
	if s := query.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 200 {
			writeError(w, badRequest("invalid_limit", "limit must be 1 to 200"))
			return
		}
		f.Limit = n
	}
	tasks, err := q.ListTasks(f)
	if err != nil {
		writeError(w, err)
		return
	}
	views := make([]taskView, 0, len(tasks))
	for _, t := range tasks {
		views = append(views, viewTask(t, false))
	}
	plan := q.PlanSchedule(a.now())
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks": views,
		"scheduler": map[string]any{
			"admission":           plan.AdmissionMode,
			"admission_label":     plan.AdmissionLabel,
			"quota_remaining_pct": plan.Quota.RemainingPct,
			"counts_by_state":     plan.CountsByState,
			"queued_by_priority":  plan.QueuedByPriority,
			"computers":           len(plan.Machines),
		},
	})
}

func (a *atsApp) getTask(w http.ResponseWriter, r *http.Request) {
	t, err := a.q().GetTask(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": viewTask(t, true), "attempts": t.Attempts})
}

// getEvidence answers "is it really done": the state and verdict, the acceptance command, the last report (exit
// code, output tail, what the attempt left behind) and whether the outcome was written to the to-do.
func (a *atsApp) getEvidence(w http.ResponseWriter, r *http.Request) {
	t, err := a.q().GetTask(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	out := map[string]any{
		"task_id": t.ID, "todo_id": t.TodoID(), "state": t.State, "verdict": t.Verdict, "failure_reason": t.FailureReason,
		"attempts": len(t.Attempts), "verified": t.State == StateSucceeded && t.Verdict == VerdictPass,
	}
	if t.Acceptance != nil {
		out["acceptance"] = t.Acceptance.Command
	}
	if n := len(t.Reports); n > 0 {
		out["last_report"] = t.Reports[n-1]
		out["reports"] = n
	}
	if t.DecisionWrittenRef != "" {
		out["decision_ref"] = t.DecisionWrittenRef
	} else if t.DecisionSkipReason != "" {
		out["decision_skipped"] = t.DecisionSkipReason
	}
	writeJSON(w, http.StatusOK, out)
}

type enqueueRequest struct {
	TodoID            string            `json:"todo_id"`
	Title             string            `json:"title"`
	Prompt            string            `json:"prompt"`
	Repo              string            `json:"repo"`
	Ref               string            `json:"ref"`
	Priority          string            `json:"priority"`
	Difficulty        string            `json:"difficulty"`
	QoS               string            `json:"qos"`
	Pool              string            `json:"pool"`
	Machine           string            `json:"machine"`
	Acceptance        string            `json:"acceptance"`
	PermissionProfile string            `json:"permission_profile"`
	Tags              []string          `json:"tags"`
	Labels            map[string]string `json:"labels"`
	IdempotencyKey    string            `json:"idempotency_key"`
}

func (a *atsApp) postTask(w http.ResponseWriter, r *http.Request) {
	// Who may queue: the owner's apps, and the assistant's gated command (which asked the user first). Other
	// callers the hub lets through to an extension's API (an answer-only token, a share) may read.
	switch r.Header.Get("X-Persona-Role") {
	case "", "full", "extension":
	default:
		writeJSON(w, http.StatusForbidden, &apiError{Code: "forbidden", Msg: "this caller may not queue tasks"})
		return
	}
	var req enqueueRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, badRequest("invalid_body", "the body must be a JSON object of the task's fields: %v", err))
		return
	}
	in, err := a.enqueueInput(req)
	if err != nil {
		writeError(w, err)
		return
	}
	res, err := a.q().Enqueue(in)
	if err != nil {
		writeError(w, err)
		return
	}
	a.mu.Lock()
	d := a.disp
	a.mu.Unlock()
	if d != nil {
		d.poke()
	}
	status := http.StatusCreated
	if !res.Created {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"created": res.Created, "task": viewTask(res.Task, false)})
}

// enqueueInput checks a request to queue a task. A task always belongs to a to-do, runs in a repository under a
// directory the settings allow, and is read-only unless it says otherwise.
func (a *atsApp) enqueueInput(req enqueueRequest) (EnqueueInput, error) {
	todo := strings.TrimSpace(req.TodoID)
	if !todoIDPattern.MatchString(todo) {
		return EnqueueInput{}, badRequest("invalid_todo_id", "todo_id is required: the id of the to-do the task is for")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return EnqueueInput{}, badRequest("invalid_prompt", "prompt is required: what the agent is to do")
	}
	if len(req.Prompt) > maxPromptBytes {
		return EnqueueInput{}, badRequest("invalid_prompt", "prompt is longer than %d bytes", maxPromptBytes)
	}
	if len(req.Acceptance) > maxAcceptanceBytes || strings.ContainsAny(req.Acceptance, "\x00") {
		return EnqueueInput{}, badRequest("invalid_acceptance", "acceptance must be one shell command of at most %d bytes", maxAcceptanceBytes)
	}
	repo, err := checkRepo(req.Repo, a.config().Repos)
	if err != nil {
		return EnqueueInput{}, err
	}
	ref := strings.TrimSpace(req.Ref)
	if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, " \t\n\x00") || len(ref) > 200 {
		return EnqueueInput{}, badRequest("invalid_ref", "ref must be a branch, tag or commit")
	}
	switch strings.TrimSpace(req.QoS) {
	case "", "expedited", "standard", "bulk":
	default:
		return EnqueueInput{}, badRequest("invalid_qos", "qos must be expedited, standard or bulk")
	}
	for k := range req.Labels {
		if k == PersonaTodoLabel {
			return EnqueueInput{}, badRequest("invalid_labels", "the %s label is set from todo_id", PersonaTodoLabel)
		}
	}
	for _, tag := range req.Tags {
		if k, _, _ := strings.Cut(tag, "="); strings.TrimSpace(k) == PersonaTodoLabel {
			return EnqueueInput{}, badRequest("invalid_tags", "the %s label is set from todo_id", PersonaTodoLabel)
		}
	}
	return EnqueueInput{
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey), Title: strings.TrimSpace(req.Title), Prompt: req.Prompt,
		Repo: repo, BaseSHA: ref, QoS: strings.TrimSpace(req.QoS), Pool: strings.TrimSpace(req.Pool),
		Priority: req.Priority, Difficulty: req.Difficulty, Machine: req.Machine,
		AcceptanceCommand: req.Acceptance, PermissionProfile: req.PermissionProfile, Labels: req.Labels, Tags: req.Tags,
		Source:      AssistantSource,
		ExternalRef: &ExternalRef{System: AssistantSource, ID: PersonaSystemID, Conversation: todo},
		Now:         a.now(),
	}, nil
}

// checkRepo is the hub program's part of the repository allowlist: the path as written must be under one of the
// allowed directories as written. The program on the computer checks again with the real paths (symlinks, the
// home directory), and its answer is the one that counts; this one refuses the plainly wrong early, with a
// message the assistant can show.
func checkRepo(raw string, allowed []string) (string, error) {
	repo := strings.TrimSpace(raw)
	if repo == "" || !(strings.HasPrefix(repo, "/") || strings.HasPrefix(repo, "~/")) || strings.ContainsRune(repo, 0) {
		return "", badRequest("invalid_repo", "repo is required: the repository's absolute path on the computer (or ~/…)")
	}
	for _, seg := range strings.Split(repo, "/") {
		if seg == ".." {
			return "", badRequest("invalid_repo", "repo must not contain ..")
		}
	}
	repo = path.Clean(repo)
	if len(allowed) == 0 {
		return "", &apiError{Status: http.StatusForbidden, Code: "repos_not_configured",
			Msg: "no repository directory is allowed yet: the user adds the directories tasks may run in to the extension's settings (repos)"}
	}
	home := strings.HasPrefix(repo, "~/")
	undecided := false
	for _, root := range allowed {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		root = path.Clean(root)
		if strings.HasPrefix(root, "~") != home {
			undecided = true // one is written from the home directory, the other is not: only the computer can tell
			continue
		}
		if repo == root || strings.HasPrefix(repo, strings.TrimRight(root, "/")+"/") {
			return repo, nil
		}
	}
	if undecided {
		return repo, nil
	}
	return "", &apiError{Status: http.StatusForbidden, Code: "repo_not_allowed",
		Msg: "the repository is not under a directory in the extension's settings (repos)"}
}
