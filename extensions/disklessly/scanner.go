package main

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Runner executes a shell script on a machine (either local /bin/sh or remote via ssh).
// Tests can swap Runner on app/mcpServer to deterministically verify scan and clean without touching real disks.
type Runner func(ctx context.Context, m *Machine, script string, timeout time.Duration) (string, error)

// DefaultRunner executes scripts locally via /bin/sh when m.IsLocal (or a self-host alias),
// and via OpenSSH with BatchMode=yes for remote machines.
func DefaultRunner(ctx context.Context, m *Machine, script string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if m == nil || m.IsLocal || isSelfHostAlias(m.Host) {
		cmd = exec.CommandContext(cctx, "/bin/sh", "-s")
	} else {
		if !IsConcreteHost(m.Host) {
			return "", fmt.Errorf("非法 SSH 主机名：%q", m.Host)
		}
		args := []string{
			"-T",
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=12",
			"-o", "ServerAliveInterval=15",
		}
		if m.Port > 0 && m.Port != 22 {
			args = append(args, "-p", strconv.Itoa(m.Port))
		}
		if m.IdentityFile != "" {
			args = append(args, "-i", m.IdentityFile)
		}
		target := m.Host
		if m.User != "" && !strings.Contains(target, "@") {
			target = m.User + "@" + target
		}
		args = append(args, "--", target, "sh -s")
		sshBin := resolveSSHBinary()
		cmd = exec.CommandContext(cctx, sshBin, args...)
	}

	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		reason := lastNonEmptyLine(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		if m != nil && !m.IsLocal {
			return stdout.String(), fmt.Errorf("SSH 连接 %s 失败：%s", m.Host, reason)
		}
		return stdout.String(), fmt.Errorf("本地探测命令执行失败：%s", reason)
	}
	return stdout.String(), nil
}

func resolveSSHBinary() string {
	if env := strings.TrimSpace(os.Getenv("DISKLESSLY_SSH")); env != "" {
		return env
	}
	for _, p := range []string{"/usr/bin/ssh", "/bin/ssh", "/usr/local/bin/ssh", "/opt/homebrew/bin/ssh"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("ssh"); err == nil {
		return p
	}
	return "ssh"
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// ProbeScript is the single-trip, read-only POSIX shell probe that inspects:
// - OS & architecture (uname -sm), hostname, $HOME, installed disklessly CLI version
// - Root / data mount capacity (df -B1 / or df -kP /)
// - Reclaimable caches across all 6 categories:
//  1. go-cache (Go build cache, Go module cache)
//  2. node-python-cache (npm, pnpm, yarn, pip, uv)
//  3. docker-unused (Docker buildx cache, dangling images; never running containers)
//  4. rust-xcode-build (Cargo registry/git, Xcode DerivedData, SwiftPM, Clang, Gradle)
//  5. tmp-logs (systemd journal, rotated /var/log, /tmp stale build/test artifacts >7d)
//  6. cold-agent-sessions (Claude Code & Codex transcripts tiered into hot <=7d vs cold >7d)
const ProbeScript = `export LC_ALL=C
export LANG=C
echo "@@SECTION:UNAME"
uname -sm 2>/dev/null || true
echo "@@SECTION:HOSTNAME"
hostname 2>/dev/null || uname -n 2>/dev/null || echo "machine"
echo "@@SECTION:HOME"
printf '%s\n' "$HOME"
echo "@@SECTION:BIN_VERSION"
if command -v disklessly >/dev/null 2>&1; then
  disklessly version 2>/dev/null | head -n 1 || true
elif [ -x "$HOME/.disklessly/bin/disklessly" ]; then
  "$HOME/.disklessly/bin/disklessly" version 2>/dev/null | head -n 1 || true
elif [ -x "/Applications/Disklessly.app/Contents/MacOS/disklessly" ]; then
  "/Applications/Disklessly.app/Contents/MacOS/disklessly" version 2>/dev/null | head -n 1 || true
fi
echo "@@SECTION:DF"
if [ -d "/System/Volumes/Data" ]; then
  df -kP /System/Volumes/Data 2>/dev/null || df -kP / 2>/dev/null || true
else
  df -B1 / 2>/dev/null || df -kP / 2>/dev/null || true
fi
echo "@@SECTION:CACHES"
probe_dir() {
  id="$1"; cat="$2"; title="$3"; target="$4"; safety="$5"; reason="$6"
  if [ -e "$target" ]; then
    kb=$(du -xsk "$target" 2>/dev/null | awk '{print $1}' | head -n 1)
    if [ -n "$kb" ] && [ "$kb" -gt 0 ] 2>/dev/null; then
      printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$id" "$cat" "$title" "$target" "$kb" "$safety" "$reason"
    fi
  fi
}
GOCACHE_DIR=$(go env GOCACHE 2>/dev/null || printf '%s' "$HOME/.cache/go-build")
GOMOD_DIR=$(go env GOMODCACHE 2>/dev/null || printf '%s' "$HOME/go/pkg/mod")
[ -z "$GOCACHE_DIR" ] && GOCACHE_DIR="$HOME/.cache/go-build"
[ -z "$GOMOD_DIR" ] && GOMOD_DIR="$HOME/go/pkg/mod"
probe_dir "cache-gobuild" "go-cache" "Go 编译构建缓存 (go-build)" "$GOCACHE_DIR" "safe" "可随时重建的 Go 编译器构建缓存"
probe_dir "cache-gomod" "go-cache" "Go 模块下载缓存 (pkg/mod)" "$GOMOD_DIR" "safe" "可重新下载的 Go 依赖模块缓存"

probe_dir "cache-npm" "node-python-cache" "npm 包缓存 (~/.npm)" "$HOME/.npm" "safe" "可重新下载的 npm 缓存包"
probe_dir "cache-pnpm" "node-python-cache" "pnpm 内容寻址缓存 (pnpm/store)" "$HOME/.local/share/pnpm/store" "safe" "可重新拉取的 pnpm 全局存储"
probe_dir "cache-pnpm-cache" "node-python-cache" "pnpm 下载缓存 (~/.cache/pnpm)" "$HOME/.cache/pnpm" "safe" "可重新生成的 pnpm 元数据与 dlx 缓存"
probe_dir "cache-yarn" "node-python-cache" "Yarn 包缓存 (~/.cache/yarn)" "$HOME/.cache/yarn" "safe" "可重新下载的 Yarn 缓存"
probe_dir "cache-pip" "node-python-cache" "pip 轮子与 HTTP 缓存 (~/.cache/pip)" "$HOME/.cache/pip" "safe" "可重新下载的 Python pip 缓存"
probe_dir "cache-uv" "node-python-cache" "uv Python 极速缓存 (~/.cache/uv)" "$HOME/.cache/uv" "safe" "可重新下载的 uv 包缓存"

probe_dir "cache-cargo-registry" "rust-xcode-build" "Cargo 索引与源码包 (~/.cargo/registry)" "$HOME/.cargo/registry" "safe" "可重新同步的 Rust Crates 缓存"
probe_dir "cache-cargo-git" "rust-xcode-build" "Cargo Git 检出缓存 (~/.cargo/git)" "$HOME/.cargo/git" "safe" "可重新拉取的 Cargo Git 依赖"
probe_dir "cache-xcode-derived" "rust-xcode-build" "Xcode DerivedData 构建产物" "$HOME/Library/Developer/Xcode/DerivedData" "safe" "可重新编译的 Xcode DerivedData 缓存"
probe_dir "cache-swiftpm" "rust-xcode-build" "SwiftPM 包缓存 (org.swift.swiftpm)" "$HOME/.cache/org.swift.swiftpm" "safe" "可重新拉取的 Swift Package Manager 缓存"
probe_dir "cache-clang" "rust-xcode-build" "Clang 模块缓存 (~/.cache/clang)" "$HOME/.cache/clang" "safe" "可重新生成的 Clang 预编译模块缓存"
probe_dir "cache-gradle" "rust-xcode-build" "Gradle 构建缓存 (~/.gradle/caches)" "$HOME/.gradle/caches" "safe" "可重新构建的 Gradle 缓存"
probe_dir "cache-playwright" "rust-xcode-build" "Playwright 浏览器缓存 (ms-playwright)" "$HOME/.cache/ms-playwright" "review" "无头浏览器镜像，清理后首次运行需重新下载"

echo "@@SECTION:TMP_LOGS"
if command -v journalctl >/dev/null 2>&1; then
  journalctl --disk-usage 2>/dev/null || sudo -n journalctl --disk-usage 2>/dev/null || true
fi
if [ -d /var/log ]; then
  rot_kb=$(find /var/log -maxdepth 2 -type f \( -name '*.gz' -o -name '*.1' -o -name '*.old' \) -exec du -k {} + 2>/dev/null | awk '{s+=$1} END {print s+0}')
  if [ -n "$rot_kb" ] && [ "$rot_kb" -gt 0 ] 2>/dev/null; then
    printf 'logs-rotated\ttmp-logs\t系统轮转归档日志 (/var/log/*.gz)\t/var/log\t%s\tsafe\t已轮转压缩的历史系统日志，不影响当前服务写入\n' "$rot_kb"
  fi
fi
if [ -d /tmp ]; then
  tmp_kb=$(find /tmp -mindepth 1 -maxdepth 1 \( -name 'go-build*' -o -name 'playwright*' -o -name 'npm-*' -o -name 'pnpm-*' -o -name 'cargo-install*' -o -name 'disklessly-*' -o -mtime +7 \) -exec du -xsk {} + 2>/dev/null | awk '{s+=$1} END {print s+0}')
  if [ -n "$tmp_kb" ] && [ "$tmp_kb" -gt 0 ] 2>/dev/null; then
    printf 'tmp-stale\ttmp-logs\t/tmp 陈旧构建与临时产物 (>7天)\t/tmp\t%s\tsafe\t超过 7 天或已退出的 go-build/playwright/npm 临时目录\n' "$tmp_kb"
  fi
fi

echo "@@SECTION:DOCKER"
if command -v docker >/dev/null 2>&1; then
  dangling_kb=$(docker images -f "dangling=true" --format '{{.Size}}' 2>/dev/null | awk '
    function to_kb(s,   v) {
      v = s + 0
      if (index(s, "GB") || index(s, "GiB")) return int(v * 1024 * 1024)
      if (index(s, "MB") || index(s, "MiB")) return int(v * 1024)
      if (index(s, "kB") || index(s, "KB") || index(s, "KiB")) return int(v)
      return 0
    }
    { sum += to_kb($0) }
    END { print sum + 0 }
  ')
  if [ -n "$dangling_kb" ] && [ "$dangling_kb" -gt 0 ] 2>/dev/null; then
    printf 'docker-dangling\tdocker-unused\tDocker 悬空镜像 (dangling images)\tdocker://dangling-images\t%s\tsafe\t未被任何运行或停止容器引用的悬空镜像层\n' "$dangling_kb"
  fi
  bcache_line=$(docker system df --format '{{.Type}}|{{.Reclaimable}}' 2>/dev/null | awk -F'|' '$1=="Build Cache"{print $2}' | head -n 1)
  if [ -n "$bcache_line" ]; then
    printf 'docker-buildx\t%s\n' "$bcache_line"
  fi
fi

echo "@@SECTION:SESSIONS"
probe_sessions() {
  tool="$1"; label="$2"; dir="$3"
  if [ -d "$dir" ]; then
    hot_c=$(find "$dir" -type f \( -name '*.jsonl' -o -name '*.json' \) -mtime -7 2>/dev/null | wc -l | tr -d ' ')
    hot_kb=$(find "$dir" -type f \( -name '*.jsonl' -o -name '*.json' \) -mtime -7 -exec du -k {} + 2>/dev/null | awk '{s+=$1} END {print s+0}')
    cold_c=$(find "$dir" -type f \( -name '*.jsonl' -o -name '*.json' \) -mtime +6 2>/dev/null | wc -l | tr -d ' ')
    cold_kb=$(find "$dir" -type f \( -name '*.jsonl' -o -name '*.json' \) -mtime +6 -exec du -k {} + 2>/dev/null | awk '{s+=$1} END {print s+0}')
    real=$(cd "$dir" 2>/dev/null && pwd -P)
    printf 'tool\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$tool" "$label" "$dir" "${hot_c:-0}" "${hot_kb:-0}" "${cold_c:-0}" "${cold_kb:-0}" "${real:-$dir}"
  fi
}
probe_sessions "claude" "Claude Code" "$HOME/.claude/projects"
probe_sessions "codex" "Codex" "$HOME/.codex/sessions"
arch_dir="$HOME/.disklessly/session-archives"
if [ -d "$arch_dir" ]; then
  ac=$(find "$arch_dir" -type f -name '*.tar.gz' 2>/dev/null | wc -l | tr -d ' ')
  akb=$(du -xsk "$arch_dir" 2>/dev/null | awk '{print $1}' | head -n 1)
  printf 'archive\t%s\t%s\t%s\n' "$arch_dir" "${ac:-0}" "${akb:-0}"
fi
` + resolveCLIScript + `
if [ -n "$DKL" ]; then
  echo "@@SECTION:SESSIONS_CLI_PLAN"
  "$DKL" --json sessions ` + cliDaysFlag + ` 2>/dev/null | tr '\n' ' '; echo
  echo "@@SECTION:SESSIONS_CLI_STATUS"
  "$DKL" --json sessions status 2>/dev/null | tr '\n' ' '; echo
  echo "@@SECTION:SESSIONS_FILES"
  for d in "$HOME/.claude/projects" "$HOME/.codex/sessions"; do
    if [ -d "$d" ]; then
      r=$(cd "$d" 2>/dev/null && pwd -P) || r="$d"
      find "$r/" -type f \( -name '*.jsonl' -o -name '*.json' \) -mtime +5 -exec du -k {} + 2>/dev/null
    fi
  done
fi
echo "@@SECTION:END"
`

// ParseProbeOutput parses the output of ProbeScript into a Scan report.
func ParseProbeOutput(m *Machine, output string, now time.Time) *Scan {
	sections := map[string][]string{}
	cur := ""
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(line, "@@SECTION:") {
			cur = strings.TrimSpace(strings.TrimPrefix(line, "@@SECTION:"))
			sections[cur] = nil
		} else if cur != "" {
			sections[cur] = append(sections[cur], line)
		}
	}

	unameLine := firstNonEmpty(sections["UNAME"])
	platform := parsePlatformString(unameLine)
	hostname := cmp.Or(firstNonEmpty(sections["HOSTNAME"]), m.Host)
	remoteHome := firstNonEmpty(sections["HOME"])
	binVer := firstNonEmpty(sections["BIN_VERSION"])
	scanMode := "builtin-sh"
	if binVer != "" {
		scanMode = "disklessly-cli"
	}

	mount := parseDFSection(sections["DF"], platform)
	var items []ScanItem

	for _, line := range sections["CACHES"] {
		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}
		kb, err := strconv.ParseUint(strings.TrimSpace(parts[4]), 10, 64)
		if err != nil || kb == 0 {
			continue
		}
		items = append(items, ScanItem{
			ID:       parts[0],
			Category: parts[1],
			Title:    parts[2],
			Path:     parts[3],
			Bytes:    kb * 1024,
			Safety:   parts[5],
			Action:   "clean",
			Reason:   parts[6],
		})
	}

	for _, line := range sections["TMP_LOGS"] {
		if strings.Contains(line, "\t") {
			parts := strings.Split(line, "\t")
			if len(parts) >= 7 {
				kb, err := strconv.ParseUint(strings.TrimSpace(parts[4]), 10, 64)
				if err == nil && kb > 0 {
					items = append(items, ScanItem{
						ID:       parts[0],
						Category: parts[1],
						Title:    parts[2],
						Path:     parts[3],
						Bytes:    kb * 1024,
						Safety:   parts[5],
						Action:   "clean",
						Reason:   parts[6],
					})
				}
			}
			continue
		}
		if b := parseJournalBytes(line); b > 0 {
			items = append(items, ScanItem{
				ID:       "journalctl-vacuum",
				Category: CatTmpLogs,
				Title:    "Systemd Journal 系统日志 (>7天)",
				Path:     "/var/log/journal",
				Bytes:    b,
				Safety:   SafetySafe,
				Action:   "clean",
				Reason:   "使用 journalctl --vacuum-time=7d 清理 7 天前的系统日志，保留近期日志",
			})
		}
	}

	for _, line := range sections["DOCKER"] {
		parts := strings.Split(line, "\t")
		if len(parts) >= 7 && parts[0] == "docker-dangling" {
			kb, err := strconv.ParseUint(strings.TrimSpace(parts[4]), 10, 64)
			if err == nil && kb > 0 {
				items = append(items, ScanItem{
					ID:       parts[0],
					Category: parts[1],
					Title:    parts[2],
					Path:     parts[3],
					Bytes:    kb * 1024,
					Safety:   parts[5],
					Action:   "clean",
					Reason:   parts[6],
				})
			}
		} else if len(parts) == 2 && parts[0] == "docker-buildx" {
			if b := parseHumanSize(parts[1]); b > 0 {
				items = append(items, ScanItem{
					ID:       "docker-buildx",
					Category: CatDockerUnused,
					Title:    "Docker Buildx 构建缓存 (unused)",
					Path:     "docker://build-cache",
					Bytes:    b,
					Safety:   SafetySafe,
					Action:   "clean",
					Reason:   "未被运行中构建引用的 BuildKit 缓存层，绝不触碰运行中的服务容器",
				})
			}
		}
	}

	sess := SessionSummary{
		ArchiveDir: cmp.Or(remoteHome, "~") + "/.disklessly/session-archives",
	}
	for _, line := range sections["SESSIONS"] {
		parts := strings.Split(line, "\t")
		if len(parts) >= 8 && parts[0] == "tool" {
			hotC, _ := strconv.Atoi(parts[4])
			hotKB, _ := strconv.ParseUint(parts[5], 10, 64)
			coldC, _ := strconv.Atoi(parts[6])
			coldKB, _ := strconv.ParseUint(parts[7], 10, 64)
			ts := AgentToolSessions{
				Tool:      parts[1],
				Label:     parts[2],
				Directory: parts[3],
				HotCount:  hotC,
				HotBytes:  hotKB * 1024,
				ColdCount: coldC,
				ColdBytes: coldKB * 1024,
			}
			if len(parts) >= 9 {
				ts.RealDirectory = strings.TrimSpace(parts[8])
			}
			sess.HotCount += ts.HotCount
			sess.HotBytes += ts.HotBytes
			sess.ColdCount += ts.ColdCount
			sess.ColdBytes += ts.ColdBytes
			sess.Tools = append(sess.Tools, ts)
		} else if len(parts) >= 4 && parts[0] == "archive" {
			sess.ArchiveDir = parts[1]
			ac, _ := strconv.Atoi(parts[2])
			akb, _ := strconv.ParseUint(parts[3], 10, 64)
			sess.ArchivedCount = ac
			sess.ArchivedBytes = akb * 1024
		}
	}

	items = append(items, applySessionCLI(&sess, sections)...)

	// Sort items by size descending, then path ascending.
	slices.SortFunc(items, func(a, b ScanItem) int {
		if a.Bytes != b.Bytes {
			if a.Bytes > b.Bytes {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Path, b.Path)
	})

	var reclaimable, safeBytes, reviewBytes uint64
	for _, it := range items {
		if it.InUse {
			continue
		}
		reclaimable += it.Bytes
		if it.Category == CatColdAgentSession {
			// Uploaded off the machine, not a rebuildable cache: never part of the safe total.
			continue
		}
		if it.Safety == SafetySafe {
			safeBytes += it.Bytes
		} else {
			reviewBytes += it.Bytes
		}
	}

	return &Scan{
		MachineID:        m.ID,
		Host:             m.Host,
		Hostname:         hostname,
		RemoteHome:       remoteHome,
		Platform:         platform,
		BinaryVersion:    binVer,
		ScanMode:         scanMode,
		Mount:            mount,
		Items:            items,
		Sessions:         sess,
		ReclaimableBytes: reclaimable,
		SafeBytes:        safeBytes,
		ReviewBytes:      reviewBytes,
		ScannedAt:        now,
	}
}

func firstNonEmpty(lines []string) string {
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

func parsePlatformString(uname string) string {
	fields := strings.Fields(strings.ToLower(uname))
	if len(fields) < 2 {
		return strings.TrimSpace(uname)
	}
	sys, arch := fields[0], fields[1]
	switch {
	case strings.Contains(sys, "linux"):
		sys = "linux"
	case strings.Contains(sys, "darwin"):
		sys = "darwin"
	}
	switch arch {
	case "x86_64", "amd64":
		arch = "amd64"
	case "aarch64", "arm64":
		arch = "arm64"
	}
	return sys + "-" + arch
}

func parseDFSection(lines []string, platform string) MountInfo {
	fsType := "ext4"
	if strings.HasPrefix(platform, "darwin") {
		fsType = "apfs"
	}
	var header string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "filesystem") {
			header = strings.ToLower(line)
			continue
		}
		cols := strings.Fields(line)
		if len(cols) < 6 {
			continue
		}
		total, err1 := strconv.ParseUint(cols[1], 10, 64)
		used, err2 := strconv.ParseUint(cols[2], 10, 64)
		avail, err3 := strconv.ParseUint(cols[3], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		// df -kP reports 1024-byte blocks; df -B1 reports 1B-blocks.
		isBytes := strings.Contains(header, "1b-blocks") || total > 10*1024*1024*1024
		mult := uint64(1024)
		if isBytes {
			mult = 1
		}
		return MountInfo{
			Device:         cols[0],
			MountPoint:     strings.Join(cols[5:], " "),
			FSType:         fsType,
			TotalBytes:     total * mult,
			UsedBytes:      used * mult,
			AvailableBytes: avail * mult,
		}
	}
	return MountInfo{MountPoint: "/", FSType: fsType}
}

func parseJournalBytes(line string) uint64 {
	_, after, ok := strings.Cut(line, "take up ")
	if !ok {
		return 0
	}
	tok, _, _ := strings.Cut(strings.TrimSpace(after), " ")
	return parseHumanSize(tok)
}

func parseHumanSize(raw string) uint64 {
	s := strings.TrimSpace(raw)
	if idx := strings.IndexAny(s, " ("); idx > 0 {
		s = s[:idx]
	}
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" || s == "0B" || s == "0" {
		return 0
	}
	type unit struct {
		suffix string
		mult   float64
	}
	units := []unit{
		{"TIB", float64(1024 * gib)},
		{"TB", float64(1024 * gib)},
		{"T", float64(1024 * gib)},
		{"GIB", float64(gib)},
		{"GB", float64(gib)},
		{"G", float64(gib)},
		{"MIB", float64(mib)},
		{"MB", float64(mib)},
		{"M", float64(mib)},
		{"KIB", 1024},
		{"KB", 1024},
		{"K", 1024},
		{"B", 1},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			numStr := strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			val, err := strconv.ParseFloat(numStr, 64)
			if err != nil || val <= 0 {
				return 0
			}
			return uint64(val * u.mult)
		}
	}
	val, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return val
}

// ScanMachine probes m using runner and updates st.Scans[m.ID] and m's metadata.
func ScanMachine(ctx context.Context, runner Runner, st *State, m *Machine, now time.Time) (*Scan, error) {
	if runner == nil {
		runner = DefaultRunner
	}
	out, err := runner(ctx, m, ProbeScript, 90*time.Second)
	if err != nil {
		m.Reachable = false
		m.LastError = err.Error()
		m.UpdatedAt = now
		return nil, err
	}
	scan := ParseProbeOutput(m, out, now)
	m.Reachable = true
	m.LastError = ""
	if scan.Platform != "" {
		m.Platform = scan.Platform
	}
	if scan.BinaryVersion != "" {
		m.BinaryVersion = scan.BinaryVersion
	}
	m.ScanMode = scan.ScanMode
	m.UpdatedAt = now
	if st.Scans == nil {
		st.Scans = map[string]*Scan{}
	}
	st.Scans[m.ID] = scan
	if scan.BinaryVersion != "" || scan.Sessions.CLIReady {
		// Keep the local cold index current without pulling the index on every scan; a failed
		// refresh only marks the cloud state unknown.
		refreshColdIfStale(ctx, runner, st, m, now, coldScanRefreshAge)
	}
	return scan, nil
}

// sizeKB looks up a transcript's size in the SESSIONS_FILES map, which lists files under the
// resolved tool directory. p may be in either the configured (dir) or the resolved (real) form.
func sizeKB(kb map[string]uint64, p, dir, real string) uint64 {
	if v, ok := kb[p]; ok {
		return v
	}
	for _, pair := range [][2]string{{dir, real}, {real, dir}} {
		from, to := path.Clean(pair[0]), path.Clean(pair[1])
		if pair[0] == "" || pair[1] == "" || from == to || !underDir(p, from) {
			continue
		}
		if v, ok := kb[to+strings.TrimPrefix(path.Clean(p), from)]; ok {
			return v
		}
	}
	return 0
}

// applySessionCLI replaces the find-based cold session estimate with the disklessly CLI's own
// cold plan (which skips hot, open and per-project longer-threshold sessions), attaches the
// cloud backup state, and returns the archivable cold session items. Without a CLI that reports
// both its plan and a reachable backend, no session item is offered: the extension never
// archives or deletes transcripts itself.
func applySessionCLI(sess *SessionSummary, sections map[string][]string) []ScanItem {
	var plan sessionReport
	planOK := parseCLIJSONLine(sections["SESSIONS_CLI_PLAN"], &plan)
	var status CloudStatus
	if parseCLIJSONLine(sections["SESSIONS_CLI_STATUS"], &status) {
		sess.Cloud = &status
	}
	sess.CLIReady = planOK && sess.Cloud != nil
	switch {
	case !sess.CLIReady:
		sess.Note = "目标机器没有支持云端归档的 disklessly CLI（需要 sessions status 与 --exclude-from），冷会话保持原样；安装或升级 disklessly 后重新扫描。"
		return nil
	case !sess.Cloud.Backend.Reachable:
		sess.Note = "备份后端不可达，冷会话暂不归档：" + cmpOr(sess.Cloud.Backend.Error, "backend unreachable")
	}

	kb := map[string]uint64{}
	for _, line := range sections["SESSIONS_FILES"] {
		size, p, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if v, err := strconv.ParseUint(strings.TrimSpace(size), 10, 64); err == nil {
			kb[p] = v
		}
	}
	sess.ColdCount, sess.ColdBytes = 0, 0
	var items []ScanItem
	for i := range sess.Tools {
		ts := &sess.Tools[i]
		ts.ColdCount, ts.ColdBytes = 0, 0
		for _, p := range plan.Cold {
			if underAny(p, ts.Directory, ts.RealDirectory) {
				ts.ColdCount++
				ts.ColdBytes += sizeKB(kb, p, ts.Directory, ts.RealDirectory) * 1024
			}
		}
		sess.ColdCount += ts.ColdCount
		sess.ColdBytes += ts.ColdBytes
		if ts.ColdCount == 0 || !sess.Cloud.Backend.Reachable {
			continue
		}
		items = append(items, ScanItem{
			ID:         "sessions-cold-" + ts.Tool,
			Category:   CatColdAgentSession,
			Title:      fmt.Sprintf("%s 冷会话日志 (%d 个 >7天会话)", ts.Label, ts.ColdCount),
			Path:       ts.Directory,
			Bytes:      ts.ColdBytes,
			Safety:     SafetySafe,
			Action:     "archive",
			Reason:     "由 disklessly 上传到备份后端并写入索引后才移除本地副本；7 天内活跃、正在打开或仍被引用的会话保留",
			SessionCnt: ts.ColdCount,
			RealPath:   ts.RealDirectory,
		})
	}
	return items
}
