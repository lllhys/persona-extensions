package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// english matches a run of Latin words that reads as an untranslated message (three or more
// words in a row), the kind of text that must stay in 详情. Commands are drawn as code and are
// not part of a row's detail.
var english = regexp.MustCompile(`[A-Za-z]{2,}[ ,:]+[A-Za-z]{2,}[ ,:]+[A-Za-z]{2,}`)

func noSandbox(t *testing.T) {
	t.Helper()
	prev := sandboxed
	sandboxed = func() bool { return false }
	t.Cleanup(func() { sandboxed = prev })
}

func codeIn(items []wire.Component) []string {
	var out []string
	for _, c := range items {
		if c.Type == wire.ComponentCode {
			out = append(out, c.Text)
		}
	}
	return out
}

// TestSSHProblems: each SSH failure names the machine, says why in Chinese and what to do next
// (a command to copy); the raw OpenSSH line is kept for 详情 only.
func TestSSHProblems(t *testing.T) {
	noSandbox(t)
	m := &Machine{ID: "mm-2", Host: "mm-2", Name: "mm-2 (100.64.0.2)", User: "dev", Enabled: true}
	cases := []struct {
		name, raw  string
		cause      string
		reason     string // in Reason
		command    string // in Command
		sandbox    bool
		wantRetry  bool
		wantEditor bool
	}{
		{name: "timeout", raw: "SSH 连接 mm-2 失败：ssh: connect to host 100.64.0.2 port 22: Connection timed out", cause: causeTimeout, reason: "超时", command: "ssh -o BatchMode=yes", wantRetry: true},
		{name: "auth", raw: "SSH 连接 mm-2 失败：dev@mm-2: Permission denied (publickey).", cause: causeAuth, reason: "拒绝了 SSH 登录", command: "ssh-copy-id dev@mm-2", wantRetry: true, wantEditor: true},
		{name: "host key", raw: "SSH 连接 mm-2 失败：Host key verification failed.", cause: causeHostKey, reason: "指纹", command: "ssh dev@mm-2", wantRetry: true},
		{name: "refused", raw: "SSH 连接 mm-2 失败：ssh: connect to host mm-2 port 22: Connection refused", cause: causeRefused, reason: "拒绝了连接", wantRetry: true, wantEditor: true},
		{name: "dns", raw: "SSH 连接 mm-2 失败：ssh: Could not resolve hostname mm-2: Name or service not known", cause: causeDNS, reason: "找不到", command: "ssh -G mm-2", wantRetry: true, wantEditor: true},
		// The machine is a port forward on the hub, and nothing listens there.
		{name: "tunnel down", raw: "SSH 连接 mm-2 失败：ssh: connect to host 127.0.0.1 port 2222: Connection refused", cause: causeRefused, reason: "127.0.0.1:2222", command: "ssh -o BatchMode=yes", wantRetry: true},
		{name: "network", raw: "SSH 连接 mm-2 失败：ssh: connect to host mm-2 port 22: No route to host", cause: causeNetwork, reason: "网络不通", wantRetry: true},
		// The hub's own OpenSSH config is unreadable: outside a sandbox that is a file to fix on the hub.
		{name: "hub ssh config", raw: "SSH 连接 mm-2 失败：Bad owner or permissions on /etc/ssh/ssh_config.d/50-redhat.conf", cause: causeSSHConfig, reason: "/etc/ssh/ssh_config.d/50-redhat.conf", command: "sudo chmod 644 /etc/ssh/ssh_config.d/50-redhat.conf", wantRetry: true},
		// The same line inside the hub's sandbox: nothing on either machine is wrong.
		{name: "sandbox", raw: "SSH 连接 mm-2 失败：Bad owner or permissions on /etc/ssh/ssh_config.d/50-redhat.conf", sandbox: true, cause: causeSandbox, reason: "沙箱", command: "personad extension sandbox disklessly off", wantRetry: true},
		{name: "other", raw: "SSH 连接 mm-2 失败：kex_exchange_identification: read: Connection reset by peer", cause: "", reason: "SSH 连接", command: "ssh -o BatchMode=yes", wantRetry: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sandboxed = func() bool { return c.sandbox }
			p := sshProblem(m, c.raw)
			if p.Cause != c.cause {
				t.Fatalf("cause %q, want %q", p.Cause, c.cause)
			}
			if !strings.Contains(p.Reason, "mm-2") || strings.Contains(p.Reason, "100.64.0.2") {
				t.Fatalf("reason must name the machine without its address: %q", p.Reason)
			}
			if !strings.Contains(p.Reason, c.reason) {
				t.Fatalf("reason %q lacks %q", p.Reason, c.reason)
			}
			if c.command != "" && !strings.Contains(p.Command, c.command) {
				t.Fatalf("command %q lacks %q", p.Command, c.command)
			}
			if p.Next == "" {
				t.Fatal("no next step")
			}
			if english.MatchString(p.Reason + p.Next) {
				t.Fatalf("English in the main text: %q", p.Reason+p.Next)
			}
			if !strings.Contains(p.Raw, strings.TrimPrefix(c.raw, "SSH 连接 mm-2 失败：")) {
				t.Fatalf("raw %q lost the OpenSSH line", p.Raw)
			}
			if p.Retry != c.wantRetry || p.Edit != c.wantEditor {
				t.Fatalf("retry %v edit %v", p.Retry, p.Edit)
			}
		})
	}
	// A copyable command keeps ~/ expandable.
	if got := sshTarget(&Machine{Host: "muse", User: "root", Port: 2222, IdentityFile: "~/.ssh/id_ed25519"}); got != "-p 2222 -i ~/'.ssh/id_ed25519' root@muse" && got != "-p 2222 -i ~/.ssh/id_ed25519 root@muse" {
		t.Fatalf("target %q", got)
	}
	// A refused connection on a Linux machine says how to start sshd there.
	if p := sshProblem(&Machine{ID: "gcp", Host: "gcp", Platform: "linux-amd64"}, "SSH 连接 gcp 失败：ssh: connect to host gcp port 22: Connection refused"); !strings.Contains(p.Next, "systemctl") || strings.Contains(p.Next, "macOS") {
		t.Fatalf("linux refused: %q", p.Next)
	}
	// A failed local command is not an SSH problem.
	p := sshProblem(&Machine{ID: "local", Name: "local (devbox)", IsLocal: true}, "本地探测命令执行失败：exit status 2")
	if !strings.Contains(p.Reason, "本机") || p.Command != "" || !strings.Contains(p.Raw, "exit status 2") {
		t.Fatalf("local problem: %+v", p)
	}
}

// TestHumanize turns stored and returned error texts into the human sentence plus the raw part.
func TestHumanize(t *testing.T) {
	noSandbox(t)
	msg, raw := humanize("从 build-box 复制安装失败：读取 build-box 上的 CLI 失败：SSH 连接 build-box 失败：dev@build-box: Permission denied (publickey).")
	if !strings.HasPrefix(msg, "从 build-box 复制安装失败") || !strings.Contains(msg, "拒绝了 SSH 登录") || strings.Contains(msg, "Permission denied") {
		t.Fatalf("msg %q", msg)
	}
	if !strings.Contains(raw, "Permission denied (publickey)") {
		t.Fatalf("raw %q", raw)
	}
	msg, raw = humanize("当前没有符合条件的可清理项")
	if msg != "当前没有符合条件的可清理项" || raw != "" {
		t.Fatalf("plain message changed: %q %q", msg, raw)
	}
	// An error carrying its raw part separately.
	err := rawError("备份后端连不上，没有上传或删除任何会话", "dial tcp 10.0.0.9:443: i/o timeout")
	msg, raw = humanizeErr(err)
	if msg != "备份后端连不上，没有上传或删除任何会话" || raw != "dial tcp 10.0.0.9:443: i/o timeout" || err.Error() != msg {
		t.Fatalf("raw error: %q %q %q", err.Error(), msg, raw)
	}
	if _, raw := humanizeErr(errors.Join(errors.New("外层"), err)); raw == "" {
		t.Fatal("wrapped raw error lost its detail")
	}
}

// coldFleet is five machines in the shapes seen on a real hub: a ready one, a Mac with Homebrew
// and no CLI, a Linux box with an old CLI and no Homebrew but a same-platform source, a Linux box
// with no CLI, no Homebrew and nothing to copy from, and one that times out.
func coldFleet(t *testing.T, a *app) {
	t.Helper()
	yes := true
	err := a.db().update(func(st *State, now time.Time) error {
		for _, h := range []string{"build-box", "studio-mac", "old-box", "bare-box", "far-box"} {
			if _, err := st.upsertMachine(MachineInput{Host: h, Name: h + " (100.64.0.9)"}, now); err != nil {
				return err
			}
		}
		home := "/home/dev/.local/opt/disklessly-0.3.0-linux-amd64"
		st.setCold("build-box", &ColdCatalog{MachineID: "build-box", State: ColdReady, Platform: "linux-amd64", RefreshedAt: now, CheckedAt: now,
			Backend: ColdBackend{ID: "main", Type: "rclone", Reachable: &yes, CheckedAt: now},
			CLI:     ColdCLI{Version: "disklessly 0.3.0", Path: "/home/dev/.local/bin/disklessly", Real: home + "/bin/disklessly", Home: home, KB: 30000}})
		st.setCold("studio-mac", &ColdCatalog{MachineID: "studio-mac", State: ColdNoCLI, Platform: "darwin-arm64", CheckedAt: now,
			Error: "目标机器未安装 disklessly CLI，无法读取云端会话索引", Brew: "/opt/homebrew/bin/brew", BrewKnown: true})
		st.setCold("old-box", &ColdCatalog{MachineID: "old-box", State: ColdCLIOld, Platform: "linux-amd64", CheckedAt: now,
			BrewKnown: true, Error: "这台电脑上的 disklessly 0.1.0 太旧", Detail: `{"error":{"message":"unknown sessions command list","name":"EX_USAGE"},"ok":false}`,
			CLI: ColdCLI{Version: "disklessly 0.1.0"}})
		st.setCold("bare-box", &ColdCatalog{MachineID: "bare-box", State: ColdNoCLI, Platform: "linux-arm64", CheckedAt: now, BrewKnown: true,
			Error: "目标机器未安装 disklessly CLI，无法读取云端会话索引"})
		st.setCold("far-box", &ColdCatalog{MachineID: "far-box", State: ColdOffline, Cause: causeTimeout, CheckedAt: now,
			Error: "SSH 连接 far-box 失败：ssh: connect to host 100.64.0.9 port 2222: Connection timed out"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestColdRowsSpeakPlainly: every machine row on the Agent 对话 page names the machine, says why
// and offers a button or a command; English answers and SSH output appear only under 详情.
func TestColdRowsSpeakPlainly(t *testing.T) {
	noSandbox(t)
	a := newTestApp(t)
	coldFleet(t, a)
	v, err := a.renderSessions(context.Background(), &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"studio-mac", "old-box", "bare-box", "far-box"} {
		items := itemsOf(v, id)
		if len(items) == 0 {
			t.Fatalf("%s: no row", id)
		}
		row := items[0]
		if !strings.Contains(row.Detail, id) {
			t.Fatalf("%s: detail does not name the machine: %q", id, row.Detail)
		}
		if english.MatchString(row.Detail) || strings.Contains(row.Detail, "EX_") || strings.Contains(row.Detail, "100.64.0.9") {
			t.Fatalf("%s: raw text in the row: %q", id, row.Detail)
		}
		hasFix := false
		for _, c := range items[1:] {
			hasFix = hasFix || c.Type == wire.ComponentButton || c.Type == wire.ComponentCode
		}
		if !hasFix {
			t.Fatalf("%s: no button or command: %+v", id, items)
		}
	}

	// Homebrew first where the machine has it.
	mac := itemsOf(v, "studio-mac")
	brew := buttonIn(mac, "用 Homebrew 安装")
	if brew == nil || brew.Action.ID != "cold-brew" || brew.Action.Values["m"] != "studio-mac" {
		t.Fatalf("studio-mac: no Homebrew install: %+v", mac)
	}
	for _, w := range []string{"studio-mac", brewInstallCmd, "disklessly version"} {
		if !strings.Contains(brew.Action.Confirm, w) {
			t.Fatalf("brew confirm lacks %q: %s", w, brew.Action.Confirm)
		}
	}
	if buttonIn(mac, "从 ") != nil {
		t.Fatal("studio-mac: copy-install offered next to Homebrew")
	}
	// No Homebrew: copy from a machine of the same platform.
	old := itemsOf(v, "old-box")
	if buttonIn(old, "用 Homebrew") != nil || buttonIn(old, "从 build-box 复制安装") == nil {
		t.Fatalf("old-box: %+v", old)
	}
	if !strings.Contains(old[0].Detail, "0.1.0") || !strings.Contains(old[0].Detail, "没有 Homebrew") {
		t.Fatalf("old-box detail: %q", old[0].Detail)
	}
	// Neither: the manual commands, and no install button.
	bare := itemsOf(v, "bare-box")
	if buttonIn(bare, "用 Homebrew") != nil || buttonIn(bare, "从 ") != nil {
		t.Fatalf("bare-box offered an install: %+v", bare)
	}
	// A catalog from before Homebrew was looked for does not claim there is none.
	if err := a.db().update(func(st *State, now time.Time) error {
		c := st.cold("bare-box")
		c.BrewKnown = false
		st.setCold("bare-box", c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	v2, _ := a.renderSessions(context.Background(), &ext.UIRequest{})
	if d := itemsOf(v2, "bare-box")[0].Detail; strings.Contains(d, "这台电脑没有 Homebrew") || !strings.Contains(d, "重新检查") {
		t.Fatalf("unknown Homebrew: %q", d)
	}
	if cmds := strings.Join(codeIn(bare), "\n"); !strings.Contains(cmds, brewInstallCmd) || !strings.Contains(cmds, "brew.sh") {
		t.Fatalf("bare-box commands: %q", cmds)
	}
	// Unreachable: the retry button and a command to test the connection from the hub.
	far := itemsOf(v, "far-box")
	if buttonIn(far, "重试") == nil || !strings.Contains(strings.Join(codeIn(far), "\n"), "ssh -o BatchMode=yes") {
		t.Fatalf("far-box: %+v", far)
	}

	// 详情 holds the raw text and opens on demand.
	if strings.Contains(texts(v)+strings.Join(codeIn(machineItems(v)), ""), "unknown sessions command") {
		t.Fatal("raw CLI answer shown before 详情 is opened")
	}
	det := buttonIn(old, "详情")
	if det == nil {
		t.Fatalf("old-box: no 详情 button: %+v", old)
	}
	res, err := a.actionSessions(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{State: v.State, Action: det.Action.ID, Values: det.Action.Values}})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(res.View); err != nil {
		t.Fatal(err)
	}
	opened := itemsOf(res.View, "old-box")
	if !strings.Contains(strings.Join(codeIn(opened), "\n"), "unknown sessions command list") || buttonIn(opened, "收起详情") == nil {
		t.Fatalf("详情 not shown: %+v", opened)
	}
}

// TestBackendWithoutRepository: a backend that answers but holds no repository says so.
func TestBackendWithoutRepository(t *testing.T) {
	no := false
	st := &State{}
	m := &Machine{ID: "gcp", Host: "gcp", Name: "gcp", Enabled: true, Reachable: true}
	st.Machines = []*Machine{m}
	cat := &ColdCatalog{MachineID: "gcp", State: ColdBackendDown, Backend: ColdBackend{ID: "nas", Type: "smb", Reachable: &no,
		Error: "repo.json is not in the backend; if the repo folder is there and new, run disklessly init"}}
	a := coldProblem(st, m, cat, ColdBackendDown)
	if !strings.Contains(a.Reason, "还没有 Disklessly 仓库") || !strings.Contains(a.Command, "disklessly backend use") || !strings.Contains(a.Raw, "repo.json") || english.MatchString(a.text()) {
		t.Fatalf("%+v", a)
	}
}

// TestFleetTabSpeaksPlainly: the 多机总览 tab and the overview card word a failed probe the same way.
func TestFleetTabSpeaksPlainly(t *testing.T) {
	noSandbox(t)
	a := newTestApp(t)
	if err := a.db().update(func(st *State, now time.Time) error {
		m, err := st.upsertMachine(MachineInput{Host: "far-box", Name: "far-box"}, now)
		m.LastError = "SSH 连接 far-box 失败：dev@far-box: Permission denied (publickey)."
		return err
	}); err != nil {
		t.Fatal(err)
	}
	v, err := a.render(context.Background(), &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
	all := texts(v)
	if strings.Contains(all, "Permission denied") {
		t.Fatalf("raw SSH output on the fleet tab:\n%s", all)
	}
	if !strings.Contains(all, "far-box") || !strings.Contains(all, "拒绝了 SSH 登录") {
		t.Fatalf("fleet tab:\n%s", all)
	}
	var cmds []string
	for _, s := range v.Sections {
		cmds = append(cmds, codeIn(s.Items)...)
	}
	if !strings.Contains(strings.Join(cmds, "\n"), "ssh-copy-id far-box") {
		t.Fatalf("no command on the fleet tab: %q", cmds)
	}
	card, err := a.card(context.Background(), &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if c := texts(card); strings.Contains(c, "Permission denied") || !strings.Contains(c, "拒绝") {
		t.Fatalf("card:\n%s", c)
	}
}

// TestSandboxBanner: inside the hub's sandbox both pages say so first, with the command that
// turns it off.
func TestSandboxBanner(t *testing.T) {
	prev := sandboxed
	sandboxed = func() bool { return true }
	t.Cleanup(func() { sandboxed = prev })
	a := newTestApp(t)
	for name, render := range map[string]func(context.Context, *ext.UIRequest) (*wire.View, error){"page": a.render, "sessions": a.renderSessions} {
		v, err := render(context.Background(), &ext.UIRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if err := wire.CheckView(v); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(v.Sections[0].Title, "沙箱") || !strings.Contains(strings.Join(codeIn(v.Sections[0].Items), ""), "personad extension sandbox disklessly off") {
			t.Fatalf("%s: no sandbox banner: %+v", name, v.Sections[0])
		}
	}
}

// fakeBrew writes a Homebrew stand-in to dir/brew that records its arguments in dir/log and,
// on install or upgrade, puts a disklessly stand-in at prefix/opt/disklessly-cli/bin.
// mode "fail" fails the download; "link" installs the keg, then fails Homebrew's link step; "status-fail" makes the
// installed CLI's sessions status exit 1 without printing anything.
func fakeBrew(t *testing.T, dir, version string, installed bool, mode string) {
	t.Helper()
	prefix := filepath.Join(dir, "prefix")
	cli := filepath.Join(prefix, "opt", "disklessly-cli", "bin")
	if err := os.MkdirAll(cli, 0o755); err != nil {
		t.Fatal(err)
	}
	dkl := "#!/bin/sh\ncase \"$*\" in\n  version) echo \"disklessly " + version + "\" ;;\n  *\"sessions status\"*) if [ \"" + mode + "\" = status-fail ]; then exit 1; fi; echo '{\"ok\":true,\"backend\":{\"id\":\"\",\"type\":\"\",\"reachable\":false},\"counts\":{},\"segments\":{}}' ;;\nesac\n"
	listRC := "1"
	if installed {
		listRC = "0"
		if err := os.WriteFile(filepath.Join(cli, "disklessly"), []byte(strings.ReplaceAll(dkl, version, "0.2.0")), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	installRC := "0"
	if mode == "fail" {
		installRC = "1"
	}
	script := `#!/bin/sh
echo "$*" >> "` + filepath.Join(dir, "log") + `"
case "$1" in
  tap) exit 0 ;;
  list) exit ` + listRC + ` ;;
  --prefix) if [ -n "$2" ]; then echo "` + prefix + `/opt/${2##*/}"; else echo "` + prefix + `"; fi ;;
  install|upgrade)
    if [ "` + mode + `" = same ]; then echo "Warning: nwverse/tap/disklessly-cli 0.3.0 already installed" >&2; exit 0; fi
    if [ "` + installRC + `" = 1 ]; then echo "Error: disklessly-cli: Failed to download resource" >&2; exit 1; fi
    cat > "` + filepath.Join(cli, "disklessly") + `" <<'EOF'
` + dkl + `EOF
    chmod +x "` + filepath.Join(cli, "disklessly") + `"
    if [ "` + mode + `" = link ]; then echo "Error: The brew link step did not complete successfully" >&2; echo "Could not symlink bin/disklessly: Target /opt/homebrew/bin/disklessly already exists." >&2; exit 1; fi ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "brew"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// machineWide are the fixed locations the scripts search besides PATH and HOME: Homebrew's prefixes, /usr/local
// and the Disklessly app (TestHermeticCoversEveryFixedLocation keeps the list complete).
var machineWide = []string{"/opt/homebrew/", "/usr/local/", "/home/linuxbrew/", "/Applications/"}

// hermetic is script with machineWide moved under root, where nothing exists unless the test puts it there. A test
// then sees only its own fakes, never a Homebrew or a CLI installed on the machine running it: GitHub's Ubuntu
// runners have /home/linuxbrew/.linuxbrew, and a developer's Mac /opt/homebrew, where the unchanged script would
// tap and install for real (#592).
func hermetic(script, root string) string {
	var pairs []string
	for _, p := range machineWide {
		pairs = append(pairs, p, filepath.Join(root, p)+"/")
	}
	return strings.NewReplacer(pairs...).Replace(script)
}

func runBrewScript(t *testing.T, dir string) brewOutcome {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-s")
	cmd.Stdin = strings.NewReader(hermetic(brewScript(), dir))
	cmd.Env = []string{"HOME=" + filepath.Join(dir, "home"), "PATH=" + dir + ":/usr/bin:/bin", "TMPDIR=" + dir}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	return parseBrew(string(out))
}

func TestBrewScript(t *testing.T) {
	// Install: tap, install, verify the new CLI answers.
	dir := t.TempDir()
	fakeBrew(t, dir, "0.3.0", false, "")
	o := runBrewScript(t, dir)
	if !o.ok || o.version != "disklessly 0.3.0" || o.op != "install" {
		t.Fatalf("install: %+v", o)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "log"))
	if !strings.Contains(string(log), "tap nwverse/tap") || !strings.Contains(string(log), "install nwverse/tap/disklessly-cli") || !strings.Contains(string(log), "cleanup --prune=0 -s nwverse/tap/disklessly-cli") {
		t.Fatalf("brew calls:\n%s", log)
	}
	// Already installed by Homebrew: upgrade.
	dir = t.TempDir()
	fakeBrew(t, dir, "0.3.0", true, "")
	if o := runBrewScript(t, dir); !o.ok || o.op != "upgrade" || o.version != "disklessly 0.3.0" {
		t.Fatalf("upgrade: %+v", o)
	}
	// A failed install says which step and keeps Homebrew's last lines for 详情.
	dir = t.TempDir()
	fakeBrew(t, dir, "0.3.0", false, "fail")
	o = runBrewScript(t, dir)
	if o.ok || o.step != "install" || !strings.Contains(o.log, "Failed to download resource") {
		t.Fatalf("failed install: %+v", o)
	}
	// Already the newest version: said so, and the formula's downloads are pruned.
	dir = t.TempDir()
	fakeBrew(t, dir, "0.3.0", true, "same")
	if o := runBrewScript(t, dir); !o.ok || !o.same || o.op != "upgrade" {
		t.Fatalf("up to date: %+v", o)
	}
	if log, _ := os.ReadFile(filepath.Join(dir, "log")); !strings.Contains(string(log), "cleanup --prune=0 -s nwverse/tap/disklessly-cli") {
		t.Fatalf("no prune:\n%s", log)
	}
	// The keg works but Homebrew could not link it over another bin/disklessly: installed, with
	// the link problem reported.
	dir = t.TempDir()
	fakeBrew(t, dir, "0.3.0", false, "link")
	if o := runBrewScript(t, dir); !o.ok || !strings.Contains(o.link, "already exists") || !strings.HasSuffix(o.keg, "/opt/disklessly-cli/bin/disklessly") {
		t.Fatalf("link conflict: %+v", o)
	}
	// The extension's lookup prefers Homebrew's keg over a shared bin directory, where another
	// program (the Disklessly app's engine) may hold the name.
	if k, b := strings.Index(resolveCLIScript, "/opt/homebrew/opt/disklessly-cli/bin/disklessly"), strings.Index(resolveCLIScript, "/opt/homebrew/bin/disklessly"); k < 0 || k > b {
		t.Fatal("lookup does not try the Homebrew keg before /opt/homebrew/bin")
	}
	// No Homebrew at all, whatever the machine running the test has.
	dir = t.TempDir()
	if o := runBrewScript(t, dir); o.ok || o.step != "nobrew" {
		t.Fatalf("no brew: %+v", o)
	}
	// A Homebrew at one of the fixed prefixes, not on PATH (a non-interactive SSH shell), is found.
	dir = t.TempDir()
	fakeBrew(t, dir, "0.3.0", false, "")
	fixed := filepath.Join(dir, "home", "linuxbrew", ".linuxbrew", "bin")
	if err := os.MkdirAll(fixed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "brew"), filepath.Join(fixed, "brew")); err != nil {
		t.Fatal(err)
	}
	if o := runBrewScript(t, dir); !o.ok || o.op != "install" {
		t.Fatalf("brew at a fixed prefix: %+v", o)
	}
	// The refresh script reports where Homebrew is.
	dir = t.TempDir()
	fakeBrew(t, dir, "0.3.0", false, "")
	cmd := exec.Command("/bin/sh", "-s")
	cmd.Stdin = strings.NewReader(hermetic(coldListScript(false, true), dir))
	cmd.Env = []string{"HOME=" + filepath.Join(dir, "home"), "PATH=" + dir + ":/usr/bin:/bin"}
	out, _ := cmd.CombinedOutput()
	if got := firstNonEmpty(splitSections(string(out))["COLD_BREW"]); got != filepath.Join(dir, "brew") {
		t.Fatalf("COLD_BREW %q in\n%s", got, out)
	}
}

// TestHermeticCoversEveryFixedLocation: every absolute path the scripts search starts with one of machineWide,
// so hermetic leaves no way to a Homebrew or CLI of the machine running the tests.
func TestHermeticCoversEveryFixedLocation(t *testing.T) {
	path := regexp.MustCompile(`(?:^|[\s"'=(])(/[A-Za-z][^\s"';)]*)`)
	for name, script := range map[string]string{"brew": brewScript(), "cold list": coldListScript(false, true), "resolve": resolveCLIScript} {
		for _, m := range path.FindAllStringSubmatch(script, -1) {
			p := m[1]
			if p == "/dev/null" || strings.HasPrefix(p, "/bin/") || strings.HasPrefix(p, "/usr/bin/") {
				continue
			}
			if !slices.ContainsFunc(machineWide, func(w string) bool { return strings.HasPrefix(p, w) }) {
				t.Errorf("%s script searches %s, which hermetic does not move", name, p)
			}
		}
		root := t.TempDir()
		h := hermetic(script, root)
		for _, w := range machineWide {
			if n, moved := strings.Count(h, w), strings.Count(h, root+w); n == 0 || n != moved {
				t.Errorf("%s script: %d of %d mentions of %s moved", name, moved, n, w)
			}
		}
	}
}

// TestRefreshWithoutBrewOrCLI: a machine with neither prints an empty COLD_BREW section and then
// the no-CLI marker; the marker is not taken for Homebrew's path (seen on a real Linux box).
func TestRefreshWithoutBrewOrCLI(t *testing.T) {
	noSandbox(t)
	st := &State{}
	m := &Machine{ID: "grok", Host: "grok", Name: "grok", Enabled: true}
	st.Machines = []*Machine{m}
	out := "@@SECTION:COLD_UNAME\nLinux x86_64\n@@SECTION:COLD_BREW\n\n@@COLD_NOCLI\n"
	run := func(context.Context, *Machine, string, time.Duration) (string, error) { return out, nil }
	cat, _ := RefreshColdIndex(context.Background(), run, st, m, t0)
	if cat.State != ColdNoCLI || cat.Brew != "" || !cat.BrewKnown {
		t.Fatalf("state %q brew %q known %v", cat.State, cat.Brew, cat.BrewKnown)
	}
	if a := coldProblem(st, m, cat, ColdNoCLI); a.brew || !strings.Contains(a.Next, "这台电脑没有 Homebrew") {
		t.Fatalf("advice %+v", a)
	}
}

// TestBrewJob: the Homebrew button runs in the background, then re-reads the machine; a failure
// is worded for people with Homebrew's output under 详情.
func TestBrewJob(t *testing.T) {
	noSandbox(t)
	a := newTestApp(t)
	coldFleet(t, a)
	var mu sync.Mutex
	var calls []string
	answer := "@@BREW_OP install\n@@BREW_OK disklessly 0.3.0 (abc, built 2026-11-15) revision 2\n@@BREW_KEG /opt/homebrew/opt/disklessly-cli/bin/disklessly\n" +
		"@@BREW_BIN /opt/homebrew/bin/disklessly\t/Applications/Disklessly.app/Contents/MacOS/disklessly\tdisklessly 0.2.3 (def) revision 2\n"
	a.runner = func(ctx context.Context, m *Machine, s string, d time.Duration) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(s, "@@BREW_OK") {
			calls = append(calls, m.ID+":brew")
			return answer, nil
		}
		calls = append(calls, m.ID+":refresh")
		return coldRunner(nil)(ctx, m, s, d)
	}
	toast, err := a.coldAction(context.Background(), "cold-brew", "studio-mac", "")
	if err != nil || !strings.Contains(toast, "studio-mac") || !strings.Contains(toast, "Homebrew") {
		t.Fatalf("toast %q err %v", toast, err)
	}
	a.waitJobs()
	j := a.jobSnapshot()["studio-mac"]
	if j.Err != "" || !strings.Contains(j.Note, "已用 Homebrew 安装 disklessly 0.3.0（/opt/homebrew/opt/disklessly-cli/bin/disklessly）") ||
		!strings.Contains(j.Note, "/opt/homebrew/bin/disklessly → /Applications/Disklessly.app/Contents/MacOS/disklessly（disklessly 0.2.3）") || strings.Contains(j.Note, "revision") {
		t.Fatalf("job %+v", j)
	}
	if len(calls) < 2 || calls[0] != "studio-mac:brew" || calls[1] != "studio-mac:refresh" {
		t.Fatalf("calls %v", calls)
	}
	// A machine without Homebrew is refused at once.
	if _, err := a.coldAction(context.Background(), "cold-brew", "bare-box", ""); err == nil || !strings.Contains(err.Error(), "bare-box") || !strings.Contains(err.Error(), "Homebrew") {
		t.Fatalf("bare-box brew: %v", err)
	}

	answer = "@@BREW_OP install\n@@BREW_FAIL install\n@@BREW_LOG Error: disklessly-cli: Failed to download resource\n"
	if err := a.db().update(func(st *State, now time.Time) error {
		c := st.cold("studio-mac")
		c.State, c.Brew = ColdNoCLI, "/opt/homebrew/bin/brew"
		st.setCold("studio-mac", c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.coldAction(context.Background(), "cold-brew", "studio-mac", ""); err != nil {
		t.Fatal(err)
	}
	a.waitJobs()
	j = a.jobSnapshot()["studio-mac"]
	if !strings.Contains(j.Err, "studio-mac") || !strings.Contains(j.Err, "Homebrew") || english.MatchString(j.Err) {
		t.Fatalf("failed job error: %q", j.Err)
	}
	if !strings.Contains(j.Raw, "Failed to download resource") {
		t.Fatalf("failed job raw: %q", j.Raw)
	}
	v, err := a.renderSessions(context.Background(), &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(texts(v), "Failed to download") {
		t.Fatal("Homebrew output shown outside 详情")
	}
}

// slowRunner holds every script until release is closed.
type slowRunner struct {
	release chan struct{}
	mu      sync.Mutex
	scripts int
}

func (s *slowRunner) run(ctx context.Context, m *Machine, script string, d time.Duration) (string, error) {
	s.mu.Lock()
	s.scripts++
	s.mu.Unlock()
	select {
	case <-s.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return fakeRunner(ctx, m, script, d)
}

// TestScanAndCleanInBackground: 扫描, 扫描全部机器 and the cleanups return at once and finish as
// background jobs (a UI request has about 12 seconds; a scan may take 90).
func TestScanAndCleanInBackground(t *testing.T) {
	noSandbox(t)
	a := newTestApp(t)
	ctx := context.Background()
	slow := &slowRunner{release: make(chan struct{})}
	a.runner = slow.run
	v, err := a.render(ctx, &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *wire.ActionResult, 1)
	go func() {
		res, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: v.State, Action: "scan", Values: map[string]string{"m": "local"}}})
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	var res *wire.ActionResult
	select {
	case res = <-done:
	case <-time.After(5 * time.Second):
		close(slow.release)
		t.Fatal("scan blocked the request")
	}
	if !strings.Contains(res.Toast, "已开始扫描") || res.View.RefreshAfter == 0 {
		t.Fatalf("toast %q refresh %d", res.Toast, res.View.RefreshAfter)
	}
	if err := wire.CheckView(res.View); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(texts(res.View), "正在扫描") {
		t.Fatalf("running scan not shown:\n%s", texts(res.View))
	}
	close(slow.release)
	a.waitJobs()
	var scanned bool
	_ = a.db().read(func(st *State) { scanned = st.Scans["local"] != nil })
	if !scanned {
		t.Fatal("background scan stored nothing")
	}
	if j := a.jobSnapshot()["local"]; j.Kind != jobScan || !strings.Contains(j.Note, "可回收") {
		t.Fatalf("scan job %+v", j)
	}

	a.runner = fakeRunner
	res, err = a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: v.State, Action: "clean-safe", Values: map[string]string{"m": "local"}}})
	if err != nil || !strings.Contains(res.Toast, "已开始清理") {
		t.Fatalf("clean: %v %+v", err, res)
	}
	a.waitJobs()
	if j := a.jobSnapshot()["local"]; j.Kind != jobClean || j.Err != "" || !strings.Contains(j.Note, "→") {
		t.Fatalf("clean job %+v", j)
	}
	var hist int
	_ = a.db().read(func(st *State) { hist = len(st.History) })
	if hist != 1 {
		t.Fatalf("history %d", hist)
	}

	// A failed scan is shown under the machine in plain words.
	if err := a.db().update(func(st *State, now time.Time) error {
		_, err := st.upsertMachine(MachineInput{Host: "offline-box", Name: "offline-box"}, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	res, err = a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: v.State, Action: "scan-all"}})
	if err != nil || !strings.Contains(res.Toast, "已开始扫描 2 台") {
		t.Fatalf("scan-all: %v %q", err, res.Toast)
	}
	a.waitJobs()
	j := a.jobSnapshot()["offline-box"]
	if !strings.Contains(j.Err, "offline-box") || !strings.Contains(j.Err, "超时") || strings.Contains(j.Err, "Connection timed out") {
		t.Fatalf("offline scan job %+v", j)
	}
	v, _ = a.render(ctx, &ext.UIRequest{})
	if all := texts(v); strings.Contains(all, "Connection timed out") || !strings.Contains(all, "超时") {
		t.Fatalf("fleet tab after failed scan:\n%s", all)
	}
}

// TestMCPStatusSpeaksPlainly: disklessly_sessions_status gives the same reason, next step and
// command as the page, and a failing tool call says what went wrong before its detail.
func TestMCPStatusSpeaksPlainly(t *testing.T) {
	noSandbox(t)
	a := newTestApp(t)
	coldFleet(t, a)
	srv := &mcpServer{store: a.db(), runner: a.run()}
	out, err := srv.call(context.Background(), "disklessly_sessions_status", []byte(`{"machine":"studio-mac"}`))
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)["machines"].([]map[string]any)[0]
	if res["state"] != ColdNoCLI || !strings.Contains(res["message"].(string), "studio-mac") ||
		!strings.Contains(res["command"].(string), "brew install nwverse/tap/disklessly-cli") || res["next"] == "" {
		t.Fatalf("status: %+v", res)
	}
	out, err = srv.call(context.Background(), "disklessly_sessions_status", []byte(`{"machine":"far-box"}`))
	if err != nil {
		t.Fatal(err)
	}
	res = out.(map[string]any)["machines"].([]map[string]any)[0]
	if msg := res["message"].(string); !strings.Contains(msg, "超时") || english.MatchString(msg) {
		t.Fatalf("far-box message %q", msg)
	}
	if !strings.Contains(res["detail"].(string), "Connection timed out") {
		t.Fatalf("far-box detail %+v", res)
	}
	text := toolErrorText(errors.New("SSH 连接 far-box 失败：ssh: connect to host far-box port 22: Connection timed out"))
	head, tail, _ := strings.Cut(text, "\n")
	if !strings.Contains(head, "far-box") || !strings.Contains(head, "超时") || english.MatchString(head) || !strings.Contains(tail, "详情") || !strings.Contains(tail, "Connection timed out") {
		t.Fatalf("tool error text %q", text)
	}
}
