package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

const stateFile = "disklessly_state.json"

// coldDir holds one cold-index file per machine, so the state file stays small and a catalog is
// read only by the views and tools that use it.
const coldDir = "disklessly_cold"

// store keeps the State in <dir>/disklessly_state.json, shared between the extension the hub runs
// and `disklessly mcp` the assistant runs. Every read reloads the file; every write holds an exclusive
// flock on <dir>/.disklessly.lock and writes via temporary file + rename.
type store struct {
	mu      sync.Mutex
	dir     string
	homeDir string
	mem     []byte
	memCold map[string][]byte
	seen    stamp
	// coldCache keeps parsed cold indexes by file stamp; callers get clones.
	coldCache map[string]coldCached
	now       func() time.Time
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

type coldCached struct {
	at  stamp
	raw []byte
	cat *ColdCatalog
}

func (s *store) coldPath(id string) string {
	return filepath.Join(s.dir, coldDir, url.PathEscape(id)+".json")
}

func cloneCold(c *ColdCatalog) *ColdCatalog {
	if c == nil {
		return nil
	}
	cp := *c
	cp.Entries = slices.Clone(c.Entries)
	cp.Missing = slices.Clone(c.Missing)
	cp.Unchecked = slices.Clone(c.Unchecked)
	cp.checked = nil
	return &cp
}

// loadCold reads machine id's cold index (nil when there is none or it cannot be read; a
// refresh rebuilds it).
func (s *store) loadCold(id string) *ColdCatalog {
	var raw []byte
	var at stamp
	if s.dir == "" {
		raw = s.memCold[id]
	} else {
		fi, err := os.Stat(s.coldPath(id))
		if err != nil {
			return nil
		}
		at = stamp{fi.ModTime(), fi.Size()}
		if c, ok := s.coldCache[id]; ok && c.at == at {
			return cloneCold(c.cat)
		}
		if raw, err = os.ReadFile(s.coldPath(id)); err != nil {
			return nil
		}
	}
	if len(raw) == 0 {
		return nil
	}
	var c ColdCatalog
	if json.Unmarshal(raw, &c) != nil {
		return nil
	}
	if s.coldCache == nil {
		s.coldCache = map[string]coldCached{}
	}
	s.coldCache[id] = coldCached{at: at, raw: raw, cat: &c}
	return cloneCold(&c)
}

// saveCold writes the cold indexes st loaded or set, when they changed, and removes dropped ones.
func (s *store) saveCold(st *State) error {
	for id := range st.coldGone {
		if _, ok := st.Cold[id]; ok {
			continue
		}
		delete(s.coldCache, id)
		if s.dir == "" {
			delete(s.memCold, id)
		} else if err := os.Remove(s.coldPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for id, c := range st.Cold {
		if c == nil {
			continue
		}
		raw, err := json.Marshal(c)
		if err != nil {
			return err
		}
		if old, ok := s.coldCache[id]; ok && bytes.Equal(old.raw, raw) {
			continue
		}
		at := stamp{}
		if s.dir == "" {
			if s.memCold == nil {
				s.memCold = map[string][]byte{}
			}
			s.memCold[id] = raw
		} else {
			if err := writeFileAtomic(filepath.Join(s.dir, coldDir), filepath.Base(s.coldPath(id)), raw); err != nil {
				return err
			}
			if fi, err := os.Stat(s.coldPath(id)); err == nil {
				at = stamp{fi.ModTime(), fi.Size()}
			}
		}
		if s.coldCache == nil {
			s.coldCache = map[string]coldCached{}
		}
		s.coldCache[id] = coldCached{at: at, raw: raw, cat: cloneCold(c)}
	}
	return nil
}

func writeFileAtomic(dir, name string, raw []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(raw)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, name))
	}
	return err
}

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
	st := &State{coldLoad: s.loadCold}
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
	if err := s.saveCold(st); err != nil {
		return err
	}
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
