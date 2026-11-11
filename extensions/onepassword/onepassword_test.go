package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	op "github.com/1password/onepassword-sdk-go"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Synthetic credentials: none is a real token or secret.
const (
	testToken = "ops_c3ludGhldGljLXNlcnZpY2UtYWNjb3VudC10b2tlbi1mb3ItdGVzdHM"
	vaultDev  = "vltdevexample000000000000a"
	vaultHome = "vlthomeexample00000000000b"
	vaultDup  = "vltduplicateexample00000c"
)

// secretValues are every value the fake keeps in items. No tool result, error, status or page may contain any.
var secretValues = []string{
	"synthetic-password-Zq81",
	"example-api-key-7f3a9c",
	"JBSWY3DPEHPK3PXP",
	"private note: rotate monthly",
	"backup-code-55813",
	"tag-leaks-pw-0042",
}

type fakeOP struct {
	mu      sync.Mutex
	calls   []string
	failAll error
}

func ptr(s string) *string { return &s }

func (f *fakeOP) note(call string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	return f.failAll
}

func (f *fakeOP) listVaults(context.Context) ([]op.VaultOverview, error) {
	if err := f.note("vaults"); err != nil {
		return nil, err
	}
	at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	return []op.VaultOverview{
		{ID: vaultDev, Title: "Dev", ActiveItemCount: 3, UpdatedAt: at},
		{ID: vaultHome, Title: "Home", ActiveItemCount: 1, UpdatedAt: at},
		{ID: vaultDup, Title: "home", ActiveItemCount: 0, UpdatedAt: at},
	}, nil
}

func (f *fakeOP) listItems(_ context.Context, vaultID string) ([]op.ItemOverview, error) {
	if err := f.note("items " + vaultID); err != nil {
		return nil, err
	}
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	switch vaultID {
	case vaultDev:
		return []op.ItemOverview{
			{ID: "itmgithub", Title: "GitHub token", Category: op.ItemCategoryAPICredentials, VaultID: vaultDev, Tags: []string{"work/ci", "github"},
				Websites: []op.Website{{URL: "https://user:" + secretValues[0] + "@github.com/login?token=" + secretValues[1] + "#x"}}, UpdatedAt: at},
			{ID: "itmdb", Title: "Staging database", Category: op.ItemCategoryDatabase, VaultID: vaultDev, Tags: []string{"work"}, UpdatedAt: at},
			{ID: "itmdb2", Title: "staging database", Category: op.ItemCategoryDatabase, VaultID: vaultDev, UpdatedAt: at},
		}, nil
	case vaultHome:
		return []op.ItemOverview{
			{ID: "itmrouter", Title: "Router", Category: op.ItemCategoryRouter, VaultID: vaultHome, Tags: []string{"home"},
				Websites: []op.Website{{URL: "router.example.net/admin"}}, UpdatedAt: at},
		}, nil
	}
	return nil, nil
}

func (f *fakeOP) getItem(_ context.Context, vaultID, itemID string) (op.Item, error) {
	if err := f.note("item " + vaultID + "/" + itemID); err != nil {
		return op.Item{}, err
	}
	if vaultID != vaultDev || itemID != "itmgithub" {
		return op.Item{}, errors.New("item not found")
	}
	return op.Item{
		ID: "itmgithub", Title: "GitHub token", Category: op.ItemCategoryAPICredentials, VaultID: vaultDev,
		Tags:     []string{"work/ci", secretValues[5]},
		Websites: []op.Website{{URL: "https://github.com/settings/tokens?x=" + secretValues[1]}},
		Notes:    secretValues[3],
		Sections: []op.ItemSection{{ID: "", Title: ""}, {ID: "sec1", Title: "Backup"}, {ID: "sec2", Title: "Ünïcode"}},
		Fields: []op.ItemField{
			{ID: "username", Title: "username", FieldType: op.ItemFieldTypeText, Value: "me@example.com"},
			{ID: "credential", Title: "credential", FieldType: op.ItemFieldTypeConcealed, Value: secretValues[1]},
			{ID: "pw", Title: "password", FieldType: op.ItemFieldTypeConcealed, Value: secretValues[0], SectionID: ptr("")},
			{ID: "otp1", Title: "one-time password", FieldType: op.ItemFieldTypeTOTP, Value: secretValues[2], SectionID: ptr("sec1"),
				Details: &op.ItemFieldDetails{}},
			{ID: "code1", Title: "code", FieldType: op.ItemFieldTypeConcealed, Value: secretValues[4], SectionID: ptr("sec1")},
			{ID: "code2", Title: "code", FieldType: op.ItemFieldTypeConcealed, Value: "other-code-91", SectionID: ptr("sec1")},
			{ID: "pin", Title: "PIN " + secretValues[5], FieldType: op.ItemFieldTypeConcealed, Value: secretValues[5], SectionID: ptr("sec2")},
		},
		Files:     []op.ItemFile{{}},
		UpdatedAt: time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC),
	}, nil
}

func fakeConnect(f *fakeOP) connectFunc {
	return func(_ context.Context, token string) (opAPI, error) {
		if token != testToken {
			return nil, fmt.Errorf("error initializing client: invalid service account token %s, please create another token", token)
		}
		return f, nil
	}
}

func connected(t *testing.T) (*app, *fakeOP) {
	t.Helper()
	f := &fakeOP{}
	a := newApp(fakeConnect(f))
	a.token = testToken
	return a, f
}

func call(t *testing.T, a *app, name string, args string) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"contribution": connectorID, "name": name, "args": json.RawMessage(args)})
	out, err := a.connector(context.Background(), "tools.call", raw)
	if err != nil {
		return "", err
	}
	return out.(map[string]any)["text"].(string), nil
}

func assertNoSecrets(t *testing.T, where, text string) {
	t.Helper()
	for _, v := range append(secretValues, testToken, testToken[len(testToken)-12:]) {
		if strings.Contains(text, v) {
			t.Fatalf("%s carries a secret value %q:\n%s", where, v, text)
		}
	}
}

func TestConformance(t *testing.T) {
	a, _ := connected(t)
	a.token = "" // exttest sends no secrets: the extension is waiting for its token
	exttest.Check(t, manifest, exttest.Serve(t, program(a)))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "onepassword" || len(m.Contributes.Connectors) != 1 || len(m.Secrets) != 1 || m.Secrets[0].Name != tokenSecret {
		t.Fatalf("manifest: %+v", m)
	}
	c := m.Contributes.Connectors[0]
	if len(c.AgentTools) != 0 {
		t.Fatal("the tools are the assistant's only: no agent_tools")
	}
	for _, cap := range c.Capabilities {
		if cap == "actions" || cap == "inbox" {
			t.Fatalf("a read-only connector declares %q", cap)
		}
	}
	for _, tl := range readTools {
		if !strings.HasPrefix(tl.Name, connectorID+"_") {
			t.Fatalf("tool %s lacks the connector prefix", tl.Name)
		}
	}
}

func TestVaultsTool(t *testing.T) {
	a, _ := connected(t)
	out, err := call(t, a, "onepassword_vaults", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"3 vaults", "Dev (ID " + vaultDev + "): 3 items", "last changed 2026-11-02"} {
		if !strings.Contains(out, want) {
			t.Fatalf("vaults: want %q in\n%s", want, out)
		}
	}
	if st := a.status(); st["state"] != "ok" || st["last_sync"] == nil {
		t.Fatalf("status after a read: %v", st)
	}
}

func TestSearchTool(t *testing.T) {
	a, f := connected(t)
	for _, tc := range []struct {
		args       string
		want, not  []string
		wantErr    string
		noItemList bool
	}{
		{args: `{"query":"github"}`, want: []string{"1 items matched", "GitHub token (ID itmgithub) · api credentials · vault Dev", "Websites: github.com/login"}, not: []string{"Router"}},
		{args: `{"query":"STAGING data"}`, want: []string{"2 items matched", "itmdb", "itmdb2"}},
		{args: `{"tag":"work"}`, want: []string{"2 items matched", "itmgithub", "itmdb"}, not: []string{"itmrouter"}},
		{args: `{"tag":"work/ci"}`, want: []string{"1 items matched", "itmgithub"}},
		{args: `{"tag":"wor"}`, want: []string{"No items matched."}},
		{args: `{"website":"https://www.github.com/x"}`, want: []string{"itmgithub"}},
		{args: `{"website":"example.net"}`, want: []string{"itmrouter"}},
		{args: `{"vault":"Dev"}`, want: []string{"3 items matched"}},
		{args: `{"vault":"` + vaultHome + `"}`, want: []string{"1 items matched", "Router"}},
		{args: `{"vault":"HOME"}`, wantErr: "2 vaults are called"},
		{args: `{"vault":"nope"}`, wantErr: "no vault"},
		{args: `{}`, wantErr: "give at least one"},
		{args: `{"query":"x","extra":1}`, wantErr: "invalid arguments"},
	} {
		out, err := call(t, a, "onepassword_search", tc.args)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("search %s: want error %q, got %v / %s", tc.args, tc.wantErr, err, out)
			}
			assertNoSecrets(t, "search error", err.Error())
			continue
		}
		if err != nil {
			t.Fatalf("search %s: %v", tc.args, err)
		}
		assertNoSecrets(t, "search "+tc.args, out)
		for _, w := range tc.want {
			if !strings.Contains(out, w) {
				t.Fatalf("search %s: want %q in\n%s", tc.args, w, out)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(out, n) {
				t.Fatalf("search %s: %q should not match:\n%s", tc.args, n, out)
			}
		}
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "item ") {
			t.Fatalf("a search read a whole item (values included): %s", c)
		}
	}
}

func TestItemTool(t *testing.T) {
	a, _ := connected(t)
	out, err := call(t, a, "onepassword_item", `{"vault":"dev","item":"GitHub Token"}`)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecrets(t, "item", out)
	for _, want := range []string{
		"GitHub token (ID itmgithub) · api credentials · vault Dev (ID " + vaultDev + ") · changed 2026-11-04",
		"Websites: github.com/settings/tokens\n",
		"Tags: work/ci, [hidden]",
		// unique names outside a section are kept; the field in the unnamed default section counts as outside one
		"- username · text · op://" + vaultDev + "/itmgithub/username",
		"- credential · concealed (password or key) · op://" + vaultDev + "/itmgithub/credential",
		"- password · concealed (password or key) · op://" + vaultDev + "/itmgithub/password",
		// a named section and a unique field in it, by name; a name with spaces is allowed
		"- Backup › one-time password · one-time password · op://" + vaultDev + "/itmgithub/Backup/one-time password",
		// two fields called "code" in one section: by ID
		"- Backup › code · concealed (password or key) · op://" + vaultDev + "/itmgithub/Backup/code1",
		"- Backup › code · concealed (password or key) · op://" + vaultDev + "/itmgithub/Backup/code2",
		// a section name with characters references don't allow, and a field name that held a value: IDs
		"- Ünïcode › PIN [hidden] · concealed (password or key) · op://" + vaultDev + "/itmgithub/sec2/pin",
		"It has notes (not shown).",
		"It has 1 attached files (not shown).",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("item: want %q in\n%s", want, out)
		}
	}
	for _, args := range []string{`{"vault":"Dev","item":"staging database"}`, `{"vault":"Dev","item":"nothing"}`, `{"vault":"Dev"}`} {
		if out, err := call(t, a, "onepassword_item", args); err == nil {
			t.Fatalf("item %s: want an error, got %s", args, out)
		}
	}
	if out, err := call(t, a, "onepassword_item", `{"vault":"Dev","item":"staging database"}`); err == nil || !strings.Contains(err.Error(), "itmdb, itmdb2") {
		t.Fatalf("an ambiguous title lists the IDs: %v %s", err, out)
	}
}

func TestReferencesAreValidSyntax(t *testing.T) {
	a, _ := connected(t)
	d, err := a.getItem(context.Background(), vaultDev, "itmgithub")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range d.Fields {
		rest, ok := strings.CutPrefix(f.Reference, "op://")
		parts := strings.Split(rest, "/")
		if !ok || len(parts) < 3 || len(parts) > 4 {
			t.Fatalf("reference %q: want op://vault/item/[section/]field", f.Reference)
		}
		for _, p := range parts {
			if !refSafe(p) {
				t.Fatalf("reference %q: segment %q has characters references don't allow", f.Reference, p)
			}
		}
	}
}

func TestCleanURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://user:pw@github.com/login?token=abc#frag": "github.com/login",
		"github.com":                        "github.com",
		"router.example.net/admin/":         "router.example.net/admin",
		"http://192.0.2.1:8080/setup?key=1": "http://192.0.2.1:8080/setup",
		"ssh://git@example.org/repo":        "ssh://example.org/repo",
		"   ":                               "",
		"https://":                          "",
	} {
		if got := cleanURL(in); got != want {
			t.Errorf("cleanURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestErrorsAreRedacted(t *testing.T) {
	a, f := connected(t)
	f.failAll = fmt.Errorf("upstream said: bad request for %s (%s)", testToken, testToken[len(testToken)-12:])
	_, err := call(t, a, "onepassword_vaults", `{}`)
	if err == nil {
		t.Fatal("want an error")
	}
	assertNoSecrets(t, "tool error", err.Error())
	st, _ := json.Marshal(a.status())
	assertNoSecrets(t, "status", string(st))
	if a.status()["state"] != "ok" {
		t.Fatalf("an ordinary error leaves the tools offered: %v", a.status())
	}
}

func TestStatus(t *testing.T) {
	a := newApp(fakeConnect(&fakeOP{}))
	if st := a.status(); st["state"] != "not_configured" || st["guide"] == nil {
		t.Fatalf("no token: %v", st)
	}

	a.token = "ops_" + strings.Repeat("x", 40) // a token 1Password refuses
	if _, err := call(t, a, "onepassword_vaults", `{}`); err == nil {
		t.Fatal("a refused token is an error")
	}
	st := a.status()
	if st["state"] != "not_signed_in" {
		t.Fatalf("refused token: %v", st)
	}
	if strings.Contains(fmt.Sprint(st), "xxxxxxxxxxxx") {
		t.Fatalf("the status quotes the token: %v", st)
	}

	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	b, f := connected(t)
	b.now = func() time.Time { return now }
	f.failAll = &op.RateLimitExceededError{}
	_, err := call(t, b, "onepassword_vaults", `{}`)
	var e *ext.Error
	if !errors.As(err, &e) || e.Code != "rate_limited" {
		t.Fatalf("a rate limit keeps its code: %v", err)
	}
	if st := b.status(); st["state"] != "rate_limited" {
		t.Fatalf("rate limited: %v", st)
	}
	now = now.Add(rateWindow + time.Second)
	if st := b.status(); st["state"] != "ok" {
		t.Fatalf("the rate limit is over after its window: %v", st)
	}
}

func TestSettingsCheck(t *testing.T) {
	a := newApp(fakeConnect(&fakeOP{}))
	check := func(token string) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"config": map[string]any{}, "secrets": map[string]string{tokenSecret: token}})
		out, err := a.settings(context.Background(), "check", raw)
		if err != nil {
			t.Fatal(err)
		}
		return out.(map[string]any)
	}
	if res := check(testToken); res["ok"] != true {
		t.Fatalf("a good token: %v", res)
	}
	if res := check("not-a-token"); res["ok"] != false || !strings.Contains(res["error"].(string), "ops_") {
		t.Fatalf("not a service account token: %v", res)
	}
	bad := "ops_" + strings.Repeat("y", 40)
	res := check(bad)
	if res["ok"] != false || strings.Contains(res["error"].(string), strings.Repeat("y", 12)) {
		t.Fatalf("a refused token is refused without quoting it: %v", res)
	}
	if !strings.Contains(res["error"].(string), "1Password did not accept this token: invalid service account token") {
		t.Fatalf("the refusal drops the SDK's prefix: %v", res)
	}
	a.locale = "zh-Hans"
	if res := check("not-a-token"); res["error"] != locales["zh-Hans"].checkNotToken {
		t.Fatalf("the check speaks the hub's language: %v", res)
	}
	a.locale = ""
	if res := check(""); res["ok"] != true {
		t.Fatalf("no token is the hub's to judge (required): %v", res)
	}
	if _, err := a.settings(context.Background(), "other", nil); err == nil {
		t.Fatal("settings.other is an unknown method")
	}
	// the check signs in on its own: the running program keeps its token and client
	if a.api != nil || a.token != "" {
		t.Fatal("the check changed the running program's state")
	}
}

func TestPage(t *testing.T) {
	render := func(a *app, locale string) (*wire.View, string) {
		t.Helper()
		v := a.view(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{Contribution: uiConnection, Locale: locale, Size: wire.SizeFull}})
		raw, _ := json.Marshal(v)
		if err := exttest.CheckView(raw, false); err != nil {
			t.Fatalf("view: %v\n%s", err, raw)
		}
		assertNoSecrets(t, "page", string(raw))
		return v, string(raw)
	}
	_, raw := render(newApp(fakeConnect(&fakeOP{})), "en")
	if !strings.Contains(raw, "Not connected yet") {
		t.Fatalf("no token: %s", raw)
	}
	a, f := connected(t)
	_, raw = render(a, "zh-Hans")
	for _, want := range []string{"已连接", "可读的保险库：3 个", "Dev", "3 个条目"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("connected: want %q in %s", want, raw)
		}
	}
	f.failAll = errors.New("dial tcp: lookup example.1password.com: no such host " + testToken)
	_, raw = render(a, "ja")
	if !strings.Contains(raw, "1Password に接続できません") || !strings.Contains(raw, "refresh") {
		t.Fatalf("unreachable: %s", raw)
	}
	if !strings.Contains(raw, "1Password からのメッセージ") {
		t.Fatalf("unreachable shows 1Password's words: %s", raw)
	}
	f.failAll = context.DeadlineExceeded
	_, raw = render(a, "en")
	if !strings.Contains(raw, "1Password did not answer in time.") || strings.Contains(raw, "1Password says") {
		t.Fatalf("timed out: %s", raw)
	}
	f.failAll = &op.RateLimitExceededError{}
	_, raw = render(a, "en")
	if !strings.Contains(raw, "1Password's rate limit reached") {
		t.Fatalf("rate limited: %s", raw)
	}
	res, err := a.page().Action(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{Contribution: uiConnection, Action: "refresh", Locale: "en"}})
	if err != nil || res.View == nil {
		t.Fatalf("refresh: %v %v", res, err)
	}
	refused := newApp(fakeConnect(&fakeOP{}))
	refused.token = "ops_" + strings.Repeat("z", 40)
	_, raw = render(refused, "en")
	if !strings.Contains(raw, "1Password refused the token") {
		t.Fatalf("refused: %s", raw)
	}
}

func TestScrubber(t *testing.T) {
	s := newScrubber([]string{"abc", "secret-1234", "line one\nline-two-secret", "   "})
	if got := s.clean("title abc secret-1234 line-two-secret"); got != "title abc [hidden] [hidden]" {
		t.Fatalf("clean: %q", got)
	}
}
