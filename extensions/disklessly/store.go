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

const stateFile = "disklessly_state.json"

// store keeps the State in <dir>/disklessly_state.json, shared between the extension the hub runs
// and `disklessly mcp` the assistant runs. Every read reloads the file; every write holds an exclusive
// flock on <dir>/.disklessly.lock and writes via temporary file + rename.
type store struct {
	mu      sync.Mutex
	dir     string
	homeDir string
	mem     []byte
	seen    stamp
	now     func() time.Time
}

type stamp struct {
	mod  time.Time
	size int64
}

func newStore(dir string) *store {
	home, _ := os.UserHomeDir()
	return &store{dir: dir, homeDir: home, now: time.Now}
}

func (s *store) path() string { return filepath.Join(s.dir, stateFile) }

// read runs f on the current state; f must not retain st after returning.
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

// update runs f on the current state under an exclusive file lock and saves it when f returns nil.
func (s *store) update(f func(st *State, now time.Time) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir != "" {
		if err := os.MkdirAll(s.dir, 0o700); err != nil {
			return err
		}
		lock, err := os.OpenFile(filepath.Join(s.dir, ".disklessly.lock"), os.O_CREATE|os.O_RDWR, 0o600)
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

// changed reports whether the state file changed on disk since this process last read or wrote it.
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
	st := &State{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, st); err != nil {
			return nil, fmt.Errorf("%s 无法读取（文件保持原样）：%v", s.path(), err)
		}
	}
	now := s.now().UTC()
	st.ensureDefaults(now)
	if s.homeDir != "" {
		st.DiscoverMachines(s.homeDir, now)
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

// locatorFile records the hub extension's data directory so `disklessly mcp` on the same host finds it.
var locatorFile = func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "persona-disklessly", "data-dir")
}

func rememberDataDir(dir string) {
	if path := locatorFile(); path != "" && dir != "" {
		if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
			_ = os.WriteFile(path, []byte(dir+"\n"), 0o600)
		}
	}
}

func dataDir(flagDir string) (string, error) {
	if flagDir != "" {
		return flagDir, nil
	}
	if d := strings.TrimSpace(os.Getenv("PERSONA_DISKLESSLY_DATA")); d != "" {
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
		return "", fmt.Errorf("no data directory: pass --data or set PERSONA_DISKLESSLY_DATA (%w)", err)
	}
	return filepath.Join(dir, "persona-disklessly", "data"), nil
}
