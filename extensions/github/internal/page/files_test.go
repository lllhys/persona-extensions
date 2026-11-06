package page

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/github/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/github/internal/github"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The 改动 tab for apps below UI version 4 (filesTab) on PRs fakeGitHub's two files don't cover: files answers
// /pulls/12/files (page by page, 100 a page) and changed_files is how many there are.

func filesSignedIn(t *testing.T, changed int, files func(page int) []string) *Page {
	base, _ := fakeGitHub(t)
	up, _ := url.Parse(base.URL)
	proxy := httputil.NewSingleHostReverseProxy(up)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case repoAPI + "/pulls/12":
			_, _ = w.Write([]byte(`{"draft":false,"merged":false,"head":{"ref":"cache","sha":"abc123"},"base":{"ref":"main"},
"additions":` + strconv.Itoa(changed) + `,"deletions":0,"commits":1,"changed_files":` + strconv.Itoa(changed) + `,"mergeable":true,"mergeable_state":"clean"}`))
		case repoAPI + "/pulls/12/files":
			pg, _ := strconv.Atoi(r.URL.Query().Get("page"))
			var rows []string
			for _, name := range files(max(pg, 1)) {
				rows = append(rows, `{"filename":"`+name+`","status":"modified","additions":1,"deletions":0}`)
			}
			_, _ = w.Write([]byte("[" + strings.Join(rows, ",") + "]"))
		default:
			proxy.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := github.New(github.Config{}, "", nil)
	c.UseAPI(srv.URL, "https://github.com")
	c.UseSecrets(&memSecrets{data: []byte(`{"token":"gho_example"}`)})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for c.Status(ctx).State != connector.StateOK {
		if time.Now().After(deadline) {
			t.Fatalf("not signed in: %+v", c.Status(ctx))
		}
		time.Sleep(10 * time.Millisecond)
	}
	p := New(c)
	p.now = func() time.Time { return now }
	return p
}

func fileRows(v *wire.View) int {
	n := 0
	for _, s := range v.Sections {
		for _, c := range s.Items {
			if c.Type == wire.ComponentRow && strings.HasPrefix(c.Key, "f-") {
				n++
			}
		}
	}
	return n
}

// 450 files: 「还有 N 个文件」 loads 100 more until the tab is full, then the rest are on GitHub; the hub takes every
// page of it (wire.CheckView; exttest.CheckView doesn't count items).
func TestFilesTabStopsAtWhatTheHubTakes(t *testing.T) {
	p := filesSignedIn(t, 450, func(page int) []string {
		var names []string
		for i := (page - 1) * 100; i < page*100 && i < 450; i++ {
			names = append(names, fmt.Sprintf("vendor/pkg%03d/file.go", i))
		}
		return names
	})
	ctx := context.Background()
	for _, tc := range []struct {
		page, rows int
		more       bool
	}{{1, 100, true}, {2, 200, true}, {3, 280, false}, {9, 280, false}} {
		v, _ := p.Render(ctx, req(3, state{Repo: "example-org/example-repo", Number: 12, Tab: tabFiles, Page: tc.page}, "", nil))
		if err := wire.CheckView(v); err != nil {
			t.Fatalf("page %d: the hub refuses the 改动 tab: %v", tc.page, err)
		}
		if n := fileRows(v); n != tc.rows {
			t.Errorf("page %d: %d file rows, want %d", tc.page, n, tc.rows)
		}
		raw := checked(t, v)
		if more := v.Sections[1].More != nil; more != tc.more {
			t.Errorf("page %d: 「还有 N 个文件」 %v, want %v", tc.page, more, tc.more)
		}
		if !tc.more {
			has(t, "full", raw, "还有 170 个文件太多放不下，在 GitHub 上看")
		}
	}
}

// Two paths the same for their first 120 characters are two rows the hub tells apart.
func TestFilesTabLongPathsHaveTheirOwnKeys(t *testing.T) {
	dir := "src/test/resources/com/example/integration/fixtures/snapshots/very/deeply/nested/package/structure/for/generated/output/"
	if len([]rune(dir)) < 120 {
		t.Fatalf("dir is only %d characters", len([]rune(dir)))
	}
	p := filesSignedIn(t, 2, func(page int) []string {
		if page > 1 {
			return nil
		}
		return []string{dir + "alpha_test.snap", dir + "beta_test.snap"}
	})
	v, _ := p.Render(context.Background(), req(3, state{Repo: "example-org/example-repo", Number: 12, Tab: tabFiles}, "", nil))
	if err := wire.CheckView(v); err != nil {
		t.Fatalf("the hub refuses the 改动 tab: %v", err)
	}
	has(t, "rows", checked(t, v), "alpha_test.snap", "beta_test.snap")
}
