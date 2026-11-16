package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// texts joins every text a view shows, for assertions.
func texts(v *wire.View) string {
	var b strings.Builder
	for _, s := range v.Sections {
		b.WriteString(s.Title + "\n" + s.Footer + "\n")
		for _, c := range s.Items {
			b.WriteString(strings.Join([]string{c.Title, c.Subtitle, c.Detail, c.Text, c.URL}, "\n") + "\n")
			if c.Badge != nil {
				b.WriteString(c.Badge.Text + "\n")
			}
			for _, st := range c.Stats {
				b.WriteString(st.Label + " " + st.Value + " " + st.Detail + "\n")
			}
		}
	}
	return b.String()
}

// machineItems returns the components of the machine-state section.
func machineItems(v *wire.View) []wire.Component {
	for _, s := range v.Sections {
		if s.Key == "cold-machines" {
			return s.Items
		}
	}
	return nil
}

// itemsOf returns machine id's row and the components drawn under it.
func itemsOf(v *wire.View, id string) []wire.Component {
	items := machineItems(v)
	var out []wire.Component
	in := false
	for _, c := range items {
		if c.Type == wire.ComponentRow {
			in = c.Key == "mach-"+id
		}
		if in {
			out = append(out, c)
		}
	}
	return out
}

func buttonIn(items []wire.Component, prefix string) *wire.Component {
	for i, c := range items {
		if c.Type == wire.ComponentButton && strings.HasPrefix(c.Title, prefix) {
			return &items[i]
		}
	}
	return nil
}

const usageAnswer = `{
  "error" : {
    "code" : 2,
    "message" : "unknown sessions command list",
    "name" : "EX_USAGE"
  },
  "ok" : false
}`

func TestColdStateClassifier(t *testing.T) {
	remote := func() *Machine {
		return &Machine{ID: "build-box", Host: "build-box", Name: "build-box", Enabled: true, Reachable: true}
	}
	list := coldListJSON(coldEntries...)
	okStatus := coldStatusJSON
	cases := []struct {
		name, out string
		err       error
		state     string
		cause     string
		want      string // in Error or Detail
	}{
		{name: "ready", out: coldRefreshOut(list, okStatus), state: ColdReady},
		{name: "no cli", out: "@@SECTION:COLD_UNAME\nLinux x86_64\n@@COLD_NOCLI\n", state: ColdNoCLI, want: "未安装"},
		{name: "old cli, pretty-printed usage answer", state: ColdCLIOld, want: "0.1.0",
			out: "@@SECTION:COLD_UNAME\nLinux x86_64\n@@SECTION:COLD_CLI\nversion\tdisklessly 0.1.0\npath\t/home/dev/.local/bin/disklessly\n" +
				"@@SECTION:COLD_LIST\n" + usageAnswer + "\n@@SECTION:COLD_STATUS\n" + strings.ReplaceAll(usageAnswer, "list", "status") + "\n@@SECTION:END\n"},
		{name: "old cli, status only", state: ColdCLIOld, want: "太旧",
			out: coldRefreshOut(list, strings.ReplaceAll(usageAnswer, "list", "status"))},
		{name: "no backend", state: ColdNoBackend,
			out: coldRefreshOut(list, `{"ok":true,"backend":{"id":"","type":"","reachable":false},"counts":{},"segments":{}}`)},
		{name: "no backend, refused", state: ColdNoBackend, want: "no backend configured",
			out: coldRefreshOut(`{"ok":false,"error":{"code":78,"message":"no backend configured","name":"EX_CONFIG"}}`, "")},
		{name: "backend unreachable", state: ColdBackendDown,
			out: coldRefreshOut(list, `{"ok":true,"backend":{"id":"main","type":"rclone","reachable":false,"latencyMs":3000,"error":"dial tcp: i/o timeout"},"counts":{},"segments":{}}`)},
		{name: "ssh timeout", err: errors.New("SSH 连接 build-box 失败：ssh: connect to host build-box port 22: Connection timed out"), state: ColdOffline, cause: causeTimeout},
		{name: "ssh auth", err: errors.New("SSH 连接 build-box 失败：dev@build-box: Permission denied (publickey)."), state: ColdOffline, cause: causeAuth},
		{name: "ssh refused", err: errors.New("SSH 连接 build-box 失败：ssh: connect to host build-box port 22: Connection refused"), state: ColdOffline, cause: causeRefused},
		{name: "cli hangs", out: "@@SECTION:COLD_LIST\n", err: errors.New("SSH 连接 build-box 失败：signal: killed"), state: ColdUnreadable},
		{name: "unreadable", out: coldRefreshOut("panic: something odd happened\ngoroutine 1", okStatus), state: ColdUnreadable, want: "panic: something odd happened"},
		{name: "other cli error", out: coldRefreshOut(`{"ok":false,"error":{"code":1,"message":"index locked","name":"EX_SOFTWARE"}}`, okStatus), state: ColdUnreadable, want: "index locked"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := &State{}
			m := remote()
			st.Machines = []*Machine{m}
			run := func(context.Context, *Machine, string, time.Duration) (string, error) { return c.out, c.err }
			cat, _ := RefreshColdIndex(context.Background(), run, st, m, t0)
			if cat.State != c.state || cat.stateCode() != c.state {
				t.Fatalf("state %q (error %q, detail %q), want %q", cat.State, cat.Error, cat.Detail, c.state)
			}
			if cat.Cause != c.cause {
				t.Fatalf("cause %q, want %q", cat.Cause, c.cause)
			}
			if c.want != "" && !strings.Contains(cat.Error+"|"+cat.Detail, c.want) {
				t.Fatalf("error %q / detail %q lacks %q", cat.Error, cat.Detail, c.want)
			}
			if c.state == ColdOffline && m.Reachable {
				t.Fatal("unreachable machine still marked reachable")
			}
			// Every state draws a valid row, and user-facing text never names the CLI's error code.
			d := draw{st: st, now: t0, loc: time.UTC}
			items := d.machineStateItems(m, cat, cat.stateCode())
			v := &wire.View{Sections: []wire.Section{{Items: items}}}
			if err := wire.CheckView(v); err != nil {
				t.Fatal(err)
			}
			if items[0].Badge == nil || items[0].Badge.Text != coldStateLabel(c.state) {
				t.Fatalf("badge %+v", items[0].Badge)
			}
			if strings.Contains(items[0].Badge.Text+items[0].Title, "EX_") {
				t.Fatalf("jargon in row: %+v", items[0])
			}
		})
	}
	// The real script parts the classifier depends on.
	s := coldListScript(false, true)
	for _, want := range []string{"@@SECTION:COLD_UNAME", "@@SECTION:COLD_CLI", "@@COLD_NOCLI", "sessions status --check-segments"} {
		if !strings.Contains(s, want) {
			t.Fatalf("refresh script lacks %s", want)
		}
	}
}

func TestOldColdCatalogLoads(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, coldDir), 0o700); err != nil {
		t.Fatal(err)
	}
	state := `{"machines":[{"id":"local","host":"localhost","name":"local","is_local":true,"source":"local","enabled":true,"reachable":true},
{"id":"build-box","host":"build-box","name":"build-box","source":"manual","enabled":true,"reachable":true},
{"id":"studio-mac","host":"studio-mac","name":"studio-mac","source":"manual","enabled":true,"reachable":true}]}`
	files := map[string]string{
		stateFile:                                 state,
		filepath.Join(coldDir, "local.json"):      `{"machine_id":"local","backend":{"id":"main","type":"rclone","reachable":true,"latency_ms":40,"checked_at":"2026-11-09T09:00:00Z"},"total":0,"refreshed_at":"2026-11-09T09:00:00Z"}`,
		filepath.Join(coldDir, "build-box.json"):  `{"machine_id":"build-box","backend":{},"total":0,"error":"目标机器未安装 disklessly CLI，无法读取云端会话索引"}`,
		filepath.Join(coldDir, "studio-mac.json"): `{"machine_id":"studio-mac","backend":{},"total":0,"error":"disklessly sessions list 没有返回可解析的结果"}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := newTestApp(t)
	a.store = newStore(dir)
	a.store.homeDir = ""
	a.store.now = func() time.Time { return t0 }
	want := map[string]string{"local": ColdReady, "build-box": ColdNoCLI, "studio-mac": ColdUnreadable}
	if err := a.db().read(func(st *State) {
		for id, w := range want {
			if got := st.cold(id).stateCode(); got != w {
				t.Errorf("%s: %s, want %s", id, got, w)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	v, err := a.renderSessions(context.Background(), &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
	if got := texts(v); !strings.Contains(got, "机器 3 台 1 回答无法识别 · 1 未安装 CLI · 1 正常") {
		t.Fatalf("summary:\n%s", got)
	}
}

// blockingRunner answers cold-index scripts; on machine slow it waits until release is closed.
type blockingRunner struct {
	release chan struct{}
	mu      sync.Mutex
	calls   map[string]int
}

func (b *blockingRunner) run(ctx context.Context, m *Machine, s string, d time.Duration) (string, error) {
	if strings.Contains(s, "sessions list --state all") && strings.Contains(s, "sessions status") {
		b.mu.Lock()
		b.calls[m.ID]++
		b.mu.Unlock()
	}
	if m.ID == "build-box" {
		select {
		case <-b.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return coldRunner(nil)(ctx, m, s, d)
}

func TestBackgroundRefreshDoesNotBlock(t *testing.T) {
	a := newTestApp(t)
	b := &blockingRunner{release: make(chan struct{}), calls: map[string]int{}}
	a.runner = b.run
	if err := a.db().update(func(st *State, now time.Time) error {
		_, err := st.upsertMachine(MachineInput{Host: "build-box", Name: "build-box"}, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	act := func(state string, values map[string]string) *wire.ActionResult {
		t.Helper()
		done := make(chan *wire.ActionResult, 1)
		go func() {
			res, err := a.actionSessions(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: state, Action: "cold-refresh", Values: values}})
			if err != nil {
				t.Error(err)
			}
			done <- res
		}()
		select {
		case res := <-done:
			if res == nil {
				t.FailNow()
			}
			if err := wire.CheckView(res.View); err != nil {
				t.Fatal(err)
			}
			return res
		case <-time.After(10 * time.Second):
			close(b.release)
			t.Fatal("the refresh action waited for the slow machine")
		}
		return nil
	}
	res := act("", map[string]string{"m": ""})
	if !strings.Contains(res.Toast, "已开始读取 2 台机器") {
		t.Fatalf("toast: %s", res.Toast)
	}
	if res.View.RefreshAfter != coldPollSeconds {
		t.Fatalf("refresh_after %d while reading", res.View.RefreshAfter)
	}
	if !strings.Contains(texts(res.View), "正在后台读取") || !strings.Contains(texts(res.View), "build-box") {
		t.Fatalf("no progress shown:\n%s", texts(res.View))
	}
	if rows := itemsOf(res.View, "build-box"); len(rows) == 0 || rows[0].Badge.Text != "进行中" {
		t.Fatalf("build-box row: %+v", rows)
	}

	// A second tap while build-box is still being read does not start it again.
	again := act(res.View.State, map[string]string{"m": "build-box"})
	if !strings.Contains(again.Toast, "已在读取") {
		t.Fatalf("second tap: %s", again.Toast)
	}

	close(b.release)
	a.waitJobs()
	b.mu.Lock()
	if b.calls["build-box"] != 1 {
		t.Fatalf("build-box refreshed %d times", b.calls["build-box"])
	}
	b.mu.Unlock()
	v, err := a.renderSessions(ctx, &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if v.RefreshAfter != 0 {
		t.Fatalf("still polling: %d", v.RefreshAfter)
	}
	rows := itemsOf(v, "build-box")
	if len(rows) == 0 || rows[0].Badge.Text != coldStateLabel(ColdReady) || !strings.Contains(rows[0].Detail, "已索引 4 个") {
		t.Fatalf("build-box after refresh: %+v", rows)
	}
	if !strings.Contains(texts(v), "2 正常") {
		t.Fatalf("summary:\n%s", texts(v))
	}
}

// fleetForInstall sets up machines with known CLIs: build-box (linux, a self-contained 0.3.0),
// old-box (linux, 0.1.0 too old), studio-mac (darwin, Homebrew) and mini-mac (darwin, no CLI).
func fleetForInstall(t *testing.T, a *app) {
	t.Helper()
	yes := true
	err := a.db().update(func(st *State, now time.Time) error {
		for _, h := range []string{"build-box", "old-box", "studio-mac", "mini-mac"} {
			if _, err := st.upsertMachine(MachineInput{Host: h, Name: h}, now); err != nil {
				return err
			}
		}
		home := "/home/dev/.local/opt/disklessly-0.3.0-linux-amd64"
		st.setCold("build-box", &ColdCatalog{MachineID: "build-box", State: ColdReady, Platform: "linux-amd64", RefreshedAt: now, CheckedAt: now,
			Backend: ColdBackend{ID: "main", Reachable: &yes, CheckedAt: now},
			CLI:     ColdCLI{Version: "disklessly 0.3.0", Path: "/home/dev/.local/bin/disklessly", Real: home + "/bin/disklessly", Home: home, KB: 30000}})
		st.setCold("old-box", &ColdCatalog{MachineID: "old-box", State: ColdCLIOld, Platform: "linux-amd64", CheckedAt: now,
			Error: "这台电脑上的 disklessly 0.1.0 太旧", CLI: ColdCLI{Version: "disklessly 0.1.0"}})
		st.setCold("studio-mac", &ColdCatalog{MachineID: "studio-mac", State: ColdReady, Platform: "darwin-arm64", RefreshedAt: now, CheckedAt: now,
			Backend: ColdBackend{ID: "main", Reachable: &yes, CheckedAt: now},
			CLI: ColdCLI{Version: "disklessly 0.3.0", Path: "/opt/homebrew/bin/disklessly", Real: "/opt/homebrew/Cellar/disklessly/0.3.0/bin/disklessly",
				Home: "/opt/homebrew/Cellar/disklessly/0.3.0"}})
		st.setCold("mini-mac", &ColdCatalog{MachineID: "mini-mac", State: ColdNoCLI, Platform: "darwin-arm64", CheckedAt: now})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCopyInstallOffer(t *testing.T) {
	a := newTestApp(t)
	fleetForInstall(t, a)
	v, err := a.renderSessions(context.Background(), &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
	old := itemsOf(v, "old-box")
	btn := buttonIn(old, "从 build-box 复制安装")
	if btn == nil {
		t.Fatalf("old-box offers no copy-install: %+v", old)
	}
	for _, w := range []string{"build-box", "old-box", "0.3.0", "~/.local/opt/disklessly-0.3.0-linux-amd64", "原来的版本保留"} {
		if !strings.Contains(btn.Action.Confirm, w) {
			t.Fatalf("confirm lacks %q: %s", w, btn.Action.Confirm)
		}
	}
	if btn.Action.Values["m"] != "old-box" || btn.Action.Values["id"] != "build-box" {
		t.Fatalf("install values: %v", btn.Action.Values)
	}
	if !strings.Contains(old[0].Detail, "0.1.0") || !strings.Contains(old[0].Detail, "太旧") {
		t.Fatalf("old-box detail: %s", old[0].Detail)
	}

	// Across platforms nothing is offered; the only darwin CLI is a Homebrew install, so the row says why.
	mini := itemsOf(v, "mini-mac")
	if buttonIn(mini, "从 ") != nil {
		t.Fatalf("mini-mac offered a copy: %+v", mini)
	}
	if !strings.Contains(mini[0].Detail, "studio-mac") || !strings.Contains(mini[0].Detail, "不能复制") {
		t.Fatalf("mini-mac reason: %s", mini[0].Detail)
	}
	hasLink := false
	for _, c := range mini {
		hasLink = hasLink || (c.Type == wire.ComponentLink && c.URL == coldInstallDocsURL)
	}
	if !hasLink {
		t.Fatal("no install documentation link")
	}
	if _, err := a.coldAction(context.Background(), "cold-install", "mini-mac", "studio-mac"); err == nil {
		t.Fatal("copy-install across a non-copyable source accepted")
	}
	// Worst first: the ready machines come last.
	var order []string
	for _, c := range machineItems(v) {
		if c.Type == wire.ComponentRow {
			order = append(order, strings.TrimPrefix(c.Key, "mach-"))
		}
	}
	if order[len(order)-1] == "old-box" || order[0] == "build-box" {
		t.Fatalf("order: %v", order)
	}

	// The action runs in the background, then re-reads the target.
	var scripts []string
	var mu sync.Mutex
	a.streamer = func(ctx context.Context, m *Machine, s string, in io.Reader, out io.Writer, _ time.Duration) error {
		mu.Lock()
		scripts = append(scripts, m.ID+": "+strings.SplitN(s, "\n", 2)[0])
		mu.Unlock()
		if m.ID == "old-box" {
			_, _ = io.Copy(io.Discard, in)
			_, err := io.WriteString(out, "@@INSTALL_PREV /home/dev/.local/opt/disklessly-0.1.0-linux-amd64/bin/disklessly\n@@INSTALL_OK disklessly 0.3.0\n")
			return err
		}
		_, err := io.WriteString(out, "tar bytes")
		return err
	}
	var refreshed []string
	a.runner = func(ctx context.Context, m *Machine, s string, d time.Duration) (string, error) {
		mu.Lock()
		refreshed = append(refreshed, m.ID)
		mu.Unlock()
		return coldRunner(nil)(ctx, m, s, d)
	}
	toast, err := a.coldAction(context.Background(), "cold-install", "old-box", "build-box")
	if err != nil || !strings.Contains(toast, "已开始从「build-box」复制安装到「old-box」") {
		t.Fatalf("install: %q %v", toast, err)
	}
	a.waitJobs()
	j := a.jobSnapshot()["old-box"]
	if j.Err != "" || !strings.Contains(j.Note, "已从 build-box 复制安装 disklessly 0.3.0") || !strings.Contains(j.Note, "disklessly-0.1.0") {
		t.Fatalf("install job: %+v", j)
	}
	if len(scripts) != 2 || len(refreshed) == 0 || refreshed[0] != "old-box" {
		t.Fatalf("scripts %v, refreshed %v", scripts, refreshed)
	}
}

// fakeCLI writes a disklessly stand-in into home/.local/opt/<dir>/bin that reports version and
// answers sessions status with statusAnswer.
func fakeCLI(t *testing.T, home, dir, version, statusAnswer string) string {
	t.Helper()
	root := filepath.Join(home, ".local", "opt", dir)
	for _, d := range []string{"bin", "share/disklessly"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\ncase \"$*\" in\n  version) echo \"disklessly " + version + "\" ;;\n  *\"sessions status\"*) cat <<'EOF'\n" + statusAnswer + "\nEOF\n  ;;\n  *) echo '{\"ok\":false}' ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "disklessly"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "share", "disklessly", "NOTICE"), []byte("synthetic\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// shellStreamer runs stream scripts under /bin/sh with a per-machine HOME.
func shellStreamer(homes map[string]string) StreamRunner {
	return func(ctx context.Context, m *Machine, s string, in io.Reader, out io.Writer, _ time.Duration) error {
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", s)
		cmd.Env = []string{"HOME=" + homes[m.ID], "PATH=/usr/bin:/bin"}
		cmd.Stdin, cmd.Stdout = in, out
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return errors.New(err.Error() + ": " + stderr.String())
		}
		return nil
	}
}

func TestCopyInstallScript(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("no tar")
	}
	const okStatus = "{\n  \"ok\" : true,\n  \"backend\" : {\"id\":\"main\",\"type\":\"rclone\",\"reachable\":true}\n}"
	setup := func(t *testing.T) (src, dst *Machine, homes map[string]string, link, oldTarget string) {
		srcHome, dstHome := t.TempDir(), t.TempDir()
		oldRoot := fakeCLI(t, dstHome, "disklessly-0.1.0-linux-amd64", "0.1.0", usageAnswer)
		link = filepath.Join(dstHome, ".local", "bin", "disklessly")
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		oldTarget = filepath.Join(oldRoot, "bin", "disklessly")
		if err := os.Symlink(oldTarget, link); err != nil {
			t.Fatal(err)
		}
		homes = map[string]string{"build-box": srcHome, "old-box": dstHome}
		return &Machine{ID: "build-box", Host: "build-box", Name: "build-box"}, &Machine{ID: "old-box", Host: "old-box", Name: "old-box"}, homes, link, oldTarget
	}

	t.Run("happy path", func(t *testing.T) {
		src, dst, homes, link, oldTarget := setup(t)
		root := fakeCLI(t, homes["build-box"], "disklessly-0.3.0-linux-amd64", "0.3.0", okStatus)
		o, err := copyInstall(context.Background(), shellStreamer(homes), src, dst, root)
		if err != nil {
			t.Fatal(err)
		}
		if o.version != "disklessly 0.3.0" || o.prev != oldTarget || o.shadow != "" {
			t.Fatalf("outcome %+v", o)
		}
		final := filepath.Join(homes["old-box"], ".local", "opt", "disklessly-0.3.0-linux-amd64")
		if got, _ := os.Readlink(link); got != filepath.Join(final, "bin", "disklessly") {
			t.Fatalf("link -> %q", got)
		}
		if _, err := os.Stat(filepath.Join(final, "share", "disklessly", "NOTICE")); err != nil {
			t.Fatal("share/ not copied")
		}
		if _, err := os.Stat(oldTarget); err != nil {
			t.Fatal("previous install removed")
		}
		if _, err := os.Stat(final + ".new"); !os.IsNotExist(err) {
			t.Fatal("staging directory left behind")
		}
		// The installed CLI is the one the extension's lookup finds.
		cmd := exec.Command("/bin/sh", "-c", resolveCLIScript+"\nprintf '%s' \"$DKL\"")
		cmd.Env = []string{"HOME=" + homes["old-box"], "PATH=/usr/bin:/bin"}
		if out, _ := cmd.Output(); string(out) != link {
			t.Fatalf("lookup finds %q", out)
		}
	})

	t.Run("verify failure keeps the old link", func(t *testing.T) {
		src, dst, homes, link, oldTarget := setup(t)
		root := fakeCLI(t, homes["build-box"], "disklessly-0.2.0-linux-amd64", "0.2.0", strings.ReplaceAll(usageAnswer, "list", "status"))
		_, err := copyInstall(context.Background(), shellStreamer(homes), src, dst, root)
		if err == nil || !strings.Contains(err.Error(), "不支持 sessions status") {
			t.Fatalf("err %v", err)
		}
		if got, _ := os.Readlink(link); got != oldTarget {
			t.Fatalf("link changed to %q", got)
		}
		opt := filepath.Join(homes["old-box"], ".local", "opt")
		for _, name := range []string{"disklessly-0.2.0-linux-amd64", "disklessly-0.2.0-linux-amd64.new"} {
			if _, err := os.Stat(filepath.Join(opt, name)); !os.IsNotExist(err) {
				t.Fatalf("%s left behind", name)
			}
		}
	})

	t.Run("missing source", func(t *testing.T) {
		src, dst, homes, link, oldTarget := setup(t)
		_, err := copyInstall(context.Background(), shellStreamer(homes), src, dst, filepath.Join(homes["build-box"], ".local", "opt", "disklessly-9.9.9"))
		if err == nil {
			t.Fatal("install from a missing directory succeeded")
		}
		if got, _ := os.Readlink(link); got != oldTarget {
			t.Fatalf("link changed to %q", got)
		}
	})

	if _, err := copyInstall(context.Background(), shellStreamer(nil), &Machine{}, &Machine{}, "/usr/local"); err == nil {
		t.Fatal("a shared prefix accepted as an install directory")
	}
}

func TestChipLabelsCarryNoAddress(t *testing.T) {
	a := newTestApp(t)
	if err := a.db().update(func(st *State, now time.Time) error {
		for _, in := range []MachineInput{
			{ID: "build-box", Host: "build-box", Name: "build-box (192.0.2.10)", Source: "ssh-config"},
			{ID: "studio-mac", Host: "studio-mac", Name: "studio-mac (studio-mac.example.net)", Source: "ssh-config"},
			{ID: "gpu", Host: "198.51.100.7", Name: "gpu box (198.51.100.7)", Source: "manual"},
		} {
			if _, err := st.upsertMachine(in, now); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	v, err := a.renderSessions(context.Background(), &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	main, err := a.render(context.Background(), &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range []*wire.View{v, main} {
		for _, c := range view.Filter.Choices {
			if strings.ContainsAny(c.Title, "()") && c.Value != "local" || strings.Contains(c.Title, "192.0.2") || strings.Contains(c.Title, "example.net") || strings.Contains(c.Title, "198.51") {
				t.Fatalf("chip %q shows an address", c.Title)
			}
		}
	}
	for _, w := range []string{"build-box", "studio-mac", "gpu box"} {
		if !strings.Contains(texts(v), w) {
			t.Fatalf("machine %q missing from rows", w)
		}
	}
	if got := machineLabel(&Machine{ID: "x", Host: "x", Name: "lab (gpu)"}); got != "lab (gpu)" {
		t.Fatalf("a name's own parenthesis dropped: %q", got)
	}
}

// TestRefreshScriptReadsCLILayout runs the real refresh script under /bin/sh against a stand-in
// CLI linked from ~/.local/bin: it reports the platform, the version and the install directory,
// and the CLI's pretty-printed usage answer classifies it as too old.
func TestRefreshScriptReadsCLILayout(t *testing.T) {
	home := t.TempDir()
	root := fakeCLI(t, home, "disklessly-0.1.0-linux-amd64", "0.1.0", usageAnswer)
	// Every other command (sessions list) gets the same usage answer, over several lines.
	script := "#!/bin/sh\ncase \"$*\" in\n  version) echo \"disklessly 0.1.0\" ;;\n  *) cat <<'EOF'\n" + usageAnswer + "\nEOF\n  ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "disklessly"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".local", "bin", "disklessly")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../opt/disklessly-0.1.0-linux-amd64/bin/disklessly", link); err != nil {
		t.Fatal(err)
	}
	run := func(ctx context.Context, _ *Machine, s string, _ time.Duration) (string, error) {
		cmd := exec.CommandContext(ctx, "/bin/sh", "-s")
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
		cmd.Stdin = strings.NewReader(s)
		out, err := cmd.Output()
		return string(out), err
	}
	m := &Machine{ID: "old-box", Host: "old-box", Name: "old-box", Enabled: true}
	st := &State{Machines: []*Machine{m}}
	cat, err := RefreshColdIndex(context.Background(), run, st, m, t0)
	if err == nil || cat.State != ColdCLIOld || !strings.Contains(cat.Error, "0.1.0") {
		t.Fatalf("state %q error %v", cat.State, err)
	}
	realRoot, _ := filepath.EvalSymlinks(root)
	if cat.CLI.Path != link || cat.CLI.Home != realRoot || cat.CLI.KB == 0 || cat.CLI.Version != "disklessly 0.1.0" {
		t.Fatalf("cli %+v (want home %s)", cat.CLI, realRoot)
	}
	if ok, why := cat.CLI.selfContained(); !ok {
		t.Fatalf("not self-contained: %s", why)
	}
	if cat.Platform == "" {
		t.Fatal("no platform")
	}
}
