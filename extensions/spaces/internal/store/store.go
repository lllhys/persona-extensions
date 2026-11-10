// Package store keeps the one Base of the extension in a JSON file under a data directory. It is the only package
// that touches the file system, and it implements kernel.Storage.
//
// A file that cannot be read, has another format or fails Base.Check is refused with its path in the error and is
// never overwritten. Every update takes an exclusive flock on the data directory, re-reads the file under that lock,
// applies the change, checks the result and replaces the file atomically. Readers see either the old or the new file.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nwverse/persona-extensions/extensions/spaces/internal/kernel"
)

const (
	fileName = "spaces.json"
	lockName = "spaces.lock"
	dataSub  = "data"
	envData  = "PERSONA_SPACES_DATA"
)

// Locator is the file that records the data directory of the hub-run extension, so that `spaces mcp` finds the same
// directory. It is a variable so that tests never touch the real configuration directory.
var Locator = func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "persona-spaces", "data-dir")
}

var _ kernel.Storage = (*Store)(nil)

// Store is a kernel.Storage over one JSON file, or over memory.
type Store struct {
	dir string // "" = in memory

	mu    sync.Mutex
	mem   []byte       // the file contents when dir is ""; nil before the first update
	cache *kernel.Base // the last decoded state; nil after an Update
	at    stamp        // the file stamp the cache was decoded from
	seen  stamp        // the file stamp this process last loaded or saved
}

// stamp identifies one version of the file. A change of mtime or size means another version.
type stamp struct {
	ok    bool // the file exists
	mtime time.Time
	size  int64
}

func (a stamp) same(b stamp) bool {
	return a.ok == b.ok && a.size == b.size && a.mtime.Equal(b.mtime)
}

// Open returns the store in dir; dir "" keeps the state in memory. Open reads nothing.
func Open(dir string) *Store {
	s := &Store{dir: dir}
	s.seen = s.stat()
	return s
}

// View runs f on the current state. f must not keep b. The state is reused from the cache while the file stamp is
// unchanged.
func (s *Store) View(f func(b *kernel.Base) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.load()
	if err != nil {
		return err
	}
	return f(b)
}

// Update runs f on a fresh copy of the stored state under the lock, checks the result and saves it. An error from f
// or from the check saves nothing.
func (s *Store) Update(f func(b *kernel.Base, now time.Time) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = nil
	if s.dir == "" {
		return s.commit(f)
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return s.commit(f)
}

// Changed reports whether the stored state differs from what this process last loaded or saved. Each foreign change
// is reported once: the stamp reported is recorded, so the next call is false until the file changes again. The
// cache is still reloaded by View, because its own stamp is kept apart from seen.
func (s *Store) Changed() bool {
	if s.dir == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stat()
	if st.same(s.seen) {
		return false
	}
	s.seen = st
	return true
}

// Remember writes the data directory to the locator file. It is best effort: failures are ignored.
func Remember(dir string) {
	loc := Locator()
	if dir == "" || loc == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(loc), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(loc, []byte(dir+"\n"), 0o600)
}

// DataDir resolves the data directory: the flag, then PERSONA_SPACES_DATA, then the directory recorded in the
// locator file when it exists, then <UserConfigDir>/persona-spaces/data.
func DataDir(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if env := os.Getenv(envData); env != "" {
		return env, nil
	}
	loc := Locator()
	if loc == "" {
		return "", errors.New("找不到用户配置目录，请用 --data 指定数据目录")
	}
	if raw, err := os.ReadFile(loc); err == nil {
		if dir := strings.TrimSpace(string(raw)); dir != "" {
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				return dir, nil
			}
		}
	}
	return filepath.Join(filepath.Dir(loc), dataSub), nil
}

// load returns the current state, from the cache when the file stamp has not changed. The caller holds mu.
func (s *Store) load() (*kernel.Base, error) {
	st := s.stat() // before the read: a write in between only makes the next load reread
	if s.cache != nil && s.at.same(st) {
		return s.cache, nil
	}
	raw, ok, err := s.read()
	if err != nil {
		return nil, err
	}
	b, err := decode(s.name(), raw, ok)
	if err != nil {
		return nil, err
	}
	s.cache, s.at, s.seen = b, st, st
	return b, nil
}

// commit applies f to the stored state and saves the result. The caller holds mu and, for a file, the lock.
func (s *Store) commit(f func(b *kernel.Base, now time.Time) error) error {
	prev := s.stat()
	raw, ok, err := s.read()
	if err != nil {
		return err
	}
	out, err := edit(s.name(), raw, ok, f)
	if err != nil {
		return err
	}
	if err := s.write(out, prev); err != nil {
		return err
	}
	s.seen = s.stat()
	return nil
}

// edit decodes the stored bytes, applies f and returns the bytes to save.
func edit(name string, raw []byte, ok bool, f func(b *kernel.Base, now time.Time) error) ([]byte, error) {
	b, err := decode(name, raw, ok)
	if err != nil {
		return nil, err
	}
	if err := f(b, time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := b.Check(); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// decode reads the stored bytes. Missing content is an empty state. Anything else must be a format-1 file with no
// unknown fields and no trailing content, and it must pass Base.Check; otherwise it is refused.
func decode(name string, raw []byte, ok bool) (*kernel.Base, error) {
	if !ok {
		return &kernel.Base{Format: 1, Spaces: []*kernel.Space{}}, nil
	}
	var head struct {
		Format int `json:"format"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, refuse(name, "不是合法的 JSON", err)
	}
	if head.Format != 1 {
		return nil, refuse(name, fmt.Sprintf("的格式是 %d，只能读取格式 1", head.Format), nil)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var b kernel.Base
	if err := dec.Decode(&b); err != nil {
		return nil, refuse(name, "不能解析", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, refuse(name, "含有多余内容", nil)
	}
	if err := b.Check(); err != nil {
		return nil, refuse(name, "没有通过检查", err)
	}
	return &b, nil
}

// refuse is the error for a file that is not read. The file is then left as it is.
func refuse(name, why string, err error) error {
	if err != nil {
		return fmt.Errorf("结构文件 %s %s，已拒绝读取，且不会覆盖它：%w", name, why, err)
	}
	return fmt.Errorf("结构文件 %s %s，已拒绝读取，且不会覆盖它", name, why)
}

// read returns the stored bytes; ok is false when there is no stored state yet.
func (s *Store) read() (raw []byte, ok bool, err error) {
	if s.dir == "" {
		return s.mem, s.mem != nil, nil
	}
	raw, err = os.ReadFile(s.path())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("无法读取结构文件 %s：%w", s.path(), err)
	}
	return raw, true, nil
}

// write replaces the stored bytes. prev is the stamp of the version being replaced.
func (s *Store) write(data []byte, prev stamp) error {
	if s.dir == "" {
		s.mem = data
		return nil
	}
	return writeAtomic(s.path(), data, prev.mtime)
}

// syncDir flushes the directory dir so that a rename in it survives a power loss. It is best effort: a platform that
// cannot sync a directory keeps the rename as the file system has it. It is a variable so that tests can observe it.
var syncDir = func(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// writeAtomic writes data to a temporary file beside path, syncs it and renames it over path. The file is created
// with mode 0600. Its mtime is moved past after, the mtime of the version it replaces, so that a save always has a
// new file stamp even when two saves fall within one clock tick of the file system.
func writeAtomic(path string, data []byte, after time.Time) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	mt := time.Now()
	if !mt.After(after) {
		mt = after.Add(time.Nanosecond)
	}
	if err = os.Chtimes(tmp.Name(), mt, mt); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// lock takes the exclusive cross-process lock of the data directory and returns the function that releases it.
func (s *Store) lock() (func(), error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, lockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flock(f, syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = flock(f, syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// flock applies a flock operation and retries when a signal interrupts it.
func flock(f *os.File, how int) error {
	for {
		err := syscall.Flock(int(f.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func (s *Store) path() string { return filepath.Join(s.dir, fileName) }

// name identifies the state in error messages.
func (s *Store) name() string {
	if s.dir == "" {
		return "（内存中的结构）"
	}
	return s.path()
}

// stat returns the stamp of the file; the zero stamp in memory or when the file is absent.
func (s *Store) stat() stamp {
	if s.dir == "" {
		return stamp{}
	}
	fi, err := os.Stat(s.path())
	if err != nil {
		return stamp{}
	}
	return stamp{ok: true, mtime: fi.ModTime(), size: fi.Size()}
}
