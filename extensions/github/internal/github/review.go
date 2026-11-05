package github

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// What the page's PR side reads beyond Detail: the changed files and their patches, the comments on lines grouped
// into conversations (resolved or not, from GraphQL), the whole conversation (comments, reviews, events), the head
// commit's checks with a failed one's annotations, steps and log, and the writes the page offers (a comment, a
// comment on a line, a reply to a conversation, a review, a merge). Lists go through cachedGet / cachedList.

// PRFile is one file a PR changes.
type PRFile struct {
	Path     string `json:"filename"`
	Previous string `json:"previous_filename"`
	Status   string `json:"status"` // added, removed, modified, renamed, copied, changed, unchanged
	Adds     int    `json:"additions"`
	Dels     int    `json:"deletions"`
	Patch    string `json:"patch"` // "" for a binary file or one too big for GitHub to show
	BlobURL  string `json:"blob_url"`
}

// filesMost is how many files GitHub lists for a PR.
const filesMost = 3000

// PRFiles is a page (100 a page) of the files a PR changes.
func (c *Connector) PRFiles(ctx context.Context, repo string, number, page int) ([]PRFile, bool, error) {
	if !validRepo(repo) || number <= 0 {
		return nil, false, fmt.Errorf("%s#%d is not a PR", repo, number)
	}
	fs, more, err := pageGet[PRFile](c, ctx, fmt.Sprintf("/repos/%s/pulls/%d/files", repo, number), page, 100)
	return fs, more && page*100 < filesMost, err
}

// PRFileAt is one of the files a PR changes, by its path.
func (c *Connector) PRFileAt(ctx context.Context, repo string, number int, path string) (PRFile, error) {
	for page := 1; page*100 <= filesMost; page++ {
		fs, more, err := c.PRFiles(ctx, repo, number, page)
		if err != nil {
			return PRFile{}, err
		}
		for _, f := range fs {
			if f.Path == path {
				return f, nil
			}
		}
		if !more {
			break
		}
	}
	return PRFile{}, fmt.Errorf("%s#%d does not change %s", repo, number, path)
}

// LineComment is a comment on a line of a PR's diff.
type LineComment struct {
	ID        int64
	Path      string
	Line      int    // in the file on Side; 0 when the PR no longer has it (outdated)
	Side      string // RIGHT (the new file) or LEFT (the old)
	Original  int    // the line when the comment was written
	ReplyTo   int64
	Author    string
	Avatar    string
	Body      string
	At        time.Time
	URL       string
	DiffHunk  string
	CommitSHA string
}

// Conversation is the comments on one line: the first and its replies, oldest first.
type Conversation struct {
	Comments []LineComment
	Resolved bool
	Outdated bool
}

// Root is the conversation's first comment.
func (v Conversation) Root() LineComment { return v.Comments[0] }

// Conversations are a PR's comments on lines, a conversation each, in the order of their first comment.
func (c *Connector) Conversations(ctx context.Context, repo string, number int) ([]Conversation, error) {
	if !validRepo(repo) || number <= 0 {
		return nil, fmt.Errorf("%s#%d is not a PR", repo, number)
	}
	type raw struct {
		threadComment
		Side     string `json:"side"`
		CommitID string `json:"commit_id"`
	}
	lines, err := cachedList[raw](c, ctx, fmt.Sprintf("/repos/%s/pulls/%d/comments", repo, number))
	if err != nil {
		return nil, err
	}
	var out []Conversation
	at := map[int64]int{} // a comment's id → its conversation
	for _, cm := range lines {
		lc := LineComment{ID: cm.ID, Path: cm.Path, Line: cm.Line, Side: cmp.Or(cm.Side, "RIGHT"), Original: cm.OriginalLine,
			ReplyTo: cm.InReplyTo, Author: cm.User.Login, Avatar: cm.User.AvatarURL, Body: cm.Body, At: cm.CreatedAt, URL: cm.HTMLURL,
			DiffHunk: cm.DiffHunk, CommitSHA: cm.CommitID}
		if i, ok := at[cm.InReplyTo]; ok && cm.InReplyTo != 0 {
			out[i].Comments = append(out[i].Comments, lc)
			at[cm.ID] = i
			continue
		}
		at[cm.ID] = len(out)
		out = append(out, Conversation{Comments: []LineComment{lc}, Outdated: cm.Line == 0})
	}
	for id, state := range c.resolved(ctx, repo, number) {
		if i, ok := at[id]; ok {
			out[i].Resolved = state.resolved
			out[i].Outdated = out[i].Outdated || state.outdated
		}
	}
	return out, nil
}

type threadState struct{ resolved, outdated bool }

const reviewThreadsQuery = `query($owner: String!, $name: String!, $n: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $n) {
      reviewThreads(first: 100) { nodes { isResolved isOutdated comments(first: 1) { nodes { databaseId } } } }
    }
  }
}`

// resolved is which conversations are resolved or outdated, by their first comment's id; nothing when GraphQL fails
// (the page then shows every conversation as open).
func (c *Connector) resolved(ctx context.Context, repo string, number int) map[int64]threadState {
	owner, name, _ := strings.Cut(repo, "/")
	var out struct {
		Repository struct {
			PullRequest struct {
				ReviewThreads struct {
					Nodes []struct {
						IsResolved bool `json:"isResolved"`
						IsOutdated bool `json:"isOutdated"`
						Comments   struct {
							Nodes []struct {
								DatabaseID int64 `json:"databaseId"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	if c.graphQL(ctx, reviewThreadsQuery, map[string]any{"owner": owner, "name": name, "n": number}, &out) != nil {
		return nil
	}
	m := map[int64]threadState{}
	for _, t := range out.Repository.PullRequest.ReviewThreads.Nodes {
		if len(t.Comments.Nodes) > 0 {
			m[t.Comments.Nodes[0].DatabaseID] = threadState{t.IsResolved, t.IsOutdated}
		}
	}
	return m
}

// Note is one entry of a PR's or issue's conversation: a comment, a review, a comment on a line, or an event.
type Note struct {
	ID     string
	Kind   string // comment, review, code, event, commit
	Author string
	Avatar string
	Body   string // markdown; an event's or a commit's line
	Review string // a review's: APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED
	Path   string // a comment on a line: where
	Line   int
	At     time.Time
	URL    string
}

// Notes are a PR's or issue's whole conversation, oldest first: comments, reviews (with words or a verdict), comments
// on lines, the commits pushed, and the events worth a line.
func (c *Connector) Notes(ctx context.Context, repo string, number int, pr bool) ([]Note, error) {
	if !validRepo(repo) || number <= 0 {
		return nil, fmt.Errorf("%s#%d is not a PR or issue", repo, number)
	}
	base := fmt.Sprintf("/repos/%s/issues/%d", repo, number)
	var out []Note
	comments, err := cachedList[threadComment](c, ctx, base+"/comments")
	if err != nil {
		return nil, err
	}
	for _, cm := range comments {
		out = append(out, Note{ID: "c" + strconv.FormatInt(cm.ID, 10), Kind: "comment", Author: cm.User.Login, Avatar: cm.User.AvatarURL,
			Body: cm.Body, At: cm.CreatedAt, URL: cm.HTMLURL})
	}
	events, err := cachedList[threadEvent](c, ctx, base+"/events")
	if err != nil {
		return nil, err
	}
	for _, ev := range events {
		if ev.Event == "closed" && slices.ContainsFunc(events, func(m threadEvent) bool {
			return m.Event == "merged" && m.CreatedAt.Sub(ev.CreatedAt).Abs() <= time.Second
		}) {
			continue // said by the merge
		}
		if text := eventText(ev); text != "" {
			out = append(out, Note{ID: "e" + strconv.FormatInt(ev.ID, 10), Kind: "event", Author: ev.Actor.Login, Avatar: ev.Actor.AvatarURL,
				Body: text, At: ev.CreatedAt})
		}
	}
	if pr {
		pulls := fmt.Sprintf("/repos/%s/pulls/%d", repo, number)
		reviews, err := cachedList[threadComment](c, ctx, pulls+"/reviews")
		if err != nil {
			return nil, err
		}
		for _, r := range reviews {
			if r.State == "PENDING" || r.State == "" || r.State == "COMMENTED" && strings.TrimSpace(r.Body) == "" {
				continue
			}
			out = append(out, Note{ID: "r" + strconv.FormatInt(r.ID, 10), Kind: "review", Author: r.User.Login, Avatar: r.User.AvatarURL,
				Body: r.Body, Review: r.State, At: r.SubmittedAt, URL: r.HTMLURL})
		}
		convs, err := c.Conversations(ctx, repo, number)
		if err != nil {
			return nil, err
		}
		for _, v := range convs {
			for _, lc := range v.Comments {
				out = append(out, Note{ID: "rc" + strconv.FormatInt(lc.ID, 10), Kind: "code", Author: lc.Author, Avatar: lc.Avatar,
					Body: lc.Body, Path: lc.Path, Line: cmp.Or(lc.Line, lc.Original), At: lc.At, URL: lc.URL})
			}
		}
		if commits, err := c.PRCommits(ctx, repo, number); err == nil { // extra: the rest without them
			for _, cm := range commits {
				out = append(out, Note{ID: "k" + cm.SHA, Kind: "commit", Author: cm.Author, Avatar: cm.Avatar,
					Body: cm.SHA[:min(7, len(cm.SHA))] + " " + cm.Title, At: cm.At, URL: cm.HTMLURL})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Note) int { return a.At.Compare(b.At) })
	return out, nil
}

// Check is one check of a commit: a check run, or a commit status (CI that reports the old way).
type Check struct {
	ID        int64 // a check run's; 0 for a status
	Name      string
	App       string
	State     string // pending, success, failure, skipped, neutral
	Title     string // a check run's output title, a status's description
	URL       string
	Started   time.Time
	Completed time.Time
	Actions   bool // a GitHub Actions job: it has steps and a log
}

// Checks are a commit's check runs and statuses, the failed ones first, then running, then the rest by name.
func (c *Connector) Checks(ctx context.Context, repo, sha string) ([]Check, error) {
	if !validRepo(repo) || sha == "" {
		return nil, fmt.Errorf("no commit to check")
	}
	var runs struct {
		CheckRuns []struct {
			threadCheckRun
			Output struct {
				Title string `json:"title"`
			} `json:"output"`
		} `json:"check_runs"`
	}
	if err := c.cachedGet(ctx, fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100", repo, url.PathEscape(sha)), &runs); err != nil {
		return nil, err
	}
	var out []Check
	for _, r := range runs.CheckRuns {
		ck := Check{ID: r.ID, Name: r.Name, App: r.App.Slug, State: checkState(r.threadCheckRun), Title: r.Output.Title, URL: r.HTMLURL,
			Actions: r.App.Slug == "github-actions"}
		if r.StartedAt != nil {
			ck.Started = *r.StartedAt
		}
		if r.CompletedAt != nil {
			ck.Completed = *r.CompletedAt
		}
		out = append(out, ck)
	}
	var st struct {
		Statuses []struct {
			Context     string    `json:"context"`
			State       string    `json:"state"`
			Description string    `json:"description"`
			TargetURL   string    `json:"target_url"`
			UpdatedAt   time.Time `json:"updated_at"`
		} `json:"statuses"`
	}
	if err := c.cachedGet(ctx, fmt.Sprintf("/repos/%s/commits/%s/status", repo, url.PathEscape(sha)), &st); err == nil {
		for _, s := range st.Statuses {
			state := map[string]string{"error": "failure", "failure": "failure", "pending": "pending", "success": "success"}[s.State]
			out = append(out, Check{Name: s.Context, State: cmp.Or(state, "neutral"), Title: s.Description, URL: s.TargetURL, Completed: s.UpdatedAt})
		}
	}
	rank := map[string]int{"failure": 0, "pending": 1}
	slices.SortStableFunc(out, func(a, b Check) int {
		ra, ok := rank[a.State]
		if !ok {
			ra = 2
		}
		rb, ok := rank[b.State]
		if !ok {
			rb = 2
		}
		return cmp.Or(cmp.Compare(ra, rb), strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)))
	})
	return out, nil
}

// CheckRun is one check run in full: its output, annotations, an Actions job's steps and the end of its log.
type CheckRun struct {
	Check
	Summary     string // markdown
	Annotations []Annotation
	Steps       []Step
}

// Annotation is a check's note on a line.
type Annotation struct {
	Path    string `json:"path"`
	Line    int    `json:"start_line"`
	Level   string `json:"annotation_level"` // notice, warning, failure
	Title   string `json:"title"`
	Message string `json:"message"`
}

// Step is an Actions job's step.
type Step struct {
	Number     int    `json:"number"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// errorLinesMost is how many of a failed log's error lines the summary keeps.
const errorLinesMost = 20

// CheckRunAt reads a check run by id.
func (c *Connector) CheckRunAt(ctx context.Context, repo string, id int64) (CheckRun, error) {
	if !validRepo(repo) || id <= 0 {
		return CheckRun{}, fmt.Errorf("no check %d", id)
	}
	var r struct {
		threadCheckRun
		Output struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
			Text    string `json:"text"`
		} `json:"output"`
	}
	if err := c.cachedGet(ctx, fmt.Sprintf("/repos/%s/check-runs/%d", repo, id), &r); err != nil {
		return CheckRun{}, err
	}
	out := CheckRun{Check: Check{ID: r.ID, Name: r.Name, App: r.App.Slug, State: checkState(r.threadCheckRun), Title: r.Output.Title,
		URL: r.HTMLURL, Actions: r.App.Slug == "github-actions"}, Summary: strings.TrimSpace(r.Output.Summary + "\n\n" + r.Output.Text)}
	if r.StartedAt != nil {
		out.Started = *r.StartedAt
	}
	if r.CompletedAt != nil {
		out.Completed = *r.CompletedAt
	}
	_ = c.cachedGet(ctx, fmt.Sprintf("/repos/%s/check-runs/%d/annotations?per_page=50", repo, id), &out.Annotations)
	if out.Actions {
		var job struct {
			Steps []Step `json:"steps"`
		}
		if c.cachedGet(ctx, fmt.Sprintf("/repos/%s/actions/jobs/%d", repo, id), &job) == nil {
			out.Steps = job.Steps
		}
	}
	return out, nil
}

// logStamp is the time Actions puts before each line of a log.
var logStamp = regexp.MustCompile(`(?m)^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z ?`)

// Unstamped is a log without the times before its lines.
func Unstamped(log string) string { return logStamp.ReplaceAllString(log, "") }

// ErrorLines are a log's lines that say what went wrong (GitHub's ##[error] marks, and lines that start like an
// error); the last errorLinesMost of them.
func ErrorLines(log string) []string {
	var out []string
	for _, line := range strings.Split(Unstamped(log), "\n") {
		line = strings.TrimSpace(line)
		low := strings.ToLower(line)
		switch {
		case strings.HasPrefix(line, "##[error]"):
			line = strings.TrimPrefix(line, "##[error]")
		case strings.HasPrefix(low, "error") || strings.HasPrefix(low, "fatal") || strings.HasPrefix(line, "--- FAIL") ||
			strings.HasPrefix(line, "FAIL") || strings.Contains(low, "panic:") || strings.Contains(line, "✗") || strings.Contains(low, ": error"):
		default:
			continue
		}
		if line = strings.TrimSpace(line); line != "" && (len(out) == 0 || out[len(out)-1] != line) {
			out = append(out, line)
		}
	}
	if len(out) > errorLinesMost {
		out = out[len(out)-errorLinesMost:]
	}
	return out
}

// LogAround is the part of a log worth reading: the n lines up to its last error, else its last n lines.
func LogAround(log string, n int) []string {
	lines := strings.Split(strings.TrimRight(Unstamped(log), "\n"), "\n")
	end := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "##[error]") {
			end = i + 1
			break
		}
	}
	return lines[max(0, end-n):end]
}

// HeadSHA is a PR's head commit.
func (c *Connector) HeadSHA(ctx context.Context, repo string, number int) (string, error) {
	var pr threadPull
	if err := c.cachedGet(ctx, fmt.Sprintf("/repos/%s/pulls/%d", repo, number), &pr); err != nil {
		return "", err
	}
	return pr.Head.SHA, nil
}

// The page's writes. The page asks before each one (its confirm button, with the text in full); GitHub's answer is
// a link to what was posted.

// PostComment comments on a PR or issue.
func (c *Connector) PostComment(ctx context.Context, repo string, number int, body string) (string, error) {
	args, _ := json.Marshal(map[string]any{"repo": repo, "number": number, "body": body})
	return c.Do(ctx, "github_comment", args)
}

// PostReview submits a review: APPROVE, REQUEST_CHANGES or COMMENT.
func (c *Connector) PostReview(ctx context.Context, repo string, number int, event, body string) (string, error) {
	args, _ := json.Marshal(map[string]any{"repo": repo, "number": number, "event": event, "body": body})
	return c.Do(ctx, "github_review", args)
}

// PostLineComment comments on a line of the PR's diff at its head commit (side RIGHT: the new file, LEFT: the old).
func (c *Connector) PostLineComment(ctx context.Context, repo string, number int, path string, line int, side, body string) (string, error) {
	if !validRepo(repo) || number <= 0 || path == "" || line <= 0 || strings.TrimSpace(body) == "" || side != "LEFT" && side != "RIGHT" {
		return "", fmt.Errorf("a comment on a line needs the file, the line, the side and the words")
	}
	sha, err := c.HeadSHA(ctx, repo, number)
	if err != nil {
		return "", err
	}
	req, _ := json.Marshal(map[string]any{"body": body, "commit_id": sha, "path": path, "line": line, "side": side})
	return c.post(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/comments", repo, number), req, repo, number)
}

// PostReply answers a conversation on a line (its first comment's id).
func (c *Connector) PostReply(ctx context.Context, repo string, number int, comment int64, body string) (string, error) {
	if !validRepo(repo) || number <= 0 || comment <= 0 || strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("a reply needs the conversation and the words")
	}
	req, _ := json.Marshal(map[string]string{"body": body})
	return c.post(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/comments/%d/replies", repo, number, comment), req, repo, number)
}

// Merge merges a PR (merge, squash or rebase) if its head is still sha (GitHub refuses it when someone pushed since).
func (c *Connector) Merge(ctx context.Context, repo string, number int, method, sha string) (string, error) {
	if !validRepo(repo) || number <= 0 || method != "merge" && method != "squash" && method != "rebase" {
		return "", fmt.Errorf("merge with merge, squash or rebase")
	}
	req := map[string]string{"merge_method": method}
	if sha != "" {
		req["sha"] = sha
	}
	body, _ := json.Marshal(req)
	var out struct {
		Message string `json:"message"`
		SHA     string `json:"sha"`
	}
	if _, _, err := c.request(ctx, http.MethodPut, fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, number), nil, body, &out); err != nil {
		return "", err
	}
	c.forget(repo, number)
	return cmp.Or(out.Message, "merged"), nil
}

func (c *Connector) post(ctx context.Context, method, path string, body []byte, repo string, number int) (string, error) {
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	if _, _, err := c.request(ctx, method, path, nil, body, &out); err != nil {
		return "", err
	}
	c.forget(repo, number)
	return out.HTMLURL, nil
}

// Commit is one commit of a list.
type Commit struct {
	SHA     string
	Title   string
	Author  string
	Avatar  string
	At      time.Time
	HTMLURL string
}

type rawCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Name string    `json:"name"`
			Date time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
	Author  *person `json:"author"`
	HTMLURL string  `json:"html_url"`
}

func (r rawCommit) commit() Commit {
	c := Commit{SHA: r.SHA, Title: firstLine(r.Commit.Message), Author: r.Commit.Author.Name, At: r.Commit.Author.Date, HTMLURL: r.HTMLURL}
	if r.Author != nil && r.Author.Login != "" {
		c.Author, c.Avatar = r.Author.Login, r.Author.AvatarURL
	}
	return c
}

// PRCommits are a PR's commits, oldest first (GitHub lists at most 250).
func (c *Connector) PRCommits(ctx context.Context, repo string, number int) ([]Commit, error) {
	if !validRepo(repo) || number <= 0 {
		return nil, fmt.Errorf("%s#%d is not a PR", repo, number)
	}
	raw, err := cachedList[rawCommit](c, ctx, fmt.Sprintf("/repos/%s/pulls/%d/commits", repo, number))
	if err != nil {
		return nil, err
	}
	out := make([]Commit, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.commit())
	}
	return out, nil
}

// Commits is a page of a ref's history, newest first.
func (c *Connector) Commits(ctx context.Context, repo, ref string, page int) ([]Commit, bool, error) {
	if !validRepo(repo) {
		return nil, false, fmt.Errorf("%q is not owner/name", repo)
	}
	p := "/repos/" + repo + "/commits"
	if ref != "" {
		p += "?sha=" + url.QueryEscape(ref)
	}
	raw, more, err := pageGet[rawCommit](c, ctx, p, page, PageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]Commit, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.commit())
	}
	return out, more, nil
}

// Rerun runs a failed Actions job's workflow run's failed jobs again; jobs of one run are run again once.
func (c *Connector) Rerun(ctx context.Context, repo string, jobs []int64) (int, error) {
	if !validRepo(repo) || len(jobs) == 0 {
		return 0, fmt.Errorf("nothing to run again")
	}
	runs := map[int64]bool{}
	for _, id := range jobs {
		var job struct {
			RunID int64 `json:"run_id"`
		}
		if err := c.cachedGet(ctx, fmt.Sprintf("/repos/%s/actions/jobs/%d", repo, id), &job); err != nil {
			return 0, err
		}
		if job.RunID == 0 || runs[job.RunID] {
			continue
		}
		if _, _, err := c.request(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/actions/runs/%d/rerun-failed-jobs", repo, job.RunID), nil, []byte("{}"), nil); err != nil {
			return len(runs), err
		}
		runs[job.RunID] = true
	}
	return len(runs), nil
}
