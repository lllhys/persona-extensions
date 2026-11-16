package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// Exercise names. The document names exercises by openGym catalogue id ("0025"); the names are in openGym's public
// catalogue (catalogue/i18n/<lang>.json in its repository). With exercise_names on, the Chinese names are read from
// there at most once a week and only id -> name is kept, in the data directory. The profile's own exercises
// (customEx) carry their names.

const (
	defaultCatalog = "https://raw.githubusercontent.com/DuarteSantos8/openGym/main/catalogue/i18n/zh.json"
	namesFile      = "exercise-names.json"
	namesFresh     = 7 * 24 * time.Hour
	catalogMost    = 16 << 20
)

// catalogNames is the catalogue's names in memory.
type catalogNames struct {
	mu    sync.RWMutex
	names map[string]string
}

func (c *catalogNames) get(id string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n, ok := c.names[id]
	return n, ok
}

func (c *catalogNames) set(m map[string]string) {
	c.mu.Lock()
	c.names = m
	c.mu.Unlock()
}

func (c *catalogNames) all() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.names
}

// namesOnDisk is what the data directory keeps.
type namesOnDisk struct {
	ETag    string            `json:"etag,omitempty"`
	Checked time.Time         `json:"checked"`
	Source  string            `json:"source"`
	Names   map[string]string `json:"names"`
}

// loadNames makes the catalogue names current: from the data directory while they are less than a week old, else
// from the catalogue (a conditional request). A failed read keeps what was there.
func (a *app) loadNames(ctx context.Context) error {
	a.mu.Lock()
	on, dir := a.names, a.dataDir
	a.mu.Unlock()
	if !on {
		a.catalog.set(nil)
		return nil
	}
	path := ""
	var disk namesOnDisk
	if dir != "" {
		path = filepath.Join(dir, namesFile)
		if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &disk) == nil && disk.Source == a.catalogURL {
			a.catalog.set(disk.Names)
			if a.now().Sub(disk.Checked) < namesFresh && len(disk.Names) > 0 {
				return nil
			}
		} else {
			disk = namesOnDisk{}
		}
	}
	names, etag, err := a.fetchCatalog(ctx, disk.ETag)
	if err != nil {
		return err
	}
	if names == nil { // not modified
		names, etag = disk.Names, disk.ETag
	}
	a.catalog.set(names)
	if path != "" {
		b, _ := json.Marshal(namesOnDisk{ETag: etag, Checked: a.now(), Source: a.catalogURL, Names: names})
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, b, 0o600); err == nil {
			_ = os.Rename(tmp, path)
		}
	}
	a.invalidate()
	return nil
}

// fetchCatalog reads the catalogue; nil names and no error is a 304.
func (a *app) fetchCatalog(ctx context.Context, etag string) (map[string]string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.catalogURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "persona-opengym/"+about.Version)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	res, err := a.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("读不到 openGym 的动作库：%w", unwrapURL(err))
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotModified {
		return nil, etag, nil
	}
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("openGym 的动作库回答 %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, catalogMost+1))
	if err != nil {
		return nil, "", err
	}
	if len(b) > catalogMost {
		return nil, "", errors.New("openGym 的动作库超过 16 MB")
	}
	var raw map[string]struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, "", fmt.Errorf("openGym 的动作库不是预期的 JSON：%w", err)
	}
	names := make(map[string]string, len(raw))
	for id, v := range raw {
		if n := strings.TrimSpace(v.Name); n != "" && len(id) <= 32 {
			names[id] = clip(n, 80)
		}
	}
	return names, res.Header.Get("ETag"), nil
}

// exName is an exercise's name: the profile's own, the catalogue's, else 动作 <id>.
func (a *app) exName(id string) string {
	if s := a.snapshot(); s != nil {
		if c := s.State.custom(id); c != nil && strings.TrimSpace(c.N) != "" {
			return strings.TrimSpace(c.N)
		}
	}
	if n, ok := a.catalog.get(id); ok {
		return n
	}
	return "动作 " + id
}

var catalogID = regexp.MustCompile(`^[0-9]{4}$`)

// knownID reports whether id names an exercise this profile can use: one it used, its own, or a catalogue id.
func (a *app) knownID(id string) bool {
	if _, ok := a.catalog.get(id); ok || catalogID.MatchString(id) {
		return true
	}
	if s := a.snapshot(); s != nil {
		return slices.Contains(s.State.usedExercises(), id)
	}
	return false
}

// exerciseMatch is one exercise found by name.
type exerciseMatch struct {
	ID     string
	Name   string
	Used   bool
	Custom bool
}

// findExercises is the exercises whose name or id contains q (all the used ones without q), used ones first.
func (a *app) findExercises(q string, most int) []exerciseMatch {
	q = strings.ToLower(strings.TrimSpace(q))
	var out []exerciseMatch
	seen := map[string]bool{}
	var used []string
	var st gymState
	if s := a.snapshot(); s != nil {
		st = s.State
		used = st.usedExercises()
	}
	match := func(id, name string) bool {
		return q == "" || strings.Contains(strings.ToLower(name), q) || strings.ToLower(id) == q
	}
	for _, id := range used {
		if n := a.exName(id); match(id, n) {
			seen[id] = true
			out = append(out, exerciseMatch{ID: id, Name: n, Used: true, Custom: st.custom(id) != nil})
		}
	}
	if q != "" {
		var rest []exerciseMatch
		for id, n := range a.catalog.all() {
			if !seen[id] && match(id, n) {
				rest = append(rest, exerciseMatch{ID: id, Name: n})
			}
		}
		slices.SortFunc(rest, func(x, y exerciseMatch) int {
			if d := len([]rune(x.Name)) - len([]rune(y.Name)); d != 0 {
				return d
			}
			return strings.Compare(x.ID, y.ID)
		})
		out = append(out, rest...)
	}
	if len(out) > most {
		out = out[:most]
	}
	return out
}

// resolveExercise turns what the assistant said into an exercise id: an id this profile can use, or a name that
// matches exactly one exercise (the profile's own and used ones first). Anything else is an error with candidates,
// never a guess.
func (a *app) resolveExercise(said string) (string, error) {
	said = strings.TrimSpace(said)
	if said == "" {
		return "", errors.New("动作不能是空的")
	}
	if a.knownID(said) {
		return said, nil
	}
	low := strings.ToLower(said)
	var exact []exerciseMatch
	for _, m := range a.findExercises(said, 400) {
		if strings.ToLower(m.Name) == low {
			exact = append(exact, m)
		}
	}
	if len(exact) == 1 || (len(exact) > 1 && exact[0].Used && !exact[1].Used) {
		return exact[0].ID, nil
	}
	cands := a.findExercises(said, 8)
	if len(exact) > 1 {
		cands = exact
	}
	if len(cands) == 0 {
		return "", fmt.Errorf("找不到动作「%s」：换个说法，用 opengym_exercises 查编号，或者 create_custom: true 新建一个自定义动作", said)
	}
	var names []string
	for _, c := range cands {
		names = append(names, fmt.Sprintf("%s（%s）", c.Name, c.ID))
	}
	return "", fmt.Errorf("「%s」不能确定是哪个动作，可能是：%s。用编号再试", said, strings.Join(names, "、"))
}
