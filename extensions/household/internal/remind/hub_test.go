package remind

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

const testToken = "test-token"

// recorded is one request the fake hub received.
type recorded struct {
	method, path, auth, contentType, body string
}

// fakeHub records every request and answers with status and reply.
type fakeHub struct {
	mu   sync.Mutex
	reqs []recorded
}

func (f *fakeHub) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func (f *fakeHub) last(t *testing.T) recorded {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		t.Fatal("the hub got no request")
	}
	return f.reqs[len(f.reqs)-1]
}

func newFakeHub(t *testing.T, status int, reply string) (*fakeHub, *httptest.Server) {
	t.Helper()
	f := &fakeHub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, recorded{
			method:      r.Method,
			path:        r.URL.EscapedPath(),
			auth:        r.Header.Get("Authorization"),
			contentType: r.Header.Get("Content-Type"),
			body:        string(b),
		})
		f.mu.Unlock()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

var allScopes = []string{ScopeNotify, ScopeTodos}

func TestScopeConstants(t *testing.T) {
	if ScopeNotify != "notify" || ScopeTodos != "todos.write" {
		t.Fatalf("scopes = %q, %q; want notify, todos.write", ScopeNotify, ScopeTodos)
	}
}

func TestCan(t *testing.T) {
	tests := []struct {
		name   string
		hub    *Hub
		scope  string
		expect bool
	}{
		{"granted notify", NewHub("https://example.com", testToken, []string{ScopeNotify}), ScopeNotify, true},
		{"todos not granted", NewHub("https://example.com", testToken, []string{ScopeNotify}), ScopeTodos, false},
		{"no scopes", NewHub("https://example.com", testToken, nil), ScopeNotify, false},
		{"no token", NewHub("https://example.com", "", allScopes), ScopeNotify, false},
		{"no url", NewHub("", testToken, allScopes), ScopeTodos, false},
		{"nil hub", nil, ScopeNotify, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.hub.Can(tt.scope); got != tt.expect {
				t.Fatalf("Can(%q) = %v, want %v", tt.scope, got, tt.expect)
			}
		})
	}
}

func TestNotify(t *testing.T) {
	notice := Notice{Title: "家庭管家：2 项需要处理", Body: "已过期：纯牛奶", ID: "household-2026-11-10", UI: "household"}
	tests := []struct {
		name    string
		status  int
		reply   string
		wantErr string // substring; "" means no error
	}{
		{"sent", http.StatusOK, `{"sent":true}`, ""},
		{"not sent is not an error", http.StatusOK, `{"sent":false,"reason":"no device"}`, ""},
		{"server error", http.StatusInternalServerError, "boom", "HTTP 500"},
		{"unauthorized", http.StatusUnauthorized, `{"error":"bad token"}`, "HTTP 401"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, srv := newFakeHub(t, tt.status, tt.reply)
			h := NewHub(srv.URL, testToken, allScopes)
			err := h.Notify(context.Background(), notice)
			if f.count() != 1 {
				t.Fatalf("requests = %d, want 1", f.count())
			}
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Notify: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Notify error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}

	t.Run("request", func(t *testing.T) {
		f, srv := newFakeHub(t, http.StatusOK, `{"sent":true}`)
		if err := NewHub(srv.URL, testToken, allScopes).Notify(context.Background(), notice); err != nil {
			t.Fatalf("Notify: %v", err)
		}
		r := f.last(t)
		if r.method != http.MethodPost || r.path != "/api/v1/notify" {
			t.Fatalf("request = %s %s, want POST /api/v1/notify", r.method, r.path)
		}
		if r.auth != "Bearer "+testToken {
			t.Fatalf("Authorization = %q", r.auth)
		}
		if r.contentType != "application/json" {
			t.Fatalf("Content-Type = %q", r.contentType)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(r.body), &got); err != nil {
			t.Fatalf("body is not JSON: %v", err)
		}
		want := map[string]any{
			"title": notice.Title,
			"body":  notice.Body,
			"level": "active",
			"id":    notice.ID,
			"ui":    notice.UI,
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("body[%q] = %v, want %v", k, got[k], v)
			}
		}
	})

	t.Run("scope not granted sends nothing", func(t *testing.T) {
		f, srv := newFakeHub(t, http.StatusOK, `{"sent":true}`)
		h := NewHub(srv.URL, testToken, []string{ScopeTodos})
		if err := h.Notify(context.Background(), notice); err == nil {
			t.Fatal("Notify without the notify scope: no error")
		}
		if f.count() != 0 {
			t.Fatalf("requests = %d, want 0", f.count())
		}
	})

	t.Run("unreachable hub", func(t *testing.T) {
		_, srv := newFakeHub(t, http.StatusOK, `{}`)
		addr := srv.URL
		srv.Close()
		if err := NewHub(addr, testToken, allScopes).Notify(context.Background(), notice); err == nil {
			t.Fatal("Notify to a closed server: no error")
		}
	})
}

func TestErrorBodyIsClipped(t *testing.T) {
	long := strings.Repeat("错误", 200) + "\n\n  more"
	_, srv := newFakeHub(t, http.StatusBadGateway, long)
	err := NewHub(srv.URL, testToken, allScopes).Notify(context.Background(), Notice{Title: "t"})
	if err == nil {
		t.Fatal("no error for HTTP 502")
	}
	msg := err.Error()
	prefix, tail, ok := strings.Cut(msg, "：")
	if !ok || !strings.Contains(prefix, "HTTP 502") {
		t.Fatalf("error = %q, want the status before the body", msg)
	}
	if len(tail) > 200 {
		t.Fatalf("body part is %d bytes, want at most 200", len(tail))
	}
	if !utf8.ValidString(tail) {
		t.Fatalf("body part is not valid UTF-8: %q", tail)
	}
	if strings.ContainsAny(tail, "\n\t") {
		t.Fatalf("body part is not one line: %q", tail)
	}
}

func TestClipBytes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"short", "abc", 200, "abc"},
		{"exact", strings.Repeat("a", 200), 200, strings.Repeat("a", 200)},
		{"ascii cut", strings.Repeat("a", 201), 200, strings.Repeat("a", 200)},
		{"multibyte not split", strings.Repeat("鸡", 100), 200, strings.Repeat("鸡", 66)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clipBytes(tt.in, tt.n)
			if got != tt.want {
				t.Fatalf("clipBytes len=%d, want len=%d", len(got), len(tt.want))
			}
		})
	}
}

func TestCreateTodo(t *testing.T) {
	todo := Todo{Title: "补货：纯牛奶（已过期）", Notes: "家庭管家自动创建", DueDate: "2026-11-10"}

	t.Run("request and id", func(t *testing.T) {
		f, srv := newFakeHub(t, http.StatusOK, `{"todo":{"id":"todo-1"}}`)
		id, err := NewHub(srv.URL, testToken, allScopes).CreateTodo(context.Background(), todo)
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
		if id != "todo-1" {
			t.Fatalf("id = %q, want todo-1", id)
		}
		r := f.last(t)
		if r.method != http.MethodPost || r.path != "/api/v1/todos" {
			t.Fatalf("request = %s %s, want POST /api/v1/todos", r.method, r.path)
		}
		if r.auth != "Bearer "+testToken {
			t.Fatalf("Authorization = %q", r.auth)
		}
		var got map[string]string
		if err := json.Unmarshal([]byte(r.body), &got); err != nil {
			t.Fatalf("body is not JSON: %v", err)
		}
		want := map[string]string{"title": todo.Title, "notes": todo.Notes, "due_date": todo.DueDate}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("body[%q] = %q, want %q", k, got[k], v)
			}
		}
	})

	tests := []struct {
		name    string
		status  int
		reply   string
		wantErr string
	}{
		{"no id in reply", http.StatusOK, `{"todo":{}}`, "编号"},
		{"server error", http.StatusInternalServerError, "nope", "HTTP 500"},
		{"assistant offline", http.StatusServiceUnavailable, "offline", "HTTP 503"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, srv := newFakeHub(t, tt.status, tt.reply)
			id, err := NewHub(srv.URL, testToken, allScopes).CreateTodo(context.Background(), todo)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("CreateTodo error = %v, want it to contain %q", err, tt.wantErr)
			}
			if id != "" {
				t.Fatalf("id = %q on error, want empty", id)
			}
		})
	}

	t.Run("scope not granted sends nothing", func(t *testing.T) {
		f, srv := newFakeHub(t, http.StatusOK, `{"todo":{"id":"todo-1"}}`)
		h := NewHub(srv.URL, testToken, []string{ScopeNotify})
		if _, err := h.CreateTodo(context.Background(), todo); err == nil {
			t.Fatal("CreateTodo without the todos.write scope: no error")
		}
		if f.count() != 0 {
			t.Fatalf("requests = %d, want 0", f.count())
		}
	})
}

func TestDoneTodo(t *testing.T) {
	t.Run("request", func(t *testing.T) {
		f, srv := newFakeHub(t, http.StatusOK, `{}`)
		if err := NewHub(srv.URL, testToken, allScopes).DoneTodo(context.Background(), "todo-1"); err != nil {
			t.Fatalf("DoneTodo: %v", err)
		}
		r := f.last(t)
		if r.method != http.MethodPatch || r.path != "/api/v1/todos/todo-1" {
			t.Fatalf("request = %s %s, want PATCH /api/v1/todos/todo-1", r.method, r.path)
		}
		if r.auth != "Bearer "+testToken {
			t.Fatalf("Authorization = %q", r.auth)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(r.body), &got); err != nil {
			t.Fatalf("body is not JSON: %v", err)
		}
		if len(got) != 1 || got["done"] != true {
			t.Fatalf("body = %s, want {\"done\":true}", r.body)
		}
	})

	t.Run("id is escaped in the path", func(t *testing.T) {
		f, srv := newFakeHub(t, http.StatusOK, `{}`)
		if err := NewHub(srv.URL, testToken, allScopes).DoneTodo(context.Background(), "a/b"); err != nil {
			t.Fatalf("DoneTodo: %v", err)
		}
		if p := f.last(t).path; p != "/api/v1/todos/a%2Fb" {
			t.Fatalf("path = %q, want /api/v1/todos/a%%2Fb", p)
		}
	})

	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"done", http.StatusOK, false},
		{"no content", http.StatusNoContent, false},
		{"already gone is success", http.StatusNotFound, false},
		{"server error", http.StatusInternalServerError, true},
		{"forbidden", http.StatusForbidden, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, srv := newFakeHub(t, tt.status, "")
			err := NewHub(srv.URL, testToken, allScopes).DoneTodo(context.Background(), "todo-1")
			if (err != nil) != tt.wantErr {
				t.Fatalf("DoneTodo error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}

	t.Run("empty id sends nothing", func(t *testing.T) {
		f, srv := newFakeHub(t, http.StatusOK, `{}`)
		if err := NewHub(srv.URL, testToken, allScopes).DoneTodo(context.Background(), ""); err == nil {
			t.Fatal("DoneTodo with an empty id: no error")
		}
		if f.count() != 0 {
			t.Fatalf("requests = %d, want 0", f.count())
		}
	})

	t.Run("scope not granted sends nothing", func(t *testing.T) {
		f, srv := newFakeHub(t, http.StatusOK, `{}`)
		h := NewHub(srv.URL, testToken, []string{ScopeNotify})
		if err := h.DoneTodo(context.Background(), "todo-1"); err == nil {
			t.Fatal("DoneTodo without the todos.write scope: no error")
		}
		if f.count() != 0 {
			t.Fatalf("requests = %d, want 0", f.count())
		}
	})
}

func TestTrailingSlashInURL(t *testing.T) {
	f, srv := newFakeHub(t, http.StatusOK, `{"sent":true}`)
	if err := NewHub(srv.URL+"/", testToken, allScopes).Notify(context.Background(), Notice{Title: "t"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if p := f.last(t).path; p != "/api/v1/notify" {
		t.Fatalf("path = %q, want /api/v1/notify", p)
	}
}

// Hub must satisfy API.
var _ API = (*Hub)(nil)
