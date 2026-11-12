package main

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	op "github.com/1password/onepassword-sdk-go"
	"github.com/nwverse/persona/sdk/go/ext"
)

// opAPI is the part of 1Password the extension uses: three reads. The official SDK's client implements it (sdkAPI);
// tests give a fake that answers the SDK's own types, secret values included, so they can check none gets out.
type opAPI interface {
	listVaults(ctx context.Context) ([]op.VaultOverview, error)
	listItems(ctx context.Context, vaultID string) ([]op.ItemOverview, error)
	getItem(ctx context.Context, vaultID, itemID string) (op.Item, error)
	// resolve is a reference's value (secrets.resolve): the only way a value leaves 1Password through this program.
	resolve(ctx context.Context, reference string) (string, error)
}

type connectFunc func(ctx context.Context, token string) (opAPI, error)

type sdkAPI struct{ c *op.Client }

func (s sdkAPI) listVaults(ctx context.Context) ([]op.VaultOverview, error) {
	return s.c.Vaults().List(ctx)
}

func (s sdkAPI) listItems(ctx context.Context, vaultID string) ([]op.ItemOverview, error) {
	active := op.NewItemListFilterTypeVariantByState(&op.ItemListFilterByStateInner{Active: true})
	return s.c.Items().List(ctx, vaultID, active)
}

func (s sdkAPI) getItem(ctx context.Context, vaultID, itemID string) (op.Item, error) {
	return s.c.Items().Get(ctx, vaultID, itemID)
}

func (s sdkAPI) resolve(ctx context.Context, reference string) (string, error) {
	return s.c.Secrets().Resolve(ctx, reference)
}

// sdkMu makes one SDK client at a time: the SDK keeps its core in a package variable it sets without a lock, and a
// settings check can sign in while a tool call does.
var sdkMu sync.Mutex

// validateSDK is 1Password's own parser of a reference (local: no request, no sign-in), so the extension takes
// exactly the references 1Password resolves. Under sdkMu: its first use makes the SDK's core.
func validateSDK(ctx context.Context, reference string) error {
	sdkMu.Lock()
	defer sdkMu.Unlock()
	return op.Secrets.ValidateSecretReference(ctx, reference)
}

// connectSDK signs in to 1Password with a service account token. The SDK's core is WebAssembly compiled on the first
// client, which takes a second or two and is why the extension connects only when something needs it.
func connectSDK(ctx context.Context, token string) (opAPI, error) {
	sdkMu.Lock()
	defer sdkMu.Unlock()
	c, err := op.NewClient(ctx, op.WithServiceAccountToken(token), op.WithIntegrationInfo("Persona 1Password extension", about.Version))
	if err != nil {
		return nil, err
	}
	return sdkAPI{c}, nil
}

// What the extension keeps of 1Password's answers. Secret values never reach these types: itemFromSDK drops them, and
// first scrubs every name it keeps of them.
type vaultInfo struct {
	ID, Title string
	Items     int
	Updated   time.Time
}

type itemInfo struct {
	ID, Title, Category, VaultID string
	Tags, Websites               []string
	Updated                      time.Time
}

// fieldInfo is a field's name, type, section and reference; ID and SectionID (1Password's own, never values) and the
// keys of its and its section's names as 1Password has them (nameKey) let a reference be matched against what was
// kept, also by a name that held a value and is kept scrubbed.
type fieldInfo struct {
	Title, Type, Section, Reference string
	ID, SectionID                   string
	TitleKey, SectionKey            string
}

type itemDetail struct {
	itemInfo
	Fields   []fieldInfo
	Files    int
	HasNotes bool
}

// vaultFromSDK keeps a vault's name and size; Items is -1 when op on a computer did not say (unknownCount).
func vaultFromSDK(v op.VaultOverview) vaultInfo {
	items := int(v.ActiveItemCount)
	if v.ActiveItemCount == unknownCount {
		items = -1
	}
	return vaultInfo{ID: v.ID, Title: v.Title, Items: items, Updated: v.UpdatedAt}
}

func itemFromOverview(it op.ItemOverview) itemInfo {
	return itemInfo{ID: it.ID, Title: it.Title, Category: string(it.Category), VaultID: it.VaultID, Tags: it.Tags,
		Websites: websites(it.Websites), Updated: it.UpdatedAt}
}

// itemFromSDK keeps an item's metadata and each field's name, type, section and op:// reference. Field values, notes,
// one-time codes and other field details are dropped here; any of those values that also appears in a name it keeps
// (a title, a tag, a website, a field or section name) is replaced first, so nothing it returns can carry one.
func itemFromSDK(it op.Item) itemDetail {
	var values []string
	for _, f := range it.Fields {
		values = append(values, f.Value)
	}
	values = append(values, it.Notes)
	s := newScrubber(values)

	d := itemDetail{
		itemInfo: itemInfo{ID: it.ID, Title: s.clean(it.Title), Category: string(it.Category), VaultID: it.VaultID,
			Updated: it.UpdatedAt},
		Files:    len(it.Files),
		HasNotes: strings.TrimSpace(it.Notes) != "",
	}
	if it.Document != nil {
		d.Files++
	}
	for _, t := range it.Tags {
		d.Tags = append(d.Tags, s.clean(t))
	}
	for _, w := range websites(it.Websites) {
		d.Websites = append(d.Websites, s.clean(w))
	}

	sections := map[string]op.ItemSection{}
	sectionTitles := map[string]int{}
	for _, sec := range it.Sections {
		sections[sec.ID] = sec
		sectionTitles[strings.ToLower(strings.TrimSpace(sec.Title))]++
	}
	// A field is named in a reference by its section and its own name when both are unique and only use the
	// characters references allow; otherwise by IDs, which always are.
	// A field outside any section is looked up among all the item's fields, one in a section among that section's.
	fieldTitles, allTitles := map[string]int{}, map[string]int{}
	for _, f := range it.Fields {
		t := strings.ToLower(strings.TrimSpace(f.Title))
		fieldTitles[sectionOf(f, sections)+"\x00"+t]++
		allTitles[t]++
	}
	for _, f := range it.Fields {
		sid := sectionOf(f, sections)
		fi := fieldInfo{Title: s.clean(f.Title), Type: string(f.FieldType), ID: f.ID, SectionID: sid, TitleKey: nameKey(f.Title)}
		if sid != "" {
			fi.SectionKey = nameKey(sections[sid].Title)
		}
		ref := "op://" + it.VaultID + "/" + it.ID + "/"
		if sid != "" {
			sec := sections[sid]
			fi.Section = s.clean(sec.Title)
			if refSafe(sec.Title) && sectionTitles[strings.ToLower(strings.TrimSpace(sec.Title))] == 1 && s.clean(sec.Title) == sec.Title {
				ref += strings.TrimSpace(sec.Title) + "/"
			} else {
				ref += sid + "/"
			}
		}
		t := strings.ToLower(strings.TrimSpace(f.Title))
		unique := fieldTitles[sid+"\x00"+t] == 1 && (sid != "" || allTitles[t] == 1)
		if refSafe(f.Title) && unique && s.clean(f.Title) == f.Title {
			ref += strings.TrimSpace(f.Title)
		} else {
			ref += f.ID
		}
		fi.Reference = ref
		d.Fields = append(d.Fields, fi)
	}
	return d
}

// sectionOf is the ID of the section a field is in, "" for none (or the item's unnamed default section).
func sectionOf(f op.ItemField, sections map[string]op.ItemSection) string {
	if f.SectionID == nil || *f.SectionID == "" {
		return ""
	}
	if sec, ok := sections[*f.SectionID]; !ok || strings.TrimSpace(sec.Title) == "" {
		return ""
	}
	return *f.SectionID
}

// refSafe says a name can stand in an op:// reference as it is: 1Password references allow only ASCII letters and
// digits, "-", "_", "." and spaces in a name.
var refName = regexp.MustCompile(`^[A-Za-z0-9_.\- ]+$`)

func refSafe(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && refName.MatchString(name)
}

// websites keeps each website's scheme, host and path: a user name, password, query or fragment in a saved URL can
// carry a credential, so they are left out.
func websites(ws []op.Website) []string {
	var out []string
	for _, w := range ws {
		if u := cleanURL(w.URL); u != "" {
			out = append(out, u)
		}
	}
	return out
}

func cleanURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	withScheme := raw
	if !strings.Contains(raw, "://") {
		withScheme = "https://" + raw
	}
	u, err := url.Parse(withScheme)
	if err != nil || u.Host == "" {
		return ""
	}
	out := u.Host + strings.TrimSuffix(u.EscapedPath(), "/")
	if strings.Contains(raw, "://") && u.Scheme != "https" {
		out = u.Scheme + "://" + out
	}
	return out
}

// hostOf is the host of a website as cleanURL writes it, lowercase and without "www.".
func hostOf(site string) string {
	site = strings.ToLower(site)
	if i := strings.Index(site, "://"); i >= 0 {
		site = site[i+3:]
	}
	if i := strings.IndexAny(site, "/?#"); i >= 0 {
		site = site[:i]
	}
	if at := strings.LastIndex(site, "@"); at >= 0 {
		site = site[at+1:]
	}
	return strings.TrimPrefix(site, "www.")
}

// matchesTag is an exact tag (case aside) or one of its nested tags ("work" matches "work/servers").
func matchesTag(tags []string, want string) bool {
	want = strings.Trim(strings.ToLower(strings.TrimSpace(want)), "/")
	for _, t := range tags {
		t = strings.Trim(strings.ToLower(strings.TrimSpace(t)), "/")
		if t == want || strings.HasPrefix(t, want+"/") {
			return true
		}
	}
	return false
}

// matchesSite: the website's host is the one asked for or a subdomain of it.
func matchesSite(sites []string, want string) bool {
	want = hostOf(strings.TrimSpace(want))
	if want == "" {
		return false
	}
	for _, s := range sites {
		h := hostOf(s)
		if h == want || strings.HasSuffix(h, "."+want) {
			return true
		}
	}
	return false
}

func sortItems(items []itemInfo) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := strings.ToLower(items[i].Title), strings.ToLower(items[j].Title)
		if a != b {
			return a < b
		}
		return items[i].ID < items[j].ID
	})
}

// errKind sorts 1Password's errors for the connector's status.
type errKind int

const (
	errOther errKind = iota
	errAuth          // the token refused, or op on the computer missing or without an account: the tools go
	errRate
	errLocked // the 1Password app on the computer locked or op not approved: said, but the tools stay
)

func kindOf(err error) errKind {
	var rate *op.RateLimitExceededError
	var coded *ext.Error // a rate limit after clean, or an error of op on a computer (node.go)
	if errors.As(err, &rate) {
		return errRate
	}
	if errors.As(err, &coded) && coded.Code != "" { // decided by the code alone: its words quote item names
		switch coded.Code {
		case codeRateLimited:
			return errRate
		case codeMissing, codeNotSignedIn:
			return errAuth
		case codeLocked:
			return errLocked
		}
		return errOther
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"service account token", "unauthorized", "authentication", "invalid token", "token is expired", "revoked"} {
		if strings.Contains(msg, s) {
			return errAuth
		}
	}
	return errOther
}
