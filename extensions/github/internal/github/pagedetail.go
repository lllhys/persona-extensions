package github

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// Detail is one PR or issue as the page shows it: what it is, where it stands (CI, reviews), and its latest comments.
type Detail struct {
	Repo      string
	Number    int
	PR        bool
	Title     string
	State     string // open, closed, merged, draft
	Author    string
	Avatar    string
	Body      string
	URL       string
	Labels    []string
	Assignees []string
	Created   time.Time
	Updated   time.Time
	// PRs only.
	Head, Base string
	Checks     CheckSummary
	Reviews    []Review // the latest review of each reviewer, and those asked who have not answered
	// Comments are the newest few, oldest first; CommentCount is all of them.
	Comments     []Comment
	CommentCount int
}

// CheckSummary counts a PR head's check runs.
type CheckSummary struct {
	Passed, Failed, Pending, Skipped int
	FailedNames                      []string
}

// Total is how many checks there are.
func (s CheckSummary) Total() int { return s.Passed + s.Failed + s.Pending + s.Skipped }

// Review is where one reviewer stands: APPROVED, CHANGES_REQUESTED, COMMENTED, or REQUESTED (asked, not answered).
type Review struct {
	Login string
	State string
}

// Comment is one comment of the thread.
type Comment struct {
	Author string
	Avatar string
	Body   string
	At     time.Time
	URL    string
}

// commentsShown is how many of the newest comments the page shows.
const commentsShown = 5

// Detail reads repo#number for the page.
func (c *Connector) Detail(ctx context.Context, repo string, number int) (Detail, error) {
	if !validRepo(repo) || number <= 0 {
		return Detail{}, fmt.Errorf("%s#%d is not a PR or issue", repo, number)
	}
	var it struct {
		threadIssue
		Comments  int       `json:"comments"`
		UpdatedAt time.Time `json:"updated_at"`
		Assignees []person  `json:"assignees"`
	}
	base := fmt.Sprintf("/repos/%s/issues/%d", repo, number)
	if err := c.cachedGet(ctx, base, &it); err != nil {
		return Detail{}, err
	}
	d := Detail{Repo: repo, Number: number, PR: it.PullRequest != nil, Title: it.Title, State: it.State, Author: it.User.Login,
		Avatar: it.User.AvatarURL, Body: it.Body, URL: it.HTMLURL, Created: it.CreatedAt, Updated: it.UpdatedAt, CommentCount: it.Comments}
	for _, l := range it.Labels {
		d.Labels = append(d.Labels, l.Name)
	}
	for _, a := range it.Assignees {
		d.Assignees = append(d.Assignees, a.Login)
	}
	if d.PR {
		if err := c.pullDetail(ctx, &d); err != nil {
			return Detail{}, err
		}
	}
	if it.Comments > 0 {
		// the last page of the comments (oldest first), 30 a page
		page := (it.Comments + 29) / 30
		var cs []threadComment
		if err := c.cachedGet(ctx, base+"/comments?per_page=30&page="+strconv.Itoa(page), &cs); err != nil {
			return Detail{}, err
		}
		if len(cs) > commentsShown {
			cs = cs[len(cs)-commentsShown:]
		}
		for _, cm := range cs {
			d.Comments = append(d.Comments, Comment{Author: cm.User.Login, Avatar: cm.User.AvatarURL, Body: cm.Body, At: cm.CreatedAt, URL: cm.HTMLURL})
		}
	}
	return d, nil
}

// pullDetail adds a PR's branches, state, checks and reviews.
func (c *Connector) pullDetail(ctx context.Context, d *Detail) error {
	var pr struct {
		threadPull
		RequestedReviewers []person `json:"requested_reviewers"`
	}
	if err := c.cachedGet(ctx, fmt.Sprintf("/repos/%s/pulls/%d", d.Repo, d.Number), &pr); err != nil {
		return err
	}
	d.Head, d.Base = pr.Head.Ref, pr.Base.Ref
	switch {
	case pr.Merged:
		d.State = "merged"
	case pr.Draft && d.State == "open":
		d.State = "draft"
	}
	var reviews []threadComment
	if err := c.cachedGet(ctx, fmt.Sprintf("/repos/%s/pulls/%d/reviews?per_page=100", d.Repo, d.Number), &reviews); err != nil {
		return err
	}
	latest := map[string]string{}
	var order []string
	for _, r := range reviews {
		if r.State == "PENDING" || r.User.Login == "" {
			continue
		}
		if _, ok := latest[r.User.Login]; !ok {
			order = append(order, r.User.Login)
		}
		// a comment after an approval does not undo it
		if r.State != "COMMENTED" || latest[r.User.Login] == "" {
			latest[r.User.Login] = r.State
		}
	}
	for _, p := range pr.RequestedReviewers {
		if _, ok := latest[p.Login]; !ok {
			order = append(order, p.Login)
		}
		latest[p.Login] = "REQUESTED" // asked again: waiting for them
	}
	for _, login := range order {
		d.Reviews = append(d.Reviews, Review{Login: login, State: latest[login]})
	}
	if pr.Head.SHA == "" {
		return nil
	}
	var runs struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if err := c.cachedGet(ctx, fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100", d.Repo, url.PathEscape(pr.Head.SHA)), &runs); err != nil {
		return nil // checks are extra: the PR is shown without them
	}
	for _, r := range runs.CheckRuns {
		switch {
		case r.Status != "completed":
			d.Checks.Pending++
		case r.Conclusion == "success" || r.Conclusion == "neutral":
			d.Checks.Passed++
		case r.Conclusion == "skipped" || r.Conclusion == "cancelled" || r.Conclusion == "stale":
			d.Checks.Skipped++
		default:
			d.Checks.Failed++
			d.Checks.FailedNames = append(d.Checks.FailedNames, r.Name)
		}
	}
	sort.Strings(d.Checks.FailedNames)
	return nil
}

// ForgetDetail drops what is kept of repo#number, so the page reads it again (its refresh button).
func (c *Connector) ForgetDetail(repo string, number int) { c.forget(repo, number) }

// Web is GitHub's site (https://github.com, or an Enterprise host).
func (c *Connector) Web() string { return c.web }

// RatesLow says whether this hour's requests are nearly used up (the page then shows what it read before).
func (c *Connector) RatesLow() bool { return c.ratesLow() }

// UseAPI points the connector at another API and site (a test's fake GitHub), before it runs.
func (c *Connector) UseAPI(api, web string) { c.api, c.web = api, web }
