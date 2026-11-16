package hubapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/nwverse/persona/sdk/go/ext"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

// fakeHub answers the routes as the hub writes them (placeholder data only) and records the queries it got.
type fakeHub struct {
	mu      sync.Mutex
	queries []string // "<path>?<query>"
	deny    map[string]string
	errorsK bool // folds carry errors and tool_names (H3)
	paged   bool // the history applies since / before and pages with a cursor (H3)
}

func (f *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.queries = append(f.queries, r.URL.Path+"?"+r.URL.RawQuery)
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer tok-placeholder" {
		w.WriteHeader(401)
		return
	}
	if scope, ok := f.deny[r.URL.Path]; ok {
		w.WriteHeader(403)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "not granted", "code": "permission_denied", "scope": scope})
		return
	}
	t := func(s string) string { return "2026-11-0" + s + "T10:00:00Z" }
	var body any
	switch r.URL.Path {
	case "/api/v1/sessions":
		body = map[string]any{"sessions": []map[string]any{
			{"machine_id": "m1", "session_id": "s-work", "title": "project-x", "updated_at": t("3"), "assistant": false, "status": "done"},
			{"machine_id": "m1", "session_id": "s-health", "title": "health", "updated_at": t("3"), "assistant": true, "space": "health"},
			{"machine_id": "m1", "session_id": "s-main", "title": "main", "updated_at": t("3"), "assistant": true},
		}, "has_more": false}
	case "/api/v1/sessions/m1/s-work/items":
		fold := map[string]any{"items": 3, "tools": 2, "through_seq": 4, "changes": []map[string]any{{"path": "/repo/project-x/a.go"}}}
		if f.errorsK {
			fold["errors"], fold["tool_names"] = 1, []string{"Bash", "Edit"}
		}
		body = map[string]any{"items": []map[string]any{
			{"id": "1", "seq": 1, "ts": t("3"), "role": "user", "kind": "text", "text": "do the thing for user-a"},
			{"id": "1b", "seq": 1, "ts": t("3"), "role": "user", "kind": "text", "text": "a supervision note", "source": map[string]any{"kind": "supervision"}},
			{"id": "2", "seq": 2, "ts": t("3"), "role": "assistant", "kind": "fold", "fold": fold},
			{"id": "5", "seq": 5, "ts": t("3"), "role": "assistant", "kind": "text", "text": "done"},
		}}
	case "/api/v1/interactions/history":
		if f.paged {
			// An extension's token: the private and health cards are left out but still take their place, so the first
			// page is empty with a cursor, the second holds the one card with a cursor, the third ends.
			card := map[string]any{"interaction": map[string]any{"id": "c1", "machine_id": "m1", "session_id": "s-work", "kind": "question", "created_at": t("3"),
				"tool_name": "ask_user", "reversible": true, "deadline": t("4"),
				"questions": []map[string]any{{"question": "which option?", "recommend": "A (Recommended)", "options": []map[string]any{{"label": "A (Recommended)"}, {"label": "B"}}}}},
				"status": "resolved", "resolution": map[string]any{"answers": map[string]string{"which option?": "B"}}}
			switch r.URL.Query().Get("before_id") {
			case "":
				body = map[string]any{"interactions": []map[string]any{}, "next_before": t("4"), "next_before_id": "p2"}
			case "p2":
				body = map[string]any{"interactions": []map[string]any{card}, "next_before": t("3"), "next_before_id": "p3"}
			default:
				body = map[string]any{"interactions": []map[string]any{}}
			}
			break
		}
		body = map[string]any{"interactions": []map[string]any{
			{"interaction": map[string]any{"id": "c1", "machine_id": "m1", "session_id": "s-work", "kind": "question", "created_at": t("3"),
				"tool_name": "ask_user", "reversible": true, "deadline": t("4"),
				"questions": []map[string]any{{"question": "which option?", "recommend": "A (Recommended)", "options": []map[string]any{{"label": "A (Recommended)"}, {"label": "B"}}}}},
				"status": "resolved", "resolution": map[string]any{"answers": map[string]string{"which option?": "B"}}},
			{"interaction": map[string]any{"id": "c2", "machine_id": "m1", "session_id": "s-work", "kind": "question", "created_at": t("3"), "private": true,
				"questions": []map[string]any{{"question": "private one"}}}, "status": "resolved"},
			{"interaction": map[string]any{"id": "c3", "machine_id": "m1", "session_id": "s-health", "kind": "question", "created_at": t("3"),
				"questions": []map[string]any{{"question": "health one"}}}, "status": "resolved"},
			{"interaction": map[string]any{"id": "c4", "machine_id": "m1", "session_id": "s-work", "kind": "question", "created_at": t("1"),
				"questions": []map[string]any{{"question": "older one"}}}, "status": "dismissed"},
		}}
	case "/api/v1/todos":
		body = map[string]any{"todos": []map[string]any{
			{"id": "t1", "title": "ship project-x", "created_at": t("2"), "updated_at": t("3"), "done_at": t("3"), "done_reason": "merged"},
			{"id": "t2", "title": "health todo", "created_at": t("2"), "updated_at": t("3"), "spaces": []map[string]any{{"space": "health"}}},
		}}
	case "/api/v1/todos/t1/decisions":
		body = map[string]any{"decisions": []map[string]any{{"question": "which branch?", "choice": "main", "reason": "small change", "source": "card", "created_at": t("3")}}}
	case "/api/v1/assistant":
		body = map[string]any{"name": "x", "notes_effective": "I work on project-x."}
	case "/api/v1/notify":
		body = map[string]any{"sent": true}
	default:
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "no such route", "code": "not_found"})
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeHub) asked(path string) []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []url.Values
	for _, q := range f.queries {
		p, raw, _ := cutQuery(q)
		if p == path {
			v, _ := url.ParseQuery(raw)
			out = append(out, v)
		}
	}
	return out
}

func cutQuery(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '?' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func newHTTP(t *testing.T, f *fakeHub) *HTTP {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &HTTP{Base: srv.URL, Token: "tok-placeholder"}
}

func TestHTTPSessionsDropHealth(t *testing.T) {
	f := &fakeHub{}
	h := newHTTP(t, f)
	ss, err := h.Sessions(context.Background(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ss {
		if s.ID == "s-health" {
			t.Fatal("a health space session was listed")
		}
	}
	if len(ss) != 2 {
		t.Fatalf("sessions: %+v", ss)
	}
	if q := f.asked("/api/v1/sessions"); len(q) == 0 || q[0].Get("limit") == "" {
		t.Errorf("the paged list was not asked: %v", q)
	}
	items, err := h.Items(context.Background(), "m1", "s-health", ItemsQuery{Fold: true})
	if err != nil || len(items) != 0 || len(f.asked("/api/v1/sessions/m1/s-health/items")) != 0 {
		t.Errorf("a health space session's items were read: %v %v", items, err)
	}
}

func TestHTTPItemsFoldedAfter(t *testing.T) {
	f := &fakeHub{}
	h := newHTTP(t, f)
	items, err := h.Items(context.Background(), "m1", "s-work", ItemsQuery{Fold: true, After: 7})
	if err != nil {
		t.Fatal(err)
	}
	q := f.asked("/api/v1/sessions/m1/s-work/items")
	if len(q) != 1 || q[0].Get("fold_work") != "1" || q[0].Get("after_seq") != "7" || q[0].Has("through_seq") {
		t.Fatalf("query: %v", q)
	}
	kinds := map[string]int{}
	for _, it := range items {
		kinds[it.Kind]++
	}
	if kinds[KindUser] != 1 || kinds[KindNotice] != 1 || kinds[KindFold] != 1 || kinds[KindAgent] != 1 {
		t.Errorf("kinds: %v", kinds)
	}
	for _, it := range items {
		if it.Kind == KindFold {
			if it.Fold.Calls != 2 || it.Fold.To != 4 || len(it.Fold.Files) != 1 || it.Fold.Known {
				t.Errorf("fold without H3: %+v", it.Fold)
			}
		}
	}
	f.errorsK = true
	items, _ = h.Items(context.Background(), "m1", "s-work", ItemsQuery{Fold: true})
	for _, it := range items {
		if it.Kind == KindFold && (!it.Fold.Known || it.Fold.Errors != 1 || len(it.Fold.Tools) != 2) {
			t.Errorf("fold with H3: %+v", it.Fold)
		}
	}
	_, _ = h.Items(context.Background(), "m1", "s-work", ItemsQuery{From: 2, Through: 4})
	q = f.asked("/api/v1/sessions/m1/s-work/items")
	last := q[len(q)-1]
	if last.Get("from_seq") != "2" || last.Get("through_seq") != "4" || last.Has("fold_work") {
		t.Errorf("segment query: %v", last)
	}
}

func TestHTTPInteractionsFiltered(t *testing.T) {
	f := &fakeHub{}
	h := newHTTP(t, f)
	since := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	cs, err := h.Interactions(context.Background(), InteractionsQuery{Since: since, Before: since.AddDate(0, 0, 1)})
	var w *WaitingHub
	if !errors.As(err, &w) || w.Change != "H3" {
		t.Fatalf("a range without H3: want WaitingHub H3, got %v", err)
	}
	if len(cs) != 1 || cs[0].Question != "which option?" {
		t.Fatalf("private, health space and out-of-range cards must be dropped: %+v", cs)
	}
	c := cs[0]
	if len(c.Options) != 2 || c.Recommend != "A (Recommended)" || len(c.Answers) != 1 || c.Answers[0] != "B" || c.AnsweredBy != "user" ||
		c.Tool != "ask_user" || c.Reversible == nil || !*c.Reversible || c.Deadline.IsZero() {
		t.Errorf("card: %+v", c)
	}
	if q := f.asked("/api/v1/interactions/history"); len(q) != 1 || q[0].Get("limit") != "500" || !q[0].Has("since") {
		t.Errorf("an old hub is asked once, with the range it ignores: %v", q)
	}
	f.paged = true
	cs, err = h.Interactions(context.Background(), InteractionsQuery{Since: since})
	if err != nil || len(cs) != 1 || cs[0].Question != "which option?" {
		t.Fatalf("with H3, pages are followed past an empty one to the end: %v %+v", err, cs)
	}
	q := f.asked("/api/v1/interactions/history")
	if len(q) != 4 || q[2].Get("before_id") != "p2" || q[3].Get("before_id") != "p3" {
		t.Errorf("with H3 the cursor is followed until there is none: %v", q)
	}
}

func TestHTTPScopesAndWaits(t *testing.T) {
	f := &fakeHub{deny: map[string]string{"/api/v1/todos": "todos.read", "/api/v1/assistant": "assistant.ask"}}
	h := newHTTP(t, f)
	if _, err := h.Todos(context.Background(), time.Time{}); !errors.Is(err, ErrNoScope) {
		t.Errorf("403 with a scope: %v", err)
	}
	if _, err := h.AboutMe(context.Background()); !errors.Is(err, ErrNoScope) {
		t.Errorf("403 with a scope: %v", err)
	}
	var w *WaitingHub
	if _, err := h.Memories(context.Background(), true); !errors.As(err, &w) || w.Change != "H2" {
		t.Errorf("memories wait for H2: %v", err)
	}
	if _, err := h.Adoptions(context.Background()); !errors.Is(err, ErrWaitingHub) {
		t.Errorf("adoptions wait for H1: %v", err)
	}
}

func TestHTTPTodosAboutNotify(t *testing.T) {
	f := &fakeHub{}
	h := newHTTP(t, f)
	ts, err := h.Todos(context.Background(), time.Time{})
	if err != nil || len(ts) != 1 || !ts[0].Done || ts[0].Reason != "merged" {
		t.Fatalf("to-dos (health-only dropped): %+v %v", ts, err)
	}
	ds, err := h.TodoDecisions(context.Background(), "t1")
	if err != nil || len(ds) != 1 || ds[0].Choice != "main" || ds[0].By != "card" {
		t.Fatalf("decisions: %+v %v", ds, err)
	}
	if q := f.asked("/api/v1/todos/t1/decisions"); q[0].Get("all") != "1" {
		t.Errorf("decisions query: %v", q)
	}
	about, err := h.AboutMe(context.Background())
	if err != nil || about != "I work on project-x." {
		t.Errorf("about me: %q %v", about, err)
	}
	if err := h.Notify(context.Background(), "t", "b"); err != nil {
		t.Errorf("notify: %v", err)
	}
	if h.Read("items") != 0 || h.Read("todos") == 0 {
		t.Errorf("bytes read: items %d todos %d", h.Read("items"), h.Read("todos"))
	}
}

func TestHTTPMemories(t *testing.T) {
	h := &HTTP{}
	var w *WaitingHub
	if _, err := h.Memories(context.Background(), false); !errors.As(err, &w) || w.Change != "H2" {
		t.Fatalf("without an RPC: %v", err)
	}
	answer := func(code string) func(context.Context, string, any, any) error {
		return func(_ context.Context, method string, _ any, out any) error {
			if method != "assistant.memory" {
				t.Fatalf("method %s", method)
			}
			if code != "" {
				return &ext.Error{Message: "no", Code: code}
			}
			return json.Unmarshal([]byte(`{"entries":[{"name":"a.md","type":"feedback","size":3,"modified":"2026-11-03T00:00:00Z","truncated":true}]}`), out)
		}
	}
	for code, want := range map[string]error{"unknown_method": &WaitingHub{}, "permission_denied": &WaitingHub{}, "machine_offline": ErrOffline, "assistant_offline": ErrOffline} {
		h.Call = answer(code)
		if _, err := h.Memories(context.Background(), true); !errors.Is(err, want) {
			t.Errorf("%s: %v", code, err)
		}
	}
	h.Call = answer("")
	ms, err := h.Memories(context.Background(), true)
	if err != nil || len(ms) != 1 || !ms[0].Truncated || ms[0].Type != "feedback" || !ms[0].Created.IsZero() {
		t.Fatalf("entries: %v %+v", err, ms)
	}
}
