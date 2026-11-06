package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const manySHA = "0123456789abcdef0123456789abcdef01234567"

// fakeManyChecksAPI is GitHub's REST API for one made-up PR (example-org/example-repo#12) whose head commit has a
// matrix build: total check runs, all passed but the last, "matrix (N)", which failed. They are served 100 a page
// like GitHub does; a page past the last is a 404, a tripwire for a loop that does not stop. The combined status has
// 40 statuses, the last one failed, and GitHub's default of 30 a page when per_page is not asked for. pages says which
// check-runs page numbers were asked for, statusQueries the query of each combined-status request.
func fakeManyChecksAPI(t *testing.T, total int) (c *Connector, pages *[]int, statusQueries *[]string) {
	routes := map[string]string{
		"/user": `{"login":"alice"}`,
		"/repos/example-org/example-repo/issues/12": `{"title":"Add the example","state":"open","user":{"login":"bob"},"comments":0,
			"html_url":"https://github.com/example-org/example-repo/pull/12","created_at":"2026-11-01T00:00:00Z",
			"pull_request":{"merged_at":null}}`,
		"/repos/example-org/example-repo/pulls/12": `{"draft":false,"merged":false,
			"head":{"ref":"feat/example","sha":"` + manySHA + `"},"base":{"ref":"main"},"requested_reviewers":[]}`,
		"/repos/example-org/example-repo/pulls/12/reviews": `[]`,
	}
	var ps []int
	var sq []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/example-org/example-repo/commits/" + manySHA + "/check-runs":
			per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
			if per <= 0 {
				per = 30
			}
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if page <= 0 {
				page = 1
			}
			ps = append(ps, page)
			if (page-1)*per > total {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			var runs []string
			for n := (page-1)*per + 1; n <= min(page*per, total); n++ {
				conclusion := "success"
				if n == total {
					conclusion = "failure"
				}
				runs = append(runs, fmt.Sprintf(`{"id":%d,"name":"matrix (%d)","status":"completed","conclusion":%q,
					"html_url":"https://ci.example.com/run/%d","completed_at":"2026-11-01T01:00:00Z","app":{"slug":"example-ci"}}`,
					n, n, conclusion, n))
			}
			fmt.Fprintf(w, `{"total_count":%d,"check_runs":[%s]}`, total, strings.Join(runs, ","))
			return
		case "/repos/example-org/example-repo/commits/" + manySHA + "/status":
			sq = append(sq, r.URL.RawQuery)
			per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
			if per <= 0 {
				per = 30
			}
			var sts []string
			for n := 1; n <= min(per, 40); n++ {
				state := "success"
				if n == 40 {
					state = "failure"
				}
				sts = append(sts, fmt.Sprintf(`{"context":"status (%d)","state":%q,"updated_at":"2026-11-01T01:00:00Z"}`, n, state))
			}
			fmt.Fprintf(w, `{"total_count":40,"statuses":[%s]}`, strings.Join(sts, ","))
			return
		}
		if body, ok := routes[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	t.Cleanup(srv.Close)
	m := &machine{tokens: []string{oldToken}}
	c = New(Config{Auth: AuthConfig{FromMachine: "mac"}}, "Mac", m.fetch)
	c.api = srv.URL
	c.check(context.Background())
	return c, &ps, &sq
}

// A matrix build has more than 100 check runs; the one that failed comes after the 100th. The PR page must not
// look green.
func TestDetailCountsCheckRunsPastTheFirstPage(t *testing.T) {
	c, pages, _ := fakeManyChecksAPI(t, 150)
	d, err := c.Detail(context.Background(), "example-org/example-repo", 12)
	if err != nil {
		t.Fatal(err)
	}
	if d.Checks.Passed != 149 || d.Checks.Failed != 1 || !slices.Equal(d.Checks.FailedNames, []string{"matrix (150)"}) {
		t.Errorf("checks = %+v, want 149 passed and matrix (150) failed", d.Checks)
	}
	if !slices.Equal(*pages, []int{1, 2}) {
		t.Errorf("check-runs pages fetched = %v, want [1 2]", *pages)
	}
}

// The review page's check list has every check run, and every status of the combined status (100 a page, not 30).
func TestChecksPastTheFirstPage(t *testing.T) {
	c, pages, statusQueries := fakeManyChecksAPI(t, 150)
	cks, err := c.Checks(context.Background(), "example-org/example-repo", manySHA)
	if err != nil {
		t.Fatal(err)
	}
	if len(cks) != 190 {
		t.Errorf("%d checks, want 150 check runs and 40 statuses", len(cks))
	}
	var failed []string
	for _, ck := range cks {
		if ck.State == "failure" {
			failed = append(failed, ck.Name)
		}
	}
	if !slices.Equal(failed, []string{"matrix (150)", "status (40)"}) {
		t.Errorf("failed checks = %v, want [matrix (150) status (40)] first", failed)
	}
	if !slices.Equal(*pages, []int{1, 2}) {
		t.Errorf("check-runs pages fetched = %v, want [1 2]", *pages)
	}
	if len(*statusQueries) != 1 || !strings.Contains((*statusQueries)[0], "per_page=100") {
		t.Errorf("combined status queries = %q, want one with per_page=100", *statusQueries)
	}
}

// The thread shows the check run that failed past the first page.
func TestThreadChecksPastTheFirstPage(t *testing.T) {
	c, pages, _ := fakeManyChecksAPI(t, 150)
	ms, err := c.checkMessages(context.Background(), "example-org/example-repo", manySHA, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 150 {
		t.Errorf("%d check messages, want 150", len(ms))
	}
	found := false
	for _, m := range ms {
		if m.ID == "ck150" {
			found = m.Check != nil && m.Check.State == "failure"
		}
	}
	if !found {
		t.Errorf("no failed check message ck150")
	}
	if !slices.Equal(*pages, []int{1, 2}) {
		t.Errorf("check-runs pages fetched = %v, want [1 2]", *pages)
	}
}

// github_checks lists every check run it counts, the failed one after the 100th included.
func TestChecksToolPastTheFirstPage(t *testing.T) {
	c, pages, _ := fakeManyChecksAPI(t, 150)
	out := call(t, c, "github_checks", `{"repo":"example-org/example-repo","number":12}`)
	mustContain(t, out, "150 checks on 0123456789ab", "matrix (1): success", "matrix (101): success",
		"matrix (150): failure https://ci.example.com/run/150")
	if n := strings.Count(out, "\nmatrix ("); n != 150 {
		t.Errorf("%d check runs listed, want 150", n)
	}
	if !slices.Equal(*pages, []int{1, 2}) {
		t.Errorf("check-runs pages fetched = %v, want [1 2]", *pages)
	}
}

// Exactly one full page still reads the empty page 2 only when total_count says there is more; 100 of 100 stops.
// Paging stops at checkRunPages however many GitHub says there are.
func TestCheckRunPagesStop(t *testing.T) {
	cases := []struct {
		name  string
		total int
		want  []int
	}{
		{"none", 0, []int{1}},
		{"exactly one full page", 100, []int{1}},
		{"over the cap", 1500, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, pages, _ := fakeManyChecksAPI(t, tc.total)
			if _, err := c.Checks(context.Background(), "example-org/example-repo", manySHA); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(*pages, tc.want) {
				t.Errorf("pages fetched = %v, want %v", *pages, tc.want)
			}
		})
	}
}
