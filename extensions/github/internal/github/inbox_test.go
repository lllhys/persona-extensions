package github

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/github/internal/connector"
)

// fakeNotifications is GitHub's notifications and the threads they are about, on made-up data. The test changes
// notifications, prs and comments between polls; the server counts what it was asked.
type fakeNotifications struct {
	mu            sync.Mutex
	notifications string
	prs           map[string]string   // path -> JSON
	comments      map[string]string   // path -> JSON
	reviews       map[string][]string // path -> one JSON object per review, served by per_page / page
	notModified   bool                // answer 304 when If-Modified-Since comes
	failPage      int                 // answer this page of the notifications with a 502
	rateLeft      string              // X-RateLimit-Remaining of 5000 (default 4000)
	sinceSeen     []string
	requests      []string
	posted        []string
}

func (f *fakeNotifications) server(t *testing.T) *Connector {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", cmp.Or(f.rateLeft, "4000"))
		w.Header().Set("X-RateLimit-Reset", "9999999999")
		switch {
		case r.URL.Path == "/user":
			_, _ = w.Write([]byte(`{"login":"me"}`))
		case r.URL.Path == "/notifications":
			f.sinceSeen = append(f.sinceSeen, r.Header.Get("If-Modified-Since"))
			w.Header().Set("X-Poll-Interval", "90")
			if f.notModified && r.Header.Get("If-Modified-Since") != "" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			// Pages of per_page (at most 50), newest first, the next one named in Link, like GitHub.
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			page = max(page, 1)
			if page == f.failPage {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"message":"Server Error"}`))
				return
			}
			per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
			if per <= 0 || per > 50 {
				per = 50
			}
			var all []json.RawMessage
			_ = json.Unmarshal([]byte(f.notifications), &all)
			from, to := min((page-1)*per, len(all)), min(page*per, len(all))
			if to < len(all) {
				w.Header().Set("Link", fmt.Sprintf(`<%s/notifications?per_page=%d&page=%d>; rel="next", <%s/notifications?per_page=%d&page=%d>; rel="last"`,
					srv.URL, per, page+1, srv.URL, per, (len(all)+per-1)/per))
			}
			w.Header().Set("Last-Modified", "Mon, 02 Nov 2026 10:00:00 GMT")
			b, _ := json.Marshal(all[from:to])
			_, _ = w.Write(b)
		case r.Method == http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			f.posted = append(f.posted, r.URL.Path+" "+string(b))
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case f.prs[r.URL.Path] != "":
			_, _ = w.Write([]byte(f.prs[r.URL.Path]))
		case f.reviews[r.URL.Path] != nil:
			all := f.reviews[r.URL.Path]
			per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
			if per <= 0 || per > 100 {
				per = 30
			}
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			page = max(1, page)
			from, to := min((page-1)*per, len(all)), min(page*per, len(all))
			_, _ = w.Write([]byte("[" + strings.Join(all[from:to], ",") + "]"))
		case f.comments[r.URL.Path] != "":
			_, _ = w.Write([]byte(f.comments[r.URL.Path]))
		case strings.HasSuffix(r.URL.Path, "/comments") || strings.HasSuffix(r.URL.Path, "/reviews"):
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	t.Cleanup(srv.Close)
	c := New(Config{Auth: AuthConfig{FromMachine: "mac"}}, "Mac", (&machine{tokens: []string{oldToken}}).fetch)
	c.api = srv.URL
	c.check(context.Background())
	return c
}

func note(id, reason, typ, url, updated string) string {
	b, _ := json.Marshal(map[string]any{"id": id, "reason": reason, "updated_at": updated,
		"subject":    map[string]string{"title": "Subject " + id, "url": url, "type": typ},
		"repository": map[string]string{"full_name": "example-org/example-repo", "html_url": "https://github.com/example-org/example-repo"}})
	return string(b)
}

const (
	api12 = "/repos/example-org/example-repo/pulls/12"
	api7  = "/repos/example-org/example-repo/issues/7"
	t1    = "2026-11-02T09:00:00Z"
	t2    = "2026-11-02T09:30:00Z"
)

func openPR(reviewers string) string {
	return `{"title":"Add the example","state":"open","merged":false,"user":{"login":"alice"},"body":"示例描述\n第二行",
		"html_url":"https://github.com/example-org/example-repo/pull/12","requested_reviewers":[` + reviewers + `]}`
}

func things(t *testing.T, c *Connector) map[string]connector.InboxThing {
	out := map[string]connector.InboxThing{}
	list, _ := c.Collect(context.Background())
	for _, th := range list {
		out[th.Thread] = th
	}
	return out
}

func TestNotificationsBecomeThings(t *testing.T) {
	f := &fakeNotifications{prs: map[string]string{
		api12: openPR(`{"login":"me"}`),
		"/repos/example-org/example-repo/issues/12": `{"comments":1}`,
		api7: `{"title":"Example bug","state":"open","user":{"login":"bob"},"body":"出错了","html_url":"https://github.com/example-org/example-repo/issues/7","comments":0}`,
	}, comments: map[string]string{
		"/repos/example-org/example-repo/issues/12/comments": `[{"user":{"login":"bob"},"body":"请看一下示例","created_at":"2026-11-02T08:59:00Z"}]`,
	}}
	f.notifications = "[" + strings.Join([]string{
		note("1", "review_requested", "PullRequest", "https://api.github.com"+api12, t1),
		note("2", "assign", "Issue", "https://api.github.com"+api7, t1),
		note("4", "subscribed", "Issue", "https://api.github.com/repos/example-org/example-repo/issues/8", t1),
	}, ",") + "]" // CI: ci_test.go
	c := f.server(t)

	wait, moved, err := c.poll(context.Background())
	if err != nil || !moved || wait != 90*time.Second {
		t.Fatalf("poll: %v %v %v", wait, moved, err)
	}
	got := things(t, c)
	if len(got) != 2 {
		t.Fatalf("%d things: %+v", len(got), got)
	}
	pr := got["example-org/example-repo#12"]
	if pr.Kind != "review_requested" || pr.Label != "请你 review" || pr.Tier != "action" || pr.Done != "" || !pr.CanReply ||
		pr.Sender != "bob" || pr.Preview != "请看一下示例" || pr.URL != "https://github.com/example-org/example-repo/pull/12" ||
		pr.ReplyTo != "评论到 example-org/example-repo#12（仓库可见的人都能看到）" || pr.Last != t1 || pr.Why != "规则：请你 review" {
		t.Errorf("review request: %+v", pr)
	}
	if !strings.Contains(pr.Context, "GitHub PR example-org/example-repo#12：Add the example") || !strings.Contains(pr.Context, "等这些人 review：me") ||
		!strings.Contains(pr.Context, "bob：\n请看一下示例") {
		t.Errorf("context:\n%s", pr.Context)
	}
	if is := got["example-org/example-repo#7"]; is.Kind != "assign" || is.Sender != "bob" || is.Preview != "出错了" || is.Tier != "action" {
		t.Errorf("assignment: %+v", is)
	}

	// Nothing changed: GitHub answers 304 to If-Modified-Since, nothing is read.
	f.notModified = true
	before := len(f.requests)
	if _, moved, _ := c.poll(context.Background()); moved || len(f.requests) != before+1 || f.sinceSeen[len(f.sinceSeen)-1] == "" {
		t.Fatalf("304: moved %v, %d requests, since %q", moved, len(f.requests)-before, f.sinceSeen)
	}

	// Answered: my comment is the newest; the review request withdrawn without a review: read; the issue is gone
	// from the unread list: read elsewhere.
	f.notModified = false
	c.inbox.fullAt = time.Time{}
	f.comments["/repos/example-org/example-repo/issues/12/comments"] = `[{"user":{"login":"bob"},"body":"请看一下示例","created_at":"2026-11-02T08:59:00Z"},
		{"user":{"login":"me"},"body":"看过了","created_at":"2026-11-02T09:29:00Z"}]`
	f.notifications = "[" + note("1", "review_requested", "PullRequest", "https://api.github.com"+api12, t2) + "]"
	c.poll(context.Background())
	got = things(t, c)
	if got["example-org/example-repo#12"].Done != connector.DoneReplied {
		t.Errorf("answered: %+v", got["example-org/example-repo#12"])
	}
	if !got["example-org/example-repo#7"].Read {
		t.Errorf("no longer unread: %+v", got)
	}
}

func TestReviewRequestWithdrawnOrMerged(t *testing.T) {
	f := &fakeNotifications{prs: map[string]string{api12: openPR(""), "/repos/example-org/example-repo/issues/12": `{"comments":0}`},
		comments: map[string]string{}}
	f.notifications = "[" + note("1", "review_requested", "PullRequest", "https://api.github.com"+api12, t1) + "]"
	c := f.server(t)
	c.poll(context.Background())
	if th := things(t, c)["example-org/example-repo#12"]; th.Done != connector.DoneRead {
		t.Errorf("withdrawn: %+v", th)
	}

	f.comments["/repos/example-org/example-repo/pulls/12/reviews"] = `[{"user":{"login":"me"},"state":"APPROVED","body":"","submitted_at":"2026-11-02T09:10:00Z"}]`
	f.notifications = "[" + note("1", "review_requested", "PullRequest", "https://api.github.com"+api12, t2) + "]"
	c.inbox.fullAt = time.Time{}
	c.poll(context.Background())
	if th := things(t, c)["example-org/example-repo#12"]; th.Done != connector.DoneReplied {
		t.Errorf("reviewed: %+v", th)
	}

	f.prs[api12] = strings.Replace(openPR(`{"login":"me"}`), `"merged":false`, `"merged":true`, 1)
	f.notifications = "[" + note("1", "review_requested", "PullRequest", "https://api.github.com"+api12, "2026-11-02T10:00:00Z") + "]"
	c.inbox.fullAt = time.Time{}
	c.poll(context.Background())
	if th := things(t, c)["example-org/example-repo#12"]; th.Done != connector.DoneRead {
		t.Errorf("merged: %+v", th)
	}
}

// A busy PR has more than a page of reviews (each inline-thread reply is an empty COMMENTED one); the newest
// is on the last page, and is the one that counts.
func TestReviewsPastTheFirstPage(t *testing.T) {
	var rs []string
	for i := range 120 {
		rs = append(rs, fmt.Sprintf(`{"user":{"login":"bob"},"state":"COMMENTED","body":"","submitted_at":"2026-11-01T%02d:%02d:00Z"}`, i/60, i%60))
	}
	f := &fakeNotifications{prs: map[string]string{
		api12: strings.Replace(openPR(""), `"login":"alice"`, `"login":"me"`, 1),
		"/repos/example-org/example-repo/issues/12": `{"comments":0}`,
	}, comments: map[string]string{}, reviews: map[string][]string{
		api12 + "/reviews": append(rs, `{"user":{"login":"carol"},"state":"CHANGES_REQUESTED","body":"Please split this change","submitted_at":"2026-11-02T08:59:00Z"}`),
	}}
	f.notifications = "[" + note("1", "author", "PullRequest", "https://api.github.com"+api12, t1) + "]"
	c := f.server(t)
	if _, _, err := c.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if th := things(t, c)["example-org/example-repo#12"]; th.Tier != connector.TierAction || th.Sender != "carol" ||
		th.Preview != "要求修改 Please split this change" {
		t.Errorf("author: tier %q why %q sender %q preview %q", th.Tier, th.Why, th.Sender, th.Preview)
	}

	// My own review past the first page answers a review request.
	f.prs[api12] = openPR("")
	f.reviews[api12+"/reviews"] = append(rs, `{"user":{"login":"me"},"state":"APPROVED","body":"","submitted_at":"2026-11-02T08:59:00Z"}`)
	f.notifications = "[" + note("1", "review_requested", "PullRequest", "https://api.github.com"+api12, t2) + "]"
	c.inbox.fullAt = time.Time{}
	c.poll(context.Background())
	if th := things(t, c)["example-org/example-repo#12"]; th.Done != connector.DoneReplied {
		t.Errorf("reviewed: %+v", th)
	}
}

// With requests running low only the notifications are read.
func TestFewRequestsLeftReadsNoThreads(t *testing.T) {
	f := &fakeNotifications{prs: map[string]string{api12: openPR(`{"login":"me"}`)}, comments: map[string]string{}}
	f.notifications = "[" + note("1", "review_requested", "PullRequest", "https://api.github.com"+api12, t1) + "]"
	c := f.server(t)
	h := http.Header{}
	h.Set("X-RateLimit-Limit", "5000")
	h.Set("X-RateLimit-Remaining", "100")
	h.Set("X-RateLimit-Reset", "9999999999")
	c.noteRate(h)
	if st := c.Status(context.Background()); st.State != connector.StateRateLimited || !strings.Contains(st.Error, "只看通知") {
		t.Fatalf("%+v", st)
	}
	f.mu.Lock()
	before := len(f.requests)
	f.mu.Unlock()
	var ns []notification
	_ = json.Unmarshal([]byte(f.notifications), &ns)
	c.applyNotifications(context.Background(), ns, true, time.Now())
	if len(f.requests) != before {
		t.Fatalf("threads were read with few requests left: %v", f.requests[before:])
	}
}

// noise is n newer notifications the inbox passes over (subscribed issues), to fill the first pages.
func noise(n int) []string {
	var out []string
	for i := range n {
		out = append(out, note(fmt.Sprintf("s%d", i), "subscribed", "Issue",
			fmt.Sprintf("https://api.github.com/repos/example-org/example-repo/issues/%d", 100+i), "2026-11-02T10:00:00Z"))
	}
	return out
}

func pagedPR() *fakeNotifications {
	return &fakeNotifications{prs: map[string]string{api12: openPR(`{"login":"me"}`), "/repos/example-org/example-repo/issues/12": `{"comments":0}`},
		comments: map[string]string{}}
}

// A review request behind 50 newer notifications is on the second page: it comes in all the same, and only the
// first page is asked with If-Modified-Since.
func TestReviewRequestPastFirstPageComesIn(t *testing.T) {
	f := pagedPR()
	f.notifications = "[" + strings.Join(append(noise(50), note("1", "review_requested", "PullRequest", "https://api.github.com"+api12, t1)), ",") + "]"
	c := f.server(t)
	if _, _, err := c.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := things(t, c)["example-org/example-repo#12"]; !ok {
		t.Fatalf("the review request on the second page is not in the inbox: %v", things(t, c))
	}
	c.inbox.lastModified = "Mon, 02 Nov 2026 10:00:00 GMT"
	f.sinceSeen = nil
	if _, _, err := c.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.sinceSeen) != 2 || f.sinceSeen[0] == "" || f.sinceSeen[1] != "" {
		t.Fatalf("If-Modified-Since by page: %q", f.sinceSeen)
	}
}

// A thing still unread on GitHub but pushed to the second page by newer notifications is not read elsewhere.
func TestUnreadThingPushedToSecondPageStaysUnread(t *testing.T) {
	f := pagedPR()
	f.notifications = "[" + note("1", "review_requested", "PullRequest", "https://api.github.com"+api12, t1) + "]"
	c := f.server(t)
	c.poll(context.Background())
	if th, ok := things(t, c)["example-org/example-repo#12"]; !ok || th.Read {
		t.Fatalf("first poll: %+v %v", th, ok)
	}
	f.mu.Lock()
	f.notifications = "[" + strings.Join(append(noise(50), note("1", "review_requested", "PullRequest", "https://api.github.com"+api12, t1)), ",") + "]"
	f.mu.Unlock()
	c.inbox.fullAt = time.Time{}
	if _, _, err := c.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if th := things(t, c)["example-org/example-repo#12"]; th.Read {
		t.Fatalf("still unread on GitHub but marked read: %+v", th)
	}
}

// When the pages stop early (the page cap, a page that fails, requests running low) the unread list is not
// whole, so nothing is marked read elsewhere; a whole read afterwards does.
func TestNotificationPagesStopEarlyMarkNothingRead(t *testing.T) {
	f := pagedPR()
	f.notifications = "[" + note("1", "review_requested", "PullRequest", "https://api.github.com"+api12, t1) + "]"
	c := f.server(t)
	c.poll(context.Background())
	f.notifications = "[" + strings.Join(noise(120), ",") + "]" // #12 read on GitHub, but on no page read below
	defer func(n int) { notificationPages = n }(notificationPages)
	for _, tc := range []struct {
		name     string
		pages    int
		fail     int
		rateLeft string
	}{{"page cap", 2, 0, ""}, {"page fails", 10, 2, ""}, {"requests low", 10, 0, "100"}} {
		notificationPages, f.failPage, f.rateLeft = tc.pages, tc.fail, tc.rateLeft
		c.inbox.fullAt = time.Time{}
		if _, _, err := c.poll(context.Background()); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if th := things(t, c)["example-org/example-repo#12"]; th.Read {
			t.Fatalf("%s: marked read from a partial list: %+v", tc.name, th)
		}
	}
	notificationPages, f.failPage, f.rateLeft = 10, 0, ""
	c.inbox.fullAt = time.Time{}
	c.poll(context.Background())
	if th := things(t, c)["example-org/example-repo#12"]; !th.Read {
		t.Fatalf("all three pages read: %+v", th)
	}
}

func TestReplyComments(t *testing.T) {
	f := &fakeNotifications{prs: map[string]string{}, comments: map[string]string{}}
	f.notifications = "[]"
	c := f.server(t)
	if err := c.Reply(context.Background(), connector.InboxReply{Thread: "example-org/example-repo#12", Text: "看过了"}); err != nil {
		t.Fatal(err)
	}
	if len(f.posted) != 1 || f.posted[0] != `/repos/example-org/example-repo/issues/12/comments {"body":"看过了"}` {
		t.Fatalf("%v", f.posted)
	}
	for _, bad := range []string{"notification:3", "../x#1", "example-org/example-repo#x"} {
		if err := c.Reply(context.Background(), connector.InboxReply{Thread: bad, Text: "x"}); err == nil {
			t.Errorf("%s was replied to", bad)
		}
	}
}

// An @ in a Discussion (GitHub sends no subject URL) or on a commit is a person's, not a CI run: it links to the
// discussions or the commit, and settling does not close it as an old per-notification CI item.
func TestDiscussionAndCommitMentionsAreNotCI(t *testing.T) {
	f := &fakeNotifications{}
	f.notifications = "[" + strings.Join([]string{
		note("77", "mention", "Discussion", "", t1),
		note("78", "mention", "Commit", "https://api.github.com/repos/example-org/example-repo/commits/abc123", t1),
	}, ",") + "]"
	c := f.server(t)
	if _, _, err := c.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, _ := c.Collect(context.Background())
	if len(list) != 2 {
		t.Fatalf("%d things: %+v", len(list), list)
	}
	want := map[string][2]string{ // title -> thread, URL
		"Subject 77": {"subject:example-org/example-repo@77", "https://github.com/example-org/example-repo/discussions"},
		"Subject 78": {"subject:example-org/example-repo@78", "https://github.com/example-org/example-repo/commit/abc123"},
	}
	var threads []string
	for _, th := range list {
		w := want[th.Title]
		if th.Thread != w[0] || th.URL != w[1] || th.Kind != "mention" || th.Sender == "GitHub Actions" ||
			strings.Contains(th.Context, "CI") || th.Tier != "action" || th.CanReply {
			t.Errorf("%s: thread %q url %q sender %q tier %q context %q", th.Title, th.Thread, th.URL, th.Sender, th.Tier, th.Context)
		}
		threads = append(threads, th.Thread)
	}
	if over := c.Settle(context.Background(), threads); len(over) != 0 {
		t.Errorf("settled %v", over)
	}
}
