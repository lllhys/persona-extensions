package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "disklessly-locator-")
	if err != nil {
		panic(err)
	}
	locatorFile = func() string { return filepath.Join(dir, "data-dir") }
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

var t0 = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)

const sampleProbeLocal = `@@SECTION:UNAME
Linux x86_64
@@SECTION:HOSTNAME
gcp-dev
@@SECTION:HOME
/home/dev
@@SECTION:BIN_VERSION
disklessly 0.3.0
@@SECTION:DF
Filesystem     1B-blocks         Used    Available Use% Mounted on
/dev/sda1   214748364800 182536110080  32212254720  85% /
@@SECTION:CACHES
cache-gobuild	go-cache	Go 编译构建缓存 (go-build)	/home/dev/.cache/go-build	4404019	safe	可随时重建的 Go 编译器构建缓存
cache-gomod	go-cache	Go 模块下载缓存 (pkg/mod)	/home/dev/go/pkg/mod	3145728	safe	可重新下载的 Go 依赖模块缓存
cache-pnpm	node-python-cache	pnpm 内容寻址缓存 (pnpm/store)	/home/dev/.local/share/pnpm/store	2621440	safe	可重新拉取的 pnpm 全局存储
cache-uv	node-python-cache	uv Python 极速缓存 (~/.cache/uv)	/home/dev/.cache/uv	1572864	safe	可重新下载的 uv 包缓存
cache-cargo-registry	rust-xcode-build	Cargo 索引与源码包 (~/.cargo/registry)	/home/dev/.cargo/registry	1048576	safe	可重新同步的 Rust Crates 缓存
cache-playwright	rust-xcode-build	Playwright 浏览器缓存 (ms-playwright)	/home/dev/.cache/ms-playwright	1258291	review	无头浏览器镜像，清理后首次运行需重新下载
@@SECTION:TMP_LOGS
Archived and active journals take up 1.5G in the file system.
logs-rotated	tmp-logs	系统轮转归档日志 (/var/log/*.gz)	/var/log	524288	safe	已轮转压缩的历史系统日志，不影响当前服务写入
tmp-stale	tmp-logs	/tmp 陈旧构建与临时产物 (>7天)	/tmp	314572	safe	超过 7 天或已退出的 go-build/playwright/npm 临时目录
@@SECTION:DOCKER
docker-dangling	docker-unused	Docker 悬空镜像 (dangling images)	docker://dangling-images	2097152	safe	未被任何运行或停止容器引用的悬空镜像层
docker-buildx	3.2GB (100%)
@@SECTION:SESSIONS
tool	claude	Claude Code	/home/dev/.claude/projects	18	245760	42	1887436
tool	codex	Codex	/home/dev/.codex/sessions	7	98304	19	629145
archive	/home/dev/.disklessly/session-archives	6	314572
@@SECTION:END
`

const sampleProbeOPC = `@@SECTION:UNAME
Linux aarch64
@@SECTION:HOSTNAME
opc-arm64
@@SECTION:HOME
/home/opc
@@SECTION:BIN_VERSION
@@SECTION:DF
Filesystem     1024-blocks      Used Available Capacity Mounted on
/dev/sda1        104857600  96468992   8388608      92% /
@@SECTION:CACHES
cache-gobuild	go-cache	Go 编译构建缓存 (go-build)	/home/opc/.cache/go-build	5242880	safe	可随时重建的 Go 编译器构建缓存
cache-pnpm	node-python-cache	pnpm 内容寻址缓存 (pnpm/store)	/home/opc/.local/share/pnpm/store	4194304	safe	可重新拉取的 pnpm 全局存储
@@SECTION:TMP_LOGS
Archived and active journals take up 800M in the file system.
@@SECTION:DOCKER
docker-buildx	2.5GB
@@SECTION:SESSIONS
tool	claude	Claude Code	/home/opc/.claude/projects	9	131072	31	1468006
@@SECTION:END
`

func fakeRunner(_ context.Context, m *Machine, script string, _ time.Duration) (string, error) {
	if strings.Contains(script, "@@SECTION:UNAME") {
		if m != nil && m.Host == "offline-box" {
			return "", fmt.Errorf("SSH 连接 offline-box 失败：Connection timed out")
		}
		if m != nil && (m.ID == "opc" || m.Host == "opc") {
			return sampleProbeOPC, nil
		}
		return sampleProbeLocal, nil
	}
	// Clean script simulation: report before/after df bytes
	return "@@RC 0\n@@BEFORE_B 8589934592\n@@AFTER_B 21797958656\n", nil
}

func newTestApp(t *testing.T) *app {
	t.Helper()
	a := newApp(t.TempDir())
	a.store.homeDir = "" // isolate from real host ~/.ssh/config in unit tests
	a.store.now = func() time.Time { return t0 }
	a.now = func() time.Time { return t0 }
	a.runner = fakeRunner
	return a
}

func TestConformance(t *testing.T) {
	a := newTestApp(t)
	exttest.Check(t, manifest, exttest.Serve(t, program(a)))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m struct {
		Contributes struct {
			MCPServers []struct {
				Allow []string `json:"allow"`
			} `json:"mcp_servers"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil || len(m.Contributes.MCPServers) != 1 {
		t.Fatalf("one mcp server: %v", err)
	}
	var readOnly []string
	for _, tl := range tools {
		if tl.readOnly {
			readOnly = append(readOnly, tl.name)
		}
	}
	if allow := m.Contributes.MCPServers[0].Allow; !slices.Equal(allow, readOnly) {
		t.Fatalf("allow %v, want exactly read-only tools %v", allow, readOnly)
	}
	if !strings.Contains(usage, "mcp") {
		t.Fatal("--help names mcp, which the manifest requires")
	}
	if _, err := os.Stat(filepath.Join("skills", "disklessly", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestSSHAndPersonaDiscovery(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".persona"), 0o700); err != nil {
		t.Fatal(err)
	}

	sshCfg := `
# Global defaults
Host *
    User defaultuser
    Port 22

Host opc
    HostName 198.51.100.10
    User opc
    IdentityFile ~/.ssh/id_ed25519

Host mm-2
    HostName 198.51.100.20
    Port 2222

Host *.internal !bastion.internal
    User internal
`
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(sshCfg), 0o600); err != nil {
		t.Fatal(err)
	}

	personaCfg := `{
  "machines": [
    {"id": "gcp", "name": "gcp (Cloud Workstation)", "host": "gcp", "user": "dev"},
    {"id": "local", "name": "local", "host": "localhost"}
  ]
}`
	if err := os.WriteFile(filepath.Join(home, ".persona", "config.json"), []byte(personaCfg), 0o600); err != nil {
		t.Fatal(err)
	}

	st := &State{}
	added := st.DiscoverMachines(home, t0)
	if added != 3 {
		t.Fatalf("DiscoverMachines added = %d, want 3 (gcp, opc, mm-2); machines=%+v", added, st.Machines)
	}

	opc := st.machine("opc")
	if opc == nil || opc.User != "opc" || opc.Port != 22 || !strings.Contains(opc.Name, "198.51.100.10") {
		t.Fatalf("opc machine = %+v", opc)
	}
	mm2 := st.machine("mm-2")
	if mm2 == nil || mm2.User != "defaultuser" || mm2.Port != 2222 {
		t.Fatalf("mm-2 machine (with wildcard User default) = %+v", mm2)
	}
	gcp := st.machine("gcp")
	if gcp == nil || gcp.Source != "persona" || gcp.Name != "gcp (Cloud Workstation)" {
		t.Fatalf("gcp machine = %+v", gcp)
	}

	// Deleting a discovered machine disables it so re-discovery does not re-enable it.
	if err := st.deleteMachine("opc"); err != nil {
		t.Fatal(err)
	}
	if st.DiscoverMachines(home, t0) != 0 {
		t.Fatal("disabled discovered machine must stay disabled on re-discovery")
	}
	if st.machine("opc").Enabled {
		t.Fatal("opc should remain disabled")
	}
	if err := st.deleteMachine("local"); err == nil {
		t.Fatal("local machine must not be deletable")
	}
}

func TestHealthEvaluationAndOrdering(t *testing.T) {
	if got := EvaluateHealth(false, 100*gib, 80*gib); got != HealthOffline {
		t.Fatalf("offline = %s", got)
	}
	if got := EvaluateHealth(true, 100*gib, 8*gib); got != HealthCritical {
		t.Fatalf("92%% used = %s, want critical", got)
	}
	if got := EvaluateHealth(true, 100*gib, 15*gib); got != HealthWarning {
		t.Fatalf("85%% used = %s, want warning", got)
	}
	if got := EvaluateHealth(true, 100*gib, 50*gib); got != HealthOK {
		t.Fatalf("50%% used = %s, want ok", got)
	}
}

func TestParseProbeAndCleanSafetyRedlines(t *testing.T) {
	m := &Machine{ID: "local", Host: "localhost", Name: "local (gcp-dev)", IsLocal: true, Enabled: true, Reachable: true}
	scan := ParseProbeOutput(m, sampleProbeLocal, t0)
	if scan.Platform != "linux-amd64" || scan.ScanMode != "disklessly-cli" || scan.BinaryVersion != "disklessly 0.3.0" {
		t.Fatalf("unexpected scan metadata: %+v", scan)
	}
	if scan.Mount.TotalBytes != 214748364800 || scan.Mount.AvailableBytes != 32212254720 {
		t.Fatalf("unexpected mount: %+v", scan.Mount)
	}
	if scan.Sessions.HotCount != 25 || scan.Sessions.ColdCount != 61 || scan.Sessions.ArchivedCount != 6 {
		t.Fatalf("unexpected sessions summary: %+v", scan.Sessions)
	}
	if len(scan.Items) < 10 {
		t.Fatalf("expected >= 10 scan items, got %d", len(scan.Items))
	}
	// Verify items are sorted by size descending
	for i := 1; i < len(scan.Items); i++ {
		if scan.Items[i].Bytes > scan.Items[i-1].Bytes {
			t.Fatalf("items not sorted descending at %d: %d > %d", i, scan.Items[i].Bytes, scan.Items[i-1].Bytes)
		}
	}

	// Verify dry-run plan excludes review items by default
	safePlan, err := BuildCleanPlan(m, scan, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range safePlan.Items {
		if it.Safety != SafetySafe {
			t.Fatalf("safe-only plan included non-safe item: %+v", it)
		}
	}
	fullPlan, err := BuildCleanPlan(m, scan, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(fullPlan.Items) <= len(safePlan.Items) {
		t.Fatalf("fullPlan (%d) should include review items beyond safePlan (%d)", len(fullPlan.Items), len(safePlan.Items))
	}

	// Verify safety redlines reject forbidden paths
	for _, badPath := range []string{
		"/",
		"/etc/passwd",
		"/usr/bin",
		"/home/dev",
		"/home/dev/.ssh",
		"/home/dev/.ssh/id_ed25519",
		"/home/dev/.persona/launch.key",
		"/home/dev/../dev",
		"relative/path",
		"docker://running-postgres-container",
	} {
		if err := CheckSafeCleanTarget(ScanItem{ID: "bad", Path: badPath}, "/home/dev"); err == nil {
			t.Errorf("CheckSafeCleanTarget accepted unsafe path %q", badPath)
		}
	}
}

func TestUIViewsAndActions(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	// Seed a second remote machine
	if err := a.db().update(func(st *State, now time.Time) error {
		_, err := st.upsertMachine(MachineInput{ID: "opc", Host: "opc", Name: "opc (Oracle Cloud ARM)", Source: "ssh-config"}, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	check := func(v *wire.View) {
		t.Helper()
		if err := wire.CheckView(v); err != nil {
			t.Fatalf("CheckView failed: %v\nview=%+v", err, v)
		}
	}

	// 1. Initial render (unscanned)
	v, err := a.render(ctx, &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	check(v)

	// 2. Scan all machines
	res, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: v.State, Action: "scan-all"}})
	if err != nil {
		t.Fatal(err)
	}
	check(res.View)

	// 3. Switch across all tabs and verify every view passes CheckView
	for _, tab := range []string{tabFleet, tabItems, tabPlan, tabSessions} {
		r, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{
			State:  res.View.State,
			Action: "tab",
			Values: map[string]string{"tab": tab},
		}})
		if err != nil {
			t.Fatalf("tab %s: %v", tab, err)
		}
		check(r.View)
		res = r
	}

	// 4. Search on items tab
	rSearch, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{
		State:  ext.EncodeState(page{Machine: "local", Tab: tabItems}),
		Action: "search",
		Values: map[string]string{"q": "go-build"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	check(rSearch.View)

	// 5. Archive cold sessions on local
	rArch, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{
		State:  ext.EncodeState(page{Machine: "local", Tab: tabSessions}),
		Action: "archive-sessions",
		Values: map[string]string{"m": "local"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	check(rArch.View)
	if !strings.Contains(rArch.Toast, "冷会话已归档") {
		t.Fatalf("unexpected toast: %s", rArch.Toast)
	}

	// 6. Execute safe clean on opc
	rClean, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{
		State:  ext.EncodeState(page{Machine: "opc", Tab: tabPlan}),
		Action: "clean-safe",
		Values: map[string]string{"m": "opc"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	check(rClean.View)

	// 7. New machine form & save
	rNew, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{
		State:  rClean.View.State,
		Action: "new-machine",
	}})
	if err != nil {
		t.Fatal(err)
	}
	check(rNew.View)

	rSave, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{
		State:  rNew.View.State,
		Action: "save-machine",
		Values: map[string]string{
			"host": "mm-2",
			"name": "mm-2 (Mac mini M2)",
			"user": "admin",
			"port": "22",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	check(rSave.View)

	// 8. Overview card
	cardView, err := a.card(ctx, &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	check(cardView)
}

func TestMCPOverStdio(t *testing.T) {
	dir := t.TempDir()
	st := newStore(dir)
	st.homeDir = ""
	st.now = func() time.Time { return t0 }
	srv := &mcpServer{store: st, runner: fakeRunner}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = srv.serve(ctx, inR, outW)
		_ = outW.Close()
	}()

	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	id := 0
	rpc := func(method string, params any) map[string]any {
		t.Helper()
		id++
		req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
		b, _ := json.Marshal(req)
		if _, err := inW.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
		if !sc.Scan() {
			t.Fatalf("no response for %s", method)
		}
		var resp map[string]any
		if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp["error"] != nil {
			t.Fatalf("%s returned error: %v", method, resp["error"])
		}
		res, _ := resp["result"].(map[string]any)
		return res
	}
	callTool := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res := rpc("tools/call", map[string]any{"name": name, "arguments": args})
		if res["isError"] == true {
			t.Fatalf("tool %s failed: %v", name, res["content"])
		}
		scMap, _ := res["structuredContent"].(map[string]any)
		return scMap
	}

	initRes := rpc("initialize", map[string]any{"protocolVersion": mcpProtocol})
	if initRes["protocolVersion"] != mcpProtocol {
		t.Fatalf("initialize: %v", initRes)
	}
	listRes := rpc("tools/list", map[string]any{})
	tList, _ := listRes["tools"].([]any)
	if len(tList) != 6 {
		t.Fatalf("tools/list count = %d, want 6", len(tList))
	}

	// 1. Upsert remote machine opc
	upRes := callTool("disklessly_machine_upsert", map[string]any{
		"host": "opc",
		"name": "opc (Oracle Cloud ARM)",
		"user": "opc",
	})
	if upRes["machine"] == nil {
		t.Fatalf("upsert result: %v", upRes)
	}

	// 2. Scan local and opc
	scanLocal := callTool("disklessly_scan", map[string]any{"machine": "local"})
	if scanLocal["scan"] == nil || scanLocal["dry_run_plan"] == nil {
		t.Fatalf("scan local: %v", scanLocal)
	}
	scanOPC := callTool("disklessly_scan", map[string]any{"machine": "opc"})
	if scanOPC["scan"] == nil {
		t.Fatalf("scan opc: %v", scanOPC)
	}

	// 3. Check fleet status
	statusRes := callTool("disklessly_machines_status", map[string]any{})
	if statusRes["machine_count"] != float64(2) {
		t.Fatalf("machine_count = %v, want 2", statusRes["machine_count"])
	}

	// 4. Execute clean on opc (dry-run then real)
	dryRes := callTool("disklessly_clean", map[string]any{"machine": "opc", "dry_run": true})
	if dryRes["dry_run"] != true || dryRes["record"] != nil {
		t.Fatalf("dry_run clean: %v", dryRes)
	}
	cleanRes := callTool("disklessly_clean", map[string]any{
		"machine":    "opc",
		"categories": []string{CatGoCache, CatColdAgentSession},
	})
	if cleanRes["record"] == nil {
		t.Fatalf("real clean should produce record: %v", cleanRes)
	}

	// 5. History ledger
	histRes := callTool("disklessly_history", map[string]any{"machine": "opc"})
	if histRes["count"] != float64(1) {
		t.Fatalf("history count = %v, want 1", histRes["count"])
	}

	// 6. Delete remote machine
	delRes := callTool("disklessly_machine_delete", map[string]any{"machine": "opc"})
	if delRes["deleted"] != "opc" {
		t.Fatalf("delete result: %v", delRes)
	}
	_ = inW.Close()
}
