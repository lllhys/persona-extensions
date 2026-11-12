package main

// The program on the user's computer (#376). With a computer picked in the settings (configuration "machines"), the
// hub program asks it instead of a service account, through the hub's nodes.call: it runs the 1Password CLI (op) there,
// signed in through the 1Password app, which asks the user to authorize op the first time and after it locks. No
// token is involved, and none leaves the computer.
//
// A read's answer never carries a value: every field's value, the notes, one-time codes and the details of fields and
// files are dropped here, after any name that contains one of them is scrubbed, so the hub gets the same metadata the
// service account's reads give, already clean. Only op.read answers a value: a reference's, for the hub program's
// secrets.resolve, which hands it to Persona's manager and nowhere else.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	op "github.com/1password/onepassword-sdk-go"
	"github.com/nwverse/persona/sdk/go/ext"
)

const (
	// opTimeout is the longest one op run may take: long enough for the user to approve op in the 1Password app.
	opTimeout = 60 * time.Second
	// mostOpOutput is the most of op's output read (an item with many fields is a few hundred KB at most).
	mostOpOutput = 8 << 20
)

// Codes of the computer program's errors, which the hub program reads (opError).
const (
	codeMissing     = "op_missing"    // op is not installed there
	codeNotSignedIn = "not_signed_in" // no account in op, or the app's CLI integration is off
	codeLocked      = "locked"        // the app is locked, or the user dismissed or never answered its prompt
	codeNotFound    = "not_found"     // no such vault, item or field
	codeRateLimited = "rate_limited"
	codeRefused     = "refused" // a value op gives that Persona can't take (not text)
	codeFailed      = "failed"
)

// opID is a vault's or an item's ID as 1Password makes them; the hub program sends only IDs it read from op.
var opID = regexp.MustCompile(`^[a-z0-9]{26}$`)

// opPaths are where op is installed when it is not on the program's PATH (the node starts it with a clean environment).
var opPaths = []string{"/opt/homebrew/bin/op", "/usr/local/bin/op", "/usr/bin/op"}

// runner runs op with args and gives what it wrote to stdout, or an error with what it wrote to stderr.
type runner func(ctx context.Context, args ...string) ([]byte, error)

type nodeApp struct {
	mu  sync.Mutex // one op at a time, so the app asks the user once
	run runner
}

func newNodeApp() *nodeApp { return &nodeApp{run: runOp} }

func nodeProgram(n *nodeApp) ext.Program {
	return ext.Program{ID: about.ID, Version: about.Version,
		Namespaces: map[string]ext.Handler{"op": n.handle}}
}

type nodeParams struct {
	Vault     string `json:"vault"`
	Item      string `json:"item"`
	Reference string `json:"reference"`
	// WithinMS is how long the hub program still waits for the answer: op gets no longer, so nothing (a value least
	// of all) is read for a caller that has given up.
	WithinMS int64 `json:"within_ms"`
}

// handoffMargin is the part of the caller's time kept for the answer's way back through the node and the hub.
const handoffMargin = 500 * time.Millisecond

// nodeStatus is op.status's answer: whether op is installed (op --version, which asks the 1Password app nothing).
type nodeStatus struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
	Code      string `json:"code,omitempty"`
}

func (n *nodeApp) handle(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var in nodeParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, &ext.Error{Code: "invalid", Message: "invalid op parameters"}
		}
	}
	limit := opTimeout
	if in.WithinMS > 0 {
		limit = min(limit, time.Duration(in.WithinMS)*time.Millisecond-handoffMargin)
	}
	if limit <= 0 {
		return nil, &ext.Error{Code: codeLocked, Message: "no time left to ask op"}
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	if method == "status" { // asks the app nothing, so it never waits behind a prompt
		return n.status(ctx), nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if ctx.Err() != nil { // waited behind another op call until the caller gave up
		return nil, &ext.Error{Code: codeLocked, Message: "op was busy with an earlier request that waited for the 1Password app"}
	}
	switch method {
	case "vaults":
		return n.vaults(ctx)
	case "items":
		if !opID.MatchString(in.Vault) {
			return nil, &ext.Error{Code: "invalid", Message: "a vault ID"}
		}
		return n.items(ctx, in.Vault)
	case "item":
		if !opID.MatchString(in.Vault) || !opID.MatchString(in.Item) {
			return nil, &ext.Error{Code: "invalid", Message: "a vault ID and an item ID"}
		}
		return n.item(ctx, in.Vault, in.Item)
	case "read":
		if _, ok := parseReference(in.Reference); !ok {
			return nil, &ext.Error{Code: "invalid", Message: "an op:// reference"}
		}
		out, err := n.run(ctx, "read", "--no-newline", "--", strings.TrimSpace(in.Reference))
		if err != nil {
			return nil, err
		}
		if !utf8.Valid(out) { // a file's bytes, say: JSON would mangle them, so none is handed on
			clear(out)
			return nil, &ext.Error{Code: codeRefused, Message: "this field's value is not text"}
		}
		return map[string]string{"value": string(out)}, nil
	}
	return nil, ext.UnknownMethod("op." + method)
}

func (n *nodeApp) status(ctx context.Context) nodeStatus {
	out, err := n.run(ctx, "--version")
	if err != nil {
		return statusOf(nodeStatus{}, err)
	}
	return nodeStatus{Installed: true, Version: clip(strings.TrimSpace(string(out)), 40)}
}

func statusOf(st nodeStatus, err error) nodeStatus {
	var e *ext.Error
	if errors.As(err, &e) {
		st.Code, st.Error = e.Code, e.Message
	} else {
		st.Code, st.Error = codeFailed, oneLine(err.Error(), 300)
	}
	return st
}

// What op answers with --format json, as far as the extension reads it.
type cliVault struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Items     *uint32 `json:"items"`
	UpdatedAt cliTime `json:"updated_at"`
}

type cliItem struct {
	ID       string              `json:"id"`
	Title    string              `json:"title"`
	Category string              `json:"category"`
	Version  uint32              `json:"version"`
	Vault    struct{ ID string } `json:"vault"`
	Tags     []string            `json:"tags"`
	URLs     []struct {
		Label string `json:"label"`
		Href  string `json:"href"`
	} `json:"urls"`
	CreatedAt cliTime `json:"created_at"`
	UpdatedAt cliTime `json:"updated_at"`
	Sections  []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	} `json:"sections"`
	Fields []struct {
		ID      string          `json:"id"`
		Type    string          `json:"type"`
		Purpose string          `json:"purpose"`
		Label   string          `json:"label"`
		Value   json.RawMessage `json:"value"`
		TOTP    string          `json:"totp"`
		Section *struct {
			ID string `json:"id"`
		} `json:"section"`
	} `json:"fields"`
	Files []json.RawMessage `json:"files"`
}

// cliTime is op's timestamp; one it writes in another form is left zero rather than failing the answer.
type cliTime struct{ time.Time }

func (t *cliTime) UnmarshalJSON(b []byte) error {
	_ = t.Time.UnmarshalJSON(b)
	return nil
}

func (n *nodeApp) vaults(ctx context.Context) ([]op.VaultOverview, error) {
	out, err := n.run(ctx, "vault", "list", "--format", "json")
	if err != nil {
		return nil, err
	}
	var vs []cliVault
	if err := json.Unmarshal(out, &vs); err != nil {
		return nil, &ext.Error{Code: codeFailed, Message: "op's vault list could not be read"}
	}
	res := make([]op.VaultOverview, 0, len(vs))
	for _, v := range vs {
		ov := op.VaultOverview{ID: v.ID, Title: v.Name, UpdatedAt: v.UpdatedAt.Time}
		if v.Items != nil {
			ov.ActiveItemCount = *v.Items
		} else {
			ov.ActiveItemCount = unknownCount
		}
		res = append(res, ov)
	}
	return res, nil
}

// unknownCount stands for "op did not say how many items" in a vault overview's ActiveItemCount; the page then shows none.
const unknownCount = ^uint32(0)

func (n *nodeApp) items(ctx context.Context, vault string) ([]op.ItemOverview, error) {
	out, err := n.run(ctx, "item", "list", "--vault", vault, "--format", "json")
	if err != nil {
		return nil, err
	}
	var its []cliItem
	if err := json.Unmarshal(out, &its); err != nil {
		return nil, &ext.Error{Code: codeFailed, Message: "op's item list could not be read"}
	}
	res := make([]op.ItemOverview, 0, len(its))
	for _, it := range its {
		ov := op.ItemOverview{ID: it.ID, Title: it.Title, Category: cliCategory(it.Category), VaultID: it.Vault.ID,
			Tags: it.Tags, CreatedAt: it.CreatedAt.Time, UpdatedAt: it.UpdatedAt.Time, State: op.ItemStateActive}
		var sites []op.Website
		for _, u := range it.URLs {
			sites = append(sites, op.Website{URL: u.Href})
		}
		for _, w := range websites(sites) { // a user name, password or query in a saved address stays here
			ov.Websites = append(ov.Websites, op.Website{URL: w})
		}
		res = append(res, ov)
	}
	return res, nil
}

func (n *nodeApp) item(ctx context.Context, vault, item string) (op.Item, error) {
	out, err := n.run(ctx, "item", "get", item, "--vault", vault, "--format", "json")
	if err != nil {
		return op.Item{}, err
	}
	var it cliItem
	err = json.Unmarshal(out, &it)
	clear(out) // the values are in it now, and leave with it
	if err != nil {
		return op.Item{}, &ext.Error{Code: codeFailed, Message: "op's item could not be read"}
	}
	full, codes := itemFromCLI(it)
	return scrubItem(full, codes...), nil
}

// itemFromCLI is op's item as the SDK's type, values included (scrubItem drops them before it leaves), and other
// values to scrub for: the current one-time codes op adds to its one-time password fields, and the parts of a value
// that is not one string (an address).
func itemFromCLI(it cliItem) (op.Item, []string) {
	out := op.Item{ID: it.ID, Title: it.Title, Category: cliCategory(it.Category), VaultID: it.Vault.ID, Tags: it.Tags,
		Version: it.Version, CreatedAt: it.CreatedAt.Time, UpdatedAt: it.UpdatedAt.Time}
	for _, u := range it.URLs {
		out.Websites = append(out.Websites, op.Website{URL: u.Href, Label: u.Label})
	}
	for _, s := range it.Sections {
		out.Sections = append(out.Sections, op.ItemSection{ID: s.ID, Title: s.Label})
	}
	var otps []string
	for _, f := range it.Fields {
		value, parts := cliValue(f.Value)
		otps = append(otps, parts...)
		if f.Purpose == "NOTES" {
			out.Notes = value
			continue
		}
		field := op.ItemField{ID: f.ID, Title: f.Label, FieldType: cliFieldType(f.Type), Value: value}
		if f.Section != nil && f.Section.ID != "" {
			sid := f.Section.ID
			field.SectionID = &sid
		}
		if f.TOTP != "" {
			otps = append(otps, f.TOTP)
		}
		out.Fields = append(out.Fields, field)
	}
	out.Files = make([]op.ItemFile, len(it.Files))
	return out, otps
}

// cliValue is a field's value as text, and when op wrote it as something other than a string (an address is an
// object), every string and number in it, each a value to scrub for.
func cliValue(raw json.RawMessage) (string, []string) {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw), nil
	}
	var parts []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			parts = append(parts, x)
		case float64:
			parts = append(parts, strconv.FormatFloat(x, 'f', -1, 64))
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
	return strings.Join(parts, "\n"), parts
}

// scrubItem is an item as it may leave the computer: what itemFromSDK keeps, and nothing it drops. Any value that also
// appears in a name (title, tag, website, section or field name) is replaced in it first, as itemFromSDK does; then
// every value, the notes and the files' details are dropped. Notes that were there leave as a placeholder, so the
// hub can still say the item has some. extra are other values to scrub for (one-time codes).
func scrubItem(it op.Item, extra ...string) op.Item {
	values := append([]string{it.Notes}, extra...)
	for _, f := range it.Fields {
		values = append(values, f.Value)
	}
	s := newScrubber(values)
	out := op.Item{ID: it.ID, Title: s.clean(it.Title), Category: it.Category, VaultID: it.VaultID, Version: it.Version,
		CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt, Files: make([]op.ItemFile, len(it.Files))}
	if it.Document != nil {
		out.Document = &op.FileAttributes{}
	}
	if strings.TrimSpace(it.Notes) != "" {
		out.Notes = hidden
	}
	for _, t := range it.Tags {
		out.Tags = append(out.Tags, s.clean(t))
	}
	for _, w := range websites(it.Websites) { // scheme, host and path only, as websites keeps them
		out.Websites = append(out.Websites, op.Website{URL: s.clean(w)})
	}
	for _, sec := range it.Sections {
		out.Sections = append(out.Sections, op.ItemSection{ID: sec.ID, Title: s.clean(sec.Title)})
	}
	for _, f := range it.Fields {
		out.Fields = append(out.Fields, op.ItemField{ID: f.ID, Title: s.clean(f.Title), SectionID: f.SectionID, FieldType: f.FieldType})
	}
	return out
}

// cliCategory is op's category (LOGIN, API_CREDENTIAL…) as the SDK names it.
func cliCategory(c string) op.ItemCategory {
	switch c {
	case "LOGIN":
		return op.ItemCategoryLogin
	case "SECURE_NOTE":
		return op.ItemCategorySecureNote
	case "CREDIT_CARD":
		return op.ItemCategoryCreditCard
	case "CRYPTO_WALLET":
		return op.ItemCategoryCryptoWallet
	case "IDENTITY":
		return op.ItemCategoryIdentity
	case "PASSWORD":
		return op.ItemCategoryPassword
	case "DOCUMENT":
		return op.ItemCategoryDocument
	case "API_CREDENTIAL":
		return op.ItemCategoryAPICredentials
	case "BANK_ACCOUNT":
		return op.ItemCategoryBankAccount
	case "DATABASE":
		return op.ItemCategoryDatabase
	case "DRIVER_LICENSE":
		return op.ItemCategoryDriverLicense
	case "EMAIL_ACCOUNT":
		return op.ItemCategoryEmail
	case "MEDICAL_RECORD":
		return op.ItemCategoryMedicalRecord
	case "MEMBERSHIP":
		return op.ItemCategoryMembership
	case "OUTDOOR_LICENSE":
		return op.ItemCategoryOutdoorLicense
	case "PASSPORT":
		return op.ItemCategoryPassport
	case "REWARD_PROGRAM":
		return op.ItemCategoryRewards
	case "WIRELESS_ROUTER":
		return op.ItemCategoryRouter
	case "SERVER":
		return op.ItemCategoryServer
	case "SSH_KEY":
		return op.ItemCategorySSHKey
	case "SOCIAL_SECURITY_NUMBER":
		return op.ItemCategorySocialSecurityNumber
	case "SOFTWARE_LICENSE":
		return op.ItemCategorySoftwareLicense
	}
	return op.ItemCategoryUnsupported
}

// cliFieldType is op's field type (STRING, CONCEALED, OTP…) as the SDK names it.
func cliFieldType(t string) op.ItemFieldType {
	switch t {
	case "STRING":
		return op.ItemFieldTypeText
	case "CONCEALED":
		return op.ItemFieldTypeConcealed
	case "CREDIT_CARD_TYPE":
		return op.ItemFieldTypeCreditCardType
	case "CREDIT_CARD_NUMBER":
		return op.ItemFieldTypeCreditCardNumber
	case "PHONE":
		return op.ItemFieldTypePhone
	case "URL":
		return op.ItemFieldTypeURL
	case "OTP":
		return op.ItemFieldTypeTOTP
	case "EMAIL":
		return op.ItemFieldTypeEmail
	case "REFERENCE":
		return op.ItemFieldTypeReference
	case "SSHKEY":
		return op.ItemFieldTypeSSHKey
	case "MENU":
		return op.ItemFieldTypeMenu
	case "MONTH_YEAR":
		return op.ItemFieldTypeMonthYear
	case "ADDRESS":
		return op.ItemFieldTypeAddress
	case "DATE":
		return op.ItemFieldTypeDate
	}
	return op.ItemFieldTypeUnsupported
}

// runOp runs op with args in the user's own sign-in: the 1Password app's, never a service account or Connect server
// that the environment might name, so those variables are left out.
func runOp(ctx context.Context, args ...string) ([]byte, error) {
	path := findOp()
	if path == "" {
		return nil, &ext.Error{Code: codeMissing, Message: "op (the 1Password CLI) is not installed on this computer"}
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = opEnv(os.Environ(), runtime.GOOS, os.Getuid(), exists)
	var stdout, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if err == nil && stdout.over {
		clear(stdout.Bytes())
		return nil, &ext.Error{Code: codeFailed, Message: "op's answer was too large"}
	}
	if err == nil {
		return stdout.Bytes(), nil
	}
	clear(stdout.Bytes())
	if ctx.Err() != nil {
		return nil, &ext.Error{Code: codeLocked, Message: "op got no answer from the 1Password app in time"}
	}
	return nil, opError(stderr.String())
}

func findOp() string {
	if p, err := exec.LookPath("op"); err == nil {
		return p
	}
	for _, p := range opPaths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// opEnv is the environment op runs in: the node's, without what would make op sign in some other way, and with HOME
// (op keeps its settings there and finds the app through it). On Linux op reaches the 1Password app through the
// user's session, which the node's clean environment leaves out: XDG_RUNTIME_DIR and the session bus are added from
// /run/user/<uid> when they are there (not tried with a real app yet).
func opEnv(env []string, goos string, uid int, exists func(string) bool) []string {
	out := make([]string, 0, len(env)+3)
	have := map[string]bool{}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "OP_SERVICE_ACCOUNT_TOKEN", "OP_CONNECT_HOST", "OP_CONNECT_TOKEN", "OP_SESSION", "OP_FORMAT":
			continue
		}
		if strings.HasPrefix(k, "OP_SESSION_") {
			continue
		}
		have[k] = true
		out = append(out, kv)
	}
	if !have["HOME"] {
		if u, err := user.Current(); err == nil && u.HomeDir != "" {
			out = append(out, "HOME="+filepath.Clean(u.HomeDir))
		}
	}
	if goos == "linux" {
		run := "/run/user/" + strconv.Itoa(uid)
		if !have["XDG_RUNTIME_DIR"] && exists(run) {
			out = append(out, "XDG_RUNTIME_DIR="+run)
		}
		if !have["DBUS_SESSION_BUS_ADDRESS"] && exists(run+"/bus") {
			out = append(out, "DBUS_SESSION_BUS_ADDRESS=unix:path="+run+"/bus")
		}
	}
	return out
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// errorPrefix is op's "[ERROR] 2026/01/02 15:04:05 " in front of each message.
var errorPrefix = regexp.MustCompile(`^\[ERROR\]\s+\d{4}/\d{2}/\d{2}\s+\d{2}:\d{2}:\d{2}\s+`)

// quoted is what op's messages name in quotes, and references: an item called "Rate limit key" or "Google
// Authentication" must not decide what kind of error it is.
var quoted = regexp.MustCompile(`"[^"]*"|'[^']*'|“[^”]*”|op://\S+`)

// opError is what op wrote to stderr as an error with a code the hub program reads: its last message, one line. The
// code comes from op's own fixed words, with whatever it quotes left out, not-found ones first.
func opError(stderr string) error {
	msg := ""
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if line = strings.TrimSpace(errorPrefix.ReplaceAllString(strings.TrimSpace(line), "")); line != "" {
			msg = line
		}
	}
	if msg == "" {
		msg = "op failed without saying why"
	}
	msg = oneLine(msg, 300)
	low := strings.ToLower(quoted.ReplaceAllString(msg, `""`))
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(low, w) {
				return true
			}
		}
		return false
	}
	code := codeFailed
	switch {
	case has("isn't an item", "isn't a vault", "isn't a field", "isn't a section", "does not have a field",
		"doesn't have a field", "no item matched", "could not find", "not found", "does not exist", "doesn't exist"):
		code = codeNotFound
	case has("too many requests", "rate limit exceeded", "rate-limited"):
		code = codeRateLimited
	case has("authorization prompt dismissed", "authorization timeout", "authorization was denied", "app is locked",
		"1password is locked", "connecting to desktop app", "cannot connect to 1password", "connect to the 1password app"):
		code = codeLocked
	case has("not currently signed in", "account is not signed in", "no accounts configured", "no account found",
		"not signed in"):
		code = codeNotSignedIn
	}
	return &ext.Error{Code: code, Message: msg}
}

// limitedBuffer keeps the first mostOpOutput bytes a command writes, drops the rest and notes that it did.
type limitedBuffer struct {
	bytes.Buffer
	over bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := mostOpOutput - b.Len()
	if len(p) > room {
		b.over = true
		p2 := p
		if room > 0 {
			p2 = p[:room]
		} else {
			p2 = nil
		}
		b.Buffer.Write(p2)
		return len(p), nil
	}
	b.Buffer.Write(p)
	return len(p), nil
}
