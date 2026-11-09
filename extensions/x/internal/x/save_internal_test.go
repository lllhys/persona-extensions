package x

import (
	"context"
	"os"
	"testing"
	"time"
)

// A save that took its snapshot before a sign-out must not put the signed-out account's files back after the wipe,
// and a save that starts after it writes nothing.
func TestSaveDoesNotOutliveASignOut(t *testing.T) {
	dir := t.TempDir()
	c := New(Settings{}, Options{DataDir: dir})
	c.mu.Lock()
	c.keys = Keys{APIKey: "k", APISecret: "s", AccessToken: "t", AccessTokenSecret: "ts"}
	c.me = Account{ID: "1000", Username: "example_user"}
	c.posts["1"] = Post{ID: "1", Text: "a post"}
	c.mentions["2"] = Post{ID: "2", Text: "a mention"}
	gen := c.gen
	c.mu.Unlock()

	done := make(chan error, 1)
	beforeWrite = func() {
		go func() { done <- c.Logout(context.Background()) }()
		time.Sleep(100 * time.Millisecond) // the sign-out runs now, unless it waits for the writes
	}
	t.Cleanup(func() { beforeWrite = nil })
	if err := c.save(gen); err != nil {
		t.Fatal(err)
	}
	beforeWrite = nil
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left after the sign-out: %v", entries)
	}
	if err := c.save(gen); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a save after the sign-out wrote %v", entries)
	}
}

// What load read is dropped when a sign-in happened while it read: the files are the last account's, not this one's.
func TestLoadYieldsToASignIn(t *testing.T) {
	dir := t.TempDir()
	old := New(Settings{}, Options{DataDir: dir})
	old.mu.Lock()
	old.keys = Keys{APIKey: "k", APISecret: "s", AccessToken: "t", AccessTokenSecret: "ts"}
	old.mentions["2"] = Post{ID: "2", Text: "the old account's mention"}
	old.state.MentionsSince = "2"
	gen := old.gen
	old.mu.Unlock()
	if err := old.save(gen); err != nil {
		t.Fatal(err)
	}

	c := New(Settings{}, Options{DataDir: dir})
	c.disk.Lock() // load waits here after it took gen, as if it were still reading the files
	loaded := make(chan struct{})
	go func() { c.load(context.Background()); close(loaded) }()
	time.Sleep(50 * time.Millisecond)
	c.mu.Lock() // a sign-in meanwhile (what LoginStep does to the fields)
	c.keys, c.me = Keys{APIKey: "k2", APISecret: "s2", AccessToken: "t2", AccessTokenSecret: "ts2"}, Account{ID: "2000", Username: "someone"}
	c.loaded = true
	c.gen++
	c.mu.Unlock()
	c.disk.Unlock()
	<-loaded
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.mentions) != 0 || c.state.MentionsSince != "" || c.me.ID != "2000" {
		t.Fatalf("load overwrote the sign-in: %v %+v %+v", c.mentions, c.state, c.me)
	}
}
