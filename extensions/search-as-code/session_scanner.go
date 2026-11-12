package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxScannedSessionFiles = 80
	maxSessionFileBytes    = 4 << 20
	maxRestoredExtraFiles  = 20 // restored transcripts scanned beyond the live window
	maxScannedRepoFiles    = 200
	maxRepoFileBytes       = 512 << 10
)

// defaultSessionDirs returns the default coding-agent session directories on this computer.
// Overridden in tests so tests stay hermetic and fast.
var defaultSessionDirs = func() []string {
	if env := strings.TrimSpace(os.Getenv("PERSONA_SEARCH_AS_CODE_SESSIONS")); env != "" {
		return splitDirs(env)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	var out []string
	for _, rel := range []string{
		filepath.Join(home, ".claude", "projects"),
		filepath.Join(home, ".codex", "sessions"),
	} {
		if fi, err := os.Stat(rel); err == nil && fi.IsDir() {
			out = append(out, rel)
		}
	}
	return out
}

// SessionTurn represents a single parsed turn/event inside an agent session transcript.
type SessionTurn struct {
	Line      int       `json:"line"`
	Role      string    `json:"role"` // "user", "assistant", "tool", "error"
	Text      string    `json:"text"`
	ToolName  string    `json:"tool_name,omitempty"`
	IsTool    bool      `json:"is_tool,omitempty"`
	IsError   bool      `json:"is_error,omitempty"`
	ErrorKind string    `json:"error_kind,omitempty"`
	Files     []string  `json:"files,omitempty"`
	At        time.Time `json:"at,omitzero"`
}

// SessionRecord holds the parsed metadata and turns of one coding-agent session file.
type SessionRecord struct {
	SessionID  string        `json:"session_id"`
	Title      string        `json:"title"`
	Project    string        `json:"project"`
	CWD        string        `json:"cwd,omitempty"`
	Branch     string        `json:"branch,omitempty"`
	Harness    string        `json:"harness"` // "claude-code", "codex", "agent"
	Model      string        `json:"model,omitempty"`
	FilePath   string        `json:"file_path,omitempty"`
	UpdatedAt  time.Time     `json:"updated_at"`
	TurnCount  int           `json:"turn_count"`
	ToolCalls  int           `json:"tool_calls"`
	ErrorCount int           `json:"error_count"`
	Files      []string      `json:"files,omitempty"`
	Turns      []SessionTurn `json:"turns"`
	// Archived sessions come from a disklessly text index: the transcript is in the cloud and FilePath (the
	// original path) does not exist on this computer.
	Archived   bool      `json:"archived,omitempty"`
	EntryID    string    `json:"entry_id,omitempty"`
	TextPath   string    `json:"text_path,omitempty"`
	StateDir   string    `json:"state_dir,omitempty"`
	ArchivedAt time.Time `json:"archived_at,omitzero"`
	// Unconfirmed: the index has no segment or archive time yet (the upload did not complete), so there is no
	// confirmed cloud copy to restore.
	Unconfirmed bool `json:"unconfirmed,omitempty"`
}

// SourceStats summarizes the discovered session and file sources.
type SourceStats struct {
	SessionDirs     []string       `json:"session_dirs"`
	RepoDirs        []string       `json:"repo_dirs"`
	TotalSessions   int            `json:"total_sessions"`
	TotalTurns      int            `json:"total_turns"`
	ErrorSessions   int            `json:"error_sessions"`
	ByHarness       map[string]int `json:"by_harness"`
	ByModel         map[string]int `json:"by_model"`
	ByProject       map[string]int `json:"by_project"`
	ScannedRepoFile int            `json:"scanned_repo_files"`
	ColdTextDirs    []string       `json:"cold_text_dirs,omitempty"`
	// ArchivedSessions counts sessions read from disklessly text indexes (已上云), included in TotalSessions;
	// UnconfirmedSessions those of them whose upload is not confirmed (未确认上云), and ArchivedOmitted the
	// archived indexes left out beyond the per-scan cap.
	ArchivedSessions    int `json:"archived_sessions"`
	UnconfirmedSessions int `json:"unconfirmed_sessions,omitempty"`
	ArchivedOmitted     int `json:"archived_omitted,omitempty"`
}

var (
	errorPattern       = regexp.MustCompile(`(?i)(\bpanic:|\bfatal\b|\berror\b|\bfailed\b|\bfailure\b|\bexit status [1-9]|\btimed? out\b|\bexception\b|\bpermission denied\b|\bquota\b|\b429\b|\b50[0234]\b|报错|失败|异常|超时|崩溃)`)
	strictErrorPattern = regexp.MustCompile(`(?i)(\bpanic:|\bfatal:|\berror:|\bexception:|\bexit status [1-9]|\bhttp 429\b|\b50[0234] bad gateway\b)`)
	fileTokenRe        = regexp.MustCompile(`(?:^|[\s"'` + "`" + `(])([a-zA-Z0-9_.-]+(?:/[a-zA-Z0-9_.-]+)+\.(?:go|ts|tsx|js|py|rs|md|json|yaml|yml|sh|sql|swift|kt))(?:$|[\s"'` + "`" + `):,])`)
	xmlTagNoise        = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
)

func classifyErrorKind(text string) string {
	low := strings.ToLower(text)
	switch {
	case strings.Contains(low, "panic:") || strings.Contains(low, "nil pointer") || strings.Contains(low, "崩溃"):
		return "panic"
	case strings.Contains(low, "timeout") || strings.Contains(low, "timed out") || strings.Contains(low, "超时"):
		return "timeout"
	case strings.Contains(low, "429") || strings.Contains(low, "quota") || strings.Contains(low, "rate limit"):
		return "quota_or_ratelimit"
	case strings.Contains(low, "permission denied") || strings.Contains(low, "401") || strings.Contains(low, "403"):
		return "auth_or_permission"
	case strings.Contains(low, "exit status") || strings.Contains(low, "test") || strings.Contains(low, "fail"):
		return "command_or_test_failure"
	default:
		return "runtime_error"
	}
}

func activeSessionDirs(st *State) []string {
	var out []string
	for _, d := range defaultSessionDirs() {
		if d != "" && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	if st != nil {
		for _, d := range st.SessionDirs {
			if d != "" && !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
	}
	return out
}

type fileEntry struct {
	path string
	mod  time.Time
}

// loadSessions scans the configured session directories and parses up to maxScannedSessionFiles recent JSONL files.
func loadSessions(st *State) ([]*SessionRecord, SourceStats) {
	dirs := activeSessionDirs(st)
	var repoDirs []string
	if st != nil {
		repoDirs = slices.Clone(st.RepoDirs)
	}
	stats := SourceStats{
		SessionDirs: dirs,
		RepoDirs:    repoDirs,
		ByHarness:   map[string]int{},
		ByModel:     map[string]int{},
		ByProject:   map[string]int{},
	}

	var files []fileEntry
	for _, root := range dirs {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "node_modules" || name == "subagents" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
				return nil
			}
			fi, err := d.Info()
			if err != nil || fi.Size() == 0 {
				return nil
			}
			files = append(files, fileEntry{path: path, mod: fi.ModTime()})
			return nil
		})
	}

	slices.SortFunc(files, func(a, b fileEntry) int {
		return b.mod.Compare(a.mod)
	})
	if len(files) > maxScannedSessionFiles {
		files = files[:maxScannedSessionFiles]
	}

	var sessions []*SessionRecord
	for _, fe := range files {
		rec := parseSessionFile(fe.path, fe.mod)
		if rec == nil || len(rec.Turns) == 0 {
			continue
		}
		sessions = append(sessions, rec)
	}

	// Archived (已上云) sessions from disklessly text indexes, minus any that are also present live.
	livePaths, liveIDs := map[string]bool{}, map[string]bool{}
	for _, rec := range sessions {
		livePaths[filepath.Clean(rec.FilePath)] = true
		liveIDs[rec.SessionID] = true
	}
	cold, restored, coldDirs, omitted := loadColdSessions(st)
	stats.ColdTextDirs = coldDirs
	stats.ArchivedOmitted = omitted
	// A restored transcript may sit outside the session dirs or the most-recent window: scan it as local, newest
	// first and at most maxRestoredExtraFiles of them; when the live window is full, only those as new as it.
	var windowFloor time.Time
	if len(files) >= maxScannedSessionFiles {
		windowFloor = files[len(files)-1].mod
	}
	var extra []fileEntry
	for _, path := range restored {
		if livePaths[filepath.Clean(path)] {
			continue
		}
		fi, err := os.Stat(path)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 || fi.Size() > maxSessionFileBytes || fi.ModTime().Before(windowFloor) {
			continue
		}
		extra = append(extra, fileEntry{path: path, mod: fi.ModTime()})
	}
	slices.SortFunc(extra, func(a, b fileEntry) int { return b.mod.Compare(a.mod) })
	for _, fe := range extra[:min(len(extra), maxRestoredExtraFiles)] {
		if rec := parseSessionFile(fe.path, fe.mod); rec != nil && len(rec.Turns) > 0 && !liveIDs[rec.SessionID] {
			livePaths[filepath.Clean(fe.path)] = true
			liveIDs[rec.SessionID] = true
			sessions = append(sessions, rec)
		}
	}
	for _, rec := range cold {
		if (rec.FilePath != "" && livePaths[filepath.Clean(rec.FilePath)]) || liveIDs[rec.SessionID] {
			continue
		}
		liveIDs[rec.SessionID] = true
		sessions = append(sessions, rec)
		stats.ArchivedSessions++
		if rec.Unconfirmed {
			stats.UnconfirmedSessions++
		}
	}

	for _, rec := range sessions {
		stats.TotalSessions++
		stats.TotalTurns += len(rec.Turns)
		if rec.ErrorCount > 0 {
			stats.ErrorSessions++
		}
		stats.ByHarness[rec.Harness]++
		if rec.Model != "" {
			stats.ByModel[rec.Model]++
		}
		if rec.Project != "" {
			stats.ByProject[rec.Project]++
		}
	}
	return sessions, stats
}

func parseSessionFile(path string, mod time.Time) *SessionRecord {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	baseID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	harness := detectHarness(path)
	rec := &SessionRecord{
		SessionID: baseID,
		Harness:   harness,
		FilePath:  path,
		UpdatedAt: mod.UTC(),
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxSessionFileBytes)
	lineNo := 0
	var fileSet []string
	addFile := func(fp string) {
		fp = cleanRelPath(fp)
		if fp != "" && !slices.Contains(fileSet, fp) && len(fileSet) < 24 {
			fileSet = append(fileSet, fp)
		}
	}

	for sc.Scan() {
		lineNo++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			continue
		}
		if sid, _ := obj["sessionId"].(string); sid != "" && rec.SessionID == baseID {
			rec.SessionID = sid
		} else if sid, _ := obj["session_id"].(string); sid != "" && rec.SessionID == baseID {
			rec.SessionID = sid
		}
		if cwd, _ := obj["cwd"].(string); cwd != "" && rec.CWD == "" {
			rec.CWD = cwd
		}
		if br, _ := obj["gitBranch"].(string); br != "" && rec.Branch == "" {
			rec.Branch = br
		} else if br, _ := obj["branch"].(string); br != "" && rec.Branch == "" {
			rec.Branch = br
		}
		if proj, _ := obj["project"].(string); proj != "" && rec.Project == "" {
			rec.Project = proj
		}
		if h, _ := obj["harness"].(string); h != "" {
			rec.Harness = h
		}
		if m, _ := obj["model"].(string); m != "" && rec.Model == "" {
			rec.Model = m
		}
		if title, _ := obj["title"].(string); title != "" && rec.Title == "" {
			rec.Title = title
		} else if sum, _ := obj["summary"].(string); sum != "" && rec.Title == "" {
			rec.Title = sum
		}
		var turnAt time.Time
		if ts, _ := obj["timestamp"].(string); ts != "" {
			if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				turnAt = t.UTC()
				if turnAt.After(rec.UpdatedAt) {
					rec.UpdatedAt = turnAt
				}
			}
		}

		if filesArr, ok := obj["files"].([]any); ok {
			for _, fv := range filesArr {
				if fs, ok := fv.(string); ok {
					addFile(fs)
				}
			}
		}

		turns := extractTurnsFromObject(lineNo, obj, turnAt, rec, addFile)
		for _, tr := range turns {
			rec.Turns = append(rec.Turns, tr)
			if tr.IsTool {
				rec.ToolCalls++
			} else {
				rec.TurnCount++
				if rec.Title == "" && tr.Role == "user" && len(tr.Text) > 0 {
					rec.Title = firstLineTitle(tr.Text)
				}
			}
			if tr.IsError {
				rec.ErrorCount++
			}
		}
	}

	if rec.Project == "" {
		rec.Project = inferProject(rec.CWD, path)
	}
	if rec.Model == "" {
		rec.Model = cmp.Or(inferDefaultModel(rec.Harness), "claude-sonnet-5-5")
	}
	if rec.Title == "" {
		for _, tr := range rec.Turns {
			if tr.Text != "" {
				rec.Title = firstLineTitle(tr.Text)
				break
			}
		}
	}
	if rec.Title == "" {
		rec.Title = "会话 " + shortID(rec.SessionID)
	}
	rec.Files = fileSet
	return rec
}

func extractTurnsFromObject(lineNo int, obj map[string]any, at time.Time, rec *SessionRecord, addFile func(string)) []SessionTurn {
	recType, _ := obj["type"].(string)

	// 1. Codex session_meta / response_item / event_msg format
	if payload, ok := obj["payload"].(map[string]any); ok {
		if cwd, _ := payload["cwd"].(string); cwd != "" && rec.CWD == "" {
			rec.CWD = cwd
		}
		if m, _ := payload["model"].(string); m != "" && rec.Model == "" {
			rec.Model = m
		}
		pType, _ := payload["type"].(string)
		role, _ := payload["role"].(string)
		if pType == "function_call" || pType == "function_call_output" || pType == "custom_tool_call" {
			name, _ := payload["name"].(string)
			out := extractStringField(payload, "output", "arguments", "content")
			collectPathMatches(out, addFile)
			isErr := isErrorTurn(out, payload["is_error"] == true)
			turnRole := "tool"
			if isErr {
				turnRole = "error"
			}
			if out == "" {
				out = name
			}
			return []SessionTurn{{
				Line:      lineNo,
				Role:      turnRole,
				Text:      cleanTurnText(out),
				ToolName:  name,
				IsTool:    true,
				IsError:   isErr,
				ErrorKind: condErrorKind(isErr, out),
				At:        at,
			}}
		}
		text := extractContentText(payload["content"])
		if text == "" {
			text = extractStringField(payload, "text", "message")
		}
		if text != "" {
			normRole := normalizeRole(cmp.Or(role, recType))
			collectPathMatches(text, addFile)
			isErr := isConversationalError(text, payload["is_error"] == true || normRole == "error")
			return []SessionTurn{{
				Line:      lineNo,
				Role:      normRole,
				Text:      cleanTurnText(text),
				IsError:   isErr,
				ErrorKind: condErrorKind(isErr, text),
				Files:     extractFilesFromText(text),
				At:        at,
			}}
		}
	}

	// 2. Claude Code format: obj["message"] is a map with role, model, content
	if msg, ok := obj["message"].(map[string]any); ok {
		if m, _ := msg["model"].(string); m != "" && rec.Model == "" && m != "<synthetic>" {
			rec.Model = m
		}
		msgRole, _ := msg["role"].(string)
		baseRole := normalizeRole(cmp.Or(msgRole, recType))
		content := msg["content"]

		switch c := content.(type) {
		case string:
			cleaned := cleanTurnText(c)
			if cleaned == "" {
				return nil
			}
			collectPathMatches(cleaned, addFile)
			isErr := isConversationalError(cleaned, obj["is_error"] == true || baseRole == "error")
			return []SessionTurn{{
				Line:      lineNo,
				Role:      baseRole,
				Text:      cleaned,
				IsError:   isErr,
				ErrorKind: condErrorKind(isErr, cleaned),
				Files:     extractFilesFromText(cleaned),
				At:        at,
			}}
		case []any:
			var out []SessionTurn
			for _, item := range c {
				blk, ok := item.(map[string]any)
				if !ok {
					continue
				}
				bType, _ := blk["type"].(string)
				switch bType {
				case "text":
					txt, _ := blk["text"].(string)
					cleaned := cleanTurnText(txt)
					if cleaned == "" {
						continue
					}
					collectPathMatches(cleaned, addFile)
					isErr := isConversationalError(cleaned, false)
					out = append(out, SessionTurn{
						Line:      lineNo,
						Role:      baseRole,
						Text:      cleaned,
						IsError:   isErr,
						ErrorKind: condErrorKind(isErr, cleaned),
						Files:     extractFilesFromText(cleaned),
						At:        at,
					})
				case "tool_use":
					tName, _ := blk["name"].(string)
					var inputSummary string
					if inMap, ok := blk["input"].(map[string]any); ok {
						for _, k := range []string{"file_path", "path", "target_file"} {
							if fp, ok := inMap[k].(string); ok && fp != "" {
								addFile(fp)
							}
						}
						for _, k := range []string{"command", "query", "file_path", "path", "pattern", "description"} {
							if v, ok := inMap[k].(string); ok && v != "" {
								inputSummary = k + ": " + v
								break
							}
						}
					}
					txt := strings.TrimSpace(tName + " " + inputSummary)
					if txt == "" {
						continue
					}
					collectPathMatches(txt, addFile)
					out = append(out, SessionTurn{
						Line:     lineNo,
						Role:     "tool",
						Text:     cleanTurnText(txt),
						ToolName: tName,
						IsTool:   true,
						Files:    extractFilesFromText(txt),
						At:       at,
					})
				case "tool_result":
					resTxt := extractContentText(blk["content"])
					cleaned := cleanTurnText(resTxt)
					if cleaned == "" {
						continue
					}
					collectPathMatches(cleaned, addFile)
					explicitErr := blk["is_error"] == true
					isErr := isErrorTurn(cleaned, explicitErr)
					role := "tool"
					if isErr {
						role = "error"
					}
					out = append(out, SessionTurn{
						Line:      lineNo,
						Role:      role,
						Text:      cleaned,
						IsTool:    true,
						IsError:   isErr,
						ErrorKind: condErrorKind(isErr, cleaned),
						Files:     extractFilesFromText(cleaned),
						At:        at,
					})
				}
			}
			return out
		}
	}

	// 3. Direct / flat JSONL record format: {role, content/text, is_error, is_tool, tool_name}
	role := normalizeRole(cmp.Or(stringVal(obj["role"]), recType))
	if role == "" {
		return nil
	}
	text := cleanTurnText(cmp.Or(stringVal(obj["text"]), extractContentText(obj["content"])))
	if text == "" {
		return nil
	}
	isTool := obj["is_tool"] == true || role == "tool"
	isErr := isErrorTurn(text, obj["is_error"] == true || role == "error")
	if isErr && isTool {
		role = "error"
	}
	collectPathMatches(text, addFile)
	return []SessionTurn{{
		Line:      lineNo,
		Role:      role,
		Text:      text,
		ToolName:  stringVal(obj["tool_name"]),
		IsTool:    isTool,
		IsError:   isErr,
		ErrorKind: condErrorKind(isErr, text),
		Files:     extractFilesFromText(text),
		At:        at,
	}}
}

func extractContentText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, item := range c {
			switch elem := item.(type) {
			case string:
				parts = append(parts, elem)
			case map[string]any:
				if t, ok := elem["text"].(string); ok && t != "" {
					parts = append(parts, t)
				} else if t, ok := elem["content"].(string); ok && t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func extractStringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func stringVal(v any) string {
	s, _ := v.(string)
	return s
}

func normalizeRole(r string) string {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case "user", "human", "prompt":
		return "user"
	case "assistant", "agent", "model", "ai":
		return "assistant"
	case "tool", "tool_use", "tool_result", "function":
		return "tool"
	case "error", "err":
		return "error"
	default:
		return ""
	}
}

func cleanTurnText(s string) string {
	s = xmlTagNoise.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	if len(s) > 1600 {
		s = clipRunes(s, 800)
	}
	return s
}

func isErrorTurn(text string, explicit bool) bool {
	if explicit {
		return true
	}
	return errorPattern.MatchString(text)
}

func isConversationalError(text string, explicit bool) bool {
	if explicit {
		return true
	}
	return strictErrorPattern.MatchString(text)
}

func condErrorKind(isErr bool, text string) string {
	if !isErr {
		return ""
	}
	return classifyErrorKind(text)
}

func collectPathMatches(text string, add func(string)) {
	for _, f := range extractFilesFromText(text) {
		add(f)
	}
}

func extractFilesFromText(text string) []string {
	matches := fileTokenRe.FindAllStringSubmatch(text, 8)
	var out []string
	for _, m := range matches {
		if len(m) > 1 {
			fp := cleanRelPath(m[1])
			if fp != "" && !slices.Contains(out, fp) {
				out = append(out, fp)
			}
		}
	}
	return out
}

func cleanRelPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, `"'`+"`"+`(),:;`)
	if p == "" || strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		return ""
	}
	if strings.HasPrefix(p, "/") {
		parts := strings.Split(filepath.ToSlash(p), "/")
		if len(parts) > 3 {
			return strings.Join(parts[len(parts)-3:], "/")
		}
	}
	return filepath.ToSlash(p)
}

func detectHarness(path string) string {
	low := strings.ToLower(filepath.ToSlash(path))
	switch {
	case strings.Contains(low, ".codex") || strings.Contains(low, "codex"):
		return "codex"
	case strings.Contains(low, ".claude") || strings.Contains(low, "claude"):
		return "claude-code"
	default:
		return "claude-code"
	}
}

func inferDefaultModel(harness string) string {
	if harness == "codex" {
		return "gpt-5-codex"
	}
	return "claude-sonnet-5-5"
}

func inferProject(cwd, filePath string) string {
	if cwd != "" {
		base := filepath.Base(filepath.Clean(cwd))
		if base != "" && base != "." && base != "/" {
			return base
		}
	}
	dir := filepath.Base(filepath.Dir(filePath))
	if strings.HasPrefix(dir, "-") {
		parts := strings.Split(strings.Trim(dir, "-"), "-")
		if len(parts) >= 2 {
			return parts[len(parts)-2] + "-" + parts[len(parts)-1]
		}
	}
	if dir != "" && dir != "." && dir != "projects" && dir != "sessions" {
		return dir
	}
	return "workspace"
}

func firstLineTitle(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "#*- >")
		line = strings.TrimSpace(line)
		if line != "" {
			return clipRunes(line, 72)
		}
	}
	return clipRunes(s, 72)
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func clipRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

// RepoFileRecord represents a scanned file from local repository directories.
type RepoFileRecord struct {
	Path      string
	RelPath   string
	Project   string
	UpdatedAt time.Time
	Lines     []string
}

func scanRepoFiles(dirs []string, pattern, glob string) ([]*SearchHit, int) {
	if len(dirs) == 0 {
		return nil, 0
	}
	var re *regexp.Regexp
	if pattern != "" {
		re, _ = regexp.Compile("(?i)" + pattern)
	}
	terms := splitQueryTerms(pattern)
	scanned := 0
	var hits []*SearchHit

	for _, root := range dirs {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		proj := filepath.Base(filepath.Clean(root))
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "dist" || name == "bin" {
					return filepath.SkipDir
				}
				return nil
			}
			if scanned >= maxScannedRepoFiles {
				return fs.SkipAll
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				rel = d.Name()
			}
			rel = filepath.ToSlash(rel)
			if glob != "" {
				matched, _ := filepath.Match(glob, filepath.Base(rel))
				if !matched && !strings.Contains(strings.ToLower(rel), strings.ToLower(strings.Trim(glob, "*"))) {
					return nil
				}
			}
			ext := strings.ToLower(filepath.Ext(d.Name()))
			if !slices.Contains([]string{".go", ".md", ".json", ".ts", ".tsx", ".js", ".py", ".rs", ".yaml", ".yml", ".sh"}, ext) {
				return nil
			}
			fi, err := d.Info()
			if err != nil || fi.Size() == 0 || fi.Size() > maxRepoFileBytes {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			scanned++
			lines := strings.Split(string(raw), "\n")
			var snippets []EvidenceSnippet
			score := 0.0
			pathMatched := pattern != "" && ((re != nil && re.MatchString(rel)) || (len(terms) > 0 && matchesAllTerms(rel, terms)))
			if pathMatched {
				score += 4.0
			}
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" {
					continue
				}
				matched := false
				if pattern == "" || pathMatched {
					matched = len(snippets) < 3
				} else if re != nil && re.MatchString(line) {
					matched = true
					score += 3.0
				} else if len(terms) > 0 && matchesAllTerms(line, terms) {
					matched = true
					score += 2.5
				}
				if matched && len(snippets) < 4 {
					snippets = append(snippets, EvidenceSnippet{
						Role: "File",
						Line: i + 1,
						Text: clipRunes(trimmed, 180),
					})
				}
			}
			if len(snippets) > 0 {
				hits = append(hits, &SearchHit{
					ID:         "file:" + proj + ":" + rel,
					SourceKind: "file",
					Title:      rel,
					Project:    proj,
					Harness:    "repo",
					Model:      strings.TrimPrefix(ext, "."),
					UpdatedAt:  fi.ModTime().UTC(),
					Score:      score + 1.0,
					Files:      []string{rel},
					Snippets:   snippets,
				})
			}
			return nil
		})
	}
	return hits, scanned
}
