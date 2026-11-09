package x

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/x/internal/connector"
)

// The Grok source's digest: every grok_every_hours, grok searches X for the watched accounts (grok_handles, else the
// accounts the user follows when the X API read them) in batches of grokBatch (X search takes at most 20 handles),
// at most grokBatchesMost batches a refresh (the next refresh goes on with the next ones), plus one run for the
// topics; what it found is merged into grok_digest.json. Every run (the digest's and x_search's) counts against
// grok_searches_per_day per UTC day, counted before it starts and kept at once, so a restart does not forget it.
//
// Which source the page and x_digest use (Settings.dataSource): x_api; grok; auto (the default) the X API when
// signed in, else Grok when grok is on the hub machine. With grok, an X API sign-in still brings the mentions and
// who the user follows, but not the paid home timeline.

// Sources (the source setting).
const (
	SourceAuto = "auto"
	SourceXAPI = "x_api"
	SourceGrok = "grok"
)

// Bounds of the Grok settings (the manifest's).
const (
	defaultGrokEvery = 6
	maxGrokEvery     = 24
	defaultGrokCap   = 30
	maxGrokCap       = 200
	handlesMost      = 200
	followingHandles = 100
	topicsMost       = 10
	topicLong        = 100
	// grokBatch is how many handles one run searches (X search's allowed_x_handles takes 20 at most).
	grokBatch = 20
	// grokBatchesMost is how many handle batches one refresh runs at most.
	grokBatchesMost = 5
	// grokKeptMost is how many posts grok_digest.json keeps at most.
	grokKeptMost = 300
	// grokWindow is how far back the digest searches, and the page shows.
	grokWindow = 24 * time.Hour
)

// Intervals of the Grok loop.
var (
	// grokCheckEvery is how often the loop looks for grok and whether a refresh is due.
	grokCheckEvery = 15 * time.Minute
	// grokRetry is how long a refresh that failed waits.
	grokRetry = 30 * time.Minute
	// grokForceGap is how soon after a refresh that found something the page's refresh button runs another: taps
	// within it are ignored (each refresh is several grok runs of the day's cap and of the subscription's quota).
	grokForceGap = 30 * time.Minute
)

// dataSource is the source setting: auto, x_api or grok.
func (s Settings) dataSource() string {
	switch s.Source {
	case SourceXAPI, SourceGrok:
		return s.Source
	}
	return SourceAuto
}

// grokEvery is how often the digest refreshes.
func (s Settings) grokEvery() time.Duration {
	h := defaultGrokEvery
	if s.GrokEveryHours != nil {
		h = min(max(*s.GrokEveryHours, 1), maxGrokEvery)
	}
	return time.Duration(h) * time.Hour
}

// grokCap is how many grok runs a UTC day may have.
func (s Settings) grokCap() int {
	if s.GrokSearchesPerDay == nil {
		return defaultGrokCap
	}
	return min(max(*s.GrokSearchesPerDay, 0), maxGrokCap)
}

// handles are the watched accounts: valid handles without @, each once, at most handlesMost.
func (s Settings) handles() []string {
	var out []string
	seen := map[string]bool{}
	for _, h := range s.Handles {
		h = strings.TrimPrefix(strings.TrimSpace(h), "@")
		if !validHandle(h) || seen[strings.ToLower(h)] {
			continue
		}
		seen[strings.ToLower(h)] = true
		out = append(out, h)
		if len(out) == handlesMost {
			break
		}
	}
	return out
}

// topics are the topics to search too: trimmed, each once, at most topicsMost of at most topicLong characters.
func (s Settings) topics() []string {
	var out []string
	for _, t := range s.Topics {
		t = clip(oneLine(t), topicLong)
		if t == "" || slices.Contains(out, t) {
			continue
		}
		out = append(out, t)
		if len(out) == topicsMost {
			break
		}
	}
	return out
}

// grokState is what grok_digest.json keeps.
type grokState struct {
	// RefreshedAt: the last refresh that found something; RetryAt: when a failed one tries again.
	RefreshedAt time.Time `json:"refreshed_at,omitzero"`
	RetryAt     time.Time `json:"retry_at,omitzero"`
	// Summaries, Topics: the last refresh's, a summary a run.
	Summaries []string    `json:"summaries,omitempty"`
	Topics    []GrokTopic `json:"topics,omitempty"`
	// Posts are what the refreshes found, newest first, for retention_days.
	Posts []GrokPost `json:"posts,omitempty"`
	// Day is the UTC day Runs counts (2006-01-02).
	Day  string `json:"day,omitempty"`
	Runs int    `json:"runs,omitempty"`
	// NextBatch is the batch of handles the next refresh starts with.
	NextBatch int `json:"next_batch,omitempty"`
	// LastState is how the last run ended (GrokAuth, GrokRate, GrokFailed; "" ok), LastError its line.
	LastState string `json:"last_state,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

// via is the source the page and x_digest use now (c.mu held): SourceXAPI, SourceGrok, or "" (none set up).
func (c *Connector) viaLocked() string {
	switch c.cfg.dataSource() {
	case SourceXAPI:
		return SourceXAPI
	case SourceGrok:
		return SourceGrok
	}
	if c.keys.complete() {
		return SourceXAPI
	}
	if c.grokPath != "" {
		return SourceGrok
	}
	return ""
}

// Via is the source the page and x_digest use now.
func (c *Connector) Via() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.viaLocked()
}

// lookGrok looks for grok (none when the source is x_api).
func (c *Connector) lookGrok() {
	path := ""
	if c.cfg.dataSource() != SourceXAPI {
		path = c.findGrok(c.cfg.GrokPath)
	}
	c.mu.Lock()
	c.grokPath, c.grokLooked = path, true
	c.mu.Unlock()
}

// grokStatusLocked is the status while the X API is not signed in and Grok may be used (c.mu held).
func (c *Connector) grokStatusLocked() connector.Status {
	if c.grokPath == "" {
		st := connector.Status{State: connector.StateNotConfigured, Error: c.tx.grokMissing, Guide: c.tx.grokMissingGuide}
		if c.cfg.dataSource() == SourceAuto { // neither way is set up
			st.Error = c.tx.nothingSetUp
		}
		return st
	}
	st := connector.Status{State: connector.StateOK, Account: "Grok", Via: c.tx.grokVia, LastSync: c.grok.RefreshedAt}
	switch c.grok.LastState {
	case GrokAuth:
		st.State, st.Error, st.Guide = connector.StateNotSignedIn, c.tx.grokAuth, c.tx.grokAuthGuide
	case GrokRate:
		st.State, st.Error = connector.StateRateLimited, c.tx.grokRate
	case GrokFailed:
		st.State, st.Error = connector.StateError, fmt.Sprintf(c.tx.grokFailed, c.grok.LastError)
	}
	return st
}

// grokOnLocked says whether the digest refreshes with Grok (c.mu held).
func (c *Connector) grokOnLocked() bool { return c.viaLocked() == SourceGrok && c.grokPath != "" }

// grokDueLocked says whether a refresh is due at now (c.mu held).
func (c *Connector) grokDueLocked(now time.Time) bool {
	if now.Before(c.grok.RetryAt) {
		return false
	}
	return c.grok.RefreshedAt.IsZero() || !now.Before(c.grok.RefreshedAt.Add(c.cfg.grokEvery()))
}

// runGrok is the Grok loop: it looks for grok and refreshes the digest when it is due (or asked to), until ctx ends.
func (c *Connector) runGrok(ctx context.Context) {
	force := false
	for ctx.Err() == nil {
		c.lookGrok()
		if force && c.grokFresh() { // a tap that waited for the refresh it came during
			force = false
		}
		if _, err := c.RefreshGrok(ctx, force); err != nil && ctx.Err() == nil {
			log.Printf("grok: %v", err)
		}
		force = false
		t := time.NewTimer(c.grokWait())
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case force = <-c.grokPoke:
		case <-t.C:
		}
		t.Stop()
	}
}

// grokWait is how long the loop sleeps: until the next refresh is due, at most grokCheckEvery.
func (c *Connector) grokWait() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	wait := grokCheckEvery
	if !c.grokOnLocked() {
		return wait
	}
	next := c.grok.RefreshedAt.Add(c.cfg.grokEvery())
	if c.grok.RetryAt.After(next) {
		next = c.grok.RetryAt
	}
	if d := next.Sub(c.now()); d < wait {
		wait = max(d, time.Minute)
	}
	return wait
}

// RefreshGrokSoon asks the loop to refresh now (the page's refresh), within the day's cap: false (nothing asked)
// within grokForceGap of the last refresh that found something.
func (c *Connector) RefreshGrokSoon() bool {
	if c.grokFresh() {
		return false
	}
	select {
	case c.grokPoke <- true:
	default:
	}
	return true
}

// grokFresh says whether the last refresh that found something was within grokForceGap.
func (c *Connector) grokFresh() bool {
	c.loadGrok()
	c.mu.Lock()
	defer c.mu.Unlock()
	at := c.grok.RefreshedAt
	return !at.IsZero() && c.now().Before(at.Add(grokForceGap))
}

// grokJob is one run of a refresh: a batch of handles, or the topics.
type grokJob struct {
	handles, topics []string
}

// errGrokCap: the day's grok runs are used up.
var errGrokCap = errors.New("today's Grok runs are used up: Grok searches again after 00:00 UTC, or raise grok_searches_per_day in the extension's settings")

// RefreshGrok refreshes the Grok digest when it is due (or force), answering how many runs found something. A
// refresh already running, Grok not in use, or nothing to search: nothing happens.
func (c *Connector) RefreshGrok(ctx context.Context, force bool) (int, error) {
	if !c.grokLookedOnce() {
		c.lookGrok()
	}
	c.loadGrok()
	c.mu.Lock()
	now := c.now()
	if c.refreshing || !c.grokOnLocked() || (!force && !c.grokDueLocked(now)) {
		c.mu.Unlock()
		return 0, nil
	}
	c.refreshing = true
	path, handles, topics, start := c.grokPath, c.grokHandlesLocked(), c.cfg.topics(), c.grok.NextBatch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.refreshing = false
		c.mu.Unlock()
	}()

	var jobs []grokJob
	batches := slices.Collect(slices.Chunk(handles, grokBatch))
	if n := len(batches); n > 0 {
		start = ((start % n) + n) % n
		for i := range min(n, grokBatchesMost) {
			jobs = append(jobs, grokJob{handles: batches[(start+i)%n]})
		}
	}
	if len(topics) > 0 {
		jobs = append(jobs, grokJob{topics: topics})
	}
	if len(jobs) == 0 {
		return 0, nil
	}
	var (
		found []GrokResult
		// batched: handle batches run; lead: those that found something before the first that did not; batchOK:
		// all that found something.
		batched, lead, batchOK int
		lastKind               string
		lastMsg                string
		capHit                 bool
	)
	for _, j := range jobs {
		prompt, err := c.grokDigestPrompt(j, now)
		if err != nil {
			return 0, err
		}
		if !c.takeGrokRun() {
			capHit = true
			break
		}
		text, err := c.grokRun.Run(ctx, path, prompt)
		if ctx.Err() != nil {
			return len(found), ctx.Err()
		}
		var ge *GrokError
		switch {
		case errors.As(err, &ge):
			lastKind, lastMsg = ge.Kind, ge.Msg
		case err != nil:
			lastKind, lastMsg = GrokFailed, scrubLine(err.Error())
		default:
			res, ok := ParseGrok(text, c.now())
			if !ok {
				lastKind, lastMsg = GrokFailed, "Grok's answer was not the JSON asked for"
				break
			}
			found = append(found, res)
			lastKind, lastMsg = "", ""
		}
		if j.handles != nil {
			if lastKind == "" {
				batchOK++
				if lead == batched {
					lead++
				}
			}
			batched++
		}
		if lastKind == GrokAuth || lastKind == GrokRate {
			break // the user has to act, or wait: no use running the rest
		}
	}

	c.mu.Lock()
	now = c.now()
	if n := len(batches); n > 0 {
		// The next refresh starts with the first batch that failed: a failure that passes (a network blip, the
		// rate limit) does not skip those accounts for a whole turn of the batches. When the first batch failed
		// but a later one did not, it moves on past all that ran, so one batch that always fails does not stall
		// the turn.
		advance := lead
		if lead == 0 && batchOK > 0 {
			advance = batched
		}
		c.grok.NextBatch = (start + advance) % n
	}
	switch {
	case len(found) > 0:
		c.mergeGrokLocked(found, now)
		c.grok.RefreshedAt, c.grok.RetryAt = now, time.Time{}
		c.grok.LastState, c.grok.LastError = "", ""
		if lastKind == GrokAuth || lastKind == GrokRate {
			c.grok.LastState, c.grok.LastError = lastKind, lastMsg
		}
	case lastKind != "":
		c.grok.LastState, c.grok.LastError = lastKind, lastMsg
		retry := grokRetry
		if lastKind == GrokAuth {
			retry = c.cfg.grokEvery() // grok login first: no use asking soon
		}
		c.grok.RetryAt = now.Add(retry)
	}
	c.mu.Unlock()
	if err := c.saveGrok(); err != nil {
		log.Printf("keeping the Grok digest: %v", err)
	}
	if capHit && len(found) == 0 && lastKind == "" {
		return 0, errGrokCap
	}
	return len(found), nil
}

// grokHandlesLocked are the accounts the digest watches (c.mu held): grok_handles, else those the user follows (as
// the X API last read them), the most active in the kept feed first, then those with the most followers.
func (c *Connector) grokHandlesLocked() []string {
	if h := c.cfg.handles(); len(h) > 0 {
		return h
	}
	if len(c.following) == 0 {
		return nil
	}
	last := map[string]time.Time{}
	for _, p := range c.posts {
		if !p.Ref && p.CreatedAt.After(last[p.AuthorID]) {
			last[p.AuthorID] = p.CreatedAt
		}
	}
	accts := slices.Clone(c.following)
	sort.SliceStable(accts, func(i, j int) bool {
		a, b := accts[i], accts[j]
		if !last[a.ID].Equal(last[b.ID]) {
			return last[a.ID].After(last[b.ID])
		}
		if a.Followers != b.Followers {
			return a.Followers > b.Followers
		}
		return strings.ToLower(a.Username) < strings.ToLower(b.Username)
	})
	var out []string
	for _, a := range accts {
		if validHandle(a.Username) && !a.Protected {
			out = append(out, a.Username)
			if len(out) == followingHandles {
				break
			}
		}
	}
	return out
}

// takeGrokRun counts a run against the day's cap and keeps the count at once: false when the cap is reached.
func (c *Connector) takeGrokRun() bool {
	c.loadGrok()
	c.mu.Lock()
	c.rollGrokDayLocked()
	if c.grok.Runs >= c.cfg.grokCap() {
		c.mu.Unlock()
		return false
	}
	c.grok.Runs++
	c.mu.Unlock()
	if err := c.saveGrok(); err != nil {
		log.Printf("keeping the day's Grok runs: %v", err)
	}
	return true
}

// rollGrokDayLocked starts the day's count over on a new UTC day (c.mu held).
func (c *Connector) rollGrokDayLocked() {
	if d := c.today(); c.grok.Day != d {
		c.grok.Day, c.grok.Runs = d, 0
	}
}

// GrokRuns are the day's (UTC) grok runs and the cap.
func (c *Connector) GrokRuns() (used, most int) {
	c.loadGrok()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rollGrokDayLocked()
	return c.grok.Runs, c.cfg.grokCap()
}

// loadGrok reads grok_digest.json once. It is apart from load: the Grok state is not the X sign-in's, so a sign-in
// or sign-out while load reads (or before it runs) does not drop it. Whatever changes or saves the Grok state (a
// run counted, a refresh, x_search) reads it first, so nothing writes a fresh day's count or an empty digest over
// the kept one before it was read; when the source is x_api the file goes instead.
func (c *Connector) loadGrok() {
	c.mu.Lock()
	done := c.grokLoaded
	c.mu.Unlock()
	if done {
		return
	}
	c.disk.Lock() // a save waits for it (and none ran: saveGrok writes only once it was read)
	defer c.disk.Unlock()
	c.mu.Lock()
	done, xapi := c.grokLoaded, c.cfg.dataSource() == SourceXAPI
	c.mu.Unlock()
	if done {
		return
	}
	var gs grokState
	if xapi { // Grok is off: what it found is not kept
		if err := c.store.remove(fileGrok); err != nil {
			log.Printf("removing %s: %v", fileGrok, err)
		}
	} else if err := c.store.read(fileGrok, &gs); err != nil {
		log.Printf("%s unreadable, starting it over: %v", fileGrok, err)
	}
	c.mu.Lock()
	c.grok, c.grokLoaded = gs, true
	c.pruneGrokLocked()
	c.mu.Unlock()
}

// saveGrok writes grok_digest.json (under c.disk, like save), once it was read (loadGrok).
func (c *Connector) saveGrok() error {
	c.disk.Lock()
	defer c.disk.Unlock()
	c.mu.Lock()
	if !c.grokLoaded || c.cfg.dataSource() == SourceXAPI {
		c.mu.Unlock()
		return nil
	}
	st := c.grok
	st.Summaries, st.Topics, st.Posts = slices.Clone(st.Summaries), slices.Clone(st.Topics), slices.Clone(st.Posts)
	c.mu.Unlock()
	return c.store.write(fileGrok, st)
}

// mergeGrokLocked keeps a refresh's results (c.mu held): its summaries and topics replace the last refresh's, its
// posts join those kept (a post found again is updated), pruned and capped.
func (c *Connector) mergeGrokLocked(found []GrokResult, now time.Time) {
	byID := map[string]GrokPost{}
	for _, p := range c.grok.Posts {
		byID[p.ID] = p
	}
	var sums []string
	var topics []GrokTopic
	for _, r := range found {
		if r.Summary != "" && !slices.Contains(sums, r.Summary) {
			sums = append(sums, r.Summary)
		}
		for _, t := range r.Topics { // one topic a title: two runs that found the same topic share it
			i := slices.IndexFunc(topics, func(o GrokTopic) bool { return strings.EqualFold(o.Title, t.Title) })
			if i < 0 {
				topics = append(topics, t)
				continue
			}
			for _, id := range t.PostIDs {
				if !slices.Contains(topics[i].PostIDs, id) && len(topics[i].PostIDs) < 2*grokTopicPosts {
					topics[i].PostIDs = append(topics[i].PostIDs, id)
				}
			}
		}
		for _, p := range r.Posts {
			if old, ok := byID[p.ID]; ok && p.At.IsZero() {
				p.At = old.At
			}
			byID[p.ID] = p
		}
	}
	c.grok.Summaries, c.grok.Topics = sums, topics[:min(len(topics), 2*grokTopicsMost)]
	c.grok.Posts = slices.Collect(func(yield func(GrokPost) bool) {
		for _, p := range byID {
			if !yield(p) {
				return
			}
		}
	})
	c.pruneGrokLocked()
}

// pruneGrokLocked drops Grok's posts older than the retention, newest first, at most grokKeptMost (c.mu held).
func (c *Connector) pruneGrokLocked() {
	keep := c.now().Add(-c.cfg.retention())
	posts := c.grok.Posts[:0:0]
	for _, p := range c.grok.Posts {
		if grokTime(p).After(keep) {
			posts = append(posts, p)
		}
	}
	sortGrok(posts)
	c.grok.Posts = posts[:min(len(posts), grokKeptMost)]
}

// grokTime is when a post was posted, or when Grok found it when it did not say.
func grokTime(p GrokPost) time.Time {
	if !p.At.IsZero() {
		return p.At
	}
	return p.Seen
}

func sortGrok(posts []GrokPost) {
	sort.Slice(posts, func(i, j int) bool {
		a, b := grokTime(posts[i]), grokTime(posts[j])
		if !a.Equal(b) {
			return a.After(b)
		}
		return posts[i].ID > posts[j].ID
	})
}

// language is the language the user reads, for Grok's prompts.
func (c *Connector) language() string {
	switch {
	case strings.HasPrefix(c.locale, "en"):
		return "English"
	case strings.HasPrefix(c.locale, "ja"):
		return "Japanese"
	}
	return "Simplified Chinese"
}

// grokDigestPrompt is the prompt of one digest run.
func (c *Connector) grokDigestPrompt(j grokJob, now time.Time) (string, error) {
	return grokPrompt("grok_digest", map[string]any{
		"Today": now.UTC().Format("2006-01-02"), "From": now.Add(-grokWindow).UTC().Format("2006-01-02 15:04"), "To": now.UTC().Format("2006-01-02 15:04"),
		"Handles": j.handles, "Topics": j.topics, "Language": c.language(), "MaxPosts": grokPostsMost,
	})
}

// GrokDigest is the Grok digest as the page shows it.
type GrokDigest struct {
	RefreshedAt time.Time
	Summaries   []string
	// Topics with their posts, then the other posts of the window.
	Topics []GrokTopicPosts
	Posts  []GrokPost
	// Runs today (UTC) and the cap; Handles and Topics watched.
	Runs, Cap, Handles, TopicsWatched int
}

// GrokTopicPosts is a topic and its kept posts.
type GrokTopicPosts struct {
	GrokTopic
	Posts []GrokPost
}

// GrokDigest is what Grok found: the last refresh's summaries and topics, and the posts of the last 24 hours.
func (c *Connector) GrokDigest() GrokDigest {
	c.loadGrok()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rollGrokDayLocked()
	d := GrokDigest{RefreshedAt: c.grok.RefreshedAt, Summaries: slices.Clone(c.grok.Summaries), Runs: c.grok.Runs, Cap: c.cfg.grokCap(),
		Handles: len(c.grokHandlesLocked()), TopicsWatched: len(c.cfg.topics())}
	byID := map[string]GrokPost{}
	for _, p := range c.grok.Posts {
		byID[p.ID] = p
	}
	inTopic := map[string]bool{}
	for _, t := range c.grok.Topics {
		tp := GrokTopicPosts{GrokTopic: t}
		for _, id := range t.PostIDs {
			if p, ok := byID[id]; ok && !inTopic[id] {
				tp.Posts = append(tp.Posts, p)
				inTopic[id] = true
			}
		}
		d.Topics = append(d.Topics, tp)
	}
	from := c.now().Add(-grokWindow)
	for _, p := range c.grok.Posts {
		if !inTopic[p.ID] && !grokTime(p).Before(from) {
			d.Posts = append(d.Posts, p)
		}
	}
	return d
}

// grokDigestText is x_digest in Grok mode.
func (c *Connector) grokDigestText() string {
	d := c.GrokDigest()
	var b strings.Builder
	b.WriteString(untrustedNote + "\n")
	b.WriteString("X digest found by Grok's X search (a model's search over the watched accounts and topics: it may miss posts or be incomplete; only posts with a real permalink are kept).\n")
	if d.RefreshedAt.IsZero() {
		b.WriteString("Last refresh: not yet.\n")
	} else {
		fmt.Fprintf(&b, "Last refresh: %s. ", c.when(d.RefreshedAt))
	}
	fmt.Fprintf(&b, "Watching %d accounts and %d topics. Grok runs today (UTC): %d of %d.\n", d.Handles, d.TopicsWatched, d.Runs, d.Cap)
	b.WriteString("\n## Summary\n")
	if len(d.Summaries) == 0 {
		b.WriteString("(none)\n")
	}
	for _, s := range d.Summaries {
		b.WriteString(s + "\n\n")
	}
	b.WriteString("\n## Topics\n")
	if len(d.Topics) == 0 {
		b.WriteString("(none)\n")
	}
	for i, t := range d.Topics {
		fmt.Fprintf(&b, "\n%d. %s\n", i+1, t.Title)
		if t.Summary != "" {
			b.WriteString(t.Summary + "\n")
		}
		for _, p := range t.Posts {
			c.grokPostBlock(&b, p)
		}
	}
	b.WriteString("\n## Other posts\n")
	if len(d.Posts) == 0 {
		b.WriteString("(none)\n")
	}
	for _, p := range d.Posts {
		c.grokPostBlock(&b, p)
	}
	return b.String()
}

// grokPostBlock is one post Grok found.
func (c *Connector) grokPostBlock(b *strings.Builder, p GrokPost) {
	who := "@" + p.Author
	if p.Name != "" {
		who = p.Name + " @" + p.Author
	}
	at := "time unknown"
	if !p.At.IsZero() {
		at = c.when(p.At)
	}
	fmt.Fprintf(b, "\n%s · %s\n%s\n", who, at, p.Text)
	if p.Why != "" {
		fmt.Fprintf(b, "(Grok: %s)\n", p.Why)
	}
	b.WriteString(p.Permalink() + "\n")
}

// grokFeedText is x_feed in Grok mode: the posts Grok found.
func (c *Connector) grokFeedText(query, author string, hours, limit int) string {
	if hours <= 0 {
		hours = 24
	}
	hours = min(hours, maxRetention*24)
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 50)
	query, author = strings.ToLower(strings.TrimSpace(query)), strings.ToLower(strings.TrimPrefix(strings.TrimSpace(author), "@"))
	c.mu.Lock()
	posts := slices.Clone(c.grok.Posts)
	from := c.now().Add(-time.Duration(hours) * time.Hour)
	c.mu.Unlock()
	var b strings.Builder
	n := 0
	for _, p := range posts {
		if n >= limit {
			break
		}
		if grokTime(p).Before(from) || query != "" && !strings.Contains(strings.ToLower(p.Text), query) ||
			author != "" && !strings.Contains(strings.ToLower(p.Author), author) && !strings.Contains(strings.ToLower(p.Name), author) {
			continue
		}
		n++
		c.grokPostBlock(&b, p)
	}
	head := fmt.Sprintf("%s\n%d posts Grok found in the last %d hours, newest first (Grok's search may have missed some).\n", untrustedNote, n, hours)
	if n == 0 {
		head += "None match. Use x_search to search X live.\n"
	}
	return head + b.String()
}

// grokFollowingText is x_following in Grok mode: the accounts the digest watches.
func (c *Connector) grokFollowingText(query string, limit int) string {
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 100)
	query = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(query), "@"))
	c.mu.Lock()
	handles := c.grokHandlesLocked()
	fromConfig := len(c.cfg.handles()) > 0
	byName := map[string]Account{}
	for _, a := range c.following {
		byName[strings.ToLower(a.Username)] = a
	}
	c.mu.Unlock()
	var b strings.Builder
	n := 0
	for _, h := range handles {
		if n >= limit {
			break
		}
		a, known := byName[strings.ToLower(h)]
		if query != "" && !strings.Contains(strings.ToLower(h+"\n"+a.Name+"\n"+a.Description), query) {
			continue
		}
		n++
		if known && a.Name != "" {
			fmt.Fprintf(&b, "%s @%s · https://x.com/%s\n", a.Name, h, h)
		} else {
			fmt.Fprintf(&b, "@%s · https://x.com/%s\n", h, h)
		}
	}
	src := "the extension's handles setting"
	if !fromConfig {
		src = "the accounts the user follows, as the X API last read them"
	}
	head := fmt.Sprintf("%s\n%d of the %d accounts Grok's digest watches (from %s).\n", untrustedNote, n, len(handles), src)
	if len(handles) == 0 {
		head += "None: add accounts to the handles setting of the extension (Grok cannot read who the user follows).\n"
	}
	return head + b.String()
}

// searchArgs are x_search's arguments.
type searchArgs struct {
	Query   string   `json:"query"`
	Handles []string `json:"handles"`
	From    string   `json:"from"`
	To      string   `json:"to"`
}

// Bounds of x_search.
const (
	searchQueryMost = 300
)

// checkSearch holds x_search's arguments to their bounds and normalizes them.
func checkSearch(a searchArgs) (searchArgs, error) {
	a.Query = strings.TrimSpace(a.Query)
	if a.Query == "" {
		return a, errors.New("query is required")
	}
	if utf8.RuneCountInString(a.Query) > searchQueryMost {
		return a, fmt.Errorf("query is over %d characters", searchQueryMost)
	}
	if len(a.Handles) > grokBatch {
		return a, fmt.Errorf("at most %d handles", grokBatch)
	}
	var hs []string
	for _, h := range a.Handles {
		h = strings.TrimPrefix(strings.TrimSpace(h), "@")
		if !validHandle(h) {
			return a, fmt.Errorf("handle %q is not an X username", clip(h, 40))
		}
		if !slices.ContainsFunc(hs, func(o string) bool { return strings.EqualFold(o, h) }) {
			hs = append(hs, h)
		}
	}
	a.Handles = hs
	var from, to time.Time
	for _, d := range []struct {
		v *string
		t *time.Time
		n string
	}{{&a.From, &from, "from"}, {&a.To, &to, "to"}} {
		*d.v = strings.TrimSpace(*d.v)
		if *d.v == "" {
			continue
		}
		t, err := time.Parse("2006-01-02", *d.v)
		if err != nil {
			return a, fmt.Errorf("%s must be a date as YYYY-MM-DD, not %q", d.n, clip(*d.v, 40))
		}
		*d.t = t
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return a, errors.New("from is after to")
	}
	return a, nil
}

// Errors of x_search.
var (
	errSearchXAPI = errors.New("x_search needs Grok, and the extension's source setting is x_api: set it to auto or grok")
	errNoGrok     = errors.New("x_search needs the grok CLI on the hub machine (install it with `curl -fsSL https://x.ai/cli/install.sh | bash`, then run `grok login` with a SuperGrok or X Premium+ subscription)")
)

// searchText is x_search: one live grok run.
func (c *Connector) searchText(ctx context.Context, a searchArgs) (string, error) {
	a, err := checkSearch(a)
	if err != nil {
		return "", err
	}
	if !c.grokLookedOnce() {
		c.lookGrok()
	}
	c.mu.Lock()
	path, src := c.grokPath, c.cfg.dataSource()
	c.mu.Unlock()
	switch {
	case src == SourceXAPI:
		return "", errSearchXAPI
	case path == "":
		return "", errNoGrok
	}
	now := c.now()
	prompt, err := grokPrompt("grok_search", map[string]any{"Today": now.UTC().Format("2006-01-02"), "Query": a.Query, "Handles": a.Handles,
		"From": a.From, "To": a.To, "Language": c.language(), "MaxPosts": grokPostsMost})
	if err != nil {
		return "", err
	}
	if !c.takeGrokRun() {
		return "", errGrokCap
	}
	text, err := c.grokRun.Run(ctx, path, prompt)
	if err != nil {
		var ge *GrokError
		if errors.As(err, &ge) {
			c.noteGrok(ge)
			switch ge.Kind {
			case GrokAuth:
				return "", errors.New("grok is not signed in on the hub machine, or its sign-in expired (sign-ins last 7 days): run `grok login` there")
			case GrokRate:
				return "", errors.New("Grok's rate limit or the subscription's quota was reached: try again later")
			}
			return "", fmt.Errorf("Grok's search failed: %s", ge.Msg)
		}
		return "", err
	}
	res, ok := ParseGrok(text, c.now())
	if !ok {
		return "", errors.New("Grok's answer was not the JSON asked for: try again, or rephrase the query")
	}
	c.noteGrok(nil)
	used, most := c.GrokRuns()
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nGrok searched X for %q (a model's search: it may miss posts; only posts with a real permalink are kept). Grok runs today (UTC): %d of %d.\n",
		untrustedNote, a.Query, used, most)
	b.WriteString("\n## Summary\n")
	if res.Summary == "" {
		b.WriteString("(none)\n")
	} else {
		b.WriteString(res.Summary + "\n")
	}
	fmt.Fprintf(&b, "\n## Posts (%d)\n", len(res.Posts))
	if len(res.Posts) == 0 {
		b.WriteString("(none found)\n")
	}
	for _, p := range res.Posts {
		c.grokPostBlock(&b, p)
	}
	return b.String(), nil
}

// grokLookedOnce says whether grok was looked for yet.
func (c *Connector) grokLookedOnce() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.grokLooked
}

// noteGrok keeps how a run outside the digest ended, for the status: an auth or rate failure, or (nil) a success
// that clears one.
func (c *Connector) noteGrok(ge *GrokError) {
	c.loadGrok()
	c.mu.Lock()
	switch {
	case ge == nil:
		if c.grok.LastState == GrokAuth || c.grok.LastState == GrokRate {
			c.grok.LastState, c.grok.LastError = "", ""
		}
	case ge.Kind == GrokAuth || ge.Kind == GrokRate:
		c.grok.LastState, c.grok.LastError = ge.Kind, ge.Msg
	}
	c.mu.Unlock()
	if err := c.saveGrok(); err != nil {
		log.Printf("keeping the Grok state: %v", err)
	}
}
