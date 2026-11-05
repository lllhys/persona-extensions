package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func fakeWrites(t *testing.T) (*Connector, *[]string) {
	var mu sync.Mutex
	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			posted = append(posted, r.URL.Path+" "+string(b))
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"html_url":"https://github.com/example-org/example-repo/pull/12#x"}`))
		case r.URL.Path == "/repos/example-org/example-repo/issues/12":
			_, _ = w.Write([]byte(`{"title":"Add the example"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c := New(Config{Auth: AuthConfig{FromMachine: "mac"}}, "Mac", (&machine{tokens: []string{oldToken}}).fetch)
	c.api = srv.URL
	return c, &posted
}

func TestWritesDescribeTheCardAndPost(t *testing.T) {
	c, posted := fakeWrites(t)
	ctx := context.Background()
	if names := []string{c.Actions()[0].Name, c.Actions()[1].Name}; names[0] != "github_comment" || names[1] != "github_review" {
		t.Fatalf("%v", names)
	}

	comment := json.RawMessage(`{"repo":"example-org/example-repo","number":12,"body":"看过了，可以合。"}`)
	act, card, _ := c.Describe(ctx, "github_comment", comment)
	if card != "在 GitHub 上评论 example-org/example-repo#12「Add the example」：\n\n看过了，可以合。" ||
		act.Visible != "example-org/example-repo 仓库能看到的所有人（公开仓库是所有人）" {
		t.Fatalf("card %q, visible %q", card, act.Visible)
	}
	if out, err := c.Do(ctx, "github_comment", comment); err != nil || !strings.Contains(out, "Commented on example-org/example-repo#12") {
		t.Fatalf("%q %v", out, err)
	}

	approve := json.RawMessage(`{"repo":"example-org/example-repo","number":12,"event":"APPROVE"}`)
	if _, card, _ := c.Describe(ctx, "github_review", approve); !strings.HasPrefix(card, "【批准】在 GitHub 上批准（approve）这个 PR：example-org/example-repo#12") {
		t.Fatalf("approve card %q", card)
	}
	if _, err := c.Do(ctx, "github_review", approve); err != nil {
		t.Fatal(err)
	}
	want := []string{`/repos/example-org/example-repo/issues/12/comments {"body":"看过了，可以合。"}`, `/repos/example-org/example-repo/pulls/12/reviews {"event":"APPROVE"}`}
	if strings.Join(*posted, "\n") != strings.Join(want, "\n") {
		t.Fatalf("posted\n%s", strings.Join(*posted, "\n"))
	}

	// Offline (GitHub does not answer the title): the card still says where and what.
	other := json.RawMessage(`{"repo":"example-org/other","number":3,"event":"REQUEST_CHANGES","body":"请补测试"}`)
	if _, card, _ := c.Describe(ctx, "github_review", other); card != "在 GitHub 上要求修改（request changes）example-org/other#3：\n\n请补测试" {
		t.Fatalf("card %q", card)
	}
}

func TestBadWritesPostNothing(t *testing.T) {
	c, posted := fakeWrites(t)
	for _, tc := range []struct{ name, args string }{
		{"github_comment", `{"repo":"example-org/example-repo","number":12,"body":"  "}`},
		{"github_comment", `{"repo":"../x","number":12,"body":"x"}`},
		{"github_review", `{"repo":"example-org/example-repo","number":12,"event":"MERGE"}`},
		{"github_review", `{"repo":"example-org/example-repo","number":12,"event":"REQUEST_CHANGES"}`},
	} {
		if _, err := c.Do(context.Background(), tc.name, json.RawMessage(tc.args)); err == nil {
			t.Errorf("%s %s ran", tc.name, tc.args)
		}
	}
	if len(*posted) != 0 {
		t.Fatalf("posted %v", *posted)
	}
}
