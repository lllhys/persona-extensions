package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	stateFile   = "search_as_code_state.json"
	maxRecipes  = 50
	maxHistory  = 20
	maxSteps    = 12
	maxCodeLen  = 4000
	defaultLim  = 25
	maxLimitCap = 100
)

var validID = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,47}$`)

var scopes = []string{"conversation", "all", "user", "assistant", "errors"}

var scopeLabels = map[string]string{
	"conversation": "纯对话 (Without tools)",
	"all":          "全部 (含工具)",
	"user":         "仅用户提问",
	"assistant":    "仅助手回复",
	"errors":       "报错与异常",
}

func isValidScope(s string) bool {
	return slices.Contains(scopes, s)
}

// Recipe is a reusable Search-as-Code program template.
type Recipe struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Icon        string    `json:"icon,omitempty"`
	Description string    `json:"description,omitempty"`
	Scope       string    `json:"scope"`
	Code        string    `json:"code"`
	Builtin     bool      `json:"builtin,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitzero"`
}

// RunSummary records a brief summary of a past Search-as-Code execution.
type RunSummary struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Query           string    `json:"query,omitempty"`
	RecipeID        string    `json:"recipe_id,omitempty"`
	Scope           string    `json:"scope"`
	Code            string    `json:"code"`
	TotalMatches    int       `json:"total_matches"`
	StepCount       int       `json:"step_count"`
	TotalDurationMs int64     `json:"total_duration_ms"`
	RanAt           time.Time `json:"ran_at"`
}

// State is persisted in <dir>/search_as_code_state.json and shared between the hub extension and `search-as-code mcp`.
type State struct {
	DefaultScope string           `json:"default_scope,omitempty"`
	SessionDirs  []string         `json:"session_dirs,omitempty"`
	RepoDirs     []string         `json:"repo_dirs,omitempty"`
	ColdTextDirs []string         `json:"cold_text_dirs,omitempty"`
	Recipes      []*Recipe        `json:"recipes"`
	LastRun      *ExecutionResult `json:"last_run,omitempty"`
	History      []RunSummary     `json:"history,omitempty"`
	// Restores are the page's recent restores of archived sessions, newest first (at most maxRestoreNotices).
	Restores []RestoreNotice `json:"restores,omitempty"`
}

const maxRestoreNotices = 5

// RestoreNotice is one restore started from the page: running, then its outcome.
type RestoreNotice struct {
	EntryID   string    `json:"entry_id"`
	Title     string    `json:"title,omitempty"`
	Path      string    `json:"path,omitempty"`
	Running   bool      `json:"running,omitempty"`
	OK        bool      `json:"ok,omitempty"`
	Message   string    `json:"message,omitempty"`
	Resume    string    `json:"resume,omitempty"`
	StartedAt time.Time `json:"started_at"`
	At        time.Time `json:"at,omitzero"`
}

// restoreRunning reports whether the page is restoring entryID.
func (st *State) restoreRunning(entryID string) bool {
	for _, n := range st.Restores {
		if n.EntryID == entryID && n.Running {
			return true
		}
	}
	return false
}

// setRestore records n, replacing an earlier notice of the same entry.
func (st *State) setRestore(n RestoreNotice) {
	st.Restores = slices.DeleteFunc(st.Restores, func(o RestoreNotice) bool { return o.EntryID == n.EntryID })
	st.Restores = append([]RestoreNotice{n}, st.Restores...)
	if len(st.Restores) > maxRestoreNotices {
		st.Restores = st.Restores[:maxRestoreNotices]
	}
}

var builtinRecipes = []Recipe{
	{
		ID:          "error-triage",
		Name:        "近期报错会话分诊",
		Icon:        "warning",
		Description: "扫描本机 Agent 会话中的报错、异常与非零退出，排除工具输出噪音，关联同文件上下文并按项目聚合排序。",
		Scope:       "errors",
		Code: "scan_sessions(query=\"\", scope=\"errors\")\n" +
			"|> exclude_tools()\n" +
			"|> correlate(by=\"file\")\n" +
			"|> group_by(field=\"project\")\n" +
			"|> rank(by=\"errors_first\")\n" +
			"|> limit(n=25)",
		Builtin: true,
	},
	{
		ID:          "cross-session-context",
		Name:        "跨会话决策与上下文溯源",
		Icon:        "branch",
		Description: "在纯对话模式（Without tools）下检索模块设计与修改讨论，按项目与关联文件串联历次会话并去重。",
		Scope:       "conversation",
		Code: "scan_sessions(query=\"\", scope=\"conversation\")\n" +
			"|> exclude_tools()\n" +
			"|> correlate(by=\"project\")\n" +
			"|> dedup(by=\"session\")\n" +
			"|> group_by(field=\"project\")\n" +
			"|> rank(by=\"relevance_freshness\")\n" +
			"|> limit(n=25)",
		Builtin: true,
	},
	{
		ID:          "model-harness-audit",
		Name:        "模型与 Harness 产出审计",
		Icon:        "chart",
		Description: "跨 Claude Code 与 Codex 会话统计各模型与 Harness 的近期会话产出、关联改动文件与异常分布。",
		Scope:       "all",
		Code: "scan_sessions(query=\"\", scope=\"all\")\n" +
			"|> correlate(by=\"file\")\n" +
			"|> group_by(field=\"model\")\n" +
			"|> rank(by=\"freshness\")\n" +
			"|> limit(n=30)",
		Builtin: true,
	},
}

// allRecipes returns the built-in recipes (unless overridden by a custom recipe with the same ID) followed by user recipes.
func (st *State) allRecipes() []*Recipe {
	customByID := map[string]*Recipe{}
	for _, r := range st.Recipes {
		customByID[r.ID] = r
	}
	var out []*Recipe
	for _, b := range builtinRecipes {
		if c, ok := customByID[b.ID]; ok {
			cp := *c
			cp.Builtin = true
			out = append(out, &cp)
			delete(customByID, b.ID)
		} else {
			cp := b
			out = append(out, &cp)
		}
	}
	for _, r := range st.Recipes {
		if _, ok := customByID[r.ID]; ok {
			out = append(out, r)
		}
	}
	return out
}

func (st *State) recipe(id string) *Recipe {
	for _, r := range st.allRecipes() {
		if r.ID == id {
			return r
		}
	}
	return nil
}

type RecipeInput struct {
	ID          string
	Name        string
	Icon        string
	Description string
	Scope       string
	Code        string
}

func (st *State) upsertRecipe(in RecipeInput, now time.Time) (*Recipe, error) {
	id := strings.TrimSpace(in.ID)
	name := strings.TrimSpace(in.Name)
	code := strings.TrimSpace(in.Code)
	scope := cmp.Or(strings.TrimSpace(in.Scope), "conversation")
	icon := cmp.Or(strings.TrimSpace(in.Icon), "code")
	desc := strings.TrimSpace(in.Description)

	if id == "" {
		id = slugify(name)
	}
	if !validID.MatchString(id) {
		return nil, fmt.Errorf("配方 ID %q 必须是小写字母开头的字母、数字、连字符或下划线（2-48 字符）", id)
	}
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return nil, errors.New("配方名称必填，且不超过 80 个字符")
	}
	if !isValidScope(scope) {
		return nil, fmt.Errorf("不支持的检索范围 %q（可选：%s）", scope, strings.Join(scopes, ", "))
	}
	if code == "" || len(code) > maxCodeLen {
		return nil, fmt.Errorf("Search-as-Code 代码必填，且不超过 %d 字节", maxCodeLen)
	}
	if !slices.Contains(
		[]string{"code", "search", "warning", "branch", "chart", "doc", "folder", "star", "check", "clock", "tag", "list", "file"},
		icon,
	) {
		icon = "code"
	}
	if _, err := CompileProgram(CompileInput{Code: code, Scope: scope}); err != nil {
		return nil, fmt.Errorf("Search-as-Code 代码语法错误：%w", err)
	}

	for _, r := range st.Recipes {
		if r.ID == id {
			r.Name = name
			r.Icon = icon
			r.Description = desc
			r.Scope = scope
			r.Code = code
			r.UpdatedAt = now
			return r, nil
		}
	}
	if len(st.Recipes) >= maxRecipes {
		return nil, fmt.Errorf("最多保存 %d 个自定义搜索配方", maxRecipes)
	}
	r := &Recipe{
		ID:          id,
		Name:        name,
		Icon:        icon,
		Description: desc,
		Scope:       scope,
		Code:        code,
		UpdatedAt:   now,
	}
	st.Recipes = append(st.Recipes, r)
	return r, nil
}

func (st *State) deleteRecipe(id string) error {
	id = strings.TrimSpace(id)
	for i, r := range st.Recipes {
		if r.ID == id {
			st.Recipes = slices.Delete(st.Recipes, i, i+1)
			return nil
		}
	}
	for _, b := range builtinRecipes {
		if b.ID == id {
			return fmt.Errorf("预置配方 %q 处于默认状态，无需删除", id)
		}
	}
	return fmt.Errorf("找不到配方 %q", id)
}

func (st *State) recordRun(res *ExecutionResult, now time.Time) {
	st.LastRun = res
	sum := RunSummary{
		ID:              fmt.Sprintf("run-%d", now.UnixMilli()),
		Title:           res.Program.Title,
		Query:           res.Program.Query,
		RecipeID:        res.Program.RecipeID,
		Scope:           res.Program.Scope,
		Code:            res.Program.CodeListing,
		TotalMatches:    res.TotalMatches,
		StepCount:       len(res.StepTraces),
		TotalDurationMs: res.TotalDurationMs,
		RanAt:           now,
	}
	st.History = append([]RunSummary{sum}, st.History...)
	if len(st.History) > maxHistory {
		st.History = st.History[:maxHistory]
	}
}

func slugify(s string) string {
	var b strings.Builder
	b.WriteString("recipe-")
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 7 && !strings.HasSuffix(b.String(), "-") {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) < 8 {
		return fmt.Sprintf("recipe-%d", time.Now().Unix()%100000)
	}
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

func splitDirs(raw string) []string {
	home, _ := os.UserHomeDir()
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n'
	}) {
		p := strings.TrimSpace(part)
		if home != "" && strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// store persists State in <dir>/search_as_code_state.json with flock synchronization.
type store struct {
	mu   sync.Mutex
	dir  string
	mem  []byte
	seen stamp
	now  func() time.Time
}

type stamp struct {
	mod  time.Time
	size int64
}

func newStore(dir string) *store { return &store{dir: dir, now: time.Now} }

func (s *store) path() string { return filepath.Join(s.dir, stateFile) }

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

func (s *store) update(f func(st *State, now time.Time) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir != "" {
		if err := os.MkdirAll(s.dir, 0o700); err != nil {
			return err
		}
		lock, err := os.OpenFile(filepath.Join(s.dir, ".search_as_code.lock"), os.O_CREATE|os.O_RDWR, 0o600)
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
	st := &State{DefaultScope: "conversation", Recipes: []*Recipe{}, History: []RunSummary{}}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, st); err != nil {
			return nil, fmt.Errorf("%s 无法读取（文件保持原样）：%v", s.path(), err)
		}
	}
	if !isValidScope(st.DefaultScope) {
		st.DefaultScope = "conversation"
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

// locatorFile is where the extension the hub runs leaves its data directory for `search-as-code mcp` on the same computer.
var locatorFile = func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "persona-search-as-code", "data-dir")
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
	if d := strings.TrimSpace(os.Getenv("PERSONA_SEARCH_AS_CODE_DATA")); d != "" {
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
		return "", fmt.Errorf("no data directory: pass --data or set PERSONA_SEARCH_AS_CODE_DATA (%w)", err)
	}
	return filepath.Join(dir, "persona-search-as-code", "data"), nil
}
