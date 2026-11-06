package main

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// item is a story, a comment or a poll as the API answers it (only the fields this reads).
type item struct {
	ID          int64   `json:"id"`
	Type        string  `json:"type"`
	By          string  `json:"by"`
	Time        int64   `json:"time"`
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	Text        string  `json:"text"`
	Score       int     `json:"score"`
	Descendants int     `json:"descendants"`
	Kids        []int64 `json:"kids"`
	Dead        bool    `json:"dead"`
	Deleted     bool    `json:"deleted"`
}

// gone says the item is not there to show: deleted, flagged dead, or empty.
func (it item) gone() bool { return it.Deleted || it.Dead || it.ID == 0 }

// domain is the article's host without www., empty for a text post.
func (it item) domain() string {
	u, err := url.Parse(it.URL)
	if err != nil || it.URL == "" {
		return ""
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

// link is the article's address when it can be opened as a link (views take only HTTPS), else empty.
func (it item) link() string {
	if u, err := url.Parse(it.URL); err == nil && u.Scheme == "https" && u.Host != "" {
		return it.URL
	}
	return ""
}

// listDef is one of the site's lists.
type listDef struct{ id, endpoint string }

var lists = []listDef{
	{"top", "topstories"}, {"new", "newstories"}, {"best", "beststories"}, {"ask", "askstories"}, {"show", "showstories"},
}

func listEndpoint(id string) (string, bool) {
	i := slices.IndexFunc(lists, func(l listDef) bool { return l.id == id })
	if i < 0 {
		return "", false
	}
	return lists[i].endpoint, true
}

// listMost is how many of a list's stories are read: the site's front page is 30.
const listMost = 30

// listing is a list as read: its stories in order, how many could not be read, and an older copy's reason.
type listing struct {
	stories []item
	missed  int
	at      time.Time
	stale   error
}

// stories reads a list's first n stories.
func (a *app) stories(ctx context.Context, list string, n int, refresh bool) (listing, error) {
	endpoint, ok := listEndpoint(list)
	if !ok {
		return listing{}, errors.New("unknown list " + list)
	}
	rd, err := a.read(ctx, listPath(endpoint), freshness(listFor, refresh))
	if err != nil {
		return listing{}, err
	}
	var ids []int64
	if err := json.Unmarshal(rd.body, &ids); err != nil {
		return listing{}, errBadAnswer
	}
	if len(ids) > n {
		ids = ids[:n]
	}
	items, missed := a.items(ctx, ids, refresh)
	l := listing{at: rd.at, stale: rd.stale, missed: missed}
	for _, it := range items {
		if !it.gone() {
			l.stories = append(l.stories, it)
		}
	}
	if len(ids) > 0 && missed == len(ids) && ctx.Err() == nil {
		return listing{}, errors.New("no story of the list could be read")
	}
	return l, nil
}

// items reads ids at once (the site's gate keeps it to a few requests at a time), in their order; missed counts the
// ones that could not be read, deleted ones aside.
func (a *app) items(ctx context.Context, ids []int64, refresh bool) ([]item, int) {
	got := make([]*item, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if it, err := a.item(ctx, id, refresh); err == nil {
				got[i] = &it
			} else if errors.Is(err, errNotFound) {
				got[i] = &item{}
			}
		}()
	}
	wg.Wait()
	var out []item
	missed := 0
	for _, it := range got {
		if it == nil {
			missed++
		} else if it.ID != 0 {
			out = append(out, *it)
		}
	}
	return out, missed
}

func (a *app) item(ctx context.Context, id int64, refresh bool) (item, error) {
	rd, err := a.read(ctx, itemPath(id), freshness(itemFor, refresh))
	if err != nil {
		return item{}, err
	}
	var it item
	if err := json.Unmarshal(rd.body, &it); err != nil || it.ID != id {
		return item{}, errBadAnswer
	}
	return it, nil
}

// thread is a story with its first top-level comments (in the site's order, which is its ranking).
type thread struct {
	story    item
	comments []item
	missed   int
}

func (a *app) thread(ctx context.Context, id int64, comments int, refresh bool) (thread, error) {
	st, err := a.item(ctx, id, refresh)
	if err != nil {
		return thread{}, err
	}
	th := thread{story: st}
	if st.gone() {
		return th, nil
	}
	// Read a few more than asked: some are deleted or dead.
	kids := st.Kids
	if want := comments + comments/2; len(kids) > want {
		kids = kids[:want]
	}
	all, missed := a.items(ctx, kids, refresh)
	th.missed = missed
	for _, c := range all {
		if !c.gone() && len(th.comments) < comments {
			th.comments = append(th.comments, c)
		}
	}
	return th, nil
}

// freshness is how old a cached answer may be: refreshFloor when the user asked for a refresh.
func freshness(normal time.Duration, refresh bool) time.Duration {
	if refresh {
		return refreshFloor
	}
	return normal
}

var (
	paraRE   = regexp.MustCompile(`(?i)<p>`)
	preRE    = regexp.MustCompile(`(?is)<pre><code>(.*?)</code></pre>`)
	anchorRE = regexp.MustCompile(`(?is)<a\s[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	tagRE    = regexp.MustCompile(`(?s)<[^>]*>`)
	blankRE  = regexp.MustCompile(`\n{3,}`)
)

// plain turns the site's HTML (paragraphs as <p>, links, <i>, code in <pre><code>) into text: a link becomes its
// address, since the site shortens long addresses in the link's text.
func plain(s string) string {
	if s == "" {
		return ""
	}
	s = preRE.ReplaceAllStringFunc(s, func(m string) string {
		return "\n\n" + strings.TrimRight(preRE.FindStringSubmatch(m)[1], "\n") + "\n\n"
	})
	s = anchorRE.ReplaceAllString(s, "$1")
	s = paraRE.ReplaceAllString(s, "\n\n")
	s = tagRE.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(blankRE.ReplaceAllString(s, "\n\n"))
}

// filter is the user's interests: a story shows when its title or domain has one of the keywords (when there are
// any), none of the blocked words, and at least the minimum score.
type filter struct {
	Keywords, ExcludeKeywords []string
	MinScore                  int
}

func (f filter) active() bool {
	return len(nonEmpty(f.Keywords)) > 0 || len(nonEmpty(f.ExcludeKeywords)) > 0 || f.MinScore > 0
}

func (f filter) keep(it item) bool {
	hay := strings.ToLower(it.Title + " " + it.domain())
	has := func(words []string) bool {
		return slices.ContainsFunc(nonEmpty(words), func(w string) bool { return strings.Contains(hay, strings.ToLower(w)) })
	}
	if kw := nonEmpty(f.Keywords); len(kw) > 0 && !has(kw) {
		return false
	}
	return !has(f.ExcludeKeywords) && it.Score >= f.MinScore
}

func (f filter) apply(its []item) []item {
	out := []item{}
	for _, it := range its {
		if f.keep(it) {
			out = append(out, it)
		}
	}
	return out
}

func nonEmpty(xs []string) []string {
	var out []string
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// clip shortens s to n characters, with an ellipsis.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
