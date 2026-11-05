package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
)

// fakeDetailAPI is GitHub's REST API for one made-up PR (example-org/example-repo#12), serving its reviews oldest
// first a page at a time, like GitHub does. reviewPages says which page numbers (1-based) were asked for.
func fakeDetailAPI(t *testing.T, reviews []string) (*Connector, *[]int) {
	routes := map[string]string{
		"/user": `{"login":"alice"}`,
		"/repos/example-org/example-repo/issues/12": `{"title":"Add the example","state":"open","user":{"login":"bob"},"comments":0,
			"html_url":"https://github.com/example-org/example-repo/pull/12","created_at":"2026-11-01T00:00:00Z",
			"pull_request":{"merged_at":null}}`,
		"/repos/example-org/example-repo/pulls/12": `{"draft":false,"merged":false,
			"head":{"ref":"feat/example","sha":"0123456789abcdef"},"base":{"ref":"main"},"requested_reviewers":[]}`,
		"/repos/example-org/example-repo/commits/0123456789abcdef/check-runs": `{"check_runs":[]}`,
	}
	var pages []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := routes[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		if r.URL.Path == "/repos/example-org/example-repo/pulls/12/reviews" {
			per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
			if per <= 0 {
				per = 100
			}
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if page <= 0 {
				page = 1
			}
			pages = append(pages, page)
			from := min((page-1)*per, len(reviews))
			to := min(page*per, len(reviews))
			_, _ = w.Write([]byte("[" + joinComma(reviews[from:to]) + "]"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	m := &machine{tokens: []string{oldToken}}
	c := New(Config{Auth: AuthConfig{FromMachine: "mac"}}, "Mac", m.fetch)
	c.api = srv.URL
	c.check(context.Background())
	return c, &pages
}

func joinComma(items []string) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += ","
		}
		out += it
	}
	return out
}

// A busy PR has more than a page of reviews (every reply in an inline thread is an empty COMMENTED review).
// A reviewer's latest verdict lands past the oldest 100, and a reviewer can first appear there too.
func TestDetailReviewsPastTheFirstPage(t *testing.T) {
	reviews := []string{
		// carol first asks for changes; then 99 inline-thread replies fill the first page.
		`{"id":1,"user":{"login":"carol"},"state":"CHANGES_REQUESTED","body":"Please split this","submitted_at":"2026-11-01T08:00:00Z"}`,
	}
	for i := 2; i <= 100; i++ {
		reviews = append(reviews, fmt.Sprintf(
			`{"id":%d,"user":{"login":"dave"},"state":"COMMENTED","body":"","submitted_at":"2026-11-01T09:%02d:00Z"}`, i, i%60))
	}
	// Page 2: carol approves, and erin's first review is an approval.
	reviews = append(reviews,
		`{"id":101,"user":{"login":"carol"},"state":"APPROVED","body":"LGTM","submitted_at":"2026-11-02T08:00:00Z"}`,
		`{"id":102,"user":{"login":"erin"},"state":"APPROVED","body":"","submitted_at":"2026-11-02T09:00:00Z"}`)

	c, pages := fakeDetailAPI(t, reviews)
	d, err := c.Detail(context.Background(), "example-org/example-repo", 12)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, rv := range d.Reviews {
		states[rv.Login] = rv.State
	}
	if states["carol"] != "APPROVED" {
		t.Errorf("carol's latest verdict = %q, want APPROVED (it is review #101)", states["carol"])
	}
	if states["erin"] != "APPROVED" {
		t.Errorf("erin's review = %q, want APPROVED (her first review is #102, past the first page)", states["erin"])
	}
	if !slices.Equal(*pages, []int{1, 2}) {
		t.Errorf("review pages fetched = %v, want [1 2]", *pages)
	}
}

// A PR that fills a page exactly still reads the empty page 2, so the loop ends on a short page, not on a full one.
func TestDetailReviewsExactAndEmptyPages(t *testing.T) {
	cases := []struct {
		name string
		n    int
		want []int
	}{
		{"no reviews", 0, []int{1}},
		{"exactly one full page", 100, []int{1, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reviews []string
			for i := range tc.n {
				reviews = append(reviews, fmt.Sprintf(
					`{"id":%d,"user":{"login":"dave"},"state":"COMMENTED","body":"","submitted_at":"2026-11-01T08:00:00Z"}`, i+1))
			}
			c, pages := fakeDetailAPI(t, reviews)
			d, err := c.Detail(context.Background(), "example-org/example-repo", 12)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(*pages, tc.want) {
				t.Errorf("pages fetched = %v, want %v", *pages, tc.want)
			}
			if tc.n == 0 && len(d.Reviews) != 0 {
				t.Errorf("reviews = %v, want none", d.Reviews)
			}
		})
	}
}
