package x

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// What the connector keeps, in the extension's own data directory (initialize's extension.data_dir), not in the
// hub's knowledge base: X requires deleting copies of posts that were removed, and the knowledge base has no way to
// delete one, so posts stay here for retention_days at most (mentions 8 days) and go when the user signs out.
//
// Every file is written whole to a temporary file and renamed over the old one, so a crash leaves the old or the new
// one, never half of either. Without a data directory (a test, a hub that sent none) it is kept in memory only.

// Files in the data directory.
const (
	fileFollowing = "following.json"
	filePosts     = "posts.json"
	fileMentions  = "mentions.json"
	fileState     = "state.json"
	// fileGrok is what the Grok source found and its day's runs (grokdigest.go): not the X sign-in's, so signing out
	// of X keeps it; it goes when the source is set to x_api, or with the extension's data.
	fileGrok = "grok_digest.json"
)

// Post is a post as kept: its text whole and unaltered, its author as X said when it was read, its counts.
type Post struct {
	ID             string    `json:"id"`
	AuthorID       string    `json:"author_id"`
	Username       string    `json:"username,omitempty"`
	Name           string    `json:"name,omitempty"`
	Followers      int       `json:"followers,omitempty"`
	Verified       bool      `json:"verified,omitempty"`
	Text           string    `json:"text"`
	CreatedAt      time.Time `json:"created_at"`
	ConversationID string    `json:"conversation_id,omitempty"`
	Lang           string    `json:"lang,omitempty"`
	InReplyToUser  string    `json:"in_reply_to_user_id,omitempty"`
	Likes          int       `json:"likes,omitempty"`
	Reposts        int       `json:"reposts,omitempty"`
	Replies        int       `json:"replies,omitempty"`
	Quotes         int       `json:"quotes,omitempty"`
	Links          []Link    `json:"links,omitempty"`
	// RepostOf, QuoteOf, ReplyTo: the post this one reposts, quotes, answers.
	RepostOf string `json:"repost_of,omitempty"`
	QuoteOf  string `json:"quote_of,omitempty"`
	ReplyTo  string `json:"reply_to,omitempty"`
	// Ref: read only as a post another one references (includes.tweets), not from the feed itself.
	Ref bool `json:"ref,omitempty"`
	// Source is where it came from: "home" or "list".
	Source string    `json:"source,omitempty"`
	Seen   time.Time `json:"seen"`
}

// Link is a link in a post: Expanded is where it goes, Display how X shows it.
type Link struct {
	Expanded string `json:"expanded"`
	Display  string `json:"display,omitempty"`
}

// Permalink is the post on x.com.
func (p Post) Permalink() string { return permalink(p.Username, p.ID) }

func permalink(username, id string) string {
	if username == "" {
		username = "i/web"
	}
	return "https://x.com/" + username + "/status/" + id
}

// Account is a followed account.
type Account struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Username    string `json:"username"`
	Description string `json:"description,omitempty"`
	Followers   int    `json:"followers,omitempty"`
	Following   int    `json:"following,omitempty"`
	Posts       int    `json:"posts,omitempty"`
	Verified    bool   `json:"verified,omitempty"`
	Protected   bool   `json:"protected,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
}

// syncState is where each read stands, and the day's budget.
type syncState struct {
	// Since are the newest ids read (the next read's since_id): home timeline, mentions, the user's own posts.
	HomeSince     string `json:"home_since,omitempty"`
	MentionsSince string `json:"mentions_since,omitempty"`
	OwnSince      string `json:"own_since,omitempty"`
	// GapTop / GapUntil: a home or mentions read that stopped halfway, still to finish (cursorRef, sync.go).
	HomeGapTop       string `json:"home_gap_top,omitempty"`
	HomeGapUntil     string `json:"home_gap_until,omitempty"`
	MentionsGapTop   string `json:"mentions_gap_top,omitempty"`
	MentionsGapUntil string `json:"mentions_gap_until,omitempty"`
	// BudgetDay is the UTC day BudgetUsed counts (2006-01-02).
	BudgetDay  string `json:"budget_day,omitempty"`
	BudgetUsed int    `json:"budget_used,omitempty"`
	// When each read last finished.
	FollowingAt time.Time `json:"following_at,omitzero"`
	FeedAt      time.Time `json:"feed_at,omitzero"`
	MentionsAt  time.Time `json:"mentions_at,omitzero"`
	// Replied is, per conversation, when the user last posted in it themselves (an answer to a mention).
	Replied map[string]time.Time `json:"replied,omitempty"`
}

// store reads and writes the files.
type store struct{ dir string }

// read decodes name into v; a missing file leaves v as it is.
func (s store) read(name string, v any) error {
	if s.dir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(s.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// write writes v as name atomically: a temporary file in the same directory, renamed over it.
func (s store) write(name string, v any) error {
	if s.dir == "" {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, "."+name+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o600)
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(s.dir, name))
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// remove removes name.
func (s store) remove(name string) error {
	if s.dir == "" {
		return nil
	}
	err := os.Remove(filepath.Join(s.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// wipe removes everything in the directory but keep (signing out: the files above and anything left from an older
// version, Grok's file kept).
func (s store) wipe(keep ...string) error {
	if s.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if slices.Contains(keep, e.Name()) {
			continue
		}
		errs = append(errs, os.RemoveAll(filepath.Join(s.dir, e.Name())))
	}
	return errors.Join(errs...)
}
