package main

// The secret provider (contributes.secret_providers "vault", scheme op; Persona's docs/extensions/secret-providers.md).
// Persona's manager, and only it, asks this program to name a reference in words (secrets.describe), for the value of
// one when a setting that holds it is used or the user approved handing it to one Agent run (secrets.resolve), and
// tells it of each use (secrets.used), which the connection page lists. A value goes back in the answer to the
// manager's call and nowhere else: never in a log line, an error, the page or the assistant's tools, never kept.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

const (
	providerID = "vault"
	// mostUses is how many uses the page lists, newest first; Persona's own audit keeps every one.
	mostUses = 20
	// describeDetailMost is where Persona cuts a description's detail (secretDetailMost): the vault's name.
	describeDetailMost = 40
	describeTitleMost  = 200
	// secretTimeout stays under the manager's 10 seconds a call, so the answer says why rather than the manager timing out.
	secretTimeout = 9 * time.Second
)

// secretUse is one use the manager told of (secrets.used): never a value.
type secretUse struct {
	Reference  string
	Purpose    wire.SecretPurpose
	At         time.Time
	ApprovedBy string
}

func (a *app) secrets(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "describe":
		var in wire.SecretsDescribeParams
		if err := readSecrets(raw, &in, &in.Contribution); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, secretTimeout)
		defer cancel()
		ref, ok := parseReference(in.Reference)
		if !ok || a.validate(ctx, in.Reference) != nil {
			return nil, &ext.Error{Code: "invalid", Message: "not a 1Password reference (op://vault/item/[section/]field)"}
		}
		title, detail := a.describe(ctx, ref)
		return wire.SecretsDescribeResult{Title: title, Detail: detail}, nil
	case "resolve":
		var in wire.SecretsResolveParams
		if err := readSecrets(raw, &in, &in.Contribution); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, secretTimeout)
		defer cancel()
		value, err := a.resolve(ctx, in.Reference)
		if err != nil {
			return nil, err
		}
		return wire.SecretsResolveResult{Value: value}, nil
	case "used": // an event: no answer goes back
		var in wire.SecretsUsedEvent
		if err := readSecrets(raw, &in, &in.Contribution); err != nil {
			return nil, err
		}
		a.noteUse(in)
		return nil, nil
	}
	return nil, ext.UnknownMethod("secrets." + method)
}

// readSecrets decodes a secrets message's parameters into in, and refuses one meant for another contribution.
func readSecrets(raw json.RawMessage, in any, contribution *string) error {
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, in); err != nil {
			return &ext.Error{Code: "invalid", Message: "invalid secrets parameters"}
		}
	}
	if *contribution != "" && *contribution != providerID {
		return ext.NoContribution(*contribution)
	}
	return nil
}

// reference is an op:// reference's parts: op://vault/item/field or op://vault/item/section/field, each a name or an
// ID, and an optional query (?attribute=otp) that only resolving reads. parseReference only splits it; whether
// 1Password takes it is a.validate's (1Password's own parser), so describe never names what resolve would refuse.
type reference struct {
	vault, item, section, field string
}

func parseReference(s string) (reference, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(s), "op://")
	if !ok {
		return reference{}, false
	}
	path, _, _ := strings.Cut(rest, "?")
	parts := strings.Split(path, "/")
	if len(parts) < 3 || len(parts) > 4 {
		return reference{}, false
	}
	for i, p := range parts {
		if p = strings.TrimSpace(p); p == "" {
			return reference{}, false
		}
		parts[i] = p
	}
	r := reference{vault: parts[0], item: parts[1], field: parts[len(parts)-1]}
	if len(parts) == 4 {
		r.section = parts[2]
	}
	return r, true
}

// describe names what a reference points at for people: "item › field", and the vault alone as the detail. The names
// come from the item as 1Password has it (scrubbed of every value, as the tools' are), or from the reference itself when
// it cannot be looked up (not signed in, not found, no answer): Persona shows these, it never decides anything by them.
func (a *app) describe(ctx context.Context, r reference) (title, detail string) {
	title, detail = joinNames(r.item, r.section, r.field), r.vault
	if vault, item, section, field, ok := a.lookup(ctx, r); ok {
		title, detail = joinNames(item, section, field), vault
	}
	a.mu.Lock()
	token := a.token
	a.mu.Unlock()
	return oneLine(redact(title, token), describeTitleMost), oneLine(redact(detail, token), describeDetailMost)
}

// joinNames is "item › field", or "item › section › field" when the section is needed to tell the field from another.
func joinNames(item, section, field string) string {
	if section == "" {
		return item + " › " + field
	}
	return item + " › " + section + " › " + field
}

// lookup finds a reference's vault, item and field by ID or by name (case aside; a name must match one only), and the
// field's section when another field of the item has its name (two "password" fields in two sections read alike
// otherwise on the card the user approves).
func (a *app) lookup(ctx context.Context, r reference) (vault, item, section, field string, ok bool) {
	// Through the cache (cache.go): a name the kept answers lack is looked up again once, as it may be new.
	vaults, err := a.vaultsHaving(ctx, func(v vaultInfo) bool {
		return v.ID == r.vault || strings.EqualFold(strings.TrimSpace(v.Title), r.vault)
	})
	if err != nil {
		return "", "", "", "", false
	}
	v, found := pick(vaults, r.vault, func(v vaultInfo) (string, string) { return v.ID, v.Title })
	if !found {
		return "", "", "", "", false
	}
	items, err := a.itemsHaving(ctx, v.ID, func(it itemInfo) bool {
		return it.ID == r.item || strings.EqualFold(strings.TrimSpace(it.Title), r.item)
	})
	if err != nil {
		return "", "", "", "", false
	}
	it, found := pick(items, r.item, func(it itemInfo) (string, string) { return it.ID, it.Title })
	if !found {
		return "", "", "", "", false
	}
	// Names compare by their keys (nameKey): the kept names may be scrubbed of a value the reference spells out.
	sectionKey, fieldKey := nameKey(r.section), nameKey(r.field)
	inSection := func(f fieldInfo) bool {
		return r.section == "" || f.SectionID != "" && (f.SectionID == r.section || f.SectionKey == sectionKey)
	}
	d, err := a.itemHaving(ctx, v.ID, it.ID, func(f fieldInfo) bool {
		return inSection(f) && (f.ID == r.field || f.TitleKey == fieldKey)
	})
	if err != nil {
		return "", "", "", "", false
	}
	match := -1
	for i, f := range d.Fields {
		if !inSection(f) {
			continue
		}
		if f.ID == r.field {
			match = i
			break
		}
		if f.TitleKey == fieldKey {
			if match >= 0 {
				return "", "", "", "", false // two fields of that name: 1Password would not resolve it either
			}
			match = i
		}
	}
	if match < 0 {
		return "", "", "", "", false
	}
	named := 0
	for _, f := range d.Fields {
		if f.TitleKey == d.Fields[match].TitleKey {
			named++
		}
	}
	if named > 1 {
		section = d.Fields[match].Section
	}
	return clip(redact(v.Title, ""), describeDetailMost), d.Title, section, d.Fields[match].Title, true
}

// pick is the one entry whose ID is want, else the only one whose name is want (case aside).
func pick[T any](list []T, want string, key func(T) (id, name string)) (T, bool) {
	var zero, named T
	n := 0
	for _, x := range list {
		id, name := key(x)
		if id == want {
			return x, true
		}
		if strings.EqualFold(strings.TrimSpace(name), want) {
			named = x
			n++
		}
	}
	if n == 1 {
		return named, true
	}
	return zero, false
}

// resolve is a reference's value, through the service account. Its errors carry Persona's codes and words in the hub's
// language; none carries the value or the token.
func (a *app) resolve(ctx context.Context, reference string) (string, error) {
	a.mu.Lock()
	t := textsFor(a.locale)
	a.mu.Unlock()
	if _, ok := parseReference(reference); !ok || a.validate(ctx, reference) != nil {
		return "", &ext.Error{Code: "refused", Message: t.secretInvalid}
	}
	api, err := a.client(ctx)
	if err != nil {
		return "", a.secretError(err, t)
	}
	value, err := api.resolve(ctx, reference)
	if err != nil {
		a.record(err)
		return "", a.secretError(err, t)
	}
	a.record(nil)
	if value == "" {
		return "", &ext.Error{Code: "refused", Message: t.secretEmpty}
	}
	return value, nil
}

// secretError is how a failed resolution reads to Persona: nothing set up, a token 1Password refuses, or op on the
// computer missing, without an account or locked is not_signed_in, its
// rate limit rate_limited (retry_after: as long as the page says so), a vault, item, section or field it has not
// not_found, a reference that matches more than one, or that its parser refuses, refused; anything else is a failure
// in 1Password's words, redacted and on one line.
func (a *app) secretError(err error, t *texts) error {
	a.mu.Lock()
	says := ""
	if !errors.Is(err, context.DeadlineExceeded) {
		says = oneLine(redact(err.Error(), a.token), 200)
	}
	onComputer := a.machine != ""
	a.mu.Unlock()
	rateGuide := t.rateLimitedGuide
	if onComputer {
		rateGuide = t.opRateLimitedGuide
	}
	retry := json.RawMessage(fmt.Sprintf(`{"retry_after":%d}`, int(rateWindow.Seconds())))
	if code := opCode(err); onComputer && code != "" { // from the computer (or the hub about it): by its code alone
		title, guide, needsUser := opWords(t, err)
		switch {
		case code == "offline":
			return errors.New(title + ". " + guide)
		case needsUser:
			return &ext.Error{Code: "not_signed_in", Message: title + ". " + guide}
		case code == codeNotFound:
			return &ext.Error{Code: "not_found", Message: fmt.Sprintf(t.secretNotFound, says)}
		case code == codeRateLimited:
			return &ext.Error{Code: "rate_limited", Message: t.rateLimited + ". " + rateGuide, Data: retry}
		case code == codeRefused, code == "invalid":
			return &ext.Error{Code: "refused", Message: fmt.Sprintf(t.says, says)}
		}
		return fmt.Errorf(t.says, says)
	}
	switch {
	case errors.Is(err, errNotConfigured):
		return &ext.Error{Code: "not_signed_in", Message: t.secretNoToken}
	case errors.Is(err, context.DeadlineExceeded) && onComputer: // most likely op waiting for the user to approve it
		return errors.New(t.timedOut + " " + t.opLockedGuide)
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New(t.timedOut)
	case onComputer: // no code: the hub could not reach the program there
		return fmt.Errorf(t.says, says)
	case kindOf(err) == errAuth:
		return &ext.Error{Code: "not_signed_in", Message: t.refused + ". " + t.refusedGuide}
	case kindOf(err) == errRate:
		return &ext.Error{Code: "rate_limited", Message: t.rateLimited + ". " + rateGuide, Data: retry}
	case hasAny(err, "more than one"):
		return &ext.Error{Code: "refused", Message: fmt.Sprintf(t.says, says)}
	case notFound(err):
		return &ext.Error{Code: "not_found", Message: fmt.Sprintf(t.secretNotFound, says)}
	case hasAny(err, "could not be parsed", "invalid character", "invalid query parameter", "invalid format"):
		return &ext.Error{Code: "refused", Message: t.secretInvalid}
	}
	return fmt.Errorf(t.says, says)
}

// notFound: 1Password's words for a vault, item, section or field this service account has not ("no item matched the
// secret reference query", "no section matched the secret reference", …).
func notFound(err error) bool {
	return hasAny(err, "matched the secret reference", "not found", "cannot be found", "could not find", "does not exist")
}

func hasAny(err error, words ...string) bool {
	msg := strings.ToLower(err.Error())
	for _, w := range words {
		if strings.Contains(msg, w) {
			return true
		}
	}
	return false
}

// noteUse keeps one use for the page, newest first by when Persona used it (events may arrive out of order: the SDK
// hands each to its own goroutine), the last mostUses of them.
func (a *app) noteUse(in wire.SecretsUsedEvent) {
	if _, ok := parseReference(in.Reference); !ok {
		return
	}
	use := secretUse{Reference: clip(strings.TrimSpace(in.Reference), 300), At: in.At, ApprovedBy: clip(in.ApprovedBy, 80),
		Purpose: wire.SecretPurpose{Kind: clip(in.Purpose.Kind, 40), ID: clip(in.Purpose.ID, 120), Machine: clip(in.Purpose.Machine, 120)}}
	a.mu.Lock()
	defer a.mu.Unlock()
	if use.At.IsZero() {
		use.At = a.now()
	}
	i := 0
	for i < len(a.uses) && !a.uses[i].At.Before(use.At) {
		i++
	}
	a.uses = append(a.uses[:i], append([]secretUse{use}, a.uses[i:]...)...)
	if len(a.uses) > mostUses {
		a.uses = a.uses[:mostUses]
	}
}

func (a *app) recentUses() []secretUse {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]secretUse(nil), a.uses...)
}
