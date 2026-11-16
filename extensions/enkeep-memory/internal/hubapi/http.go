package hubapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nwverse/persona/sdk/go/ext"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HTTP reads the hub's HTTP API with the extension's token. Route shapes follow the hub (server/internal/hub/http.go
// sessionItems and listSessions, assistant/fleet_routes.go interactions history, assistant/todos.go and
// todo_decisions.go, assistant/profile.go GET /api/v1/assistant, hub/notify_api.go).
type HTTP struct {
	Base   string // PERSONA_HUB_URL, e.g. http://127.0.0.1:8787
	Token  string // PERSONA_HUB_TOKEN
	Client *http.Client
	// Paging is no longer set by hand: Interactions always asks with since / before and tells an older hub (which
	// ignores them) by the cards it answers. Kept so callers that set it still compile.
	Paging bool
	// Call is the extension's RPC to the hub (ext.Hub.Call), for what is not an HTTP route: assistant.memory (H2).
	// Nil: those sources are missing.
	Call func(ctx context.Context, method string, params, out any) error
	// Bytes counts the response bytes read, by route kind (sessions, items, interactions, todos, decisions, about);
	// the sources card shows how much was read.
	mu    sync.Mutex
	bytes map[string]int

	health map[string]bool // machine/session → a health space session, as the last Sessions listed
}

// FromEnv is the HTTP client of PERSONA_HUB_URL and PERSONA_HUB_TOKEN, or nil when either is unset.
func FromEnv() *HTTP {
	base, tok := os.Getenv("PERSONA_HUB_URL"), os.Getenv("PERSONA_HUB_TOKEN")
	if base == "" || tok == "" {
		return nil
	}
	return &HTTP{Base: base, Token: tok}
}

// Read is how many response bytes a route kind gave so far.
func (h *HTTP) Read(kind string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bytes[kind]
}

// apiError is the hub's error body (apierr.Body): {error, code, scope?}.
type apiError struct {
	Status int
	Msg    string `json:"error"`
	Code   string `json:"code"`
	Scope  string `json:"scope"`
}

func (e *apiError) Error() string { return fmt.Sprintf("hub %d %s: %s", e.Status, e.Code, e.Msg) }

func (h *HTTP) get(ctx context.Context, kind, path string, q url.Values, out any) error {
	return h.do(ctx, kind, http.MethodGet, path, q, nil, out)
}

func (h *HTTP) do(ctx context.Context, kind, method, path string, q url.Values, body any, out any) error {
	u := strings.TrimRight(h.Base, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c := h.Client
	if c == nil {
		c = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return err
	}
	h.mu.Lock()
	if h.bytes == nil {
		h.bytes = map[string]int{}
	}
	h.bytes[kind] += len(raw)
	h.mu.Unlock()
	if resp.StatusCode >= 300 {
		e := &apiError{Status: resp.StatusCode}
		_ = json.Unmarshal(raw, e)
		return mapStatus(e)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// mapStatus turns a refusal into what the curator records: 403 for a scope not granted is ErrNoScope (so is a
// route the token cannot open at all, which an old hub answers without a scope); 404 and 405 of a route an older
// hub does not have are a wait for the hub (the caller says which change).
func mapStatus(e *apiError) error {
	switch {
	case e.Status == http.StatusForbidden && (e.Scope != "" || e.Code == "permission_denied"):
		return fmt.Errorf("%w: %s", ErrNoScope, e.Scope)
	case e.Status == http.StatusNotFound && e.Code == "not_found" && strings.Contains(e.Msg, "route"),
		e.Status == http.StatusMethodNotAllowed:
		return errNoRoute
	}
	return e
}

var errNoRoute = errors.New("route not on this hub")

// hubSession is proto.Session as the sessions list writes it (the fields read here).
type hubSession struct {
	Machine   string    `json:"machine_id"`
	ID        string    `json:"session_id"`
	Title     string    `json:"title"`
	Agent     string    `json:"agent"`
	Cwd       string    `json:"cwd"`
	Repo      string    `json:"repo_root"`
	Updated   time.Time `json:"updated_at"`
	Started   time.Time `json:"started_at"`
	Status    string    `json:"status"`
	TurnErr   string    `json:"turn_error_kind"`
	Assistant bool      `json:"assistant"`
	Space     string    `json:"space"`
	Subagent  bool      `json:"is_subagent"`
	ByPersona bool      `json:"started_by_persona"`
	ByJarvis  bool      `json:"started_by_jarvis"`
}

func (s hubSession) session() Session {
	out := Session{Machine: s.Machine, ID: s.ID, Title: s.Title, Agent: s.Agent, Cwd: s.Cwd, Started: s.Started,
		Updated: s.Updated, Status: s.Status, Assistant: s.Assistant, Health: IsHealth(s.Assistant, s.Space),
		ByPersona: s.ByPersona || s.ByJarvis}
	if out.Agent == "" {
		out.Agent = "claude"
	}
	out.Project = s.Repo
	if out.Project == "" {
		out.Project = s.Cwd
	}
	switch {
	case strings.Contains(s.TurnErr, "timeout"):
		out.Ended = "timeout"
	case s.Status == "interrupted":
		out.Ended = "interrupted"
	case s.Status == "done":
		out.Ended = "normal"
	}
	return out
}

// IsHealth says whether a session is a health space conversation (proto.Session: assistant and space "health").
func IsHealth(assistant bool, space string) bool { return assistant && space == "health" }

// Sessions lists the sessions updated since since, in the paged list (limit, cursor), without health space ones and
// without subagents (their work shows in the parent's items).
func (h *HTTP) Sessions(ctx context.Context, since time.Time) ([]Session, error) {
	q := url.Values{"limit": {"200"}}
	var out []Session
	health := map[string]bool{}
	for page := 0; page < 500; page++ {
		var r struct {
			Sessions   []hubSession `json:"sessions"`
			HasMore    bool         `json:"has_more"`
			NextCursor string       `json:"next_cursor"`
		}
		if err := h.get(ctx, "sessions", "/api/v1/sessions", q, &r); err != nil {
			return out, err
		}
		for _, s := range r.Sessions {
			if IsHealth(s.Assistant, s.Space) {
				health[s.Machine+"/"+s.ID] = true
				continue
			}
			if s.Subagent || s.Updated.Before(since) {
				continue
			}
			out = append(out, s.session())
		}
		if !r.HasMore || r.NextCursor == "" {
			break
		}
		q.Set("cursor", r.NextCursor)
	}
	h.mu.Lock()
	h.health = health
	h.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.Before(out[j].Updated) })
	return out, nil
}

// listed makes sure the sessions were listed (for the health space filter); false when the list cannot be read.
func (h *HTTP) listed(ctx context.Context) error {
	h.mu.Lock()
	known := h.health != nil
	h.mu.Unlock()
	if known {
		return nil
	}
	_, err := h.Sessions(ctx, time.Now())
	return err
}

func (h *HTTP) isHealth(machine, session string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.health[machine+"/"+session]
}

// hubItem is proto.Item (the fields read here).
type hubItem struct {
	ID       string          `json:"id"`
	Seq      int             `json:"seq"`
	TS       time.Time       `json:"ts"`
	Role     string          `json:"role"`
	Kind     string          `json:"kind"`
	Text     string          `json:"text"`
	ToolName string          `json:"tool_name"`
	ToolUse  string          `json:"tool_use_id"`
	Input    json.RawMessage `json:"tool_input"`
	IsError  bool            `json:"is_error"`
	FullSize int             `json:"full_size"`
	From     string          `json:"from"`
	Injected *struct {
		Kind string `json:"kind"`
	} `json:"injected"`
	Source *struct {
		Kind string `json:"kind"`
	} `json:"source"`
	Outcome *struct {
		Status string `json:"status"`
	} `json:"outcome"`
	Fold *hubFold `json:"fold"`
}

// hubFold is proto.Fold. errors and tool_names come with H3: their presence is what Known says.
type hubFold struct {
	Items   int `json:"items"`
	Tools   int `json:"tools"`
	Through int `json:"through_seq"`
	Changes []struct {
		Path string `json:"path"`
	} `json:"changes"`
	Errors    *int     `json:"errors"`
	ToolNames []string `json:"tool_names"`
}

func (it hubItem) item() Item {
	out := Item{ID: it.ID, Seq: it.Seq, At: it.TS, Text: it.Text, Tool: it.ToolName, Cut: it.FullSize > 0, Call: it.ToolUse}
	switch it.Kind {
	case "text":
		switch {
		case it.Role != "user":
			out.Kind = KindAgent
		case it.Injected != nil:
			out.Kind, out.Notice = KindNotice, "injected:"+it.Injected.Kind
		case it.Source != nil:
			out.Kind, out.Notice = KindNotice, "source:"+it.Source.Kind
		case it.From != "":
			out.Kind, out.Notice = KindNotice, "from:"+it.From
		default:
			out.Kind = KindUser
		}
	case "tool_use":
		out.Kind = KindToolUse
		if len(it.Input) > 0 {
			out.Text = string(it.Input)
		}
	case "tool_result":
		out.Kind = KindToolResult
		out.Err = it.IsError || (it.Outcome != nil && it.Outcome.Status == "failed")
	case "thinking":
		out.Kind = KindThinking
	case "fold":
		out.Kind = KindFold
		f := &Fold{From: it.Seq, To: it.Fold.Through, Calls: it.Fold.Tools}
		for _, c := range it.Fold.Changes {
			f.Files = append(f.Files, c.Path)
		}
		if it.Fold.Errors != nil {
			f.Errors, f.Tools, f.Known = *it.Fold.Errors, it.Fold.ToolNames, true
		}
		out.Fold = f
		out.Text = ""
	default: // notice, recap, image
		out.Kind, out.Notice = KindNotice, it.Kind
		if it.Kind == "image" {
			out.Text = ""
		}
	}
	return out
}

// Items reads a session's items: folded and after a seq for daily reading, or one segment unfolded for a dig. A
// health space session is never read.
func (h *HTTP) Items(ctx context.Context, machine, session string, q ItemsQuery) ([]Item, error) {
	if err := h.listed(ctx); err != nil {
		return nil, err // without the list a health space session could not be told apart
	}
	if h.isHealth(machine, session) {
		return nil, nil
	}
	v := url.Values{}
	if q.Fold {
		v.Set("fold_work", "1")
	}
	if q.Through > 0 {
		v.Set("from_seq", strconv.Itoa(q.From))
		v.Set("through_seq", strconv.Itoa(q.Through))
	} else {
		v.Set("after_seq", strconv.Itoa(q.After))
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	path := "/api/v1/sessions/" + url.PathEscape(machine) + "/" + url.PathEscape(session) + "/items"
	var out []Item
	for page := 0; page < 200; page++ {
		var r struct {
			Items     []hubItem `json:"items"`
			MoreAfter bool      `json:"more_after"`
			More      bool      `json:"more"`
		}
		if err := h.get(ctx, "items", path, v, &r); err != nil {
			return out, err
		}
		last := -1
		for _, it := range r.Items {
			if it.Kind == "fold" && it.Fold == nil {
				continue
			}
			out = append(out, it.item())
			if it.Seq > last {
				last = it.Seq
			}
		}
		if last < 0 {
			break
		}
		switch {
		case q.Through > 0 && r.More && last < q.Through: // a fold's lines go on
			v.Set("from_seq", strconv.Itoa(last+1))
		case q.Through == 0 && r.MoreAfter:
			v.Set("after_seq", strconv.Itoa(last))
		default:
			return out, nil
		}
	}
	return out, nil
}

// hubInteraction is db.InteractionRecord: the card, its status and its resolution.
type hubInteraction struct {
	Interaction struct {
		ID         string     `json:"id"`
		Machine    string     `json:"machine_id"`
		Session    string     `json:"session_id"`
		Title      string     `json:"session_title"`
		Kind       string     `json:"kind"`
		Created    time.Time  `json:"created_at"`
		Private    bool       `json:"private"`
		Summary    string     `json:"summary"`
		ToolName   string     `json:"tool_name"`
		Deadline   *time.Time `json:"deadline"`
		Reversible *bool      `json:"reversible"`
		Questions  []struct {
			Question  string `json:"question"`
			Recommend string `json:"recommend"`
			Options   []struct {
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
		Delegate *struct {
			Recommend string `json:"recommend"`
		} `json:"delegate"`
		Suggestion json.RawMessage `json:"suggestion"`
	} `json:"interaction"`
	Status     string `json:"status"`
	Resolution *struct {
		Answers    map[string]string `json:"answers"`
		AnsweredBy string            `json:"answered_by"`
		Reason     string            `json:"reason"`
		Option     string            `json:"option"`
		Text       string            `json:"text"`
		Approve    *bool             `json:"approve"`
		Allow      *bool             `json:"allow"`
		Dismiss    bool              `json:"dismiss"`
		Late       bool              `json:"late"`
	} `json:"resolution"`
}

// interactions makes one Interaction per question of a card (a card asks one or more).
func (r hubInteraction) interactions() []Interaction {
	in := r.Interaction
	base := Interaction{ID: in.ID, Machine: in.Machine, Session: in.Session, Kind: in.Kind, At: in.Created,
		Private: in.Private, Status: r.Status, Title: in.Title, Tool: in.ToolName, Reversible: in.Reversible}
	if in.Deadline != nil {
		base.Deadline = *in.Deadline
	}
	if in.Delegate != nil {
		base.Recommend = in.Delegate.Recommend
	}
	if res := r.Resolution; res != nil && !res.Dismiss {
		base.AnsweredBy = res.AnsweredBy // absent: the user (hub #5876); assistant; default
		if base.AnsweredBy == "" {
			base.AnsweredBy = "user"
		}
		base.Late = res.Late
		base.Reason = res.Reason
	} else if res != nil {
		base.Reason = res.Reason
	}
	if len(in.Questions) == 0 {
		x := base
		x.Question = in.Summary
		if res := r.Resolution; res != nil {
			switch {
			case res.Option != "":
				x.Answers = []string{res.Option}
			case res.Approve != nil:
				x.Answers = []string{map[bool]string{true: "approve", false: "reject"}[*res.Approve]}
			case res.Allow != nil:
				x.Answers = []string{map[bool]string{true: "allow", false: "deny"}[*res.Allow]}
			case res.Text != "":
				x.Answers = []string{res.Text}
			}
		}
		return []Interaction{x}
	}
	var out []Interaction
	for i, q := range in.Questions {
		x := base
		if len(in.Questions) > 1 {
			x.ID = fmt.Sprintf("%s#%d", in.ID, i)
		}
		x.Question = q.Question
		for _, o := range q.Options {
			x.Options = append(x.Options, o.Label)
		}
		if q.Recommend != "" {
			x.Recommend = q.Recommend
		}
		if res := r.Resolution; res != nil && res.Answers[q.Question] != "" {
			x.Answers = SplitAnswer(res.Answers[q.Question])
		}
		out = append(out, x)
	}
	return out
}

// Limits on what a card keeps (the hub does not cut questions, options or answers).
const (
	maxQuestion = 2000
	maxOption   = 300
	maxAnswer   = 2000
	maxPages    = 200 // a first read walks back at most this many pages (100 000 cards)
)

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// Interactions reads the cards (GET /api/v1/interactions/history). It asks with since, before / before_id and kind
// and follows next_before / next_before_id until the hub gives no cursor: with an extension token a page may hold
// fewer cards than asked, or none, because private and health cards the hub leaves out still take their place, so a
// short page is not the end. A hub before H3 ignores the range and answers its newest ≤500: that is told by cards
// outside the range, which are then dropped here, and the answer comes with *WaitingHub{"H3"}. The hub already
// leaves out private and health cards for an extension's token; the checks here stay as a second guard.
func (h *HTTP) Interactions(ctx context.Context, q InteractionsQuery) ([]Interaction, error) {
	limit := 500
	if q.Limit > 0 && q.Limit < 500 {
		limit = q.Limit
	}
	if err := h.listed(ctx); err != nil {
		return nil, err // without the list a health space card could not be told apart
	}
	ranged := !q.Since.IsZero() || !q.Before.IsZero() || q.Kind != ""
	before, beforeID := q.Before, q.BeforeID
	var out []Interaction
	old := false
	for page := 0; page < maxPages; page++ {
		v := url.Values{"limit": {strconv.Itoa(limit)}}
		if q.Machine != "" {
			v.Set("machine_id", q.Machine)
		}
		if q.Session != "" {
			v.Set("session_id", q.Session)
		}
		if !q.Since.IsZero() {
			v.Set("since", q.Since.UTC().Format(time.RFC3339))
		}
		if !before.IsZero() {
			v.Set("before", before.UTC().Format(time.RFC3339Nano))
			if beforeID != "" {
				v.Set("before_id", beforeID)
			}
		}
		if q.Kind != "" {
			v.Set("kind", q.Kind)
		}
		var r struct {
			Interactions []hubInteraction `json:"interactions"`
			NextBefore   *time.Time       `json:"next_before"`
			NextBeforeID string           `json:"next_before_id"`
		}
		if err := h.get(ctx, "interactions", "/api/v1/interactions/history", v, &r); err != nil {
			return nil, err
		}
		for _, rec := range r.Interactions {
			for _, x := range rec.interactions() {
				outside := (!q.Since.IsZero() && x.At.Before(q.Since)) || (!q.Before.IsZero() && !x.At.Before(q.Before)) ||
					(q.Kind != "" && x.Kind != q.Kind)
				if outside {
					old = true // the hub did not apply the range: a hub before H3
					continue
				}
				if x.Private || h.isHealth(x.Machine, x.Session) {
					continue
				}
				x.Question = clip(x.Question, maxQuestion)
				for i := range x.Options {
					x.Options[i] = clip(x.Options[i], maxOption)
				}
				for i := range x.Answers {
					x.Answers[i] = clip(x.Answers[i], maxAnswer)
				}
				out = append(out, x)
			}
		}
		if old || !ranged || r.NextBefore == nil || r.NextBefore.IsZero() {
			break
		}
		before, beforeID = *r.NextBefore, r.NextBeforeID
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	if old {
		return out, &WaitingHub{Change: "H3"}
	}
	return out, nil
}

// hubTodo is assistant.Todo as GET /api/v1/todos lists it (the fields read here).
type hubTodo struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Created         time.Time  `json:"created_at"`
	Updated         time.Time  `json:"updated_at"`
	DoneAt          *time.Time `json:"done_at"`
	DoneBy          string     `json:"done_by"`
	DoneReason      string     `json:"done_reason"`
	ReopenedAt      *time.Time `json:"reopened_at"`
	CancelledAt     *time.Time `json:"cancelled_at"`
	CancelledReason string     `json:"cancelled_reason"`
	Project         string     `json:"project"`
	Kind            string     `json:"kind"`
	Spaces          []struct {
		Space string `json:"space"`
	} `json:"spaces"`
}

// Todos lists the to-dos updated since since. A to-do linked only to the health space is dropped.
func (h *HTTP) Todos(ctx context.Context, since time.Time) ([]Todo, error) {
	var r struct {
		Todos []hubTodo `json:"todos"`
	}
	if err := h.get(ctx, "todos", "/api/v1/todos", nil, &r); err != nil {
		return nil, err
	}
	var out []Todo
	for _, t := range r.Todos {
		if healthOnly(t) || t.Updated.Before(since) {
			continue
		}
		x := Todo{ID: t.ID, Title: t.Title, Project: t.Project, Updated: t.Updated, Created: t.Created, Kind: t.Kind}
		switch {
		case t.CancelledAt != nil:
			x.Outcome, x.Closed, x.Reason = "cancelled", *t.CancelledAt, t.CancelledReason
		case t.DoneAt != nil:
			x.Done, x.Outcome, x.Closed, x.Reason, x.By = true, "done", *t.DoneAt, t.DoneReason, t.DoneBy
		}
		if t.ReopenedAt != nil {
			x.Reopened = 1
		}
		out = append(out, x)
	}
	return out, nil
}

func healthOnly(t hubTodo) bool {
	if len(t.Spaces) == 0 {
		return false
	}
	for _, s := range t.Spaces {
		if s.Space != "health" {
			return false
		}
	}
	return true
}

// TodoDecisions reads a to-do's decisions, superseded ones included (all=1), oldest first.
func (h *HTTP) TodoDecisions(ctx context.Context, id string) ([]Decision, error) {
	var r struct {
		Decisions []struct {
			Question     string    `json:"question"`
			Choice       string    `json:"choice"`
			Reason       string    `json:"reason"`
			Source       string    `json:"source"`
			SupersededBy string    `json:"superseded_by"`
			Created      time.Time `json:"created_at"`
		} `json:"decisions"`
	}
	if err := h.get(ctx, "decisions", "/api/v1/todos/"+url.PathEscape(id)+"/decisions", url.Values{"all": {"1"}}, &r); err != nil {
		return nil, err
	}
	var out []Decision
	for _, d := range r.Decisions {
		out = append(out, NewDecision(d.Created, d.Source, d.Question, d.Choice, d.Reason, d.SupersededBy != ""))
	}
	return out, nil
}

// NewDecision is a decision with its one-line text.
func NewDecision(at time.Time, by, question, choice, reason string, replaced bool) Decision {
	text := strings.TrimSpace(question)
	if choice != "" {
		if text != "" {
			text += " → "
		}
		text += choice
	}
	if reason != "" {
		text += "（" + reason + "）"
	}
	return Decision{At: at, By: by, Text: text, Question: question, Choice: choice, Reason: reason, Replaced: replaced}
}

// AboutMe is the notes the assistant is told about the user (GET /api/v1/assistant, notes_effective): what the
// user wrote in the app's "about me", as the hub uses it.
func (h *HTTP) AboutMe(ctx context.Context) (string, error) {
	var r struct {
		Notes string `json:"notes_effective"`
	}
	if err := h.get(ctx, "about", "/api/v1/assistant", nil, &r); err != nil {
		return "", err
	}
	return r.Notes, nil
}

// Memories reads the assistant's hand-written memory with the RPC assistant.memory (H2, scope
// assistant.memory.read): the metadata, and the text with content. A hub without the method, or before the
// manifest declares the scope (it is declared once the hub that knows it is deployed), is *WaitingHub{"H2"}; the
// assistant's computer away is ErrOffline (missing for now, not an empty memory); no memory at all is an empty list.
func (h *HTTP) Memories(ctx context.Context, content bool) ([]MemoryFile, error) {
	if h.Call == nil {
		return nil, &WaitingHub{Change: "H2"}
	}
	params := map[string]any{}
	if content {
		params["include_content"] = true
	}
	var r struct {
		Entries []MemoryFile `json:"entries"`
	}
	if err := h.Call(ctx, "assistant.memory", params, &r); err != nil {
		return nil, memoryErr(err)
	}
	return r.Entries, nil
}

// memoryErr maps assistant.memory's errors by their code (the SDK's *ext.Error carries it).
func memoryErr(err error) error {
	var coded *ext.Error
	code := ""
	if errors.As(err, &coded) {
		code = coded.Code
	} else {
		msg := err.Error()
		for _, c := range []string{"unknown_method", "permission_denied", "machine_offline", "assistant_offline"} {
			if strings.Contains(msg, c) {
				code = c
			}
		}
	}
	switch code {
	case "unknown_method", "permission_denied":
		return &WaitingHub{Change: "H2"}
	case "machine_offline", "assistant_offline":
		return ErrOffline
	}
	return err
}

// Adoptions reads back what the hub took of the data contributions (assistant.data_status, hub #5834/#5865: no
// parameters, no permission, only the extension's own). A hub without the method waits for H1.
func (h *HTTP) Adoptions(ctx context.Context) ([]Adoption, error) {
	if h.Call == nil {
		return nil, &WaitingHub{Change: "H1"}
	}
	var r struct {
		Items []struct {
			Contribution string     `json:"contribution"`
			Kind         string     `json:"kind"`
			State        string     `json:"state"`
			Reason       string     `json:"reason"`
			Detail       string     `json:"detail"`
			Hash         string     `json:"hash"`
			AdoptedHash  string     `json:"adopted_hash"`
			AdoptedAt    *time.Time `json:"adopted_at"`
			NextAdoptAt  *time.Time `json:"next_adopt_at"`
			Truncated    bool       `json:"truncated"`
		} `json:"items"`
	}
	if err := h.Call(ctx, "assistant.data_status", map[string]any{}, &r); err != nil {
		var coded *ext.Error
		if errors.As(err, &coded) && coded.Code == "unknown_method" || strings.Contains(err.Error(), "unknown_method") {
			return nil, &WaitingHub{Change: "H1"}
		}
		return nil, err
	}
	var out []Adoption
	for _, it := range r.Items {
		a := Adoption{Exit: it.Kind, Hash: it.AdoptedHash, Truncated: it.Truncated, Reason: it.Reason, State: it.State,
			Detail: it.Detail}
		if it.AdoptedAt != nil {
			a.At = *it.AdoptedAt
		}
		if it.NextAdoptAt != nil {
			a.Earliest = *it.NextAdoptAt
		}
		// The context kinds are read whenever a prompt is built: adopted means the next prompt gets the file as it is.
		a.Current = it.State == "adopted" && (it.Hash == "" || it.AdoptedHash == "" || it.Hash == it.AdoptedHash)
		out = append(out, a)
	}
	return out, nil
}

// Notify sends a phone alert (POST /api/v1/notify). A muted or deviceless hub is not an error.
func (h *HTTP) Notify(ctx context.Context, title, body string) error {
	var r struct {
		Sent   bool   `json:"sent"`
		Reason string `json:"reason"`
	}
	return h.do(ctx, "notify", http.MethodPost, "/api/v1/notify", nil, map[string]any{"title": title, "body": body}, &r)
}

// SplitAnswer splits a multi-select answer ("a, b") into its choices; a single answer stays whole.
func SplitAnswer(a string) []string {
	a = strings.TrimSpace(a)
	if a == "" {
		return nil
	}
	return []string{a}
}

var _ Hub = (*HTTP)(nil)
