package main

import (
	"errors"
	"strconv"
	"strings"
)

// The reference probe lists the sessions something on the target machine still uses, so a cold
// session run never uploads and deletes a transcript an agent may resume or is writing:
//
//   - running processes (Linux /proc/<pid>/cmdline, macOS `ps -axww -o pid= -o args=`): the UUID
//     after --resume / -r / --session-id, and every UUID in a claude or codex command line (so
//     `codex resume [OPTIONS] <id>` and `codex exec resume` count); a claude or codex process
//     without one (claude -c, the --resume picker, codex resume --last) keeps every transcript
//     of its working directory: the Claude project folder for that cwd, Codex rollouts whose
//     session_meta names that cwd (cwd from /proc/<pid>/cwd or `lsof -d cwd`);
//   - open transcripts (Linux /proc/<pid>/fd, macOS `lsof`): every open *.jsonl;
//   - herdr: every UUID and absolute *.jsonl path in ~/.config/herdr/session.json (Persona keeps
//     resumable agents there as agent_resume argv `--resume <id>`) and in `herdr pane list`.
//
// It prints one session id or transcript path per line. Over-inclusion only keeps a file local,
// so it is harmless. When neither /proc nor ps can list processes, it returns 3 and the caller
// aborts before the disklessly CLI runs (fail closed). DISKLESSLY_PROC_ROOT overrides /proc for tests.
const refProbeFunc = `__dkl_refs() {
  __p="${DISKLESSLY_PROC_ROOT:-/proc}"
  __h="[0-9a-fA-F]"
  __args=$(mktemp "${TMPDIR:-/tmp}/dkl-refs.XXXXXX") || return 3
  __seen=0
  if [ -r "$__p/self/cmdline" ]; then
    for __f in "$__p"/[0-9]*/cmdline; do
      [ -r "$__f" ] || continue
      __pid=${__f%/cmdline}
      printf '%s ' "${__pid##*/}"
      tr '\000' ' ' < "$__f" 2>/dev/null
      echo
    done > "$__args"
    [ -s "$__args" ] && __seen=1
  fi
  if [ "$__seen" = 0 ] && ps -axww -o pid= -o args= > "$__args" 2>/dev/null && [ -s "$__args" ]; then
    __seen=1
  fi
  if [ "$__seen" = 0 ]; then
    rm -f "$__args"
    return 3
  fi
  awk -v h="$__h" -v self="$$" '
    BEGIN {
      h4 = h h h h; h8 = h4 h4; h12 = h8 h4
      uuid = "^" h8 "-" h4 "-" h4 "-" h4 "-" h12 "$"
    }
    function emit(t) { if (t ~ uuid) { print t; found = 1 } }
    $1 == self || /__dkl_refs/ { next }
    {
      found = 0; tool = ""
      for (i = 2; i <= NF; i++) {
        if ($i ~ /(^|\/)claude(\.js|\.exe)?$/ || $i ~ /claude-code\/cli\.js$/) tool = "claude"
        else if ($i ~ /(^|\/)codex(\.js|\.exe)?$/) tool = "codex"
      }
      for (i = 2; i <= NF; i++) {
        a = $i
        if ((a == "--resume" || a == "-r" || a == "--session-id") && i < NF) emit($(i + 1))
        else if (a ~ /^--resume=/) emit(substr(a, 10))
        else if (a ~ /^--session-id=/) emit(substr(a, 14))
        else if (tool != "") emit(a)
      }
      # An agent without a session id in its argv (claude -c, the --resume picker, codex
      # resume --last, a fresh session) may hold any transcript of its working directory.
      if (tool != "" && !found) print "@@AGENT " $1 " " tool
    }' "$__args" > "$__args.out"
  rm -f "$__args"
  while IFS= read -r __l; do
    case "$__l" in
      "@@AGENT "*) ;;
      *) printf '%s\n' "$__l"; continue ;;
    esac
    set -- $__l
    __cwd=""
    if [ -d "$__p/$2" ]; then
      __cwd=$(readlink "$__p/$2/cwd" 2>/dev/null)
    elif command -v lsof >/dev/null 2>&1; then
      __cwd=$(lsof -a -p "$2" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p' | head -n 1)
    fi
    case "$__cwd" in /*) ;; *) continue ;; esac
    if [ "$3" = claude ]; then
      __key=$(printf '%s' "$__cwd" | sed 's/[^A-Za-z0-9]/-/g')
      for __t in "$HOME/.claude/projects/$__key"/*.jsonl; do
        [ -f "$__t" ] && printf '%s\n' "$__t"
      done
    elif [ -d "$HOME/.codex/sessions" ]; then
      find "$HOME/.codex/sessions" -type f -name 'rollout-*.jsonl' 2>/dev/null | while IFS= read -r __t; do
        head -n 1 "$__t" 2>/dev/null | grep -qF "\"cwd\":\"$__cwd\"" && printf '%s\n' "$__t"
      done
    fi
  done < "$__args.out"
  rm -f "$__args.out"
  if [ -d "$__p/self/fd" ]; then
    ls -l "$__p"/[0-9]*/fd/ 2>/dev/null | sed -n 's/.* -> \(\/.*\.jsonl\)$/\1/p'
  elif command -v lsof >/dev/null 2>&1; then
    lsof -w -nP -Fn -u "$(id -u)" 2>/dev/null | sed -n 's/^n\(\/.*\.jsonl\)$/\1/p'
  fi
  __hs="$HOME/.config/herdr/session.json"
  __ho=""
  if command -v herdr >/dev/null 2>&1; then
    __ho=$(mktemp "${TMPDIR:-/tmp}/dkl-herdr.XXXXXX") || __ho=""
    if [ -n "$__ho" ]; then
      herdr pane list > "$__ho" 2>/dev/null < /dev/null &
      __hp=$!
      ( sleep 5; kill "$__hp" 2>/dev/null ) > /dev/null 2>&1 &
      __kp=$!
      wait "$__hp" 2>/dev/null
      kill "$__kp" 2>/dev/null
    fi
  fi
  for __src in "$__hs" "$__ho"; do
    [ -n "$__src" ] && [ -f "$__src" ] || continue
    grep -Eo "$__h{8}-$__h{4}-$__h{4}-$__h{4}-$__h{12}" "$__src" 2>/dev/null
    grep -Eo '/[^"[:space:]]*\.jsonl' "$__src" 2>/dev/null
  done
  [ -n "$__ho" ] && rm -f "$__ho"
  return 0
}`

// refProbeFailed is the message of a run aborted because no process listing was available.
const refProbeFailed = "无法列出目标机器上运行中的进程（/proc 与 ps 都不可用），不能确认哪些会话仍被引用；已中止，未上传或删除任何会话"

// ReferencedSessionsScript runs the reference probe on its own: `@@REF <id or path>` per
// reference, then `@@REFS_OK`, or `@@REFS_FAIL` when processes cannot be listed.
const ReferencedSessionsScript = refProbeFunc + `
__rf=$(mktemp "${TMPDIR:-/tmp}/dkl-reflist.XXXXXX") || { echo "@@REFS_FAIL"; exit 0; }
if __dkl_refs > "$__rf" 2>/dev/null; then
  sort -u "$__rf" | sed '/^$/d; s/^/@@REF /'
  echo "@@REFS_OK"
else
  echo "@@REFS_FAIL"
fi
rm -f "$__rf"
`

// refProbeArchiveStep appends the probe's references to the exclusion file $EXF, or aborts the
// archive script before the CLI runs when processes cannot be listed.
const refProbeArchiveStep = `if ! __dkl_refs > "$EXF.refs" 2>/dev/null; then
  echo "@@REFS_FAIL"
  rm -f "$EXF" "$EXF.refs"
  exit 0
fi
echo "@@REFS $(sort -u "$EXF.refs" | grep -c .)"
cat "$EXF.refs" >> "$EXF"
rm -f "$EXF.refs"
`

// parseReferences reads the output of ReferencedSessionsScript.
func parseReferences(out string) ([]string, error) {
	var refs []string
	ok := false
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.HasPrefix(line, "@@REF "):
			if r := strings.TrimSpace(strings.TrimPrefix(line, "@@REF ")); r != "" {
				refs = append(refs, r)
			}
		case line == "@@REFS_OK":
			ok = true
		case line == "@@REFS_FAIL":
			return nil, errors.New(refProbeFailed)
		}
	}
	if !ok {
		return nil, errors.New("引用探测没有返回结果，未上传或删除任何会话")
	}
	return refs, nil
}

func parseRefsCount(line string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "@@REFS ")))
	return n, err == nil
}
