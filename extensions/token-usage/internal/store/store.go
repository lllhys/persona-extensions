// Package store keeps the extension's long history in one JSON file under its data directory, guarded by an
// exclusive flock. The hub keeps about a week of minute buckets; this file keeps the daily sums for 400 days.
//
// A bucket is one minute of one model and one launcher on one machine, and it is the minute's whole total, so
// merging it again replaces it (the hub sends a minute's whole total each time it changes). Every update takes the
// lock, re-reads the file, applies the change and replaces the file atomically, so readers see the old or the new.
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
)

const (
	fileName = "usage.json"
	lockName = "usage.lock"
	format   = 1
	// KeepDays is how many days of history the file holds. A day older than this, counted from the newest day
	// written, is dropped on the next merge.
	KeepDays = 400
)

// Bucket is one minute of one model and one launcher on one machine: the four kinds of tokens and the replies.
// A minute is its start in Unix seconds. An empty launcher is usage with no launcher (typed in a terminal).
type Bucket struct {
	Minute     int64  `json:"minute"`
	Model      string `json:"model"`
	Launcher   string `json:"launcher,omitempty"`
	Input      int64  `json:"in,omitempty"`
	CacheWrite int64  `json:"cache_write,omitempty"`
	CacheRead  int64  `json:"cache_read,omitempty"`
	Output     int64  `json:"out,omitempty"`
	Requests   int    `json:"req,omitempty"`
}

// Total is every token, cache reads included.
func (b Bucket) Total() int64 { return b.Input + b.CacheWrite + b.CacheRead + b.Output }

// key is the minute, model and launcher a bucket is summed under. The same key replaces, never adds twice.
type key struct {
	minute   int64
	model    string
	launcher string
}

// Day is one machine's minutes of one calendar day, in the hub's time zone.
type Day struct {
	Date    string   `json:"date"` // YYYY-MM-DD
	Machine string   `json:"machine"`
	Buckets []Bucket `json:"buckets"`
}

// State is the whole file.
type State struct {
	Format int   `json:"format"`
	Days   []Day `json:"days"`
}

// Store is the file in dir, or memory when dir is "".
type Store struct {
	dir string

	mu  sync.Mutex
	mem []byte // the file contents when dir is ""; nil before the first write
}

// Open returns the store in dir; dir "" keeps the state in memory. Open reads nothing.
func Open(dir string) *Store { return &Store{dir: dir} }

// View runs f on the current state. f must not keep s or change it.
func (s *Store) View(f func(st *State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	return f(st)
}

// Update runs f on a fresh copy of the stored state under the lock and saves the result. An error from f saves
// nothing.
func (s *Store) Update(f func(st *State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir != "" {
		unlock, err := s.lock()
		if err != nil {
			return err
		}
		defer unlock()
	}
	st, err := s.load()
	if err != nil {
		return err
	}
	if err := f(st); err != nil {
		return err
	}
	return s.save(st)
}

// Merge adds one machine's buckets, replacing a minute of the same model and launcher that is already stored, and
// drops days older than KeepDays before the newest day written. loc is the hub's time zone, which decides the day a
// minute belongs to. It returns how many buckets were new or changed.
func (s *Store) Merge(machine string, buckets []Bucket, loc *time.Location) (int, error) {
	if machine == "" {
		return 0, errors.New("a machine has no id")
	}
	changed := 0
	err := s.Update(func(st *State) error {
		byDay := map[string]int{}
		for i := range st.Days {
			d := &st.Days[i]
			if d.Machine == machine {
				byDay[d.Date] = i
			}
		}
		index := map[string]map[key]int{}
		for date, i := range byDay {
			m := map[key]int{}
			for j, b := range st.Days[i].Buckets {
				m[keyOf(b)] = j
			}
			index[date] = m
		}
		for _, b := range buckets {
			if b.Minute <= 0 {
				continue
			}
			date := time.Unix(b.Minute, 0).In(loc).Format("2006-01-02")
			dayIndex, ok := byDay[date]
			if !ok {
				st.Days = append(st.Days, Day{Date: date, Machine: machine})
				dayIndex = len(st.Days) - 1
				byDay[date] = dayIndex
				index[date] = map[key]int{}
			}
			d := &st.Days[dayIndex]
			k := keyOf(b)
			if i, ok := index[date][k]; ok {
				if d.Buckets[i] != b {
					d.Buckets[i] = b
					changed++
				}
				continue
			}
			index[date][k] = len(d.Buckets)
			d.Buckets = append(d.Buckets, b)
			changed++
		}
		prune(st, KeepDays)
		return nil
	})
	return changed, err
}

// keyOf is the identity a bucket replaces under.
func keyOf(b Bucket) key { return key{b.Minute, b.Model, b.Launcher} }

// prune drops the days older than keep days before the newest day in the file, and days with no buckets.
func prune(st *State, keep int) {
	newest := ""
	for _, d := range st.Days {
		if d.Date > newest {
			newest = d.Date
		}
	}
	if newest == "" {
		st.Days = []Day{}
		return
	}
	from, err := time.Parse("2006-01-02", newest)
	if err != nil {
		return
	}
	cutoff := from.AddDate(0, 0, -(keep - 1)).Format("2006-01-02")
	out := st.Days[:0]
	for _, d := range st.Days {
		if d.Date >= cutoff && len(d.Buckets) > 0 {
			out = append(out, d)
		}
	}
	st.Days = out
}

// load returns the current state. Missing content is an empty state. The caller holds mu and, for a file, the lock.
func (s *Store) load() (*State, error) {
	raw, ok, err := s.read()
	if err != nil {
		return nil, err
	}
	return decode(s.name(), raw, ok)
}

// save writes st. The caller holds mu and, for a file, the lock.
func (s *Store) save(st *State) error {
	st.Format = format
	if st.Days == nil {
		st.Days = []Day{}
	}
	out, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if s.dir == "" {
		s.mem = out
		return nil
	}
	return writeAtomic(s.path(), out)
}

// decode reads the stored bytes. Anything but a format-1 file with no unknown fields and no trailing content is
// refused, and the file is left as it is.
func decode(name string, raw []byte, ok bool) (*State, error) {
	if !ok {
		return &State{Format: format, Days: []Day{}}, nil
	}
	var head struct {
		Format int `json:"format"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, refuse(name, "不是合法的 JSON", err)
	}
	if head.Format != format {
		return nil, refuse(name, fmt.Sprintf("的格式是 %d，只能读取格式 %d", head.Format, format), nil)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var st State
	if err := dec.Decode(&st); err != nil {
		return nil, refuse(name, "不能解析", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, refuse(name, "含有多余内容", nil)
	}
	if st.Days == nil {
		st.Days = []Day{}
	}
	return &st, nil
}

// refuse is the error for a file that is not read. The file is then left as it is.
func refuse(name, why string, err error) error {
	if err != nil {
		return fmt.Errorf("用量文件 %s %s，已拒绝读取，且不会覆盖它：%w", name, why, err)
	}
	return fmt.Errorf("用量文件 %s %s，已拒绝读取，且不会覆盖它", name, why)
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
		return nil, false, fmt.Errorf("无法读取用量文件 %s：%w", s.path(), err)
	}
	return raw, true, nil
}

// writeAtomic writes data to a temporary file beside path, syncs it and renames it over path. The file is created
// with mode 0600.
func writeAtomic(path string, data []byte) (err error) {
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
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
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}
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
		return "（内存中的用量）"
	}
	return s.path()
}

// --- the data directory the assistant's server finds ---

const (
	envData  = "PERSONA_TOKEN_USAGE_DATA"
	dataSub  = "data"
	locName  = "data-dir"
	zoneFile = "zone"
	appDir   = "persona-token-usage"
	noConfig = "找不到用户配置目录，请用 --data 指定数据目录"
)

// Locator is the file that records the data directory of the hub-run extension, so that `token-usage mcp` finds the
// same records. It is a variable so that tests never touch the real configuration directory.
var Locator = func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, appDir, locName)
}

// Resolve returns the data directory: the flag, then PERSONA_TOKEN_USAGE_DATA, then the directory recorded in the
// locator file when it exists, then <UserConfigDir>/persona-token-usage/data.
func Resolve(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if env := os.Getenv(envData); env != "" {
		return env, nil
	}
	loc := Locator()
	if loc == "" {
		return "", errors.New(noConfig)
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

// RememberZone writes the IANA name of the hub's time zone to the zone file in dir, so that `token-usage mcp` counts
// days the same way as the hub-run extension. It is best effort: failures are ignored.
func RememberZone(dir, name string) {
	if dir == "" || name == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, zoneFile), []byte(name+"\n"), 0o600)
}

// Zone reads the hub's time zone from the zone file in dir. Without a readable record it is the local zone.
func Zone(dir string) *time.Location {
	raw, err := os.ReadFile(filepath.Join(dir, zoneFile))
	if err != nil {
		return time.Local
	}
	loc, err := time.LoadLocation(strings.TrimSpace(string(raw)))
	if err != nil {
		return time.Local
	}
	return loc
}
