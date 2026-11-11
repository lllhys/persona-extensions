package datadir

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// defaultLocator is the production locator, kept so that one test can check it without touching the real
// configuration directory (Locator is replaced in TestMain).
var defaultLocator = Locator

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "household-datadir-test-")
	if err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	Locator = func() string { return filepath.Join(root, "persona-household", "data-dir") }
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

// withLocator replaces Locator for one test and restores it afterwards.
func withLocator(t *testing.T, f func() string) {
	t.Helper()
	saved := Locator
	Locator = f
	t.Cleanup(func() { Locator = saved })
}

func TestResolve(t *testing.T) {
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
			want: func(_ *testing.T, loc string) string { return filepath.Join(filepath.Dir(loc), "data") },
		},
		{
			name: "locator names a regular file",
			locator: func(t *testing.T) func() string {
				loc := filepath.Join(t.TempDir(), "data-dir")
				if err := os.WriteFile(loc, []byte(loc), 0o600); err != nil {
					t.Fatal(err)
				}
				return func() string { return loc }
			},
			want: func(_ *testing.T, loc string) string { return filepath.Join(filepath.Dir(loc), "data") },
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
			want: func(_ *testing.T, loc string) string { return filepath.Join(filepath.Dir(loc), "data") },
		},
		{
			name: "no locator file",
			want: func(_ *testing.T, loc string) string { return filepath.Join(filepath.Dir(loc), "data") },
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
				loc := filepath.Join(t.TempDir(), "persona-household", "data-dir")
				withLocator(t, func() string { return loc })
			}
			got, err := Resolve(tc.flag)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Resolve = %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := tc.want(t, Locator()); got != want {
				t.Fatalf("Resolve = %q, want %q", got, want)
			}
		})
	}
}

func TestRemember(t *testing.T) {
	t.Run("empty directory is not remembered", func(t *testing.T) {
		loc := filepath.Join(t.TempDir(), "persona-household", "data-dir")
		withLocator(t, func() string { return loc })
		Remember("")
		if _, err := os.Stat(loc); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("locator written for an empty directory: %v", err)
		}
	})
	t.Run("directory is remembered and found again", func(t *testing.T) {
		loc := filepath.Join(t.TempDir(), "persona-household", "data-dir")
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
		got, err := Resolve("")
		if err != nil || got != dir {
			t.Fatalf("Resolve = %q, %v; want %q", got, err, dir)
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
	if got, want := defaultLocator(), filepath.Join(base, "persona-household", "data-dir"); got != want {
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
	if _, err := Resolve(""); err == nil {
		t.Fatal("Resolve without a configuration directory succeeded")
	}
}

func TestRememberZone(t *testing.T) {
	t.Run("the zone is recorded and read back", func(t *testing.T) {
		dir := t.TempDir()
		RememberZone(dir, "Asia/Shanghai")
		raw, err := os.ReadFile(filepath.Join(dir, "zone"))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != "Asia/Shanghai\n" {
			t.Fatalf("zone file = %q", raw)
		}
		if got := Zone(dir); got.String() != "Asia/Shanghai" {
			t.Fatalf("Zone = %q, want Asia/Shanghai", got)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("temporary files left behind: %v", entries)
		}
	})
	t.Run("a later zone replaces the earlier one", func(t *testing.T) {
		dir := t.TempDir()
		RememberZone(dir, "Asia/Shanghai")
		RememberZone(dir, "Europe/Berlin")
		if got := Zone(dir); got.String() != "Europe/Berlin" {
			t.Fatalf("Zone = %q, want Europe/Berlin", got)
		}
	})
	t.Run("an empty name is not recorded", func(t *testing.T) {
		dir := t.TempDir()
		RememberZone(dir, "")
		if _, err := os.Stat(filepath.Join(dir, "zone")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("zone written for an empty name: %v", err)
		}
	})
	t.Run("a missing directory is created and the zone recorded", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "gone")
		RememberZone(dir, "Asia/Shanghai")
		if got := Zone(dir); got.String() != "Asia/Shanghai" {
			t.Fatalf("Zone = %q, want Asia/Shanghai", got)
		}
	})
}

func TestZone(t *testing.T) {
	tests := []struct {
		name string
		file *string // nil: no zone file
	}{
		{name: "no zone file: this machine", file: nil},
		{name: "unknown name: this machine", file: ptr("Not/AZone\n")},
		{name: "blank file: this machine", file: ptr("  \n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.file != nil {
				if err := os.WriteFile(filepath.Join(dir, "zone"), []byte(*tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := Zone(dir); got != time.Local {
				t.Fatalf("Zone = %q, want this machine's zone %q", got, time.Local)
			}
		})
	}
}

func ptr(s string) *string { return &s }
