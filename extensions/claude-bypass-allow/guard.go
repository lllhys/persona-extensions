package main

import (
	"path"
	"regexp"
	"strings"
)

// The floor: Bash commands that stay with a person even in bypassPermissions mode, because they delete or overwrite
// data, force a push, or change permissions (local or online). When Claude Code asks about such a command, the hook
// stays silent and the person answers. It reads the command roughly as a shell would and errs toward asking: every
// part of a list, pipeline or substitution counts; output redirected into a file counts as overwriting it (except
// under /dev/ and /tmp/); a quoted argument is read again as a command when a shell runs it (bash -c '…', ssh host
// '…', eval) or when it holds a substitution ($(…), `…`); SQL counts when a SQL client is in the command. It skips
// free text: commit messages (git -m), pull request titles and bodies (gh --title, --body), and the body of a
// heredoc fed to anything but a shell.
//
// What it cannot see: a command hidden in a variable, a script file, base64 or another program's input.

// alwaysPerson are programs that delete, overwrite or change permissions whatever their arguments.
var alwaysPerson = map[string]string{
	"rm": "deletion", "rmdir": "deletion", "unlink": "deletion", "shred": "deletion", "srm": "deletion",
	"trash": "deletion", "trash-put": "deletion", "userdel": "deletion", "groupdel": "deletion",
	"dd": "overwrite", "truncate": "overwrite", "wipefs": "overwrite", "fdisk": "overwrite", "parted": "overwrite",
	"mv":     "overwrite",
	"rimraf": "deletion",
	"chmod":  "permission change", "chown": "permission change", "chgrp": "permission change",
	"chattr": "permission change", "setfacl": "permission change", "usermod": "permission change",
	"passwd": "permission change", "chpasswd": "permission change", "visudo": "permission change",
	"gpasswd": "permission change", "setcap": "permission change",
}

// shells run their arguments (or input) as commands.
var shells = map[string]bool{"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
	"eval": true, "ssh": true, "su": true, "watch": true}

// sqlClients run SQL given as an argument or on their input.
var sqlClients = map[string]bool{"psql": true, "mysql": true, "mariadb": true, "sqlite3": true, "sqlcmd": true,
	"clickhouse": true, "clickhouse-client": true, "duckdb": true, "cqlsh": true}

// sqlDestructive is SQL that deletes data or changes who may use it.
var sqlDestructive = regexp.MustCompile(`(?i)\b(drop\s+(table|database|schema|index|view|user|role)|truncate\b|delete\s+from|grant\b|revoke\b|alter\s+(user|role))`)

// onlinePermission is a path in a GitHub API call that is about who may do what.
var onlinePermission = regexp.MustCompile(`(?i)(collaborators|permissions|protection|/teams|/members|memberships|invitations|rulesets|/keys|secrets|/hooks|environments)`)

// heredocOp is what follows a heredoc's << (-EOF, 'EOF', "EOF"): the dash, and the delimiter.
var heredocOp = regexp.MustCompile(`^(-?)\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// personOnly says why cmd must stay with a person ("" when it need not).
func personOnly(cmd string) string { return personOnlyDepth(cmd, 0) }

func personOnlyDepth(cmd string, depth int) string {
	all, _ := scan(cmd)
	for _, words := range all {
		for _, w := range words {
			if sqlClients[path.Base(w)] && sqlDestructive.MatchString(cmd) {
				return "deletion or permission change (SQL)"
			}
		}
	}
	segs, redirects := scan(stripHeredocs(cmd))
	if len(redirects) > 0 {
		return "overwrite: > " + redirects[0]
	}
	for _, words := range segs {
		kept := withoutMessages(words)
		if why := segmentPersonOnly(kept); why != "" {
			return why
		}
		if depth >= 3 {
			continue
		}
		shell := false
		for _, w := range kept {
			shell = shell || shells[path.Base(w)]
		}
		for _, w := range kept {
			sub := strings.Contains(w, "$(") || strings.Contains(w, "`")
			if sub || (shell && strings.ContainsAny(w, " \t\n;&|()<>")) {
				if why := personOnlyDepth(w, depth+1); why != "" {
					return why
				}
			}
		}
	}
	return ""
}

// stripHeredocs takes out the body of each heredoc that is not fed to a shell: what cat, tee or python read is data.
// Only a << the shell reads as one starts a heredoc: not one inside quotes ('usage: x <<FILE'), a comment, or
// arithmetic ($((1<<n))), whose following lines are commands.
func stripHeredocs(cmd string) string {
	lines := strings.Split(cmd, "\n")
	var out []string
	var st heredocScan
	var pending []heredocStart
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		out = append(out, line)
		keep := false
		segs, _ := scan(line)
		for _, words := range segs {
			for _, w := range words {
				keep = keep || shells[path.Base(w)]
			}
		}
		for _, h := range st.line(line) {
			h.keep = keep
			pending = append(pending, h)
		}
		if st.single || st.double {
			continue // a quote goes on to the next line, and the bodies start after the line it ends on
		}
		for _, h := range pending {
			for i+1 < len(lines) {
				i++
				end := lines[i]
				if h.dash {
					end = strings.TrimLeft(end, "\t")
				}
				if end == h.delim {
					out = append(out, lines[i])
					break
				}
				if h.keep {
					out = append(out, lines[i])
				}
			}
		}
		pending = nil
	}
	return strings.Join(out, "\n")
}

// heredocStart is a heredoc operator: its delimiter, whether it was <<- (leading tabs stripped), and whether its
// line feeds a shell (its body is then commands).
type heredocStart struct {
	delim      string
	dash, keep bool
}

// heredocScan reads a command line by line for heredoc operators, carrying quotes and arithmetic across lines.
type heredocScan struct {
	single, double bool
	arith          int // depth of (( … )) and $(( … ))
}

// line is the heredoc operators on one line that the shell reads as operators.
func (st *heredocScan) line(line string) []heredocStart {
	var found []heredocStart
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case st.single:
			st.single = r != '\''
		case r == '\\':
			i++ // the next character is escaped, in double quotes too
		case st.double:
			st.double = r != '"'
		case r == '\'':
			st.single = true
		case r == '"':
			st.double = true
		case r == '#' && (i == 0 || strings.ContainsRune(" \t;&|()", rs[i-1])):
			return found // a comment, to the end of the line
		case r == '(' && i+1 < len(rs) && rs[i+1] == '(':
			st.arith++
			i++
		case r == ')' && i+1 < len(rs) && rs[i+1] == ')' && st.arith > 0:
			st.arith--
			i++
		case r == '<' && st.arith == 0 && i+1 < len(rs) && rs[i+1] == '<':
			if i+2 < len(rs) && rs[i+2] == '<' {
				i += 2 // a here-string
				continue
			}
			i++
			if m := heredocOp.FindStringSubmatch(string(rs[i+1:])); m != nil {
				found = append(found, heredocStart{delim: m[2], dash: m[1] == "-"})
			}
		}
	}
	return found
}

// withoutMessages drops free text: what follows git's -m and --message, and gh's --title, --body, --notes, -t, -b
// and -n; text that holds a substitution ($(…), `…`) is kept, since the shell runs it.
func withoutMessages(words []string) []string {
	prog := ""
	out := make([]string, 0, len(words))
	for i := 0; i < len(words); i++ {
		w := words[i]
		if b := path.Base(w); b == "git" || b == "gh" {
			prog = b
		}
		flag, val, inline := w, "", false
		if k, v, ok := strings.Cut(w, "="); ok && strings.HasPrefix(k, "--") {
			flag, val, inline = k, v, true
		}
		if !isMessageFlag(prog, flag) {
			out = append(out, w)
			continue
		}
		out = append(out, flag)
		if !inline && i+1 < len(words) {
			i++
			val = words[i]
		}
		if strings.Contains(val, "$(") || strings.Contains(val, "`") {
			out = append(out, val)
		}
	}
	return out
}

func isMessageFlag(prog, flag string) bool {
	switch flag {
	case "-m", "--message":
		return prog == "git"
	case "--title", "--body", "--notes", "-t", "-b", "-n":
		return prog == "gh"
	}
	return false
}

// scan splits cmd into the words of each simple command, as a shell would group them: quotes keep a word together
// (and are removed), a backslash escapes the next character, and lists, pipes, subshells, groups and command
// substitutions separate. redirects are the files output is written to with >, >|, N> or &> (not >>, not >&N),
// outside /dev/ and /tmp/.
func scan(cmd string) (segs [][]string, redirects []string) {
	var words []string
	var cur strings.Builder
	inWord, single, double := false, false, false
	endWord := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	endSegment := func() {
		endWord()
		if len(words) > 0 {
			segs = append(segs, words)
			words = nil
		}
	}
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case single:
			if r == '\'' {
				single = false
			} else {
				cur.WriteRune(r)
			}
		case r == '\\' && i+1 < len(rs):
			i++
			if rs[i] != '\n' {
				cur.WriteRune(rs[i])
				inWord = true
			}
		case double:
			if r == '"' {
				double = false
			} else {
				cur.WriteRune(r)
			}
		case r == '\'':
			single, inWord = true, true
		case r == '"':
			double, inWord = true, true
		case r == '>':
			if inWord && isDigits(cur.String()) {
				cur.Reset()
				inWord = false
			}
			endWord()
			var target string
			if i, target = redirectTarget(rs, i); target != "" {
				redirects = append(redirects, target)
			}
		case r == '<':
			endWord()
		case strings.ContainsRune(";&|\n()`{}", r):
			endSegment()
		case r == ' ' || r == '\t' || r == '\r':
			endWord()
		case r == '$' && i+1 < len(rs) && rs[i+1] == '(':
			endSegment()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	endSegment()
	return segs, redirects
}

// redirectTarget reads the redirect whose > is at rs[i]: the file it overwrites ("" for >>, >&N, /dev/ and /tmp/),
// and the index of its last character.
func redirectTarget(rs []rune, i int) (int, string) {
	appendTo := false
	if i+1 < len(rs) && rs[i+1] == '>' {
		appendTo = true
		i++
	}
	if i+1 < len(rs) && rs[i+1] == '|' {
		i++
	}
	if i+1 < len(rs) && rs[i+1] == '&' {
		i++
		if i+1 < len(rs) && (rs[i+1] == '-' || (rs[i+1] >= '0' && rs[i+1] <= '9')) {
			return i + 1, ""
		}
	}
	for i+1 < len(rs) && (rs[i+1] == ' ' || rs[i+1] == '\t') {
		i++
	}
	var t strings.Builder
	for i+1 < len(rs) && !strings.ContainsRune(" \t\n;&|()<>", rs[i+1]) {
		i++
		if rs[i] != '\'' && rs[i] != '"' {
			t.WriteRune(rs[i])
		}
	}
	target := t.String()
	if appendTo || target == "" || scratch(target) {
		return i, ""
	}
	return i, target
}

// scratch says whether a file is one nobody keeps: under /dev/ or /tmp/.
func scratch(f string) bool { return strings.HasPrefix(f, "/dev/") || strings.HasPrefix(f, "/tmp/") }

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// wrappers run the next program word as a command (sudo rm, xargs rm, find -exec rm, for …; do rm …), so after one
// every word counts as a possible program; shell keywords are here too.
var wrappers = map[string]bool{
	"sudo": true, "doas": true, "xargs": true, "parallel": true, "env": true, "nice": true, "ionice": true, "nohup": true,
	"time": true, "timeout": true, "exec": true, "command": true, "builtin": true, "stdbuf": true, "setsid": true,
	"flock": true, "chroot": true, "runuser": true, "strace": true, "unbuffer": true, "watch": true,
	"npx": true, "pnpx": true, "bunx": true, "dlx": true, "uvx": true, "pipx": true,
	"-exec": true, "-execdir": true, "-ok": true, "-okdir": true,
	"do": true, "then": true, "else": true, "elif": true, "if": true, "while": true, "until": true, "!": true,
}

// segmentPersonOnly looks at one simple command. A word counts as a program when it comes first (after NAME=value
// assignments) or after a wrapper; so `grep -rn truncate src/` and `cd /tmp/rm` go through, `sudo -b rm x` does not.
func segmentPersonOnly(words []string) string {
	first, wrapped := true, false
	for i, w := range words {
		if first && strings.Contains(w, "=") && !strings.HasPrefix(w, "-") {
			continue
		}
		program := first || wrapped
		first = false
		name := path.Base(w)
		if name == "command" && program && i+1 < len(words) && (words[i+1] == "-v" || words[i+1] == "-V") {
			return "" // command -v rm only looks rm up
		}
		if wrappers[w] || (program && wrappers[name]) {
			wrapped = true
			continue
		}
		if !program {
			continue
		}
		if why, ok := alwaysPerson[name]; ok {
			return why + ": " + name
		}
		if strings.HasPrefix(name, "mkfs") {
			return "overwrite: " + name
		}
		rest := words[i+1:]
		var why string
		switch name {
		case "git": // its arguments are not programs (git mv, git rm are git's own)
			return gitPersonOnly(rest)
		case "gh":
			return ghPersonOnly(rest)
		case "find": // what -exec runs is checked as the words after it
			if has(rest, "-delete") {
				why = "deletion: find -delete"
			}
		case "rsync":
			if hasPrefix(rest, "--del") || has(rest, "--remove-source-files") {
				why = "deletion: rsync --delete"
			}
		case "sed", "perl":
			if hasPrefix(rest, "--in-place") || hasShort(rest, 'i') {
				why = "overwrite: " + name + " -i"
			}
		case "ln":
			if has(rest, "--force") || hasShort(rest, 'f') {
				why = "overwrite: ln -f"
			}
		case "install":
			if hasShort(rest, 'm') || hasShort(rest, 'o') || hasShort(rest, 'g') || hasPrefix(rest, "--mode") || hasPrefix(rest, "--owner") || hasPrefix(rest, "--group") {
				why = "permission change: install -m"
			}
		case "cp":
			if has(rest, "--force") || has(rest, "--remove-destination") || hasShort(rest, 'f') {
				why = "overwrite: cp -f"
			}
		case "tee":
			if !has(rest, "--append") && !hasShort(rest, 'a') {
				for _, f := range rest {
					if !strings.HasPrefix(f, "-") && !scratch(f) {
						why = "overwrite: tee " + f
						break
					}
				}
			}
		case "docker", "podman":
			if hasAny(rest, "rm", "rmi", "prune") || (has(rest, "down") && (has(rest, "--volumes") || hasShort(rest, 'v'))) {
				why = "deletion: " + name
			}
		case "kubectl", "helm":
			if hasAny(rest, "delete", "uninstall", "drain") {
				why = "deletion: " + name
			} else if hasAny(rest, "create", "apply", "edit", "patch", "replace", "set") &&
				hasAny(rest, "role", "rolebinding", "clusterrole", "clusterrolebinding", "serviceaccount", "sa") {
				why = "permission change: " + name
			}
		case "terraform", "tofu", "pulumi":
			if has(rest, "destroy") || has(rest, "-destroy") {
				why = "deletion: " + name + " destroy"
			}
		case "aws":
			switch {
			case has(rest, "iam") || hasPrefixContaining(rest, "put-", "policy") || hasPrefixContaining(rest, "put-", "acl"):
				why = "permission change: aws"
			case hasPrefix(rest, "delete-") || hasPrefix(rest, "terminate-") || has(rest, "--delete") || (has(rest, "s3") && hasAny(rest, "rm", "rb")):
				why = "deletion: aws"
			}
		case "gcloud", "gsutil":
			for _, r := range rest {
				if strings.Contains(r, "iam-policy") || r == "iam" || r == "acl" {
					why = "permission change: " + name
				}
			}
			if why == "" && hasAny(rest, "delete", "rm", "rb") {
				why = "deletion: " + name
			}
		case "az":
			if has(rest, "role") {
				why = "permission change: az role"
			} else if has(rest, "delete") {
				why = "deletion: az"
			}
		}
		if why != "" {
			return why
		}
	}
	return ""
}

// gitPersonOnly looks at what follows git: its subcommand (after -C, -c and other global flags) and that one's
// arguments.
func gitPersonOnly(args []string) string {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		if args[i] == "-C" || args[i] == "-c" || args[i] == "--git-dir" || args[i] == "--work-tree" {
			i++
		}
		i++
	}
	if i >= len(args) {
		return ""
	}
	sub, rest := args[i], args[i+1:]
	switch sub {
	case "push":
		for _, a := range rest {
			if strings.HasPrefix(a, "--force") || a == "--mirror" || a == "--delete" || a == "--prune" ||
				strings.HasPrefix(a, "+") || (strings.HasPrefix(a, ":") && len(a) > 1) || shortFlag(a, 'f') || shortFlag(a, 'd') {
				return "force push: git push " + a
			}
		}
	case "clean", "rm", "filter-branch", "filter-repo":
		return "deletion: git " + sub
	case "restore":
		return "overwrite: git restore"
	case "reset":
		if has(rest, "--hard") || has(rest, "--merge") || has(rest, "--keep") {
			return "overwrite: git reset --hard"
		}
	case "checkout", "switch":
		if has(rest, "--") || has(rest, ".") || has(rest, "--force") || has(rest, "--discard-changes") || hasShort(rest, 'f') {
			return "overwrite: git " + sub
		}
	case "branch", "tag":
		if has(rest, "--delete") || has(rest, "--force") || hasShort(rest, 'd') || hasShort(rest, 'D') || hasShort(rest, 'f') || hasShort(rest, 'M') {
			return "deletion or overwrite: git " + sub
		}
	case "stash":
		if hasAny(rest, "drop", "clear") {
			return "deletion: git stash drop"
		}
	case "worktree":
		if hasAny(rest, "remove", "prune") {
			return "deletion: git worktree remove"
		}
	case "update-ref":
		if has(rest, "-d") {
			return "deletion: git update-ref -d"
		}
	case "reflog":
		if hasAny(rest, "expire", "delete") {
			return "deletion: git reflog"
		}
	}
	return ""
}

// ghPersonOnly looks at what follows gh.
func ghPersonOnly(args []string) string {
	if has(args, "delete") || has(args, "--delete-branch") || hasPrefix(args, "--delete") {
		return "deletion: gh"
	}
	if len(args) == 0 {
		return ""
	}
	switch args[0] {
	case "secret", "variable", "ssh-key", "gpg-key":
		return "permission change: gh " + args[0]
	case "repo":
		if len(args) > 1 && (args[1] == "deploy-key" || args[1] == "archive" || (args[1] == "edit" && hasPrefix(args, "--visibility"))) {
			return "permission change: gh repo " + args[1]
		}
	case "api":
		method := "GET"
		for i, a := range args {
			switch {
			case (a == "-X" || a == "--method") && i+1 < len(args):
				method = strings.ToUpper(args[i+1])
			case strings.HasPrefix(a, "-X") && len(a) > 2:
				method = strings.ToUpper(a[2:])
			case strings.HasPrefix(a, "--method="):
				method = strings.ToUpper(strings.TrimPrefix(a, "--method="))
			case a == "-f" || a == "-F" || a == "--field" || a == "--raw-field" || a == "--input" ||
				strings.HasPrefix(a, "--field=") || strings.HasPrefix(a, "--raw-field=") || strings.HasPrefix(a, "--input="):
				if method == "GET" {
					method = "POST"
				}
			}
		}
		if method == "DELETE" {
			return "deletion: gh api DELETE"
		}
		if has(args, "graphql") {
			for _, a := range args {
				if strings.Contains(strings.ToLower(a), "mutation") {
					return "online change: gh api graphql mutation"
				}
			}
		}
		if method != "GET" && method != "HEAD" {
			for _, a := range args {
				v := strings.ToLower(a)
				for _, p := range []string{"--field=", "--raw-field=", "-f", "-F"} {
					v = strings.TrimPrefix(v, strings.ToLower(p))
				}
				if strings.HasPrefix(v, "visibility=") || strings.HasPrefix(v, "private=") {
					return "permission change: gh api " + a
				}
			}
		}
		if method != "GET" && method != "HEAD" {
			for _, a := range args[1:] {
				if !strings.HasPrefix(a, "-") && onlinePermission.MatchString(a) {
					return "permission change: gh api " + method
				}
			}
		}
	}
	return ""
}

func has(words []string, w string) bool { return hasAny(words, w) }

func hasAny(words []string, ws ...string) bool {
	for _, x := range words {
		for _, w := range ws {
			if x == w {
				return true
			}
		}
	}
	return false
}

// hasPrefixContaining says whether a word starts with p and has c in it (put-bucket-policy).
func hasPrefixContaining(words []string, p, c string) bool {
	for _, x := range words {
		if strings.HasPrefix(x, p) && strings.Contains(x, c) {
			return true
		}
	}
	return false
}

func hasPrefix(words []string, p string) bool {
	for _, x := range words {
		if strings.HasPrefix(x, p) {
			return true
		}
	}
	return false
}

// shortFlag says whether a is a cluster of one-letter flags (-f, -fu) that has c.
func shortFlag(a string, c byte) bool {
	if len(a) < 2 || a[0] != '-' || a[1] == '-' {
		return false
	}
	return strings.IndexByte(a[1:], c) >= 0
}

func hasShort(words []string, c byte) bool {
	for _, x := range words {
		if shortFlag(x, c) {
			return true
		}
	}
	return false
}
