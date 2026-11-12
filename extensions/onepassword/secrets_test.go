package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	op "github.com/1password/onepassword-sdk-go"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The fake's references: what secrets.resolve answers for each, as 1Password's SDK would.
const (
	refByName  = "op://Dev/GitHub token/credential"
	refByID    = "op://" + vaultDev + "/itmgithub/credential"
	refEmpty   = "op://Dev/GitHub token/username-empty"
	refLimited = "op://Dev/GitHub token/rate-limited"
	refMissing = "op://Dev/Nothing here/password"
)

func (f *fakeOP) resolve(_ context.Context, ref string) (string, error) {
	if err := f.note("resolve " + ref); err != nil {
		return "", err
	}
	switch ref {
	case refByName, refByID:
		return secretValues[1], nil
	case refEmpty:
		return "", nil
	case refLimited:
		return "", &op.RateLimitExceededError{}
	}
	return "", errors.New("error resolving secret reference: no item matched the secret reference query")
}

// secretsCall sends params as the manager does and reads the answer as it would: through its JSON.
func secretsCall(t *testing.T, a *app, method string, params any) (any, error) {
	t.Helper()
	raw, _ := json.Marshal(params)
	out, err := a.secrets(context.Background(), method, raw)
	if err != nil || out == nil {
		return out, err
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var answer map[string]string
	if err := json.Unmarshal(b, &answer); err != nil {
		t.Fatal(err)
	}
	return answer, nil
}

func codeOf(err error) string {
	var e *ext.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestDescribeNamesItemFieldAndVault(t *testing.T) {
	a, _ := connected(t)
	for _, c := range []struct{ ref, title, detail string }{
		{refByName, "GitHub token › credential", "Dev"},
		{refByID, "GitHub token › credential", "Dev"}, // IDs are looked up to their names
		{"op://dev/github TOKEN/Backup/one-time password", "GitHub token › one-time password", "Dev"},
		{"op://Dev/GitHub token/sec1/otp1", "GitHub token › one-time password", "Dev"},
		// not found, or a name that matches two (two vaults are called "home", two fields "code"): the reference's
		// own names, never a guess
		{refMissing, "Nothing here › password", "Dev"},
		{"op://Dev/Nothing here/Old keys/password", "Nothing here › Old keys › password", "Dev"},
		{"op://home/Router/password", "Router › password", "home"},
		{"op://Dev/GitHub token/Backup/code", "GitHub token › Backup › code", "Dev"},
		// another field of the item has its name: the section tells them apart on the card
		{"op://Dev/GitHub token/sec1/code1", "GitHub token › Backup › code", "Dev"},
		{"op://Dev/GitHub token/credential?attribute=otp", "GitHub token › credential", "Dev"},
	} {
		out, err := secretsCall(t, a, "describe", map[string]string{"contribution": providerID, "reference": c.ref})
		if err != nil {
			t.Fatalf("%s: %v", c.ref, err)
		}
		got := out.(map[string]string)
		if got["title"] != c.title || got["detail"] != c.detail {
			t.Errorf("%s: %q / %q, want %q / %q", c.ref, got["title"], got["detail"], c.title, c.detail)
		}
	}
}

func TestDescribeNeverCarriesAValue(t *testing.T) {
	a, _ := connected(t)
	// The field's name holds one of the item's values: itemFromSDK scrubs it, and describe uses those names.
	out, err := secretsCall(t, a, "describe", map[string]string{"reference": "op://Dev/GitHub token/sec2/pin"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	assertNoSecrets(t, "describe", string(b))
	if !strings.HasPrefix(out.(map[string]string)["title"], "GitHub token › PIN") {
		t.Fatalf("describe: %s", b)
	}
	// Not signed in: still words, from the reference.
	out, err = secretsCall(t, newApp(fakeConnect(&fakeOP{})), "describe", map[string]string{"reference": refByName})
	if err != nil || out.(map[string]string)["title"] != "GitHub token › credential" {
		t.Fatalf("describe without a token: %v %v", out, err)
	}
	// The detail stays as short as Persona shows it.
	out, _ = secretsCall(t, a, "describe", map[string]string{"reference": "op://" + strings.Repeat("v", 60) + "/item/field"})
	if d := out.(map[string]string)["detail"]; len([]rune(d)) > describeDetailMost || !strings.HasSuffix(d, "…") {
		t.Fatalf("detail %q", d)
	}
	// What 1Password's own parser refuses is not described either, so no card names what resolve would refuse.
	for _, ref := range []string{"https://example.com/a/b", "op://Dev/GitHub%20token/credential", "op://Dev/GitHub token/credential?attribute=type"} {
		if _, err := secretsCall(t, a, "describe", map[string]string{"reference": ref}); codeOf(err) != "invalid" {
			t.Fatalf("%s: %v", ref, err)
		}
	}
}

func TestResolveGivesTheValueAndCodedErrors(t *testing.T) {
	a, f := connected(t)
	for _, ref := range []string{refByName, refByID} {
		out, err := secretsCall(t, a, "resolve", map[string]any{"contribution": providerID, "reference": ref,
			"purpose": map[string]string{"kind": "launcher", "id": "gateway", "machine": "laptop"}})
		if err != nil || out.(map[string]string)["value"] != secretValues[1] {
			t.Fatalf("%s: %v %v", ref, out, err)
		}
	}
	for _, c := range []struct{ ref, code string }{
		{refMissing, "not_found"},
		{refEmpty, "refused"},
		{refLimited, "rate_limited"},
		{"op://Dev/only-two-parts", "refused"},
		{"op://Dev/GitHub%20token/credential", "refused"}, // 1Password's parser takes no escapes
	} {
		_, err := secretsCall(t, a, "resolve", map[string]any{"reference": c.ref})
		if codeOf(err) != c.code {
			t.Errorf("%s: %v (code %q), want %s", c.ref, err, codeOf(err), c.code)
		}
		if err != nil {
			assertNoSecrets(t, c.ref, err.Error())
		}
	}
	_, err := secretsCall(t, a, "resolve", map[string]any{"reference": refLimited})
	var e *ext.Error
	if !errors.As(err, &e) || string(e.Data) != fmt.Sprintf(`{"retry_after":%d}`, int(rateWindow.Seconds())) {
		t.Fatalf("rate limit: %v", err)
	}

	// No token, or one 1Password refuses: not_signed_in, without the token in the words. (A program with a new token is
	// a new program: the hub restarts it.)
	if _, err := secretsCall(t, newApp(fakeConnect(f)), "resolve", map[string]any{"reference": refByName}); codeOf(err) != "not_signed_in" {
		t.Fatalf("no token: %v", err)
	}
	b := newApp(fakeConnect(f))
	b.token = "ops_" + strings.Repeat("x", 40) // one the fake refuses, naming it in its error
	_, err = secretsCall(t, b, "resolve", map[string]any{"reference": refByName})
	if codeOf(err) != "not_signed_in" || strings.Contains(err.Error(), b.token) {
		t.Fatalf("refused token: %v", err)
	}

	// Another contribution is not this one's.
	if _, err := secretsCall(t, a, "resolve", map[string]any{"contribution": "other", "reference": refByName}); err == nil {
		t.Fatal("another contribution's call answered")
	}
}

func TestResolveSpeaksTheHubsLanguage(t *testing.T) {
	a, _ := connected(t)
	a.locale = "zh-Hans"
	_, err := secretsCall(t, a, "resolve", map[string]any{"reference": refEmpty})
	if err == nil || err.Error() != locales["zh-Hans"].secretEmpty {
		t.Fatalf("zh-Hans: %v", err)
	}
}

func TestUsesAreListedNewestFirstAndBounded(t *testing.T) {
	a, _ := connected(t)
	at := time.Date(2026, 11, 12, 1, 30, 0, 0, time.UTC)
	used := func(i int, p map[string]string, by string) {
		t.Helper()
		out, err := secretsCall(t, a, "used", map[string]any{"contribution": providerID, "reference": fmt.Sprintf("op://Dev/Item %d/credential", i),
			"purpose": p, "at": at.Add(time.Duration(i) * time.Minute), "approved_by": by})
		if out != nil || err != nil {
			t.Fatalf("an event has no answer: %v %v", out, err)
		}
	}
	used(1, map[string]string{"kind": "extension", "id": "example-notes"}, "settings")
	used(2, map[string]string{"kind": "agent_run", "id": "GH_TOKEN", "machine": "laptop", "session": "s1"}, "device:d1")
	used(3, map[string]string{"kind": "launcher", "id": "gateway", "machine": "laptop"}, "rule")
	// not a reference: not listed
	_, _ = secretsCall(t, a, "used", map[string]any{"reference": "plain text"})
	// an event that arrives late still sits by its time; an approver Persona did not name is left out
	used(0, map[string]string{"kind": "extension", "id": "late"}, "")

	sec := a.usesSection(locales["en"], "Asia/Tokyo")
	var rows []string
	for _, c := range sec.Items {
		rows = append(rows, c.Title+" | "+c.Subtitle)
	}
	want := []string{
		"op://Dev/Item 3/credential | Model provider key · gateway on laptop · an Always rule · 2026-11-12 10:33",
		"op://Dev/Item 2/credential | For an Agent run · GH_TOKEN on laptop · approved on a device · 2026-11-12 10:32",
		"op://Dev/Item 1/credential | Extension secret · example-notes · saved in Settings · 2026-11-12 10:31",
		"op://Dev/Item 0/credential | Extension secret · late · 2026-11-12 10:30",
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
	for i := 4; i < 4+mostUses; i++ {
		used(i, map[string]string{"kind": "extension", "id": "x"}, "settings")
	}
	if got := a.recentUses(); len(got) != mostUses || got[0].Reference != fmt.Sprintf("op://Dev/Item %d/credential", 3+mostUses) {
		t.Fatalf("kept %d, first %+v", len(got), got[0])
	}
}

func TestPageListsUsesAndNoValue(t *testing.T) {
	a, _ := connected(t)
	empty := a.view(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{Locale: "en"}})
	if !strings.Contains(viewText(empty), "Not used yet") {
		t.Fatalf("no uses: %s", viewText(empty))
	}
	if _, err := secretsCall(t, a, "resolve", map[string]any{"reference": refByName}); err != nil {
		t.Fatal(err)
	}
	_, _ = secretsCall(t, a, "used", map[string]any{"reference": refByName, "purpose": map[string]string{"kind": "extension", "id": "example-notes"},
		"approved_by": "settings"})
	text := viewText(a.view(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{Locale: "en"}}))
	if !strings.Contains(text, refByName) || !strings.Contains(text, "Used by Persona") {
		t.Fatalf("page: %s", text)
	}
	assertNoSecrets(t, "page", text)
}

func viewText(v *wire.View) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestProgramAnswersTheSecretsNamespace(t *testing.T) {
	a, _ := connected(t)
	p := program(a)
	h := p.Namespaces["secrets"]
	if h == nil {
		t.Fatal("no secrets namespace")
	}
	out, err := h(context.Background(), "resolve", json.RawMessage(`{"contribution":"vault","reference":"`+refByName+`","purpose":{"kind":"extension","id":"example-notes"}}`))
	if res, ok := out.(wire.SecretsResolveResult); err != nil || !ok || res.Value != secretValues[1] {
		t.Fatalf("resolve: %T %v", out, err)
	}
	if _, err := h(context.Background(), "write", nil); err == nil {
		t.Fatal("an unknown method answered")
	}
	var m struct {
		Contributes struct {
			SecretProviders []struct {
				ID, Name string
				Schemes  []string
			} `json:"secret_providers"`
		}
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if sp := m.Contributes.SecretProviders; len(sp) != 1 || sp[0].ID != providerID || strings.Join(sp[0].Schemes, ",") != "op" {
		t.Fatalf("manifest secret_providers: %+v", sp)
	}
}
