package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const stateFile = "spaces_state.json"

// store keeps the State in <dir>/spaces_state.json, shared by two processes: the extension the hub runs and
// `spaces mcp` the assistant runs. Every read loads the file again, so each sees the other's change at once; a
// change holds an exclusive lock on <dir>/.spaces.lock from load to write, and writes a temporary file and renames
// it. With no directory the state lives in memory (tests).
type store struct {
	mu   sync.Mutex
	dir  string
	mem  []byte
	seen stamp // the file as this process last read or wrote it
	now  func() time.Time
}

type stamp struct {
	mod  time.Time
	size int64
}

func newStore(dir string) *store { return &store{dir: dir, now: time.Now} }

func (s *store) path() string { return filepath.Join(s.dir, stateFile) }

// read runs f on the current state; f must not keep it.
func (s *store) read(f func(st *State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	f(st)
	return nil
}

// update runs f on the current state and saves it; when f fails nothing is saved.
func (s *store) update(f func(st *State, now time.Time) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir != "" {
		if err := os.MkdirAll(s.dir, 0o700); err != nil {
			return err
		}
		lock, err := os.OpenFile(filepath.Join(s.dir, ".spaces.lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		defer lock.Close()
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
			return err
		}
	}
	st, err := s.load()
	if err != nil {
		return err
	}
	if err := f(st, s.now().UTC()); err != nil {
		return err
	}
	return s.save(st)
}

// changed says whether the file changed since this process last read or wrote it.
func (s *store) changed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.stat()
	if now == s.seen {
		return false
	}
	s.seen = now
	return true
}

func (s *store) stat() stamp {
	if s.dir == "" {
		return stamp{}
	}
	fi, err := os.Stat(s.path())
	if err != nil {
		return stamp{}
	}
	return stamp{fi.ModTime(), fi.Size()}
}

func (s *store) load() (*State, error) {
	raw := s.mem
	if s.dir != "" {
		s.seen = s.stat()
		var err error
		raw, err = os.ReadFile(s.path())
		if errors.Is(err, os.ErrNotExist) {
			raw = nil
		} else if err != nil {
			return nil, err
		}
	}
	st := &State{Spaces: []*Space{}, Entities: []*Entity{}, Relations: []Relation{}}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, st); err != nil {
			// Left as it is, for the user to look at: never overwritten with an empty state.
			return nil, fmt.Errorf("%s 无法读取（文件保持原样）：%v", s.path(), err)
		}
	}
	return st, nil
}

func (s *store) save(st *State) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if s.dir == "" {
		s.mem = raw
		return nil
	}
	tmp, err := os.CreateTemp(s.dir, stateFile+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(append(raw, '\n'))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), s.path())
	}
	s.seen = s.stat()
	return err
}

// ---- Where `spaces mcp` finds the hub's data ----

// locatorFile is where the extension the hub runs leaves its data directory for `spaces mcp` on the same computer.
var locatorFile = func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "persona-spaces", "data-dir")
}

// rememberDataDir writes dir to the locator; best effort, since a sandbox may not allow it.
func rememberDataDir(dir string) {
	if path := locatorFile(); path != "" && dir != "" {
		if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
			_ = os.WriteFile(path, []byte(dir+"\n"), 0o600)
		}
	}
}

// dataDir is the MCP server's data directory: the flag, PERSONA_SPACES_DATA, the directory in the locator, else
// one of its own in the user's config directory.
func dataDir(flagDir string) (string, error) {
	if flagDir != "" {
		return flagDir, nil
	}
	if d := strings.TrimSpace(os.Getenv("PERSONA_SPACES_DATA")); d != "" {
		return d, nil
	}
	if path := locatorFile(); path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			if d := strings.TrimSpace(string(raw)); d != "" {
				if fi, err := os.Stat(d); err == nil && fi.IsDir() {
					return d, nil
				}
			}
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("no data directory: pass --data or set PERSONA_SPACES_DATA (%w)", err)
	}
	return filepath.Join(dir, "persona-spaces", "data"), nil
}
