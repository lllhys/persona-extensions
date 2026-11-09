package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const (
	MCPProtocolVersion = "2025-06-18"
	MaxListTasks       = 100
	MaxTodoIDLen       = 128
	PromptPreviewLimit = 400
)

var todoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// MCPOptions configures the stdio MCP server.
type MCPOptions struct {
	AllowEnqueue bool
	Version      string
	PinWorkspace func(repo, ref string) (WorkspaceSpec, error)
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

func readOnlyMCPTools() []mcpToolDef {
	return []mcpToolDef{
		{
			Name:        "list_tasks",
			Description: "List ATS tasks, newest first: id, title, state, verdict, priority, difficulty, model tier, machine, persona to-do label and times, plus offline scheduler status. Read-only.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"todo_id":    map[string]any{"type": "string", "description": "only tasks labelled persona_todo=<todo_id>"},
					"status":     map[string]any{"description": "task state or array of states (queued, running, succeeded, failed, canceled)"},
					"states":     map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": AllTaskStates}},
					"priority":   map[string]any{"type": "string", "description": "filter by priority (p0, p1, p2, p3)"},
					"difficulty": map[string]any{"type": "string", "description": "filter by difficulty (easy, medium, hard, extreme)"},
					"machine":    map[string]any{"type": "string", "description": "filter by target machine"},
					"pool":       map[string]any{"type": "string"},
					"source":     map[string]any{"type": "string"},
					"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": MaxListTasks},
				},
				"additionalProperties": false,
			},
			Annotations: map[string]any{"readOnlyHint": true},
		},
		{
			Name:        "get_task",
			Description: "One ATS task: summary, priority/difficulty/tier tags, prompt preview, acceptance command and its fenced attempts. Read-only.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
				"required":             []string{"id"},
				"additionalProperties": false,
			},
			Annotations: map[string]any{"readOnlyHint": true},
		},
		{
			Name:        "get_evidence",
			Description: "What proves an ATS task done or not: final state, verdict, failure reason, acceptance command and each verification report (outcome, verdict, exit code, message, artifacts). Read-only.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
				"required":             []string{"id"},
				"additionalProperties": false,
			},
			Annotations: map[string]any{"readOnlyHint": true},
		},
	}
}

func enqueueMCPTool() mcpToolDef {
	return mcpToolDef{
		Name: "enqueue_task",
		Description: "Queues a new ATS task on behalf of the user, labelled with the Persona to-do (persona_todo=<todo_id>). " +
			"Supports priority (p0..p3), difficulty (easy|medium|hard|extreme -> model tier fast|standard|strong) and machine tags for multi-factor scheduling. " +
			"The task gets the readonly permission profile unless permission_profile is \"default\". Confirm each call with the user before you make it.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"todo_id": map[string]any{
					"type":        "string",
					"minLength":   1,
					"maxLength":   MaxTodoIDLen,
					"pattern":     todoIDPattern.String(),
					"description": "the Persona to-do this task belongs to",
				},
				"prompt":             map[string]any{"type": "string", "description": "what the task must do"},
				"repo":               map[string]any{"type": "string", "description": "absolute path of a git repository (pinned to its top level)"},
				"title":              map[string]any{"type": "string"},
				"ref":                map[string]any{"type": "string", "description": "commit-ish to pin the workspace at (default HEAD)"},
				"qos":                map[string]any{"type": "string", "enum": []string{"expedited", "standard", "bulk"}},
				"pool":               map[string]any{"type": "string", "minLength": 1},
				"priority":           map[string]any{"type": "string", "description": "task priority: p0, p1, p2, p3 (or high, normal, low; default p2)"},
				"difficulty":         map[string]any{"type": "string", "description": "task difficulty: easy, medium, hard, extreme (default medium)"},
				"machine":            map[string]any{"type": "string", "description": "optional target machine affinity"},
				"tags":               map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"acceptance":         map[string]any{"type": "string", "minLength": 1, "description": "shell command that proves the task done"},
				"permission_profile": map[string]any{"type": "string", "enum": []string{PermissionReadonly, PermissionReadOnlyDash, PermissionDefault}, "default": PermissionReadonly},
				"labels":             map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"idempotency_key":    map[string]any{"type": "string", "minLength": 1},
			},
			"required":             []string{"todo_id", "prompt", "repo"},
			"additionalProperties": false,
		},
		Annotations: map[string]any{
			"readOnlyHint":    false,
			"destructiveHint": false,
			"idempotentHint":  false,
			"openWorldHint":   false,
		},
	}
}

var allowedListFields = []string{
	"todo_id", "status", "states", "priority", "difficulty", "machine", "pool", "source", "limit",
}

var allowedEnqueueFields = []string{
	"todo_id", "prompt", "repo", "title", "ref", "qos", "pool",
	"priority", "difficulty", "machine", "tags", "acceptance",
	"permission_profile", "labels", "idempotency_key",
}

// ServeMCP serves newline-delimited JSON-RPC 2.0 MCP requests over stdio until r reaches EOF.
func ServeMCP(q *Queue, r io.Reader, w io.Writer, opts MCPOptions) error {
	if opts.Version == "" {
		opts.Version = "0.3.0"
	}
	if opts.PinWorkspace == nil {
		opts.PinWorkspace = defaultPinWorkspace
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(w)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		resp := HandleMCPMessage(q, []byte(line), opts)
		if resp == nil {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// HandleMCPMessage processes a single JSON-RPC 2.0 message and returns a response (or nil for notifications).
func HandleMCPMessage(q *Queue, raw []byte, opts MCPOptions) *jsonRPCResponse {
	if opts.Version == "" {
		opts.Version = "0.3.0"
	}
	if opts.PinWorkspace == nil {
		opts.PinWorkspace = defaultPinWorkspace
	}

	var req jsonRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &jsonRPCError{Code: -32700, Message: "parse error"},
		}
	}
	if req.JSONRPC != "2.0" || strings.TrimSpace(req.Method) == "" {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &jsonRPCError{Code: -32600, Message: "invalid request"},
		}
	}
	// JSON-RPC notifications have no id field.
	if len(req.ID) == 0 {
		return nil
	}

	switch req.Method {
	case "initialize":
		protoVer := MCPProtocolVersion
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if len(req.Params) > 0 && json.Unmarshal(req.Params, &p) == nil && strings.TrimSpace(p.ProtocolVersion) != "" {
			protoVer = p.ProtocolVersion
		}
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": protoVer,
				"capabilities": map[string]any{
					"tools": map[string]any{"listChanged": false},
				},
				"serverInfo": map[string]any{
					"name":    "ats",
					"version": opts.Version,
				},
			},
		}
	case "ping":
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{},
		}
	case "tools/list":
		tools := readOnlyMCPTools()
		if opts.AllowEnqueue {
			tools = append(tools, enqueueMCPTool())
		}
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"tools": tools},
		}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				return mcpToolError(req.ID, fmt.Errorf("invalid tool call arguments: %w", err))
			}
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		out, err := CallMCPTool(q, p.Name, p.Arguments, opts)
		if err != nil {
			return mcpToolError(req.ID, err)
		}
		textBytes, _ := json.Marshal(out)
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": string(textBytes)},
				},
				"structuredContent": out,
			},
		}
	default:
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

func mcpToolError(id json.RawMessage, err error) *jsonRPCResponse {
	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": err.Error()},
			},
			"isError": true,
		},
	}
}

// CallMCPTool executes one of the 4 MCP tools against the Queue.
func CallMCPTool(q *Queue, name string, args map[string]any, opts MCPOptions) (any, error) {
	if q == nil {
		return nil, errors.New("queue is not initialized")
	}
	_ = q.Reload()
	if opts.PinWorkspace == nil {
		opts.PinWorkspace = defaultPinWorkspace
	}

	switch name {
	case "list_tasks":
		return callListTasks(q, args)
	case "get_task":
		return callGetTask(q, args)
	case "get_evidence":
		return callGetEvidence(q, args)
	case "enqueue_task":
		if !opts.AllowEnqueue {
			return nil, fmt.Errorf("unknown tool: %s", name)
		}
		return callEnqueueTask(q, args, opts)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func callListTasks(q *Queue, args map[string]any) (any, error) {
	for k := range args {
		if !slices.Contains(allowedListFields, k) {
			return nil, fmt.Errorf("%s is not a list_tasks field", k)
		}
	}

	var states []string
	if rawStates, ok := args["states"]; ok && rawStates != nil {
		list, err := parseStatesArg(rawStates)
		if err != nil {
			return nil, err
		}
		states = append(states, list...)
	}
	if rawStatus, ok := args["status"]; ok && rawStatus != nil {
		list, err := parseStatesArg(rawStatus)
		if err != nil {
			return nil, err
		}
		states = append(states, list...)
	}

	limit := 20
	if rawLimit, ok := args["limit"]; ok && rawLimit != nil {
		n, okNum := asInt(rawLimit)
		if !okNum || n < 1 || n > MaxListTasks {
			return nil, fmt.Errorf("limit must be an integer 1..%d", MaxListTasks)
		}
		limit = n
	}

	filter := TaskFilter{
		States: states,
		Limit:  limit,
	}
	if s, ok := args["pool"].(string); ok {
		filter.Pool = strings.TrimSpace(s)
	}
	if s, ok := args["source"].(string); ok {
		filter.Source = strings.TrimSpace(s)
	}
	if s, ok := args["todo_id"].(string); ok {
		filter.TodoID = strings.TrimSpace(s)
	}
	if s, ok := args["priority"].(string); ok {
		filter.Priority = strings.TrimSpace(s)
	}
	if s, ok := args["difficulty"].(string); ok {
		filter.Difficulty = strings.TrimSpace(s)
	}
	if s, ok := args["machine"].(string); ok {
		filter.Machine = strings.TrimSpace(s)
	}

	tasks, err := q.ListTasks(filter)
	if err != nil {
		return nil, err
	}
	summaries := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		summaries = append(summaries, formatTaskSummary(t))
	}
	plan := q.PlanSchedule(q.QuotaStatus().CheckedAt)
	return map[string]any{
		"tasks": summaries,
		"scheduler": map[string]any{
			"admissionMode":      plan.AdmissionMode,
			"admissionLabel":     plan.AdmissionLabel,
			"quotaRemainingPct":  plan.Quota.RemainingPct,
			"queuedByPriority":   plan.QueuedByPriority,
			"queuedByDifficulty": plan.QueuedByDifficulty,
		},
	}, nil
}

func parseStatesArg(raw any) ([]string, error) {
	if s, ok := raw.(string); ok {
		s = strings.TrimSpace(s)
		if !slices.Contains(AllTaskStates, s) {
			return nil, fmt.Errorf("states must be one of %s", strings.Join(AllTaskStates, ", "))
		}
		return []string{s}, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("states must be a list of %s", strings.Join(AllTaskStates, ", "))
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		s, ok := item.(string)
		if !ok || !slices.Contains(AllTaskStates, s) {
			return nil, fmt.Errorf("states must be a list of %s", strings.Join(AllTaskStates, ", "))
		}
		out = append(out, s)
	}
	return out, nil
}

func callGetTask(q *Queue, args map[string]any) (any, error) {
	id, err := requireTaskID(args)
	if err != nil {
		return nil, err
	}
	t, err := q.GetTask(id)
	if err != nil {
		return nil, err
	}
	attempts := make([]map[string]any, 0, len(t.Attempts))
	for _, a := range t.Attempts {
		attempts = append(attempts, map[string]any{
			"attemptId":      a.AttemptID,
			"attemptNo":      a.AttemptNo,
			"fenceToken":     a.FenceToken,
			"state":          a.State,
			"harness":        a.Harness,
			"workerId":       a.WorkerID,
			"machine":        a.Machine,
			"modelTier":      a.ModelTier,
			"leasedAt":       a.LeasedAt,
			"leaseExpiresAt": a.LeaseExpiresAt,
			"startedAt":      a.StartedAt,
			"finishedAt":     a.FinishedAt,
		})
	}
	return map[string]any{
		"task":       formatTaskSummary(t),
		"prompt":     truncateRunes(t.Prompt, PromptPreviewLimit),
		"repo":       t.Workspace.Repo,
		"acceptance": t.Acceptance,
		"labels":     t.Labels,
		"attempts":   attempts,
	}, nil
}

func callGetEvidence(q *Queue, args map[string]any) (any, error) {
	id, err := requireTaskID(args)
	if err != nil {
		return nil, err
	}
	t, err := q.GetTask(id)
	if err != nil {
		return nil, err
	}
	reports := make([]map[string]any, 0, len(t.Reports))
	for _, r := range t.Reports {
		artifacts := r.Artifacts
		if artifacts == nil {
			artifacts = []Artifact{}
		}
		var verdict any
		if r.Verdict != "" {
			verdict = r.Verdict
		}
		var errClass any
		if r.ErrorClass != "" {
			errClass = r.ErrorClass
		}
		var msg any
		if r.Message != "" {
			msg = r.Message
		}
		reports = append(reports, map[string]any{
			"attemptNo":  r.AttemptNo,
			"fenceToken": r.FenceToken,
			"workerId":   r.WorkerID,
			"reportId":   r.ReportID,
			"receivedAt": r.ReceivedAt,
			"outcome":    r.Outcome,
			"verdict":    verdict,
			"errorClass": errClass,
			"exitCode":   r.ExitCode,
			"message":    msg,
			"artifacts":  artifacts,
		})
	}
	var failureReason any
	if t.FailureReason != "" {
		failureReason = t.FailureReason
	}
	return map[string]any{
		"id":            t.ID,
		"state":         t.State,
		"verdict":       t.Verdict,
		"failureReason": failureReason,
		"acceptance":    t.Acceptance,
		"decisionRef":   t.DecisionWrittenRef,
		"reports":       reports,
	}, nil
}

func callEnqueueTask(q *Queue, args map[string]any, opts MCPOptions) (any, error) {
	for k := range args {
		if !slices.Contains(allowedEnqueueFields, k) {
			return nil, fmt.Errorf("%s is not an enqueue_task field", k)
		}
	}

	todoID, ok := args["todo_id"].(string)
	if !ok || len(todoID) < 1 || len(todoID) > MaxTodoIDLen || !todoIDPattern.MatchString(todoID) {
		return nil, fmt.Errorf("todo_id must be 1..%d characters of letters, digits, _ . : -", MaxTodoIDLen)
	}
	prompt, ok := args["prompt"].(string)
	if !ok || strings.TrimSpace(prompt) == "" {
		return nil, errors.New("prompt must be a non-empty string")
	}
	repo, ok := args["repo"].(string)
	if !ok || !filepath.IsAbs(repo) {
		return nil, errors.New("repo must be an absolute path")
	}

	var title string
	if raw, exists := args["title"]; exists && raw != nil {
		s, ok := raw.(string)
		if !ok {
			return nil, errors.New("title must be a string")
		}
		title = s
	}

	var ref string
	if raw, exists := args["ref"]; exists && raw != nil {
		s, ok := raw.(string)
		if !ok || s == "" || strings.HasPrefix(s, "-") {
			return nil, errors.New("ref must be a commit-ish that does not start with -")
		}
		ref = s
	}

	var qos string
	if raw, exists := args["qos"]; exists && raw != nil {
		s, ok := raw.(string)
		if !ok || (s != "expedited" && s != "standard" && s != "bulk") {
			return nil, errors.New("qos must be one of expedited, standard, bulk")
		}
		qos = s
	}

	var pool string
	if raw, exists := args["pool"]; exists && raw != nil {
		s, ok := raw.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, errors.New("pool must be a non-empty string")
		}
		pool = strings.TrimSpace(s)
	}

	var priority string
	if raw, exists := args["priority"]; exists && raw != nil {
		s, ok := raw.(string)
		if !ok {
			return nil, errors.New("priority must be a string")
		}
		if _, err := NormalizePriority(s); err != nil {
			return nil, err
		}
		priority = s
	}

	var difficulty string
	if raw, exists := args["difficulty"]; exists && raw != nil {
		s, ok := raw.(string)
		if !ok {
			return nil, errors.New("difficulty must be a string")
		}
		if _, _, _, err := NormalizeDifficulty(s, ""); err != nil {
			return nil, err
		}
		difficulty = s
	}

	var machine string
	if raw, exists := args["machine"]; exists && raw != nil {
		s, ok := raw.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, errors.New("machine must be a non-empty string")
		}
		machine = strings.TrimSpace(s)
	}

	var tags []string
	if raw, exists := args["tags"]; exists && raw != nil {
		arr, ok := raw.([]any)
		if !ok {
			return nil, errors.New("tags must be an array of strings")
		}
		for _, item := range arr {
			s, ok := item.(string)
			if !ok {
				return nil, errors.New("tags must be an array of strings")
			}
			k, _, hasEq := strings.Cut(s, "=")
			if (hasEq && strings.EqualFold(strings.TrimSpace(k), PersonaTodoLabel)) || strings.EqualFold(strings.TrimSpace(s), PersonaTodoLabel) {
				return nil, fmt.Errorf("labels.%s is set from todo_id; the caller cannot set it", PersonaTodoLabel)
			}
			tags = append(tags, s)
		}
	}

	var acceptance string
	if raw, exists := args["acceptance"]; exists && raw != nil {
		s, ok := raw.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, errors.New("acceptance must be a non-empty string (the command that proves the task done)")
		}
		acceptance = strings.TrimSpace(s)
	}

	permProfile := PermissionReadonly
	if raw, exists := args["permission_profile"]; exists && raw != nil {
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("permission_profile must be one of %s, %s, %s", PermissionReadonly, PermissionReadOnlyDash, PermissionDefault)
		}
		norm, err := NormalizePermissionProfile(s)
		if err != nil {
			return nil, err
		}
		permProfile = norm
	}

	labels := map[string]string{}
	if raw, exists := args["labels"]; exists && raw != nil {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("labels must be an object of string values")
		}
		for k, v := range m {
			cleanKey := strings.TrimSpace(k)
			if strings.EqualFold(cleanKey, PersonaTodoLabel) {
				return nil, fmt.Errorf("labels.%s is set from todo_id; the caller cannot set it", PersonaTodoLabel)
			}
			sv, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("labels.%s must be a string", k)
			}
			labels[cleanKey] = sv
		}
	}

	var idempotencyKey string
	if raw, exists := args["idempotency_key"]; exists && raw != nil {
		s, ok := raw.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, errors.New("idempotency_key must be a non-empty string")
		}
		idempotencyKey = strings.TrimSpace(s)
	}

	ws, err := opts.PinWorkspace(repo, ref)
	if err != nil {
		return nil, err
	}

	labels[PersonaTodoLabel] = todoID
	res, err := q.Enqueue(EnqueueInput{
		IdempotencyKey:    idempotencyKey,
		Title:             title,
		Prompt:            prompt,
		Repo:              ws.Repo,
		BaseSHA:           ws.BaseSHA,
		QoS:               qos,
		Pool:              pool,
		Priority:          priority,
		Difficulty:        difficulty,
		Machine:           machine,
		AcceptanceCommand: acceptance,
		PermissionProfile: permProfile,
		Labels:            labels,
		Tags:              tags,
		Source:            AssistantSource,
		ExternalRef: &ExternalRef{
			System:       AssistantSource,
			ID:           PersonaSystemID,
			Conversation: todoID,
		},
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"created":           res.Created,
		"task":              formatTaskSummary(res.Task),
		"permissionProfile": res.Task.PermissionProfile,
	}, nil
}

func requireTaskID(args map[string]any) (string, error) {
	if len(args) > 1 {
		for k := range args {
			if k != "id" {
				return "", fmt.Errorf("%s is not a valid field", k)
			}
		}
	}
	id, ok := args["id"].(string)
	if !ok || strings.TrimSpace(id) == "" {
		return "", errors.New("id must be a non-empty string")
	}
	return strings.TrimSpace(id), nil
}

func formatTaskSummary(t *Task) map[string]any {
	var todoID any
	if id := t.TodoID(); id != "" {
		todoID = id
	}
	var failureReason any
	if t.FailureReason != "" {
		failureReason = t.FailureReason
	}
	var machine any
	if t.Machine != "" {
		machine = t.Machine
	}
	return map[string]any{
		"id":                t.ID,
		"title":             t.Title,
		"state":             t.State,
		"verdict":           t.Verdict,
		"failureReason":     failureReason,
		"pools":             t.Pools,
		"qos":               t.QoS,
		"priority":          t.Priority,
		"difficulty":        t.Difficulty,
		"modelTier":         t.ModelTier,
		"quotaWeight":       t.QuotaWeight,
		"machine":           machine,
		"permissionProfile": t.PermissionProfile,
		"source":            t.Source,
		"todoId":            todoID,
		"attemptNo":         t.AttemptNo,
		"fenceToken":        t.FenceToken,
		"retriesLeft":       t.RetriesLeft,
		"notBefore":         t.NotBefore,
		"submittedAt":       t.SubmittedAt,
		"finishedAt":        t.FinishedAt,
	}
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if float64(int(n)) != n {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

func defaultPinWorkspace(repo, ref string) (WorkspaceSpec, error) {
	if !filepath.IsAbs(repo) {
		return WorkspaceSpec{}, errors.New("repo must be an absolute path")
	}
	commitRef := strings.TrimSpace(ref)
	if commitRef == "" {
		commitRef = "HEAD"
	}
	topOut, err := exec.Command("git", "-C", repo, "rev-parse", "--show-toplevel").Output()
	if err == nil {
		top := strings.TrimSpace(string(topOut))
		shaOut, shaErr := exec.Command("git", "-C", top, "rev-parse", "--verify", commitRef+"^{commit}").Output()
		if shaErr == nil {
			return WorkspaceSpec{
				Repo:    top,
				BaseSHA: strings.TrimSpace(string(shaOut)),
			}, nil
		}
	}
	return WorkspaceSpec{
		Repo:    filepath.Clean(repo),
		BaseSHA: commitRef,
	}, nil
}
