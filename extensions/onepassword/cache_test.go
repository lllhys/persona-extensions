package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	op "github.com/1password/onepassword-sdk-go"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The metadata cache (cache.go, #488), counted at the fake 1Password: what reaches it and what does not.

// clocked is a connected app whose clock the test moves.
func clocked(t *testing.T) (*app, *fakeOP, func(time.Duration)) {
	t.Helper()
	a, f := connected(t)
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	return a, f, func(d time.Duration) { now = now.Add(d) }
}

func (f *fakeOP) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeOP) since(n int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls[n:]...)
}

func describe(t *testing.T, a *app, ref string) map[string]string {
	t.Helper()
	out, err := secretsCall(t, a, "describe", map[string]string{"reference": ref})
	if err != nil {
		t.Fatalf("describe %s: %v", ref, err)
	}
	return out.(map[string]string)
}

func callTool(t *testing.T, a *app, name, args string) string {
	t.Helper()
	text, err := a.callTool(context.Background(), name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s %s: %v", name, args, err)
	}
	return text
}

func TestRepeatedReadsWithinTheTTLAskOnce(t *testing.T) {
	a, f, wait := clocked(t)
	if got := describe(t, a, refByName); got["title"] != "GitHub token › credential" {
		t.Fatalf("describe: %v", got)
	}
	if got := f.since(0); strings.Join(got, ",") != "vaults,items "+vaultDev+",item "+vaultDev+"/itmgithub" {
		t.Fatalf("first describe asked %q", got)
	}
	n := f.count()
	wait(4 * time.Minute)
	describe(t, a, refByName)
	describe(t, a, "op://Dev/GitHub token/Backup/one-time password") // another field of the same item
	callTool(t, a, "onepassword_item", `{"vault":"Dev","item":"GitHub token"}`)
	if more := f.since(n); len(more) != 0 {
		t.Fatalf("within the TTL 1Password was asked %q", more)
	}

	// A search lists each vault's items once, then answers from what it kept.
	n = f.count()
	first := callTool(t, a, "onepassword_search", `{"query":"database"}`)
	if more := f.since(n); len(more) != 2 { // the two vaults not yet listed; Dev and the vault list are kept
		t.Fatalf("first search asked %q", more)
	}
	n = f.count()
	if again := callTool(t, a, "onepassword_search", `{"query":"database"}`); again != first {
		t.Fatalf("second search answered differently:\n%s\n%s", first, again)
	}
	callTool(t, a, "onepassword_vaults", `{}`)
	if more := f.since(n); len(more) != 0 {
		t.Fatalf("a repeated search asked %q", more)
	}

	// Past the TTL it asks again, once.
	wait(metaTTL)
	n = f.count()
	describe(t, a, refByName)
	if more := f.since(n); len(more) != 3 {
		t.Fatalf("after the TTL describe asked %q", more)
	}
	n = f.count()
	describe(t, a, refByName)
	if more := f.since(n); len(more) != 0 {
		t.Fatalf("again after refilling: %q", more)
	}
}

func TestTheCacheIsEmptiedByCheckAgainAndARestart(t *testing.T) {
	a, f, _ := clocked(t)
	describe(t, a, refByName)
	n := f.count()
	if _, err := a.page().Action(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{Contribution: uiConnection, Action: "refresh", Locale: "en"}}); err != nil {
		t.Fatal(err)
	}
	if more := f.since(n); strings.Join(more, ",") != "vaults" { // the page lists the vaults afresh
		t.Fatalf("Check again asked %q", more)
	}
	n = f.count()
	describe(t, a, refByName)
	if more := f.since(n); len(more) != 2 { // the vault list is fresh from the page; items and the item again
		t.Fatalf("after Check again describe asked %q", more)
	}

	// A start (the hub restarts the program with a new token, computer or setting) keeps nothing.
	n = f.count()
	if _, err := a.init(context.Background(), &ext.Init{InitializeParams: wire.InitializeParams{Secrets: map[string]string{tokenSecret: testToken}}}, nil); err != nil {
		t.Fatal(err)
	}
	describe(t, a, refByName)
	if more := f.since(n); len(more) != 3 {
		t.Fatalf("after a new start describe asked %q", more)
	}
}

func TestUnderTheRateLimitTheCacheAnswers(t *testing.T) {
	a, f, wait := clocked(t)
	describe(t, a, refByName)
	search := callTool(t, a, "onepassword_search", `{"query":"github"}`)
	wait(metaTTL + time.Minute) // all of it stale now

	// 1Password refuses for its limit: what was kept answers, however old. (By IDs, so the names can only come from
	// what was kept: without it, describe falls back to the reference's own words, "itmgithub › credential".)
	f.failAll = &op.RateLimitExceededError{}
	n := f.count()
	if got := describe(t, a, refByID); got["title"] != "GitHub token › credential" || got["detail"] != "Dev" {
		t.Fatalf("describe under the limit: %v", got)
	}
	if more := f.since(n); len(more) != 1 { // the one refused request that found the limit
		t.Fatalf("describe under the limit asked %q", more)
	}
	if st := a.status(); st["state"] != "rate_limited" {
		t.Fatalf("status: %v", st)
	}
	n = f.count()
	if again := callTool(t, a, "onepassword_search", `{"query":"github"}`); again != search {
		t.Fatalf("search under the limit:\n%s\n%s", search, again)
	}
	describe(t, a, refByName)
	if more := f.since(n); len(more) != 0 {
		t.Fatalf("within the limit's window 1Password was asked %q", more)
	}
	// Nothing kept for it (the Router's fields were never read): the limit is answered at once, without asking.
	if _, err := a.callTool(context.Background(), "onepassword_item", json.RawMessage(`{"vault":"`+vaultHome+`","item":"Router"}`)); err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("item under the limit: %v", err)
	}
	if more := f.since(n); len(more) != 0 {
		t.Fatalf("an uncached read under the limit asked %q", more)
	}
	// Check again under the limit keeps the cache and asks nothing.
	if _, err := a.page().Action(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{Contribution: uiConnection, Action: "refresh", Locale: "en"}}); err != nil {
		t.Fatal(err)
	}
	if more := f.since(n); len(more) != 0 {
		t.Fatalf("Check again under the limit asked %q", more)
	}
	// A value is never answered from the cache: resolve still asks.
	n = f.count()
	_, _ = secretsCall(t, a, "resolve", map[string]any{"reference": refByName})
	if more := f.since(n); len(more) != 1 || !strings.HasPrefix(more[0], "resolve ") {
		t.Fatalf("resolve under the limit asked %q", more)
	}

	// After the window it asks again.
	f.failAll = nil
	wait(rateWindow)
	n = f.count()
	describe(t, a, refByName)
	if more := f.since(n); len(more) != 3 {
		t.Fatalf("after the window describe asked %q", more)
	}
}

func TestAMissInAKeptListLooksOnceMore(t *testing.T) {
	a, f, wait := clocked(t)
	describe(t, a, refByName)
	// A name the kept item list lacks, asked again within missAgain: the miss stands, nothing asked.
	n := f.count()
	describe(t, a, refMissing)
	if more := f.since(n); len(more) != 0 {
		t.Fatalf("a fresh miss asked %q", more)
	}
	// Later (the item may have just been made): the vault's items are listed once more.
	wait(missAgain)
	n = f.count()
	describe(t, a, refMissing)
	if more := f.since(n); strings.Join(more, ",") != "items "+vaultDev {
		t.Fatalf("an older miss asked %q", more)
	}
}

func TestTheCacheKeepsNoValues(t *testing.T) {
	a, _, _ := clocked(t)
	for _, ref := range []string{refByName, refByID, "op://Dev/GitHub token/Backup/one-time password", "op://Dev/GitHub token/sec2/pin"} {
		describe(t, a, ref)
	}
	callTool(t, a, "onepassword_search", `{"query":"github"}`)
	callTool(t, a, "onepassword_item", `{"vault":"Dev","item":"GitHub token"}`)
	_, _ = secretsCall(t, a, "resolve", map[string]any{"reference": refByName})
	a.metaMu.Lock()
	kept := fmt.Sprintf("%#v %#v %#v", a.meta.vaults, a.meta.items, a.meta.details)
	a.metaMu.Unlock()
	if !strings.Contains(kept, "itmgithub") || !strings.Contains(kept, "one-time password") {
		t.Fatalf("nothing kept: %s", kept)
	}
	assertNoSecrets(t, "the cache", kept)
}

func TestTheComputersReadsAreKeptToo(t *testing.T) {
	cli := newFakeCLI()
	a, _ := onComputer(t, cli)
	if got := describe(t, a, "op://Dev/GitHub token/credential"); got["title"] != "GitHub token › credential" || got["detail"] != "Dev" {
		t.Fatalf("describe through op: %v", got)
	}
	n := len(cli.ran())
	if got := describe(t, a, "op://"+fixVault+"/"+fixItem+"/credential"); got["title"] != "GitHub token › credential" {
		t.Fatalf("describe by IDs from what was kept: %v", got)
	}
	callTool(t, a, "onepassword_item", `{"vault":"Dev","item":"GitHub token"}`)
	if more := cli.ran()[n:]; len(more) != 0 {
		t.Fatalf("op on the computer was asked again: %q", more)
	}
	_, _ = secretsCall(t, a, "resolve", map[string]any{"reference": "op://Dev/GitHub token/credential"})
	if more := cli.ran()[n:]; len(more) != 1 || !strings.HasPrefix(more[0], "read ") {
		t.Fatalf("resolve on the computer: %q", more)
	}
}

func TestARecheckRefusedForTheLimitKeepsWhatWasKept(t *testing.T) {
	a, f, wait := clocked(t)
	describe(t, a, refByName)
	wait(time.Minute) // fresh, but old enough for a miss to look again
	f.failAll = &op.RateLimitExceededError{}
	n := f.count()
	// A typo: Dev's items are asked again, 1Password refuses for its limit, and the kept list answers that very call
	// (the item is not in it), rather than the limit.
	_, err := a.callTool(context.Background(), "onepassword_item", json.RawMessage(`{"vault":"Dev","item":"GitHub tokn"}`))
	if err == nil || !strings.Contains(err.Error(), `no item "GitHub tokn"`) {
		t.Fatalf("the miss under the limit: %v", err)
	}
	if more := f.since(n); strings.Join(more, ",") != "items "+vaultDev {
		t.Fatalf("the miss asked %q", more)
	}
	// What was kept of Dev's items still answers: the limit's window asks nothing, and the list was not dropped.
	if text := callTool(t, a, "onepassword_item", `{"vault":"Dev","item":"GitHub token"}`); !strings.Contains(text, "credential") {
		t.Fatalf("item: %s", text)
	}
	if more := f.since(n); len(more) != 1 {
		t.Fatalf("under the limit 1Password was asked %q", more)
	}
}

func TestMissesLookAgainAtEachLevel(t *testing.T) {
	a, f, wait := clocked(t)
	describe(t, a, refByName)
	wait(missAgain)
	n := f.count()
	describe(t, a, "op://Nowhere/GitHub token/credential") // a vault the kept list lacks
	if more := f.since(n); strings.Join(more, ",") != "vaults" {
		t.Fatalf("a missing vault asked %q", more)
	}
	wait(missAgain)
	n = f.count()
	describe(t, a, "op://Dev/GitHub token/no such field") // a field the kept item lacks
	if more := f.since(n); strings.Join(more, ",") != "item "+vaultDev+"/itmgithub" {
		t.Fatalf("a missing field asked %q", more)
	}
	n = f.count()
	describe(t, a, "op://Dev/GitHub token/no such field") // again at once: the miss stands
	if more := f.since(n); len(more) != 0 {
		t.Fatalf("a fresh miss asked %q", more)
	}
}

func TestANameThatHeldAValueStillMatchesWithoutShowingIt(t *testing.T) {
	// The field is called "PIN <its own value>": kept as "PIN [hidden]", found by its key.
	a, f, wait := clocked(t)
	got := describe(t, a, "op://Dev/GitHub token/sec2/PIN "+secretValues[5])
	if got["title"] != "GitHub token › PIN "+hidden {
		t.Fatalf("describe: %v", got)
	}
	assertNoSecrets(t, "describe", got["title"]+got["detail"])
	// Found in what was kept, so no miss: later it is not looked up again.
	wait(missAgain)
	n := f.count()
	describe(t, a, "op://Dev/GitHub token/sec2/PIN "+secretValues[5])
	if more := f.since(n); len(more) != 0 {
		t.Fatalf("a kept field named with a value was looked up again: %q", more)
	}
}

func TestAReadAcrossAClearKeepsNothing(t *testing.T) {
	a, _, _ := clocked(t)
	_, err := cachedRead(a, false, func(c *metaCache) (kept[[]vaultInfo], bool) { return c.getVaults() },
		func(c *metaCache, k kept[[]vaultInfo]) { c.vaults = &k },
		func() ([]vaultInfo, error) {
			a.forgetMeta() // Check again, or a start, while 1Password answers
			return []vaultInfo{{ID: "v1", Title: "Old"}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	a.metaMu.Lock()
	defer a.metaMu.Unlock()
	if a.meta.vaults != nil {
		t.Fatalf("a read that began before the clear refilled it: %+v", a.meta.vaults)
	}
}

func TestTheLimitsWindow(t *testing.T) {
	a, f, wait := clocked(t)
	describe(t, a, refByName)
	wait(metaTTL)
	f.failAll = &op.RateLimitExceededError{}
	if vs, err := a.listVaults(context.Background()); err != nil || len(vs) != 3 { // refused: the kept list answers, the window opens
		t.Fatalf("refused refresh: %v %v", vs, err)
	}
	f.failAll = nil
	// The page under the limit says so above what was kept (asking nothing); it never says Connected then.
	n := f.count()
	text := viewText(a.view(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{Locale: "en"}}))
	if !strings.Contains(text, "1Password's rate limit reached") || strings.Contains(text, `"title":"Connected"`) || !strings.Contains(text, `"title":"Dev"`) {
		t.Fatalf("page under the limit: %s", text)
	}
	if more := f.since(n); len(more) != 0 {
		t.Fatalf("the page under the limit asked %q", more)
	}
	// Another error meanwhile does not end the window.
	a.record(context.DeadlineExceeded)
	wait(9 * time.Minute)
	n = f.count()
	_, _ = a.listVaults(context.Background())
	if more := f.since(n); len(more) != 0 {
		t.Fatalf("at 9 minutes the window asked %q", more)
	}
	// A request that succeeds (a resolve, which always asks) ends it.
	if _, err := secretsCall(t, a, "resolve", map[string]any{"reference": refByName}); err != nil {
		t.Fatal(err)
	}
	n = f.count()
	if _, err := a.listVaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	if more := f.since(n); strings.Join(more, ",") != "vaults" {
		t.Fatalf("after a success the vault list asked %q", more)
	}
}
