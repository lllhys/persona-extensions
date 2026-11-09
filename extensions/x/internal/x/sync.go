package x

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"time"

	"github.com/nwverse/persona-extensions/extensions/x/internal/connector"
)

// One sync: the day's budget rolled over, old posts dropped, who the user follows (once a day), the feed (the home
// timeline or the chosen lists, within the budget), the mentions, and the user's own recent posts when a mention is
// still open (to tell it was answered). Everything X answered is kept before the next request, so a failure halfway
// loses nothing already paid for.

// perPostWorst is what one post of the feed can cost at most: the post, the two posts it can reference (a reply that
// quotes), and the three users they are by. A page of n posts is asked for only while n*perPostWorst fits in the
// budget left, and that worst case is reserved until the answer is counted (c.reserved), so requests running at
// once (the sync, x_post) cannot together pass the budget either: the day's count (what X really returned) never
// passes it.
const perPostWorst = 6

// Pages read at most in one sync.
const (
	homePagesMost      = 20
	listPagesMost      = 5
	followingPagesMost = 20
	mentionsMost       = 100 // a page
	mentionsPagesMost  = 3
)

// Sync reads X once.
func (c *Connector) Sync(ctx context.Context) error {
	c.mu.Lock()
	if c.syncing || !c.keys.complete() {
		c.mu.Unlock()
		return nil
	}
	c.syncing = true
	k, me, gen := c.keys, c.me, c.gen
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.syncing = false
		c.mu.Unlock()
	}()

	c.prune()
	if me.ID == "" { // a sign-in kept by an older version
		var u oneUser
		if err := c.api.get(ctx, k, "/2/users/me", url.Values{"user.fields": {"id,name,username"}}, &u); err != nil {
			return c.failed(err, gen)
		}
		if u.Data == nil || !validID(u.Data.ID) {
			return c.failed(errUnauthorized, gen)
		}
		me = Account{ID: u.Data.ID, Username: u.Data.Username, Name: u.Data.Name}
		if !c.apply(gen, func() { c.me = me }) {
			return nil
		}
	}

	var errs []error
	var limited *rateLimited
	step := func(err error) bool { // false: stop the sync (the user has to act, or it was signed out)
		if err == nil {
			return true
		}
		if errors.Is(err, errUnauthorized) || errors.Is(err, errCredits) || errors.Is(err, errSignedOut) || ctx.Err() != nil {
			errs = append(errs, err)
			return false
		}
		var rl *rateLimited
		if errors.As(err, &rl) {
			if limited == nil || rl.Until.After(limited.Until) {
				limited = rl
			}
			return true
		}
		errs = append(errs, err)
		return true
	}
	ok := step(c.syncFollowing(ctx, k, me, gen))
	if ok {
		ok = step(c.syncFeed(ctx, k, me, gen))
	}
	newMentions := 0
	if ok {
		var err error
		newMentions, err = c.syncMentions(ctx, k, me, gen)
		ok = step(err)
	}
	if ok {
		step(c.syncOwn(ctx, k, me, gen))
	}
	if !c.apply(gen, func() {}) {
		return nil
	}
	if err := c.save(gen); err != nil {
		log.Printf("keeping what was read: %v", err)
	}
	c.mu.Lock()
	notify := c.notify
	c.mu.Unlock()
	if newMentions > 0 && notify != nil {
		notify()
	}
	var err error
	switch {
	case len(errs) > 0:
		err = errs[0]
	case limited != nil:
		err = limited
	}
	return c.failed(err, gen)
}

// errSignedOut: the user signed out (or in as someone else) while a sync was running: what it read is dropped.
var errSignedOut = errors.New("signed out during the sync")

// apply runs f under the lock if the sign-in is still the one the sync started with.
func (c *Connector) apply(gen int, f func()) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return false
	}
	f()
	return true
}

// failed sets the status for how the sync ended (nil: ok) and answers err.
func (c *Connector) failed(err error, gen int) error {
	var st connector.Status
	var rl *rateLimited
	switch {
	case err == nil:
		st = connector.Status{State: connector.StateOK}
	case errors.Is(err, errSignedOut):
		return nil
	case errors.Is(err, errUnauthorized):
		st = connector.Status{State: connector.StateError, Error: c.tx.unauthorized, Guide: c.tx.unauthorizedGuide}
	case errors.Is(err, errCredits):
		st = connector.Status{State: connector.StateError, Error: c.tx.credits, Guide: c.tx.creditsGuide}
	case errors.As(err, &rl):
		st = connector.Status{State: connector.StateRateLimited, Error: fmt.Sprintf(c.tx.rateLimited, c.clock(rl.Until))}
	case errors.Is(err, context.Canceled):
		return err
	default:
		st = connector.Status{State: connector.StateError, Error: fmt.Sprintf(c.tx.failed, clip(err.Error(), 200))}
	}
	c.apply(gen, func() {
		c.status = st
		if rl != nil {
			c.rateUntil = rl.Until
		}
	})
	return err
}

// today is the UTC day the budget counts.
func (c *Connector) today() string { return c.now().UTC().Format("2006-01-02") }

// budgetLeftLocked is what is left of the day's budget for another request (c.mu held), rolling the day over: the
// budget less what was spent and what requests in flight reserved.
func (c *Connector) budgetLeftLocked() int {
	if d := c.today(); c.state.BudgetDay != d {
		c.state.BudgetDay, c.state.BudgetUsed = d, 0
	}
	return max(c.cfg.budget()-c.state.BudgetUsed-c.reserved, 0)
}

// reserveLocked holds n posts' worst case for a request (c.mu held): 0 when n is 0. The request gives it back with
// settleLocked once it answered or failed.
func (c *Connector) reserveLocked(n int) int {
	held := n * perPostWorst
	c.reserved += held
	return held
}

// settleLocked gives back what a request reserved and counts what its answer cost (c.mu held).
func (c *Connector) settleLocked(held, spent int) {
	c.reserved -= held
	c.spendLocked(spent)
}

// Budget is the day's use and the budget.
func (c *Connector) Budget() (used, budget int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.budgetLeftLocked()
	return c.state.BudgetUsed, c.cfg.budget()
}

// pageSize is how many posts a request may ask for with left of the budget: 0 when not even min fits.
func pageSize(left, most, least int) int {
	n := min(most, left/perPostWorst)
	if n < least {
		return 0
	}
	return n
}

// spend counts what an answer cost (c.mu held).
func (c *Connector) spendLocked(n int) {
	c.budgetLeftLocked()
	c.state.BudgetUsed += n
}

// prune drops posts older than the retention, mentions and answers older than mentionDays.
func (c *Connector) prune() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	keep, mkeep := now.Add(-c.cfg.retention()), now.Add(-mentionDays*24*time.Hour)
	for id, p := range c.posts {
		if p.CreatedAt.Before(keep) {
			delete(c.posts, id)
		}
	}
	for id, p := range c.mentions {
		if p.CreatedAt.Before(mkeep) {
			delete(c.mentions, id)
		}
	}
	for conv, at := range c.state.Replied {
		if at.Before(mkeep) {
			delete(c.state.Replied, conv)
		}
	}
}

// syncFollowing reads who the user follows once every followingEvery.
func (c *Connector) syncFollowing(ctx context.Context, k Keys, me Account, gen int) error {
	c.mu.Lock()
	due := c.state.FollowingAt.IsZero() || c.now().Sub(c.state.FollowingAt) >= followingEvery
	c.mu.Unlock()
	if !due {
		return nil
	}
	var all []Account
	token := ""
	for range followingPagesMost {
		q := url.Values{"max_results": {"1000"}, "user.fields": {followFields}}
		if token != "" {
			q.Set("pagination_token", token)
		}
		var page usersPage
		if err := c.api.get(ctx, k, "/2/users/"+me.ID+"/following", q, &page); err != nil {
			return err
		}
		for _, u := range page.Data {
			all = append(all, accountOf(u))
		}
		if token = page.Meta.NextToken; token == "" {
			break
		}
	}
	if !c.apply(gen, func() { c.following, c.state.FollowingAt = all, c.now() }) {
		return errSignedOut
	}
	return nil
}

func accountOf(u apiUser) Account {
	return Account{ID: u.ID, Name: u.Name, Username: u.Username, Description: u.Description, Followers: u.PublicMetrics.Followers,
		Following: u.PublicMetrics.Following, Posts: u.PublicMetrics.Posts, Verified: u.Verified, Protected: u.Protected, Avatar: u.ProfileImage}
}

// syncFeed reads the feed's source.
func (c *Connector) syncFeed(ctx context.Context, k Keys, me Account, gen int) error {
	if c.cfg.dataSource() == SourceGrok { // the digest comes from Grok: no paid timeline
		return nil
	}
	var err error
	switch c.cfg.source() {
	case FeedHome:
		err = c.syncHome(ctx, k, me, gen)
	case FeedLists:
		for _, id := range c.cfg.lists() {
			if err = c.syncList(ctx, k, id, gen); err != nil {
				break
			}
		}
	default:
		return nil
	}
	if err == nil && !c.apply(gen, func() { c.state.FeedAt = c.now() }) {
		return errSignedOut
	}
	return err
}

// cursorRef points at one newest-first read's cursor in syncState. Everything up to *since was read, or skipped on
// purpose (the day's budget ran out). A read that stopped halfway (an error, a 429, the sync's time, the run's pages)
// leaves a gap: *gapTop is the newest post it read and *gapUntil the oldest, so what lies between *since and
// *gapUntil is still to read. The next run reads the gap first (since_id, until_id) and only then what is newer than
// *gapTop, so a failure loses nothing and nothing is asked for twice.
type cursorRef struct{ since, gapTop, gapUntil *string }

func homeCursor(s *syncState) cursorRef {
	return cursorRef{&s.HomeSince, &s.HomeGapTop, &s.HomeGapUntil}
}
func mentionsCursor(s *syncState) cursorRef {
	return cursorRef{&s.MentionsSince, &s.MentionsGapTop, &s.MentionsGapUntil}
}

// cursor is a cursorRef's values.
type cursor struct{ since, gapTop, gapUntil string }

// pageFetch asks for one page newer than since and older than until ("": unbounded), continuing token. A nil page
// with a nil error: the budget says stop, which counts as finished (what is older is skipped on purpose).
type pageFetch func(since, until, token string) (*tweetsPage, error)

// readNewest reads a newest-first source with since_id / until_id through the cursor at ref, at most pages pages.
func (c *Connector) readNewest(gen, pages int, ref func(*syncState) cursorRef, fetch pageFetch) error {
	c.mu.Lock()
	r := ref(&c.state)
	cur := cursor{*r.since, *r.gapTop, *r.gapUntil}
	c.mu.Unlock()
	keep := func() bool {
		return c.apply(gen, func() {
			r := ref(&c.state)
			*r.since, *r.gapTop, *r.gapUntil = cur.since, cur.gapTop, cur.gapUntil
		})
	}
	for {
		gap := cur.gapUntil != ""
		newest, oldest, token := "", "", ""
		finished := false
		var err error
		for pages > 0 {
			pages--
			page, ferr := fetch(cur.since, cur.gapUntil, token)
			if ferr != nil {
				err = ferr
				break
			}
			if page == nil {
				finished = true
				break
			}
			if newest == "" {
				newest = page.Meta.NewestID
			}
			if o := page.Meta.OldestID; o != "" {
				oldest = o
			} else if len(page.Data) > 0 {
				oldest = page.Data[len(page.Data)-1].ID
			}
			if token = page.Meta.NextToken; token == "" || len(page.Data) == 0 {
				finished = true
				break
			}
		}
		top := cur.gapTop
		if !gap {
			top = newest
		}
		if !finished { // stopped halfway: keep (or narrow) the gap, read it next time
			if top != "" {
				if oldest == "" {
					oldest = cur.gapUntil
				}
				cur.gapTop, cur.gapUntil = top, oldest
			}
			if !keep() {
				return errSignedOut
			}
			return err
		}
		if top != "" {
			cur.since = top
		}
		cur.gapTop, cur.gapUntil = "", ""
		if !keep() {
			return errSignedOut
		}
		if !gap || pages == 0 {
			return nil // read up to the newest; or the gap closed and what is newer waits for the next run
		}
	}
}

// syncHome reads the home timeline since the newest post read, newest first, page by page while the budget lasts.
func (c *Connector) syncHome(ctx context.Context, k Keys, me Account, gen int) error {
	start := c.now().Add(-c.cfg.retention()).UTC().Format(time.RFC3339)
	return c.readNewest(gen, homePagesMost, homeCursor, func(since, until, token string) (*tweetsPage, error) {
		c.mu.Lock()
		n := pageSize(c.budgetLeftLocked(), 100, 1)
		held := c.reserveLocked(n)
		c.mu.Unlock()
		if n == 0 {
			return nil, nil // the day's budget is spent
		}
		q := postQuery()
		q.Set("max_results", strconv.Itoa(n))
		if since != "" {
			q.Set("since_id", since)
		} else {
			q.Set("start_time", start)
		}
		if until != "" {
			q.Set("until_id", until)
		}
		if token != "" {
			q.Set("pagination_token", token)
		}
		var page tweetsPage
		err := c.api.get(ctx, k, "/2/users/"+me.ID+"/timelines/reverse_chronological", q, &page)
		c.mu.Lock()
		defer c.mu.Unlock()
		if err != nil {
			c.settleLocked(held, 0)
			return nil, err
		}
		if gen != c.gen {
			c.reserved -= held
			return nil, errSignedOut
		}
		c.settleLocked(held, page.resources())
		c.ingestLocked(page, FeedHome)
		return &page, nil
	})
}

// syncList reads a list's posts, newest first, until a post already kept (lists have no since_id).
func (c *Connector) syncList(ctx context.Context, k Keys, id string, gen int) error {
	token := ""
	cutoff := c.now().Add(-c.cfg.retention())
	for range listPagesMost {
		c.mu.Lock()
		n := pageSize(c.budgetLeftLocked(), 100, 1)
		held := c.reserveLocked(n)
		c.mu.Unlock()
		if n == 0 {
			return nil
		}
		q := postQuery()
		q.Set("max_results", strconv.Itoa(n))
		if token != "" {
			q.Set("pagination_token", token)
		}
		var page tweetsPage
		err := c.api.get(ctx, k, "/2/lists/"+id+"/tweets", q, &page)
		c.mu.Lock()
		c.reserved -= held
		c.mu.Unlock()
		if err != nil {
			return err
		}
		stop := false
		if !c.apply(gen, func() {
			c.spendLocked(page.resources())
			for i, t := range page.Data {
				if p, ok := c.posts[t.ID]; (ok && !p.Ref) || t.CreatedAt.Before(cutoff) {
					page.Data, stop = page.Data[:i], true
					break
				}
			}
			c.ingestLocked(page, FeedLists)
		}) {
			return errSignedOut
		}
		if token = page.Meta.NextToken; stop || token == "" || len(page.Data) == 0 {
			return nil
		}
	}
	return nil
}

// ingestLocked keeps a page's posts (c.mu held): its data as the feed's, its includes.tweets as referenced posts.
func (c *Connector) ingestLocked(page tweetsPage, source string) {
	users := map[string]apiUser{}
	for _, u := range page.Includes.Users {
		users[u.ID] = u
	}
	now := c.now()
	for _, t := range page.Includes.Tweets {
		if old, ok := c.posts[t.ID]; ok && !old.Ref {
			continue
		}
		p := postOf(t, users, now)
		p.Ref = true
		c.posts[t.ID] = p
	}
	for _, t := range page.Data {
		p := postOf(t, users, now)
		p.Source = source
		c.posts[t.ID] = p
	}
}

// postOf is a post as kept, its author from users.
func postOf(t apiTweet, users map[string]apiUser, seen time.Time) Post {
	p := Post{ID: t.ID, AuthorID: t.AuthorID, Text: t.text(), CreatedAt: t.CreatedAt, ConversationID: t.ConversationID, Lang: t.Lang,
		InReplyToUser: t.InReplyToUser, Likes: t.PublicMetrics.Likes, Reposts: t.PublicMetrics.Reposts, Replies: t.PublicMetrics.Replies,
		Quotes: t.PublicMetrics.Quotes, RepostOf: t.ref("retweeted"), QuoteOf: t.ref("quoted"), ReplyTo: t.ref("replied_to"), Seen: seen}
	if u, ok := users[t.AuthorID]; ok {
		p.Username, p.Name, p.Followers, p.Verified = u.Username, u.Name, u.PublicMetrics.Followers, u.Verified
	}
	for _, l := range t.Entities.URLs {
		if l.ExpandedURL != "" {
			p.Links = append(p.Links, Link{Expanded: l.ExpandedURL, Display: l.DisplayURL})
		}
	}
	return p
}

// syncMentions reads the posts mentioning the user since the newest read, through a cursor like the home
// timeline's: at most mentionsPagesMost pages of mentionsMost a sync, a backlog beyond that read on the next runs
// (the first time those of the last mentionDays). Owned Reads: not counted against the feed's budget.
func (c *Connector) syncMentions(ctx context.Context, k Keys, me Account, gen int) (int, error) {
	start := c.now().Add(-mentionDays*24*time.Hour + time.Minute).UTC().Format(time.RFC3339)
	cutoff := c.now().Add(-mentionDays * 24 * time.Hour)
	added := 0
	err := c.readNewest(gen, mentionsPagesMost, mentionsCursor, func(since, until, token string) (*tweetsPage, error) {
		q := url.Values{"max_results": {strconv.Itoa(mentionsMost)}, "tweet.fields": {tweetFields}, "expansions": {"author_id"}, "user.fields": {feedUsers}}
		if since != "" {
			q.Set("since_id", since)
		} else {
			q.Set("start_time", start)
		}
		if until != "" {
			q.Set("until_id", until)
		}
		if token != "" {
			q.Set("pagination_token", token)
		}
		var page tweetsPage
		if err := c.api.get(ctx, k, "/2/users/"+me.ID+"/mentions", q, &page); err != nil {
			return nil, err
		}
		users := map[string]apiUser{}
		for _, u := range page.Includes.Users {
			users[u.ID] = u
		}
		if !c.apply(gen, func() {
			for _, t := range page.Data {
				if t.AuthorID == me.ID || t.CreatedAt.Before(cutoff) {
					continue
				}
				if _, ok := c.mentions[t.ID]; !ok {
					added++
				}
				c.mentions[t.ID] = postOf(t, users, c.now())
			}
		}) {
			return nil, errSignedOut
		}
		return &page, nil
	})
	if err != nil {
		return added, err
	}
	if !c.apply(gen, func() { c.state.MentionsAt = c.now() }) {
		return added, errSignedOut
	}
	return added, nil
}

// syncOwn reads the user's own posts since the newest read, only while a mention is still open: a post of theirs in
// a mention's conversation after it marks the mention answered. Owned Reads, not counted against the budget.
func (c *Connector) syncOwn(ctx context.Context, k Keys, me Account, gen int) error {
	c.mu.Lock()
	open := false
	for _, m := range c.mentions {
		if !c.answeredLocked(m) {
			open = true
			break
		}
	}
	since := c.state.OwnSince
	c.mu.Unlock()
	if !open {
		return nil
	}
	q := url.Values{"max_results": {"100"}, "tweet.fields": {"created_at,conversation_id"}}
	if since != "" {
		q.Set("since_id", since)
	} else {
		q.Set("start_time", c.now().Add(-mentionDays*24*time.Hour+time.Minute).UTC().Format(time.RFC3339))
	}
	var page tweetsPage
	if err := c.api.get(ctx, k, "/2/users/"+me.ID+"/tweets", q, &page); err != nil {
		return err
	}
	if !c.apply(gen, func() {
		if c.state.Replied == nil {
			c.state.Replied = map[string]time.Time{}
		}
		for _, t := range page.Data {
			if t.ConversationID != "" && t.CreatedAt.After(c.state.Replied[t.ConversationID]) {
				c.state.Replied[t.ConversationID] = t.CreatedAt
			}
		}
		if page.Meta.NewestID != "" {
			c.state.OwnSince = page.Meta.NewestID
		}
	}) {
		return errSignedOut
	}
	return nil
}

// answeredLocked says whether the user posted in m's conversation after it (c.mu held).
func (c *Connector) answeredLocked(m Post) bool {
	at, ok := c.state.Replied[m.ConversationID]
	return ok && at.After(m.CreatedAt)
}
