package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// What the review page (UI version 5, nwverse/persona#3136) reads and writes beyond review.go: a file's lines at the
// PR's head (for the lines a diff hides), which files the user marked viewed (GitHub's own Viewed), the user's pending
// review and its draft comments, and the writes that go with them: mark a file viewed, add a draft, submit or discard
// the pending review, resolve a conversation. Viewed and drafts are the user's own, as on github.com.

// HeadFile is a file of the PR as its head has it (in the fork when the head is one): its lines.
func (c *Connector) HeadFile(ctx context.Context, repo string, number int, path string) ([]string, error) {
	var pr threadPull
	if err := c.cachedGet(ctx, fmt.Sprintf("/repos/%s/pulls/%d", repo, number), &pr); err != nil {
		return nil, err
	}
	from := repo
	if pr.Head.Repo != nil && pr.Head.Repo.FullName != "" {
		from = pr.Head.Repo.FullName
	}
	f, err := c.FileAt(ctx, from, pr.Head.SHA, path)
	if err != nil {
		return nil, err
	}
	if f.Binary || f.TooBig {
		return nil, fmt.Errorf("%s is binary or too big to show", path)
	}
	return strings.Split(strings.TrimSuffix(f.Text, "\n"), "\n"), nil
}

// prNode is a PR's GraphQL id.
func (c *Connector) prNode(ctx context.Context, repo string, number int) (string, error) {
	var pr struct {
		NodeID string `json:"node_id"`
	}
	if err := c.cachedGet(ctx, fmt.Sprintf("/repos/%s/pulls/%d", repo, number), &pr); err != nil {
		return "", err
	}
	if pr.NodeID == "" {
		return "", fmt.Errorf("%s#%d has no node id", repo, number)
	}
	return pr.NodeID, nil
}

const viewedQuery = `query($owner: String!, $name: String!, $n: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $n) {
      files(first: 100, after: $after) { pageInfo { hasNextPage endCursor } nodes { path viewerViewedState } }
    }
  }
}`

// Viewed is the files of a PR the user marked viewed (and not changed since: GitHub then says DISMISSED); nil when
// GraphQL fails (the page then shows none viewed).
func (c *Connector) Viewed(ctx context.Context, repo string, number int) map[string]bool {
	owner, name, _ := strings.Cut(repo, "/")
	out := map[string]bool{}
	var after any
	for range filesMost / 100 {
		var res struct {
			Repository struct {
				PullRequest struct {
					Files struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []struct {
							Path  string `json:"path"`
							State string `json:"viewerViewedState"`
						} `json:"nodes"`
					} `json:"files"`
				} `json:"pullRequest"`
			} `json:"repository"`
		}
		if c.graphQL(ctx, viewedQuery, map[string]any{"owner": owner, "name": name, "n": number, "after": after}, &res) != nil {
			return nil
		}
		f := res.Repository.PullRequest.Files
		for _, n := range f.Nodes {
			if n.State == "VIEWED" {
				out[n.Path] = true
			}
		}
		if !f.PageInfo.HasNextPage {
			break
		}
		after = f.PageInfo.EndCursor
	}
	return out
}

// MarkViewed marks a file of a PR viewed by the user, or not.
func (c *Connector) MarkViewed(ctx context.Context, repo string, number int, path string, viewed bool) error {
	id, err := c.prNode(ctx, repo, number)
	if err != nil {
		return err
	}
	op := "unmarkFileAsViewed"
	if viewed {
		op = "markFileAsViewed"
	}
	q := fmt.Sprintf(`mutation($id: ID!, $path: String!) { %s(input: {pullRequestId: $id, path: $path}) { clientMutationId } }`, op)
	var out json.RawMessage
	return c.graphQL(ctx, q, map[string]any{"id": id, "path": path}, &out)
}

// Pending is the user's pending review of a PR: its ids and its draft comments (each a conversation of its own, or a
// reply in one already there).
type Pending struct {
	ID       int64
	NodeID   string
	Comments []LineComment
}

// PendingReview is the user's pending review of a PR; a zero one when there is none.
func (c *Connector) PendingReview(ctx context.Context, repo string, number int) (Pending, error) {
	type review struct {
		ID     int64  `json:"id"`
		NodeID string `json:"node_id"`
		State  string `json:"state"`
	}
	var p Pending
	for page := 1; page <= 10; page++ {
		rs, more, err := pageGet[review](c, ctx, fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number), page, 100)
		if err != nil {
			return Pending{}, err
		}
		for _, r := range rs {
			if r.State == "PENDING" { // only its author sees a pending review
				p.ID, p.NodeID = r.ID, r.NodeID
			}
		}
		if p.ID != 0 || !more {
			break
		}
	}
	if p.ID == 0 {
		return p, nil
	}
	var raw []struct {
		ID        int64  `json:"id"`
		Path      string `json:"path"`
		Line      int    `json:"line"`
		StartLine int    `json:"start_line"`
		Side      string `json:"side"`
		ReplyTo   int64  `json:"in_reply_to_id"`
		Body      string `json:"body"`
		User      person `json:"user"`
		Created   string `json:"created_at"`
	}
	if _, _, err := c.request(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d/reviews/%d/comments?per_page=100", repo, number, p.ID), nil, nil, &raw); err != nil {
		return Pending{}, err
	}
	for _, r := range raw {
		p.Comments = append(p.Comments, LineComment{ID: r.ID, Path: r.Path, Line: r.Line, Original: r.StartLine, Side: r.Side, ReplyTo: r.ReplyTo,
			Author: r.User.Login, Avatar: r.User.AvatarURL, Body: r.Body})
	}
	return p, nil
}

// AddDraft adds a comment on lines of a PR (start to line on side, start 0 for one line) to the user's pending
// review, starting one when there is none: nobody else sees it until the review is submitted.
func (c *Connector) AddDraft(ctx context.Context, repo string, number int, path, side string, start, line int, body string) error {
	if !validRepo(repo) || number <= 0 || path == "" || line <= 0 || strings.TrimSpace(body) == "" || side != "LEFT" && side != "RIGHT" {
		return fmt.Errorf("a draft needs the file, the line, the side and the words")
	}
	id, err := c.prNode(ctx, repo, number)
	if err != nil {
		return err
	}
	p, err := c.PendingReview(ctx, repo, number)
	if err != nil {
		return err
	}
	in := map[string]any{"pullRequestId": id, "path": path, "line": line, "side": side, "body": body}
	if p.NodeID != "" {
		in["pullRequestReviewId"] = p.NodeID
	}
	if start > 0 && start < line {
		in["startLine"], in["startSide"] = start, side
	}
	const q = `mutation($in: AddPullRequestReviewThreadInput!) { addPullRequestReviewThread(input: $in) { thread { id } } }`
	var out json.RawMessage
	if err := c.graphQL(ctx, q, map[string]any{"in": in}, &out); err != nil {
		return err
	}
	c.forget(repo, number)
	return nil
}

// SubmitPending sends the user's pending review with event (COMMENT, APPROVE, REQUEST_CHANGES) and a summary; with no
// pending review it is a review of its own.
func (c *Connector) SubmitPending(ctx context.Context, repo string, number int, event, body string) (string, error) {
	if event != "COMMENT" && event != "APPROVE" && event != "REQUEST_CHANGES" {
		return "", fmt.Errorf("a review is COMMENT, APPROVE or REQUEST_CHANGES")
	}
	p, err := c.PendingReview(ctx, repo, number)
	if err != nil {
		return "", err
	}
	if p.ID == 0 {
		return c.PostReview(ctx, repo, number, event, body)
	}
	req, _ := json.Marshal(map[string]string{"event": event, "body": body})
	return c.post(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/reviews/%d/events", repo, number, p.ID), req, repo, number)
}

// DiscardPending throws the user's pending review away, its drafts with it.
func (c *Connector) DiscardPending(ctx context.Context, repo string, number int) error {
	p, err := c.PendingReview(ctx, repo, number)
	if err != nil || p.ID == 0 {
		return err
	}
	_, err = c.post(ctx, http.MethodDelete, fmt.Sprintf("/repos/%s/pulls/%d/reviews/%d", repo, number, p.ID), nil, repo, number)
	return err
}

// Resolve resolves a conversation on lines (by its first comment's id), or opens it again.
func (c *Connector) Resolve(ctx context.Context, repo string, number int, comment int64, resolved bool) error {
	threads, err := c.reviewThreads(ctx, repo, number)
	if err != nil {
		return err
	}
	id := ""
	for _, t := range threads {
		if t.first == comment {
			id = t.id
		}
	}
	if id == "" {
		return fmt.Errorf("no conversation starts with comment %d", comment)
	}
	op := "unresolveReviewThread"
	if resolved {
		op = "resolveReviewThread"
	}
	var out json.RawMessage
	if err := c.graphQL(ctx, fmt.Sprintf(`mutation($id: ID!) { %s(input: {threadId: $id}) { thread { id } } }`, op), map[string]any{"id": id}, &out); err != nil {
		return err
	}
	c.forget(repo, number)
	return nil
}

// PostLines comments on lines start..line of the PR's diff at its head (start 0: one line), on side.
func (c *Connector) PostLines(ctx context.Context, repo string, number int, path string, start, line int, side, body string) (string, error) {
	if start <= 0 || start >= line {
		return c.PostLineComment(ctx, repo, number, path, line, side, body)
	}
	if !validRepo(repo) || number <= 0 || path == "" || strings.TrimSpace(body) == "" || side != "LEFT" && side != "RIGHT" {
		return "", fmt.Errorf("a comment on lines needs the file, the lines, the side and the words")
	}
	sha, err := c.HeadSHA(ctx, repo, number)
	if err != nil {
		return "", err
	}
	req, _ := json.Marshal(map[string]any{"body": body, "commit_id": sha, "path": path, "start_line": start, "start_side": side, "line": line, "side": side})
	return c.post(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/comments", repo, number), req, repo, number)
}
