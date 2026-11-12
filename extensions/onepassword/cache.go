package main

// The extension's memory of 1Password's metadata (#488): the vault list, each vault's item overviews and each item's
// field structure, as the extension keeps them (vaultInfo, itemInfo, itemDetail: values already dropped, names
// scrubbed), for metaTTL, so repeated describes, searches and item looks do not each spend requests of the service
// account's hourly and daily limits (or ask op on the computer again). Never a value: secrets.resolve always asks.
//
// Only in this process's memory. A new token, computer or setting restarts the program, so it starts empty; the
// page's Check again empties it. While 1Password's rate limit holds (rateWindow after it refused), reads answer from
// what is kept, however old, and ask 1Password nothing; with nothing kept they answer the limit at once.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"
)

const (
	// metaTTL is how long kept metadata answers without asking 1Password again.
	metaTTL = 5 * time.Minute
	// missAgain: a vault, item or field named that a kept answer lacks is looked up again once that answer is older
	// than this (it may have just been made); younger, the miss stands.
	missAgain = 30 * time.Second
)

type kept[T any] struct {
	v  T
	at time.Time
}

type metaCache struct {
	vaults  *kept[[]vaultInfo]
	items   map[string]kept[[]itemInfo] // by vault ID
	details map[string]kept[itemDetail] // by vault ID + "/" + item ID
}

// limited says whether 1Password's rate limit holds (rateWindow after it refused, until a request succeeds), and the
// error it answered then.
func (a *app) limited() (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rateErr != nil && a.now().Sub(a.rateAt) < rateWindow {
		return true, a.rateErr
	}
	return false, nil
}

// forgetMeta empties the cache; a read that started before it keeps nothing it got.
func (a *app) forgetMeta() {
	a.metaMu.Lock()
	defer a.metaMu.Unlock()
	a.meta = metaCache{}
	a.metaGen++
}

// cachedRead answers what get finds kept while it is fresh (unless again asks for a new answer), or at any age under
// the rate limit; otherwise it fetches, keeps the answer with put (unless the cache was emptied meanwhile) and answers
// it. A fetch refused for the rate limit answers what was kept, if anything was.
func cachedRead[T any](a *app, again bool, get func(*metaCache) (kept[T], bool), put func(*metaCache, kept[T]), fetch func() (T, error)) (T, error) {
	now := a.now()
	limited, limitErr := a.limited()
	a.metaMu.Lock()
	k, ok := get(&a.meta)
	gen := a.metaGen
	a.metaMu.Unlock()
	if ok && (limited || !again && now.Sub(k.at) < metaTTL) {
		return k.v, nil
	}
	var zero T
	if limited {
		return zero, limitErr
	}
	v, err := fetch()
	if err != nil {
		if ok && kindOf(err) == errRate { // refused for the limit just now: what is kept answers instead
			return k.v, nil
		}
		return zero, err
	}
	a.metaMu.Lock()
	if gen == a.metaGen {
		put(&a.meta, kept[T]{v: v, at: now})
	}
	a.metaMu.Unlock()
	return v, nil
}

// worthAgain says a miss in what get finds kept is worth one more request: it is older than missAgain (what was
// named may have just been made) and the rate limit does not hold.
func worthAgain[T any](a *app, get func(*metaCache) (kept[T], bool)) bool {
	if limited, _ := a.limited(); limited {
		return false
	}
	now := a.now()
	a.metaMu.Lock()
	defer a.metaMu.Unlock()
	k, ok := get(&a.meta)
	return ok && now.Sub(k.at) >= missAgain
}

func (c *metaCache) getVaults() (kept[[]vaultInfo], bool) {
	if c.vaults == nil {
		return kept[[]vaultInfo]{}, false
	}
	return *c.vaults, true
}

// vaultsHaving is the vault list, asked again once when a kept one has no vault has(…) accepts.
func (a *app) vaultsHaving(ctx context.Context, has func(vaultInfo) bool) ([]vaultInfo, error) {
	vaults, err := a.readVaults(ctx, false)
	if err != nil || slices.ContainsFunc(vaults, has) || !worthAgain(a, (*metaCache).getVaults) {
		return vaults, err
	}
	return a.readVaults(ctx, true)
}

// itemsHaving is a vault's item list, asked again once when a kept one has no item has(…) accepts.
func (a *app) itemsHaving(ctx context.Context, vaultID string, has func(itemInfo) bool) ([]itemInfo, error) {
	items, err := a.readItems(ctx, vaultID, false)
	get := func(c *metaCache) (kept[[]itemInfo], bool) { k, ok := c.items[vaultID]; return k, ok }
	if err != nil || slices.ContainsFunc(items, has) || !worthAgain(a, get) {
		return items, err
	}
	return a.readItems(ctx, vaultID, true)
}

// itemHaving is an item's structure, asked again once when a kept one has no field has(…) accepts.
func (a *app) itemHaving(ctx context.Context, vaultID, itemID string, has func(fieldInfo) bool) (itemDetail, error) {
	d, err := a.readItem(ctx, vaultID, itemID, false)
	key := vaultID + "/" + itemID
	get := func(c *metaCache) (kept[itemDetail], bool) { k, ok := c.details[key]; return k, ok }
	if err != nil || slices.ContainsFunc(d.Fields, has) || !worthAgain(a, get) {
		return d, err
	}
	return a.readItem(ctx, vaultID, itemID, true)
}

// nameKey is a name as lookups compare it (case and surrounding spaces aside), keyed with a secret of this process:
// itemFromSDK keeps it beside a field's or section's name, so a reference that names one whose name held a value
// (and is kept scrubbed) still finds it, and what is kept carries no form of that value anyone can read back.
func nameKey(name string) string {
	m := hmac.New(sha256.New, nameSecret[:])
	m.Write([]byte(strings.ToLower(strings.TrimSpace(name))))
	return hex.EncodeToString(m.Sum(nil)[:16])
}

var nameSecret = func() (k [32]byte) {
	if _, err := rand.Read(k[:]); err != nil {
		panic(err)
	}
	return k
}()
