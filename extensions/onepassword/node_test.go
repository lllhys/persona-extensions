package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	op "github.com/1password/onepassword-sdk-go"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// op on a computer (#376): the program there (node.go) answers op's output without values, and the hub program
// (nodeapi.go) reaches it through nodes.call. Every op answer below is made up, in the shape op 2 writes.

var (
	fixVault = "v" + strings.Repeat("a", 25)
	fixItem  = "i" + strings.Repeat("b", 25)
	// cliValues are the made-up item's values: none may leave the computer in a read.
	cliValues = append(append([]string{}, secretValues...), "481516", "me@example.com", "recovery-kit.pdf")
)

func cliItemJSON() string {
	return fmt.Sprintf(`{
  "id": %[2]q, "title": "GitHub token", "version": 4, "vault": {"id": %[1]q, "name": "Dev"},
  "category": "API_CREDENTIAL", "last_edited_by": "ABCDEF", "created_at": "2026-01-02T03:04:05Z",
  "updated_at": "2026-02-03T04:05:06.123456Z", "additional_information": "me@example.com",
  "tags": ["ci", "tag-leaks-pw-0042"],
  "urls": [{"label": "website", "primary": true, "href": "https://me:synthetic-password-Zq81@example.com/login?token=example-api-key-7f3a9c"}],
  "sections": [{"id": "add more"}, {"id": "sec2", "label": "Backup"}],
  "fields": [
    {"id": "username", "type": "STRING", "purpose": "USERNAME", "label": "username", "value": "me@example.com", "reference": "op://Dev/GitHub token/username"},
    {"id": "credential", "type": "CONCEALED", "label": "credential", "value": "example-api-key-7f3a9c", "reference": "op://Dev/GitHub token/credential"},
    {"id": "password", "type": "CONCEALED", "purpose": "PASSWORD", "label": "password", "value": "synthetic-password-Zq81", "password_details": {"strength": "FANTASTIC"}},
    {"id": "notesPlain", "type": "STRING", "purpose": "NOTES", "label": "notesPlain", "value": "private note: rotate monthly"},
    {"id": "otp1", "section": {"id": "sec2", "label": "Backup"}, "type": "OTP", "label": "one-time password", "value": "otpauth://totp/x?secret=JBSWY3DPEHPK3PXP", "totp": "481516"},
    {"id": "leak", "section": {"id": "sec2", "label": "Backup"}, "type": "STRING", "label": "code backup-code-55813", "value": "backup-code-55813"},
    {"id": "tagv", "type": "CONCEALED", "label": "tag value", "value": "tag-leaks-pw-0042"}
  ],
  "files": [{"id": "f1", "name": "recovery-kit.pdf", "size": 100, "content_path": "/v1/vaults/x/items/y/files/f1/content"}]
}`, fixVault, fixItem)
}

// fakeCLI answers op's command lines with made-up output, or an error as op writes it.
type fakeCLI struct {
	mu      sync.Mutex
	calls   []string
	answers map[string]string
	fail    map[string]error
}

func newFakeCLI() *fakeCLI {
	return &fakeCLI{fail: map[string]error{}, answers: map[string]string{
		"--version":                  "2.30.3\n",
		"account list --format json": `[{"url": "my.1password.com", "email": "me@example.com", "user_uuid": "U1", "account_uuid": "A1"}]`,
		"vault list --format json":   fmt.Sprintf(`[{"id": %q, "name": "Dev", "content_version": 3, "updated_at": "2026-01-02T03:04:05Z", "items": 1}, {"id": "w%s", "name": "Shared"}]`, fixVault, strings.Repeat("c", 25)),
		"item list --vault " + fixVault + " --format json":                      fmt.Sprintf(`[{"id": %q, "title": "GitHub token", "category": "API_CREDENTIAL", "vault": {"id": %q, "name": "Dev"}, "tags": ["ci"], "additional_information": "me@example.com", "urls": [{"href": "https://me:synthetic-password-Zq81@example.com/login?x=1"}], "updated_at": "2026-02-03T04:05:06Z"}]`, fixItem, fixVault),
		"item get " + fixItem + " --vault " + fixVault + " --format json":       cliItemJSON(),
		"item list --vault w" + strings.Repeat("c", 25) + " --format json":      `[]`,
		"read --no-newline -- op://Dev/GitHub token/credential":                 secretValues[1],
		"read --no-newline -- op://" + fixVault + "/" + fixItem + "/credential": secretValues[1],
	}}
}

func (f *fakeCLI) run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.Join(args, " ")
	f.calls = append(f.calls, key)
	if err, ok := f.fail[key]; ok {
		return nil, err
	}
	if out, ok := f.answers[key]; ok {
		return []byte(out), nil
	}
	return nil, opError(`[ERROR] 2026/01/02 03:04:05 "` + key + `" isn't an item. Specify the item with its UUID, name, or domain.`)
}

func (f *fakeCLI) ran() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// onComputer is the hub program with computer m1 picked, its nodes.call answered in this process by the program on
// that computer. left gets every answer that left the computer, as the hub would get it.
func onComputer(t *testing.T, cli *fakeCLI) (a *app, left func() []string) {
	t.Helper()
	n := &nodeApp{run: cli.run}
	a = newApp(func(context.Context, string) (opAPI, error) {
		t.Error("signed in with a token while a computer is picked")
		return nil, errors.New("no")
	})
	a.token = testToken // set too: the computer wins
	a.machine = "m1"
	var mu sync.Mutex
	var answers []string
	a.nodes = func(ctx context.Context, machine, method string, params, out any) error {
		if machine != "m1" {
			t.Errorf("nodes.call to %q", machine)
		}
		name, ok := strings.CutPrefix(method, "op.")
		if !ok {
			t.Fatalf("method %q", method)
		}
		raw, _ := json.Marshal(params)
		res, err := n.handle(ctx, name, raw)
		if err != nil {
			return err
		}
		b, _ := json.Marshal(res)
		if name != "read" {
			mu.Lock()
			answers = append(answers, string(b))
			mu.Unlock()
		}
		return json.Unmarshal(b, out)
	}
	return a, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), answers...)
	}
}

func assertNoCLIValues(t *testing.T, where, text string) {
	t.Helper()
	for _, v := range cliValues {
		if strings.Contains(text, v) {
			t.Fatalf("%s carries a value %q:\n%s", where, v, text)
		}
	}
}

func TestTheComputerDropsValuesBeforeAnswering(t *testing.T) {
	cli := newFakeCLI()
	n := &nodeApp{run: cli.run}
	res, err := n.handle(context.Background(), "item", json.RawMessage(fmt.Sprintf(`{"vault":%q,"item":%q}`, fixVault, fixItem)))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	assertNoCLIValues(t, "op.item's answer", string(b))

	var it op.Item
	if err := json.Unmarshal(b, &it); err != nil {
		t.Fatal(err)
	}
	d := itemFromSDK(it)
	if d.Title != "GitHub token" || d.Category != string(op.ItemCategoryAPICredentials) || d.VaultID != fixVault || !d.HasNotes || d.Files != 1 {
		t.Fatalf("item: %+v", d.itemInfo)
	}
	if strings.Join(d.Tags, ",") != "ci,"+hidden || strings.Join(d.Websites, ",") != "example.com/login" {
		t.Fatalf("tags %v websites %v", d.Tags, d.Websites)
	}
	want := map[string]string{ // title → reference
		"username":          "op://" + fixVault + "/" + fixItem + "/username",
		"credential":        "op://" + fixVault + "/" + fixItem + "/credential",
		"password":          "op://" + fixVault + "/" + fixItem + "/password",
		"one-time password": "op://" + fixVault + "/" + fixItem + "/Backup/one-time password",
		"code " + hidden:    "op://" + fixVault + "/" + fixItem + "/Backup/leak", // a name with a value in it: by ID
		"tag value":         "op://" + fixVault + "/" + fixItem + "/tag value",
	}
	if len(d.Fields) != len(want) {
		t.Fatalf("fields: %+v", d.Fields)
	}
	for _, f := range d.Fields {
		if want[f.Title] != f.Reference {
			t.Errorf("field %q: reference %q, want %q", f.Title, f.Reference, want[f.Title])
		}
	}
	if d.Fields[3].Type != string(op.ItemFieldTypeTOTP) || d.Fields[1].Type != string(op.ItemFieldTypeConcealed) {
		t.Fatalf("types: %+v", d.Fields)
	}

	// Overviews: a saved address keeps only scheme, host and path; op's extra line (a user name) stays behind.
	res, err = n.handle(context.Background(), "items", json.RawMessage(fmt.Sprintf(`{"vault":%q}`, fixVault)))
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(res)
	assertNoCLIValues(t, "op.items' answer", string(b))
	if !strings.Contains(string(b), `"example.com/login"`) {
		t.Fatalf("items: %s", b)
	}

	// Vaults: a count op does not give is unknown, not zero.
	res, err = n.handle(context.Background(), "vaults", nil)
	if err != nil {
		t.Fatal(err)
	}
	vs := res.([]op.VaultOverview)
	if len(vs) != 2 || vaultFromSDK(vs[0]).Items != 1 || vaultFromSDK(vs[1]).Items != -1 {
		t.Fatalf("vaults: %+v", vs)
	}

	// Status asks the app nothing and keeps the accounts' addresses on the computer.
	res, _ = n.handle(context.Background(), "status", nil)
	b, _ = json.Marshal(res)
	if st := res.(nodeStatus); !st.Installed || st.Version != "2.30.3" {
		t.Fatalf("status: %s", b)
	}
	if ran := cli.ran(); ran[len(ran)-1] != "--version" {
		t.Fatalf("status ran %q", ran)
	}
}

func TestTheComputerTakesOnlyIDsAndReferences(t *testing.T) {
	cli := newFakeCLI()
	n := &nodeApp{run: cli.run}
	for _, c := range []struct{ method, params string }{
		{"items", `{"vault":"--format=table"}`},
		{"items", `{"vault":"Dev"}`},
		{"item", fmt.Sprintf(`{"vault":%q,"item":"--reveal"}`, fixVault)},
		{"read", `{"reference":"--help"}`},
		{"read", `{"reference":"op://Dev"}`},
		{"items", `not json`},
	} {
		if _, err := n.handle(context.Background(), c.method, json.RawMessage(c.params)); codeOf(err) != "invalid" {
			t.Errorf("%s %s: %v", c.method, c.params, err)
		}
	}
	if ran := cli.ran(); len(ran) != 0 {
		t.Fatalf("op ran for refused parameters: %q", ran)
	}
	if _, err := n.handle(context.Background(), "write", nil); codeOf(err) != "unknown_method" {
		t.Fatalf("unknown method: %v", err)
	}
	res, err := n.handle(context.Background(), "read", json.RawMessage(`{"reference":"op://Dev/GitHub token/credential"}`))
	if err != nil || res.(map[string]string)["value"] != secretValues[1] {
		t.Fatalf("read: %v", err)
	}
	if ran := cli.ran(); ran[len(ran)-1] != "read --no-newline -- op://Dev/GitHub token/credential" {
		t.Fatalf("read ran %q", ran)
	}
}

func TestOpErrorsCarryCodes(t *testing.T) {
	for _, c := range []struct{ stderr, code, says string }{
		{"[ERROR] 2026/01/02 03:04:05 account is not signed in", codeNotSignedIn, "account is not signed in"},
		{"[ERROR] 2026/01/02 03:04:05 You are not currently signed in. Please run `op signin --help` for instructions", codeNotSignedIn, ""},
		{"[ERROR] 2026/01/02 03:04:05 authorization prompt dismissed, please try again", codeLocked, ""},
		{"[ERROR] 2026/01/02 03:04:05 connecting to desktop app: 1Password is locked", codeLocked, ""},
		{"[ERROR] 2026/01/02 03:04:05 \"x\" isn't an item. Specify the item with its UUID, name, or domain.", codeNotFound, ""},
		{"[ERROR] 2026/01/02 03:04:05 Too many requests", codeRateLimited, ""},
		// What op quotes (item and vault names, references) never decides the kind.
		{`[ERROR] 2026/01/02 03:04:05 could not read secret 'op://Work/Google Authentication/password': "Google Authentication" isn't an item in the "Locked box" vault`, codeNotFound, ""},
		{`[ERROR] 2026/01/02 03:04:05 "Rate limit key" isn't an item. Specify the item with its UUID, name, or domain.`, codeNotFound, ""},
		{`[ERROR] 2026/01/02 03:04:05 item 'Apple sign in' does not have a field 'otp'`, codeNotFound, ""},
		{`[ERROR] 2026/01/02 03:04:05 "Not signed in yet" failed for another reason`, codeFailed, ""},
		{"warning: something\n[ERROR] 2026/01/02 03:04:05 something else broke", codeFailed, "something else broke"},
		{"", codeFailed, "op failed without saying why"},
	} {
		err := opError(c.stderr)
		if codeOf(err) != c.code || c.says != "" && err.Error() != c.says {
			t.Errorf("%q: %s %q", c.stderr, codeOf(err), err)
		}
	}
}

func TestOpRunsInTheAppsSignIn(t *testing.T) {
	base := []string{"PATH=/bin", "OP_SERVICE_ACCOUNT_TOKEN=" + testToken, "OP_CONNECT_TOKEN=x", "OP_SESSION_my=abc", "OP_FORMAT=human", "LANG=en_US.UTF-8"}
	all := func(string) bool { return true }
	joined := strings.Join(opEnv(base, "darwin", 501, all), "\n")
	if strings.Contains(joined, "OP_") || !strings.Contains(joined, "LANG=en_US.UTF-8") || !strings.Contains(joined, "HOME=") || strings.Contains(joined, "XDG_") {
		t.Fatalf("env: %q", joined)
	}
	// Linux: the user's session, which the node leaves out, when it is there.
	joined = strings.Join(opEnv(base, "linux", 1000, all), "\n")
	if !strings.Contains(joined, "XDG_RUNTIME_DIR=/run/user/1000") || !strings.Contains(joined, "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus") {
		t.Fatalf("linux env: %q", joined)
	}
	if joined = strings.Join(opEnv(append(base, "XDG_RUNTIME_DIR=/x"), "linux", 1000, func(string) bool { return false }), "\n"); strings.Count(joined, "XDG_RUNTIME_DIR") != 1 || strings.Contains(joined, "DBUS") {
		t.Fatalf("linux env kept: %q", joined)
	}

	dir := t.TempDir()
	script := `#!/bin/sh
if [ -n "$OP_SERVICE_ACCOUNT_TOKEN" ]; then echo "[ERROR] 2026/01/02 03:04:05 used the token" >&2; exit 3; fi
case "$1" in
  --version) echo 2.30.3 ;;
  vault) echo "[ERROR] 2026/01/02 03:04:05 authorization prompt dismissed, please try again" >&2; exit 1 ;;
  slow) exec /bin/sleep 5 ;;
  *) echo "$@" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "op"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("OP_SERVICE_ACCOUNT_TOKEN", testToken)
	ctx := context.Background()
	if out, err := runOp(ctx, "--version"); err != nil || strings.TrimSpace(string(out)) != "2.30.3" {
		t.Fatalf("version: %q %v", out, err)
	}
	if _, err := runOp(ctx, "vault", "list"); codeOf(err) != codeLocked || err.Error() != "authorization prompt dismissed, please try again" {
		t.Fatalf("locked: %v", err)
	}
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, err := runOp(short, "slow"); codeOf(err) != codeLocked {
		t.Fatalf("no answer: %v", err)
	}

	t.Setenv("PATH", t.TempDir())
	old := opPaths
	opPaths = []string{filepath.Join(dir, "none")}
	defer func() { opPaths = old }()
	if _, err := runOp(ctx, "--version"); codeOf(err) != codeMissing {
		t.Fatalf("missing: %v", err)
	}
	st := (&nodeApp{run: runOp}).status(ctx)
	if st.Installed || st.Code != codeMissing {
		t.Fatalf("status without op: %+v", st)
	}
}

func TestTheHubReadsThroughTheComputer(t *testing.T) {
	cli := newFakeCLI()
	a, left := onComputer(t, cli)
	ctx := context.Background()
	for _, c := range []struct{ name, args, want string }{
		{"onepassword_vaults", `{}`, "Dev (ID " + fixVault + "): 1 items"},
		{"onepassword_search", `{"query":"github"}`, "GitHub token (ID " + fixItem + ")"},
		{"onepassword_item", `{"vault":"Dev","item":"GitHub token"}`, "op://" + fixVault + "/" + fixItem + "/credential"},
	} {
		text, err := a.callTool(ctx, c.name, json.RawMessage(c.args))
		if err != nil || !strings.Contains(text, c.want) {
			t.Fatalf("%s: %v\n%s", c.name, err, text)
		}
		assertNoCLIValues(t, c.name, text)
	}
	if strings.Contains(mustTool(t, a, "onepassword_vaults"), "Shared (ID w"+strings.Repeat("c", 25)+"): ") {
		t.Fatal("a vault op gave no count for is listed with one")
	}

	out, err := secretsCall(t, a, "describe", map[string]string{"reference": "op://Dev/GitHub token/credential"})
	if err != nil || out.(map[string]string)["title"] != "GitHub token › credential" || out.(map[string]string)["detail"] != "Dev" {
		t.Fatalf("describe: %v %v", out, err)
	}
	out, err = secretsCall(t, a, "resolve", map[string]any{"reference": "op://Dev/GitHub token/credential",
		"purpose": map[string]string{"kind": "launcher", "id": "gateway"}})
	if err != nil || out.(map[string]string)["value"] != secretValues[1] {
		t.Fatalf("resolve: %v", err)
	}
	for _, s := range left() {
		assertNoCLIValues(t, "an answer from the computer", s)
	}
	if st := a.status(); st["state"] != "ok" || st["via"] != "op on a computer" {
		t.Fatalf("status: %v", st)
	}
}

func mustTool(t *testing.T, a *app, name string) string {
	t.Helper()
	text, err := a.callTool(context.Background(), name, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestTheComputersTroublesAreSaidOnce(t *testing.T) {
	ctx := context.Background()
	locked := opError("[ERROR] 2026/01/02 03:04:05 authorization prompt dismissed, please try again")
	t.Run("locked", func(t *testing.T) {
		cli := newFakeCLI()
		cli.fail["vault list --format json"] = locked
		cli.fail["read --no-newline -- op://Dev/GitHub token/credential"] = locked
		a, _ := onComputer(t, cli)
		text := viewText(a.view(ctx, &ext.UIRequest{UIParams: wire.UIParams{Locale: "en"}}))
		if !strings.Contains(text, "1Password on that computer is locked") || !strings.Contains(text, "doesn't retry") {
			t.Fatalf("page: %s", text)
		}
		_, err := secretsCall(t, a, "resolve", map[string]any{"reference": "op://Dev/GitHub token/credential"})
		if codeOf(err) != "not_signed_in" || !strings.Contains(err.Error(), "1Password on that computer is locked") {
			t.Fatalf("resolve: %v", err)
		}
		if st := a.status(); st["state"] != "ok" { // a dismissed prompt is routine: the tools stay offered
			t.Fatalf("status: %v", st)
		}
		vaults := 0
		for _, c := range cli.ran() {
			if c == "vault list --format json" {
				vaults++
			}
		}
		if vaults != 1 {
			t.Fatalf("op asked the app %d times for one page", vaults)
		}
	})
	t.Run("missing", func(t *testing.T) {
		cli := newFakeCLI()
		cli.fail["--version"] = &ext.Error{Code: codeMissing, Message: "op (the 1Password CLI) is not installed on this computer"}
		a, _ := onComputer(t, cli)
		text := viewText(a.view(ctx, &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans"}}))
		if !strings.Contains(text, textsFor("zh-Hans").opMissing) {
			t.Fatalf("page: %s", text)
		}
		for _, c := range cli.ran() {
			if strings.HasPrefix(c, "vault") {
				t.Fatal("the page asked the app for vaults without op")
			}
		}
	})
	t.Run("offline", func(t *testing.T) {
		a, _ := onComputer(t, newFakeCLI())
		a.nodes = func(context.Context, string, string, any, any) error {
			return &ext.Error{Code: "offline", Message: "machine m1 is offline"}
		}
		_, err := secretsCall(t, a, "resolve", map[string]any{"reference": "op://Dev/GitHub token/credential"})
		if err == nil || codeOf(err) != "" || !strings.Contains(err.Error(), "That computer is offline") {
			t.Fatalf("resolve: %v", err)
		}
		text := viewText(a.view(ctx, &ext.UIRequest{UIParams: wire.UIParams{Locale: "en"}}))
		if !strings.Contains(text, "That computer is offline") {
			t.Fatalf("page: %s", text)
		}
	})
	t.Run("not found", func(t *testing.T) {
		a, _ := onComputer(t, newFakeCLI())
		_, err := secretsCall(t, a, "resolve", map[string]any{"reference": "op://Dev/Nothing here/credential"})
		if codeOf(err) != "not_found" {
			t.Fatalf("resolve: %v", err)
		}
	})
}

func TestTheComputerIsPickedInTheSettings(t *testing.T) {
	a := newApp(fakeConnect(&fakeOP{}))
	in := &ext.Init{InitializeParams: wire.InitializeParams{Config: json.RawMessage(`{"machines":["m1"]}`)}}
	if _, err := a.init(context.Background(), in, nil); err != nil || a.machine != "m1" {
		t.Fatalf("init: %q %v", a.machine, err)
	}
	if st := a.status(); st["state"] == "not_configured" {
		t.Fatalf("a computer without a token is set up: %v", st)
	}
	text := viewText(newApp(fakeConnect(&fakeOP{})).view(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{Locale: "en"}}))
	if !strings.Contains(text, "Or use op on one of your computers") {
		t.Fatalf("set-up page: %s", text)
	}
	var m struct {
		Node struct {
			Args     []string `json:"args"`
			Machines string   `json:"machines"`
		} `json:"node"`
		Configuration struct {
			Properties map[string]struct {
				MaxItems int `json:"maxItems"`
				Items    struct {
					Format string   `json:"format"`
					OS     []string `json:"x-os"`
				} `json:"items"`
			} `json:"properties"`
		} `json:"configuration"`
		Secrets []struct {
			Required bool `json:"required"`
		} `json:"secrets"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	p := m.Configuration.Properties[m.Node.Machines]
	if strings.Join(m.Node.Args, " ") != "node" || p.MaxItems != 1 || p.Items.Format != "machine" || len(p.Items.OS) != 2 || m.Secrets[0].Required {
		t.Fatalf("manifest: %+v", m)
	}
}

func TestTheComputersCodeDecidesNotItsWords(t *testing.T) {
	// The computer said not_found; its words quote an item named like an error of another kind.
	cli := newFakeCLI()
	cli.fail["read --no-newline -- op://Dev/Google Authentication/password"] = opError(
		`[ERROR] 2026/01/02 03:04:05 "Google Authentication" isn't an item in the "Dev" vault`)
	cli.fail["read --no-newline -- op://Dev/Unauthorized revoked/password"] = &ext.Error{Code: codeFailed, Message: "unauthorized: revoked"}
	a, _ := onComputer(t, cli)
	_, err := secretsCall(t, a, "resolve", map[string]any{"reference": "op://Dev/Google Authentication/password"})
	if codeOf(err) != "not_found" {
		t.Fatalf("resolve: %s %v", codeOf(err), err)
	}
	if st := a.status(); st["state"] != "ok" {
		t.Fatalf("a missing item withdrew the tools: %v", st)
	}
	// A failure in words that sound like a refused token is not the token's words on the computer.
	_, err = secretsCall(t, a, "resolve", map[string]any{"reference": "op://Dev/Unauthorized revoked/password"})
	if codeOf(err) != "" || strings.Contains(err.Error(), textsFor("en").refusedGuide) {
		t.Fatalf("resolve: %s %v", codeOf(err), err)
	}
	if st := a.status(); st["state"] != "ok" {
		t.Fatalf("status: %v", st)
	}
	// Missing op does withdraw them, and says why.
	a.record(&ext.Error{Code: codeMissing, Message: "op (the 1Password CLI) is not installed on this computer"})
	if st := a.status(); st["state"] != "not_signed_in" || !strings.Contains(fmt.Sprint(st["guide"]), "Install the 1Password CLI") {
		t.Fatalf("status: %v", st)
	}
}

func TestOpGetsOnlyTheCallersTime(t *testing.T) {
	var left time.Duration
	n := &nodeApp{run: func(ctx context.Context, _ ...string) ([]byte, error) {
		d, _ := ctx.Deadline()
		left = time.Until(d)
		return []byte(secretValues[1]), nil
	}}
	if _, err := n.handle(context.Background(), "read", json.RawMessage(`{"reference":"op://Dev/GitHub token/credential","within_ms":2000}`)); err != nil {
		t.Fatal(err)
	}
	if left <= 0 || left > 1600*time.Millisecond {
		t.Fatalf("op got %v of the caller's 2 s", left)
	}
	if _, err := n.handle(context.Background(), "read", json.RawMessage(`{"reference":"op://Dev/GitHub token/credential","within_ms":300}`)); codeOf(err) != codeLocked {
		t.Fatalf("no time left: %v", err)
	}
	// The hub program sends how long it waits.
	var params map[string]any
	api := nodeAPI{machine: "m1", call: func(_ context.Context, _, _ string, p, _ any) error {
		params = p.(map[string]any)
		return nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
	defer cancel()
	_, _ = api.resolve(ctx, "op://Dev/GitHub token/credential")
	if ms, _ := params["within_ms"].(int64); ms < 8000 || ms > 9000 {
		t.Fatalf("within_ms: %v", params["within_ms"])
	}
}

func TestValuesOpCannotHandOnWhole(t *testing.T) {
	cli := newFakeCLI()
	cli.answers["read --no-newline -- op://Dev/GitHub token/credential"] = "\xff\xfe binary"
	a, _ := onComputer(t, cli)
	_, err := secretsCall(t, a, "resolve", map[string]any{"reference": "op://Dev/GitHub token/credential"})
	if codeOf(err) != "refused" {
		t.Fatalf("binary value: %s %v", codeOf(err), err)
	}
	var b limitedBuffer
	_, _ = b.Write(make([]byte, mostOpOutput))
	if b.over {
		t.Fatal("over at the limit")
	}
	_, _ = b.Write([]byte{1})
	if !b.over || b.Len() != mostOpOutput {
		t.Fatalf("past the limit: over %v, %d bytes", b.over, b.Len())
	}
}

func TestAFieldOpWritesAsAnObjectIsScrubbedToo(t *testing.T) {
	it := cliItem{ID: fixItem, Title: "Home 12 Example Street"}
	it.Vault.ID = fixVault
	it.Fields = append(it.Fields, struct {
		ID      string          `json:"id"`
		Type    string          `json:"type"`
		Purpose string          `json:"purpose"`
		Label   string          `json:"label"`
		Value   json.RawMessage `json:"value"`
		TOTP    string          `json:"totp"`
		Section *struct {
			ID string `json:"id"`
		} `json:"section"`
	}{ID: "address", Type: "ADDRESS", Label: "address", Value: json.RawMessage(`{"street": "12 Example Street", "zip": 12345, "country": "xx"}`)})
	full, extra := itemFromCLI(it)
	out := scrubItem(full, extra...)
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "12 Example Street") || strings.Contains(string(b), "12345") || out.Title != "Home "+hidden {
		t.Fatalf("item: %s", b)
	}
}
