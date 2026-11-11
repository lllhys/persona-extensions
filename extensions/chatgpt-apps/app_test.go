package main

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

type harness struct {
	t    *testing.T
	f    *fakeChatGPT
	app  *App
	host *exttest.Host
	data string
	logs *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func start(t *testing.T, f *fakeChatGPT, locale string) *harness {
	t.Helper()
	logs := &syncBuffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	app := newApp(f.endpoints())
	app.loadTimeout = 2 * time.Second
	data := t.TempDir()
	host := exttest.StartWith(t, manifest, exttest.Serve(t, app.program()), func(p *wire.InitializeParams) {
		p.Extension = &wire.ExtensionInfo{ID: "chatgpt-apps", Version: manifestVersion(), DataDir: data}
		p.Hub.Locale = locale
	})
	return &harness{t: t, f: f, app: app, host: host, data: data, logs: logs}
}

func (h *harness) call(method string, params, out any) error {
	h.t.Helper()
	return h.host.Call(method, params, out)
}

func (h *harness) tool(name string, args any) (string, error) {
	h.t.Helper()
	raw, _ := json.Marshal(args)
	var res wire.ConnectorCallResult
	err := h.call("connector.tools.call", wire.ConnectorCallParams{Contribution: connectorID, Name: name, Args: raw}, &res)
	return res.Text, err
}

func (h *harness) describe(name string, args any) (wire.ConnectorDescribeResult, error) {
	h.t.Helper()
	raw, _ := json.Marshal(args)
	var res wire.ConnectorDescribeResult
	err := h.call("connector.actions.describe", wire.ConnectorCallParams{Contribution: connectorID, Name: name, Args: raw}, &res)
	return res, err
}

func (h *harness) do(name, requestID string, args any) (string, error) {
	h.t.Helper()
	raw, _ := json.Marshal(args)
	var res wire.ConnectorCallResult
	err := h.call("connector.actions.do", wire.ConnectorDoParams{Contribution: connectorID, Name: name, Args: raw, RequestID: requestID}, &res)
	return res.Text, err
}

// signIn goes through the device code as the phone does: start, the user enters the code, ask again until done.
func (h *harness) signIn() {
	h.t.Helper()
	var p wire.AuthPrompt
	if err := h.call("auth.step", wire.AuthStepParams{Step: ""}, &p); err != nil {
		h.t.Fatalf("auth.step: %v", err)
	}
	if p.Kind != "device_code" || p.UserCode != "ABCD-EFGH" || p.URL != h.f.srv.URL+"/codex/device" || p.Interval != 1 || p.Step == "" {
		h.t.Fatalf("first step: %+v", p)
	}
	h.f.approve()
	deadline := time.Now().Add(10 * time.Second)
	for !p.Done {
		if time.Now().After(deadline) {
			h.t.Fatalf("never signed in: %+v", p)
		}
		time.Sleep(200 * time.Millisecond)
		if err := h.call("auth.step", wire.AuthStepParams{Step: p.Step}, &p); err != nil {
			h.t.Fatalf("auth.step again: %v", err)
		}
		if p.Error != "" {
			h.t.Fatalf("sign-in failed: %s", p.Error)
		}
	}
	if p.Account != fakeEmail+" (plus)" {
		h.t.Errorf("account: %q", p.Account)
	}
}

func (h *harness) storedTokens() *tokens {
	h.t.Helper()
	t, err := decodeSecret(h.host.Secret(connectorID))
	if err != nil {
		h.t.Fatalf("stored secret: %v", err)
	}
	return t
}

// noSecretsInLogs: the log has no token, no mail address and no tool result.
func (h *harness) noSecretsInLogs() {
	h.t.Helper()
	logs := h.logs.String()
	h.f.mu.Lock()
	secrets := []string{h.f.access, h.f.refresh, "refresh-1", fakeEmail, "Example subject", "msg_example", "verifier_example", "code_example"}
	h.f.mu.Unlock()
	for _, s := range secrets {
		if s != "" && strings.Contains(logs, s) {
			h.t.Errorf("the log holds %q:\n%s", s, logs)
		}
	}
}

func TestProtocol(t *testing.T) {
	f := newFakeChatGPT(t)
	app := newApp(f.endpoints())
	app.loadTimeout = time.Second
	exttest.CheckConnectors(t, manifest, exttest.Serve(t, app.program()))
}

func TestSignInWithADeviceCode(t *testing.T) {
	h := start(t, newFakeChatGPT(t), "")
	var st wire.AuthStatus
	h.call("auth.status", struct{}{}, &st)
	if st.State != wire.AuthStateSignedOut {
		t.Fatalf("before: %+v", st)
	}
	var cs wire.ConnectorStatus
	h.call("connector.status", wire.ConnectorParams{Contribution: connectorID}, &cs)
	if cs.State != wire.ConnectorStateNotSignedIn || len(cs.Guide) == 0 {
		t.Fatalf("connector before: %+v", cs)
	}
	if _, err := h.tool("chatgpt_apps", map[string]any{}); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("a tool before signing in: %v", err)
	}

	h.signIn()
	h.call("auth.status", struct{}{}, &st)
	if st.State != wire.AuthStateSignedIn || st.Account != fakeEmail+" (plus)" {
		t.Fatalf("after: %+v", st)
	}
	h.call("connector.status", wire.ConnectorParams{Contribution: connectorID}, &cs)
	if cs.State != wire.ConnectorStateOk {
		t.Fatalf("connector after: %+v", cs)
	}
	stored := h.storedTokens()
	if stored == nil || stored.RefreshToken != "refresh-1" || stored.accountID() != fakeAccount {
		t.Fatalf("stored: %+v", stored)
	}
	changed := false
	for _, e := range h.host.Events() {
		changed = changed || e.Name == "auth.changed"
	}
	if !changed {
		t.Error("no auth.changed after signing in")
	}
	// Nothing in the data directory holds the tokens.
	filepath.WalkDir(h.data, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			raw, _ := os.ReadFile(path)
			if bytes.Contains(raw, []byte("refresh-")) {
				t.Errorf("%s holds a token", path)
			}
		}
		return nil
	})

	h.call("auth.logout", struct{}{}, &st)
	if st.State != wire.AuthStateSignedOut || h.host.Secret(connectorID) != nil {
		t.Fatalf("after logout: %+v, secret %q", st, h.host.Secret(connectorID))
	}
	h.noSecretsInLogs()
}

func TestTheSignInCardIsInTheUsersLanguage(t *testing.T) {
	for locale, want := range map[string]string{"": "验证码", "zh-Hans": "验证码", "en": "enter the code", "ja": "enter the code"} {
		h := start(t, newFakeChatGPT(t), locale)
		var p wire.AuthPrompt
		h.call("auth.step", wire.AuthStepParams{}, &p)
		if !strings.Contains(p.Hint, want) {
			t.Errorf("%q: hint %q", locale, p.Hint)
		}
	}
}

func TestReadsRunAndWritesAreRefusedAsReads(t *testing.T) {
	h := start(t, newFakeChatGPT(t), "")
	h.signIn()
	text, err := h.tool("chatgpt_apps", map[string]any{"filter": "mail"})
	if err != nil {
		t.Fatal(err)
	}
	var list []toolSummary
	json.Unmarshal([]byte(text), &list)
	ro := map[string]bool{}
	for _, s := range list {
		ro[s.Name] = s.ReadOnly
	}
	want := map[string]bool{"example_mail.search_messages": true, "example_mail.send_message": false, "example_mail.forward_message": false}
	if len(ro) != len(want) {
		t.Fatalf("filtered list: %+v", list)
	}
	for n, w := range want {
		if ro[n] != w {
			t.Errorf("%s read_only %v, want %v", n, ro[n], w)
		}
	}

	text, err = h.tool("chatgpt_read", map[string]any{"name": "example_mail.search_messages", "arguments": map[string]any{"query": "x"}})
	if err != nil || !strings.Contains(text, "Example subject") {
		t.Fatalf("read: %q %v", text, err)
	}
	for _, name := range []string{"example_mail.send_message", "example_mail.forward_message", "example_broker.list_orders"} {
		if _, err := h.tool("chatgpt_read", map[string]any{"name": name}); err == nil || !strings.Contains(err.Error(), "chatgpt_write") {
			t.Errorf("%s as a read: %v", name, err)
		}
	}
	if calls := h.f.toolCalls(); len(calls) != 1 || calls[0].Name != "example_mail.search_messages" {
		t.Fatalf("calls that reached ChatGPT: %+v", calls)
	}
	h.noSecretsInLogs()
}

func TestAWriteIsCardedThenRunOnce(t *testing.T) {
	h := start(t, newFakeChatGPT(t), "")
	h.signIn()
	args := map[string]any{"name": "example_mail.send_message", "arguments": map[string]any{
		"to": "me@example.com", "subject": "Weekend plan", "body": "Line one\nLine two"}}

	// From memory only: no tool list yet, so no card.
	if _, err := h.describe("chatgpt_write", args); err == nil || !strings.Contains(err.Error(), "chatgpt_apps") {
		t.Fatalf("describe before a list: %v", err)
	}
	if _, err := h.tool("chatgpt_apps", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	card, err := h.describe("chatgpt_write", args)
	if err != nil {
		t.Fatal(err)
	}
	if card.Action.Name != "Example Mail · send_message" || card.Action.Visible != "me@example.com" {
		t.Errorf("card action: %+v", card.Action)
	}
	for _, want := range []string{"Example Mail", "example_mail.send_message", "Send a mail message.",
		"to: me@example.com\nsubject: Weekend plan\nbody:\nLine one\nLine two"} {
		if !strings.Contains(card.Summary, want) {
			t.Errorf("card summary lacks %q:\n%s", want, card.Summary)
		}
	}
	// A read-only tool is not carded as a write; bad arguments are refused before the user is asked.
	if _, err := h.describe("chatgpt_write", map[string]any{"name": "example_mail.search_messages"}); err == nil {
		t.Error("a read-only tool got a write card")
	}
	if _, err := h.describe("chatgpt_write", map[string]any{"name": "example_mail.send_message", "arguments": "text"}); err == nil {
		t.Error("arguments that are not an object got a card")
	}
	if len(h.f.toolCalls()) != 0 {
		t.Fatal("describing ran something")
	}

	first, err := h.do("chatgpt_write", "req-1", args)
	if err != nil || !strings.Contains(first, "msg_example") {
		t.Fatalf("do: %q %v", first, err)
	}
	again, err := h.do("chatgpt_write", "req-1", args)
	if err != nil || again != first {
		t.Fatalf("the same request again: %q %v", again, err)
	}
	if calls := h.f.toolCalls(); len(calls) != 1 || calls[0].Args["subject"] != "Weekend plan" {
		t.Fatalf("writes that reached ChatGPT: %+v", calls)
	}
	// A new request id is a new write.
	if _, err := h.do("chatgpt_write", "req-2", args); err != nil || len(h.f.toolCalls()) != 2 {
		t.Fatalf("second write: %v, calls %d", err, len(h.f.toolCalls()))
	}
	// After a restart the id is still known, its result not kept.
	l := newLedger(h.data)
	if _, err := l.once("req-1", func() (wire.ConnectorCallResult, error) { t.Fatal("ran again"); return wire.ConnectorCallResult{}, nil }); err != errAlreadyRan {
		t.Fatalf("after a restart: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(h.data, "done-requests.json"))
	if bytes.Contains(raw, []byte("Weekend")) || bytes.Contains(raw, []byte("msg_example")) {
		t.Errorf("the ledger file holds a result: %s", raw)
	}
	h.noSecretsInLogs()
}

// A request whose id cannot be recorded does not run: after a restart the id would be unknown and the same approved
// request would run a second time. Once the data directory is writable again it runs once, and a restart still
// knows it.
func TestARequestThatCannotBeRecordedDoesNotRun(t *testing.T) {
	data := t.TempDir()
	if err := os.Mkdir(filepath.Join(data, "done-requests.json"), 0o700); err != nil { // the ledger cannot be written
		t.Fatal(err)
	}
	runs := 0
	run := func() (wire.ConnectorCallResult, error) { runs++; return wire.ConnectorCallResult{Text: "sent"}, nil }
	for restart := 0; restart < 2; restart++ {
		if _, err := newLedger(data).once("req-1", run); err == nil {
			t.Fatalf("restart %d: an unrecorded request ran", restart)
		}
	}
	if runs != 0 {
		t.Fatalf("ran %d times without a record", runs)
	}
	if err := os.Remove(filepath.Join(data, "done-requests.json")); err != nil {
		t.Fatal(err)
	}
	if res, err := newLedger(data).once("req-1", run); err != nil || res.Text != "sent" {
		t.Fatalf("writable again: %+v %v", res, err)
	}
	if _, err := newLedger(data).once("req-1", run); err != errAlreadyRan || runs != 1 {
		t.Fatalf("after a restart: %v, ran %d times", err, runs)
	}
}

func TestTheWriteCardInEnglish(t *testing.T) {
	h := start(t, newFakeChatGPT(t), "en")
	h.signIn()
	h.tool("chatgpt_apps", map[string]any{})
	card, err := h.describe("chatgpt_write", map[string]any{"name": "example_mail.forward_message", "arguments": map[string]any{"id": "m1"}})
	if err != nil {
		t.Fatal(err)
	}
	if card.Action.Visible != "Your account on Example Mail" || !strings.HasPrefix(card.Summary, "Calls example_mail.forward_message on Example Mail") {
		t.Errorf("card: %+v", card)
	}
}

func TestEveryActionIsAlwaysConfirmed(t *testing.T) {
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	c := m.Contributes.Connectors[0]
	if len(c.ConfirmAlways) != 1 || c.ConfirmAlways[0] != "chatgpt_*" {
		t.Fatalf("confirm_always: %v", c.ConfirmAlways)
	}
	for _, tool := range writeTools().Tools {
		if !strings.HasPrefix(tool.Name, "chatgpt_") {
			t.Errorf("%s is not covered by confirm_always", tool.Name)
		}
	}
	if len(c.AgentTools) != 0 {
		t.Errorf("agent_tools %v: only the assistant gets these tools", c.AgentTools)
	}
}

func TestTheAccessTokenIsRefreshedOnceAndSavedBeforeUse(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "")
	h.signIn()
	// The next access token ends within the two minutes: every call refreshes first, one at a time.
	f.mu.Lock()
	f.ttl = time.Minute
	f.mu.Unlock()
	h.app.mu.Lock()
	h.app.tok.AccessToken = jwt(map[string]any{"exp": time.Now().Add(30 * time.Second).Unix()})
	h.app.mu.Unlock()
	f.mu.Lock()
	f.access = h.app.tok.AccessToken
	f.mu.Unlock()

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := h.app.listTools(h.app.ctx, true); err != nil {
				t.Errorf("list: %v", err)
			}
		}()
	}
	wg.Wait()
	f.mu.Lock()
	refreshes := f.refreshes
	f.mu.Unlock()
	if refreshes < 1 {
		t.Fatal("never refreshed")
	}
	stored := h.storedTokens()
	f.mu.Lock()
	current := f.refresh
	f.mu.Unlock()
	if stored.RefreshToken != current {
		t.Fatalf("stored refresh token %q, in force %q", stored.RefreshToken, current)
	}
	h.noSecretsInLogs()
}

func TestARefusedAccessTokenIsRefreshedAndTheCallRetried(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "")
	h.signIn()
	f.mu.Lock()
	f.refuse401 = 1
	f.mu.Unlock()
	if _, err := h.tool("chatgpt_apps", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refreshes != 1 {
		t.Fatalf("refreshes: %d", f.refreshes)
	}
}

func TestARefusedRefreshSignsOut(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "")
	h.signIn()
	f.mu.Lock()
	f.refresh = "someone-else" // the refresh token was used elsewhere
	f.refuse401 = 1
	f.mu.Unlock()
	if _, err := h.tool("chatgpt_apps", map[string]any{}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("call: %v", err)
	}
	var st wire.AuthStatus
	h.call("auth.status", struct{}{}, &st)
	if st.State != wire.AuthStateExpired || st.Message == "" {
		t.Fatalf("auth: %+v", st)
	}
	var cs wire.ConnectorStatus
	h.call("connector.status", wire.ConnectorParams{Contribution: connectorID}, &cs)
	if cs.State != wire.ConnectorStateNotSignedIn {
		t.Fatalf("connector: %+v", cs)
	}
	if h.host.Secret(connectorID) != nil {
		t.Error("the dead sign-in is still stored")
	}
}

func TestAnEventStreamAnswer(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "")
	h.signIn()
	f.mu.Lock()
	f.sse = true
	f.mu.Unlock()
	text, err := h.tool("chatgpt_read", map[string]any{"name": "example_broker.get_positions"})
	if err != nil || text != "no positions" {
		t.Fatalf("%q %v", text, err)
	}
}

func TestPluginsAreLookedAtThenInstalledOnApproval(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "")
	h.signIn()
	text, err := h.tool("chatgpt_plugin_search", map[string]any{"query": "mail"})
	if err != nil || !strings.Contains(text, "example-mail") || strings.Contains(text, "example-notes") {
		t.Fatalf("search: %q %v", text, err)
	}

	f.mu.Lock()
	delete(f.installed, "plugin_example_notes")
	delete(f.connected, "connector_notes")
	f.mu.Unlock()
	// The card is drawn from what chatgpt_plugin_info read: not before.
	h.app.clearCaches()
	if _, err := h.describe("chatgpt_plugin_install", map[string]any{"plugin": "example-notes"}); err == nil {
		t.Fatal("an install card before looking at the plugin")
	}
	text, err = h.tool("chatgpt_plugin_info", map[string]any{"plugin": "example-notes"})
	if err != nil || !strings.Contains(text, "https://chatgpt.com/plugins/plugin_example_notes") || !strings.Contains(text, "needs Connect") {
		t.Fatalf("info: %s %v", text, err)
	}
	card, err := h.describe("chatgpt_plugin_install", map[string]any{"plugin": "example-notes"})
	if err != nil {
		t.Fatal(err)
	}
	if card.Action.Name != "安装插件「Example Notes」" || !strings.Contains(card.Summary, "Example Notes（装好后要在 ChatGPT 里点一次连接）") ||
		!strings.Contains(card.Summary, "开发者：Example Co") || !strings.Contains(card.Summary, "技能：1 个") {
		t.Errorf("install card: %+v", card)
	}
	out, err := h.do("chatgpt_plugin_install", "req-install", map[string]any{"plugin": "example-notes"})
	if err != nil || !strings.Contains(out, "connect_url") || !strings.Contains(out, `"notes"`) {
		t.Fatalf("install: %s %v", out, err)
	}
	f.mu.Lock()
	installed := f.installed["plugin_example_notes"]
	f.mu.Unlock()
	if !installed {
		t.Fatal("not installed")
	}
	text, err = h.tool("chatgpt_plugin_skill", map[string]any{"plugin": "example-notes", "skill": "notes"})
	if err != nil || !strings.Contains(text, "mcp__codex_apps__example_notes__read_note") || !strings.Contains(text, "reference/fields.md") {
		t.Fatalf("skill: %s %v", text, err)
	}
	text, err = h.tool("chatgpt_plugin_skill", map[string]any{"plugin": "example-notes", "skill": "notes", "file": "../../../../etc/passwd"})
	if err == nil {
		t.Fatalf("a file out of the skill: %s", text)
	}
	// The synthetic tool of the unconnected app says to connect, without running.
	if _, err := h.tool("chatgpt_read", map[string]any{"name": "example_notes.read_note"}); err == nil || !strings.Contains(err.Error(), "connect_url") {
		t.Fatalf("an unconnected app's tool: %v", err)
	}

	card, err = h.describe("chatgpt_plugin_uninstall", map[string]any{"plugin": "example-notes"})
	if err != nil || card.Action.Name != "卸载插件「Example Notes」" {
		t.Fatalf("uninstall card: %+v %v", card, err)
	}
	if _, err := h.do("chatgpt_plugin_uninstall", "req-uninstall", map[string]any{"plugin": "example-notes"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.data, "plugins", "example-notes")); !os.IsNotExist(err) {
		t.Errorf("the package stayed: %v", err)
	}
	h.noSecretsInLogs()
}

func TestAPackageWithAPathOutOfItIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := extract(bytes.NewReader(bundle(t, true)), filepath.Join(dir, "p")); err == nil {
		t.Fatal("extracted a path out of the package")
	}
	if _, err := os.Stat(filepath.Join(dir, "outside.txt")); !os.IsNotExist(err) {
		t.Fatal("wrote out of the package")
	}
}

func TestWhatCountsAsReadOnly(t *testing.T) {
	ro := func(name string, hint *bool, destructive *bool) bool {
		t := mcpTool{Name: name}
		if hint != nil || destructive != nil {
			t.Annotations = &struct {
				Title           string `json:"title,omitempty"`
				ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
				DestructiveHint *bool  `json:"destructiveHint,omitempty"`
			}{ReadOnlyHint: hint, DestructiveHint: destructive}
		}
		return t.readOnly()
	}
	cases := []struct {
		name        string
		hint, destr *bool
		want        bool
	}{
		{"mail.search_messages", boolp(true), nil, true},
		{"mail.getThread", boolp(true), nil, true},
		{"broker.get_positions", boolp(true), boolp(false), true},
		{"mail.search_messages", nil, nil, false},          // no annotation: ask
		{"mail.search_messages", boolp(false), nil, false}, // marked as a change
		{"broker.list_orders", boolp(true), boolp(true), false},
		{"mail.sendMessage", boolp(true), nil, false},
		{"mail.forward_message", boolp(true), nil, false},
		{"broker.place-order", boolp(true), nil, false},
		{"docs.delete_file", boolp(true), nil, false},
		{"calendar.list_events", boolp(true), nil, true},
	}
	for _, c := range cases {
		if got := ro(c.name, c.hint, c.destr); got != c.want {
			t.Errorf("%s (hint %v, destructive %v): %v, want %v", c.name, c.hint, c.destr, got, c.want)
		}
	}
}

var _ = ext.ConnectorNamespace
