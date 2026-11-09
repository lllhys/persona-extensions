package x

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
)

// The organized feed: what was read in the last 24 hours (or 3 days), sorted the same way every time from what is
// kept, no model involved. Four sections of at most digestMost each: the posts that drew the most engagement for
// their author's reach, the links shared by the most accounts, the threads (an author's chain of replies to themself),
// and the followed accounts that posted the most. Not a timeline: whatever is not in those sections is not shown.

// Windows of the digest.
const (
	Window24h = "24h"
	Window3d  = "3d"
)

// digestMost is how many each section shows at most.
const digestMost = 10

// threadLeast is how many posts a thread has at least.
const threadLeast = 3

// WindowOf is a window's length; anything but Window3d is 24 hours.
func WindowOf(w string) (string, time.Duration) {
	if w == Window3d {
		return Window3d, 72 * time.Hour
	}
	return Window24h, 24 * time.Hour
}

// Digest is the organized feed of one window.
type Digest struct {
	Window  string
	Top     []Ranked
	Links   []Shared
	Threads []Thread
	Active  []Active
	// Seen is how many posts of the feed (reposts too) were read in the window.
	Seen int
	// BudgetUsed / Budget: the day's (UTC) posts and users read of the feed's budget.
	BudgetUsed, Budget int
}

// Ranked is an original post, the reposts of it collapsed into it.
type Ranked struct {
	Post
	RepostedBy []string // usernames of those who reposted it in the window
	Score      float64
	At         time.Time // the newest of the post and its reposts
}

// Shared is a link and who shared it.
type Shared struct {
	URL, Display string
	Authors      []string // usernames, the first sharer first
	Posts        int
	Last         time.Time
	Example      Post // the newest post with it
}

// Thread is an author's chain of replies to themself in one conversation.
type Thread struct {
	Root  Post
	Count int
	Last  time.Time
}

// Active is a followed account and how many posts of theirs the feed had.
type Active struct {
	Account
	Posts int
}

// score is a post's engagement for its author's reach: likes, reposts and quotes twice, replies, over
// log2(followers+2), so a large account's ordinary post does not crowd out a small one's that drew attention.
func score(p Post) float64 {
	e := float64(p.Likes + 2*p.Reposts + 2*p.Quotes + p.Replies)
	return e / math.Log2(float64(p.Followers)+2)
}

// BuildDigest is the digest of posts (the kept ones) over window ending at now, for the followed accounts.
func BuildDigest(posts []Post, following []Account, now time.Time, window string) Digest {
	name, d := WindowOf(window)
	from := now.Add(-d)
	in := func(t time.Time) bool { return !t.Before(from) && !t.After(now.Add(time.Hour)) }
	byID := map[string]Post{}
	for _, p := range posts {
		byID[p.ID] = p
	}
	out := Digest{Window: name}

	// Originals and who reposted them.
	cands := map[string]*Ranked{}
	reposters := map[string]map[string]bool{}
	get := func(p Post) *Ranked {
		r, ok := cands[p.ID]
		if !ok {
			r = &Ranked{Post: p, Score: score(p), At: p.CreatedAt}
			cands[p.ID] = r
			reposters[p.ID] = map[string]bool{}
		}
		return r
	}
	for _, p := range posts {
		if p.Ref || !in(p.CreatedAt) {
			continue
		}
		out.Seen++
		if p.RepostOf == "" {
			get(p)
			continue
		}
		orig, ok := byID[p.RepostOf]
		if !ok {
			continue
		}
		r := get(orig)
		if p.CreatedAt.After(r.At) {
			r.At = p.CreatedAt
		}
		if p.Username != "" && !reposters[orig.ID][p.Username] {
			reposters[orig.ID][p.Username] = true
			r.RepostedBy = append(r.RepostedBy, p.Username)
		}
	}
	ranked := make([]Ranked, 0, len(cands))
	for _, r := range cands {
		sort.Strings(r.RepostedBy)
		ranked = append(ranked, *r)
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if !a.At.Equal(b.At) {
			return a.At.After(b.At)
		}
		return a.ID > b.ID
	})
	out.Links = sharedLinks(ranked)
	out.Top = ranked[:min(len(ranked), digestMost)]
	out.Threads = threads(posts, in)
	out.Active = active(posts, following, in)
	return out
}

// sharedLinks are the links of the originals (shared by their author and whoever reposted them), by how many
// accounts shared each.
func sharedLinks(ranked []Ranked) []Shared {
	type acc struct {
		Shared
		who map[string]bool
	}
	groups := map[string]*acc{}
	// Oldest first, so a link's first sharer comes first and its example ends up the newest.
	order := append([]Ranked(nil), ranked...)
	sort.Slice(order, func(i, j int) bool {
		if !order[i].At.Equal(order[j].At) {
			return order[i].At.Before(order[j].At)
		}
		return order[i].ID < order[j].ID
	})
	for _, r := range order {
		seenHere := map[string]bool{}
		for _, l := range r.Links {
			key, ok := normalizeURL(l.Expanded)
			if !ok || seenHere[key] {
				continue
			}
			seenHere[key] = true
			g, ok := groups[key]
			if !ok {
				g = &acc{Shared: Shared{URL: l.Expanded, Display: l.Display}, who: map[string]bool{}}
				groups[key] = g
			}
			g.Posts++
			for _, u := range append([]string{r.Username}, r.RepostedBy...) {
				if u != "" && !g.who[u] {
					g.who[u] = true
					g.Authors = append(g.Authors, u)
				}
			}
			if !r.At.Before(g.Last) {
				g.Last, g.Example = r.At, r.Post
			}
		}
	}
	out := make([]Shared, 0, len(groups))
	for _, g := range groups {
		out = append(out, g.Shared)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case len(a.Authors) != len(b.Authors):
			return len(a.Authors) > len(b.Authors)
		case a.Posts != b.Posts:
			return a.Posts > b.Posts
		case !a.Last.Equal(b.Last):
			return a.Last.After(b.Last)
		}
		return a.URL < b.URL
	})
	return out[:min(len(out), digestMost)]
}

// trackingParams are query parameters that only say where a click came from: a link without them is the same link.
var trackingParams = map[string]bool{"fbclid": true, "gclid": true, "igshid": true, "mc_cid": true, "mc_eid": true, "ref_src": true}

// normalizeURL is the key links are grouped by (host without www., path without a trailing slash, the query without
// tracking parameters, no fragment, http and https alike); false for a link that is not grouped: not http(s), or a
// post on X itself.
func normalizeURL(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	host = strings.TrimPrefix(host, "mobile.")
	if (host == "x.com" || host == "twitter.com") && strings.Contains(u.Path, "/status/") {
		return "", false
	}
	q := u.Query()
	for k := range q {
		if strings.HasPrefix(strings.ToLower(k), "utm_") || trackingParams[strings.ToLower(k)] {
			q.Del(k)
		}
	}
	key := host + strings.TrimRight(u.EscapedPath(), "/")
	if enc := q.Encode(); enc != "" { // Encode sorts by name
		key += "?" + enc
	}
	return key, true
}

// threads are the conversations where one author replied to themself, threadLeast posts or more, newest first.
func threads(posts []Post, in func(time.Time) bool) []Thread {
	type key struct{ conv, author string }
	groups := map[key][]Post{}
	for _, p := range posts {
		if p.RepostOf != "" || p.ConversationID == "" || !in(p.CreatedAt) {
			continue
		}
		if p.ID == p.ConversationID || (p.ReplyTo != "" && p.InReplyToUser == p.AuthorID) {
			k := key{p.ConversationID, p.AuthorID}
			groups[k] = append(groups[k], p)
		}
	}
	var out []Thread
	for _, ps := range groups {
		if len(ps) < threadLeast {
			continue
		}
		sort.Slice(ps, func(i, j int) bool {
			if ps[i].ID == ps[i].ConversationID {
				return true
			}
			if ps[j].ID == ps[j].ConversationID {
				return false
			}
			return ps[i].CreatedAt.Before(ps[j].CreatedAt)
		})
		t := Thread{Root: ps[0], Count: len(ps)}
		for _, p := range ps {
			if p.CreatedAt.After(t.Last) {
				t.Last = p.CreatedAt
			}
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].Root.ID > out[j].Root.ID
	})
	return out[:min(len(out), digestMost)]
}

// active are the followed accounts with the most posts in the feed (reposts count as theirs).
func active(posts []Post, following []Account, in func(time.Time) bool) []Active {
	byID := map[string]Account{}
	for _, a := range following {
		byID[a.ID] = a
	}
	counts := map[string]int{}
	for _, p := range posts {
		if p.Ref || !in(p.CreatedAt) {
			continue
		}
		if _, ok := byID[p.AuthorID]; ok {
			counts[p.AuthorID]++
		}
	}
	out := make([]Active, 0, len(counts))
	for id, n := range counts {
		out = append(out, Active{Account: byID[id], Posts: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Posts != out[j].Posts {
			return out[i].Posts > out[j].Posts
		}
		return strings.ToLower(out[i].Username) < strings.ToLower(out[j].Username)
	})
	return out[:min(len(out), digestMost)]
}

// Digest is the digest of what is kept now.
func (c *Connector) Digest(window string) Digest {
	c.mu.Lock()
	posts := sortedPosts(c.posts)
	following := c.following
	c.mu.Unlock()
	d := BuildDigest(posts, following, c.now(), window)
	d.BudgetUsed, d.Budget = c.Budget()
	return d
}

// counts is a post's counts in a line.
func counts(p Post) string {
	return fmt.Sprintf("%d likes · %d reposts · %d replies · %d quotes", p.Likes, p.Reposts, p.Replies, p.Quotes)
}
