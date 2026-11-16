package kbsrc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFixtureSingleFileEntities: a copy of a hub's data directory keeps docs, events and meetings as one JSON file
// each; they are read as entities with a summary and a time of change, beside the directory layout, and the
// directory is never written.
func TestFixtureSingleFileEntities(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "chats", "lark-dm-1", "meta.json"), `{"id":"lark-dm-1","name":"Alex","kind":"dm","source":"lark"}`)
	write(t, filepath.Join(dir, "chats", "lark-dm-1", "summary.md"), "a chat summary")
	write(t, filepath.Join(dir, "chats", "lark-dm-1", "messages", "2026-11-05.md"), "- 10:00 Alex: hi\n- 10:01 我: hello\n")
	write(t, filepath.Join(dir, "chats", "stray.json"), `{"not":"a chat"}`)
	write(t, filepath.Join(dir, "chats", "no-meta", "messages", "2026-11-05.md"), "- 10:00 A: x\n")
	write(t, filepath.Join(dir, "docs", "doc-1.json"), `{"id":"doc-1","title":"Design notes","updated_at":"2026-11-05T18:00:00Z","summary":"what the design settles","text":"full text"}`)
	write(t, filepath.Join(dir, "docs", "doc-2.json"), `{"id":"doc-2","name":"Long doc","modified":"2026-11-06T01:00:00Z","content":"`+strings.Repeat("字", maxSummary+50)+`"}`)
	write(t, filepath.Join(dir, "events", "ev-1.json"), `{"id":"ev-1","name":"Review","start":"2026-11-05T21:00:00Z","text":"agenda"}`)
	write(t, filepath.Join(dir, "meetings", "m-1.json"), `{"id":"m-1","name":"Sync","updated":"2026-11-05T20:00:00Z"}`)
	write(t, filepath.Join(dir, "recordings", "r-1", "meta.json"), `{"id":"r-1","name":"Call","updated":"2026-11-05T19:00:00Z"}`)
	write(t, filepath.Join(dir, "recordings", "r-1", "summary.md"), "call summary")
	// The same id as a directory and a file: the directory wins.
	write(t, filepath.Join(dir, "docs", "doc-3", "meta.json"), `{"id":"doc-3","name":"From dir"}`)
	write(t, filepath.Join(dir, "docs", "doc-3.json"), `{"id":"doc-3","name":"From file"}`)
	before := tree(t, dir)

	fx, err := LoadFixture(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	docs, _ := fx.List(ctx, "docs")
	if len(docs) != 3 || docs[0].Name != "Design notes" || !docs[0].Updated.Equal(time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)) ||
		!docs[1].Updated.Equal(time.Date(2026, 11, 6, 1, 0, 0, 0, time.UTC)) || docs[2].Name != "From dir" {
		t.Fatalf("docs: %+v", docs)
	}
	d, err := fx.Get(ctx, "docs", "doc-1")
	if err != nil || d.Summary != "what the design settles" || DecodeMeta(d).ID != "doc-1" {
		t.Errorf("doc-1: %+v %v", d, err)
	}
	d, _ = fx.Get(ctx, "docs", "doc-2")
	if n := len([]rune(d.Summary)); n > maxSummary+20 || !strings.HasSuffix(d.Summary, "…[略去 50 字]") {
		t.Errorf("doc-2 summary: %d runes, ends %q", n, string([]rune(d.Summary)[n-12:]))
	}
	if d, _ := fx.Get(ctx, "events", "ev-1"); d.Summary != "agenda" {
		t.Errorf("event summary %q", d.Summary)
	}
	if d, _ := fx.Get(ctx, "chats", "lark-dm-1"); d.Summary != "a chat summary" {
		t.Errorf("chat summary %q", d.Summary)
	}
	if _, err := fx.Get(ctx, "chats", "stray"); err != ErrNotFound {
		t.Errorf("a JSON file in chats is not a chat: %v", err)
	}
	if _, err := fx.Get(ctx, "chats", "no-meta"); err != ErrNotFound {
		t.Errorf("a directory without meta.json is not a chat: %v", err)
	}
	// The day's entities, every kind, by their time of change.
	rs, err := fx.Recent(ctx, time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range rs {
		got = append(got, e.Kind+"/"+e.ID)
	}
	if strings.Join(got, " ") != "chats/lark-dm-1 events/ev-1 meetings/m-1 recordings/r-1 docs/doc-1" {
		t.Errorf("recent: %v", got)
	}
	if after := tree(t, dir); after != before {
		t.Errorf("the directory changed")
	}
}

func tree(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			b.WriteString(p + " " + info.ModTime().String() + "\n")
		}
		return nil
	})
	return b.String()
}
