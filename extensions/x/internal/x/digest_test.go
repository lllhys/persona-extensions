package x

import (
	"fmt"
	"math"
	"slices"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func post(id, user, authorID string, ago time.Duration, mod func(*Post)) Post {
	p := Post{ID: id, AuthorID: authorID, Username: user, Name: "Name of " + user, Text: "text of " + id, CreatedAt: now.Add(-ago), ConversationID: id}
	if mod != nil {
		mod(&p)
	}
	return p
}

func TestScoreDampensByFollowers(t *testing.T) {
	small := Post{Likes: 10, Reposts: 1, Quotes: 1, Replies: 2, Followers: 0}
	if got, want := score(small), 16.0/math.Log2(2); got != want {
		t.Fatalf("score %v, want %v", got, want)
	}
	big := small
	big.Followers = 1 << 20
	if score(big) >= score(small) {
		t.Fatal("the same engagement scores higher for a larger account")
	}
}

func TestTopPosts(t *testing.T) {
	posts := []Post{
		post("10", "big", "1", time.Hour, func(p *Post) { p.Likes, p.Followers = 100, 1_000_000 }),        // 100/~19.9 ≈ 5
		post("11", "small", "2", 2*time.Hour, func(p *Post) { p.Likes, p.Followers = 30, 2 }),             // 30/2 = 15
		post("12", "mid", "3", 3*time.Hour, func(p *Post) { p.Likes, p.Reposts, p.Followers = 10, 5, 6 }), // 20/3 ≈ 6.7
		post("13", "tie_old", "4", 5*time.Hour, func(p *Post) { p.Likes = 2 }),
		post("14", "tie_new", "5", 4*time.Hour, func(p *Post) { p.Likes = 2 }),
		post("15", "outside", "6", 25*time.Hour, func(p *Post) { p.Likes = 1000 }),
		// Two reposts of post 10: collapsed into it, counted once each, newest time kept.
		post("20", "fan_b", "7", 10*time.Minute, func(p *Post) { p.RepostOf = "10" }),
		post("21", "fan_a", "8", 20*time.Minute, func(p *Post) { p.RepostOf = "10" }),
		post("22", "fan_a", "8", 30*time.Minute, func(p *Post) { p.RepostOf = "10" }),
		// A referenced post (not from the feed) reposted in the window still ranks; alone it does not.
		post("30", "quoted", "9", 40*time.Hour, func(p *Post) { p.Ref, p.Likes = true, 1 }),
		post("31", "fan_c", "10", time.Hour, func(p *Post) { p.RepostOf = "30" }),
		post("32", "ref_only", "11", time.Hour, func(p *Post) { p.Ref, p.Likes = true, 500 }),
	}
	d := BuildDigest(posts, nil, now, Window24h)
	var ids []string
	for _, r := range d.Top {
		ids = append(ids, r.ID)
	}
	if want := []string{"11", "12", "10", "14", "13", "30"}; !slices.Equal(ids, want) {
		t.Fatalf("top %v, want %v", ids, want)
	}
	if r := d.Top[2]; !slices.Equal(r.RepostedBy, []string{"fan_a", "fan_b"}) || !r.At.Equal(now.Add(-10*time.Minute)) {
		t.Fatalf("collapsed reposts %+v", r)
	}
	if d.Seen != 9 { // five originals and four reposts: 15 is outside, 30 and 32 are references
		t.Fatalf("seen %d", d.Seen)
	}
	if d3 := BuildDigest(posts, nil, now, Window3d); d3.Window != Window3d || d3.Top[0].ID != "15" {
		t.Fatalf("3d: %+v", d3.Top)
	}
}

func TestTopIsCapped(t *testing.T) {
	var posts []Post
	for i := range 25 {
		posts = append(posts, post(fmt.Sprint(100+i), fmt.Sprintf("u%d", i), fmt.Sprint(i), time.Duration(i)*time.Minute, func(p *Post) { p.Likes = i }))
	}
	d := BuildDigest(posts, nil, now, "")
	if len(d.Top) != digestMost || d.Top[0].ID != "124" || d.Window != Window24h {
		t.Fatalf("top %d, first %s", len(d.Top), d.Top[0].ID)
	}
}

func TestSharedLinks(t *testing.T) {
	link := func(urls ...string) func(*Post) {
		return func(p *Post) {
			for _, u := range urls {
				p.Links = append(p.Links, Link{Expanded: u, Display: u})
			}
		}
	}
	posts := []Post{
		post("1", "alice", "1", 5*time.Hour, link("https://www.example.com/article/?utm_source=x")),
		post("2", "bob", "2", 4*time.Hour, link("http://example.com/article#part", "https://x.com/someone/status/123")),
		post("3", "carol", "3", 3*time.Hour, link("https://example.com/article")),
		post("4", "alice", "1", 2*time.Hour, link("https://example.com/article", "https://example.org/other")),
		post("5", "dave", "4", time.Hour, link("https://example.org/other?id=2")), // a different query: another link
		post("6", "erin", "5", 30*time.Minute, link("https://twitter.com/someone/status/9", "ftp://example.net/file")),
		post("7", "frank", "6", 20*time.Minute, func(p *Post) { p.RepostOf = "4" }), // a repost shares its links
	}
	d := BuildDigest(posts, nil, now, Window24h)
	if len(d.Links) != 3 {
		t.Fatalf("links %+v", d.Links)
	}
	first := d.Links[0]
	if !slices.Equal(first.Authors, []string{"alice", "bob", "carol", "frank"}) || first.Posts != 4 || first.URL != "https://www.example.com/article/?utm_source=x" ||
		first.Example.ID != "4" {
		t.Fatalf("first link %+v", first)
	}
	if d.Links[1].URL != "https://example.org/other" || !slices.Equal(d.Links[1].Authors, []string{"alice", "frank"}) {
		t.Fatalf("second link %+v", d.Links[1])
	}
	if d.Links[2].URL != "https://example.org/other?id=2" {
		t.Fatalf("third link %+v", d.Links[2])
	}
}

func TestNormalizeURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.Example.com/a/?utm_medium=x&b=2&a=1#frag": "example.com/a?a=1&b=2",
		"http://example.com/a":                                 "example.com/a",
		"https://mobile.twitter.com/someone":                   "twitter.com/someone",
		"https://x.com/someone/status/1":                       "",
		"mailto:me@example.com":                                "",
	} {
		got, ok := normalizeURL(in)
		if want == "" && ok || want != "" && got != want {
			t.Errorf("%s: %q %v, want %q", in, got, ok, want)
		}
	}
}

func TestThreads(t *testing.T) {
	self := func(conv, prev string) func(*Post) {
		return func(p *Post) { p.ConversationID, p.ReplyTo, p.InReplyToUser = conv, prev, p.AuthorID }
	}
	posts := []Post{
		post("100", "writer", "1", 5*time.Hour, nil),
		post("101", "writer", "1", 4*time.Hour, self("100", "100")),
		post("102", "writer", "1", 3*time.Hour, self("100", "101")),
		post("103", "writer", "1", 2*time.Hour, self("100", "102")),
		// Another author's reply in the same conversation does not count.
		post("104", "other", "2", time.Hour, func(p *Post) { p.ConversationID, p.ReplyTo, p.InReplyToUser = "100", "103", "1" }),
		// Two posts are not a thread.
		post("200", "short", "3", 5*time.Hour, nil),
		post("201", "short", "3", 4*time.Hour, self("200", "200")),
		// A newer thread of three, its first post not read: still a thread, the oldest read first.
		post("301", "late", "4", 50*time.Minute, self("300", "300")),
		post("302", "late", "4", 40*time.Minute, self("300", "301")),
		post("303", "late", "4", 30*time.Minute, self("300", "302")),
	}
	d := BuildDigest(posts, nil, now, Window24h)
	if len(d.Threads) != 2 {
		t.Fatalf("threads %+v", d.Threads)
	}
	if d.Threads[0].Root.ID != "301" || d.Threads[0].Count != 3 || d.Threads[1].Root.ID != "100" || d.Threads[1].Count != 4 ||
		!d.Threads[1].Last.Equal(now.Add(-2*time.Hour)) {
		t.Fatalf("threads %+v", d.Threads)
	}
}

func TestActiveAccounts(t *testing.T) {
	following := []Account{{ID: "1", Username: "zed"}, {ID: "2", Username: "amy"}, {ID: "3", Username: "bo"}, {ID: "4", Username: "quiet"}}
	posts := []Post{
		post("1", "zed", "1", time.Hour, nil), post("2", "zed", "1", 2*time.Hour, nil),
		post("3", "amy", "2", time.Hour, nil), post("4", "amy", "2", 2*time.Hour, nil),
		post("5", "bo", "3", time.Hour, nil), post("6", "bo", "3", 2*time.Hour, nil), post("7", "bo", "3", 3*time.Hour, func(p *Post) { p.RepostOf = "1" }),
		post("8", "stranger", "9", time.Hour, nil), post("9", "stranger", "9", time.Hour, nil), post("10", "stranger", "9", time.Hour, nil),
		post("11", "amy", "2", 30*time.Hour, nil),
		post("12", "amy", "2", time.Hour, func(p *Post) { p.Ref = true }),
	}
	d := BuildDigest(posts, following, now, Window24h)
	var got []string
	for _, a := range d.Active {
		got = append(got, fmt.Sprintf("%s:%d", a.Username, a.Posts))
	}
	if want := []string{"bo:3", "amy:2", "zed:2"}; !slices.Equal(got, want) {
		t.Fatalf("active %v, want %v", got, want)
	}
}
