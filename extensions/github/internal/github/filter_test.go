package github

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestInboxFilterLets(t *testing.T) {
	cases := []struct {
		name   string
		filter InboxFilter
		repo   string
		kind   string
		want   bool
	}{
		{"empty lets everything", InboxFilter{}, "example-org/example-repo", "mention", true},
		{"only: listed", InboxFilter{Repos: []string{"example-org/example-repo"}}, "example-org/example-repo", "mention", true},
		{"only: not listed", InboxFilter{Repos: []string{"example-org/example-repo"}}, "example-org/other", "mention", false},
		{"only: any case", InboxFilter{Repos: []string{"Example-Org/Example-Repo"}}, "example-org/EXAMPLE-repo", "mention", true},
		{"only: owner/*", InboxFilter{Repos: []string{"example-org/*"}}, "example-org/anything", "assign", true},
		{"only: owner/* is that owner only", InboxFilter{Repos: []string{"example-org/*"}}, "example-user/anything", "assign", false},
		{"exclude: listed", InboxFilter{ExcludeRepos: []string{"example-org/noisy"}}, "example-org/noisy", "mention", false},
		{"exclude: others come in", InboxFilter{ExcludeRepos: []string{"example-org/noisy"}}, "example-org/quiet", "mention", true},
		{"exclude: owner/*", InboxFilter{ExcludeRepos: []string{"EXAMPLE-ORG/*"}}, "example-org/quiet", "mention", false},
		{"exclude after only", InboxFilter{Repos: []string{"example-org/*"}, ExcludeRepos: []string{"example-org/noisy"}}, "example-org/noisy", "mention", false},
		{"only, then not excluded", InboxFilter{Repos: []string{"example-org/*"}, ExcludeRepos: []string{"example-org/noisy"}}, "example-org/quiet", "mention", true},
		{"skipped kind", InboxFilter{SkipKinds: []string{"team_mention", "ci_failed"}}, "example-org/example-repo", "ci_failed", false},
		{"kept kind", InboxFilter{SkipKinds: []string{"team_mention", "ci_failed"}}, "example-org/example-repo", "review_requested", true},
		// Patterns that are not owner/name are left out: an only-list with none valid lets everything in.
		{"bad only patterns", InboxFilter{Repos: []string{"example-org", "a/b/c", "[", " "}}, "example-org/x", "mention", true},
		{"bad exclude pattern", InboxFilter{ExcludeRepos: []string{"example-org/[x"}}, "example-org/x", "mention", true},
		{"a bad entry beside a good one", InboxFilter{Repos: []string{"[", "example-org/x"}}, "example-org/y", "mention", false},
		{"spaces trimmed", InboxFilter{ExcludeRepos: []string{" example-org/x "}}, "example-org/x", "mention", false},
	}
	for _, tc := range cases {
		if got := tc.filter.lets(tc.repo, tc.kind); got != tc.want {
			t.Errorf("%s: lets(%s, %s) = %v", tc.name, tc.repo, tc.kind, got)
		}
	}
}

// A configuration from before the inbox group, or with an empty one, lets everything in; the group's fields are the
// form's.
func TestSettingsInboxDefaults(t *testing.T) {
	for _, raw := range []string{`{}`, `{"triage_rules":"x","host":"github.com"}`, `{"inbox":{}}`, `{"inbox":{"repos":[],"exclude_repos":[],"skip_kinds":[]}}`} {
		var s Settings
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			t.Fatal(err)
		}
		for _, kind := range (&Connector{}).Manifest().Kinds {
			if !s.Inbox.lets("example-org/example-repo", kind) {
				t.Errorf("%s: %s left out", raw, kind)
			}
		}
	}
	var s Settings
	_ = json.Unmarshal([]byte(`{"inbox":{"repos":["example-org/*"],"exclude_repos":["example-org/noisy"],"skip_kinds":["author"]}}`), &s)
	if s.Inbox.lets("example-org/noisy", "mention") || s.Inbox.lets("example-org/x", "author") || !s.Inbox.lets("example-org/x", "mention") {
		t.Errorf("parsed %+v", s.Inbox)
	}
}

func noteIn(repo, id, reason, typ, url, updated string) string {
	return strings.ReplaceAll(note(id, reason, typ, url, updated), "example-org/example-repo", repo)
}

// The filter leaves notifications out before anything about them is read: no thing, no request for their thread.
func TestFilteredNotificationsStayOut(t *testing.T) {
	const keep, skipRepo, other = "example-org/example-repo", "example-org/noisy", "example-user/side"
	f := &fakeNotifications{prs: map[string]string{
		api12: openPR(`{"login":"me"}`),
		"/repos/example-org/example-repo/issues/12": `{"comments":0}`,
		api7: `{"title":"Example bug","state":"open","user":{"login":"bob"},"body":"出错了","html_url":"https://github.com/example-org/example-repo/issues/7","comments":0}`,
	}}
	f.notifications = "[" + strings.Join([]string{
		note("1", "review_requested", "PullRequest", "https://api.github.com"+api12, t1),
		note("2", "team_mention", "Issue", "https://api.github.com"+api7, t1), // a skipped kind
		noteIn(skipRepo, "3", "mention", "Issue", "https://api.github.com/repos/"+skipRepo+"/issues/3", t1),
		noteIn(other, "4", "assign", "Issue", "https://api.github.com/repos/"+other+"/issues/4", t1),
		noteIn(skipRepo, "5", "ci_activity", "CheckSuite", "", t1),
	}, ",") + "]"
	c := f.server(t).WithFilter(InboxFilter{Repos: []string{"example-org/*"}, ExcludeRepos: []string{skipRepo}, SkipKinds: []string{"team_mention"}})
	if _, _, err := c.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := things(t, c)
	if len(got) != 1 || got[keep+"#12"].Kind != "review_requested" {
		t.Fatalf("things: %+v", got)
	}
	for _, r := range f.requests {
		if strings.Contains(r, skipRepo) || strings.Contains(r, other) || strings.Contains(r, "/issues/7") {
			t.Errorf("read a left-out thread: %s", r)
		}
	}
}
