package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

// Copy-install: the disklessly CLI has no public download, so a machine without it (or with one
// that predates the session commands) gets a copy from another managed machine of the same OS
// and architecture. The source's self-contained install directory (<dir>/bin/disklessly, dir
// named disklessly-*) is streamed as a tar through this process into the target's
// ~/.local/opt/<dir>.new, run once to verify, moved to ~/.local/opt/<dir>, and only then linked
// from ~/.local/bin/disklessly (a location resolveCLIScript searches). The previous directory and
// binary stay where they were. Nothing needs root and nothing is downloaded.

// StreamRunner runs script on machine m with stdin and stdout as byte streams.
type StreamRunner func(ctx context.Context, m *Machine, script string, stdin io.Reader, stdout io.Writer, timeout time.Duration) error

// DefaultStreamRunner runs script with /bin/sh -c locally, or `sh -c '<script>'` over OpenSSH.
func DefaultStreamRunner(ctx context.Context, m *Machine, script string, stdin io.Reader, stdout io.Writer, timeout time.Duration) error {
	cctx, cancel := context.WithTimeout(ctx, cmp.Or(timeout, 10*time.Minute))
	defer cancel()
	cmd, err := shellCommand(cctx, m, "sh -c "+shellQuote(script), "-c", script)
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, &stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		return runError(m, err, stderr.String())
	}
	return nil
}

// installTimeout bounds each side of a copy.
const installTimeout = 10 * time.Minute

// sourceTarScript writes the install directory home to stdout as a tar of its contents.
func sourceTarScript(home string) string {
	return `H=` + shellQuote(home) + `
if [ -L "$H" ] || [ ! -d "$H" ] || [ ! -x "$H/bin/disklessly" ]; then echo "no disklessly install at $H" >&2; exit 3; fi
cd "$H" || exit 3
COPYFILE_DISABLE=1 exec tar -cf - .
`
}

// installScript unpacks the tar on stdin into ~/.local/opt/<dir>.new, verifies the binary, moves
// it to ~/.local/opt/<dir> (an existing one is kept as <dir>.prev-<time>) and switches the
// ~/.local/bin/disklessly link with a rename. Any failure before the switch leaves the link as it
// was. It reports @@INSTALL_OK <version>, or @@INSTALL_FAIL <step> <reason>; @@INSTALL_PREV names
// what the link pointed to, and @@INSTALL_SHADOW a CLI the lookup still finds first.
func installScript(dir string) string {
	return `DIR=` + shellQuote(dir) + `
OPT="$HOME/.local/opt"; BIN="$HOME/.local/bin"; NEW="$OPT/$DIR.new"; FINAL="$OPT/$DIR"; LINK="$BIN/disklessly"
ERRF="$OPT/.$DIR.err"
fail() { echo "@@INSTALL_FAIL $1 $2"; rm -rf "$NEW"; rm -f "$ERRF"; cat >/dev/null 2>&1; exit 0; }
mkdir -p "$OPT" "$BIN" || fail prepare "无法创建 ~/.local/opt 或 ~/.local/bin"
if [ -d "$LINK" ]; then fail prepare "~/.local/bin/disklessly 是一个目录"; fi
rm -rf "$NEW" && mkdir "$NEW" || fail prepare "无法创建 $NEW"
if ! tar -xf - -C "$NEW" 2>"$ERRF"; then fail unpack "解包失败：$(tail -n 1 "$ERRF" 2>/dev/null)"; fi
rm -f "$ERRF"
if [ ! -x "$NEW/bin/disklessly" ] || [ -d "$NEW/bin/disklessly" ]; then fail unpack "复制来的目录里没有 bin/disklessly"; fi
V=$("$NEW/bin/disklessly" version 2>/dev/null | head -n 1)
if [ -z "$V" ]; then fail verify "复制来的 disklessly 在这台电脑上无法运行"; fi
S=$("$NEW/bin/disklessly" --json sessions status 2>/dev/null | tr '\n' ' ')
case "$S" in
  *EX_USAGE*|*"unknown sessions command"*) fail verify "复制来的 disklessly 不支持 sessions status" ;;
  *"{"*) ;;
  *) fail verify "复制来的 disklessly 没有回答 sessions status" ;;
esac
OLD=""
if [ -L "$LINK" ]; then OLD=$(readlink "$LINK"); elif [ -e "$LINK" ]; then OLD="$LINK"; fi
PREV=""
if [ -e "$FINAL" ] || [ -L "$FINAL" ]; then
  PREV="$FINAL.prev-$(date +%Y%m%d%H%M%S)"
  mv "$FINAL" "$PREV" || fail switch "无法移开已有的 $FINAL"
fi
if ! mv "$NEW" "$FINAL"; then
  if [ -n "$PREV" ]; then mv "$PREV" "$FINAL"; fi
  fail switch "无法把新目录移到 $FINAL"
fi
if [ -e "$LINK" ] && [ ! -L "$LINK" ]; then
  cp -p "$LINK" "$LINK.prev" || fail switch "无法保留原来的 $LINK"
fi
TMPL="$BIN/.disklessly.new.$$"
rm -f "$TMPL"
ln -s "$FINAL/bin/disklessly" "$TMPL" || fail switch "无法创建链接"
if ! mv -f "$TMPL" "$LINK"; then rm -f "$TMPL"; fail switch "无法切换 $LINK"; fi
if [ -n "$OLD" ]; then echo "@@INSTALL_PREV $OLD"; fi
echo "@@INSTALL_OK $V"
` + resolveCLIScript + `
AV=""
if [ -n "$DKL" ]; then AV=$("$DKL" version 2>/dev/null | head -n 1); fi
if [ "$AV" != "$V" ]; then
  case "$DKL" in */*) DP="$DKL" ;; *) DP=$(command -v "$DKL" 2>/dev/null) ;; esac
  echo "@@INSTALL_SHADOW ${DP:-?}"
fi
`
}

// installOutcome is the parsed result of installScript.
type installOutcome struct {
	ok      bool
	version string
	prev    string
	shadow  string
	step    string
	reason  string
}

func parseInstall(out string) installOutcome {
	var o installOutcome
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.HasPrefix(line, "@@INSTALL_OK "):
			o.ok, o.version = true, strings.TrimSpace(strings.TrimPrefix(line, "@@INSTALL_OK "))
		case strings.HasPrefix(line, "@@INSTALL_FAIL "):
			o.step, o.reason, _ = strings.Cut(strings.TrimPrefix(line, "@@INSTALL_FAIL "), " ")
		case strings.HasPrefix(line, "@@INSTALL_PREV "):
			o.prev = strings.TrimSpace(strings.TrimPrefix(line, "@@INSTALL_PREV "))
		case strings.HasPrefix(line, "@@INSTALL_SHADOW "):
			o.shadow = strings.TrimSpace(strings.TrimPrefix(line, "@@INSTALL_SHADOW "))
		}
	}
	return o
}

// copyInstall streams src's install directory home into dst and installs it there.
func copyInstall(ctx context.Context, streamer StreamRunner, src, dst *Machine, home string) (installOutcome, error) {
	if streamer == nil {
		streamer = DefaultStreamRunner
	}
	dir := path.Base(home)
	if !installDirName.MatchString(dir) {
		return installOutcome{}, fmt.Errorf("源目录 %q 不是 disklessly 的独立安装目录", home)
	}
	pr, pw := io.Pipe()
	srcCtx, cancelSrc := context.WithCancel(ctx)
	defer cancelSrc()
	srcDone := make(chan error, 1)
	go func() {
		err := streamer(srcCtx, src, sourceTarScript(home), nil, pw, installTimeout)
		pw.CloseWithError(err)
		srcDone <- err
	}()
	var out bytes.Buffer
	terr := streamer(ctx, dst, installScript(dir), pr, &out, installTimeout)
	// The target is done reading: a source still writing gets an error, and is stopped if it hangs.
	pr.CloseWithError(errors.New("target finished"))
	var serr error
	select {
	case serr = <-srcDone:
	case <-time.After(30 * time.Second):
		cancelSrc()
		serr = <-srcDone
	}
	o := parseInstall(out.String())
	switch {
	case o.ok:
		return o, nil
	case o.step != "":
		msg := o.reason
		if serr != nil {
			msg += "（读取 " + machineLabel(src) + " 时出错：" + serr.Error() + "）"
		}
		return o, errors.New(msg)
	case serr != nil:
		return o, fmt.Errorf("读取 %s 上的 CLI 失败：%w", machineLabel(src), serr)
	case terr != nil:
		return o, terr
	}
	return o, errors.New("安装脚本没有返回结果")
}

// installOffer is a copy-install a machine can get: the source and what it would copy, or why
// none is possible.
type installOffer struct {
	src      *Machine
	cli      ColdCLI
	reason   string
	platform string
}

// installSourceFor finds a machine to copy the CLI from for target: enabled, reachable, the same
// OS and architecture, a CLI that answered sessions list and status on its last refresh, in a
// self-contained directory.
func installSourceFor(st *State, target *Machine) installOffer {
	tcat := st.cold(target.ID)
	plat := target.Platform
	if tcat != nil {
		plat = cmp.Or(tcat.Platform, plat)
	}
	if plat == "" {
		return installOffer{reason: "还不知道这台电脑的系统和架构，先刷新一次"}
	}
	var notContained string
	for _, m := range st.Machines {
		if m.ID == target.ID || !m.Enabled || !m.Reachable {
			continue
		}
		cat := st.cold(m.ID)
		if cat == nil {
			continue
		}
		switch cat.stateCode() {
		case ColdReady, ColdNoBackend, ColdBackendDown:
		default:
			continue
		}
		if cmp.Or(cat.Platform, m.Platform) != plat {
			continue
		}
		if ok, why := cat.CLI.selfContained(); !ok {
			if notContained == "" {
				notContained = machineLabel(m) + " 有可用的 CLI，但" + why + "，不能复制"
			}
			continue
		}
		return installOffer{src: m, cli: cat.CLI, platform: plat}
	}
	if notContained != "" {
		return installOffer{reason: notContained, platform: plat}
	}
	return installOffer{reason: "没有同为 " + plat + " 的电脑装有可用的 disklessly CLI，无法复制安装", platform: plat}
}

func installConfirm(o installOffer, target *Machine) string {
	return fmt.Sprintf("从「%s」把 disklessly %s（%s）复制安装到「%s」？会装到 %s 的 ~/.local/opt/%s，先运行一次确认可用，再把 ~/.local/bin/disklessly 指向它；原来的版本保留在原处。不需要 root，也不从网络下载。",
		machineLabel(o.src), cmp.Or(shortVersion(o.cli.Version), "（版本未知）"), o.platform, machineLabel(target), machineLabel(target), path.Base(o.cli.Home))
}
