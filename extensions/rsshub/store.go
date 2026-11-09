package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// The subscriptions and what was read from them, kept in the extension's data directory (feeds.json): each feed's
// source, title and tag, its ETag / Last-Modified for the next conditional request, how the last refresh went, and
// its entries with their read state.

const (
	stateFile     = "feeds.json"
	feedsMost     = 300
	entriesMost   = 100 // entries kept per feed: the newest, read or not
	tagMost       = 40
	titleMost     = 120
	defaultTag    = "未分类"
	entryIDLength = 8
)

// feed is one subscription.
type feed struct {
	ID           string    `json:"id"`
	Source       string    `json:"source"` // a route ("/github/issue/a/b") or a feed's URL
	Title        string    `json:"title,omitempty"`
	Tag          string    `json:"tag,omitempty"`
	FeedTitle    string    `json:"feed_title,omitempty"`
	SiteLink     string    `json:"site_link,omitempty"`
	Format       string    `json:"format,omitempty"`
	Added        time.Time `json:"added"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	LastFetch    time.Time `json:"last_fetch,omitzero"`
	LastOK       time.Time `json:"last_ok,omitzero"`
	LastError    string    `json:"last_error,omitempty"`
	Entries      []entry   `json:"entries,omitempty"`
}

// name is what the feed is called: the user's title, the feed's own, or its source.
func (f *feed) name() string {
	return firstNonEmpty(f.Title, f.FeedTitle, f.Source)
}

func (f *feed) tag() string {
	return firstNonEmpty(f.Tag, defaultTag)
}

func (f *feed) unread() int {
	n := 0
	for _, e := range f.Entries {
		if !e.Read {
			n++
		}
	}
	return n
}

// entry is one item read from a feed.
type entry struct {
	ID        string    `json:"id"`  // <feed id>-<hash>: what tools and the page name it by
	Key       string    `json:"key"` // the feed's guid / id / link
	Title     string    `json:"title"`
	Link      string    `json:"link,omitempty"`
	Author    string    `json:"author,omitempty"`
	Published time.Time `json:"published,omitzero"`
	Seen      time.Time `json:"seen"`
	Summary   string    `json:"summary,omitempty"`
	Content   string    `json:"content,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	Read      bool      `json:"read,omitempty"`
}

// when is the entry's time for ordering: published, or first seen.
func (e entry) when() time.Time {
	if !e.Published.IsZero() {
		return e.Published
	}
	return e.Seen
}

type diskState struct {
	Version int     `json:"version"`
	Feeds   []*feed `json:"feeds"`
}

// store holds the feeds, in the order they were added.
type store struct {
	mu    sync.Mutex
	path  string
	logf  func(string, ...any)
	feeds []*feed
}

func openStore(dir string, logf func(string, ...any)) *store {
	s := &store{logf: logf}
	if dir == "" {
		return s
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		logf("rsshub: state: %v", err)
		return s
	}
	s.path = filepath.Join(dir, stateFile)
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s
	}
	if err == nil {
		var d diskState
		if err = json.Unmarshal(raw, &d); err == nil {
			for _, f := range d.Feeds {
				if f != nil && f.ID != "" && f.Source != "" {
					s.feeds = append(s.feeds, f)
				}
			}
			return s
		}
	}
	if rerr := os.Rename(s.path, s.path+".corrupt"); rerr != nil {
		logf("rsshub: state %s unreadable (%v), and it could not be moved aside: %v", s.path, err, rerr)
	} else {
		logf("rsshub: state %s unreadable (%v), moved aside", s.path, err)
	}
	return s
}

// flush writes the state; the caller holds mu.
func (s *store) flush() {
	if s.path == "" {
		return
	}
	raw, err := json.Marshal(diskState{Version: 1, Feeds: s.feeds})
	if err != nil {
		s.logf("rsshub: state: %v", err)
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		s.logf("rsshub: state: %v", err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		s.logf("rsshub: state: %v", err)
	}
}

// copyFeed is a deep enough copy of f for reading outside the lock.
func copyFeed(f *feed) feed {
	c := *f
	c.Entries = slices.Clone(f.Entries)
	return c
}

// list is a copy of every feed.
func (s *store) list() []feed {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]feed, 0, len(s.feeds))
	for _, f := range s.feeds {
		out = append(out, copyFeed(f))
	}
	return out
}

// get is a copy of the feed with id.
func (s *store) get(id string) (feed, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.feeds {
		if f.ID == id {
			return copyFeed(f), true
		}
	}
	return feed{}, false
}

// bySource is the feed subscribed to src, if any.
func (s *store) bySource(src string) (feed, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.feeds {
		if f.Source == src {
			return copyFeed(f), true
		}
	}
	return feed{}, false
}

// feedID is a short stable id for a source.
func feedID(src string) string { return "f" + hashText(src)[:entryIDLength] }

func cleanTag(tag string) string   { return clip(oneLine(tag), tagMost) }
func cleanTitle(t string) string   { return clip(oneLine(t), titleMost) }
func entryKeyID(key string) string { return hashText(key)[:entryIDLength] }

// add subscribes to src, unless it is already: then the existing feed is returned with added false.
func (s *store) add(src, title, tag string, now time.Time) (feed, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.feeds {
		if f.Source == src {
			return copyFeed(f), false, nil
		}
	}
	if len(s.feeds) >= feedsMost {
		return feed{}, false, fmt.Errorf("最多订阅 %d 个，先取消一些", feedsMost)
	}
	f := &feed{ID: feedID(src), Source: src, Title: cleanTitle(title), Tag: cleanTag(tag), Added: now.UTC()}
	s.feeds = append(s.feeds, f)
	s.flush()
	return copyFeed(f), true, nil
}

// remove unsubscribes from the feed with id.
func (s *store) remove(id string) (feed, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, f := range s.feeds {
		if f.ID == id {
			s.feeds = slices.Delete(s.feeds, i, i+1)
			s.flush()
			return copyFeed(f), true
		}
	}
	return feed{}, false
}

// update applies a refresh's outcome to the feed with id: new entries are added unread, entries already there keep
// their read state and take the feed's newer text, and the oldest beyond entriesMost go. It answers how many entries
// were new; a feed unsubscribed meanwhile is left alone.
func (s *store) update(id string, res *fetchResult, parsed *parsedFeed, ferr error, now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var f *feed
	for _, x := range s.feeds {
		if x.ID == id {
			f = x
		}
	}
	if f == nil {
		return 0
	}
	defer s.flush()
	f.LastFetch = now.UTC()
	if ferr != nil {
		f.LastError = clip(oneLine(ferr.Error()), 300)
		return 0
	}
	f.LastOK, f.LastError = now.UTC(), ""
	if res != nil {
		f.ETag, f.LastModified = res.ETag, res.LastModified
	}
	if parsed == nil {
		return 0
	}
	if parsed.Title != "" {
		f.FeedTitle = parsed.Title
	}
	f.SiteLink, f.Format = parsed.Link, parsed.Format
	return mergeEntries(f, parsed.Entries, now)
}

// mergeEntries adds parsed to f.Entries without duplicates and keeps the newest entriesMost.
func mergeEntries(f *feed, parsed []parsedEntry, now time.Time) int {
	index := make(map[string]int, len(f.Entries))
	for i, e := range f.Entries {
		index[e.Key] = i
	}
	added := 0
	for i, p := range parsed {
		if j, ok := index[p.Key]; ok {
			e := &f.Entries[j]
			e.Title, e.Link, e.Author, e.Summary, e.Content, e.Tags = p.Title, p.Link, p.Author, p.Summary, p.Content, p.Tags
			if !p.Published.IsZero() {
				e.Published = p.Published
			}
			continue
		}
		// entries without dates keep the feed's order: the first is the newest
		seen := now.UTC().Add(-time.Duration(i) * time.Millisecond)
		e := entry{ID: f.ID + "-" + entryKeyID(p.Key), Key: p.Key, Title: p.Title, Link: p.Link, Author: p.Author,
			Published: p.Published, Seen: seen, Summary: p.Summary, Content: p.Content, Tags: p.Tags}
		index[p.Key] = len(f.Entries)
		f.Entries = append(f.Entries, e)
		added++
	}
	sort.SliceStable(f.Entries, func(i, j int) bool { return f.Entries[i].when().After(f.Entries[j].when()) })
	if len(f.Entries) > entriesMost {
		f.Entries = f.Entries[:entriesMost]
	}
	return added
}

// markRead sets the read state of the entries ids name (all of a feed's when feedID is given and ids is empty, every
// entry when both are empty and all is set). It answers how many changed.
func (s *store) markRead(feedID string, ids []string, all, read bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[strings.TrimSpace(id)] = true
	}
	if feedID == "" && len(ids) == 0 && !all {
		return 0, errors.New("要给 feed_id、entry_ids，或者 all: true")
	}
	if feedID != "" && !slices.ContainsFunc(s.feeds, func(f *feed) bool { return f.ID == feedID }) {
		return 0, fmt.Errorf("没有订阅 %s", feedID)
	}
	changed, found := 0, 0
	for _, f := range s.feeds {
		if feedID != "" && f.ID != feedID {
			continue
		}
		for i := range f.Entries {
			e := &f.Entries[i]
			if len(want) > 0 && !want[e.ID] {
				continue
			}
			found++
			if e.Read != read {
				e.Read = read
				changed++
			}
		}
	}
	if len(want) > 0 && found == 0 {
		return 0, errors.New("没有找到这些条目（条目 id 形如 f1a2b3c4d-5e6f7a8b，从 rsshub_entries 拿）")
	}
	if changed > 0 {
		s.flush()
	}
	return changed, nil
}

// entryByID finds one entry and its feed.
func (s *store) entryByID(id string) (feed, entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fid, _, _ := strings.Cut(id, "-")
	for _, f := range s.feeds {
		if f.ID != fid {
			continue
		}
		for _, e := range f.Entries {
			if e.ID == id {
				c := *f
				c.Entries = nil
				return c, e, true
			}
		}
	}
	return feed{}, entry{}, false
}

// unread is the number of unread entries of every feed.
func (s *store) unread() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, f := range s.feeds {
		n += f.unread()
	}
	return n
}

// feedEntry is an entry with the feed it came from.
type feedEntry struct {
	entry
	FeedID   string
	FeedName string
}

// entryFilter picks entries across feeds.
type entryFilter struct {
	FeedID     string
	Tag        string
	UnreadOnly bool
	Query      string
	Limit      int
}

// entries lists entries newest first.
func (s *store) entries(fl entryFilter) (out []feedEntry, total int) {
	q := strings.ToLower(strings.TrimSpace(fl.Query))
	s.mu.Lock()
	for _, f := range s.feeds {
		if fl.FeedID != "" && f.ID != fl.FeedID {
			continue
		}
		if fl.Tag != "" && f.tag() != fl.Tag {
			continue
		}
		for _, e := range f.Entries {
			if fl.UnreadOnly && e.Read {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(e.Title+"\n"+e.Summary+"\n"+e.Content+"\n"+e.Author+"\n"+strings.Join(e.Tags, " ")), q) {
				continue
			}
			out = append(out, feedEntry{entry: e, FeedID: f.ID, FeedName: f.name()})
		}
	}
	s.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].when().After(out[j].when()) })
	total = len(out)
	if fl.Limit > 0 && len(out) > fl.Limit {
		out = out[:fl.Limit]
	}
	return out, total
}

// tags lists the feeds' tags in the order they first appear, the default last.
func tagsOf(feeds []feed) []string {
	var out []string
	hasDefault := false
	for _, f := range feeds {
		t := f.tag()
		if t == defaultTag {
			hasDefault = true
			continue
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	if hasDefault {
		out = append(out, defaultTag)
	}
	return out
}
