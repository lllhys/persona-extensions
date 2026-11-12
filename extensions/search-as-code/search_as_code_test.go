package main

import (
	"bufio"
	"context"
	"encoding/json"
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

var t0 = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "search-as-code-test-")
	if err != nil {
		panic(err)
	}
	locatorFile = func() string { return filepath.Join(dir, "data-dir") }
	defaultSessionDirs = func() []string { return nil }
	defaultColdTextDirs = func() []string { return nil }
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newApp(t.TempDir()))))
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
		t.Fatalf("expected 1 mcp server: %v", err)
	}
	var readOnly []string
	for _, tl := range tools {
		if tl.readOnly {
			readOnly = append(readOnly, tl.name)
		}
	}
	if allow := m.Contributes.MCPServers[0].Allow; !slices.Equal(allow, readOnly) {
		t.Fatalf("allow %v, want read-only tools %v", allow, readOnly)
	}
	if !strings.Contains(usage, "mcp") {
		t.Fatal("--help must mention mcp")
	}
	if _, err := os.Stat(filepath.Join("skills", "search-as-code", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

// seedFixtures writes synthetic Claude Code and Codex session transcripts plus a local repo file.
func seedFixtures(t *testing.T) (sessionDir, repoDir string) {
	t.Helper()
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude", "projects", "-workspace-persona-hub")
	codexDir := filepath.Join(root, ".codex", "sessions", "2026", "11")
	repoDir = filepath.Join(root, "persona-hub")
	for _, d := range []string{claudeDir, codexDir, filepath.Join(repoDir, "pkg", "gateway")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Claude Code session with conversational turns, noisy tool output, and a 429 quota error on pkg/gateway/quota.go
	claudeLines := []string{
		`{"type":"user","sessionId":"sess-claude-01","cwd":"/workspace/persona-hub","gitBranch":"fix/quota-cooldown","timestamp":"2026-11-09T09:10:00Z","message":{"role":"user","content":"排查一下网关单账号 429 冷却为什么导致全局任务暂停？"}}`,
		`{"type":"assistant","sessionId":"sess-claude-01","cwd":"/workspace/persona-hub","gitBranch":"fix/quota-cooldown","timestamp":"2026-11-09T09:11:00Z","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","name":"Read","input":{"file_path":"pkg/gateway/quota.go"}},{"type":"text","text":"定位到了 pkg/gateway/quota.go：当单个网关账号返回 429 时直接设置了 globalCooldown，应该仅在所有活跃账号都不可用时才触发全局冷却。"}]}}`,
		`{"type":"user","sessionId":"sess-claude-01","timestamp":"2026-11-09T09:11:30Z","message":{"role":"user","content":[{"type":"tool_result","content":"RAW_TOOL_DUMP_NOISE_TOKEN_998877 line 1\nline 2"}]}}`,
		`{"type":"user","sessionId":"sess-claude-01","timestamp":"2026-11-09T09:12:00Z","message":{"role":"user","content":[{"type":"tool_result","is_error":true,"content":"Error: gateway account pool returned HTTP 429 quota exceeded in pkg/gateway/quota.go"}]}}`,
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "sess-claude-01.jsonl"), []byte(strings.Join(claudeLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Codex session touching the same file pkg/gateway/quota.go
	codexLines := []string{
		`{"type":"session_meta","session_id":"sess-codex-02","timestamp":"2026-11-08T18:00:00Z","payload":{"cwd":"/workspace/persona-hub","model":"gpt-5-codex"}}`,
		`{"type":"response_item","session_id":"sess-codex-02","timestamp":"2026-11-08T18:01:00Z","payload":{"role":"user","content":"为 pkg/gateway/quota.go 补充多账号池轮询与冷却恢复单测"}}`,
		`{"type":"response_item","session_id":"sess-codex-02","timestamp":"2026-11-08T18:02:00Z","payload":{"role":"assistant","model":"gpt-5-codex","content":"已在 pkg/gateway/quota.go 增加 AllAccountsCoolingDown 判断并覆盖回归测试。"}}`,
	}
	if err := os.WriteFile(filepath.Join(codexDir, "sess-codex-02.jsonl"), []byte(strings.Join(codexLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. Repo file pkg/gateway/quota.go
	repoCode := "package gateway\n\n// AllAccountsCoolingDown reports whether every active account is in quota cooldown.\nfunc AllAccountsCoolingDown() bool {\n\treturn false\n}\n"
	if err := os.WriteFile(filepath.Join(repoDir, "pkg", "gateway", "quota.go"), []byte(repoCode), 0o644); err != nil {
		t.Fatal(err)
	}

	return root, repoDir
}

func TestCASSSessionScannerAndScopes(t *testing.T) {
	sessionDir, repoDir := seedFixtures(t)
	st := &State{
		DefaultScope: "conversation",
		SessionDirs:  []string{sessionDir},
		RepoDirs:     []string{repoDir},
	}

	// 1. In "conversation" scope (Without tools), noisy tool output token must NOT match
	progConv, err := CompileProgram(CompileInput{
		Query: "RAW_TOOL_DUMP_NOISE_TOKEN_998877",
		Scope: "conversation",
	})
	if err != nil {
		t.Fatal(err)
	}
	resConv := ExecuteProgram(st, progConv, t0)
	if len(resConv.Hits) != 0 {
		t.Fatalf("conversation scope should exclude tool noise, got %d hits", len(resConv.Hits))
	}

	// 2. In "all" scope (With tools), noisy tool output token MUST match when exclude_tools is false
	f := false
	progAll, err := CompileProgram(CompileInput{
		Query:        "RAW_TOOL_DUMP_NOISE_TOKEN_998877",
		Scope:        "all",
		ExcludeTools: &f,
	})
	if err != nil {
		t.Fatal(err)
	}
	resAll := ExecuteProgram(st, progAll, t0)
	if len(resAll.Hits) != 1 || resAll.Hits[0].SessionID != "sess-claude-01" {
		t.Fatalf("all scope should match tool output, got %+v", resAll.Hits)
	}

	// 3. User-only vs Assistant-only scopes
	progUser, _ := CompileProgram(CompileInput{Query: "为什么导致全局任务暂停", Scope: "user"})
	resUser := ExecuteProgram(st, progUser, t0)
	if len(resUser.Hits) != 1 || resUser.Hits[0].Snippets[0].Role != "User" {
		t.Fatalf("user scope expected User snippet, got %+v", resUser.Hits)
	}

	progAsst, _ := CompileProgram(CompileInput{Query: "globalCooldown", Scope: "assistant"})
	resAsst := ExecuteProgram(st, progAsst, t0)
	if len(resAsst.Hits) != 1 || resAsst.Hits[0].Snippets[0].Role != "Assistant" {
		t.Fatalf("assistant scope expected Assistant snippet, got %+v", resAsst.Hits)
	}

	// 4. Errors scope
	progErr, _ := CompileProgram(CompileInput{Query: "429", Scope: "errors"})
	resErr := ExecuteProgram(st, progErr, t0)
	if len(resErr.Hits) != 1 || resErr.Hits[0].ErrorCount == 0 || resErr.Hits[0].Snippets[0].Role != "Error" {
		t.Fatalf("errors scope expected Error hit, got %+v", resErr.Hits)
	}
}

func TestPipelineOperatorsAndCrossSessionCorrelation(t *testing.T) {
	sessionDir, repoDir := seedFixtures(t)
	st := &State{
		DefaultScope: "conversation",
		SessionDirs:  []string{sessionDir},
		RepoDirs:     []string{repoDir},
	}

	// Natural language query with inline operators
	prog, err := CompileProgram(CompileInput{
		Query: "quota.go project:persona-hub file:*.go group:harness correlate:file without:tools",
		Scope: "conversation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prog.CodeListing, "scan_sessions") ||
		!strings.Contains(prog.CodeListing, "scan_files") ||
		!strings.Contains(prog.CodeListing, "exclude_tools()") ||
		!strings.Contains(prog.CodeListing, `correlate(by="file")`) ||
		!strings.Contains(prog.CodeListing, `group_by(field="harness")`) {
		t.Fatalf("unexpected compiled code:\n%s", prog.CodeListing)
	}

	res := ExecuteProgram(st, prog, t0)
	if len(res.Hits) < 3 {
		t.Fatalf("expected both sessions and repo file in hits, got %d", len(res.Hits))
	}
	if len(res.StepTraces) != len(prog.Steps) {
		t.Fatalf("step traces %d != steps %d", len(res.StepTraces), len(prog.Steps))
	}
	// Verify cross-session and cross-file correlation on pkg/gateway/quota.go
	foundCorrelation := false
	for _, h := range res.Hits {
		if len(h.Correlated) > 0 {
			foundCorrelation = true
			break
		}
	}
	if !foundCorrelation {
		t.Fatal("expected correlate(by=\"file\") to link sessions and repo file sharing pkg/gateway/quota.go")
	}

	// Explicit pipeline with where, match_regex, dedup, group_by, rank, limit
	explicitCode := `
scan_sessions(query="", scope="all")
|> where(project="persona-hub")
|> exclude_tools()
|> match_regex(pattern="quota|冷却")
|> correlate(by="file")
|> dedup(by="session")
|> group_by(field="model")
|> rank(by="errors_first")
|> limit(n=5)
`
	prog2, err := CompileProgram(CompileInput{Code: explicitCode})
	if err != nil {
		t.Fatal(err)
	}
	res2 := ExecuteProgram(st, prog2, t0)
	if len(res2.Hits) != 2 {
		t.Fatalf("expected 2 deduplicated sessions, got %d", len(res2.Hits))
	}
	// Errors-first ranking puts sess-claude-01 (which has an error) before sess-codex-02
	if res2.Hits[0].SessionID != "sess-claude-01" {
		t.Fatalf("expected sess-claude-01 first under errors_first rank, got %s", res2.Hits[0].SessionID)
	}
	if len(res2.Groups) != 2 {
		t.Fatalf("expected 2 model groups (claude-opus-5-5 and gpt-5-codex), got %+v", res2.Groups)
	}
}

func TestUIViewsAndActionsPassCheckView(t *testing.T) {
	sessionDir, repoDir := seedFixtures(t)
	a := newApp(t.TempDir())
	a.now = func() time.Time { return t0 }
	_ = a.db().update(func(st *State, _ time.Time) error {
		st.SessionDirs = []string{sessionDir}
		st.RepoDirs = []string{repoDir}
		return nil
	})

	ctx := context.Background()

	// Check overview card
	cardView, err := a.card(ctx, &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(cardView); err != nil {
		t.Fatalf("card CheckView: %v", err)
	}

	// Check all 4 home tabs
	for _, tab := range []string{"results", "code", "recipes", "sources"} {
		v, err := a.render(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(page{Tab: tab})}})
		if err != nil {
			t.Fatalf("render tab %s: %v", tab, err)
		}
		if err := wire.CheckView(v); err != nil {
			t.Fatalf("CheckView tab %s: %v", tab, err)
		}
	}

	// Run a recipe via action
	actRes, err := a.action(ctx, &ext.UIRequest{
		UIParams: wire.UIParams{
			State:  ext.EncodeState(page{Tab: "recipes"}),
			Action: "run-recipe",
			Values: map[string]string{"id": "error-triage"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(actRes.View); err != nil {
		t.Fatalf("run-recipe CheckView: %v", err)
	}

	// Render hit detail page
	hitView, err := a.render(ctx, &ext.UIRequest{
		UIParams: wire.UIParams{
			State: ext.EncodeState(page{Mode: modeHit, HitID: "session:sess-claude-01"}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(hitView); err != nil {
		t.Fatalf("hit detail CheckView: %v", err)
	}

	// Render recipe edit form and save a new custom recipe
	formView, err := a.render(ctx, &ext.UIRequest{
		UIParams: wire.UIParams{
			State: ext.EncodeState(page{Mode: modeRecipeEdit}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(formView); err != nil {
		t.Fatalf("recipe form CheckView: %v", err)
	}

	saveRes, err := a.action(ctx, &ext.UIRequest{
		UIParams: wire.UIParams{
			State:  ext.EncodeState(page{Mode: modeRecipeEdit}),
			Action: "save-recipe",
			Values: map[string]string{
				"id":           "quota-audit",
				"name":         "配额冷却专项排查",
				"description":  "检索所有网关 429 冷却会话并按模型分组",
				"recipe_scope": "conversation",
				"code":         `scan_sessions(query="quota", scope="conversation") |> exclude_tools() |> group_by(field="model") |> limit(n=10)`,
			},
		},
	})
	if err != nil || !saveRes.Pop {
		t.Fatalf("save-recipe failed: %+v, %v", saveRes, err)
	}
}

func TestMCPServerOverStdio(t *testing.T) {
	sessionDir, repoDir := seedFixtures(t)
	s := newStore(t.TempDir())
	_ = s.update(func(st *State, _ time.Time) error {
		st.SessionDirs = []string{sessionDir}
		st.RepoDirs = []string{repoDir}
		return nil
	})

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- (&mcpServer{store: s}).serve(ctx, inR, outW)
	}()

	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	send := func(id int, method string, params any) map[string]any {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if _, err := inW.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
		if !sc.Scan() {
			t.Fatalf("no response for %s: %v", method, sc.Err())
		}
		var reply map[string]any
		if err := json.Unmarshal(sc.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		return reply
	}
	callTool := func(id int, name string, args map[string]any) (map[string]any, bool) {
		t.Helper()
		reply := send(id, "tools/call", map[string]any{"name": name, "arguments": args})
		res, ok := reply["result"].(map[string]any)
		if !ok {
			t.Fatalf("unexpected reply: %+v", reply)
		}
		isErr := res["isError"] == true
		content, _ := res["content"].([]any)
		first, _ := content[0].(map[string]any)
		txt, _ := first["text"].(string)
		if isErr {
			return map[string]any{"error": txt}, true
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(txt), &parsed); err != nil {
			t.Fatalf("unmarshal tool output %q: %v", txt, err)
		}
		return parsed, false
	}

	initRes := send(1, "initialize", map[string]any{})
	if initRes["error"] != nil {
		t.Fatal(initRes)
	}
	listRes := send(2, "tools/list", map[string]any{})
	toolsArr := listRes["result"].(map[string]any)["tools"].([]any)
	if len(toolsArr) != 6 {
		t.Fatalf("expected 6 MCP tools, got %d", len(toolsArr))
	}

	// 1. sac_recipes_list
	rList, isErr := callTool(3, "sac_recipes_list", map[string]any{})
	if isErr {
		t.Fatal(rList)
	}
	if len(rList["recipes"].([]any)) != 3 {
		t.Fatalf("expected 3 builtin recipes, got %+v", rList["recipes"])
	}

	// 2. sac_execute
	execOut, isErr := callTool(4, "sac_execute", map[string]any{
		"query": "quota.go project:persona-hub without:tools",
		"scope": "conversation",
	})
	if isErr {
		t.Fatal(execOut)
	}
	if int(execOut["total_matches"].(float64)) < 2 {
		t.Fatalf("expected at least 2 matches, got %+v", execOut)
	}

	// 3. sac_session_search
	sessOut, isErr := callTool(5, "sac_session_search", map[string]any{
		"query":   "429",
		"scope":   "errors",
		"harness": "claude-code",
	})
	if isErr || int(sessOut["total_matches"].(float64)) != 1 {
		t.Fatalf("sac_session_search errors failed: %+v", sessOut)
	}

	// 4. sac_recipe_upsert & sac_recipe_delete
	upOut, isErr := callTool(6, "sac_recipe_upsert", map[string]any{
		"id":          "codex-only",
		"name":        "Codex 专项检索",
		"description": "只看 Codex 会话",
		"scope":       "conversation",
		"code":        `scan_sessions(query="", scope="conversation", harness="codex") |> limit(n=10)`,
	})
	if isErr || upOut["id"] != "codex-only" {
		t.Fatalf("sac_recipe_upsert failed: %+v", upOut)
	}

	delOut, isErr := callTool(7, "sac_recipe_delete", map[string]any{"id": "codex-only"})
	if isErr || delOut["deleted"] != "codex-only" {
		t.Fatalf("sac_recipe_delete failed: %+v", delOut)
	}

	_ = inW.Close()
	cancel()
	<-done
}
