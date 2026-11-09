package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

type fakeArxiv struct {
	srv   *httptest.Server
	mu    sync.Mutex
	paths []string
}

const sampleAtom = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:opensearch="http://a9.com/-/spec/opensearch/1.1/" xmlns:arxiv="http://arxiv.org/schemas/atom">
  <title>ArXiv Query</title>
  <entry>
    <id>https://arxiv.org/abs/2601.01234v1</id>
    <updated>2026-11-10T10:00:00Z</updated>
    <published>2026-11-10T09:00:00Z</published>
    <title>Preserving Reading Context in Personal Knowledge Tools</title>
    <summary>We study how keeping source context beside notes improves the next reading and review session.</summary>
    <author><name>Example Author</name></author>
    <author><name>Example Reviewer</name></author>
    <category term="cs.HC" />
    <link href="https://arxiv.org/abs/2601.01234v1" rel="alternate" type="text/html" />
    <link href="https://arxiv.org/pdf/2601.01234v1" rel="related" type="application/pdf" title="pdf" />
  </entry>
</feed>`

func newFakeArxiv(t *testing.T) *fakeArxiv {
	f := &fakeArxiv{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		if r.URL.Path != "/api/query" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/atom+xml")
		if r.URL.Query().Get("start") != "0" && r.URL.Query().Get("id_list") == "" {
			fmt.Fprint(w, `<feed xmlns="http://www.w3.org/2005/Atom"></feed>`)
			return
		}
		fmt.Fprint(w, sampleAtom)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestConformance(t *testing.T) {
	f := newFakeArxiv(t)
	exttest.Check(t, manifest, exttest.Serve(t, program(&app{client: newClient(f.srv.URL)})))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "arxiv" || len(m.Secrets) != 0 {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.Connectors[0].Capabilities) != 2 {
		t.Fatal("public arXiv should be read-only")
	}
}

func TestSearchRecentPaginationAndPaper(t *testing.T) {
	f := newFakeArxiv(t)
	a := &app{client: newClient(f.srv.URL)}
	text, err := a.callTool(t.Context(), "arxiv_search", json.RawMessage(`{"query":"reading context","start":20}`))
	if err != nil || !strings.Contains(text, "No papers") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "arxiv_search", json.RawMessage(`{"query":"reading context"}`))
	if err != nil || !strings.Contains(text, "Preserving Reading Context") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "arxiv_paper", json.RawMessage(`{"paper_id":"2601.01234v1"}`))
	if err != nil || !strings.Contains(text, "Example Reviewer") || !strings.Contains(text, "improves the next reading") {
		t.Fatalf("%v %s", err, text)
	}
	f.mu.Lock()
	paths := strings.Join(f.paths, "\n")
	f.mu.Unlock()
	for _, want := range []string{"start=20", "max_results=20", "search_query=all%3Areading+context", "id_list=2601.01234v1"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("missing %q in %s", want, paths)
		}
	}
}

func TestLocalizedViewsAndPaperID(t *testing.T) {
	f := newFakeArxiv(t)
	a := &app{client: newClient(f.srv.URL)}
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			for _, mode := range []string{"", "recent", "detail"} {
				r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
				v, err := a.page(t.Context(), r, pageState{Mode: mode, Query: "context", PaperID: "2601.01234v1"})
				if err != nil {
					t.Fatalf("%s %d %s: %v", locale, ui, mode, err)
				}
				if err := wire.CheckView(v); err != nil {
					t.Fatalf("%s %d %s: %v", locale, ui, mode, err)
				}
			}
		}
	}
	item := paper{ID: "https://arxiv.org/abs/2601.01234v1"}
	if got := paperID(item); got != "2601.01234v1" {
		t.Fatal(got)
	}
	item.ID = "http://arxiv.org/abs/2601.01234v1"
	if got := paperID(item); got != "" {
		t.Fatal("accepted insecure paper ID")
	}
}

func TestInvalidInputAndUnavailable(t *testing.T) {
	f := newFakeArxiv(t)
	a := &app{client: newClient(f.srv.URL)}
	if _, err := a.callTool(t.Context(), "arxiv_search", json.RawMessage(`{"start":-1}`)); err == nil {
		t.Fatal("accepted negative offset")
	}
	if _, err := a.callTool(t.Context(), "arxiv_paper", json.RawMessage(`{"paper_id":"bad/id"}`)); err == nil {
		t.Fatal("accepted path as paper ID")
	}
	f.srv.Close()
	if _, err := a.callTool(t.Context(), "arxiv_search", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "could not be reached") {
		t.Fatal(err)
	}
}

func TestStatus(t *testing.T) {
	a := newApp()
	if a.status()["state"] != "ok" {
		t.Fatal(a.status())
	}
	a.record(context.Canceled)
	if a.status()["state"] != "error" {
		t.Fatal(a.status())
	}
}
