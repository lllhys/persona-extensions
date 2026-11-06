package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// fakeReviewThreads is GitHub's GraphQL API for a made-up PR (example-org/example-repo#7) with n review threads,
// oldest first, served 100 a page with pageInfo like GitHub does. Thread Ti starts with comment 1000+i; every tenth
// thread is resolved. resolvedID is the thread a resolveReviewThread mutation named.
func fakeReviewThreads(t *testing.T, n int) (c *Connector, resolvedID *string) {
	var resolved string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			_, _ = w.Write([]byte(`{"login":"me"}`))
			return
		}
		if r.URL.Path != "/graphql" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(b, &req)
		if strings.Contains(req.Query, "resolveReviewThread") {
			resolved, _ = req.Variables["id"].(string)
			_, _ = w.Write([]byte(`{"data":{"resolveReviewThread":{"thread":{"id":"x"}}}}`))
			return
		}
		start := 0
		if a, ok := req.Variables["after"].(string); ok {
			start, _ = strconv.Atoi(a)
		}
		end := min(start+100, n)
		var nodes []string
		for i := start + 1; i <= end; i++ {
			nodes = append(nodes, fmt.Sprintf(`{"id":"T%d","isResolved":%v,"isOutdated":false,"comments":{"nodes":[{"databaseId":%d}]}}`,
				i, i%10 == 0, 1000+i))
		}
		fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":%v,"endCursor":"%d"},"nodes":[%s]}}}}}`,
			end < n, end, strings.Join(nodes, ","))
	}))
	t.Cleanup(srv.Close)
	m := &machine{tokens: []string{oldToken}}
	c = New(Config{Auth: AuthConfig{FromMachine: "mac"}}, "Mac", m.fetch)
	c.api = srv.URL
	c.check(context.Background())
	return c, &resolved
}

// A conversation past a PR's first 100 review threads can be resolved.
func TestResolvePastTheFirst100Threads(t *testing.T) {
	c, resolved := fakeReviewThreads(t, 150)
	if err := c.Resolve(context.Background(), "example-org/example-repo", 7, 1120, true); err != nil {
		t.Fatalf("resolve the 120th conversation: %v", err)
	}
	if *resolved != "T120" {
		t.Fatalf("resolved %q, want T120", *resolved)
	}
}

// The review page knows the state of conversations past a PR's first 100 review threads.
func TestResolvedStatePastTheFirst100Threads(t *testing.T) {
	c, _ := fakeReviewThreads(t, 150)
	m := c.resolved(context.Background(), "example-org/example-repo", 7)
	if len(m) != 150 {
		t.Fatalf("%d threads read, want 150", len(m))
	}
	if !m[1130].resolved || m[1131].resolved {
		t.Fatalf("thread 130 resolved %v, 131 %v; want true, false", m[1130].resolved, m[1131].resolved)
	}
}
