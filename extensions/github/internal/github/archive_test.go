package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeArchiveAPI is search and one PR's conversation on made-up data; comments after since are what since asks.
func fakeArchiveAPI(t *testing.T) (*Connector, *[]string) {
	var mu sync.Mutex
	var seen []string
	routes := map[string]string{
		"/user": `{"login":"me"}`,
		"/search/issues": `{"total_count":1,"items":[{"number":12,"title":"Add the example","state":"open","user":{"login":"alice"},
			"body":"示例描述","repository_url":"https://api.github.com/repos/example-org/example-repo","created_at":"2026-11-01T08:00:00Z",
			"updated_at":"2026-11-02T10:00:00Z","pull_request":{}}]}`,
		"/repos/example-org/example-repo/issues/12/comments": `[
			{"id":1,"user":{"login":"bob"},"body":"请看一下","created_at":"2026-11-01T09:00:00Z"},
			{"id":2,"user":{"login":"ci-bot[bot]","type":"Bot"},"body":"Preview ready.","created_at":"2026-11-01T09:30:00Z"}]`,
		"/repos/example-org/example-repo/pulls/12/reviews": `[
			{"id":3,"user":{"login":"me"},"state":"APPROVED","body":"","submitted_at":"2026-11-02T10:00:00Z"},
			{"id":4,"user":{"login":"bob"},"state":"COMMENTED","body":"","submitted_at":"2026-11-01T09:10:00Z"}]`,
		"/repos/example-org/example-repo/pulls/12/comments": `[
			{"id":5,"user":{"login":"bob"},"body":"这里要判空","path":"src/example.go","line":42,"created_at":"2026-11-01T09:05:00Z"}]`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.RequestURI())
		mu.Unlock()
		if b, ok := routes[r.URL.Path]; ok {
			_, _ = w.Write([]byte(b))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	c := New(Config{Auth: AuthConfig{FromMachine: "mac"}}, "Mac", (&machine{tokens: []string{oldToken}}).fetch)
	c.api = srv.URL
	c.check(context.Background())
	return c, &seen
}

func TestChangesArchiveAThreadOnce(t *testing.T) {
	c, seen := fakeArchiveAPI(t)
	batches, next, more, err := c.Changes(context.Background(), "", 20)
	if err != nil || more || len(batches) != 1 {
		t.Fatalf("%d batches, more %v, %v", len(batches), more, err)
	}
	b := batches[0]
	if b.Chat.ID != "example-org-example-repo-12" || b.Chat.Kind != "pr" || b.Chat.Name != "example-repo#12 Add the example" {
		t.Fatalf("chat %+v", b.Chat)
	}
	var lines []string
	for _, m := range b.Messages {
		lines = append(lines, m.SenderName+"|"+m.Text)
	}
	want := []string{"alice|【Add the example】\n示例描述", "bob|请看一下", "bob|（src/example.go:42）这里要判空", "ci-bot[bot] (bot)|Preview ready.", "me|（review）批准了"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("messages\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if !b.Messages[len(b.Messages)-1].Mine {
		t.Error("my review is not mine")
	}
	if q := (*seen)[1]; !strings.Contains(q, "involves%3A%40me+updated%3A%3E%3D") || !strings.Contains(q, "order=asc") {
		t.Errorf("search %s", q)
	}
	var cur archiveCursor
	_ = json.Unmarshal([]byte(next), &cur)
	if !cur.Since.Equal(time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)) || cur.Threads["example-org/example-repo#12"].IsZero() {
		t.Fatalf("cursor %s", next)
	}

	// Again from the cursor: nothing new is archived twice, and the comments are asked since the last one.
	batches, _, _, err = c.Changes(context.Background(), next, 20)
	if err != nil || len(batches) != 0 {
		t.Fatalf("again: %d batches, %v", len(batches), err)
	}
	if q := (*seen)[len(*seen)-1]; !strings.Contains(q, "since=2026-11-02T10%3A00%3A00Z") {
		t.Errorf("not asked since the last message: %s", q)
	}
}
