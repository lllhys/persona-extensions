package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The cold index is a small local copy of each machine's disklessly session index: enough to find
// and restore an archived conversation (title, first prompt, project, a few keywords) and to say
// where it is, without opening the backend. The digests stay on the machine; only keywords
// extracted from them are kept here.

const (
	maxColdEntries  = 5000
	coldTitleRunes  = 120
	coldPromptRunes = 240
	coldKeywords    = 16
	// coldKeywordMaxRunes drops long words (hashes, pasted tokens) from the keywords.
	coldKeywordMaxRunes = 24
	coldCheckFreshness  = 24 * time.Hour
)

// Where an indexed session is, as far as the last check knows. Unknown is never treated as absent.
const (
	LocCloud   = "在云端"
	LocMissing = "云端缺失"
	LocLocal   = "本地"
	LocUnknown = "未知"
)

// ColdBackend is the backup backend as last checked from a machine.
type ColdBackend struct {
	ID        string    `json:"id,omitempty"`
	Type      string    `json:"type,omitempty"`
	Reachable *bool     `json:"reachable,omitempty"` // nil: unknown
	LatencyMs int64     `json:"latency_ms,omitempty"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
}

// ColdEntry is one session of a machine's disklessly index.
type ColdEntry struct {
	ID          string   `json:"id"`
	SessionID   string   `json:"session_id,omitempty"`
	Tool        string   `json:"tool,omitempty"`
	Path        string   `json:"path,omitempty"`
	Cwd         string   `json:"cwd,omitempty"`
	Project     string   `json:"project,omitempty"`
	Month       string   `json:"month,omitempty"`
	Mtime       string   `json:"mtime,omitempty"`
	ArchivedAt  string   `json:"archived_at,omitempty"`
	Bytes       uint64   `json:"bytes,omitempty"`
	State       string   `json:"state,omitempty"`
	Title       string   `json:"title,omitempty"`
	FirstPrompt string   `json:"first_prompt,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	Segment     string   `json:"segment,omitempty"`
	Local       bool     `json:"local,omitempty"`
	// DeviceID is kept only for another device's row (Local false), to show and dedupe it.
	DeviceID string `json:"device_id,omitempty"`
	// KeywordsPending marks a row whose digest could not be fetched yet.
	KeywordsPending bool `json:"kw_pending,omitempty"`
}

// ColdCatalog is the local lightweight index of one machine.
type ColdCatalog struct {
	MachineID     string      `json:"machine_id"`
	DeviceID      string      `json:"device_id,omitempty"`
	Backend       ColdBackend `json:"backend"`
	Missing       []string    `json:"missing,omitempty"`
	Unchecked     []string    `json:"unchecked,omitempty"`
	TimedOut      bool        `json:"timed_out,omitempty"`
	Entries       []ColdEntry `json:"entries,omitempty"`
	Total         int         `json:"total"`
	Capped        bool        `json:"capped,omitempty"`
	ArchivedBytes uint64      `json:"archived_bytes,omitempty"`
	RefreshedAt   time.Time   `json:"refreshed_at,omitzero"`
	Error         string      `json:"error,omitempty"`

	checked map[string]bool // segments of archived/remote rows, which --check-segments stats
}

// coldListScript lists the newest maxColdEntries rows of the index, with their digests when
// digest is set, and with status checks the backend and its segments. Digests (up to 8 KiB a
// row) are fetched only when the index has rows the catalog has no keywords for yet.
func coldListScript(digest, status bool) string {
	flags := " --limit " + strconv.Itoa(maxColdEntries)
	if digest {
		flags += " --digest"
	}
	s := resolveCLIScript + `
if [ -z "$DKL" ]; then
  echo "@@COLD_NOCLI"
  exit 0
fi
echo "@@SECTION:COLD_LIST"
"$DKL" --json sessions list --state all` + flags + ` 2>/dev/null | tr '\n' ' '; echo
`
	if status {
		s += `echo "@@SECTION:COLD_STATUS"
"$DKL" --json sessions status --check-segments 2>/dev/null | tr '\n' ' '; echo
`
	}
	return s + "echo \"@@SECTION:END\"\n"
}

type cliListEntry struct {
	CloudEntry
	Month       string `json:"month"`
	FirstPrompt string `json:"firstPrompt"`
	Segment     string `json:"segment"`
	Digest      string `json:"digest"`
}

type cliList struct {
	DeviceID string         `json:"deviceId"`
	Entries  []cliListEntry `json:"entries"`
}

// coldScanRefreshAge is how old a catalog may get before a scan refreshes it; an explicit refresh
// and an offload always do.
const coldScanRefreshAge = 6 * time.Hour

// refreshColdIfStale refreshes machine m's cold index when it is missing or older than maxAge.
func refreshColdIfStale(ctx context.Context, runner Runner, st *State, m *Machine, now time.Time, maxAge time.Duration) {
	if cat := st.cold(m.ID); cat != nil && !cat.RefreshedAt.IsZero() && now.Sub(cat.RefreshedAt) >= 0 && now.Sub(cat.RefreshedAt) < maxAge && cat.Error == "" {
		return
	}
	_, _ = RefreshColdIndex(ctx, runner, st, m, now)
}

// RefreshColdIndex re-reads machine m's session index and backend state into st.Cold[m.ID].
// A failed refresh keeps the entries already known and marks the backend unknown.
func RefreshColdIndex(ctx context.Context, runner Runner, st *State, m *Machine, now time.Time) (*ColdCatalog, error) {
	if runner == nil {
		runner = DefaultRunner
	}
	cat := st.cold(m.ID)
	if cat == nil {
		cat = &ColdCatalog{MachineID: m.ID}
		st.setCold(m.ID, cat)
	}
	fail := func(err error) (*ColdCatalog, error) {
		cat.Backend.Reachable = nil
		cat.Error = err.Error()
		return cat, err
	}
	out, err := runner(ctx, m, coldListScript(false, true), 3*time.Minute)
	if err != nil {
		return fail(err)
	}
	if strings.Contains(out, "@@COLD_NOCLI") {
		return fail(errors.New("目标机器未安装 disklessly CLI，无法读取云端会话索引"))
	}
	sections := splitSections(out)
	var list cliList
	if !parseCLIJSONLine(sections["COLD_LIST"], &list) {
		return fail(errors.New("disklessly sessions list 没有返回可解析的结果"))
	}
	var status CloudStatus
	statusOK := parseCLIJSONLine(sections["COLD_STATUS"], &status)

	// Keywords of rows already in the catalog are kept (a row's digest never changes); digests
	// are fetched only for new rows.
	known := map[string][]string{}
	for _, e := range cat.Entries {
		if !e.KeywordsPending {
			known[e.ID] = e.Keywords
		}
	}
	keywordsOf := map[string][]string{}
	pending := false
	for _, e := range list.Entries {
		if _, ok := known[e.ID]; !ok {
			pending = true
			break
		}
	}
	if pending {
		var full cliList
		dout, derr := runner(ctx, m, coldListScript(true, false), 3*time.Minute)
		if derr == nil && parseCLIJSONLine(splitSections(dout)["COLD_LIST"], &full) {
			for _, e := range full.Entries {
				keywordsOf[e.ID] = extractKeywords(e.Digest, coldKeywords)
			}
			pending = false
		}
	}

	entries := make([]ColdEntry, 0, min(len(list.Entries), maxColdEntries))
	for _, e := range list.Entries {
		ce := coldEntryFrom(e)
		if kw, ok := known[e.ID]; ok {
			ce.Keywords = kw
		} else if kw, ok := keywordsOf[e.ID]; ok {
			ce.Keywords = kw
		} else if ce.Keywords == nil {
			ce.KeywordsPending = pending
		}
		entries = append(entries, ce)
	}
	slices.SortStableFunc(entries, func(a, b ColdEntry) int { return entryTime(b).Compare(entryTime(a)) })
	total := len(entries)
	if statusOK {
		n := 0
		for _, c := range status.Counts {
			n += c
		}
		total = max(total, n)
	}
	if len(entries) > maxColdEntries {
		entries = entries[:maxColdEntries]
	}
	cat.Total = total
	cat.Capped = total > len(entries)
	cat.Entries = entries
	cat.checked = nil
	cat.DeviceID = cmp.Or(list.DeviceID, status.DeviceID, cat.DeviceID)
	cat.RefreshedAt = now
	cat.Error = ""
	if statusOK {
		reachable := status.Backend.Reachable
		cat.Backend = ColdBackend{
			ID: status.Backend.ID, Type: status.Backend.Type, Reachable: &reachable,
			LatencyMs: status.Backend.LatencyMs, Error: status.Backend.Error, CheckedAt: now,
		}
		cat.Missing = status.Segments.Missing
		cat.Unchecked = status.Segments.Unchecked
		cat.TimedOut = status.Segments.TimedOut
		cat.ArchivedBytes = status.ArchivedBytes
	} else {
		cat.Backend.Reachable = nil
		cat.Error = "disklessly sessions status 没有返回可解析的结果，云端状态未知"
	}
	return cat, nil
}

func splitSections(out string) map[string][]string {
	sections := map[string][]string{}
	cur := ""
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(line, "@@SECTION:") {
			cur = strings.TrimSpace(strings.TrimPrefix(line, "@@SECTION:"))
			sections[cur] = nil
		} else if cur != "" {
			sections[cur] = append(sections[cur], line)
		}
	}
	return sections
}

func coldEntryFrom(e cliListEntry) ColdEntry {
	ce := ColdEntry{
		ID: e.ID, SessionID: e.SessionID, Tool: e.Tool, Path: e.Path, Cwd: e.Cwd, Project: e.Project,
		Month: e.Month, Mtime: e.Mtime, ArchivedAt: e.ArchivedAt, Bytes: e.Bytes, State: e.State,
		Title:       clipRunes(oneLine(e.Title), coldTitleRunes),
		FirstPrompt: clipRunes(oneLine(e.FirstPrompt), coldPromptRunes),
		Keywords:    extractKeywords(e.Digest, coldKeywords),
		Segment:     e.Segment, Local: e.Local,
	}
	if !e.Local {
		ce.DeviceID = e.DeviceID
	}
	return ce
}

func entryTime(e ColdEntry) time.Time {
	for _, s := range []string{e.Mtime, e.ArchivedAt} {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

var keywordStopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`the and for with this that you your are was were but not have has had from can will
		what when where which who how all any use used using user assistant please should would could there their then than
		into also just like make need want let get got its it's out our one two now new see yes okay sure here some more
		such only them they these those been being does did done don't doesn't via per about after before because
		while each other same very well into over under again still even much many most may might must shall
		file files line lines code run running test tests error errors true false null nil none return returns
		http https www com org json jsonl txt tmp src lib bin usr home users`) {
		m[w] = true
	}
	return m
}()

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

func isLatinWord(r rune) bool {
	return r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) || unicode.In(r, unicode.Latin)
}

// hexLike reports a word of 7 or more hex digits with at least one digit, such as a commit hash.
func hexLike(w string) bool {
	if len(w) < 7 || strings.IndexFunc(w, unicode.IsDigit) < 0 {
		return false
	}
	return strings.TrimLeft(w, "0123456789abcdef") == ""
}

// extractKeywords picks up to n frequent words of text: lowercase Latin / digit words of 3
// to 24 runes that are not hex-like, stopwords or plain numbers, and CJK runs clipped to 2 to 6 runes.
func extractKeywords(text string, n int) []string {
	if text == "" || n <= 0 {
		return nil
	}
	count := map[string]int{}
	first := map[string]int{}
	order := 0
	add := func(w string) {
		if _, ok := first[w]; !ok {
			first[w] = order
			order++
		}
		count[w]++
	}
	var word []rune
	kind := 0 // 1 latin, 2 cjk
	flush := func() {
		switch kind {
		case 1:
			w := strings.ToLower(string(word))
			if n := utf8.RuneCountInString(w); n >= 3 && n <= coldKeywordMaxRunes && !keywordStopwords[w] &&
				strings.TrimFunc(w, unicode.IsDigit) != "" && !hexLike(w) {
				add(w)
			}
		case 2:
			if len(word) >= 2 {
				add(string(word[:min(len(word), 6)]))
			}
		}
		word, kind = word[:0], 0
	}
	for _, r := range text {
		k := 0
		switch {
		case isCJK(r):
			k = 2
		case isLatinWord(r):
			k = 1
		}
		if k != kind {
			flush()
		}
		if k != 0 {
			word = append(word, r)
			kind = k
		}
	}
	flush()
	words := make([]string, 0, len(count))
	for w := range count {
		words = append(words, w)
	}
	slices.SortFunc(words, func(a, b string) int {
		return cmp.Or(cmp.Compare(count[b], count[a]), cmp.Compare(first[a], first[b]))
	})
	return words[:min(len(words), n)]
}

// location says where entry e of machine m is, by the last check of its catalog. An entry
// without a known segment cannot be confirmed in the cloud, so it is unknown.
func (c *ColdCatalog) location(e ColdEntry, m *Machine, now time.Time) string {
	switch e.State {
	case "restored", "keptLocal":
		return LocLocal
	}
	if c == nil {
		return LocUnknown
	}
	// Reachability and staleness decide first: a Missing list left from an older check, or kept
	// after a failed or unreachable refresh, must not report a segment as missing.
	b := c.Backend
	fresh := !b.CheckedAt.IsZero() && now.Sub(b.CheckedAt) < coldCheckFreshness && now.Sub(b.CheckedAt) > -time.Hour
	if m == nil || !m.Reachable || b.Reachable == nil || !*b.Reachable || !fresh || e.Segment == "" {
		return LocUnknown
	}
	if slices.Contains(c.Missing, e.Segment) {
		return LocMissing
	}
	switch e.State {
	case "archived", "remote", "pendingDelete":
	default:
		return LocUnknown
	}
	if slices.Contains(c.Unchecked, e.Segment) {
		return LocUnknown
	}
	// --check-segments stats only the segments of archived and remote rows: a pendingDelete
	// row is confirmed only when its segment also holds one of those.
	if e.State == "pendingDelete" && !c.checkedSegments()[e.Segment] {
		return LocUnknown
	}
	return LocCloud
}

func (c *ColdCatalog) checkedSegments() map[string]bool {
	if c.checked == nil {
		c.checked = map[string]bool{}
		for _, e := range c.Entries {
			if e.Segment != "" && (e.State == "archived" || e.State == "remote") {
				c.checked[e.Segment] = true
			}
		}
	}
	return c.checked
}

// ColdHit is a session found in the cold index (or by a deep search on the machine).
type ColdHit struct {
	ColdEntry
	MachineID   string `json:"machine_id"`
	MachineName string `json:"machine_name"`
	Location    string `json:"location"`
	Preview     string `json:"preview,omitempty"`
	Deep        bool   `json:"deep,omitempty"`
}

func toolLabel(tool string) string {
	switch tool {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	}
	return tool
}

// SearchCold finds sessions in the local cold index: every whitespace-separated token must occur
// (case-insensitive; CJK as a substring) in the title, first prompt, keywords, cwd, project, path,
// session id, tool or machine name. An empty query lists everything. Newest first.
func SearchCold(st *State, query, machine string, limit int, now time.Time) []ColdHit {
	tokens := strings.Fields(strings.ToLower(query))
	var only *Machine
	if strings.TrimSpace(machine) != "" {
		if only = st.machine(machine); only == nil {
			return nil
		}
	}
	// Machines sharing a backend list each other's rows (state remote) after a reindex; such a
	// row is shown only under its own machine when that machine is managed here.
	ownDevice := map[string]bool{}
	for _, m := range st.Machines {
		if cat := st.cold(m.ID); cat != nil && cat.DeviceID != "" {
			ownDevice[cat.DeviceID] = true
		}
	}
	var hits []ColdHit
	for _, m := range st.Machines {
		if only != nil && m.ID != only.ID {
			continue
		}
		if only == nil && !m.Enabled {
			continue
		}
		cat := st.cold(m.ID)
		if cat == nil {
			continue
		}
		name := cmp.Or(m.Name, m.Host)
		for _, e := range cat.Entries {
			if !e.Local && e.DeviceID != "" && ownDevice[e.DeviceID] {
				continue
			}
			if len(tokens) > 0 {
				hay := strings.ToLower(strings.Join([]string{e.Title, e.FirstPrompt, strings.Join(e.Keywords, " "),
					e.Cwd, e.Project, e.Path, e.SessionID, e.Tool, toolLabel(e.Tool), name, m.ID}, "\n"))
				if !allIn(hay, tokens) {
					continue
				}
			}
			hits = append(hits, ColdHit{ColdEntry: e, MachineID: m.ID, MachineName: name, Location: cat.location(e, m, now)})
		}
	}
	sortHits(hits)
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func allIn(hay string, tokens []string) bool {
	for _, t := range tokens {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

func sortHits(hits []ColdHit) {
	slices.SortStableFunc(hits, func(a, b ColdHit) int { return entryTime(b.ColdEntry).Compare(entryTime(a.ColdEntry)) })
}

// deepSearchWords splits a query into the words passed to `disklessly sessions search`. The CLI
// takes any word starting with "-" for an option, so leading dashes are dropped (its full-text
// tokenizer ignores them anyway, e.g. a Claude project key "-home-dev-app" still matches).
func deepSearchWords(query string) []string {
	var words []string
	for _, w := range strings.Fields(query) {
		if w = strings.TrimLeft(w, "-"); w != "" {
			words = append(words, w)
		}
	}
	return words
}

// deepSearchScript runs the CLI's full-text search (titles, previews and digests) on the machine.
func deepSearchScript(words []string, limit int) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = shellQuote(w)
	}
	return resolveCLIScript + `
if [ -z "$DKL" ]; then echo "@@COLD_NOCLI"; exit 0; fi
echo "@@SECTION:SEARCH"
"$DKL" --json sessions search ` + strings.Join(q, " ") + ` --limit ` + strconv.Itoa(limit) + ` 2>/dev/null | tr '\n' ' '; echo
echo "@@SECTION:END"
`
}

// parseDeepSearch reads the CLI's hit array, or turns its {"ok":false} error into an error.
func parseDeepSearch(raw string) ([]cliSearchHit, error) {
	raw = strings.TrimSpace(raw)
	var found []cliSearchHit
	if json.Unmarshal([]byte(raw), &found) == nil {
		return found, nil
	}
	var fail struct {
		Error *cliError `json:"error"`
	}
	if json.Unmarshal([]byte(raw), &fail) == nil && fail.Error != nil && fail.Error.Message != "" {
		return nil, errors.New("全文搜索失败：" + fail.Error.Message)
	}
	return nil, errors.New("全文搜索没有返回可解析的结果")
}

type cliSearchHit struct {
	ID        string `json:"id"`
	Tool      string `json:"tool"`
	Path      string `json:"path"`
	Preview   string `json:"preview"`
	Title     string `json:"title"`
	DeviceID  string `json:"deviceId"`
	State     string `json:"state"`
	Mtime     string `json:"mtime"`
	Bytes     uint64 `json:"bytes"`
	Cwd       string `json:"cwd"`
	SessionID string `json:"sessionId"`
}

// DeepSearchCold runs `disklessly sessions search` on each machine (all enabled ones when
// machine is empty) and merges the hits with the local index by entry id, newest first.
// Machines that cannot be searched are reported in errs; their local hits remain.
func DeepSearchCold(ctx context.Context, runner Runner, st *State, query, machine string, limit int, now time.Time) (hits []ColdHit, errs []string) {
	if runner == nil {
		runner = DefaultRunner
	}
	limit = cmp.Or(limit, 50)
	local := SearchCold(st, query, machine, 0, now)
	byKey := map[string]int{}
	for i, h := range local {
		byKey[h.MachineID+"/"+h.ID] = i
	}
	hits = local
	words := deepSearchWords(query)
	if len(words) == 0 {
		sortHits(hits)
		return hits[:min(len(hits), limit)], []string{"全文搜索需要至少一个查询词"}
	}
	for _, m := range st.Machines {
		if machine != "" {
			if mm := st.machine(machine); mm == nil || mm.ID != m.ID {
				continue
			}
		} else if !m.Enabled {
			continue
		}
		out, err := runner(ctx, m, deepSearchScript(words, limit), 90*time.Second)
		if err == nil && strings.Contains(out, "@@COLD_NOCLI") {
			err = errors.New("未安装 disklessly CLI")
		}
		var found []cliSearchHit
		if err == nil {
			found, err = parseDeepSearch(strings.Join(splitSections(out)["SEARCH"], " "))
		}
		if err != nil {
			errs = append(errs, cmp.Or(m.Name, m.ID)+"："+err.Error())
			continue
		}
		cat := st.cold(m.ID)
		for _, f := range found {
			key := m.ID + "/" + f.ID
			if i, ok := byKey[key]; ok {
				hits[i].Preview = clipRunes(oneLine(f.Preview), coldPromptRunes)
				hits[i].Deep = true
				continue
			}
			e := ColdEntry{ID: f.ID, SessionID: f.SessionID, Tool: f.Tool, Path: f.Path, Cwd: f.Cwd, Mtime: f.Mtime,
				Bytes: f.Bytes, State: f.State, Title: clipRunes(oneLine(f.Title), coldTitleRunes), Local: true}
			if cat != nil && cat.DeviceID != "" && f.DeviceID != "" && f.DeviceID != cat.DeviceID {
				e.Local, e.DeviceID = false, f.DeviceID
			}
			if cat != nil {
				if i := slices.IndexFunc(cat.Entries, func(x ColdEntry) bool { return x.ID == f.ID }); i >= 0 {
					e = cat.Entries[i]
				}
			}
			byKey[key] = len(hits)
			hits = append(hits, ColdHit{ColdEntry: e, MachineID: m.ID, MachineName: cmp.Or(m.Name, m.Host),
				Location: cat.location(e, m, now), Preview: clipRunes(oneLine(f.Preview), coldPromptRunes), Deep: true})
		}
	}
	sortHits(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, errs
}

// resumeHint is the command that resumes a restored session.
// It is empty for agents without a known resume-by-id command (pi, Gemini, Qwen, …).
func resumeHint(e ColdEntry) string {
	id := cmp.Or(e.SessionID, e.ID)
	switch cmp.Or(e.Tool, toolFromPath(e.Path)) {
	case "codex":
		return "codex resume " + id
	case "claude":
		return "claude --resume " + id
	}
	return ""
}

func toolFromPath(p string) string {
	switch {
	case strings.Contains(p, "/.codex/"):
		return "codex"
	case strings.Contains(p, "/.claude/"):
		return "claude"
	}
	return ""
}

func restoreScript(e ColdEntry) string {
	var b strings.Builder
	b.WriteString(resolveCLIScript)
	b.WriteString("\nif [ -z \"$DKL\" ]; then echo \"@@RESTORE_RC " + strconv.Itoa(cliExitMissing) + "\"; exit 0; fi\n")
	b.WriteString(`OUT=$("$DKL" --json sessions restore ` + shellQuote(e.ID) + ` 2>/dev/null)
RC=$?
echo "@@RESTORE_RC $RC"
printf '@@RESTORE_JSON %s\n' "$(printf '%s' "$OUT" | tr '\n' ' ')"
`)
	if e.Path != "" {
		b.WriteString(presentCheck(e.Path))
	}
	return b.String()
}

func presentCheck(p string) string {
	return `if [ -s ` + shellQuote(p) + ` ]; then echo "@@RESTORE_PRESENT"; fi
`
}

// RestoreCold restores entry entryID of machine m from the backup backend to its original path,
// checks that the transcript is back, marks it local and records it in the history. An id the
// catalog does not hold (a full-text hit beyond the cap) is restored by id all the same. Another
// device's row is refused: its original path belongs to that device.
func RestoreCold(ctx context.Context, runner Runner, st *State, m *Machine, entryID string, now time.Time) (*ColdEntry, string, error) {
	if runner == nil {
		runner = DefaultRunner
	}
	if m == nil {
		return nil, "", errors.New("找不到目标机器")
	}
	entryID = strings.TrimSpace(entryID)
	if entryID == "" || strings.HasPrefix(entryID, "-") || strings.ContainsAny(entryID, "\r\n\x00") {
		return nil, "", fmt.Errorf("无效的会话 ID %q", entryID)
	}
	cat := st.cold(m.ID)
	if cat == nil {
		cat = &ColdCatalog{MachineID: m.ID}
		st.setCold(m.ID, cat)
	}
	idx := slices.IndexFunc(cat.Entries, func(e ColdEntry) bool { return e.ID == entryID || e.SessionID == entryID })
	e := ColdEntry{ID: entryID, Local: true, State: "archived"}
	if idx >= 0 {
		e = cat.Entries[idx]
	}
	if !e.Local {
		return nil, "", fmt.Errorf("会话 %q 来自另一台设备（%s），原路径属于那台设备；请在原设备上恢复，或在终端运行 disklessly sessions restore %s --to <本机路径>",
			cmp.Or(e.Title, e.ID), cmp.Or(e.DeviceID, "未知设备"), e.ID)
	}
	out, err := runner(ctx, m, restoreScript(e), 10*time.Minute)
	if err != nil {
		return nil, "", err
	}
	rc, present := -1, false
	var resp struct {
		OK        bool      `json:"ok"`
		Path      string    `json:"path"`
		ID        string    `json:"id"`
		SessionID string    `json:"sessionId"`
		Error     *cliError `json:"error"`
	}
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.HasPrefix(line, "@@RESTORE_RC "):
			rc, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "@@RESTORE_RC ")))
		case strings.HasPrefix(line, "@@RESTORE_JSON "):
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "@@RESTORE_JSON ")), &resp)
		case line == "@@RESTORE_PRESENT":
			present = true
		}
	}
	msg := ""
	if resp.Error != nil {
		msg = resp.Error.Message
	}
	switch {
	case rc == -1:
		return nil, "", errors.New("恢复脚本没有返回结果")
	case rc == cliExitMissing:
		return nil, "", errors.New("目标机器未安装 disklessly CLI，无法恢复会话")
	case rc == cliExitBackend:
		return nil, "", fmt.Errorf("备份后端不可达，无法恢复会话：%s", cmpOr(msg, "backend unreachable"))
	case rc != 0 || !resp.OK:
		return nil, "", fmt.Errorf("disklessly sessions restore 失败 (exit %d)：%s", rc, cmpOr(msg, "unknown error"))
	}
	if resp.Path != "" && resp.Path != e.Path {
		// The CLI restored somewhere the catalog did not know: check that path.
		check, err := runner(ctx, m, presentCheck(resp.Path), 30*time.Second)
		present = err == nil && strings.Contains(check, "@@RESTORE_PRESENT")
	}
	if !present {
		return nil, "", fmt.Errorf("disklessly 报告已恢复，但 %s 不存在或为空", cmp.Or(resp.Path, e.Path, e.ID))
	}
	e.State = "restored"
	e.ID = cmp.Or(resp.ID, e.ID)
	e.Path = cmp.Or(resp.Path, e.Path)
	e.SessionID = cmp.Or(resp.SessionID, e.SessionID)
	if e.Tool == "" {
		e.Tool = toolFromPath(e.Path)
	}
	if idx >= 0 {
		cat.Entries[idx] = e
	} else {
		cat.Entries = slices.Insert(cat.Entries, 0, e)
	}
	hint := resumeHint(e)
	note := "已从云端恢复到原路径 " + e.Path
	if hint != "" {
		note += "，可运行 " + hint
	}
	rec := &CleanRecord{
		ID:          fmt.Sprintf("restore-%s-%d", m.ID, now.UnixNano()/1e6),
		MachineID:   m.ID,
		MachineName: cmp.Or(m.Name, m.Host),
		Host:        m.Host,
		Categories:  []string{"session-restore"},
		Items: []CleanItemResult{{ID: e.ID, Category: CatColdAgentSession, Title: cmp.Or(e.Title, e.SessionID, e.ID), Path: e.Path,
			ScannedBytes: e.Bytes, Note: note}},
		Summary:   "恢复会话 " + clipRunes(cmp.Or(e.Title, e.SessionID, e.ID), 40) + " → " + cmp.Or(hint, e.Path),
		CreatedAt: now,
	}
	st.History = slices.Insert(st.History, 0, rec)
	if len(st.History) > maxHistoryItems {
		st.History = st.History[:maxHistoryItems]
	}
	return &e, hint, nil
}

// OffloadSessions archives machine m's cold sessions through the disklessly CLI (dry run: the
// plan only), then refreshes the machine's cold index.
func OffloadSessions(ctx context.Context, runner Runner, st *State, m *Machine, dryRun bool, excludes []string, now time.Time) (*CleanPlan, *CleanRecord, error) {
	if m == nil {
		return nil, nil, errors.New("找不到目标机器")
	}
	scan, err := ScanMachine(ctx, runner, st, m, now)
	if err != nil {
		return nil, nil, err
	}
	if !dryRun && !scan.Sessions.CLIReady {
		return nil, nil, errors.New(cmpOr(scan.Sessions.Note, "目标机器没有可用的 disklessly CLI"))
	}
	if !dryRun && !slices.ContainsFunc(scan.Items, func(it ScanItem) bool { return it.Category == CatColdAgentSession }) {
		if note := scan.Sessions.Note; note != "" {
			return nil, nil, errors.New(note)
		}
		return nil, nil, errors.New("当前没有可上云的冷会话（7 天外、未被引用的会话）")
	}
	return ExecuteClean(ctx, runner, st, m, []string{CatColdAgentSession}, nil, false, dryRun, excludes, now)
}
