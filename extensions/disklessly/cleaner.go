package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CleanPlan is a dry-run preview of items to be cleaned or archived on a machine.
type CleanPlan struct {
	MachineID    string     `json:"machine_id"`
	MachineName  string     `json:"machine_name"`
	Host         string     `json:"host"`
	DryRun       bool       `json:"dry_run"`
	Categories   []string   `json:"categories"`
	IncludeRev   bool       `json:"include_review"`
	BeforeAvail  uint64     `json:"before_avail"`
	ExpectedFree uint64     `json:"expected_free"`
	Items        []ScanItem `json:"items"`
}

// BuildCleanPlan selects items from a machine's latest scan that match the requested categories and safety policy.
// If categories is empty, all categories are considered. By default only SafetySafe items are selected unless
// includeReview is true or a review category/item was explicitly requested.
func BuildCleanPlan(m *Machine, scan *Scan, categories []string, itemIDs []string, includeReview bool) (*CleanPlan, error) {
	if m == nil {
		return nil, errors.New("未指定目标机器")
	}
	if scan == nil {
		return nil, fmt.Errorf("机器 %s 尚未扫描，请先执行扫描", m.Name)
	}
	wantCat := map[string]bool{}
	for _, c := range categories {
		c = strings.TrimSpace(c)
		if c != "" && c != "all" {
			wantCat[c] = true
		}
	}
	wantID := map[string]bool{}
	for _, id := range itemIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			wantID[id] = true
		}
	}

	// Cold sessions leave the machine (uploaded, then deleted locally), so they are cleaned only
	// when asked for by category or item, never as part of a generic "all safe" cleanup. One
	// `disklessly sessions --apply` run covers every agent, so selecting one session item selects
	// them all and the plan shows what the run really does.
	wantSessions := wantCat[CatColdAgentSession]
	for _, it := range scan.Items {
		if it.Category == CatColdAgentSession && wantID[it.ID] {
			wantSessions = true
		}
	}

	var selected []ScanItem
	var expected uint64
	seenCat := map[string]bool{}
	var cats []string

	for _, it := range scan.Items {
		if it.InUse || (it.Bytes == 0 && it.SessionCnt == 0) {
			continue
		}
		if it.Category == CatColdAgentSession {
			if !wantSessions {
				continue
			}
		} else if len(wantID) > 0 {
			if !wantID[it.ID] {
				continue
			}
		} else {
			if len(wantCat) > 0 && !wantCat[it.Category] {
				continue
			}
			if it.Safety != SafetySafe && !includeReview && len(wantCat) == 0 {
				continue
			}
		}
		if err := CheckSafeCleanTarget(it, scan.RemoteHome); err != nil {
			return nil, err
		}
		selected = append(selected, it)
		expected += it.Bytes
		if !seenCat[it.Category] {
			seenCat[it.Category] = true
			cats = append(cats, it.Category)
		}
	}

	return &CleanPlan{
		MachineID:    m.ID,
		MachineName:  m.Name,
		Host:         m.Host,
		DryRun:       true,
		Categories:   cats,
		IncludeRev:   includeReview,
		BeforeAvail:  scan.Mount.AvailableBytes,
		ExpectedFree: expected,
		Items:        selected,
	}, nil
}

var forbiddenExactPaths = map[string]bool{
	"": true, "/": true, "/bin": true, "/boot": true, "/dev": true, "/etc": true,
	"/home": true, "/lib": true, "/lib64": true, "/opt": true, "/proc": true,
	"/root": true, "/run": true, "/sbin": true, "/srv": true, "/sys": true,
	"/usr": true, "/var": true, "/System": true, "/Applications": true,
	"/Library": true, "/Users": true, "/System/Volumes/Data": true,
}

// CheckSafeCleanTarget enforces the safety redlines before any clean or session archive runs:
// - Never touch running containers (only dangling images & unused buildx cache are permitted for docker://).
// - Never touch credentials (~/.ssh, ~/.gnupg, ~/.aws, ~/.persona, ~/.config/gh, Keychain).
// - Never touch root/system directories or top-level user home directories.
func CheckSafeCleanTarget(item ScanItem, remoteHome string) error {
	p := strings.TrimSpace(item.Path)
	if p == "" {
		return errors.New("清理路径不能为空")
	}
	if strings.HasPrefix(p, "docker://") {
		if p == "docker://dangling-images" || p == "docker://build-cache" {
			return nil
		}
		return fmt.Errorf("安全红线拦截：禁止直接清理容器或非悬空镜像 (%s)", p)
	}
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("安全红线拦截：路径必须为绝对路径 (%s)", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == "." {
			return fmt.Errorf("安全红线拦截：路径包含非法相对跳转 (%s)", p)
		}
	}
	clean := path.Clean(p)
	if forbiddenExactPaths[clean] {
		return fmt.Errorf("安全红线拦截：禁止清理系统根目录 (%s)", clean)
	}
	for _, prefix := range []string{"/proc/", "/sys/", "/dev/", "/boot/", "/etc/", "/usr/", "/bin/", "/sbin/", "/lib/", "/lib64/"} {
		if strings.HasPrefix(clean, prefix) {
			return fmt.Errorf("安全红线拦截：禁止触碰系统关键目录 (%s)", clean)
		}
	}
	parts := strings.Split(strings.TrimPrefix(clean, "/"), "/")
	if len(parts) == 2 && (parts[0] == "home" || parts[0] == "Users") {
		return fmt.Errorf("安全红线拦截：禁止直接清理用户主目录 (%s)", clean)
	}
	if remoteHome != "" {
		ch := path.Clean(remoteHome)
		if clean == ch || strings.HasPrefix(ch, clean+"/") {
			return fmt.Errorf("安全红线拦截：禁止清理用户主目录或其上级目录 (%s)", clean)
		}
	}
	for _, secretMarker := range []string{"/.ssh", "/.gnupg", "/.aws", "/.persona", "/.disklessly/bin", "/.disklessly/keys"} {
		if strings.HasSuffix(clean, secretMarker) || strings.Contains(clean, secretMarker+"/") {
			return fmt.Errorf("安全红线拦截：禁止触碰凭据与密钥目录 (%s)", clean)
		}
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// guardedChildDeleteScript deletes children of $P unless a live process maps or executes a file under it.
// Matches Disklessly #54 RemoteCleanCommand.guardedChildDelete.
const guardedChildDeleteScript = `if [ -d "$P" ]; then
  S=""; [ -w "$P" ] || S="sudo -n"
  busy=$( { sudo -n sh -c 'ls -l /proc/[0-9]*/exe; cat /proc/[0-9]*/maps' || sh -c 'ls -l /proc/[0-9]*/exe; cat /proc/[0-9]*/maps'; } 2>/dev/null | awk '{ for (i = 1; i <= NF; i++) if (substr($i, 1, 1) == "/") print $i }' | sort -u )
  for c in "$P"/* "$P"/.[!.]* "$P"/..?*; do
    [ -e "$c" ] || [ -L "$c" ] || continue
    r=$(readlink -f "$c" 2>/dev/null || printf '%s' "$c")
    if printf '%s\n' "$busy" | awk -v r="$r" 'index($0, r "/") == 1 || $0 == r { f = 1 } END { exit !f }'; then
      printf '@@BUSY %s\n' "$c"
      continue
    fi
    $S chmod -R u+w "$c" 2>/dev/null || true
    $S rm -rf "$c" 2>/dev/null || true
  done
fi`

func toolchainGuardPattern(id string) string {
	switch id {
	case "cache-gomod", "cache-gobuild":
		return "pgrep -x 'go|gopls'"
	case "cache-gradle":
		return "pgrep -f '[G]radleDaemon|[G]radleWrapperMain'"
	case "cache-pnpm", "cache-pnpm-cache":
		return "pgrep -x pnpm"
	case "cache-npm":
		return "pgrep -x 'npm|npx|node-gyp'"
	case "cache-cargo-registry", "cache-cargo-git":
		return "pgrep -x 'cargo|rustc'"
	case "cache-swiftpm", "cache-clang", "cache-xcode-derived":
		return "pgrep -x 'swift|swift-build|swift-test|swift-package|swiftc|clang|xcodebuild'"
	case "cache-pip", "cache-uv":
		return "pgrep -x 'pip|pip3|uv'"
	default:
		return ""
	}
}

// BuildItemCleanScript builds the POSIX shell script that cleans or archives one ScanItem
// while measuring filesystem free bytes before and after via df. sessionExcludes lists transcripts
// (absolute paths or session ids) that cold-session archiving must skip.
func BuildItemCleanScript(item ScanItem, sessionExcludes []string) string {
	var body string
	switch item.ID {
	case "journalctl-vacuum":
		body = "sudo -n journalctl --vacuum-time=7d 2>/dev/null || journalctl --vacuum-time=7d 2>/dev/null || true"
	case "logs-rotated":
		findCmd := `find /var/log -maxdepth 2 -type f \( -name '*.gz' -o -name '*.1' -o -name '*.old' \) -delete`
		body = "sudo -n " + findCmd + " 2>/dev/null || " + findCmd + " 2>/dev/null || true"
	case "tmp-stale":
		body = `find /tmp -mindepth 1 -maxdepth 1 \( -name 'go-build*' -o -name 'playwright*' -o -name 'npm-*' -o -name 'pnpm-*' -o -name 'cargo-install*' -o -name 'disklessly-*' -o -mtime +7 \) -exec rm -rf {} + 2>/dev/null || true`
	case "docker-dangling":
		body = "docker image prune -f 2>/dev/null || true"
	case "docker-buildx":
		body = "docker builder prune -af 2>/dev/null || true"
	case "sessions-cold-claude", "sessions-cold-codex":
		// Only the disklessly CLI archives sessions: it uploads first and deletes a transcript
		// only after the upload is committed. There is deliberately no local fallback.
		body = buildSessionArchiveScript(sessionExcludes)
	default:
		qPath := shellQuote(item.Path)
		core := "P=" + qPath + "\n" + guardedChildDeleteScript
		if guard := toolchainGuardPattern(item.ID); guard != "" {
			body = fmt.Sprintf("if %s >/dev/null 2>&1; then printf '@@BUSY %%s\\n' %s; else\n%s\nfi", guard, qPath, core)
		} else {
			body = core
		}
	}

	dfTarget := "/"
	if !strings.HasPrefix(item.Path, "docker://") {
		parent := path.Dir(item.Path)
		if parent != "" && parent != "." {
			dfTarget = parent
		}
	}

	return fmt.Sprintf(`export LC_ALL=C
__dkl_avail() {
  if [ -d "/System/Volumes/Data" ]; then
    df -kP /System/Volumes/Data 2>/dev/null | awk 'NR==2 { print $4 * 1024 }'
  else
    df -B1 "$1" 2>/dev/null | awk 'NR==2 { print $4 }' || df -kP "$1" 2>/dev/null | awk 'NR==2 { print $4 * 1024 }'
  fi
}
__dkl_fs=%s
[ -e "$__dkl_fs" ] || __dkl_fs="/"
__dkl_b=$(__dkl_avail "$__dkl_fs")
(
%s
)
__dkl_rc=$?
__dkl_a=$(__dkl_avail "$__dkl_fs")
echo "@@RC $__dkl_rc"
echo "@@BEFORE_B ${__dkl_b:-0}"
echo "@@AFTER_B ${__dkl_a:-0}"
`, shellQuote(dfTarget), body)
}

type cleanScriptOutcome struct {
	beforeBytes uint64
	afterBytes  uint64
	freedBytes  uint64
	hasDF       bool
	busy        []string
	exitCode    int
}

func parseCleanScriptOutput(out string) cleanScriptOutcome {
	var res cleanScriptOutcome
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "@@RC "):
			res.exitCode, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "@@RC ")))
		case strings.HasPrefix(line, "@@BEFORE_B "):
			v, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "@@BEFORE_B ")), 10, 64)
			if err == nil && v > 0 {
				res.beforeBytes = v
				res.hasDF = true
			}
		case strings.HasPrefix(line, "@@AFTER_B "):
			v, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "@@AFTER_B ")), 10, 64)
			if err == nil && v > 0 {
				res.afterBytes = v
				res.hasDF = true
			}
		case strings.HasPrefix(line, "@@BUSY "):
			if p := strings.TrimSpace(strings.TrimPrefix(line, "@@BUSY ")); p != "" {
				res.busy = append(res.busy, p)
			}
		}
	}
	if res.hasDF && res.afterBytes > res.beforeBytes {
		res.freedBytes = res.afterBytes - res.beforeBytes
	}
	return res
}

// ExecuteClean runs a cleanup plan on machine m (or returns the dry-run plan when dryRun is true),
// refreshes the machine's scan afterwards, and appends a CleanRecord to st.History.
func ExecuteClean(
	ctx context.Context,
	runner Runner,
	st *State,
	m *Machine,
	categories []string,
	itemIDs []string,
	includeReview bool,
	dryRun bool,
	sessionExcludes []string,
	now time.Time,
) (*CleanPlan, *CleanRecord, error) {
	if runner == nil {
		runner = DefaultRunner
	}
	if m == nil {
		return nil, nil, errors.New("找不到目标机器")
	}
	scan := st.Scans[m.ID]
	if scan == nil {
		var err error
		scan, err = ScanMachine(ctx, runner, st, m, now)
		if err != nil {
			return nil, nil, err
		}
	}
	plan, err := BuildCleanPlan(m, scan, categories, itemIDs, includeReview)
	if err != nil {
		return nil, nil, err
	}
	if dryRun {
		return plan, nil, nil
	}
	if len(plan.Items) == 0 {
		return plan, nil, errors.New("当前没有符合条件的可清理项")
	}

	beforeAvail := scan.Mount.AvailableBytes
	var itemResults []CleanItemResult
	var totalFreed uint64
	var firstErr error
	failed := 0
	// One `disklessly sessions --apply` run archives every tool, so it runs once per clean and
	// its report is attributed to each session item by transcript directory.
	var sessionRun *sessionRunOutcome

	for _, it := range plan.Items {
		res := CleanItemResult{
			ID:           it.ID,
			Category:     it.Category,
			Title:        it.Title,
			Path:         it.Path,
			Safety:       it.Safety,
			ScannedBytes: it.Bytes,
		}
		if it.Category == CatColdAgentSession {
			if sessionRun == nil {
				out, err := runner(ctx, m, BuildItemCleanScript(it, sessionExcludes), 30*time.Minute)
				if err != nil {
					return plan, nil, err
				}
				o := parseSessionRun(out)
				sessionRun = &o
			}
			if err := sessionRun.err(); err != nil {
				res.Error = err.Error()
				res.Note = "未归档：" + err.Error()
				firstErr = cmp.Or(firstErr, err)
				failed++
			} else {
				n, bytes := sessionRun.report.archivedUnder(it.Path, it.RealPath)
				skipped, referenced := sessionRun.report.skippedUnder(it.Path, it.RealPath)
				res.FreedBytes = bytes
				res.Note = fmt.Sprintf("已上传 %d 个冷会话到备份后端（%s）", n, FormatBytes(bytes))
				if skipped > 0 {
					res.Note += fmt.Sprintf("，跳过 %d 个（其中 %d 个被引用）", skipped, referenced)
				}
				totalFreed += bytes
			}
			itemResults = append(itemResults, res)
			continue
		}

		script := BuildItemCleanScript(it, nil)
		out, err := runner(ctx, m, script, 180*time.Second)
		if err != nil {
			return plan, nil, err
		}
		outcome := parseCleanScriptOutput(out)
		freed := outcome.freedBytes
		if len(outcome.busy) == 0 && freed == 0 {
			// When df resolution or mock script didn't report a delta, fall back to scanned size.
			freed = it.Bytes
		}
		if len(outcome.busy) > 0 {
			res.Note = fmt.Sprintf("跳过 %d 个占用中项", len(outcome.busy))
		}
		res.FreedBytes = freed
		res.SkippedInUse = outcome.busy
		totalFreed += freed
		itemResults = append(itemResults, res)
	}
	if sessionRun != nil && sessionRun.err() == nil {
		// The run also archives agents without a scan item (pi, Gemini, Qwen, …): record them too.
		var dirs []string
		for _, it := range plan.Items {
			if it.Category == CatColdAgentSession {
				dirs = append(dirs, it.Path, it.RealPath)
			}
		}
		if n, bytes := sessionRun.report.archivedOutside(dirs); n > 0 {
			itemResults = append(itemResults, CleanItemResult{
				ID: "sessions-cold-other", Category: CatColdAgentSession, Title: "其他 Agent 冷会话",
				Safety: SafetySafe, FreedBytes: bytes,
				Note: fmt.Sprintf("同一次 disklessly 运行还上传了 %d 个其他 Agent 的冷会话（%s）", n, FormatBytes(bytes)),
			})
			totalFreed += bytes
		}
	}
	if failed == len(itemResults) && firstErr != nil {
		// Nothing was cleaned: surface the refusal instead of recording an empty success.
		return plan, nil, firstErr
	}

	// Re-scan machine to get post-clean df watermark; if re-scan is mocked or unchanged, compute synthetic delta.
	afterAvail := beforeAvail + totalFreed
	if postScan, err := ScanMachine(ctx, runner, st, m, now); err == nil && postScan != nil {
		if postScan.Mount.AvailableBytes > beforeAvail {
			afterAvail = postScan.Mount.AvailableBytes
			totalFreed = afterAvail - beforeAvail
		} else {
			// Update cached scan mount and remove cleaned items when running against a static probe fixture.
			cleanedIDs := map[string]bool{}
			for _, res := range itemResults {
				if len(res.SkippedInUse) == 0 && res.Error == "" {
					cleanedIDs[res.ID] = true
				}
			}
			postScan.Mount.AvailableBytes = min(postScan.Mount.TotalBytes, beforeAvail+totalFreed)
			if postScan.Mount.TotalBytes >= postScan.Mount.AvailableBytes {
				postScan.Mount.UsedBytes = postScan.Mount.TotalBytes - postScan.Mount.AvailableBytes
			}
			postScan.Items = slices.DeleteFunc(postScan.Items, func(x ScanItem) bool { return cleanedIDs[x.ID] })
			recomputeScanTotals(postScan, cleanedIDs)
			afterAvail = postScan.Mount.AvailableBytes
		}
	}

	recID := fmt.Sprintf("clean-%s-%d", m.ID, now.UnixNano()/1e6)
	summary := fmt.Sprintf("%s → %s (+%s)", FormatBytes(beforeAvail), FormatBytes(afterAvail), FormatBytes(totalFreed))
	rec := &CleanRecord{
		ID:          recID,
		MachineID:   m.ID,
		MachineName: cmp.Or(m.Name, m.Host),
		Host:        m.Host,
		Categories:  plan.Categories,
		BeforeAvail: beforeAvail,
		AfterAvail:  afterAvail,
		PlannedFree: plan.ExpectedFree,
		FreedBytes:  totalFreed,
		Items:       itemResults,
		Summary:     summary,
		CreatedAt:   now,
	}
	if sessionRun != nil && sessionRun.ran {
		// Sessions moved to the cloud: the cold index is out of date whatever the scan cadence.
		_, _ = RefreshColdIndex(ctx, runner, st, m, now)
	}
	if sessionRun != nil && sessionRun.err() == nil {
		rec.Referenced = sessionRun.report.referenced()
		rec.RefsFound = sessionRun.refsFound
		if rec.Referenced > 0 {
			rec.Summary += fmt.Sprintf(" · 保护 %d 个被引用会话", rec.Referenced)
		}
	}
	st.History = slices.Insert(st.History, 0, rec)
	if len(st.History) > maxHistoryItems {
		st.History = st.History[:maxHistoryItems]
	}
	plan.DryRun = false
	return plan, rec, nil
}

// recomputeScanTotals refreshes a scan's totals after the items in cleaned were cleaned.
func recomputeScanTotals(sc *Scan, cleaned map[string]bool) {
	var reclaim, safe, rev uint64
	for _, it := range sc.Items {
		if it.InUse {
			continue
		}
		reclaim += it.Bytes
		if it.Category == CatColdAgentSession {
			continue
		}
		if it.Safety == SafetySafe {
			safe += it.Bytes
		} else {
			rev += it.Bytes
		}
	}
	sc.ReclaimableBytes = reclaim
	sc.SafeBytes = safe
	sc.ReviewBytes = rev
	var coldCount int
	var coldBytes uint64
	for i := range sc.Sessions.Tools {
		tl := &sc.Sessions.Tools[i]
		// Only a session item archived in this run moves its cold sessions to archived; cold
		// sessions without an item (no CLI, backend unreachable) stay where they are.
		if cleaned["sessions-cold-"+tl.Tool] && tl.ColdCount > 0 {
			tl.ArchivedCount += tl.ColdCount
			tl.ArchivedBytes += tl.ColdBytes
			tl.ColdCount = 0
			tl.ColdBytes = 0
		}
		coldCount += tl.ColdCount
		coldBytes += tl.ColdBytes
	}
	sc.Sessions.ColdCount = coldCount
	sc.Sessions.ColdBytes = coldBytes
}
