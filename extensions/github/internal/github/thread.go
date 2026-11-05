package github

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/extensions/github/internal/connector"
)

// A pull request or issue opened in the app as GitHub's own thread (connector.Threads, docs/connectors.md「会话页」):
// its header (state, author, labels, branches, description) and its timeline, a page at a time, newest page first:
// comments, reviews, comments on lines with the diff before them, CI checks (a failure with the end of its log) and
// the gray lines of what happened (labeled, merged, renamed). Every list is read with If-None-Match: GitHub answers
// 304 for what has not changed, which costs nothing of the hour's requests.

const (
	threadListPages = 5                // pages of 100 read per list: a thread's newest 500 comments, reviews, events
	threadFresh     = 15 * time.Second // a list read this recently is not asked again (the next page of the same thread)
	etagKeep        = 400              // answers kept for If-None-Match
	hunkLines       = 6                // lines of the diff shown above a comment on a line
	checkLogTail    = 8 << 10          // the end of a failed check's log
	pageLogTail     = 64 << 10         // the end of a failed check's log kept (the page shows what is around its error)
	logRead         = 3 * time.Minute  // how long a log is read for
)

// etagCache keeps GitHub's answers by path with their ETag, the least recently used going first.
type etagCache struct {
	mu   sync.Mutex
	m    map[string]*etagEntry
	logs map[int64]string // failed checks' log tails, by job id (a finished job's log does not change)
	// reading: the logs being read for the page, by job id; closed once read.
	reading map[int64]chan struct{}
	// avatars: a login's avatar URL, as the lists gave it, for Media.
	avatars map[string]string
}

type etagEntry struct {
	etag string
	body []byte
	at   time.Time // read (or confirmed by a 304)
	used time.Time
}

// cachedGet reads path into out like Get, asking with If-None-Match for what it read before and not asking at all
// within threadFresh.
func (c *Connector) cachedGet(ctx context.Context, path string, out any) error {
	e := &c.etags
	e.mu.Lock()
	if e.m == nil {
		e.m = map[string]*etagEntry{}
	}
	old := e.m[path]
	var hdr http.Header
	if old != nil {
		old.used = time.Now()
		if time.Since(old.at) < threadFresh {
			body := old.body
			e.mu.Unlock()
			return decodeBody(path, body, out)
		}
		hdr = http.Header{"If-None-Match": {old.etag}}
	}
	e.mu.Unlock()
	var body []byte
	code, h, err := c.request(ctx, http.MethodGet, path, hdr, nil, &body)
	if err != nil {
		return err
	}
	e.mu.Lock()
	if code == http.StatusNotModified && old != nil {
		old.at = time.Now()
		body = old.body
	} else if tag := h.Get("ETag"); tag != "" {
		e.m[path] = &etagEntry{etag: tag, body: body, at: time.Now(), used: time.Now()}
		if len(e.m) > etagKeep {
			oldest := ""
			for k, v := range e.m {
				if oldest == "" || v.used.Before(e.m[oldest].used) {
					oldest = k
				}
			}
			delete(e.m, oldest)
		}
	}
	e.mu.Unlock()
	return decodeBody(path, body, out)
}

// forget drops what is kept for repo's PR / issue number: a comment was just posted, the page reads it again.
func (c *Connector) forget(repo string, number int) {
	e := &c.etags
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range []string{fmt.Sprintf("/repos/%s/issues/%d", repo, number), fmt.Sprintf("/repos/%s/pulls/%d", repo, number)} {
		for k := range e.m {
			if k == p || strings.HasPrefix(k, p+"/") || strings.HasPrefix(k, p+"?") {
				delete(e.m, k)
			}
		}
	}
}

func decodeBody(path string, body []byte, out any) error {
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GitHub GET %s: %v", path, err)
	}
	return nil
}

// cachedList reads up to threadListPages pages of a list of 100.
func cachedList[T any](c *Connector, ctx context.Context, path string) ([]T, error) {
	var all []T
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	for page := 1; page <= threadListPages; page++ {
		var batch []T
		if err := c.cachedGet(ctx, fmt.Sprintf("%s%sper_page=100&page=%d", path, sep, page), &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

type person struct {
	Login     string `json:"login"`
	Type      string `json:"type"`
	AvatarURL string `json:"avatar_url"`
}

type threadIssue struct {
	Title     string    `json:"title"`
	State     string    `json:"state"`
	User      person    `json:"user"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	Locked    bool      `json:"locked"`
	CreatedAt time.Time `json:"created_at"`
	Labels    []struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	} `json:"labels"`
	PullRequest *struct {
		MergedAt *time.Time `json:"merged_at"`
	} `json:"pull_request"`
}

type threadPull struct {
	Draft  bool `json:"draft"`
	Merged bool `json:"merged"`
	Head   struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

type threadComment struct {
	ID        int64     `json:"id"`
	User      person    `json:"user"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// reviews
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submitted_at"`
	// comments on a line
	Path         string `json:"path"`
	Line         int    `json:"line"`
	OriginalLine int    `json:"original_line"`
	DiffHunk     string `json:"diff_hunk"`
	InReplyTo    int64  `json:"in_reply_to_id"`
}

type threadEvent struct {
	ID        int64     `json:"id"`
	Event     string    `json:"event"`
	Actor     person    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
	Label     struct {
		Name string `json:"name"`
	} `json:"label"`
	Assignee          person `json:"assignee"`
	RequestedReviewer person `json:"requested_reviewer"`
	RequestedTeam     struct {
		Name string `json:"name"`
	} `json:"requested_team"`
	Rename struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"rename"`
}

type threadCheckRun struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Conclusion  string     `json:"conclusion"`
	HTMLURL     string     `json:"html_url"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	App         struct {
		Slug string `json:"slug"`
	} `json:"app"`
}

// Thread is a page of a PR's or issue's timeline ("example-org/example-repo#12"), oldest first: the newest limit
// entries for before "", else those before the entry before names.
func (c *Connector) Thread(ctx context.Context, thread, before string, limit int) (connector.ThreadPage, error) {
	m := threadRef.FindStringSubmatch(thread)
	if m == nil || !validRepo(m[1]) {
		return connector.ThreadPage{}, fmt.Errorf("%s is not a pull request or an issue", thread)
	}
	repo, number := m[1], m[2]
	base := fmt.Sprintf("/repos/%s/issues/%s", repo, number)
	var it threadIssue
	if err := c.cachedGet(ctx, base, &it); err != nil {
		return connector.ThreadPage{}, err
	}
	me := c.Status(ctx).Account
	chat := connector.ThreadChat{ID: thread, Name: it.Title, Kind: "issue", URL: it.HTMLURL, State: it.State,
		Author: c.threadPerson(it.User), Body: strings.TrimSpace(it.Body), BodyFormat: "markdown"}
	if !it.Locked {
		chat.Reply = "github_comment"
	}
	for _, l := range it.Labels {
		chat.Labels = append(chat.Labels, connector.ThreadLabel{Name: l.Name, Color: l.Color})
	}
	var all []connector.ThreadMessage
	add := func(ms ...connector.ThreadMessage) { all = append(all, ms...) }

	comments, err := cachedList[threadComment](c, ctx, base+"/comments")
	if err != nil {
		return connector.ThreadPage{}, err
	}
	for _, cm := range comments {
		add(connector.ThreadMessage{ID: "c" + strconv.FormatInt(cm.ID, 10), At: cm.CreatedAt, Sender: *c.threadPerson(cm.User),
			Mine: mine(cm.User, me), Text: cm.Body, Format: "markdown", Edited: edited(cm), URL: cm.HTMLURL})
	}
	events, err := cachedList[threadEvent](c, ctx, base+"/events")
	if err != nil {
		return connector.ThreadPage{}, err
	}
	merged := map[int64]bool{} // a merge's second: GitHub adds a closed event to it, said by the merge
	for _, ev := range events {
		if ev.Event == "merged" {
			merged[ev.CreatedAt.Unix()] = true
		}
	}
	for _, ev := range events {
		if ev.Event == "closed" && (merged[ev.CreatedAt.Unix()] || merged[ev.CreatedAt.Unix()-1] || merged[ev.CreatedAt.Unix()+1]) {
			continue
		}
		if text := eventText(ev); text != "" {
			add(connector.ThreadMessage{ID: "e" + strconv.FormatInt(ev.ID, 10), At: ev.CreatedAt, Sender: *c.threadPerson(ev.Actor),
				Mine: mine(ev.Actor, me), Event: true, Text: text})
		}
	}

	if it.PullRequest != nil {
		chat.Kind = "pr"
		pulls := fmt.Sprintf("/repos/%s/pulls/%s", repo, number)
		var pr threadPull
		if err := c.cachedGet(ctx, pulls, &pr); err != nil {
			return connector.ThreadPage{}, err
		}
		chat.Branch = &connector.ThreadBranch{Head: pr.Head.Ref, Base: pr.Base.Ref}
		switch {
		case pr.Merged:
			chat.State = "merged"
		case pr.Draft && it.State == "open":
			chat.State = "draft"
		}
		ms, err := c.reviewMessages(ctx, pulls, me)
		if err != nil {
			return connector.ThreadPage{}, err
		}
		add(ms...)
		if it.State == "open" && pr.Head.SHA != "" {
			// The checks of the head commit; a list that cannot be read leaves the rest of the thread as it is.
			if ms, err := c.checkMessages(ctx, repo, pr.Head.SHA, it.CreatedAt); err == nil {
				add(ms...)
			} else if errors.Is(err, context.Canceled) {
				return connector.ThreadPage{}, err
			}
		}
	}
	slices.SortStableFunc(all, func(a, b connector.ThreadMessage) int { return a.At.Compare(b.At) })

	end := len(all)
	if before != "" {
		end = slices.IndexFunc(all, func(m connector.ThreadMessage) bool { return m.ID == before })
		if end < 0 {
			return connector.ThreadPage{}, fmt.Errorf("%s: no entry %s (the thread changed: read it again)", thread, before)
		}
	}
	limit = min(max(limit, 1), connector.ThreadMost)
	start := max(0, end-limit)
	page := connector.ThreadPage{Chat: chat, Messages: slices.Clone(all[start:end])}
	if start > 0 {
		page.Before = all[start].ID
	}
	return page, nil
}

// reviewMessages are a PR's reviews (not the empty ones that only hold comments on lines) and its comments on lines.
func (c *Connector) reviewMessages(ctx context.Context, pulls, me string) ([]connector.ThreadMessage, error) {
	reviews, err := cachedList[threadComment](c, ctx, pulls+"/reviews")
	if err != nil {
		return nil, err
	}
	var out []connector.ThreadMessage
	for _, r := range reviews {
		state := strings.ToLower(r.State)
		if state == "pending" || state == "" || state == "commented" && strings.TrimSpace(r.Body) == "" {
			continue
		}
		out = append(out, connector.ThreadMessage{ID: "r" + strconv.FormatInt(r.ID, 10), At: r.SubmittedAt, Sender: *c.threadPerson(r.User),
			Mine: mine(r.User, me), Kind: "review", Review: state, Text: r.Body, Format: "markdown", URL: r.HTMLURL})
	}
	lines, err := cachedList[threadComment](c, ctx, pulls+"/comments")
	if err != nil {
		return nil, err
	}
	byID := map[int64]threadComment{}
	for _, cm := range lines {
		byID[cm.ID] = cm
	}
	for _, cm := range lines {
		line := cm.Line
		if line == 0 {
			line = cm.OriginalLine // on a line the PR no longer has
		}
		msg := connector.ThreadMessage{ID: "rc" + strconv.FormatInt(cm.ID, 10), At: cm.CreatedAt, Sender: *c.threadPerson(cm.User),
			Mine: mine(cm.User, me), Kind: "code", Text: cm.Body, Format: "markdown", Edited: edited(cm), URL: cm.HTMLURL,
			Code: &connector.ThreadCode{Path: cm.Path, Line: line}}
		if q, ok := byID[cm.InReplyTo]; ok {
			msg.ReplyTo = &connector.ThreadQuote{ID: "rc" + strconv.FormatInt(q.ID, 10), From: q.User.Login, Text: firstLine(q.Body)}
		} else {
			msg.Code.Hunk = lastLines(cm.DiffHunk, hunkLines) // the first of a conversation shows the code; answers do not repeat it
		}
		out = append(out, msg)
	}
	return out, nil
}

// checkMessages are the head commit's checks, each at the time it finished (or started); a failed Actions job carries
// the end of its log.
func (c *Connector) checkMessages(ctx context.Context, repo, sha string, opened time.Time) ([]connector.ThreadMessage, error) {
	var res struct {
		Runs []threadCheckRun `json:"check_runs"`
	}
	if err := c.cachedGet(ctx, fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100", repo, url.PathEscape(sha)), &res); err != nil {
		return nil, err
	}
	var out []connector.ThreadMessage
	logs := 0
	for _, r := range res.Runs {
		state := checkState(r)
		at := opened
		switch {
		case r.CompletedAt != nil:
			at = *r.CompletedAt
		case r.StartedAt != nil:
			at = *r.StartedAt
		}
		ck := &connector.ThreadCheck{Name: r.Name, State: state, URL: r.HTMLURL}
		if state == "failure" && r.App.Slug == "github-actions" && logs < failedLogs {
			logs++
			ck.Log = c.checkLog(ctx, repo, r.ID)
		}
		out = append(out, connector.ThreadMessage{ID: "ck" + strconv.FormatInt(r.ID, 10), At: at,
			Sender: connector.ThreadPerson{Name: cmp.Or(r.App.Slug, "checks")}, Kind: "check", Check: ck, URL: r.HTMLURL})
	}
	return out, nil
}

// checkLog is the end of a failed job's log (checkLogTail of it), read once per job; "" when it cannot be read.
func (c *Connector) checkLog(ctx context.Context, repo string, job int64) string {
	log, _ := c.jobLog(ctx, repo, job)
	return tail(log, checkLogTail)
}

// jobLog is the end of a finished job's log (pageLogTail of it), read once per job.
func (c *Connector) jobLog(ctx context.Context, repo string, job int64) (string, bool) {
	e := &c.etags
	e.mu.Lock()
	log, ok := e.logs[job]
	e.mu.Unlock()
	if ok {
		return log, true
	}
	raw, err := c.raw(ctx, fmt.Sprintf("/repos/%s/actions/jobs/%d/logs", repo, job))
	if err != nil {
		return "", false
	}
	log = tail(raw, pageLogTail)
	e.mu.Lock()
	if e.logs == nil || len(e.logs) > etagKeep {
		e.logs = map[int64]string{}
	}
	e.logs[job] = log
	e.mu.Unlock()
	return log, true
}

// JobLog is a finished job's log end for the page, waiting for it at most wait: a log comes from GitHub's blob
// storage, which can take a minute. pending says it is still being read; done is called once it has been (the page
// then draws again).
func (c *Connector) JobLog(repo string, job int64, wait time.Duration, done func()) (log string, pending bool) {
	e := &c.etags
	e.mu.Lock()
	if log, ok := e.logs[job]; ok {
		e.mu.Unlock()
		return log, false
	}
	ch, reading := e.reading[job]
	if !reading {
		if e.reading == nil {
			e.reading = map[int64]chan struct{}{}
		}
		ch = make(chan struct{})
		e.reading[job] = ch
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), logRead)
			defer cancel()
			_, ok := c.jobLog(ctx, repo, job)
			e.mu.Lock()
			delete(e.reading, job)
			e.mu.Unlock()
			close(ch)
			if ok && done != nil {
				done()
			}
		}()
	}
	e.mu.Unlock()
	select {
	case <-ch:
	case <-time.After(wait):
		return "", true
	}
	e.mu.Lock()
	log = e.logs[job]
	e.mu.Unlock()
	return log, false
}

func checkState(r threadCheckRun) string {
	if r.Status != "completed" {
		return "pending"
	}
	switch r.Conclusion {
	case "success", "neutral":
		return "success"
	case "failure", "timed_out", "action_required", "startup_failure":
		return "failure"
	case "cancelled":
		return "cancelled"
	}
	return "skipped" // skipped, stale
}

// eventText is the gray line of an event, "" for those the thread does not show (subscribed, mentioned, referenced…).
func eventText(ev threadEvent) string {
	who := ev.Actor.Login
	if who == "" {
		who = "有人"
	}
	switch ev.Event {
	case "labeled":
		return who + " 加了标签 " + ev.Label.Name
	case "unlabeled":
		return who + " 去掉了标签 " + ev.Label.Name
	case "assigned":
		if ev.Assignee.Login == ev.Actor.Login {
			return who + " 认领了"
		}
		return who + " 指派给 " + ev.Assignee.Login
	case "unassigned":
		return who + " 取消了指派 " + ev.Assignee.Login
	case "review_requested":
		if ev.RequestedTeam.Name != "" {
			return who + " 请团队 " + ev.RequestedTeam.Name + " review"
		}
		return who + " 请 " + ev.RequestedReviewer.Login + " review"
	case "review_request_removed":
		return who + " 撤回了给 " + cmp.Or(ev.RequestedReviewer.Login, ev.RequestedTeam.Name) + " 的 review 请求"
	case "renamed":
		return who + " 把标题从「" + ev.Rename.From + "」改成「" + ev.Rename.To + "」"
	case "closed":
		return who + " 关闭了"
	case "reopened":
		return who + " 重新打开了"
	case "merged":
		return who + " 合并了"
	case "head_ref_force_pushed":
		return who + " 强制推送了分支"
	case "head_ref_deleted":
		return who + " 删除了分支"
	case "ready_for_review":
		return who + " 标为可以 review"
	case "convert_to_draft":
		return who + " 改回了草稿"
	case "locked":
		return who + " 锁定了讨论"
	case "unlocked":
		return who + " 解锁了讨论"
	}
	return ""
}

// threadPerson is who wrote something, with their avatar's media ref ("avatar:<login>:<version>").
func (c *Connector) threadPerson(p person) *connector.ThreadPerson {
	tp := &connector.ThreadPerson{ID: p.Login, Name: p.Login}
	if p.Login == "" {
		tp.Name = "(deleted user)"
		return tp
	}
	if p.AvatarURL != "" {
		u, err := url.Parse(p.AvatarURL)
		if err == nil {
			tp.Avatar = "avatar:" + p.Login + ":" + cmp.Or(u.Query().Get("v"), "1")
			c.etags.mu.Lock()
			if c.etags.avatars == nil {
				c.etags.avatars = map[string]string{}
			}
			c.etags.avatars[p.Login] = p.AvatarURL
			c.etags.mu.Unlock()
		}
	}
	return tp
}

// Media is an avatar ("avatar:<login>:<version>"), small; GitHub's avatar host gets no token.
func (c *Connector) Media(ctx context.Context, ref string) (connector.Media, error) {
	parts := strings.Split(ref, ":")
	if len(parts) != 3 || parts[0] != "avatar" || parts[1] == "" {
		return connector.Media{}, fmt.Errorf("no media %q", ref)
	}
	login := parts[1]
	c.etags.mu.Lock()
	src := c.etags.avatars[login]
	c.etags.mu.Unlock()
	if src == "" { // not seen since the hub started: ask GitHub where it is
		var u person
		if err := c.cachedGet(ctx, "/users/"+url.PathEscape(login), &u); err != nil {
			return connector.Media{}, err
		}
		src = u.AvatarURL
	}
	au, err := url.Parse(src)
	if err != nil || au.Scheme != "https" {
		return connector.Media{}, fmt.Errorf("no avatar for %s", login)
	}
	q := au.Query()
	q.Set("s", "96")
	au.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, au.String(), nil)
	if err != nil {
		return connector.Media{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return connector.Media{}, errors.New(Scrub(err.Error()))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return connector.Media{}, fmt.Errorf("avatar of %s: %d", login, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, connector.MediaMost+1))
	if err != nil {
		return connector.Media{}, err
	}
	if len(data) > connector.MediaMost {
		return connector.Media{}, fmt.Errorf("avatar of %s is too big", login)
	}
	return connector.Media{Mime: resp.Header.Get("Content-Type"), Data: data}, nil
}

func mine(p person, me string) bool { return me != "" && strings.EqualFold(p.Login, me) }

func edited(cm threadComment) bool { return cm.UpdatedAt.Sub(cm.CreatedAt) > time.Minute }

// lastLines is the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
