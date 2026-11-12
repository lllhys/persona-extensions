package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

const syntheticToken = "synthetic-not-a-real-token"

type fakeRaindrop struct {
	server *httptest.Server
	mu     sync.Mutex
	posts  []map[string]any
	paths  []string
	auth   []string
	count  int // the all-bookmarks count; 100 when unset
}

func newFakeRaindrop(t *testing.T) *fakeRaindrop {
	t.Helper()
	f := &fakeRaindrop{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/v1/collections":
			fmt.Fprint(w, `{"result":true,"items":[{"_id":10,"title":"Research"},{"_id":11,"title":"Work","parent":{"$id":10}}]}`)
		case "/rest/v1/collections/childrens":
			fmt.Fprint(w, `{"result":true,"items":[{"_id":12,"title":"AI","parent":{"$id":10}}]}`)
		case "/rest/v1/raindrops/0":
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			items := make([]raindrop, 50)
			for i := range items {
				items[i] = raindrop{ID: int64(page*50 + i + 1), Title: fmt.Sprintf("Article %d", i), Link: "https://example.com/article", Domain: "example.com", Tags: []string{"research", "reading"}, Excerpt: "A synthetic excerpt"}
			}
			f.mu.Lock()
			count := cmp.Or(f.count, 100)
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "items": items, "count": count, "page": page})
		case "/rest/v1/raindrops/12":
			fmt.Fprint(w, `{"result":true,"items":[{"_id":9007199254740993,"title":"Exact ID","link":"https://example.com/exact","tags":["go","go"]}],"count":1,"page":0}`)
		case "/rest/v1/raindrop/9007199254740993":
			fmt.Fprint(w, `{"result":true,"item":{"_id":9007199254740993,"title":"Exact ID","link":"https://example.com/exact","note":"Remember this","highlights":[{"_id":"h1","text":"Useful passage","note":"Synthetic"}]}}`)
		case "/rest/v1/raindrop":
			var in map[string]any
			_ = json.NewDecoder(io.LimitReader(r.Body, 65536)).Decode(&in)
			f.mu.Lock()
			f.posts = append(f.posts, in)
			f.mu.Unlock()
			fmt.Fprint(w, `{"result":true,"item":{"_id":501,"title":"Saved article","link":"https://example.com/new","collection":{"_id":12,"title":"AI"}}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"result":false,"errorMessage":"not found"}`)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeRaindrop) app() *app {
	return &app{api: newAPIClient(f.server.URL, syntheticToken), receipts: newSaveStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeRaindrop(t).app())), json.RawMessage(`{"default_collection":"0"}`))
}

func TestManifestMatchesProgram(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != about.ID || m.Version != about.Version || len(m.Secrets) != 1 || m.Secrets[0].Name != "access_token" {
		t.Fatalf("unexpected identity/secrets: %+v", m)
	}
	if got := m.Permissions.Network; !slices.Equal(got, []string{"api.raindrop.io"}) {
		t.Fatalf("network %v", got)
	}
	if len(m.Contributes.Connectors) != 1 || !slices.Equal(m.Contributes.Connectors[0].ConfirmAlways, []string{"raindrop_save"}) {
		t.Fatal("saving must always pass the hub's write confirmation")
	}
	for _, platform := range []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"} {
		var paths map[string]string
		_ = json.Unmarshal(m.Runtime.Command, &paths)
		if paths[platform] != "bin/raindrop-"+platform {
			t.Fatalf("runtime %s: %v", platform, paths)
		}
	}
}

func TestSearchPreservesIDsAndEscapesTags(t *testing.T) {
	f := newFakeRaindrop(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "raindrop_search", json.RawMessage(`{"collection":"Research / AI","query":"go","tags":["go"],"limit":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "9007199254740993") || !strings.Contains(text, "Exact ID") {
		t.Fatal(text)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.paths[len(f.paths)-1] != "/rest/v1/raindrops/12?page=0&perpage=50&search=go+%23%22go%22" {
		t.Fatalf("query: %s", f.paths[len(f.paths)-1])
	}
	for _, auth := range f.auth {
		if auth != "Bearer "+syntheticToken {
			t.Fatalf("unexpected auth %q", auth)
		}
	}
}

func TestSearchPagesAreZeroBasedAndBounded(t *testing.T) {
	f := newFakeRaindrop(t)
	if _, err := f.app().callTool(t.Context(), "raindrop_search", json.RawMessage(`{"pages":99}`)); err == nil {
		t.Fatal("out-of-range page count was silently changed")
	}
	text, err := f.app().callTool(t.Context(), "raindrop_search", json.RawMessage(`{"page":1,"pages":4,"limit":50}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "ID: 51") || strings.Contains(text, "ID: 1\n") {
		t.Fatalf("wrong page: %s", text)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.paths) != 4 || f.paths[len(f.paths)-1] != "/rest/v1/raindrops/0?page=4&perpage=50" {
		t.Fatalf("unbounded pagination: %v", f.paths)
	}
	u, _ := url.Parse(f.paths[0])
	if u.Query().Get("page") != "1" {
		t.Fatal(f.paths[0])
	}
}

func TestReadDetailsAndUntrustedNewlines(t *testing.T) {
	f := newFakeRaindrop(t)
	text, err := f.app().callTool(t.Context(), "raindrop_read", json.RawMessage(`{"id":"9007199254740993"}`))
	if err != nil || !strings.Contains(text, "Remember this") || !strings.Contains(text, "Useful passage") {
		t.Fatalf("%v %s", err, text)
	}
	if _, err := f.app().callTool(t.Context(), "raindrop_read", json.RawMessage(`{"id":9007199254740993}`)); err != nil {
		t.Fatal("large integer IDs must survive JSON: " + err.Error())
	}
	if got := clipText("first line\nsecond paragraph", 100); !strings.Contains(got, "\n") {
		t.Fatal("note newlines were erased")
	}
}

func TestSaveNeedsConfirmationAndResolvesCollection(t *testing.T) {
	f := newFakeRaindrop(t)
	a := f.app()
	if _, err := a.listCollections(t.Context()); err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"url":"https://example.com/new#section","collection":"Research / AI","tags":["#ai","AI"]}`)
	if _, err := a.connector(t.Context(), "tools.call", json.RawMessage(`{"name":"raindrop_save","args":`+string(args)+`}`)); err == nil {
		t.Fatal("a read-only tool performed a write")
	}
	if _, err := a.connector(t.Context(), "actions.do", json.RawMessage(`{"name":"raindrop_save","args":`+string(args)+`}`)); err == nil {
		t.Fatal("a write without a hub request_id was accepted")
	}
	result, err := a.describeSave(args)
	if err != nil || !strings.Contains(result["summary"].(string), "Research / AI") {
		t.Fatalf("%v %+v", err, result)
	}
	raw, _ := json.Marshal(map[string]any{"contribution": "raindrop", "name": "raindrop_save", "args": json.RawMessage(args), "request_id": "req-1"})
	for range 2 {
		out, err := a.connector(t.Context(), "actions.do", raw)
		if err != nil {
			t.Fatal(err)
		}
		if out.(map[string]any)["message_ref"] != "501" {
			t.Fatal(out)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.posts) != 1 || f.posts[0]["collection"].(map[string]any)["$id"] != float64(12) {
		t.Fatalf("posts: %+v", f.posts)
	}
	if f.posts[0]["link"] != "https://example.com/new" || len(f.posts[0]["tags"].([]any)) != 1 {
		t.Fatalf("normalization: %+v", f.posts[0])
	}
}

func TestSaveRejectsDifferentArgumentsForReceipt(t *testing.T) {
	f := newFakeRaindrop(t)
	a := f.app()
	if _, err := a.saveOnce(t.Context(), "request-1", json.RawMessage(`{"url":"https://example.com/first"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveOnce(t.Context(), "request-1", json.RawMessage(`{"url":"https://example.com/second"}`)); err == nil {
		t.Fatal("reused request_id changed what was approved")
	}
}

func TestInvalidSaveNeverCallsAPI(t *testing.T) {
	f := newFakeRaindrop(t)
	a := f.app()
	for _, raw := range []string{
		`{"url":"file:///etc/passwd"}`, `{"url":"https://me@example.com/private"}`, `{"url":"https://example.com","collection_id":-99}`,
		`{"url":"https://example.com","tags":[""]}`, `{"url":"https://example.com","unknown":true}`,
	} {
		if _, err := a.saveOnce(t.Context(), "req-"+raw, json.RawMessage(raw)); err == nil {
			t.Fatal("accepted " + raw)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.posts) != 0 {
		t.Fatalf("unexpected writes: %+v", f.posts)
	}
}

func TestCollectionCachePreventsChangingDestination(t *testing.T) {
	f := newFakeRaindrop(t)
	a := f.app()
	if _, err := a.listCollections(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := a.resolveSaveCollection(&saveArgs{Collection: "Research / AI"}); err != nil {
		t.Fatal(err)
	}
	var paths int
	f.mu.Lock()
	paths = len(f.paths)
	f.mu.Unlock()
	if err := a.resolveSaveCollection(&saveArgs{Collection: "Research / AI"}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.paths) != paths {
		t.Fatalf("confirmation re-read collections: %v", f.paths)
	}
}

func TestPageViewsAndSave(t *testing.T) {
	f := newFakeRaindrop(t)
	a := f.app()
	for _, locale := range []string{"en", "zh-Hans", "ja"} {
		for _, ui := range []int{1, 3, 6} {
			v, err := a.render(t.Context(), &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}})
			if err != nil {
				t.Fatal(err)
			}
			if err := wire.CheckView(v); err != nil {
				t.Fatalf("%s ui%d: %v", locale, ui, err)
			}
		}
	}
	v, err := a.page(t.Context(), &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6}}, pageState{Mode: "form", DraftID: "page-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Sections[0].Items) < 6 || v.Sections[0].Items[4].Kind != "choice" {
		t.Fatal("save page lost its collection field")
	}
	if len(v.Sections[0].Items[4].Choices) != 4 {
		t.Fatalf("collection list: %+v", v.Sections[0].Items[4].Choices)
	}
	save := v.Sections[0].Items[len(v.Sections[0].Items)-1]
	if save.Action == nil || !save.Action.Submit || save.Action.ID != "review-save" {
		t.Fatal("save page lacks a review step")
	}
	v, err = a.page(t.Context(), &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6}}, pageState{
		Mode: "review", DraftID: "page-1", Save: saveArgs{URL: "https://example.com/page", CollectionID: 12},
	})
	if err != nil {
		t.Fatal(err)
	}
	save = v.Sections[0].Items[len(v.Sections[0].Items)-1]
	if save.Action == nil || save.Action.Confirm == "" {
		t.Fatal("the confirmed save has no confirmation")
	}
	res, err := a.saveFromPage(t.Context(), &ext.UIRequest{UIParams: wire.UIParams{Values: map[string]string{
		"url": "https://example.com/page", "collection": "12",
	}}}, pageState{Mode: "review", DraftID: "page-1", Save: saveArgs{URL: "https://example.com/page", CollectionID: 12}})
	if err != nil || !res.Pop || res.Toast == "" {
		t.Fatalf("%v %+v", err, res)
	}
	v, err = a.page(t.Context(), &ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{Pages: 1})
	if err != nil || wire.CheckView(v) != nil {
		t.Fatalf("%v %+v", err, v)
	}
}

// Every Load more the page offers shows 50 more bookmarks: it is offered only while search can read another page.
func TestLoadMoreStopsAtWhatSearchReads(t *testing.T) {
	f := newFakeRaindrop(t)
	f.count = 1000
	a := f.app()
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}
	v, err := a.render(t.Context(), r)
	for shown := 50; ; shown += 50 {
		if err != nil {
			t.Fatal(err)
		}
		rows := 0
		for _, c := range v.Sections[0].Items {
			if c.Type == wire.ComponentRow {
				rows++
			}
		}
		if rows != shown {
			t.Fatalf("after Load more: %d bookmarks, want %d", rows, shown)
		}
		if v.Sections[0].More == nil {
			if shown != 50*searchPagesMost {
				t.Fatalf("Load more stopped at %d", shown)
			}
			return
		}
		r.State, r.Action = v.State, v.Sections[0].More.Action.ID
		var res *wire.ActionResult
		res, err = a.action(t.Context(), r)
		if err == nil {
			v = res.View
		}
	}
}

func TestDetailViewBounds(t *testing.T) {
	a := newFakeRaindrop(t).app()
	v := a.detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{Mode: "detail", Bookmark: 1}, raindrop{
		ID: 1, Title: "Article", Domain: "example.com", Link: "https://example.com", Note: strings.Repeat("note ", 500),
		Highlights: []highlight{{Text: strings.Repeat("quote ", 1500), Note: strings.Repeat("comment ", 500)}},
	})
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
}

func TestAPIErrorsDoNotExposeToken(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			fmt.Fprint(w, syntheticToken)
		}))
		_, err := newAPIClient(s.URL, syntheticToken).collections(t.Context())
		s.Close()
		if err == nil || strings.Contains(err.Error(), syntheticToken) {
			t.Fatalf("status %d: %v", status, err)
		}
	}
	if _, err := newAPIClient("http://127.0.0.1:1", syntheticToken).collections(t.Context()); !errors.Is(err, errUnreachable) {
		t.Fatal(err)
	}
}

func TestNoTokenAndStatus(t *testing.T) {
	a := newApp()
	if a.status()["state"] != "not_configured" {
		t.Fatal(a.status())
	}
	if _, err := a.callTool(t.Context(), "raindrop_search", nil); !errors.Is(err, errMissingToken) {
		t.Fatal(err)
	}
	in := &ext.Init{}
	in.Secrets = map[string]string{"access_token": syntheticToken}
	in.InitializeParams.Config = json.RawMessage(`{"default_collection":"bad"}`)
	if _, err := a.init(t.Context(), in, nil); err == nil {
		t.Fatal("invalid collection accepted")
	}
	in.InitializeParams.Config = json.RawMessage(`{"default_collection":"12"}`)
	if _, err := a.init(t.Context(), in, nil); err != nil || a.defaultID != 12 {
		t.Fatal(err)
	}
}

func TestSaveReceiptSurvivesRestartAndUnknownOutcome(t *testing.T) {
	f := newFakeRaindrop(t)
	a := f.app()
	store, err := openSaveStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := json.RawMessage(`{"url":"https://example.com/persisted"}`)
	if _, err := a.saveOnce(t.Context(), "restart-request", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openSaveStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveOnce(t.Context(), "restart-request", args); err != nil {
		t.Fatal(err)
	}
	if len(f.posts) != 1 {
		t.Fatal("restart repeated a completed remote write")
	}
	if _, err := a.saveOnce(t.Context(), "restart-request", json.RawMessage(`{"url":"https://example.com/other"}`)); err == nil {
		t.Fatal("a receipt accepted changed parameters")
	}
	var normalized saveArgs
	if err := json.Unmarshal(args, &normalized); err != nil {
		t.Fatal(err)
	}
	if err := normalizeSave(&normalized); err != nil {
		t.Fatal(err)
	}
	key, _ := json.Marshal(normalized)
	receipts := map[string]saveReceipt{"unknown-request": {Key: string(key), Status: "pending", StartedAt: time.Now().UTC()}}
	rawReceipts, _ := json.Marshal(receipts)
	if err := os.WriteFile(store.path, rawReceipts, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openSaveStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveOnce(t.Context(), "unknown-request", args); err == nil || !strings.Contains(err.Error(), "check Raindrop before retrying") {
		t.Fatalf("unknown save outcome was not preserved: %v; receipts=%+v; key=%s", err, a.receipts.entries, key)
	}
	if len(f.posts) != 1 {
		t.Fatal("unknown remote result caused another POST")
	}
}

func TestRedirectDoesNotForwardToken(t *testing.T) {
	var redirected bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			redirected = true
		}
		w.Header().Set("Location", "/redirected")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	if _, err := newAPIClient(s.URL, syntheticToken).collections(t.Context()); err == nil {
		t.Fatal("redirect accepted")
	}
	if redirected {
		t.Fatal("API token was forwarded to a redirect")
	}
}

func TestLargeResponseAndNoMutation(t *testing.T) {
	f := newFakeRaindrop(t)
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", 4<<20+1))
	})
	if _, err := f.app().callTool(t.Context(), "raindrop_search", nil); err == nil {
		t.Fatal("oversized response accepted")
	}
}
