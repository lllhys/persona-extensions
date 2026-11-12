package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
)

// Cold coding-agent sessions are archived only by the disklessly CLI on the target machine:
// it uploads them to the configured backup backend, records them in its index and deletes the
// local transcript only after the upload is committed. The extension never deletes a transcript
// itself, so a missing CLI, an unreachable backend or a failed run leaves every file in place.

// cliExitBackend is the disklessly CLI exit code for an unreachable backend (EX_BACKEND).
const cliExitBackend = 4

// cliExitMissing is reported by the clean script when no disklessly CLI is installed.
const cliExitMissing = 127

// sessionExcludesFile is an optional, user-maintained list on the target machine of transcripts
// that must never be archived (absolute paths or session ids, one per line).
const sessionExcludesFile = "$HOME/.disklessly/session-excludes"

// coldSessionDays is the age, in days, after which a transcript counts as cold. It is passed to every
// CLI run that plans or archives sessions, so the CLI's own default never applies.
const coldSessionDays = 7

// cliDaysFlag is the `--days` argument for CLI runs that plan or archive cold sessions; it must
// spell coldSessionDays (a test checks).
const cliDaysFlag = "--days 7"

// resolveCLIScript sets DKL to the disklessly CLI on the target machine, or leaves it empty. The
// lookup order matches search-as-code's findDisklessly: $DISKLESSLY_BIN, PATH, then the usual
// install locations.
const resolveCLIScript = `DKL=""
if [ -n "$DISKLESSLY_BIN" ] && [ -x "$DISKLESSLY_BIN" ] && [ ! -d "$DISKLESSLY_BIN" ]; then
  DKL="$DISKLESSLY_BIN"
elif command -v disklessly >/dev/null 2>&1; then
  DKL=disklessly
else
  for c in "$HOME/.disklessly/bin/disklessly" "$HOME/.local/bin/disklessly" /usr/local/bin/disklessly /opt/homebrew/bin/disklessly /Applications/Disklessly.app/Contents/MacOS/disklessly; do
    if [ -x "$c" ] && [ ! -d "$c" ]; then DKL="$c"; break; fi
  done
fi`

// cliError is the {"ok":false,"error":{...}} payload of the disklessly CLI.
type cliError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Name    string `json:"name"`
}

// SessionPathReason is one skipped or kept-local transcript of a sessions run.
type SessionPathReason struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ArchivedSession is one transcript archived to the backup backend in a sessions run.
type ArchivedSession struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	SessionID string `json:"sessionId"`
	Bytes     uint64 `json:"bytes"`
}

// sessionReport is the JSON report of `disklessly --json sessions [--apply]`.
type sessionReport struct {
	OK            bool                `json:"ok"`
	Applied       bool                `json:"applied"`
	Cold          []string            `json:"cold"`
	Skipped       []SessionPathReason `json:"skipped"`
	KeptLocal     []SessionPathReason `json:"keptLocal"`
	UploadedBytes uint64              `json:"uploadedBytes"`
	Archived      []ArchivedSession   `json:"archived"`
	Error         *cliError           `json:"error,omitempty"`
}

// CloudBackend is the backup backend reachability reported by `disklessly sessions status`.
type CloudBackend struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Reachable bool   `json:"reachable"`
	LatencyMs int64  `json:"latencyMs"`
	Error     string `json:"error"`
}

// CloudSegments summarizes the archive segments referenced by the index.
type CloudSegments struct {
	Total     int      `json:"total"`
	Checked   int      `json:"checked"`
	Missing   []string `json:"missing,omitempty"`
	Unchecked []string `json:"unchecked,omitempty"`
	TimedOut  bool     `json:"timedOut,omitempty"`
}

// CloudStatus is the cloud backup state of a machine's archived sessions.
type CloudStatus struct {
	DeviceID      string         `json:"deviceId"`
	Backend       CloudBackend   `json:"backend"`
	Counts        map[string]int `json:"counts"`
	ArchivedBytes uint64         `json:"archivedBytes"`
	Segments      CloudSegments  `json:"segments"`
}

// CloudEntry is one indexed session from `disklessly sessions list`.
type CloudEntry struct {
	ID         string `json:"id"`
	DeviceID   string `json:"deviceId"`
	Local      bool   `json:"local"`
	Tool       string `json:"tool"`
	Path       string `json:"path"`
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	Project    string `json:"project"`
	Mtime      string `json:"mtime"`
	ArchivedAt string `json:"archivedAt"`
	Bytes      uint64 `json:"bytes"`
	State      string `json:"state"`
	Title      string `json:"title"`
}

// cleanSessionExcludes normalizes exclusion entries for --exclude-from: one per line, so
// entries that are empty, comments or contain a line break are dropped.
func cleanSessionExcludes(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" || strings.HasPrefix(e, "#") || strings.ContainsAny(e, "\r\n\x00") || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

// buildSessionArchiveScript runs `disklessly --json sessions --apply` with an exclusion file and
// reports its exit code and JSON report. It never deletes anything itself.
func buildSessionArchiveScript(excludes []string) string {
	var b strings.Builder
	b.WriteString(refProbeFunc)
	b.WriteString("\n")
	b.WriteString(resolveCLIScript)
	b.WriteString("\nif [ -z \"$DKL\" ]; then\n  echo \"@@SESSIONS_RC ")
	b.WriteString(strconv.Itoa(cliExitMissing))
	b.WriteString("\"\n  exit 0\nfi\n")
	b.WriteString(`EXF=$(mktemp "${TMPDIR:-/tmp}/dkl-excl.XXXXXX") || { echo "@@SESSIONS_RC 1"; echo "@@SESSIONS_ERR cannot create exclusion list"; exit 0; }
ERRF="$EXF.err"
`)
	b.WriteString(": > \"$EXF\"\n")
	for _, e := range cleanSessionExcludes(excludes) {
		b.WriteString("printf '%s\\n' " + shellQuote(e) + " >> \"$EXF\"\n")
	}
	b.WriteString(refProbeArchiveStep)
	b.WriteString(`if [ -f "` + sessionExcludesFile + `" ]; then cat "` + sessionExcludesFile + `" >> "$EXF" 2>/dev/null; fi
OUT=$("$DKL" --json sessions --apply ` + cliDaysFlag + ` --exclude-from "$EXF" 2>"$ERRF")
RC=$?
echo "@@SESSIONS_RC $RC"
printf '@@SESSIONS_JSON %s\n' "$(printf '%s' "$OUT" | tr '\n' ' ')"
if [ -s "$ERRF" ]; then printf '@@SESSIONS_ERR %s\n' "$(tail -n 3 "$ERRF" | tr '\n' ' ')"; fi
rm -f "$EXF" "$ERRF"`)
	return b.String()
}

// sessionRunOutcome is the parsed result of one sessions archive run.
type sessionRunOutcome struct {
	ran      bool
	exitCode int
	report   *sessionReport
	stderr   string
	// refsFailed is set when the reference probe could not list processes and the run was aborted.
	refsFailed bool
	// refsFound counts the session ids and paths the reference probe added to the exclusions.
	refsFound int
}

func parseSessionRun(out string) sessionRunOutcome {
	var res sessionRunOutcome
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.HasPrefix(line, "@@SESSIONS_RC "):
			res.ran = true
			res.exitCode, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "@@SESSIONS_RC ")))
		case strings.HasPrefix(line, "@@SESSIONS_JSON "):
			var rep sessionReport
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "@@SESSIONS_JSON ")), &rep); err == nil {
				res.report = &rep
			}
		case line == "@@REFS_FAIL":
			res.refsFailed = true
		case strings.HasPrefix(line, "@@REFS "):
			res.refsFound, _ = parseRefsCount(line)
		case strings.HasPrefix(line, "@@SESSIONS_ERR "):
			res.stderr = strings.TrimSpace(strings.TrimPrefix(line, "@@SESSIONS_ERR "))
		}
	}
	return res
}

// err turns a failed or refused run into an error; nil means the CLI archived successfully.
func (o sessionRunOutcome) err() error {
	msg := o.stderr
	if o.report != nil && o.report.Error != nil && o.report.Error.Message != "" {
		msg = o.report.Error.Message
	}
	switch {
	case o.refsFailed:
		return errors.New(refProbeFailed)
	case !o.ran:
		return errors.New("会话归档脚本没有返回结果，未归档或删除任何会话")
	case o.exitCode == cliExitMissing:
		return errors.New("目标机器未安装 disklessly CLI，冷会话需由 disklessly 上传到备份后端后才能移除，未归档或删除任何会话")
	case o.exitCode == cliExitBackend:
		return fmt.Errorf("备份后端不可达，未上传或删除任何会话：%s", cmpOr(msg, "backend unreachable"))
	case o.exitCode != 0:
		return fmt.Errorf("disklessly sessions --apply 失败 (exit %d)，未确认的会话保持原样：%s", o.exitCode, cmpOr(msg, "unknown error"))
	case o.report == nil || !o.report.OK:
		return fmt.Errorf("disklessly sessions --apply 返回了无法解析的报告：%s", cmpOr(msg, "no JSON report"))
	}
	return nil
}

func cmpOr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return b
}

// underDir reports whether p lies inside directory dir.
func underDir(p, dir string) bool {
	if dir == "" || p == "" {
		return false
	}
	dir = path.Clean(dir)
	return strings.HasPrefix(path.Clean(p), dir+"/")
}

// underAny reports whether p lies under any non-empty dir of dirs (e.g. a tool directory as
// configured and with symlinks resolved).
func underAny(p string, dirs ...string) bool {
	return slices.ContainsFunc(dirs, func(d string) bool { return underDir(p, d) })
}

// archivedUnder returns the sessions of a run archived from any of dirs (one directory, as
// configured and resolved), and their total original bytes.
func (r *sessionReport) archivedUnder(dirs ...string) (int, uint64) {
	if r == nil {
		return 0, 0
	}
	var n int
	var bytes uint64
	for _, a := range r.Archived {
		if underAny(a.Path, dirs...) {
			n++
			bytes += a.Bytes
		}
	}
	return n, bytes
}

// archivedOutside returns the sessions of a run archived from none of dirs, and their bytes.
func (r *sessionReport) archivedOutside(dirs []string) (int, uint64) {
	if r == nil {
		return 0, 0
	}
	var n int
	var bytes uint64
	for _, a := range r.Archived {
		if !underAny(a.Path, dirs...) {
			n++
			bytes += a.Bytes
		}
	}
	return n, bytes
}

// skippedUnder counts the transcripts under any of dirs the run skipped, and those skipped as
// referenced.
func (r *sessionReport) skippedUnder(dirs ...string) (skipped, referenced int) {
	if r == nil {
		return 0, 0
	}
	for _, s := range r.Skipped {
		if underAny(s.Path, dirs...) {
			skipped++
			if s.Reason == "referenced" {
				referenced++
			}
		}
	}
	return skipped, referenced
}

// referenced counts the transcripts a run kept because they are still referenced.
func (r *sessionReport) referenced() int {
	if r == nil {
		return 0
	}
	n := 0
	for _, s := range r.Skipped {
		if s.Reason == "referenced" {
			n++
		}
	}
	for _, s := range r.KeptLocal {
		if s.Reason == "referenced" {
			n++
		}
	}
	return n
}

// parseCLIJSONLine decodes a single-line CLI JSON payload into v when it reports ok.
func parseCLIJSONLine(lines []string, v any) bool {
	raw := strings.TrimSpace(strings.Join(lines, " "))
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return false
	}
	var head struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal([]byte(raw), &head) != nil || !head.OK {
		return false
	}
	return json.Unmarshal([]byte(raw), v) == nil
}
