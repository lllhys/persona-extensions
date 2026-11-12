package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

// Sessions that the disklessly CLI moved to the cloud leave a plain-text index on this computer:
// <stateDir>/sessions/text/<entryId>.jsonl, one header line followed by one line per user or assistant message.
// The index is never uploaded and stays after a restore; a restored session is recognized by its original path
// existing again, and its live transcript is then scanned instead.

const (
	locationLocal       = "本地"
	locationArchived    = "已上云"
	locationUnconfirmed = "未确认上云"

	coldIndexType          = "disklessly-archive"
	maxColdLineBytes       = 1 << 20
	maxColdTurnRunes       = 4000
	coldTextSubdir         = "sessions/text"
	disklesslyLegacyFolder = "256isenough"
)

// Bounds of a scan of the text indexes (vars for tests): how many archived (not restored) indexes it parses,
// newest first; how many bytes of one index it parses (a longer index is cut, not dropped, like a live transcript);
// and how many index bytes it reads in all.
var (
	maxScannedColdFiles = 1000
	maxColdReadBytes    = int64(maxSessionFileBytes)
	maxColdTotalBytes   = int64(96 << 20)
)

var validEntryID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// disklesslyStateDirFor is the state directory the disklessly CLI uses without --state-dir and
// DISKLESSLY_STATE_DIR when it resolves home (the platform default from its Engine.environment).
func disklesslyStateDirFor(home string) string {
	if runtime.GOOS == "darwin" {
		support := filepath.Join(home, "Library", "Application Support")
		current, legacy := filepath.Join(support, "Disklessly"), filepath.Join(support, disklesslyLegacyFolder)
		if !exists(current) && exists(legacy) {
			return legacy
		}
		return current
	}
	return filepath.Join(xdgDataHome(home), "disklessly")
}

// passwdHome is the account's home directory from the user database. The disklessly CLI resolves its
// default state directory from it (NSHomeDirectory), not from $HOME. A var so tests can stub it.
var passwdHome = func() string {
	if u, err := user.Current(); err == nil {
		return u.HomeDir
	}
	return ""
}

// homeDirs returns $HOME and the passwd home, cleaned and deduplicated, in that order.
func homeDirs() []string {
	var out []string
	h, _ := os.UserHomeDir()
	for _, d := range []string{h, passwdHome()} {
		if d = strings.TrimSpace(d); d != "" && !slices.Contains(out, filepath.Clean(d)) {
			out = append(out, filepath.Clean(d))
		}
	}
	return out
}

// isDisklesslyDefaultStateDir reports whether dir is the CLI's default state directory whichever home
// ($HOME or the passwd home) the CLI resolves it from, so --state-dir can be left out.
func isDisklesslyDefaultStateDir(dir string) bool {
	dir = filepath.Clean(dir)
	if d := strings.TrimSpace(os.Getenv("DISKLESSLY_STATE_DIR")); d != "" {
		return dir == filepath.Clean(d)
	}
	homes := homeDirs()
	if len(homes) == 0 {
		return false
	}
	for _, h := range homes {
		if disklesslyStateDirFor(h) != dir {
			return false
		}
	}
	return true
}

func xdgDataHome(home string) string {
	if d := os.Getenv("XDG_DATA_HOME"); strings.HasPrefix(d, "/") {
		return filepath.Clean(d)
	}
	return filepath.Join(home, ".local", "share")
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// defaultColdTextDirs returns the existing disklessly text-index directories on this computer.
// Overridden in tests so tests never read the real state directory.
var defaultColdTextDirs = func() []string {
	return existingDirs(coldTextDirCandidates(os.Getenv("DISKLESSLY_STATE_DIR"), homeDirs()))
}

// coldTextDirCandidates lists the possible text-index directories, deduplicated: DISKLESSLY_STATE_DIR,
// then for each home ($HOME, and the passwd home the CLI itself uses on Linux) the XDG data dir
// (plus the plain ~/.local/share when XDG_DATA_HOME points elsewhere) and the macOS support folders.
func coldTextDirCandidates(stateEnv string, homes []string) []string {
	var cands []string
	add := func(p string) {
		if p = filepath.Clean(p); !slices.Contains(cands, p) {
			cands = append(cands, p)
		}
	}
	if d := strings.TrimSpace(stateEnv); d != "" {
		add(filepath.Join(d, coldTextSubdir))
	}
	for _, home := range homes {
		if home == "" {
			continue
		}
		support := filepath.Join(home, "Library", "Application Support")
		add(filepath.Join(xdgDataHome(home), "disklessly", coldTextSubdir))
		add(filepath.Join(home, ".local", "share", "disklessly", coldTextSubdir))
		add(filepath.Join(support, "Disklessly", coldTextSubdir))
		add(filepath.Join(support, disklesslyLegacyFolder, coldTextSubdir))
	}
	return cands
}

func existingDirs(cands []string) []string {
	var out []string
	for _, c := range cands {
		c = filepath.Clean(c)
		if fi, err := os.Stat(c); err == nil && fi.IsDir() && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

func activeColdTextDirs(st *State) []string {
	var out []string
	add := func(d string) {
		if d = strings.TrimSpace(d); d != "" {
			d = filepath.Clean(d)
			if !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
	}
	for _, d := range defaultColdTextDirs() {
		add(d)
	}
	if st != nil {
		for _, d := range st.ColdTextDirs {
			add(d)
		}
	}
	return out
}

// stateDirOfTextDir maps <stateDir>/sessions/text back to <stateDir>.
func stateDirOfTextDir(dir string) string {
	dir = filepath.Clean(dir)
	if filepath.Base(dir) == "text" && filepath.Base(filepath.Dir(dir)) == "sessions" {
		return filepath.Dir(filepath.Dir(dir))
	}
	return ""
}

type coldHeader struct {
	Type        string `json:"type"`
	Version     int    `json:"version"`
	EntryID     string `json:"entryId"`
	DeviceID    string `json:"deviceId"`
	SessionID   string `json:"sessionId"`
	Tool        string `json:"tool"`
	Path        string `json:"path"`
	CWD         string `json:"cwd"`
	Title       string `json:"title"`
	FirstPrompt string `json:"firstPrompt"`
	Mtime       string `json:"mtime"`
	ArchivedAt  string `json:"archivedAt"`
	Bytes       int64  `json:"bytes"`
	Segment     string `json:"segment"`
}

type coldLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
}

func harnessOfTool(tool string) string {
	switch t := strings.ToLower(strings.TrimSpace(tool)); t {
	case "claude", "claude-code", "claude_code":
		return "claude-code"
	case "":
		return "agent"
	default:
		return t
	}
}

func parseRFC3339(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(s)); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// loadColdSessions parses the text indexes of archived sessions, newest first, skipping duplicate entries and
// restored sessions (original path exists again), whose original paths it returns separately (newest first).
// Only archived indexes count toward maxScannedColdFiles; omitted is how many archived indexes it left out.
func loadColdSessions(st *State) (archived []*SessionRecord, restored []string, dirs []string, omitted int) {
	dirs = activeColdTextDirs(st)
	type coldFile struct {
		fileEntry
		dir  string
		size int64
	}
	var files []coldFile
	for _, dir := range dirs {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			fi, err := e.Info()
			if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
				continue
			}
			files = append(files, coldFile{fileEntry{filepath.Join(dir, e.Name()), fi.ModTime()}, dir, fi.Size()})
		}
	}
	slices.SortFunc(files, func(a, b coldFile) int { return b.mod.Compare(a.mod) })

	seenEntry := map[string]bool{}
	var budget int64
	for _, f := range files {
		h, entryID, ok := readColdHeader(f.path)
		if !ok || seenEntry[entryID] {
			continue
		}
		seenEntry[entryID] = true
		if orig := headerPath(h); orig != "" && exists(orig) {
			// Restored: the live transcript is scanned instead (a header read, no body parse).
			if !slices.Contains(restored, orig) {
				restored = append(restored, orig)
			}
			continue
		}
		if len(archived) >= maxScannedColdFiles || budget >= maxColdTotalBytes {
			omitted++
			continue
		}
		budget += min(f.size, maxColdReadBytes)
		if rec := parseColdFile(f.path, f.dir, f.mod); rec != nil && len(rec.Turns) > 0 {
			archived = append(archived, rec)
		}
	}
	return archived, restored, dirs, omitted
}

// readColdHeader reads and checks the first line of a text index.
func readColdHeader(path string) (coldHeader, string, bool) {
	var h coldHeader
	f, err := os.Open(path)
	if err != nil {
		return h, "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 16<<10), maxColdLineBytes)
	if !sc.Scan() {
		return h, "", false
	}
	return checkColdHeader(sc.Bytes(), path)
}

func checkColdHeader(line []byte, path string) (coldHeader, string, bool) {
	var h coldHeader
	if json.Unmarshal(line, &h) != nil || h.Type != coldIndexType || h.Version != 1 {
		return h, "", false
	}
	stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	entryID := cmp.Or(strings.TrimSpace(h.EntryID), stem)
	if !validEntryID.MatchString(entryID) {
		return h, "", false
	}
	return h, entryID, true
}

// headerPath is the absolute original transcript path of an index, or "".
func headerPath(h coldHeader) string {
	if orig := strings.TrimSpace(h.Path); orig != "" && filepath.IsAbs(orig) {
		return filepath.Clean(orig)
	}
	return ""
}

func parseColdFile(path, textDir string, mod time.Time) *SessionRecord {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	// A long index is cut at maxColdReadBytes (its last, partial line fails to parse and is skipped).
	sc := bufio.NewScanner(io.LimitReader(f, maxColdReadBytes))
	sc.Buffer(make([]byte, 0, 64<<10), maxColdLineBytes)
	if !sc.Scan() {
		return nil
	}
	h, entryID, ok := checkColdHeader(sc.Bytes(), path)
	if !ok {
		return nil
	}
	orig := headerPath(h)
	rec := &SessionRecord{
		SessionID:  cmp.Or(strings.TrimSpace(h.SessionID), entryID),
		Title:      strings.TrimSpace(h.Title),
		CWD:        strings.TrimSpace(h.CWD),
		Harness:    harnessOfTool(h.Tool),
		FilePath:   orig,
		UpdatedAt:  cmp.Or(parseRFC3339(h.Mtime), mod.UTC()),
		Archived:   true,
		EntryID:    entryID,
		TextPath:   path,
		StateDir:   stateDirOfTextDir(textDir),
		ArchivedAt: parseRFC3339(h.ArchivedAt),
		// The CLI writes the index before the upload with an empty segment and archive time and fills them in
		// once the segment is stored; an index still without them has no confirmed cloud copy.
		Unconfirmed: strings.TrimSpace(h.Segment) == "" || strings.TrimSpace(h.ArchivedAt) == "",
	}
	var fileSet []string
	lineNo := 1
	for sc.Scan() {
		lineNo++
		var ln coldLine
		if json.Unmarshal(sc.Bytes(), &ln) != nil || (ln.Type != "user" && ln.Type != "assistant") {
			continue
		}
		text := strings.TrimSpace(ln.Text)
		if text == "" {
			continue
		}
		text = clipRunes(text, maxColdTurnRunes)
		files := extractFilesFromText(text)
		for _, fp := range files {
			if !slices.Contains(fileSet, fp) && len(fileSet) < 24 {
				fileSet = append(fileSet, fp)
			}
		}
		isErr := isConversationalError(text, false)
		rec.Turns = append(rec.Turns, SessionTurn{
			Line:      lineNo,
			Role:      ln.Type,
			Text:      text,
			IsError:   isErr,
			ErrorKind: condErrorKind(isErr, text),
			Files:     files,
			At:        parseRFC3339(ln.Timestamp),
		})
		rec.TurnCount++
		if isErr {
			rec.ErrorCount++
		}
	}
	rec.Files = fileSet
	rec.Project = inferProject(rec.CWD, orig)
	// The text index carries no model; leave it empty rather than show a guess.
	if rec.Title == "" {
		rec.Title = firstLineTitle(strings.TrimSpace(h.FirstPrompt))
	}
	if rec.Title == "" {
		for _, tr := range rec.Turns {
			if tr.Role == "user" {
				rec.Title = firstLineTitle(tr.Text)
				break
			}
		}
	}
	if rec.Title == "" {
		rec.Title = "会话 " + shortID(rec.SessionID)
	}
	return rec
}

// findArchivedSession looks an entry up in this computer's text indexes by its file name, so an entry is found
// however many indexes there are. A restored session (original path exists again) is not found.
func findArchivedSession(st *State, entryID string) *SessionRecord {
	if !validEntryID.MatchString(entryID) {
		return nil
	}
	for _, dir := range activeColdTextDirs(st) {
		path := filepath.Join(dir, entryID+".jsonl")
		fi, err := os.Stat(path)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		rec := parseColdFile(path, dir, fi.ModTime())
		if rec == nil || rec.EntryID != entryID || (rec.FilePath != "" && exists(rec.FilePath)) {
			continue
		}
		return rec
	}
	return nil
}

func sessionLocation(s *SessionRecord) string {
	switch {
	case !s.Archived:
		return locationLocal
	case s.Unconfirmed:
		return locationUnconfirmed
	default:
		return locationArchived
	}
}

// resumeCommand is how the user continues a restored session in its tool; "" for a tool without a known command.
func resumeCommand(harness, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	switch harness {
	case "claude-code":
		return "claude --resume " + sessionID
	case "codex":
		return "codex resume " + sessionID
	default:
		return ""
	}
}

// coldNote describes the archived sessions of a scan, e.g. "（含 3 个已上云）".
func coldNote(archived, unconfirmed, omitted int) string {
	if archived <= 0 && omitted <= 0 {
		return ""
	}
	var parts []string
	if archived > 0 {
		parts = append(parts, fmt.Sprintf("含 %d 个%s", archived, locationArchived))
	}
	if unconfirmed > 0 {
		parts = append(parts, fmt.Sprintf("其中 %d 个%s", unconfirmed, locationUnconfirmed))
	}
	if omitted > 0 {
		parts = append(parts, fmt.Sprintf("另有 %d 个较旧的%s会话超出上限未扫描", omitted, locationArchived))
	}
	return "（" + strings.Join(parts, "，") + "）"
}
