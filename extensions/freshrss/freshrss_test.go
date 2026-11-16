package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

const testUsername = "reader"
const testPassword = "synthetic-freshrss-password"
const testAuth = "reader/synthetic-session-credential"
const itemID = "tag:google.com,2005:reader/item/000000000000002a"

type fakeFreshRSS struct {
	srv       *httptest.Server
	mu        sync.Mutex
	paths     []string
	logins    int
	reads     []url.Values
	failWrite bool
}

func sampleArticle() article {
	item := article{ID: itemID, Title: "Reading in context", Author: "Example Author", Published: 1760000000}
	item.Summary.Content = `<p>Keep the source beside the next step.</p><script>not rendered</script><form>discard this form</form>`
	item.Alternate = []struct {
		Href string `json:"href"`
	}{{Href: "https://articles.example.com/context"}}
	item.Origin.Title, item.Origin.StreamID = "Example Feed", "feed/7"
	return item
}

func newFakeFreshRSS(t *testing.T) *fakeFreshRSS {
	f := &fakeFreshRSS{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		switch r.URL.Path {
		case "/accounts/ClientLogin":
			if r.Method != http.MethodPost || r.FormValue("Email") != testUsername || r.FormValue("Passwd") != testPassword || r.URL.RawQuery != "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			f.mu.Lock()
			f.logins++
			f.mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("SID=" + testAuth + "\nAuth=" + testAuth + "\n"))
		case "/reader/api/0/stream/contents/reading-list", "/reader/api/0/stream/contents/user/-/state/com.google/starred":
			if r.Header.Get("Authorization") != "GoogleLogin auth="+testAuth {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(articlePage{Items: []article{sampleArticle()}, Continuation: "42"})
		case "/reader/api/0/subscription/list":
			_ = json.NewEncoder(w).Encode(map[string]any{"subscriptions": []subscription{{ID: "feed/7", Title: "Example Feed"}}})
		case "/reader/api/0/token":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("synthetic-action-token-value\n"))
		case "/reader/api/0/edit-tag":
			if f.failWrite {
				http.Error(w, "synthetic interrupted write", http.StatusServiceUnavailable)
				return
			}
			if err := r.ParseForm(); err != nil || r.FormValue("i") != itemID || r.FormValue("a") != "user/-/state/com.google/read" || r.FormValue("T") != "synthetic-action-token-value" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			f.reads = append(f.reads, r.Form)
			f.mu.Unlock()
			_, _ = w.Write([]byte("OK"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeFreshRSS) app() *app {
	return &app{client: newClient(f.srv.URL, testUsername, testPassword), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeFreshRSS(t).app())))
}

func TestManifestAndURL(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "freshrss" || len(m.Secrets) != 2 || len(m.Contributes.Connectors[0].ConfirmAlways) != 1 || !slices.Equal(m.Permissions.Network, []string{}) {
		t.Fatalf("%+v", m)
	}
	for _, raw := range []string{"https://freshrss.example.com/api/greader.php", "http://127.0.0.1:8080/api/greader.php"} {
		if _, err := instanceURL(raw); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{"http://freshrss.example.com", "https://me@example.com", "not-a-url"} {
		if _, err := instanceURL(raw); err == nil {
			t.Fatal("accepted " + raw)
		}
	}
}

func TestUnreadArticlesSanitizationAndSubscriptions(t *testing.T) {
	f := newFakeFreshRSS(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "freshrss_articles", json.RawMessage(`{"continuation":"42"}`))
	if err != nil || !strings.Contains(text, itemID) || !strings.Contains(text, "Next continuation: 42") || strings.Contains(text, "not rendered") || strings.Contains(text, "discard this form") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "freshrss_subscriptions", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(text, "feed/7") {
		t.Fatalf("%v %s", err, text)
	}
	if f.logins != 1 {
		t.Fatal("session was not reused")
	}
	f.mu.Lock()
	paths := strings.Join(f.paths, "\n")
	f.mu.Unlock()
	for _, want := range []string{"xt=user%2F-%2Fstate%2Fcom.google%2Fread", "c=42", "n=30"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("missing %q in %s", want, paths)
		}
	}
	if strings.Contains(paths, testPassword) {
		t.Fatal("password appeared in a URL")
	}
}

func TestActionTokenPlainText(t *testing.T) {
	a := newFakeFreshRSS(t).app()
	token, err := a.client.actionToken(t.Context())
	if err != nil || token != "synthetic-action-token-value" {
		t.Fatalf("token=%q, err=%v", token, err)
	}
}

func TestUnreadDetailAndReadReplay(t *testing.T) {
	f := newFakeFreshRSS(t)
	a := f.app()
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}
	v, err := a.page(t.Context(), r, pageState{Mode: "detail", View: "unread", ArticleID: itemID})
	if err != nil || wire.CheckView(v) != nil {
		t.Fatal("unread detail did not use its own reading view", err)
	}
	args := []byte(`{"item_id":"` + itemID + `"}`)
	for range 2 {
		if _, err := a.doAction(t.Context(), "read-1", "freshrss_mark_read", args); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.reads) != 1 {
		t.Fatal("read replay repeated the write")
	}
	if _, err := describeAction("freshrss_mark_read", args); err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "read-1", "freshrss_mark_read", []byte(`{"item_id":"tag:google.com,2005:reader/item/2b"}`)); err == nil {
		t.Fatal("request ID accepted another article")
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakeFreshRSS(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"item_id":"` + itemID + `"}`)
	if _, err := a.doAction(t.Context(), "persist-1", "freshrss_mark_read", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "freshrss_mark_read", args); err != nil {
		t.Fatal(err)
	}
	if len(f.reads) != 1 {
		t.Fatal("restart repeated read update")
	}
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(args), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "freshrss_mark_read", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown read update was retried")
	}
}

func TestLocalizedViewsAndBounds(t *testing.T) {
	f := newFakeFreshRSS(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			for _, mode := range []string{"", "detail", "subscriptions"} {
				r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
				st := pageState{Mode: mode, View: "unread", ArticleID: itemID}
				v, err := a.page(t.Context(), r, st)
				if err != nil {
					t.Fatalf("%s %d %s: %v", locale, ui, mode, err)
				}
				if err := wire.CheckView(v); err != nil {
					t.Fatalf("%s %d %s: %v", locale, ui, mode, err)
				}
			}
		}
	}
	if newApp().status()["state"] != "not_configured" {
		t.Fatal("unconfigured extension was ready")
	}
}

func TestFailedReadIsUnknown(t *testing.T) {
	f := newFakeFreshRSS(t)
	f.failWrite = true
	if _, err := f.app().doAction(t.Context(), "failed-1", "freshrss_mark_read", []byte(`{"item_id":"`+itemID+`"}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal("failed read update was safe to retry")
	}
}
