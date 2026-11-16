package main

import (
	"cmp"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// What the pages, toasts and MCP tools say when something is wrong: which machine, one sentence
// of cause, the next step (a button or a command to copy), and the raw text (an SSH line, a CLI
// answer, Homebrew's output) kept apart for 详情 so the main text stays readable.

type problem struct {
	Cause   string // the classified cause, for SSH failures
	Title   string // a few words, for badges and the overview card
	Reason  string // one sentence naming the machine
	Next    string // what to do
	Command string // a command to copy, if one helps
	Raw     string // the untranslated text, shown under 详情
	Retry   bool   // trying again may help
	Edit    bool   // the machine's SSH settings may be wrong
}

// text is the reason and the next step, for a row's detail.
func (p problem) text() string { return p.Reason + p.Next }

// message is the text with the command, for toasts and MCP answers.
func (p problem) message() string {
	if p.Command == "" {
		return p.text()
	}
	return p.text() + "命令：" + strings.ReplaceAll(p.Command, "\n", "；")
}

// More causes of an SSH failure, besides those in coldstate.go.
const (
	causeHostKey   = "host_key"   // the host key is unknown or changed
	causeSSHConfig = "ssh_config" // OpenSSH refuses a config file on the hub
	causeSandbox   = "sandbox"    // the extension runs in the hub's sandbox: no network, no ~/.ssh
	causeBadHost   = "bad_host"   // the host name is not one this extension passes to ssh
)

// sandboxed reports whether the hub runs this extension in its sandbox. Behind the sandbox's
// egress proxy the hub's own API is reached as hub.persona.internal; outside it, at its address.
var sandboxed = func() bool {
	for _, k := range []string{"PERSONA_HUB_URL", "JARVIS_HUB_URL"} {
		if u, err := url.Parse(os.Getenv(k)); err == nil && u.Hostname() == "hub.persona.internal" {
			return true
		}
	}
	return false
}

const sandboxOffCommand = "personad extension sandbox disklessly off"

// sandboxProblem is what the pages say first while the extension runs in the hub's sandbox.
func sandboxProblem() problem {
	return problem{
		Cause:   causeSandbox,
		Title:   "扩展在沙箱里",
		Reason:  "Disklessly 现在运行在 hub 的沙箱里：沙箱里没有网络，也读不到 hub 上的 ~/.ssh 和本机的会话目录，所以连不上任何远程电脑，本机的结果也不准。",
		Next:    "在 hub 所在的电脑上运行下面的命令关掉这个扩展的沙箱（从官方市场安装时默认就是关的），然后点「重试」或「扫描全部机器」。",
		Command: sandboxOffCommand,
		Retry:   true,
	}
}

// sshTarget is the ssh command line's target for m: options, then [user@]host.
func sshTarget(m *Machine) string {
	var b strings.Builder
	if m.Port > 0 && m.Port != 22 {
		b.WriteString("-p " + strconv.Itoa(m.Port) + " ")
	}
	if m.IdentityFile != "" {
		b.WriteString("-i " + shellPath(m.IdentityFile) + " ")
	}
	host := cmp.Or(m.Host, m.ID)
	if m.User != "" && !strings.Contains(host, "@") {
		host = m.User + "@" + host
	}
	b.WriteString(host)
	return b.String()
}

// shellPath quotes p for a shell command line, leaving a leading ~/ to expand.
func shellPath(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if rest == "" {
			return "~/"
		}
		return "~/" + shellQuote(rest)
	}
	return shellQuote(p)
}

var (
	connectRE   = regexp.MustCompile(`connect to host (\S+) port (\d+)`)
	sshFailRE   = regexp.MustCompile(`SSH 连接 (\S+) 失败：`)
	localFailRE = regexp.MustCompile(`本地探测命令执行失败：`)
	sshConfigRE = regexp.MustCompile(`(?i)bad owner or permissions on (\S+)`)
)

// sshProblem words a failed run on machine m; raw is the error text as runError writes it
// ("SSH 连接 <host> 失败：<OpenSSH's last line>"), also as stored in older state files.
func sshProblem(m *Machine, raw string) problem {
	name := "「" + machineLabel(m) + "」"
	if loc := localFailRE.FindStringIndex(raw); loc != nil || m.IsLocal {
		line := raw
		if loc != nil {
			line = raw[loc[1]:]
		}
		return problem{Title: "本机检查失败", Reason: "在本机运行检查脚本失败。", Next: "点「重试」；还是失败时打开「详情」看脚本的输出。", Raw: strings.TrimSpace(line), Retry: true}
	}
	line := raw
	if loc := sshFailRE.FindStringIndex(raw); loc != nil {
		line = raw[loc[1]:]
	}
	line = strings.TrimSpace(line)
	test := "ssh -o BatchMode=yes -o ConnectTimeout=10 " + sshTarget(m) + " true"
	p := problem{Raw: line, Retry: true, Cause: sshCause(line)}
	if strings.Contains(raw, "非法 SSH 主机名") {
		p.Cause = causeBadHost
	}
	if sandboxed() {
		p.Cause = causeSandbox
	}
	switch p.Cause {
	case causeSandbox:
		s := sandboxProblem()
		p.Title, p.Command = s.Title, s.Command
		p.Reason = "连不上" + name + "：Disklessly 运行在 hub 的沙箱里，沙箱里没有网络也读不到 ~/.ssh，这台电脑本身可能没有问题。"
		p.Next = "在 hub 所在的电脑上运行下面的命令关掉这个扩展的沙箱，然后点「重试」。"
	case causeTimeout:
		p.Title = "连接超时"
		p.Reason = "连接" + name + "超时：它可能关机、休眠，或不在同一个网络里（例如 VPN 或 Tailscale 没连上）。"
		p.Next = "开机或连上网络后点「重试」；也可以在 hub 上运行下面的命令自己试一下。"
		p.Command = test
	case causeAuth:
		p.Title, p.Edit = "SSH 登录被拒绝", true
		p.Reason = name + "拒绝了 SSH 登录：它不接受 hub 的密钥，或者用户名不对。"
		p.Next = "在 hub 上运行下面的命令把公钥装到这台电脑，或在「机器磁盘」页「编辑当前机器节点」里改用户名和私钥，然后点「重试」。"
		p.Command = "ssh-copy-id " + sshTarget(m)
	case causeHostKey:
		p.Title = "主机指纹未确认"
		p.Reason = "hub 还没有信任" + name + "的主机指纹（或者指纹变了），自动登录被拦下。"
		p.Next = "在 hub 上运行下面的命令登录一次、核对并接受指纹，然后点「重试」。"
		p.Command = "ssh " + sshTarget(m)
	case causeRefused:
		p.Title, p.Edit = "连接被拒绝", true
		p.Command = test
		if mm := connectRE.FindStringSubmatch(line); mm != nil && isLoopback(mm[1]) {
			// The machine is reached through a port forward on the hub, and nothing listens there.
			p.Title, p.Edit = "端口转发没有运行", false
			p.Reason = name + "要经过 hub 本机的端口转发（" + mm[1] + ":" + mm[2] + "）才能连上，但这个端口现在没有程序在监听，转发（例如 ssh -L、autossh 或隧道服务）没有在运行。"
			p.Next = "在 hub 上把到这台电脑的转发重新建起来，然后点「重试」；可以用下面的命令确认能不能连上。"
			break
		}
		p.Reason = name + "拒绝了连接：它的 SSH 服务没有打开，或者端口不对。"
		switch plat := m.Platform; {
		case strings.HasPrefix(plat, "darwin"):
			p.Next = "在这台 Mac 上打开 SSH（系统设置 › 通用 › 共享 › 远程登录），或在「编辑当前机器节点」里改端口，然后点「重试」。"
		case strings.HasPrefix(plat, "linux"):
			p.Next = "在这台电脑上启动 SSH 服务（sudo systemctl enable --now sshd，Debian / Ubuntu 上服务名是 ssh），或在「编辑当前机器节点」里改端口，然后点「重试」。"
		default:
			p.Next = "在这台电脑上打开 SSH（macOS：系统设置 › 通用 › 共享 › 远程登录；Linux：启动 sshd 服务），或在「编辑当前机器节点」里改端口，然后点「重试」。"
		}
	case causeDNS:
		p.Title, p.Edit = "找不到主机名", true
		p.Reason = "hub 找不到" + name + "的主机名 " + cmp.Or(m.Host, m.ID) + "。"
		p.Next = "检查 hub 上 ~/.ssh/config 里它的 HostName，或在「编辑当前机器节点」里改成能解析的地址，然后点「重试」；下面的命令会显示 ssh 实际用的地址。"
		p.Command = "ssh -G " + cmp.Or(m.Host, m.ID) + " | grep -i '^hostname'"
	case causeNetwork:
		p.Title = "网络不通"
		p.Reason = "hub 到" + name + "的网络不通。"
		p.Next = "确认两台电脑在同一个网络或 VPN 里，然后点「重试」。"
		p.Command = test
	case causeSSHConfig:
		file := "/etc/ssh/ssh_config"
		if mm := sshConfigRE.FindStringSubmatch(line); mm != nil {
			file = strings.TrimRight(mm[1], ".:")
		}
		p.Title = "hub 的 SSH 配置有问题"
		p.Reason = "连不上" + name + "：hub 上的 SSH 配置文件 " + file + " 属主或权限不对，ssh 拒绝读取它，所以连不上任何电脑。"
		p.Next = "在 hub 上运行下面的命令改好它，然后点「重试」。"
		p.Command = "sudo chown root:root " + file + " && sudo chmod 644 " + file
	case causeBadHost:
		p.Title, p.Edit, p.Retry = "主机名无效", true, false
		p.Reason = name + "的主机名不是有效的 SSH 地址。"
		p.Next = "在「机器磁盘」页「编辑当前机器节点」里改成 ~/.ssh/config 里的 Host 别名或地址。"
	default:
		p.Title = "SSH 连接失败"
		p.Reason = "SSH 连接" + name + "失败。"
		p.Next = "在 hub 上运行下面的命令看看具体原因（原始输出在「详情」里），修好后点「重试」。"
		p.Command = test
	}
	return p
}

// isLoopback reports this computer's own address.
func isLoopback(host string) bool {
	h := strings.Trim(host, "[]")
	return h == "localhost" || h == "::1" || strings.HasPrefix(h, "127.")
}

// humanize rewrites the SSH or local-command failure inside an error text (it may be wrapped,
// "从 X 复制安装失败：读取 X 上的 CLI 失败：SSH 连接 X 失败：…") into plain words, and returns
// the raw part separately. A text without one comes back as it is.
func humanize(s string) (msg, raw string) {
	if loc := sshFailRE.FindStringSubmatchIndex(s); loc != nil {
		host := s[loc[2]:loc[3]]
		p := sshProblem(&Machine{ID: host, Host: host, Name: host}, s[loc[0]:])
		return s[:loc[0]] + p.text(), p.Raw
	}
	if loc := localFailRE.FindStringIndex(s); loc != nil {
		p := sshProblem(&Machine{ID: "local", IsLocal: true}, s[loc[0]:])
		return s[:loc[0]] + p.text(), p.Raw
	}
	return s, ""
}

// detailError is an error whose message is for people; raw is what the CLI or the tool said.
type detailError struct{ msg, raw string }

func (e *detailError) Error() string { return e.msg }

func rawError(msg, raw string) error { return &detailError{msg: msg, raw: strings.TrimSpace(raw)} }

// humanizeErr is humanize for an error, with a detailError's raw part.
func humanizeErr(err error) (msg, raw string) {
	if err == nil {
		return "", ""
	}
	msg, raw = humanize(err.Error())
	var de *detailError
	if errors.As(err, &de) && de.raw != "" {
		raw = strings.TrimSpace(strings.Join([]string{raw, de.raw}, "\n"))
	}
	return msg, raw
}

// userMessage is an error's text for a toast or an action error (no raw part).
func userMessage(err error) string {
	msg, _ := humanizeErr(err)
	return msg
}

// toolErrorText is a failing MCP tool call's text: the plain message, then 详情 on its own line.
func toolErrorText(err error) string {
	msg, raw := humanizeErr(err)
	if raw == "" {
		return msg
	}
	return msg + "\n详情：" + raw
}

// ---- Homebrew ----

const (
	brewTap     = "nwverse/tap"
	brewFormula = "disklessly-cli"
	// brewFQ is the formula's full name: another tap may carry a disklessly-cli too, and then
	// Homebrew refuses the short name ("Formulae found in multiple taps").
	brewFQ            = brewTap + "/" + brewFormula
	brewInstallCmd    = "brew tap " + brewTap + " && brew install " + brewFQ
	brewUpgradeCmd    = "brew tap " + brewTap + " && brew upgrade " + brewFQ
	brewSetupComment  = "# 先安装 Homebrew（https://brew.sh），装完按它最后的提示把 brew 加进 PATH"
	brewSetupCommand  = `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`
	brewLinuxShellEnv = `eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"`
)

// findBrewScript sets $BREW to the machine's Homebrew, or "" (a non-interactive SSH shell often
// lacks Homebrew's directory on PATH, so the usual prefixes are searched too).
const findBrewScript = `BREW=""
for c in "$(command -v brew 2>/dev/null)" /opt/homebrew/bin/brew /usr/local/bin/brew /home/linuxbrew/.linuxbrew/bin/brew "$HOME/.linuxbrew/bin/brew"; do
  if [ -n "$c" ] && [ -x "$c" ] && [ ! -d "$c" ]; then BREW="$c"; break; fi
done`

// brewManaged reports a CLI that Homebrew installed (it lives in the formula's keg).
func brewManaged(c ColdCLI) bool {
	for _, p := range []string{c.Real, c.Path, c.Home} {
		if strings.Contains(p, "/Cellar/"+brewFormula+"/") || strings.Contains(p, "/opt/"+brewFormula+"/") {
			return true
		}
	}
	return false
}

// brewScript taps nwverse/tap, installs nwverse/tap/disklessly-cli (or upgrades it when Homebrew
// already has it), clears the quarantine flag macOS puts on the unsigned binary, checks that the
// new CLI runs and knows sessions status, and removes the formula's downloads. A link step that
// fails (Homebrew's bin/disklessly already belongs to something else, such as the Disklessly app)
// does not fail the install when the keg's CLI works: it is reported as @@BREW_LINK. It reports
// @@BREW_OP install|upgrade, then @@BREW_OK <version> @@BREW_KEG <path>, or @@BREW_FAIL <step>
// [reason] with Homebrew's last lines as @@BREW_LOG; @@BREW_BIN is what Homebrew's bin/disklessly
// is (what a terminal with Homebrew on PATH runs), @@BREW_SHADOW another CLI the extension's
// lookup finds first.
func brewScript() string {
	return `export HOMEBREW_NO_ENV_HINTS=1 HOMEBREW_NO_INSTALL_CLEANUP=1 HOMEBREW_NO_ANALYTICS=1 NONINTERACTIVE=1
` + findBrewScript + `
if [ -z "$BREW" ]; then echo "@@BREW_FAIL nobrew"; exit 0; fi
LOG=$(mktemp "${TMPDIR:-/tmp}/disklessly-brew.XXXXXX" 2>/dev/null) || LOG=/dev/null
fail() { echo "@@BREW_FAIL $1 $2"; if [ "$LOG" != /dev/null ]; then tail -n 20 "$LOG" | sed 's/^/@@BREW_LOG /'; rm -f "$LOG"; fi; exit 0; }
"$BREW" tap ` + brewTap + ` >>"$LOG" 2>&1 || fail tap
if "$BREW" list --formula ` + brewFQ + ` >/dev/null 2>&1; then OP=upgrade; else OP=install; fi
echo "@@BREW_OP $OP"
RC=0
"$BREW" "$OP" ` + brewFQ + ` >>"$LOG" 2>&1 || RC=$?
PFX=$("$BREW" --prefix ` + brewFQ + ` 2>/dev/null)
P="$PFX/bin/disklessly"
if [ -z "$PFX" ] || [ ! -x "$P" ]; then
  if [ "$RC" != 0 ]; then fail "$OP"; fi
  fail verify "Homebrew 装完以后找不到 bin/disklessly"
fi
if [ "$RC" != 0 ]; then
  L=$(grep -i -m 2 -E 'symlink|brew link|already exists' "$LOG" 2>/dev/null | tr '\n' ' ')
  if [ -z "$L" ]; then fail "$OP"; fi
  echo "@@BREW_LINK $L"
fi
if [ "$(uname -s)" = Darwin ]; then xattr -d com.apple.quarantine "$P" "$PFX/bin/disklessly-stub" >/dev/null 2>&1 || true; fi
V=$("$P" version 2>/dev/null | head -n 1)
if [ -z "$V" ]; then fail verify "装好的 disklessly 在这台电脑上运行不了"; fi
J=$("$P" --json sessions status 2>/dev/null)
RC=$?
if [ "$RC" != 0 ] || [ -z "$J" ]; then fail verify "装好的 disklessly sessions status 运行失败"; fi
S=$(printf '%s' "$J" | tr '\n' ' ')
case "$S" in *EX_USAGE*|*"unknown sessions command"*) fail verify "装好的 disklessly 不支持 sessions status" ;; esac
if grep -q -i 'already installed' "$LOG" 2>/dev/null; then echo "@@BREW_SAME"; fi
"$BREW" cleanup --prune=0 -s ` + brewFQ + ` >/dev/null 2>&1 || true
rm -f "$LOG"
echo "@@BREW_OK $V"
echo "@@BREW_KEG $P"
HB="$(dirname "$(dirname "$BREW")")/bin/disklessly"
if [ -L "$HB" ] || [ -x "$HB" ]; then
  T=""; if [ -L "$HB" ]; then T=$(readlink "$HB"); fi
  printf '@@BREW_BIN %s\t%s\t%s\n' "$HB" "$T" "$("$HB" version 2>/dev/null | head -n 1)"
fi
` + resolveCLIScript + `
AV=""
if [ -n "$DKL" ]; then AV=$("$DKL" version 2>/dev/null | head -n 1); fi
if [ "$AV" != "$V" ]; then
  case "$DKL" in */*) DP="$DKL" ;; *) DP=$(command -v "$DKL" 2>/dev/null) ;; esac
  echo "@@BREW_SHADOW ${DP:-?}"
fi
`
}

// brewOutcome is the parsed result of brewScript.
type brewOutcome struct {
	ok      bool
	op      string // install or upgrade
	version string
	keg     string // the installed CLI
	same    bool   // Homebrew had it at the newest version already
	bin     string // Homebrew's bin/disklessly
	binTo   string // what that link points to
	binVer  string // what it answers to version
	link    string // why Homebrew could not link it
	shadow  string
	step    string
	reason  string
	log     string
}

func parseBrew(out string) brewOutcome {
	var o brewOutcome
	var log []string
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.HasPrefix(line, "@@BREW_OK "):
			o.ok, o.version = true, strings.TrimSpace(strings.TrimPrefix(line, "@@BREW_OK "))
		case strings.HasPrefix(line, "@@BREW_OP "):
			o.op = strings.TrimSpace(strings.TrimPrefix(line, "@@BREW_OP "))
		case strings.HasPrefix(line, "@@BREW_FAIL "):
			o.step, o.reason, _ = strings.Cut(strings.TrimPrefix(line, "@@BREW_FAIL "), " ")
			o.step, o.reason = strings.TrimSpace(o.step), strings.TrimSpace(o.reason)
		case strings.HasPrefix(line, "@@BREW_LOG "):
			log = append(log, strings.TrimPrefix(line, "@@BREW_LOG "))
		case strings.HasPrefix(line, "@@BREW_KEG "):
			o.keg = strings.TrimSpace(strings.TrimPrefix(line, "@@BREW_KEG "))
		case line == "@@BREW_SAME":
			o.same = true
		case strings.HasPrefix(line, "@@BREW_BIN "):
			f := strings.SplitN(strings.TrimPrefix(line, "@@BREW_BIN "), "\t", 3)
			for len(f) < 3 {
				f = append(f, "")
			}
			o.bin, o.binTo, o.binVer = strings.TrimSpace(f[0]), strings.TrimSpace(f[1]), strings.TrimSpace(f[2])
		case strings.HasPrefix(line, "@@BREW_LINK "):
			o.link = strings.TrimSpace(strings.TrimPrefix(line, "@@BREW_LINK "))
		case strings.HasPrefix(line, "@@BREW_SHADOW "):
			o.shadow = strings.TrimSpace(strings.TrimPrefix(line, "@@BREW_SHADOW "))
		}
	}
	o.log = strings.Join(log, "\n")
	return o
}

// brewVerb is 安装 or 升级.
func brewVerb(op string) string {
	if op == "upgrade" {
		return "升级"
	}
	return "安装"
}

// brewFailure words a failed brewScript run on machine name.
func brewFailure(name string, o brewOutcome) error {
	why := ""
	switch o.step {
	case "nobrew":
		why = "这台电脑上找不到 Homebrew"
	case "tap":
		why = "添加 " + brewTap + " 没有成功，可能是网络连不上 GitHub"
	case "install", "upgrade":
		why = "Homebrew 下载或" + brewVerb(o.step) + " " + brewFormula + " 没有成功"
	case "verify":
		why = cmp.Or(o.reason, "装好的 disklessly 检查没有通过")
	default:
		why = "Homebrew 没有给出结果"
	}
	msg := "在「" + name + "」上用 Homebrew " + brewVerb(cmp.Or(o.op, o.step)) + " disklessly 失败：" + why + "。"
	if o.log != "" {
		msg += "Homebrew 的输出在「详情」里；修好后再点一次，或在这台电脑上手动运行页面上的命令。"
	}
	return rawError(msg, o.log)
}
