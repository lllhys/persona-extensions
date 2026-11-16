package main

import (
	"cmp"
	"encoding/json"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"
)

// Each machine's cold index carries one classified state: what the last refresh found, in a
// machine-readable code (also returned by disklessly_sessions_status) plus the message.
const (
	ColdReady          = "ready"               // list and status parsed, backend reachable
	ColdNoCLI          = "no_cli"              // no disklessly CLI on the machine
	ColdCLIOld         = "cli_too_old"         // the CLI answers sessions list / status with a usage error
	ColdNoBackend      = "no_backend"          // status parsed, no backup backend set up
	ColdBackendDown    = "backend_unreachable" // status parsed, backend not reachable
	ColdOffline        = "unreachable"         // SSH failed or timed out
	ColdUnreadable     = "unreadable"          // none of the above
	ColdUnchecked      = "unchecked"           // never refreshed
	coldInstallDocsURL = "https://github.com/nwverse/persona-extensions/blob/main/docs/extensions/disklessly.md#install-the-disklessly-cli"
)

// Why SSH failed, for ColdOffline.
const (
	causeTimeout = "timeout"
	causeAuth    = "auth"
	causeRefused = "refused"
	causeDNS     = "dns"
	causeNetwork = "network"
)

// coldStateLabel is the short Chinese name of a state, used in badges and the summary.
func coldStateLabel(s string) string {
	switch s {
	case ColdReady:
		return "正常"
	case ColdNoCLI:
		return "未安装 CLI"
	case ColdCLIOld:
		return "CLI 版本过旧"
	case ColdNoBackend:
		return "未设置备份后端"
	case ColdBackendDown:
		return "备份后端连不上"
	case ColdOffline:
		return "连不上这台电脑"
	case ColdUnreadable:
		return "回答无法识别"
	}
	return "尚未检查"
}

func coldStateTone(s string) string {
	switch s {
	case ColdReady:
		return wire.TonePositive
	case ColdOffline, ColdBackendDown, ColdUnreadable:
		return wire.ToneDanger
	}
	return wire.ToneCaution
}

// coldStateRank orders machines worst first.
func coldStateRank(s string) int {
	switch s {
	case ColdOffline:
		return 0
	case ColdBackendDown:
		return 1
	case ColdUnreadable:
		return 2
	case ColdCLIOld:
		return 3
	case ColdNoCLI:
		return 4
	case ColdNoBackend:
		return 5
	case ColdUnchecked:
		return 6
	}
	return 7
}

// stateCode is the catalog's state; a catalog written before states existed is classified from
// the fields it has.
func (c *ColdCatalog) stateCode() string {
	switch {
	case c == nil:
		return ColdUnchecked
	case c.State != "":
		return c.State
	case strings.Contains(c.Error, "未安装 disklessly CLI"):
		return ColdNoCLI
	case strings.HasPrefix(c.Error, "SSH 连接"):
		return ColdOffline
	case c.Error != "":
		return ColdUnreadable
	case c.RefreshedAt.IsZero() || c.Backend.Reachable == nil:
		return ColdUnchecked
	case !*c.Backend.Reachable:
		return ColdBackendDown
	}
	return ColdReady
}

// ColdCLI describes the disklessly CLI found by the last refresh.
type ColdCLI struct {
	Version string `json:"version,omitempty"`
	// Path is what the lookup found (a link such as ~/.local/bin/disklessly), Real the file it resolves to.
	Path string `json:"path,omitempty"`
	Real string `json:"real,omitempty"`
	// Home is the install directory holding bin/disklessly, when the binary lives in one.
	Home string `json:"home,omitempty"`
	KB   uint64 `json:"kb,omitempty"`
}

// cliInfoScript reports the resolved CLI ($DKL set) as tab-separated lines: version, the path
// found, the file it resolves to, and the directory holding bin/ (sized only when it is a
// self-contained disklessly-* directory, so a shared prefix such as /usr/local is never walked).
const cliInfoScript = `echo "@@SECTION:COLD_CLI"
printf 'version\t%s\n' "$("$DKL" version 2>/dev/null | head -n 1)"
P="$DKL"
case "$P" in */*) ;; *) P=$(command -v "$P" 2>/dev/null) ;; esac
R="$P"; n=0
while [ -L "$R" ] && [ "$n" -lt 16 ]; do
  l=$(readlink "$R") || break
  case "$l" in /*) R="$l" ;; *) R="$(dirname "$R")/$l" ;; esac
  n=$((n+1))
done
d=$(cd "$(dirname "$R")" 2>/dev/null && pwd -P) && R="$d/$(basename "$R")"
printf 'path\t%s\nreal\t%s\n' "$P" "$R"
B=$(dirname "$R")
if [ "$(basename "$B")" = bin ]; then
  H=$(dirname "$B")
  printf 'home\t%s\n' "$H"
  case "$(basename "$H")" in disklessly*) printf 'kb\t%s\n' "$(du -sk "$H" 2>/dev/null | awk '{print $1}')" ;; esac
fi
`

func parseCLIInfo(lines []string) ColdCLI {
	var c ColdCLI
	for _, l := range lines {
		k, v, ok := strings.Cut(strings.TrimRight(l, "\r"), "\t")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "version":
			c.Version = clipRunes(v, 80)
		case "path":
			c.Path = v
		case "real":
			c.Real = v
		case "home":
			c.Home = v
		case "kb":
			c.KB, _ = strconv.ParseUint(v, 10, 64)
		}
	}
	return c
}

// shortVersion drops the program name from `disklessly version` ("disklessly 0.3.0" -> "0.3.0").
func shortVersion(v string) string {
	v = strings.TrimSpace(v)
	if f := strings.Fields(v); len(f) >= 2 && strings.EqualFold(f[0], "disklessly") {
		return f[1]
	}
	return v
}

// installDirName matches a self-contained install directory such as disklessly-0.3.0-linux-amd64.
var installDirName = regexp.MustCompile(`^disklessly[A-Za-z0-9._+-]*$`)

// maxInstallKB bounds what a copy-install moves (1 GiB): a CLI directory is tens of MiB.
const maxInstallKB = 1 << 20

// selfContained reports whether the CLI lives in its own directory that can be copied as a whole
// (<dir>/bin/disklessly with dir named disklessly-*), and why not otherwise.
func (c ColdCLI) selfContained() (bool, string) {
	switch {
	case c.Home == "" || c.Real == "":
		return false, "不知道它装在哪里"
	case !strings.HasPrefix(c.Home, "/") || !installDirName.MatchString(path.Base(c.Home)):
		return false, "它装在 " + cmp.Or(c.Real, c.Path) + "，不是可以整体复制的独立目录（例如 Homebrew 或系统目录）"
	case c.KB == 0 || c.KB > maxInstallKB:
		return false, "它的安装目录大小未知或过大"
	}
	return true, ""
}

// cliFailure returns the {"ok":false,"error":{...}} answer of the CLI in lines, if that is what they hold.
func cliFailure(lines []string) *cliError {
	raw := strings.TrimSpace(strings.Join(lines, " "))
	if !strings.HasPrefix(raw, "{") {
		return nil
	}
	var resp struct {
		OK    bool      `json:"ok"`
		Error *cliError `json:"error"`
	}
	if json.Unmarshal([]byte(raw), &resp) != nil || resp.OK || resp.Error == nil {
		return nil
	}
	return resp.Error
}

// usageError reports a CLI that does not know the command or a flag: it predates them.
func usageError(e *cliError) bool {
	if e == nil {
		return false
	}
	msg := strings.ToLower(e.Message)
	return e.Name == "EX_USAGE" || strings.Contains(msg, "unknown sessions command") || strings.Contains(msg, "unknown command")
}

// noBackendError reports a CLI that refuses because no backup backend is configured.
func noBackendError(e *cliError) bool {
	if e == nil {
		return false
	}
	msg := strings.ToLower(e.Message)
	return e.Name == "EX_CONFIG" || strings.Contains(msg, "no backend") || strings.Contains(msg, "backend not configured") ||
		strings.Contains(msg, "not configured")
}

// firstLine is the first non-empty line of a CLI answer, clipped for display.
func firstLine(lines []string) string {
	return clipRunes(oneLine(firstNonEmpty(lines)), 160)
}

// sshCause classifies an SSH failure message.
func sshCause(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "timed out") || strings.Contains(m, "timeout") || strings.Contains(m, "deadline") || strings.Contains(m, "signal: killed"):
		return causeTimeout
	case strings.Contains(m, "bad owner or permissions on"):
		return causeSSHConfig
	case strings.Contains(m, "host key verification") || strings.Contains(m, "remote host identification has changed"):
		return causeHostKey
	case strings.Contains(m, "permission denied") || strings.Contains(m, "authentication") ||
		strings.Contains(m, "publickey") || strings.Contains(m, "too many authentication"):
		return causeAuth
	case strings.Contains(m, "connection refused"):
		return causeRefused
	case strings.Contains(m, "could not resolve") || strings.Contains(m, "name or service not known") || strings.Contains(m, "nodename nor servname"):
		return causeDNS
	case strings.Contains(m, "no route to host") || strings.Contains(m, "network is unreachable") || strings.Contains(m, "host is down"):
		return causeNetwork
	}
	return ""
}

// machineLabel is a machine's name for chips and rows, without the network address that
// discovery from ~/.ssh/config appends as "alias (hostname)".
func machineLabel(m *Machine) string {
	if m == nil {
		return ""
	}
	name := strings.TrimSpace(m.Name)
	if name == "" {
		return cmp.Or(m.ID, m.Host)
	}
	if i := strings.LastIndex(name, " ("); i > 0 && strings.HasSuffix(name, ")") && !m.IsLocal {
		head, inner := strings.TrimSpace(name[:i]), name[i+2:len(name)-1]
		if head == m.Host || head == m.ID || looksLikeAddress(inner) {
			return head
		}
	}
	return name
}

// looksLikeAddress reports an IP address or a dotted host name.
func looksLikeAddress(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t") {
		return false
	}
	return strings.Contains(s, ".") || strings.Count(s, ":") >= 2
}
