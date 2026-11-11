package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// defaultLocator is the production locator, kept so that one test can check it without touching the real
// configuration directory (Locator is replaced in TestMain).
var defaultLocator = Locator

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "spaces-store-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	Locator = func() string { return filepath.Join(root, "persona-spaces", "data-dir") }
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

// valid returns a state with one space, in the canonical form the file holds.
func valid(name string) *kernel.Base {
	return &kernel.Base{Format: 1, Spaces: []*kernel.Space{{
		ID: "home", Name: name, Types: []*kernel.Type{}, Entities: []*kernel.Entity{},
	}}}
}

// newStore returns an in-memory store, or a store in a fresh data directory that does not exist yet.
func newStore(t *testing.T, mem bool) (*Store, string) {
	t.Helper()
	if mem {
		return Open(""), ""
	}
	dir := filepath.Join(t.TempDir(), "data")
	return Open(dir), dir
}

// setFile replaces the data file with raw content.
func setFile(t *testing.T, dir string, raw string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fileBytes returns the data file content, or nil when it is absent.
func fileBytes(t *testing.T, dir string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, fileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// spaceName returns the name of the first space as the store reports it.
func spaceName(t *testing.T, s *Store) string {
	t.Helper()
	var name string
	if err := s.View(func(b *kernel.Base) error {
		if len(b.Spaces) != 1 {
			return fmt.Errorf("want one space, got %d", len(b.Spaces))
		}
		name = b.Spaces[0].Name
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name string
		mem  bool
	}{{"memory", true}, {"file", false}} {
		t.Run(tc.name, func(t *testing.T) {
			s, dir := newStore(t, tc.mem)
			if err := s.Update(func(b *kernel.Base, now time.Time) error {
				if now.IsZero() || now.Location() != time.UTC {
					return fmt.Errorf("now = %v, want a UTC clock reading", now)
				}
				b.Spaces = append(b.Spaces, valid("家").Spaces...)
				b.Seq = 1
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if got := spaceName(t, s); got != "家" {
				t.Fatalf("name = %q, want 家", got)
			}
			if s.Changed() {
				t.Fatal("Changed after own update = true, want false")
			}
			if dir != "" {
				raw := fileBytes(t, dir)
				if !strings.HasSuffix(string(raw), "}\n") {
					t.Fatalf("file does not end with one newline after the object: %q", raw)
				}
				if !strings.Contains(string(raw), `"format": 1`) {
					t.Fatalf("file is not indented JSON with format 1: %s", raw)
				}
			}
		})
	}
}

func TestViewOfEmptyStore(t *testing.T) {
	for _, mem := range []bool{true, false} {
		t.Run(fmt.Sprint("mem=", mem), func(t *testing.T) {
			s, dir := newStore(t, mem)
			if err := s.View(func(b *kernel.Base) error {
				if b.Format != 1 || b.Seq != 0 || b.Spaces == nil || len(b.Spaces) != 0 {
					return fmt.Errorf("empty state = %+v", b)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if dir != "" {
				if raw := fileBytes(t, dir); raw != nil {
					t.Fatalf("View created a file: %s", raw)
				}
			}
			if s.Changed() {
				t.Fatal("Changed on an absent file = true, want false")
			}
		})
	}
}

func TestUpdateFailureSavesNothing(t *testing.T) {
	cases := []struct {
		name string
		f    func(b *kernel.Base, now time.Time) error
	}{
		{"callback error", func(b *kernel.Base, _ time.Time) error {
			b.Seq = 99
			return errors.New("refused")
		}},
		{"format broken", func(b *kernel.Base, _ time.Time) error {
			b.Format = 2
			return nil
		}},
		{"invariant broken", func(b *kernel.Base, _ time.Time) error {
			b.Spaces[0].ID = "Bad ID"
			return nil
		}},
	}
	for _, mem := range []bool{true, false} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("mem=%v/%s", mem, tc.name), func(t *testing.T) {
				s, dir := newStore(t, mem)
				if err := s.Update(func(b *kernel.Base, _ time.Time) error {
					b.Spaces = valid("家").Spaces
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				before := fileBytes(t, dir)
				if err := s.Update(tc.f); err == nil {
					t.Fatal("Update succeeded, want an error")
				}
				if got := spaceName(t, s); got != "家" {
					t.Fatalf("state changed to %q after a failed update", got)
				}
				if dir != "" {
					if after := fileBytes(t, dir); string(after) != string(before) {
						t.Fatalf("file changed after a failed update:\n%s\n---\n%s", before, after)
					}
				}
			})
		}
	}
}

// corrupt files are refused: View and Update fail with the path, and the bytes are never overwritten.
func TestRefusedFiles(t *testing.T) {
	cases := map[string]string{
		"empty":           "",
		"not json":        "not json",
		"array":           "[]",
		"null":            "null",
		"format 2":        `{"format":2,"seq":0,"spaces":[]}`,
		"no format":       `{"seq":0,"spaces":[]}`,
		"unknown field":   `{"format":1,"seq":0,"spaces":[],"extra":true}`,
		"two objects":     `{"format":1,"seq":0,"spaces":[]} {}`,
		"trailing text":   `{"format":1,"seq":0,"spaces":[]}garbage`,
		"bad space id":    `{"format":1,"seq":0,"spaces":[{"id":"Bad ID","name":"x","types":[],"entities":[]}]}`,
		"bad space name":  `{"format":1,"seq":0,"spaces":[{"id":"home","name":"","types":[],"entities":[]}]}`,
		"truncated file":  `{"format":1,"seq":0,"spaces":[{"id":"ho`,
		"wrong field typ": `{"format":1,"seq":"x","spaces":[]}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			s, dir := newStore(t, false)
			setFile(t, dir, raw)
			err := s.View(func(*kernel.Base) error { return nil })
			if err == nil || !strings.Contains(err.Error(), fileName) {
				t.Fatalf("View error = %v, want a refusal naming %s", err, fileName)
			}
			err = s.Update(func(*kernel.Base, time.Time) error { return nil })
			if err == nil || !strings.Contains(err.Error(), fileName) {
				t.Fatalf("Update error = %v, want a refusal naming %s", err, fileName)
			}
			if got := string(fileBytes(t, dir)); got != raw {
				t.Fatalf("refused file was overwritten: %q", got)
			}
		})
	}
}

func TestReadErrors(t *testing.T) {
	t.Run("file is a directory", func(t *testing.T) {
		s, dir := newStore(t, false)
		if err := os.MkdirAll(filepath.Join(dir, fileName), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := s.View(func(*kernel.Base) error { return nil }); err == nil {
			t.Fatal("View of a directory succeeded")
		}
		if err := s.Update(func(*kernel.Base, time.Time) error { return nil }); err == nil {
			t.Fatal("Update over a directory succeeded")
		}
	})
	t.Run("data directory is a file", func(t *testing.T) {
		parent := t.TempDir()
		blocker := filepath.Join(parent, "file")
		if err := os.WriteFile(blocker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		s := Open(filepath.Join(blocker, "data"))
		if err := s.View(func(*kernel.Base) error { return nil }); err == nil {
			t.Fatal("View below a regular file succeeded")
		}
		if err := s.Update(func(*kernel.Base, time.Time) error { return nil }); err == nil {
			t.Fatal("Update below a regular file succeeded")
		}
		if s.Changed() {
			t.Fatal("Changed below a regular file = true, want false")
		}
	})
	t.Run("lock file cannot be opened", func(t *testing.T) {
		s, dir := newStore(t, false)
		if err := os.MkdirAll(filepath.Join(dir, lockName), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := s.Update(func(*kernel.Base, time.Time) error { return nil }); err == nil {
			t.Fatal("Update with a directory as lock file succeeded")
		}
		if fileBytes(t, dir) != nil {
			t.Fatal("data file written without the lock")
		}
	})
	t.Run("directory cannot be written", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores directory permissions")
		}
		s, dir := newStore(t, false)
		if err := s.Update(func(b *kernel.Base, _ time.Time) error {
			b.Spaces = valid("家").Spaces
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		if err := s.Update(func(b *kernel.Base, _ time.Time) error {
			b.Seq++
			return nil
		}); err == nil {
			t.Fatal("Update in a read-only directory succeeded")
		}
		if got := spaceName(t, s); got != "家" {
			t.Fatalf("state after failed write = %q", got)
		}
	})
}

func TestModes(t *testing.T) {
	s, dir := newStore(t, false)
	if err := s.Update(func(b *kernel.Base, _ time.Time) error {
		b.Spaces = valid("家").Spaces
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("data directory mode = %o, want 700", got)
	}
	fi, err := os.Stat(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("data file mode = %o, want 600", got)
	}
	if _, err := os.Stat(filepath.Join(dir, lockName)); err != nil {
		t.Errorf("lock file missing: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestCacheReuseAndReload(t *testing.T) {
	s, dir := newStore(t, false)
	if err := s.Update(func(b *kernel.Base, _ time.Time) error {
		b.Spaces = valid("家").Spaces
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var first, second *kernel.Base
	if err := s.View(func(b *kernel.Base) error { first = b; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.View(func(b *kernel.Base) error { second = b; return nil }); err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("unchanged file was decoded again")
	}

	other := Open(dir)
	if err := other.Update(func(b *kernel.Base, _ time.Time) error {
		b.Spaces[0].Name = "书房"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !s.Changed() {
		t.Fatal("Changed after another store's update = false, want true")
	}
	var third *kernel.Base
	if err := s.View(func(b *kernel.Base) error { third = b; return nil }); err != nil {
		t.Fatal(err)
	}
	if third == first || third.Spaces[0].Name != "书房" {
		t.Fatalf("View did not reload the changed file: %+v", third.Spaces[0])
	}
	if s.Changed() {
		t.Fatal("Changed after reloading = true, want false")
	}
}

func TestChanged(t *testing.T) {
	_, dir := newStore(t, false)
	watcher := Open(dir)
	writer := Open(dir)
	if watcher.Changed() {
		t.Fatal("Changed before anything exists = true")
	}
	if err := writer.Update(func(b *kernel.Base, _ time.Time) error {
		b.Seq = 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !watcher.Changed() {
		t.Fatal("Changed after a file was created by another store = false")
	}
	if err := watcher.View(func(*kernel.Base) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if watcher.Changed() {
		t.Fatal("Changed after View = true")
	}
	if err := writer.Update(func(b *kernel.Base, _ time.Time) error {
		b.Seq = 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !watcher.Changed() {
		t.Fatal("Changed after a second external update = false")
	}
	if err := os.Remove(filepath.Join(dir, fileName)); err != nil {
		t.Fatal(err)
	}
	if !watcher.Changed() {
		t.Fatal("Changed after the file was removed = false")
	}
}

// each foreign change is reported once, even when the new file cannot be decoded: a watcher that keeps reporting it
// would invalidate the hub every tick.
func TestChangedReportsEachForeignChangeOnce(t *testing.T) {
	_, dir := newStore(t, false)
	watcher := Open(dir)
	writer := Open(dir)
	if err := writer.Update(func(b *kernel.Base, _ time.Time) error {
		b.Seq = 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !watcher.Changed() {
		t.Fatal("first Changed after a foreign create = false")
	}
	if watcher.Changed() {
		t.Fatal("second Changed without a new change = true, want false")
	}
	setFile(t, dir, "not json")
	if !watcher.Changed() {
		t.Fatal("Changed after a foreign write of a broken file = false")
	}
	if err := watcher.View(func(*kernel.Base) error { return nil }); err == nil {
		t.Fatal("View of a broken file succeeded")
	}
	for i := 1; i <= 3; i++ {
		if watcher.Changed() {
			t.Fatalf("Changed call %d after one broken write = true, want false", i)
		}
	}
}

// a failed Update must not absorb a foreign change that has not been loaded yet.
func TestFailedUpdateKeepsForeignChangePending(t *testing.T) {
	_, dir := newStore(t, false)
	watcher := Open(dir)
	writer := Open(dir)
	if err := writer.Update(func(b *kernel.Base, _ time.Time) error {
		b.Spaces = valid("家").Spaces
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	refused := errors.New("refused")
	if err := watcher.Update(func(*kernel.Base, time.Time) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("Update error = %v, want the refusal", err)
	}
	if !watcher.Changed() {
		t.Fatal("foreign change absorbed by a failed Update: Changed = false, want true")
	}
	if err := watcher.Update(func(*kernel.Base, time.Time) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if watcher.Changed() {
		t.Fatal("Changed after a successful Update = true, want false")
	}
}

// a save syncs the data directory after the rename, so that the new directory entry survives a power loss.
func TestWriteSyncsDirectoryAfterRename(t *testing.T) {
	s, dir := newStore(t, false)
	var calls []string
	renamed := false
	saved := syncDir
	syncDir = func(d string) {
		calls = append(calls, d)
		renamed = strings.Contains(string(fileBytes(t, dir)), "家")
	}
	t.Cleanup(func() { syncDir = saved })
	if err := s.Update(func(b *kernel.Base, _ time.Time) error {
		b.Spaces = valid("家").Spaces
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != dir {
		t.Fatalf("directory syncs = %v, want exactly [%s]", calls, dir)
	}
	if !renamed {
		t.Fatal("directory synced before the new file was renamed into place")
	}
}

func TestConcurrentUpdatesLoseNothing(t *testing.T) {
	_, dir := newStore(t, false)
	stores := []*Store{Open(dir), Open(dir)}
	const workers, each = 8, 5
	var wg sync.WaitGroup
	errs := make(chan error, workers*each)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				errs <- s.Update(func(b *kernel.Base, _ time.Time) error {
					b.Seq++
					return nil
				})
			}
		}(stores[w%len(stores)])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var seq uint64
	if err := stores[0].View(func(b *kernel.Base) error { seq = b.Seq; return nil }); err != nil {
		t.Fatal(err)
	}
	if want := uint64(workers * each); seq != want {
		t.Fatalf("seq = %d, want %d: updates were lost", seq, want)
	}
}

func TestMemoryStoreIsNotChanged(t *testing.T) {
	s := Open("")
	if err := s.Update(func(b *kernel.Base, _ time.Time) error {
		b.Seq = 3
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if s.Changed() {
		t.Fatal("in-memory store reports a change")
	}
	var seq uint64
	if err := s.View(func(b *kernel.Base) error { seq = b.Seq; return nil }); err != nil {
		t.Fatal(err)
	}
	if seq != 3 {
		t.Fatalf("seq = %d, want 3", seq)
	}
}

func TestWriteOverExistingFileReplacesIt(t *testing.T) {
	s, dir := newStore(t, false)
	setFile(t, dir, "")
	setFile(t, dir, `{"format":1,"seq":0,"spaces":[]}`)
	if err := s.Update(func(b *kernel.Base, _ time.Time) error {
		b.Spaces = valid("家").Spaces
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := spaceName(t, s); got != "家" {
		t.Fatalf("name = %q", got)
	}
}

// withLocator replaces Locator for one test and restores it afterwards.
func withLocator(t *testing.T, f func() string) {
	t.Helper()
	saved := Locator
	Locator = f
	t.Cleanup(func() { Locator = saved })
}

func TestDataDir(t *testing.T) {
	existing := t.TempDir()
	cases := []struct {
		name    string
		flag    string
		env     string
		locator func(t *testing.T) func() string
		want    func(t *testing.T, loc string) string
		wantErr bool
	}{
		{
			name: "flag wins",
			flag: "/flag/dir",
			env:  "/env/dir",
			want: func(*testing.T, string) string { return "/flag/dir" },
		},
		{
			name: "environment",
			env:  "/env/dir",
			want: func(*testing.T, string) string { return "/env/dir" },
		},
		{
			name: "locator names an existing directory",
			locator: func(t *testing.T) func() string {
				loc := filepath.Join(t.TempDir(), "data-dir")
				if err := os.WriteFile(loc, []byte(existing+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return func() string { return loc }
			},
			want: func(*testing.T, string) string { return existing },
		},
		{
			name: "locator names a missing directory",
			locator: func(t *testing.T) func() string {
				loc := filepath.Join(t.TempDir(), "data-dir")
				if err := os.WriteFile(loc, []byte(filepath.Join(t.TempDir(), "gone")), 0o600); err != nil {
					t.Fatal(err)
				}
				return func() string { return loc }
			},
			want: func(_ *testing.T, loc string) string { return filepath.Join(filepath.Dir(loc), dataSub) },
		},
		{
			name: "locator names a regular file",
			locator: func(t *testing.T) func() string {
				loc := filepath.Join(t.TempDir(), "data-dir")
				if err := os.WriteFile(loc, []byte(filepath.Join(loc)), 0o600); err != nil {
					t.Fatal(err)
				}
				return func() string { return loc }
			},
			want: func(_ *testing.T, loc string) string { return filepath.Join(filepath.Dir(loc), dataSub) },
		},
		{
			name: "locator is blank",
			locator: func(t *testing.T) func() string {
				loc := filepath.Join(t.TempDir(), "data-dir")
				if err := os.WriteFile(loc, []byte("  \n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return func() string { return loc }
			},
			want: func(_ *testing.T, loc string) string { return filepath.Join(filepath.Dir(loc), dataSub) },
		},
		{
			name: "no locator file",
			want: func(_ *testing.T, loc string) string { return filepath.Join(filepath.Dir(loc), dataSub) },
		},
		{
			name: "no configuration directory",
			locator: func(*testing.T) func() string {
				return func() string { return "" }
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envData, tc.env)
			if tc.locator != nil {
				withLocator(t, tc.locator(t))
			} else {
				loc := filepath.Join(t.TempDir(), "persona-spaces", "data-dir")
				withLocator(t, func() string { return loc })
			}
			got, err := DataDir(tc.flag)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("DataDir = %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := tc.want(t, Locator()); got != want {
				t.Fatalf("DataDir = %q, want %q", got, want)
			}
		})
	}
}

func TestRemember(t *testing.T) {
	t.Run("empty directory is not remembered", func(t *testing.T) {
		loc := filepath.Join(t.TempDir(), "persona-spaces", "data-dir")
		withLocator(t, func() string { return loc })
		Remember("")
		if _, err := os.Stat(loc); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("locator written for an empty directory: %v", err)
		}
	})
	t.Run("directory is remembered and found again", func(t *testing.T) {
		loc := filepath.Join(t.TempDir(), "persona-spaces", "data-dir")
		withLocator(t, func() string { return loc })
		dir := t.TempDir()
		t.Setenv(envData, "")
		Remember(dir)
		raw, err := os.ReadFile(loc)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != dir+"\n" {
			t.Fatalf("locator = %q", raw)
		}
		if fi, err := os.Stat(loc); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("locator mode: %v %v", fi, err)
		}
		got, err := DataDir("")
		if err != nil || got != dir {
			t.Fatalf("DataDir = %q, %v; want %q", got, err, dir)
		}
	})
	t.Run("no configuration directory", func(t *testing.T) {
		withLocator(t, func() string { return "" })
		Remember(t.TempDir())
	})
	t.Run("unwritable locator is ignored", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		withLocator(t, func() string { return filepath.Join(blocker, "data-dir") })
		Remember(t.TempDir())
	})
}

// TestDefaultLocator checks the production locator under a controlled home directory.
func TestDefaultLocator(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := defaultLocator(), filepath.Join(base, "persona-spaces", "data-dir"); got != want {
		t.Fatalf("defaultLocator = %q, want %q", got, want)
	}
	t.Setenv("HOME", "")
	if _, err := os.UserConfigDir(); err == nil {
		t.Skip("this platform finds a configuration directory without HOME")
	}
	if got := defaultLocator(); got != "" {
		t.Fatalf("defaultLocator without a home = %q, want empty", got)
	}
	withLocator(t, defaultLocator)
	if _, err := DataDir(""); err == nil {
		t.Fatal("DataDir without a configuration directory succeeded")
	}
}
