package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

type moreBookStack struct {
	srv   *httptest.Server
	mu    sync.Mutex
	paths []string
	full  bool
}

func (f *moreBookStack) records(offset int) []book {
	if !f.full && offset > 50 {
		return nil
	}
	n := 50
	if !f.full && offset == 50 {
		n = 1
	}
	out := make([]book, n)
	for i := range out {
		id := int64(offset + i + 1)
		out[i] = book{ID: id, Name: fmt.Sprintf("book-%d", id), Description: "synthetic"}
	}
	return out
}

func (f *moreBookStack) pageRecords(offset int) []page {
	if !f.full && offset > 50 {
		return nil
	}
	n := 50
	if !f.full && offset == 50 {
		n = 1
	}
	out := make([]page, n)
	for i := range out {
		id := int64(offset + i + 1)
		out[i] = page{ID: id, BookID: 7, Name: fmt.Sprintf("page-%d", id), UpdatedAt: "2026-11-10T10:00:00Z"}
	}
	return out
}

func (f *moreBookStack) searchRecords(page int) []searchResult {
	offset := (page - 1) * 50
	if !f.full && page > 2 {
		return nil
	}
	n := 50
	if !f.full && page == 2 {
		n = 1
	}
	out := make([]searchResult, n)
	for i := range out {
		id := int64(offset + i + 1)
		out[i] = searchResult{ID: id, BookID: 7, Name: fmt.Sprintf("page-%d", id), Type: "page"}
	}
	return out
}

func newMoreBookStack(t *testing.T, full bool) *moreBookStack {
	f := &moreBookStack{full: full}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token "+testTokenID+":"+testTokenSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		pageNumber, _ := strconv.Atoi(r.URL.Query().Get("page"))
		switch r.URL.Path {
		case "/api/books":
			_ = json.NewEncoder(w).Encode(listPage[book]{Data: f.records(offset), Total: 51})
		case "/api/pages":
			_ = json.NewEncoder(w).Encode(listPage[page]{Data: f.pageRecords(offset), Total: 51})
		case "/api/search":
			_ = json.NewEncoder(w).Encode(listPage[searchResult]{Data: f.searchRecords(pageNumber), Total: 51})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *moreBookStack) app() *app {
	return &app{client: newClient(f.srv.URL, testTokenID, testTokenSecret), receipts: newWriteStore("")}
}

func TestLoadMoreRetainsEarlierResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  pageState
		action string
		first  string
		last   string
	}{
		{name: "books", state: pageState{}, action: "more", first: "book-1", last: "book-51"},
		{name: "pages", state: pageState{Mode: "pages", BookID: 7, BookName: "Reading"}, action: "more", first: "page-1", last: "page-51"},
		{name: "search", state: pageState{Mode: "search", Search: "context", SearchPage: 1}, action: "more-search", first: "page-1", last: "page-51"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMoreBookStack(t, false)
			a := f.app()
			r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 3}}
			v, err := a.page(t.Context(), r, tc.state)
			if err != nil || len(v.Sections) == 0 || v.Sections[0].More == nil {
				t.Fatalf("initial view: %+v, %v", v, err)
			}
			result, err := a.action(t.Context(), withAction(r, tc.action, tc.state))
			if err != nil || result.View == nil {
				t.Fatalf("more action: %+v, %v", result, err)
			}
			items := result.View.Sections[0].Items
			if len(items) != 51 || items[0].Key != tc.first || items[len(items)-1].Key != tc.last {
				t.Fatalf("more did not retain the whole result set: first=%v last=%v count=%d", items[0], items[len(items)-1], len(items))
			}
			if result.View.Sections[0].More != nil {
				t.Fatal("a short final batch still offers more")
			}
			if err := wire.CheckView(result.View); err != nil {
				t.Fatal(err)
			}
			paths := strings.Join(f.paths, "\n")
			if tc.action == "more-search" {
				if !strings.Contains(paths, "page=1") || !strings.Contains(paths, "page=2") {
					t.Fatalf("search requests: %s", paths)
				}
			} else if !strings.Contains(paths, "offset=0") || !strings.Contains(paths, "offset=50") {
				t.Fatalf("offset requests: %s", paths)
			}
		})
	}
}

func TestLoadMoreStopsAtViewLimit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		state     pageState
		forbidden string
	}{
		{name: "books", state: pageState{Offset: 200}, forbidden: "offset=250"},
		{name: "pages", state: pageState{Mode: "pages", BookID: 7, BookName: "Reading", Offset: 200}, forbidden: "offset=250"},
		{name: "search", state: pageState{Mode: "search", Search: "context", SearchPage: 5}, forbidden: "page=6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMoreBookStack(t, true)
			v, err := f.app().page(t.Context(), &ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 3}}, tc.state)
			if err != nil {
				t.Fatal(err)
			}
			if len(v.Sections[0].Items) != 250 || v.Sections[0].More != nil {
				t.Fatalf("view cap: items=%d more=%+v", len(v.Sections[0].Items), v.Sections[0].More)
			}
			if err := wire.CheckView(v); err != nil {
				t.Fatal(err)
			}
			if paths := strings.Join(f.paths, "\n"); strings.Contains(paths, tc.forbidden) {
				t.Fatalf("loaded past the cap: %s", paths)
			}
		})
	}
}
