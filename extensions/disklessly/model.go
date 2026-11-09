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
	"strconv"
	"strings"
	"time"
)

// Categories of reclaimable items and coding-agent sessions.
const (
	CatGoCache          = "go-cache"
	CatNodePythonCache  = "node-python-cache"
	CatDockerUnused     = "docker-unused"
	CatRustXcodeBuild   = "rust-xcode-build"
	CatTmpLogs          = "tmp-logs"
	CatColdAgentSession = "cold-agent-sessions"
)

// CategoryCategories are all known category IDs in display order.
var CategoryIDs = []string{
	CatGoCache,
	CatNodePythonCache,
	CatDockerUnused,
	CatRustXcodeBuild,
	CatTmpLogs,
	CatColdAgentSession,
}

var CategoryLabels = map[string]string{
	CatGoCache:          "Go 构建与模块缓存",
	CatNodePythonCache:  "Node / Python 包缓存",
	CatDockerUnused:     "Docker 悬空镜像与 Buildx",
	CatRustXcodeBuild:   "Rust / Xcode / 编译器缓存",
	CatTmpLogs:          "系统日志与陈旧临时产物",
	CatColdAgentSession: "Agent 冷会话日志 (>7d)",
}

// Health levels for disk watermark.
const (
	HealthOK       = "ok"
	HealthWarning  = "warning"  // >= 80% used or free < 25 GiB (when total >= 50 GiB)
	HealthCritical = "critical" // >= 90% used or free < 10 GiB (when total >= 20 GiB)
	HealthOffline  = "offline"
)

// Safety levels for cleanup items.
const (
	SafetySafe   = "safe"
	SafetyReview = "review"
)

const (
	gib             = 1024 * 1024 * 1024
	mib             = 1024 * 1024
	maxMachines     = 64
	maxHistoryItems = 200
)

// State is the persisted extension state in disklessly_state.json.
type State struct {
	Machines []*Machine       `json:"machines"`
	Scans    map[string]*Scan `json:"scans,omitempty"`
	History  []*CleanRecord   `json:"history,omitempty"`
}

// Machine represents a local or SSH remote machine managed by the extension.
type Machine struct {
	ID            string    `json:"id"`
	Host          string    `json:"host"`
	Name          string    `json:"name"`
	User          string    `json:"user,omitempty"`
	Port          int       `json:"port,omitempty"`
	IdentityFile  string    `json:"identity_file,omitempty"`
	IsLocal       bool      `json:"is_local"`
	Source        string    `json:"source"` // "local", "persona", "ssh-config", "manual"
	Enabled       bool      `json:"enabled"`
	Reachable     bool      `json:"reachable"`
	Platform      string    `json:"platform,omitempty"`
	BinaryVersion string    `json:"binary_version,omitempty"`
	ScanMode      string    `json:"scan_mode,omitempty"` // "disklessly-cli" or "builtin-sh"
	LastError     string    `json:"last_error,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitzero"`
}

// MountInfo describes the root/data filesystem usage on a machine.
type MountInfo struct {
	Device         string `json:"device"`
	MountPoint     string `json:"mount_point"`
	FSType         string `json:"fs_type"`
	TotalBytes     uint64 `json:"total_bytes"`
	UsedBytes      uint64 `json:"used_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}

// ScanItem is one discovered cache, log, build artifact, or cold session group.
type ScanItem struct {
	ID         string `json:"id"`
	Category   string `json:"category"`
	Title      string `json:"title"`
	Path       string `json:"path"`
	Bytes      uint64 `json:"bytes"`
	Safety     string `json:"safety"` // "safe" or "review"
	Action     string `json:"action"` // "clean" or "archive"
	Reason     string `json:"reason"`
	InUse      bool   `json:"in_use,omitempty"`
	CleanCmd   string `json:"clean_cmd,omitempty"`
	SessionCnt int    `json:"session_cnt,omitempty"`
}

// AgentToolSessions holds hot/cold session counts and bytes for one coding agent tool.
type AgentToolSessions struct {
	Tool          string `json:"tool"`  // "claude" or "codex"
	Label         string `json:"label"` // "Claude Code" or "Codex"
	Directory     string `json:"directory"`
	HotCount      int    `json:"hot_count"`
	HotBytes      uint64 `json:"hot_bytes"`
	ColdCount     int    `json:"cold_count"`
	ColdBytes     uint64 `json:"cold_bytes"`
	ArchivedCount int    `json:"archived_count"`
	ArchivedBytes uint64 `json:"archived_bytes"`
}

// SessionSummary aggregates coding-agent session tiering on a machine.
type SessionSummary struct {
	HotCount      int                 `json:"hot_count"`
	HotBytes      uint64              `json:"hot_bytes"`
	ColdCount     int                 `json:"cold_count"`
	ColdBytes     uint64              `json:"cold_bytes"`
	ArchivedCount int                 `json:"archived_count"`
	ArchivedBytes uint64              `json:"archived_bytes"`
	ArchiveDir    string              `json:"archive_dir,omitempty"`
	Tools         []AgentToolSessions `json:"tools,omitempty"`
}

// Scan is the cached scan report and dry-run plan for a machine.
type Scan struct {
	MachineID        string         `json:"machine_id"`
	Host             string         `json:"host"`
	Hostname         string         `json:"hostname"`
	RemoteHome       string         `json:"remote_home,omitempty"`
	Platform         string         `json:"platform,omitempty"`
	BinaryVersion    string         `json:"binary_version,omitempty"`
	ScanMode         string         `json:"scan_mode"`
	Mount            MountInfo      `json:"mount"`
	Items            []ScanItem     `json:"items"`
	Sessions         SessionSummary `json:"sessions"`
	ReclaimableBytes uint64         `json:"reclaimable_bytes"`
	SafeBytes        uint64         `json:"safe_bytes"`
	ReviewBytes      uint64         `json:"review_bytes"`
	ScannedAt        time.Time      `json:"scanned_at"`
}

// CleanItemResult records the outcome of cleaning or archiving a single ScanItem.
type CleanItemResult struct {
	ID           string   `json:"id"`
	Category     string   `json:"category"`
	Title        string   `json:"title"`
	Path         string   `json:"path"`
	Safety       string   `json:"safety"`
	ScannedBytes uint64   `json:"scanned_bytes"`
	FreedBytes   uint64   `json:"freed_bytes"`
	SkippedInUse []string `json:"skipped_in_use,omitempty"`
	Note         string   `json:"note,omitempty"`
}

// CleanRecord is an entry in the cleanup history ledger.
type CleanRecord struct {
	ID          string            `json:"id"`
	MachineID   string            `json:"machine_id"`
	MachineName string            `json:"machine_name"`
	Host        string            `json:"host"`
	Categories  []string          `json:"categories"`
	BeforeAvail uint64            `json:"before_avail"`
	AfterAvail  uint64            `json:"after_avail"`
	PlannedFree uint64            `json:"planned_free"`
	FreedBytes  uint64            `json:"freed_bytes"`
	Items       []CleanItemResult `json:"items"`
	Summary     string            `json:"summary"`
	CreatedAt   time.Time         `json:"created_at"`
}

// MachineStatus is the computed view of a machine with its latest scan and cleanup stats.
type MachineStatus struct {
	Machine          *Machine       `json:"machine"`
	Level            string         `json:"level"`
	UsedPercent      float64        `json:"used_percent"`
	TotalBytes       uint64         `json:"total_bytes"`
	UsedBytes        uint64         `json:"used_bytes"`
	AvailableBytes   uint64         `json:"available_bytes"`
	ReclaimableBytes uint64         `json:"reclaimable_bytes"`
	SafeBytes        uint64         `json:"safe_bytes"`
	ReviewBytes      uint64         `json:"review_bytes"`
	Sessions         SessionSummary `json:"sessions"`
	LastScannedAt    *time.Time     `json:"last_scanned_at,omitempty"`
	LastCleanedAt    *time.Time     `json:"last_cleaned_at,omitempty"`
	LastFreedBytes   uint64         `json:"last_freed_bytes"`
	TotalFreedBytes  uint64         `json:"total_freed_bytes"`
	CleanCount       int            `json:"clean_count"`
}

// EvaluateHealth computes the health level from reachability and root disk usage.
// Critical: >= 90% used or (total >= 20 GiB and free < 10 GiB).
// Warning:  >= 80% used or (total >= 50 GiB and free < 25 GiB).
func EvaluateHealth(reachable bool, totalBytes, availableBytes uint64) string {
	if !reachable {
		return HealthOffline
	}
	if totalBytes == 0 {
		return HealthOK
	}
	var usedBytes uint64
	if totalBytes >= availableBytes {
		usedBytes = totalBytes - availableBytes
	}
	usedPct := float64(usedBytes) / float64(totalBytes)
	if usedPct >= 0.90 || (totalBytes >= 20*gib && availableBytes < 10*gib) {
		return HealthCritical
	}
	if usedPct >= 0.80 || (totalBytes >= 50*gib && availableBytes < 25*gib) {
		return HealthWarning
	}
	return HealthOK
}

func healthSortRank(level string) int {
	switch level {
	case HealthOffline:
		return 0
	case HealthCritical:
		return 1
	case HealthWarning:
		return 2
	default:
		return 3
	}
}

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9._@\-\[\]:]+$`)

// IsConcreteHost checks that a host alias or address is safe and not a wildcard or flag.
func IsConcreteHost(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 128 {
		return false
	}
	if strings.HasPrefix(token, "-") {
		return false
	}
	if strings.ContainsAny(token, "*?! \t\r\n'\"`;|&$<>\\") {
		return false
	}
	return hostPattern.MatchString(token)
}

func sanitizeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	res := strings.Trim(b.String(), "-.")
	if res == "" {
		return "machine"
	}
	if len(res) > 64 {
		res = res[:64]
	}
	return res
}

func isSelfHostAlias(h string) bool {
	h = strings.ToLower(strings.TrimSpace(h))
	if h == "local" || h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "[::1]" {
		return true
	}
	if hn, err := os.Hostname(); err == nil && hn != "" {
		hn = strings.ToLower(strings.TrimSpace(hn))
		if h == hn {
			return true
		}
		if short, _, ok := strings.Cut(hn, "."); ok && h == short {
			return true
		}
	}
	return false
}

// ensureDefaults guarantees that the State maps/slices are initialized and the local machine is present.
func (st *State) ensureDefaults(now time.Time) {
	if st.Machines == nil {
		st.Machines = []*Machine{}
	}
	if st.Scans == nil {
		st.Scans = map[string]*Scan{}
	}
	if st.History == nil {
		st.History = []*CleanRecord{}
	}
	hasLocal := false
	for _, m := range st.Machines {
		if m.ID == "local" || m.IsLocal {
			m.ID = "local"
			m.IsLocal = true
			if m.Host == "" {
				m.Host = "localhost"
			}
			if m.Name == "" {
				m.Name = defaultLocalName()
			}
			m.Reachable = true
			hasLocal = true
			break
		}
	}
	if !hasLocal {
		local := &Machine{
			ID:        "local",
			Host:      "localhost",
			Name:      defaultLocalName(),
			IsLocal:   true,
			Source:    "local",
			Enabled:   true,
			Reachable: true,
			UpdatedAt: now,
		}
		st.Machines = append([]*Machine{local}, st.Machines...)
	}
}

func defaultLocalName() string {
	if hn, err := os.Hostname(); err == nil && strings.TrimSpace(hn) != "" {
		return "local (" + strings.TrimSpace(hn) + ")"
	}
	return "local (本机)"
}

func (st *State) machine(idOrHost string) *Machine {
	idOrHost = strings.TrimSpace(idOrHost)
	if idOrHost == "" {
		if len(st.Machines) > 0 {
			return st.Machines[0]
		}
		return nil
	}
	for _, m := range st.Machines {
		if m.ID == idOrHost || strings.EqualFold(m.Host, idOrHost) || strings.EqualFold(m.Name, idOrHost) {
			return m
		}
	}
	if isSelfHostAlias(idOrHost) {
		for _, m := range st.Machines {
			if m.IsLocal {
				return m
			}
		}
	}
	return nil
}

type MachineInput struct {
	ID           string
	Host         string
	Name         string
	User         string
	Port         int
	IdentityFile string
	Source       string
	Enabled      *bool
}

func (st *State) upsertMachine(in MachineInput, now time.Time) (*Machine, error) {
	st.ensureDefaults(now)
	host := strings.TrimSpace(in.Host)
	id := strings.TrimSpace(in.ID)
	if id == "local" || isSelfHostAlias(host) {
		m := st.machine("local")
		if name := strings.TrimSpace(in.Name); name != "" {
			m.Name = name
		}
		if in.Enabled != nil {
			m.Enabled = *in.Enabled
		}
		m.UpdatedAt = now
		return m, nil
	}
	if host == "" && id != "" {
		if existing := st.machine(id); existing != nil {
			host = existing.Host
		} else {
			host = id
		}
	}
	if !IsConcreteHost(host) {
		return nil, fmt.Errorf("无效的机器地址或 SSH Host 别名：%q", host)
	}
	if id == "" {
		id = sanitizeID(host)
	}
	if !IsConcreteHost(id) {
		return nil, fmt.Errorf("无效的机器 ID：%q", id)
	}

	var existing *Machine
	for _, m := range st.Machines {
		if m.ID == id || (!m.IsLocal && strings.EqualFold(m.Host, host)) {
			existing = m
			break
		}
	}
	if existing != nil {
		existing.Host = host
		if name := strings.TrimSpace(in.Name); name != "" {
			existing.Name = name
		}
		if user := strings.TrimSpace(in.User); user != "" {
			existing.User = user
		}
		if in.Port > 0 {
			existing.Port = in.Port
		}
		if idf := strings.TrimSpace(in.IdentityFile); idf != "" {
			existing.IdentityFile = idf
		}
		if in.Enabled != nil {
			existing.Enabled = *in.Enabled
		} else {
			existing.Enabled = true
		}
		if in.Source != "" && existing.Source == "" {
			existing.Source = in.Source
		}
		existing.UpdatedAt = now
		return existing, nil
	}

	if len(st.Machines) >= maxMachines {
		return nil, fmt.Errorf("最多纳管 %d 台机器", maxMachines)
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	m := &Machine{
		ID:           id,
		Host:         host,
		Name:         cmp.Or(strings.TrimSpace(in.Name), host),
		User:         strings.TrimSpace(in.User),
		Port:         in.Port,
		IdentityFile: strings.TrimSpace(in.IdentityFile),
		IsLocal:      false,
		Source:       cmp.Or(in.Source, "manual"),
		Enabled:      enabled,
		Reachable:    true,
		UpdatedAt:    now,
	}
	st.Machines = append(st.Machines, m)
	return m, nil
}

func (st *State) deleteMachine(idOrHost string) error {
	m := st.machine(idOrHost)
	if m == nil {
		return fmt.Errorf("找不到机器 %q", idOrHost)
	}
	if m.IsLocal || m.ID == "local" {
		return errors.New("本机节点（local）不可移除")
	}
	if m.Source == "ssh-config" || m.Source == "persona" {
		// Discovered machines are disabled so automatic discovery does not immediately re-enable them.
		m.Enabled = false
		delete(st.Scans, m.ID)
		return nil
	}
	delete(st.Scans, m.ID)
	st.Machines = slices.DeleteFunc(st.Machines, func(x *Machine) bool { return x.ID == m.ID })
	return nil
}

// statuses returns all enabled machines with their computed health, scan metrics, and cleanup totals.
func (st *State) statuses() []MachineStatus {
	var out []MachineStatus
	for _, m := range st.Machines {
		if !m.Enabled {
			continue
		}
		ms := st.machineStatus(m)
		out = append(out, ms)
	}
	// Keep local first, or sort by health severity then order.
	slices.SortStableFunc(out, func(a, b MachineStatus) int {
		if a.Machine.IsLocal != b.Machine.IsLocal {
			if a.Machine.IsLocal {
				return -1
			}
			return 1
		}
		ra, rb := healthSortRank(a.Level), healthSortRank(b.Level)
		if ra != rb {
			return cmp.Compare(ra, rb)
		}
		return cmp.Compare(a.Machine.ID, b.Machine.ID)
	})
	return out
}

func (st *State) machineStatus(m *Machine) MachineStatus {
	ms := MachineStatus{
		Machine: m,
		Level:   HealthOK,
	}
	if !m.Reachable {
		ms.Level = HealthOffline
	}
	if sc := st.Scans[m.ID]; sc != nil {
		ms.TotalBytes = sc.Mount.TotalBytes
		ms.UsedBytes = sc.Mount.UsedBytes
		ms.AvailableBytes = sc.Mount.AvailableBytes
		if ms.TotalBytes > 0 {
			ms.UsedPercent = float64(ms.UsedBytes) * 100.0 / float64(ms.TotalBytes)
		}
		ms.ReclaimableBytes = sc.ReclaimableBytes
		ms.SafeBytes = sc.SafeBytes
		ms.ReviewBytes = sc.ReviewBytes
		ms.Sessions = sc.Sessions
		t := sc.ScannedAt
		ms.LastScannedAt = &t
		ms.Level = EvaluateHealth(m.Reachable, ms.TotalBytes, ms.AvailableBytes)
	}
	for _, h := range st.History {
		if h.MachineID == m.ID {
			ms.CleanCount++
			ms.TotalFreedBytes += h.FreedBytes
			if ms.LastCleanedAt == nil || h.CreatedAt.After(*ms.LastCleanedAt) {
				t := h.CreatedAt
				ms.LastCleanedAt = &t
				ms.LastFreedBytes = h.FreedBytes
			}
		}
	}
	return ms
}

// ---- Discovery from ~/.persona/config.json, ~/.ssh/config, and ~/.disklessly ----

// SSHHostEntry represents a parsed Host block from ~/.ssh/config.
type SSHHostEntry struct {
	Alias        string `json:"alias"`
	Hostname     string `json:"hostname,omitempty"`
	User         string `json:"user,omitempty"`
	Port         int    `json:"port,omitempty"`
	IdentityFile string `json:"identity_file,omitempty"`
}

// ParseSSHConfig parses an OpenSSH client configuration file and returns concrete Host entries.
func ParseSSHConfig(text string) []SSHHostEntry {
	var results []SSHHostEntry
	aliasIndex := map[string]int{}
	type wildcardBlock struct {
		patterns []string
		fields   map[string]string
	}
	var wildcards []wildcardBlock
	var currentTokens []string
	fields := map[string]string{}

	flush := func() {
		if len(currentTokens) == 0 {
			fields = map[string]string{}
			return
		}
		var concretes, wcards []string
		for _, tok := range currentTokens {
			if IsConcreteHost(tok) {
				concretes = append(concretes, tok)
			} else if !strings.HasPrefix(tok, "!") {
				wcards = append(wcards, tok)
			}
		}
		port, _ := strconv.Atoi(fields["port"])
		for _, alias := range concretes {
			if idx, ok := aliasIndex[alias]; ok {
				if results[idx].Hostname == "" {
					results[idx].Hostname = fields["hostname"]
				}
				if results[idx].User == "" {
					results[idx].User = fields["user"]
				}
				if results[idx].Port == 0 && port > 0 {
					results[idx].Port = port
				}
				if results[idx].IdentityFile == "" {
					results[idx].IdentityFile = fields["identityfile"]
				}
			} else {
				aliasIndex[alias] = len(results)
				results = append(results, SSHHostEntry{
					Alias:        alias,
					Hostname:     fields["hostname"],
					User:         fields["user"],
					Port:         port,
					IdentityFile: fields["identityfile"],
				})
			}
		}
		if len(wcards) > 0 && len(fields) > 0 {
			cp := make(map[string]string, len(fields))
			for k, v := range fields {
				cp[k] = v
			}
			wildcards = append(wildcards, wildcardBlock{patterns: wcards, fields: cp})
		}
		currentTokens = nil
		fields = map[string]string{}
	}

	for _, rawLine := range strings.Split(text, "\n") {
		line := stripSSHComment(rawLine)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, ok := splitSSHDirective(line)
		if !ok {
			continue
		}
		lowerKey := strings.ToLower(key)
		switch lowerKey {
		case "host":
			flush()
			for _, tok := range splitSSHTokens(val) {
				if !strings.HasPrefix(tok, "!") {
					currentTokens = append(currentTokens, tok)
				}
			}
		case "match":
			flush()
		default:
			if len(currentTokens) > 0 && fields[lowerKey] == "" {
				fields[lowerKey] = unquoteSSH(val)
			}
		}
	}
	flush()

	for _, wb := range wildcards {
		port, _ := strconv.Atoi(wb.fields["port"])
		for i := range results {
			alias := results[i].Alias
			matched := false
			for _, p := range wb.patterns {
				if p == "*" {
					matched = true
					break
				}
				if ok, _ := filepath.Match(p, alias); ok {
					matched = true
					break
				}
			}
			if matched {
				if results[i].Hostname == "" {
					results[i].Hostname = wb.fields["hostname"]
				}
				if results[i].User == "" {
					results[i].User = wb.fields["user"]
				}
				if results[i].Port == 0 && port > 0 {
					results[i].Port = port
				}
				if results[i].IdentityFile == "" {
					results[i].IdentityFile = wb.fields["identityfile"]
				}
			}
		}
	}
	return results
}

func stripSSHComment(line string) string {
	inQuotes := false
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch == '"' {
			inQuotes = !inQuotes
			b.WriteByte(ch)
		} else if ch == '#' && !inQuotes {
			break
		} else {
			b.WriteByte(ch)
		}
	}
	return b.String()
}

func splitSSHDirective(line string) (string, string, bool) {
	i := 0
	for i < len(line) && line[i] != ' ' && line[i] != '\t' && line[i] != '=' {
		i++
	}
	if i == 0 || i >= len(line) {
		return "", "", false
	}
	key := line[:i]
	rest := strings.TrimLeft(line[i:], " \t=")
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", false
	}
	return key, rest, true
}

func splitSSHTokens(s string) []string {
	var out []string
	var cur strings.Builder
	inQuotes := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' {
			inQuotes = !inQuotes
		} else if (ch == ' ' || ch == '\t') && !inQuotes {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		} else {
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func unquoteSSH(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// ParsePersonaConfig extracts machine nodes from a Persona configuration file (~/.persona/config.json or link.json).
func ParsePersonaConfig(raw []byte) []MachineInput {
	var doc struct {
		Machines json.RawMessage `json:"machines"`
		Nodes    json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	var out []MachineInput
	parseList := func(data json.RawMessage) {
		if len(data) == 0 {
			return
		}
		var arr []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Host string `json:"host"`
			SSH  string `json:"ssh"`
			User string `json:"user"`
			Port int    `json:"port"`
		}
		if json.Unmarshal(data, &arr) == nil && len(arr) > 0 {
			for _, item := range arr {
				host := cmp.Or(strings.TrimSpace(item.Host), strings.TrimSpace(item.SSH), strings.TrimSpace(item.ID))
				if !IsConcreteHost(host) || isSelfHostAlias(host) {
					continue
				}
				out = append(out, MachineInput{
					ID:     sanitizeID(cmp.Or(item.ID, host)),
					Host:   host,
					Name:   cmp.Or(strings.TrimSpace(item.Name), host),
					User:   strings.TrimSpace(item.User),
					Port:   item.Port,
					Source: "persona",
				})
			}
			return
		}
		var mp map[string]struct {
			Name string `json:"name"`
			Host string `json:"host"`
			SSH  string `json:"ssh"`
			User string `json:"user"`
			Port int    `json:"port"`
		}
		if json.Unmarshal(data, &mp) == nil && len(mp) > 0 {
			keys := make([]string, 0, len(mp))
			for k := range mp {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				item := mp[k]
				host := cmp.Or(strings.TrimSpace(item.Host), strings.TrimSpace(item.SSH), strings.TrimSpace(k))
				if !IsConcreteHost(host) || isSelfHostAlias(host) {
					continue
				}
				out = append(out, MachineInput{
					ID:     sanitizeID(k),
					Host:   host,
					Name:   cmp.Or(strings.TrimSpace(item.Name), k),
					User:   strings.TrimSpace(item.User),
					Port:   item.Port,
					Source: "persona",
				})
			}
		}
	}
	parseList(doc.Machines)
	parseList(doc.Nodes)
	return out
}

// DiscoverMachines merges machines from ~/.persona/config.json, ~/.ssh/config, and Disklessly's machines/config.json.
func (st *State) DiscoverMachines(homeDir string, now time.Time) int {
	st.ensureDefaults(now)
	if homeDir == "" {
		return 0
	}
	added := 0
	mergeOne := func(in MachineInput) {
		if in.Host == "" || isSelfHostAlias(in.Host) || !IsConcreteHost(in.Host) {
			return
		}
		if existing := st.machine(in.ID); existing != nil {
			// Respect user's explicit disable; only fill missing connection metadata.
			if existing.User == "" && in.User != "" {
				existing.User = in.User
			}
			if existing.Port == 0 && in.Port > 0 {
				existing.Port = in.Port
			}
			if existing.IdentityFile == "" && in.IdentityFile != "" {
				existing.IdentityFile = in.IdentityFile
			}
			return
		}
		if existing := st.machine(in.Host); existing != nil {
			return
		}
		if _, err := st.upsertMachine(in, now); err == nil {
			added++
		}
	}

	// 1. ~/.persona/config.json
	for _, rel := range []string{
		filepath.Join(".persona", "config.json"),
		filepath.Join(".config", "persona", "config.json"),
	} {
		if raw, err := os.ReadFile(filepath.Join(homeDir, rel)); err == nil {
			for _, m := range ParsePersonaConfig(raw) {
				mergeOne(m)
			}
		}
	}

	// 2. ~/.ssh/config
	if raw, err := os.ReadFile(filepath.Join(homeDir, ".ssh", "config")); err == nil {
		for _, entry := range ParseSSHConfig(string(raw)) {
			if isSelfHostAlias(entry.Alias) {
				continue
			}
			name := entry.Alias
			if entry.Hostname != "" && entry.Hostname != entry.Alias {
				name = fmt.Sprintf("%s (%s)", entry.Alias, entry.Hostname)
			}
			mergeOne(MachineInput{
				ID:           sanitizeID(entry.Alias),
				Host:         entry.Alias,
				Name:         name,
				User:         entry.User,
				Port:         entry.Port,
				IdentityFile: entry.IdentityFile,
				Source:       "ssh-config",
			})
		}
	}

	// 3. Disklessly's own state machines/config.json (if present)
	for _, rel := range []string{
		filepath.Join(".local", "share", "disklessly", "machines", "config.json"),
		filepath.Join("Library", "Application Support", "Disklessly", "machines", "config.json"),
	} {
		if raw, err := os.ReadFile(filepath.Join(homeDir, rel)); err == nil {
			var dcfg struct {
				Hosts []struct {
					Host        string `json:"host"`
					DisplayName string `json:"displayName"`
					Enabled     bool   `json:"enabled"`
				} `json:"hosts"`
			}
			if json.Unmarshal(raw, &dcfg) == nil {
				for _, h := range dcfg.Hosts {
					if !h.Enabled {
						continue
					}
					mergeOne(MachineInput{
						ID:     sanitizeID(h.Host),
						Host:   h.Host,
						Name:   cmp.Or(h.DisplayName, h.Host),
						Source: "persona",
					})
				}
			}
		}
	}

	return added
}

// FormatBytes formats a byte count in human-readable binary units (GiB, MiB, KiB, B).
func FormatBytes(b uint64) string {
	const (
		tiB = 1024 * gib
		kiB = 1024
	)
	switch {
	case b >= tiB:
		return fmt.Sprintf("%.2f TiB", float64(b)/float64(tiB))
	case b >= gib:
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(gib))
	case b >= mib:
		return fmt.Sprintf("%.1f MiB", float64(b)/float64(mib))
	case b >= kiB:
		return fmt.Sprintf("%.1f KiB", float64(b)/float64(kiB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
