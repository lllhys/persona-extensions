package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cross-computer search (#519). The hub program fans a compiled program out over nodes.call to the program of
// this extension on each computer picked in the `machines` setting (node.go), which runs it over that computer's
// transcripts and answers with the ranked evidence only. Each computer's compact conversational index is synced
// into the hub's data directory, so a computer that is offline is still searched from its last synced index.

const (
	// hubMachineKey names the hub's own computer in routes and machine filters; allKey every computer (or project).
	hubMachineKey    = "hub"
	allKey           = "all"
	localMachineName = "本机（hub）"

	nodeSearchTimeout  = 20 * time.Second
	nodeRestoreTimeout = 6 * time.Minute
	nodeSyncInterval   = 15 * time.Minute
	machinesTTL        = 30 * time.Second

	// The synced index of a computer: its newest sessions, their user / assistant turns only, each clipped.
	maxCachedSessions  = 300
	maxCachedTurns     = 80
	maxCachedTurnRunes = 280
	indexPageSessions  = 60
	maxIndexPages      = 10

	// maxKeyLen keeps a route key a single path segment the assistant's gate remembers as it is: the gate turns a
	// segment with a digit or longer than 24 characters into a wildcard.
	maxKeyLen = 24
)

// Machine statuses of a cross-computer search.
const (
	machineLocal       = "local"       // the hub's own computer
	machineOnline      = "online"      // searched live over nodes.call
	machineCached      = "cached"      // offline or not answering: searched in its synced index
	machineUnavailable = "unavailable" // offline with nothing synced yet
)

// MachineRun is how one computer answered a cross-computer search.
type MachineRun struct {
	ID         string    `json:"id,omitempty"`
	Name       string    `json:"name"`
	Key        string    `json:"key"`
	Status     string    `json:"status"`
	Sessions   int       `json:"scanned_sessions"`
	Hits       int       `json:"hits"`
	DurationMs int64     `json:"duration_ms"`
	SyncedAt   time.Time `json:"synced_at,omitzero"`
	Note       string    `json:"note,omitempty"`
}

// routeKey is a name as one path segment of the assistant's routes: lowercase letters and dashes only. Letters
// stay, a digit d becomes "z" and the d-th letter ("z" itself "zz", so keys of different names differ), anything
// else separates words. A name with other characters (CJK, …) or longer than maxKeyLen gets a short hash of the
// whole name, so two such names keep distinct keys. "all" and "hub" are reserved for every and the hub's computer.
func routeKey(name string) string {
	low := strings.ToLower(strings.TrimSpace(name))
	if low == "" {
		return ""
	}
	var b strings.Builder
	lossy := false
	for _, r := range low {
		switch {
		case r == 'z':
			b.WriteString("zz")
		case r >= 'a' && r <= 'y':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteByte('z')
			b.WriteByte(byte('a' + (r - '0')))
		case r == '-' || r == '_' || r == '.' || r == ' ' || r == '/':
			if s := b.String(); s != "" && !strings.HasSuffix(s, "-") {
				b.WriteByte('-')
			}
		default:
			lossy = true
		}
	}
	k := strings.Trim(b.String(), "-")
	if lossy || len(k) > maxKeyLen || k == "" || k == allKey || k == hubMachineKey {
		head := k[:min(len(k), maxKeyLen-6)]
		head = strings.Trim(head, "-")
		if head == "" {
			return "x" + hashLetters(low, 5)
		}
		return head + "-" + hashLetters(low, 5)
	}
	return k
}

// faithfulKey is a name routeKey keeps as it is: lowercase letters and digits, words joined by single dashes.
var faithfulKey = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// projectKey is a project's route key: routeKey for a name it spells out exactly, and for any other name (dots,
// underscores, spaces, capitals, …, which routeKey folds together: acme.web, acme_web and Acme Web all read
// acme-web) the routeKey followed by a hash of the exact name, so two projects never share a key and an approved
// key names one project. projectsForKey still refuses a key that more than one project of a computer answers to.
func projectKey(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if faithfulKey.MatchString(name) {
		return routeKey(name)
	}
	head := strings.Trim(routeKey(name), "-")
	head = strings.Trim(head[:min(len(head), maxKeyLen-6)], "-")
	if head == "" {
		return "x" + hashLetters("project:"+name, 5)
	}
	return head + "-" + hashLetters("project:"+name, 5)
}

// projectsForKey is the one project of sessions whose key is key, or nil when none or several answer to it (a
// hash collision: the key does not name one project, so it reads none).
func projectsForKey(sessions []*SessionRecord, key string) map[string]bool {
	return projectsForKeyFn(sessions, key, projectKey)
}

func projectsForKeyFn(sessions []*SessionRecord, key string, keyOf func(string) string) map[string]bool {
	names := map[string]bool{}
	for _, s := range sessions {
		if keyOf(s.Project) == key {
			names[s.Project] = true
		}
	}
	if len(names) != 1 {
		return nil
	}
	return names
}

// hashLetters is n letters of a hash of s.
func hashLetters(s string, n int) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	v := h.Sum64()
	out := make([]byte, n)
	for i := range out {
		out[i] = byte('a' + v%26)
		v /= 26
	}
	return string(out)
}

// machineMatches reports whether a machine filter (an id, a name, a route key, or a part of the name of two
// characters or more) names the computer; the hub's own computer has no id and answers to hub, local and 本机.
func machineMatches(filter, id, name string) bool {
	f := strings.ToLower(strings.TrimSpace(filter))
	if f == "" || f == allKey {
		return true
	}
	if id == "" {
		return f == hubMachineKey || f == "local" || f == "本机" || f == strings.ToLower(localMachineName)
	}
	lowName := strings.ToLower(name)
	return f == strings.ToLower(id) || f == routeKey(id) || f == lowName || f == routeKey(name) ||
		(len([]rune(f)) >= 2 && strings.Contains(lowName, f))
}

// machineLabel is how a hit's computer is shown.
func machineLabel(id, name string) string {
	if id == "" {
		return cmp.Or(name, localMachineName)
	}
	return cmp.Or(name, id)
}

// programMachine is the machine filter of a program's scan_sessions steps ("" when none has one).
func programMachine(prog SearchProgram) string {
	for _, s := range prog.Steps {
		if s.Op == "scan_sessions" {
			if m := strings.TrimSpace(s.Args["machine"]); m != "" {
				return m
			}
		}
	}
	return ""
}

// pinProgram pins every scan_sessions step to scope and, when set, projectKey: what an approved route allows,
// whatever the code or the inline tokens of the query say.
//
// The route also names the computers: a machine filter in the code (scan_sessions / where machine=, the machine:
// token) is dropped, since the computers searched are the route's and nothing else. And a route reads session
// history only: scan_files (the hub's repository directories) is dropped too.
func pinProgram(prog SearchProgram, scope, project string) SearchProgram {
	steps := make([]StepSpec, 0, len(prog.Steps))
	for _, s := range prog.Steps {
		if s.Op == "scan_files" {
			continue
		}
		args := map[string]string{}
		for k, v := range s.Args {
			args[k] = v
		}
		if s.Op == "scan_sessions" || s.Op == "where" {
			delete(args, "machine")
		}
		if s.Op == "scan_sessions" {
			args["scope"] = scope
			if project != "" {
				args["project_key"] = project
			} else {
				delete(args, "project_key")
			}
		}
		steps = append(steps, StepSpec{Op: s.Op, Args: args})
	}
	prog.Steps, prog.Scope, prog.CodeListing = steps, scope, formatPipelineCode(steps)
	return prog
}

// nodeCaller calls the program on a computer (the hub's nodes.call).
type nodeCaller interface {
	Call(ctx context.Context, machine, method string, params, out any) error
}

// machineLister lists the hub's computers.
type machineLister interface {
	Machines(ctx context.Context) ([]hubMachine, error)
}

type hubMachine struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Online   bool   `json:"online"`
	Kind     string `json:"kind,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

// target is one computer a search goes to.
type target struct {
	ID, Name, Key string
	Online, Local bool
	// AliasID and AliasName: the hub's own computer as the hub lists it, when it is among the picked ones. It is
	// then searched once, directly (not also over nodes.call), and answers to its id and name as well.
	AliasID, AliasName string
	// AliasKeys: the keys of AliasID and AliasName that no picked computer has.
	AliasKeys []string
}

// fleet is the hub program's view of the computers it searches.
type fleet struct {
	nodes  nodeCaller
	lister machineLister
	dir    string // the hub program's data directory; synced indexes are in <dir>/machines
	now    func() time.Time
	// host is the hub computer's host name: a picked computer of kind local with this host name is the hub's own.
	host string

	mu       sync.Mutex
	selected []string
	known    []hubMachine
	knownAt  time.Time

	cacheMu sync.Mutex
	syncMu  sync.Mutex
}

func newFleet(nodes nodeCaller, lister machineLister, dir string, selected []string) *fleet {
	host, _ := os.Hostname()
	return &fleet{nodes: nodes, lister: lister, dir: dir, now: time.Now, host: host, selected: cleanMachines(selected)}
}

func cleanMachines(in []string) []string {
	var out []string
	for _, m := range in {
		if m = strings.TrimSpace(m); m != "" && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

func (f *fleet) setSelected(ids []string) {
	f.mu.Lock()
	f.selected = cleanMachines(ids)
	f.mu.Unlock()
}

// hasRemote reports whether any computer besides the hub's is picked.
func (f *fleet) hasRemote() bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.selected) > 0
}

// targets are the hub's computer and the picked ones, with names and online state from the hub (kept for
// machinesTTL; the last list stands when the hub cannot be asked), each with a key unique among them.
func (f *fleet) targets(ctx context.Context) []target {
	f.mu.Lock()
	selected := slices.Clone(f.selected)
	stale := f.lister != nil && f.now().Sub(f.knownAt) > machinesTTL
	f.mu.Unlock()
	if stale {
		lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		list, err := f.lister.Machines(lctx)
		cancel()
		f.mu.Lock()
		if err == nil {
			f.known = list
		}
		f.knownAt = f.now()
		f.mu.Unlock()
	}
	f.mu.Lock()
	known := slices.Clone(f.known)
	f.mu.Unlock()

	out := []target{{Name: localMachineName, Key: hubMachineKey, Online: true, Local: true}}
	for _, id := range selected {
		t := target{ID: id, Name: id}
		if i := slices.IndexFunc(known, func(m hubMachine) bool { return m.ID == id }); i >= 0 {
			t.Name, t.Online = cmp.Or(strings.TrimSpace(known[i].Name), id), known[i].Online
			if f.isHubComputer(known[i]) && out[0].AliasID == "" {
				out[0].AliasID, out[0].AliasName = id, t.Name
				out[0].Name = t.Name + "（hub）"
				continue
			}
		}
		out = append(out, t)
	}
	// A key from the name, or from the id when two picked computers share a name; one taken already (by an
	// earlier computer, or hub / all) gets a hash of the full id, so every key names exactly one computer.
	names := map[string]int{}
	for _, t := range out[1:] {
		names[routeKey(t.Name)]++
	}
	used := map[string]bool{hubMachineKey: true, allKey: true}
	for i := range out[1:] {
		t := &out[i+1]
		if t.Key = routeKey(t.Name); names[t.Key] > 1 || t.Key == "" {
			t.Key = routeKey(t.ID)
		}
		t.Key = uniqueKey(t.Key, t.ID, used)
		used[t.Key] = true
	}
	// The hub's computer also answers to its own id and name, unless a picked computer has that key.
	if out[0].AliasID != "" {
		for _, k := range []string{routeKey(out[0].AliasName), routeKey(out[0].AliasID)} {
			if k != "" && !used[k] && !slices.Contains(out[0].AliasKeys, k) {
				out[0].AliasKeys = append(out[0].AliasKeys, k)
			}
		}
	}
	return out
}

// uniqueKey is key when it is free, else the id's key with a hash of the full id (salted further in the unlikely
// case that is taken too).
func uniqueKey(key, id string, used map[string]bool) string {
	if key != "" && !used[key] {
		return key
	}
	head := strings.Trim(routeKey(id), "-")
	head = strings.Trim(head[:min(len(head), maxKeyLen-6)], "-")
	for i := 0; ; i++ {
		salt := "machine:" + id
		if i > 0 {
			salt += fmt.Sprintf("#%d", i)
		}
		k := hashLetters(salt, 5)
		if head != "" {
			k = head + "-" + k
		} else {
			k = "x" + k
		}
		if !used[k] {
			return k
		}
	}
}

// pickedRemote is the picked computer (not the hub's own) whose id is machine, if it is picked now.
func (f *fleet) pickedRemote(ctx context.Context, machine string) (target, bool) {
	if machine == "" {
		return target{}, false
	}
	for _, t := range f.targets(ctx) {
		if !t.Local && t.ID == machine {
			return t, true
		}
	}
	return target{}, false
}

// isHubComputer reports whether the hub lists m as the computer the hub runs on: the local machine of the hub's
// manager, with the hub's host name (a manager run elsewhere has a local machine of its own).
func (f *fleet) isHubComputer(m hubMachine) bool {
	return m.Kind == "local" && f.host != "" && strings.EqualFold(strings.TrimSpace(m.Hostname), f.host)
}

// matches reports whether a machine filter or route key names t.
func (t target) matches(filter string) bool {
	k := strings.ToLower(strings.TrimSpace(filter))
	return t.Key == k || machineMatches(filter, t.ID, t.Name) || slices.Contains(t.AliasKeys, k) ||
		(t.AliasID != "" && machineMatches(filter, t.AliasID, t.AliasName))
}

// resolve is the targets a route's machine segment names: all of them, the hub's computer, or the one whose key
// it is.
func resolveTargets(all []target, key string) ([]target, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "", allKey:
		return all, nil
	}
	// A computer's own key first: an alias of the hub's computer never takes a picked computer's key.
	for _, t := range all {
		if t.Key == key {
			return []target{t}, nil
		}
	}
	for _, t := range all {
		if slices.Contains(t.AliasKeys, key) {
			return []target{t}, nil
		}
	}
	return nil, fmt.Errorf("unknown machine %q: use a key from GET /machines, %s or %s", key, hubMachineKey, allKey)
}

// filterTargets keeps the targets a pipeline's machine filter names (all of them without one).
func filterTargets(all []target, filter string) []target {
	if strings.TrimSpace(filter) == "" {
		return all
	}
	var out []target
	for _, t := range all {
		if t.matches(filter) {
			out = append(out, t)
		}
	}
	return out
}

// nodeSearchParams is sac.search's: the compiled program and the computer's id and name for its hits.
type nodeSearchParams struct {
	Program     SearchProgram `json:"program"`
	Machine     string        `json:"machine"`
	MachineName string        `json:"machine_name"`
}

type part struct {
	t   target
	res *ExecutionResult
	run MachineRun
}

// programLimit is the number of hits a program keeps: its limit step's n (at most maxLimitCap), else maxLimitCap.
func programLimit(prog SearchProgram) int {
	n := maxLimitCap
	for _, s := range prog.Steps {
		if s.Op == "limit" {
			if v, err := strconv.Atoi(s.Args["n"]); err == nil && v > 0 {
				n = min(v, maxLimitCap)
			}
		}
	}
	return n
}

// withLimit is prog keeping at most n hits: every limit step capped at n, and one appended when it has none.
func withLimit(prog SearchProgram, n int) SearchProgram {
	steps := make([]StepSpec, 0, len(prog.Steps)+1)
	has := false
	for _, s := range prog.Steps {
		if s.Op == "limit" {
			has = true
			v, err := strconv.Atoi(s.Args["n"])
			if err != nil || v <= 0 || v > n {
				s = StepSpec{Op: "limit", Args: map[string]string{"n": strconv.Itoa(n)}}
			}
		}
		steps = append(steps, s)
	}
	if !has {
		steps = append(steps, StepSpec{Op: "limit", Args: map[string]string{"n": strconv.Itoa(n)}})
	}
	prog.Steps = steps
	return prog
}

// search runs prog on every target at once and merges the evidence. Each computer runs it with the merged
// result's limit, and what one answers is cut to it as well, so a response, the merged result and the run the
// page keeps stay as large as a single computer's.
func (f *fleet) search(ctx context.Context, st *State, prog SearchProgram, now time.Time, targets []target) *ExecutionResult {
	start := time.Now()
	limit := programLimit(prog)
	sent := withLimit(prog, limit)
	parts := make([]part, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := f.searchOne(ctx, st, sent, now, t)
			if p.res != nil && len(p.res.Hits) > limit {
				p.res.Hits = p.res.Hits[:limit]
			}
			parts[i] = p
		}()
	}
	wg.Wait()
	res := mergeParts(prog, parts, now)
	res.TotalDurationMs = max(time.Since(start).Milliseconds(), 1)
	return res
}

func (f *fleet) searchOne(ctx context.Context, st *State, prog SearchProgram, now time.Time, t target) part {
	start := time.Now()
	p := part{t: t, run: MachineRun{ID: t.ID, Name: t.Name, Key: t.Key}}
	defer func() { p.run.DurationMs = max(time.Since(start).Milliseconds(), 0) }()
	if t.Local {
		if m := programMachine(prog); t.AliasID != "" && m != "" && machineMatches(m, t.AliasID, t.AliasName) {
			prog = retargetMachine(prog, hubMachineKey) // its sessions carry no machine id: name them as the hub's
		}
		p.res, p.run.Status = ExecuteProgram(st, prog, now), machineLocal
		if t.AliasID != "" {
			for _, h := range p.res.Hits {
				h.MachineName = t.Name // shown as the hub lists it; the id stays empty (the hub's own computer)
			}
		}
		return p
	}
	note := "离线"
	if t.Online && f.nodes != nil {
		cctx, cancel := context.WithTimeout(ctx, nodeSearchTimeout)
		var res ExecutionResult
		err := f.nodes.Call(cctx, t.ID, "sac.search", nodeSearchParams{Program: prog, Machine: t.ID, MachineName: t.Name}, &res)
		cancel()
		if err == nil {
			tagHits(res.Hits, t, false)
			p.res, p.run.Status = &res, machineOnline
			return p
		}
		note = "未响应（" + clipRunes(err.Error(), 80) + "）"
	}
	c := f.loadCache(t.ID)
	if c == nil || len(c.Sessions) == 0 {
		p.run.Status, p.run.Note = machineUnavailable, note+"，还没有同步过它的索引"
		return p
	}
	sessions := make([]*SessionRecord, len(c.Sessions))
	for i, s := range c.Sessions {
		cp := *s
		cp.Machine, cp.MachineName = t.ID, t.Name
		sessions[i] = &cp
	}
	res := executeSessions(&State{}, sessions, statsOf(sessions), prog, now)
	tagHits(res.Hits, t, true)
	p.res, p.run.Status, p.run.SyncedAt = res, machineCached, c.SyncedAt
	p.run.Note = note + "，检索已同步的索引"
	return p
}

// retargetMachine is prog with every machine filter (scan_sessions and where) set to machine.
func retargetMachine(prog SearchProgram, machine string) SearchProgram {
	steps := make([]StepSpec, len(prog.Steps))
	for i, s := range prog.Steps {
		args := map[string]string{}
		for k, v := range s.Args {
			args[k] = v
		}
		if _, ok := args["machine"]; ok && (s.Op == "scan_sessions" || s.Op == "where") {
			args["machine"] = machine
		}
		steps[i] = StepSpec{Op: s.Op, Args: args}
	}
	prog.Steps = steps
	return prog
}

// tagHits names the computer on each hit and makes the ids of its sessions unique across computers.
func tagHits(hits []*SearchHit, t target, cached bool) {
	for _, h := range hits {
		h.Machine, h.MachineName, h.Cached = t.ID, t.Name, cached
		if t.ID != "" && !strings.HasPrefix(h.ID, "m:") {
			h.ID = remoteHitID(t.ID, h.ID)
			for i := range h.Correlated {
				if h.Correlated[i].Kind == "session" {
					h.Correlated[i].ID = remoteHitID(t.ID, h.Correlated[i].ID)
				}
			}
		}
	}
}

func remoteHitID(machine, id string) string { return "m:" + machine + "/" + id }

// splitHitID is the computer and the computer's own id of a hit id ("" for the hub's computer).
func splitHitID(id string) (machine, local string) {
	if rest, ok := strings.CutPrefix(id, "m:"); ok {
		if m, l, ok := strings.Cut(rest, "/"); ok {
			return m, l
		}
	}
	return "", id
}

// mergeParts merges the computers' results: the evidence ranked by score, each session once (a session both on
// the hub's computer and a picked one shows under the picked one), and the step traces summed over computers.
func mergeParts(prog SearchProgram, parts []part, now time.Time) *ExecutionResult {
	out := &ExecutionResult{Program: prog, RanAt: now.UTC()}
	slices.SortStableFunc(parts, func(a, b part) int {
		return cmp.Compare(boolInt(a.t.Local), boolInt(b.t.Local))
	})
	seen := map[string]bool{}
	hits := []*SearchHit{}
	live, local, cached, missing := 0, 0, 0, 0
	var traces []StepTrace
	for i := range parts {
		p := &parts[i]
		if p.res != nil {
			r := p.res
			p.run.Sessions, p.run.Hits = r.ScannedSessions, len(r.Hits)
			out.ScannedSessions += r.ScannedSessions
			out.ScannedArchived += r.ScannedArchived
			out.ScannedUnconfirmed += r.ScannedUnconfirmed
			out.ArchivedOmitted += r.ArchivedOmitted
			out.ScannedFiles += r.ScannedFiles
			for _, h := range r.Hits {
				key := h.ID
				if h.SourceKind == "session" && h.SessionID != "" {
					key = "session:" + h.SessionID
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				hits = append(hits, h)
			}
			if traces == nil {
				traces = slices.Clone(r.StepTraces)
			} else {
				for j := range min(len(traces), len(r.StepTraces)) {
					traces[j].InputCount += r.StepTraces[j].InputCount
					traces[j].OutputCount += r.StepTraces[j].OutputCount
					traces[j].DurationMs = max(traces[j].DurationMs, r.StepTraces[j].DurationMs)
					traces[j].DurationUs = max(traces[j].DurationUs, r.StepTraces[j].DurationUs)
				}
			}
		}
		switch p.run.Status {
		case machineOnline:
			live++
		case machineLocal:
			local++
		case machineCached:
			cached++
		case machineUnavailable:
			missing++
		}
		out.Machines = append(out.Machines, p.run)
	}
	for j := range traces {
		if traces[j].Op == "scan_sessions" {
			traces[j].Note = fmt.Sprintf("在 %d 台电脑上扫描 %d 个会话", len(parts), out.ScannedSessions)
		}
	}

	slices.SortStableFunc(hits, func(a, b *SearchHit) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	field := "project"
	for _, s := range prog.Steps {
		if s.Op == "group_by" {
			field = cmp.Or(s.Args["field"], "project")
		}
	}
	out.Groups = evalGroupBy(hits, field)
	limit := maxLimitCap
	for _, s := range prog.Steps {
		if s.Op == "limit" {
			if n, err := strconv.Atoi(s.Args["n"]); err == nil && n > 0 {
				limit = n
			}
		}
	}
	merged := len(hits)
	hits = hits[:min(len(hits), limit)]
	out.Hits, out.TotalMatches = hits, len(hits)

	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = p.t.Key
	}
	fan := StepTrace{
		Op:          "fan_out",
		Code:        fmt.Sprintf("fan_out(machines=%q)", strings.Join(names, ", ")),
		InputCount:  len(parts),
		OutputCount: live + local + cached,
		DurationUs:  1,
		Note:        fanOutNote(local, live, cached, missing),
	}
	merge := StepTrace{
		Op:          "merge",
		Code:        `merge(by="score")`,
		InputCount:  merged,
		OutputCount: len(hits),
		DurationUs:  1,
		Note:        "合并各电脑的证据，按分数重排并截取",
	}
	out.StepTraces = append(append([]StepTrace{fan}, traces...), merge)
	for i := range out.StepTraces {
		out.StepTraces[i].Index = i + 1
	}
	return out
}

// fanOutNote says how each computer was searched.
func fanOutNote(local, live, cached, missing int) string {
	var parts []string
	if local > 0 {
		parts = append(parts, "hub 本机直接检索")
	}
	if live > 0 {
		parts = append(parts, fmt.Sprintf("%d 台经 nodes.call 实时检索", live))
	}
	if cached > 0 {
		parts = append(parts, fmt.Sprintf("%d 台离线、检索已同步的索引", cached))
	}
	if missing > 0 {
		parts = append(parts, fmt.Sprintf("%d 台不可用（离线且没有同步过索引）", missing))
	}
	return strings.Join(parts, "，")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// statsOf counts sessions as loadSessions does.
func statsOf(sessions []*SessionRecord) SourceStats {
	st := SourceStats{ByHarness: map[string]int{}, ByModel: map[string]int{}, ByProject: map[string]int{}}
	for _, rec := range sessions {
		st.TotalSessions++
		st.TotalTurns += len(rec.Turns)
		if rec.ErrorCount > 0 {
			st.ErrorSessions++
		}
		st.ByHarness[rec.Harness]++
		if rec.Model != "" {
			st.ByModel[rec.Model]++
		}
		if rec.Project != "" {
			st.ByProject[rec.Project]++
		}
		if rec.Archived {
			st.ArchivedSessions++
			if rec.Unconfirmed {
				st.UnconfirmedSessions++
			}
		}
	}
	return st
}

// machineCache is a computer's synced conversational index, in <data>/machines/<hash of the id>.json.
type machineCache struct {
	MachineID string           `json:"machine_id"`
	Name      string           `json:"name,omitempty"`
	SyncedAt  time.Time        `json:"synced_at"`
	Cursor    indexCursor      `json:"cursor"`
	Sessions  []*SessionRecord `json:"sessions"`
}

func (f *fleet) cachePath(machine string) string {
	sum := sha256.Sum256([]byte(machine))
	return filepath.Join(f.dir, "machines", hex.EncodeToString(sum[:8])+".json")
}

func (f *fleet) loadCache(machine string) *machineCache {
	if f.dir == "" || machine == "" {
		return nil
	}
	f.cacheMu.Lock()
	defer f.cacheMu.Unlock()
	raw, err := os.ReadFile(f.cachePath(machine))
	if err != nil {
		return nil
	}
	var c machineCache
	if json.Unmarshal(raw, &c) != nil || c.MachineID != machine {
		return nil
	}
	return &c
}

func (f *fleet) saveCache(c *machineCache) error {
	if f.dir == "" {
		return errors.New("no data directory")
	}
	f.cacheMu.Lock()
	defer f.cacheMu.Unlock()
	path := f.cachePath(c.MachineID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// syncAll brings the synced index of every picked computer that is online up to date (one sync at a time).
func (f *fleet) syncAll(ctx context.Context) {
	if !f.hasRemote() {
		return
	}
	f.syncMu.Lock()
	defer f.syncMu.Unlock()
	for _, t := range f.targets(ctx) {
		if t.Local || !t.Online {
			continue
		}
		_ = f.syncOne(ctx, t)
	}
}

// syncOne fetches the sessions changed on a computer since the last sync, page by page, and keeps the newest
// maxCachedSessions.
func (f *fleet) syncOne(ctx context.Context, t target) error {
	if f.nodes == nil {
		return errors.New("no node calls")
	}
	c := f.loadCache(t.ID)
	if c == nil {
		c = &machineCache{MachineID: t.ID}
	}
	byID := map[string]int{}
	for i, s := range c.Sessions {
		byID[s.SessionID] = i
	}
	cursor := c.Cursor
	for range maxIndexPages {
		cctx, cancel := context.WithTimeout(ctx, nodeSearchTimeout)
		var page indexPage
		err := f.nodes.Call(cctx, t.ID, "sac.index", indexParams{After: cursor, Limit: indexPageSessions}, &page)
		cancel()
		if err != nil {
			return err
		}
		for _, s := range page.Sessions {
			if s == nil || s.SessionID == "" {
				continue
			}
			if i, ok := byID[s.SessionID]; ok {
				c.Sessions[i] = s
			} else {
				byID[s.SessionID] = len(c.Sessions)
				c.Sessions = append(c.Sessions, s)
			}
		}
		if page.Cursor.after(cursor) {
			cursor = page.Cursor
		}
		if !page.More {
			break
		}
	}
	slices.SortFunc(c.Sessions, func(a, b *SessionRecord) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	c.Sessions = c.Sessions[:min(len(c.Sessions), maxCachedSessions)]
	c.Cursor, c.SyncedAt, c.Name = cursor, f.now().UTC(), t.Name
	return f.saveCache(c)
}

// syncLoop syncs once shortly after start (5 s) and then once every nodeSyncInterval, until stop closes.
func (f *fleet) syncLoop(stop chan struct{}) {
	first := time.NewTimer(5 * time.Second)
	defer first.Stop()
	tick := time.NewTicker(nodeSyncInterval)
	defer tick.Stop()
	f.runSyncLoop(stop, first.C, tick.C)
}

// runSyncLoop syncs once when first fires and once per tick of every: the start sync is a one-time timer, and each
// interval syncs exactly once (syncOne only fetches what changed since the cursor).
func (f *fleet) runSyncLoop(stop chan struct{}, first, every <-chan time.Time) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-stop
		cancel()
	}()
	for {
		select {
		case <-stop:
			return
		case <-first:
			first = nil // a timer fires once; never select it again
		case <-every:
		}
		f.syncAll(ctx)
	}
}

// machineStatus is a computer as the page and GET /machines show it.
type machineStatus struct {
	Key            string    `json:"key"`
	ID             string    `json:"id,omitempty"`
	Name           string    `json:"name"`
	Online         bool      `json:"online"`
	Local          bool      `json:"local,omitempty"`
	CachedSessions int       `json:"cached_sessions,omitempty"`
	SyncedAt       time.Time `json:"synced_at,omitzero"`
}

func (f *fleet) statuses(ctx context.Context) []machineStatus {
	var out []machineStatus
	for _, t := range f.targets(ctx) {
		s := machineStatus{Key: t.Key, ID: t.ID, Name: t.Name, Online: t.Online, Local: t.Local}
		if c := f.loadCache(t.ID); c != nil {
			s.CachedSessions, s.SyncedAt = len(c.Sessions), c.SyncedAt
		}
		out = append(out, s)
	}
	return out
}

// cachedSession is a session of a computer's synced index.
func (f *fleet) cachedSession(machine, sessionID string) *SessionRecord {
	c := f.loadCache(machine)
	if c == nil {
		return nil
	}
	for _, s := range c.Sessions {
		if s.SessionID == sessionID {
			cp := *s
			cp.Machine, cp.MachineName = machine, c.Name
			return &cp
		}
	}
	return nil
}

// restore restores an archived session on the computer that holds it.
//
// Only a computer picked now (not the hub's own, restored directly) is asked: a page action or route naming one
// that was removed from the setting since is refused. The computer itself restores only an entry of its own
// indexes (restoreArchived there), so an entry of another computer is refused by it.
func (f *fleet) restore(ctx context.Context, machine, entryID string) (*RestoreResult, error) {
	if f.nodes == nil {
		return nil, errors.New("no node calls")
	}
	if _, ok := f.pickedRemote(ctx, machine); !ok {
		return nil, fmt.Errorf("%s 不在要检索的电脑里", machine)
	}
	cctx, cancel := context.WithTimeout(ctx, nodeRestoreTimeout)
	defer cancel()
	var out RestoreResult
	if err := f.nodes.Call(cctx, machine, "sac.restore", map[string]string{"entry_id": entryID}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// projectInfo is a project as GET /projects/{machine} lists it.
type projectInfo struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Sessions int    `json:"sessions"`
}

func projectList(byProject map[string]int) []projectInfo {
	out := make([]projectInfo, 0, len(byProject))
	for name, n := range byProject {
		out = append(out, projectInfo{Key: projectKey(name), Name: name, Sessions: n})
	}
	slices.SortFunc(out, func(a, b projectInfo) int {
		if c := cmp.Compare(b.Sessions, a.Sessions); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}

// projects lists a computer's projects: asked live when it is online, else from its synced index.
func (f *fleet) projects(ctx context.Context, t target) ([]projectInfo, error) {
	if t.Online && f.nodes != nil {
		cctx, cancel := context.WithTimeout(ctx, nodeSearchTimeout)
		var out struct {
			Projects []projectInfo `json:"projects"`
		}
		err := f.nodes.Call(cctx, t.ID, "sac.projects", map[string]any{}, &out)
		cancel()
		if err == nil {
			return out.Projects, nil
		}
	}
	c := f.loadCache(t.ID)
	if c == nil {
		return nil, fmt.Errorf("%s is offline and has no synced index", t.Name)
	}
	return projectList(statsOf(c.Sessions).ByProject), nil
}
