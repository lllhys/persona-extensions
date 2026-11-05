package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// What the extension's GitHub page (page.go) reads: the lists of what needs the user (search and GraphQL), the
// notifications, the activity of the repositories and people the user follows, and one PR / issue in full. Read
// with the connector's own token and client, kept a minute per list.

// Tabs of the page.
const (
	TabReview   = "review"   // PRs waiting for my review
	TabMine     = "mine"     // my open PRs
	TabInvolved = "involved" // assigned to me or mentioning me
	TabNotify   = "notify"   // the notifications inbox
	TabActivity = "activity" // what the repositories and people I follow did
)

// Entry is one row of a list: a PR, an issue, a notification or an event.
type Entry struct {
	Repo    string
	Number  int // 0: not a PR or issue (a release, a push…)
	PR      bool
	Title   string
	Author  string
	Avatar  string
	Updated time.Time
	URL     string // on GitHub's site
	// For PRs from GraphQL: the CI's rollup (SUCCESS, FAILURE, ERROR, PENDING, EXPECTED), the review decision
	// (APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED), a draft.
	CI       string
	Decision string
	Draft    bool
	// Why: a notification's reason, an event's line.
	Why    string
	Unread bool
}

// Ref is "owner/repo#n".
func (e Entry) Ref() string {
	if e.Number == 0 {
		return e.Repo
	}
	return fmt.Sprintf("%s#%d", e.Repo, e.Number)
}

// listCache keeps each list a while: a page drawn again (a tap, coming back) does not read GitHub again.
type listCache struct {
	mu   sync.Mutex
	tabs map[string]pageList
}

type pageList struct {
	at      time.Time
	entries []Entry
}

// listFresh is how long a list is kept.
var listFresh = time.Minute

// List is the tab's entries, from the cache unless fresh is asked for or it is older than listFresh; at is when they
// were read.
func (c *Connector) List(ctx context.Context, tab string, fresh bool) (entries []Entry, at time.Time, err error) {
	c.lists.mu.Lock()
	cached, ok := c.lists.tabs[tab]
	c.lists.mu.Unlock()
	if ok && !fresh && time.Since(cached.at) < listFresh {
		return cached.entries, cached.at, nil
	}
	if ok && c.ratesLow() { // save the hour's requests for the inbox
		return cached.entries, cached.at, nil
	}
	switch tab {
	case TabReview:
		entries, err = c.pulls(ctx, "is:open is:pr review-requested:@me archived:false")
	case TabMine:
		entries, err = c.pulls(ctx, "is:open is:pr author:@me archived:false")
	case TabInvolved:
		entries, err = c.involved(ctx)
	case TabNotify:
		entries, err = c.notifications(ctx)
	case TabActivity:
		entries, err = c.activity(ctx)
	default:
		return nil, time.Time{}, fmt.Errorf("no tab %q", tab)
	}
	if err != nil {
		if ok { // what was read before, rather than nothing
			return cached.entries, cached.at, err
		}
		return nil, time.Time{}, err
	}
	now := time.Now()
	c.lists.mu.Lock()
	if c.lists.tabs == nil {
		c.lists.tabs = map[string]pageList{}
	}
	c.lists.tabs[tab] = pageList{at: now, entries: entries}
	c.lists.mu.Unlock()
	return entries, now, nil
}

// listMost is how many rows a list has.
const listMost = 30

// pullsQuery reads PRs with their CI rollup and review decision in one request.
const pullsQuery = `query($q: String!, $n: Int!) {
  search(query: $q, type: ISSUE, first: $n) {
    nodes {
      ... on PullRequest {
        number title url updatedAt isDraft reviewDecision
        repository { nameWithOwner }
        author { login avatarUrl }
        commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
      }
    }
  }
}`

type gqlPull struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	UpdatedAt      time.Time `json:"updatedAt"`
	IsDraft        bool      `json:"isDraft"`
	ReviewDecision string    `json:"reviewDecision"`
	Repository     struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Author *struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatarUrl"`
	} `json:"author"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// pulls is the PRs of a search, through GraphQL (CI and reviews in the same answer); REST's search without them
// when GraphQL fails (a token without GraphQL, an Enterprise server).
func (c *Connector) pulls(ctx context.Context, q string) ([]Entry, error) {
	var out struct {
		Search struct {
			Nodes []gqlPull `json:"nodes"`
		} `json:"search"`
	}
	err := c.graphQL(ctx, pullsQuery, map[string]any{"q": q, "n": listMost}, &out)
	if err != nil {
		if errors.Is(err, errUnauthorized) {
			return nil, err
		}
		return c.searchEntries(ctx, q)
	}
	entries := make([]Entry, 0, len(out.Search.Nodes))
	for _, p := range out.Search.Nodes {
		if p.Number == 0 {
			continue
		}
		e := Entry{Repo: p.Repository.NameWithOwner, Number: p.Number, PR: true, Title: p.Title, Updated: p.UpdatedAt, URL: p.URL,
			Decision: p.ReviewDecision, Draft: p.IsDraft}
		if p.Author != nil {
			e.Author, e.Avatar = p.Author.Login, p.Author.AvatarURL
		}
		if n := p.Commits.Nodes; len(n) > 0 && n[0].Commit.StatusCheckRollup != nil {
			e.CI = n[0].Commit.StatusCheckRollup.State
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// searchItem is a REST search result.
type searchItem struct {
	issue
	User struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	} `json:"user"`
}

// searchEntries is REST's issue search, newest updates first.
func (c *Connector) searchEntries(ctx context.Context, q string) ([]Entry, error) {
	var res struct {
		Items []searchItem `json:"items"`
	}
	path := "/search/issues?" + url.Values{"q": {q}, "sort": {"updated"}, "order": {"desc"}, "per_page": {strconv.Itoa(listMost)}}.Encode()
	if _, err := c.Get(ctx, path, &res); err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(res.Items))
	for _, it := range res.Items {
		entries = append(entries, Entry{Repo: it.repo(), Number: it.Number, PR: it.PullRequest != nil, Title: it.Title,
			Author: it.User.Login, Avatar: it.User.AvatarURL, Updated: it.UpdatedAt, URL: it.HTMLURL, Draft: it.Draft})
	}
	return entries, nil
}

// involved is what is assigned to me and what mentions me, open, one row a PR / issue.
func (c *Connector) involved(ctx context.Context) ([]Entry, error) {
	assigned, err := c.searchEntries(ctx, "is:open archived:false assignee:@me")
	if err != nil {
		return nil, err
	}
	mentions, err := c.searchEntries(ctx, "is:open archived:false mentions:@me")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Entry
	for _, e := range assigned {
		e.Why = "指派给你"
		seen[e.Ref()] = true
		out = append(out, e)
	}
	for _, e := range mentions {
		if seen[e.Ref()] {
			continue
		}
		e.Why = "提到了你"
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	if len(out) > listMost {
		out = out[:listMost]
	}
	return out, nil
}

// reasons are notifications' reasons for people.
var reasons = map[string]string{
	"assign": "指派给你", "author": "你发起的", "comment": "你评论过", "ci_activity": "CI", "invitation": "邀请",
	"manual": "你订阅的", "mention": "提到了你", "review_requested": "请你 review", "security_alert": "安全提醒",
	"state_change": "状态变了", "subscribed": "关注的仓库", "team_mention": "提到了你的团队", "approval_requested": "等你批准",
}

// notifications is the unread notifications, newest first; the latest ones, read too, when none is unread.
func (c *Connector) notifications(ctx context.Context) ([]Entry, error) {
	type note struct {
		notification
		Unread bool `json:"unread"`
	}
	read := func(all bool) ([]note, error) {
		var ns []note
		path := "/notifications?per_page=" + strconv.Itoa(listMost)
		if all {
			path += "&all=true"
		}
		_, err := c.Get(ctx, path, &ns)
		return ns, err
	}
	ns, err := read(false)
	if err == nil && len(ns) == 0 {
		ns, err = read(true)
	}
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(ns))
	for _, n := range ns {
		e := Entry{Repo: n.Repository.FullName, Title: n.Subject.Title, Updated: n.UpdatedAt, Why: reasons[n.Reason],
			URL: n.Repository.HTMLURL, Unread: n.Unread}
		if e.Why == "" {
			e.Why = n.Reason
		}
		if num, ok := subjectNumber(n.Subject.URL); ok && (n.Subject.Type == "PullRequest" || n.Subject.Type == "Issue") {
			e.Number, e.PR = num, n.Subject.Type == "PullRequest"
			kind := "issues"
			if e.PR {
				kind = "pull"
			}
			e.URL = fmt.Sprintf("%s/%s/%d", n.Repository.HTMLURL, kind, num)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// subjectNumber is the PR's or issue's number at the end of a notification subject's API URL.
func subjectNumber(u string) (int, bool) {
	i := strings.LastIndex(u, "/")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(u[i+1:])
	return n, err == nil && n > 0
}

type event struct {
	Type  string `json:"type"`
	Actor struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	} `json:"actor"`
	Repo struct {
		Name string `json:"name"`
	} `json:"repo"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// activity is what the repositories and people the user follows did lately (received events), a line each.
func (c *Connector) activity(ctx context.Context) ([]Entry, error) {
	me, err := c.login(ctx)
	if err != nil {
		return nil, err
	}
	var evs []event
	if _, err := c.Get(ctx, "/users/"+url.PathEscape(me)+"/received_events?per_page="+strconv.Itoa(listMost), &evs); err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(evs))
	for _, ev := range evs {
		if e, ok := eventEntry(ev, c.web); ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// eventEntry is an event as a row; ok is false for kinds not worth a line.
func eventEntry(ev event, web string) (Entry, bool) {
	e := Entry{Repo: ev.Repo.Name, Author: ev.Actor.Login, Avatar: ev.Actor.AvatarURL, Updated: ev.CreatedAt, URL: web + "/" + ev.Repo.Name}
	var p struct {
		Action  string `json:"action"`
		Ref     string `json:"ref"`
		RefType string `json:"ref_type"`
		Size    int    `json:"size"`
		Issue   *struct {
			Number  int    `json:"number"`
			Title   string `json:"title"`
			HTMLURL string `json:"html_url"`
			PR      *struct {
			} `json:"pull_request"`
		} `json:"issue"`
		PullRequest *struct {
			Number  int    `json:"number"`
			Title   string `json:"title"`
			HTMLURL string `json:"html_url"`
			Merged  bool   `json:"merged"`
		} `json:"pull_request"`
		Release *struct {
			Name    string `json:"name"`
			TagName string `json:"tag_name"`
			HTMLURL string `json:"html_url"`
		} `json:"release"`
		Forkee *struct {
			FullName string `json:"full_name"`
		} `json:"forkee"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	who := ev.Actor.Login
	switch ev.Type {
	case "PushEvent":
		branch := strings.TrimPrefix(p.Ref, "refs/heads/")
		if p.Size > 0 {
			e.Title = fmt.Sprintf("%s 推送了 %d 个提交到 %s", who, p.Size, branch)
		} else {
			e.Title = fmt.Sprintf("%s 推送到 %s", who, branch)
		}
	case "PullRequestEvent", "PullRequestReviewEvent", "PullRequestReviewCommentEvent":
		if p.PullRequest == nil {
			return e, false
		}
		e.Number, e.PR, e.URL = p.PullRequest.Number, true, p.PullRequest.HTMLURL
		verb := map[string]string{"opened": "发起了", "closed": "关闭了", "reopened": "重新打开了", "created": "评论了", "submitted": "review 了"}[p.Action]
		if ev.Type == "PullRequestEvent" && p.Action == "closed" && p.PullRequest.Merged {
			verb = "合并了"
		}
		if verb == "" {
			return e, false
		}
		e.Title = fmt.Sprintf("%s %s PR：%s", who, verb, p.PullRequest.Title)
	case "IssuesEvent", "IssueCommentEvent":
		if p.Issue == nil {
			return e, false
		}
		e.Number, e.PR, e.URL = p.Issue.Number, p.Issue.PR != nil, p.Issue.HTMLURL
		verb := map[string]string{"opened": "提了", "closed": "关闭了", "reopened": "重新打开了", "created": "评论了"}[p.Action]
		if verb == "" {
			return e, false
		}
		what := "issue"
		if e.PR {
			what = "PR"
		}
		e.Title = fmt.Sprintf("%s %s %s：%s", who, verb, what, p.Issue.Title)
	case "ReleaseEvent":
		if p.Release == nil {
			return e, false
		}
		name := p.Release.Name
		if name == "" {
			name = p.Release.TagName
		}
		e.Title, e.URL = fmt.Sprintf("%s 发布了 %s", who, name), p.Release.HTMLURL
	case "CreateEvent":
		switch p.RefType {
		case "repository":
			e.Title = fmt.Sprintf("%s 建了仓库", who)
		case "tag":
			e.Title = fmt.Sprintf("%s 打了标签 %s", who, p.Ref)
		default:
			e.Title = fmt.Sprintf("%s 建了分支 %s", who, p.Ref)
		}
	case "WatchEvent":
		e.Title = fmt.Sprintf("%s 加了星标", who)
	case "ForkEvent":
		e.Title = fmt.Sprintf("%s fork 了仓库", who)
		if p.Forkee != nil {
			e.URL = web + "/" + p.Forkee.FullName
		}
	case "PublicEvent":
		e.Title = fmt.Sprintf("%s 公开了仓库", who)
	default:
		return e, false
	}
	return e, true
}

// login is the signed-in user's (the last check's), asking GitHub when it is not known yet.
func (c *Connector) login(ctx context.Context) (string, error) {
	c.mu.Lock()
	me := c.status.Account
	c.mu.Unlock()
	if me != "" {
		return me, nil
	}
	var u struct {
		Login string `json:"login"`
	}
	if _, err := c.Get(ctx, "/user", &u); err != nil {
		return "", err
	}
	return u.Login, nil
}

// graphQL runs a query (GitHub's GraphQL API), its data into out.
func (c *Connector) graphQL(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	var res struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if _, _, err := c.request(ctx, http.MethodPost, "/graphql", nil, body, &res); err != nil {
		return err
	}
	if len(res.Errors) > 0 {
		return fmt.Errorf("GitHub GraphQL: %s", Scrub(res.Errors[0].Message))
	}
	return json.Unmarshal(res.Data, out)
}
