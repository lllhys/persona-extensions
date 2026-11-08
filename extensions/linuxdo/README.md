# LINUX DO

Read public LINUX DO topics in Persona, without an account: interest-filtered lists, an in-app topic reader and assistant tools.

- [Extension guide and configuration](../../docs/extensions/linuxdo.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)

## Local source profile

The TypeScript implementation is an explicit preparation profile, not the released default package.
Run these commands from this extension's directory in an SDK checkout:

```sh
npm ci --prefix ../../sdk/ts --ignore-scripts --registry=https://registry.npmjs.org
npm ci --ignore-scripts --registry=https://registry.npmjs.org
npm run typecheck
npm test
npm run pack:source
go test -race -count=1 -timeout=90s ./...
```

Packaging writes only to the ignored local build directory: a Deno manifest, an import-free ESM bundle with a
verified SHA-256, and a source archive. It does not change the released Go manifest, version, market indexes or
signatures. The canonical extension-local lockfile uses the public package registry and a relative dependency on
its repository's official TypeScript SDK. The install root contains a root source manifest, `extensions/linuxdo/`
with its complete authoring files and bundle, and unchanged official SDK authoring files under `sdk/ts`.
The package and lock retain `file:../../sdk/ts`; that path resolves inside the same install root. After extracting
into a new empty directory, run the commands above from `extensions/linuxdo/`: the SDK's own locked authoring
install supplies the official static checker's TypeScript dependency. No copied SDK files are hand-edited.
The checker uses the official SDK's static mode, never evaluates guest initialization and does not claim a security
boundary. Evaluating the import-free bundle on the confined host needs no authoring dependencies.
Packaging uses only the common GNU/BSD tar options `-czf` and `-C`, not GNU-only sorting/mtime flags. Linux packaging
is tested; a macOS execution is not claimed. Tar timestamps mean archive hashes need not repeat across builds.

The package manifest contains the actual final bundle SHA-256. RPC `manifest` instead reports the same source
mode/entry/bundle and all existing identity/config/grants/contributions (without market-only `tags`), omitting only
`runtime.source.sha256`: embedding that final hash in the hashed bundle is self-referential. It never reports a Go
command or invents a placeholder hash. The official host validates the package's hash before evaluation and mounts
only the bundle into the guest; it does not provide a trusted package-manifest injection API. RPC metadata therefore
is not an independently installable manifest, and exact full-manifest RPC equality remains an upstream-contract
question. Both metadata paths are relative to the same install root: `extensions/linuxdo/src/index.ts` and
`extensions/linuxdo/build/linuxdo.mjs`. Rebuilding an extracted authoring package independently computes and
verifies the final hash; it never embeds a self-hash in the Program.

The default Program shares one service among its connector, both tools and all page sessions. It retains all eight
configuration keys, RSS lists, Discourse first-page parsing, interest filtering, reply ranking, cache TTLs, global
throttling, request cancellation, rate-limit pauses, stale-data fallback, timezone precedence and text budgets.
The original `hot` contribution stays a native component page. Old HTTP clients must use the manager's owner-bound
legacy bridge for that same contribution; direct SDK `ui.render` and `ui.action` returning `unsupported` is expected.
No alternate navigation page is registered. Topic navigation remains inline, with a back button, on every UI version.
A pure snapshot helper exists for tests and does not claim to be a separate system-surface contribution.

Additional resource safeguards include a 32 MiB response-byte-accounted parsed cache (200 entries), bounded streaming
reads, abortable queue waits, concurrent-read coalescing, stable duplicate keys and rejection of malformed data before
cache replacement. Cancellation starts stream cleanup without awaiting a tee branch's possibly unbounded cancel
promise: awaiting it can deadlock the whole request queue. Raw topic-response integer fields, raw saved-state IDs
and RSS counters retain signed-64-bit precision; large values use exact decimal text. RPC tools opt into the official
SDK's raw-parameter API, preserving integer lexemes before the original int64 validation and business clamps.
Numeric IDs, string IDs, links and raw state/HTTP fields retain the original signed-64-bit contract without Number
rounding. Normal cache hits retain the Go
connector's last-sync/recovery behavior; the reading's timestamp, live flag and stale
reason independently describe actual data freshness. Page read-time text now uses that data timestamp rather than
claiming that a cache hit is a new network reading.

Tests use synthetic public-content fixtures and separately bundled SDK/Program instances. They cover native hooks,
loading and cancellation, event tickets/replay/stale generations, resume stability, shared services, UI 1/2/3/5
navigation, three-language resources, positive/negative production/development JSX types, and local source-artifact
checks. Regression tests include oversized/unreadable/stalled 429 responses, cancellation after a pending cache hit,
and integer field lexemes (decimal/exponent numbers never replace good topic data). Lock URL provenance and
credential checks run before packaging and on the packaged lock; those checks do not replace the private terms scan.
This extension has no account secrets, persisted business state, background reminders or companion process;
none are introduced or removed by the migration.

### Second-round local fixes and retained boundaries

- URL segments and cooked HTML use bounded forward scans/closing-tag indexes, including unmatched script, onebox
  and pre tags. RSS HTML stripping and count matching avoid unanchored backtracking too.
- Raw state/HTTP JSON syntax is validated before lossless token parsing. Decimal/exponent state IDs are rejected,
  not rounded; malformed JSON is never rewritten into valid JSON. Full int64 raw integer ranges remain supported.
- Topic/post times require an RFC3339 timestamp and a valid Gregorian date. Bad times keep the good body and data
  timestamp, mark it stale and preserve the last successful sync. RSS dates retain best-effort RFC1123Z handling.
  The original JSON time decoder accepts only four-digit years: years 0000, 0099 and 9999 remain formatted correctly;
  year 10000 and arbitrarily huge years are rejected as in Go, not replaced with epoch zero. The standalone formatter
  explicitly rejects a value outside Date's range instead of recursively retrying it in UTC.
- `Local` resolves through Intl's local zone. Missing config/hub/request zones retain local fallback; explicit config
  still wins over the request zone. No environment or timezone files are read by the guest.
- Missing/private 403/404 topics retain Go's bounded 8 MiB error-body read, then report gone even for oversized
  bodies. The challenge prefix still takes precedence. Cached bodies and connector health remain unchanged for
  gone; a body read error before the cap still fails, and challenges still use the original stale fallback.
- Lists use bounded native pagination without dropping the 271st RSS item. Topic/back and saved-state navigation
  preserve the list page; list/filter changes reset it. Frame and per-item text budgets stay unchanged.
- Topic times distinguish Go's year-0001 zero instant from one nanosecond later, even when their millisecond
  projections are identical. The Unix epoch remains visible.
- The source profile delegates ESM grammar/import/default validation to official SDK checker A in static mode,
  including top-level return/break, duplicate constants and strict-module with statements. Guest code is not run.
- Out-of-int64 Retry-After uses the default pause. In-range multiplication retains Go's signed-duration wraparound
  and one-minute floor, with a finite millisecond deadline. RSS counts retain exact int64 values and Go Atoi's
  saturated result for positive overflow. These inherited numeric behaviors are not new migration regressions.

Run the original decoder/formatter/duration controls with an installed Go 1.27.1 compiler and matching GOROOT
(no downloads). `scripts/original-round4.mjs` requires absolute `LINUXDO_GO` and `GOROOT` paths and runs the
unchanged second/third-round oracles plus the zero-time, oversized-topic, RSS and raw-tool controls.
These Go overlays require the original Go checkout; the source archive contains only TypeScript authoring files
and the unchanged relative SDK dependency. They are separate from the authoring rebuild:

```sh
node scripts/original-round4.mjs
```

The [registered CI profile](source-rebuild.json) and committed `source-reference/bundle.mjs` let a fresh
GitHub-hosted runner rebuild isolated source, then the extracted standalone archive, and compare both bundles
byte-for-byte. A successful hosted source-rebuild job is the maintainer-approved fresh-HOME proof and is required
by `CI required`. It is not yet claimed passed here. A local fresh-HOME rebuild is not a merge requirement;
local HOME/permission restrictions are unchanged and are never bypassed through config-only mode, peers or wrappers.

The original gone-cache retention, null-post handling, hidden-first-post selection, empty-feed behavior for no valid
links, and title double decoding are inherited and unchanged. They are not reported as new migration bugs or removed
for a green suite. The default manifest, version, market indexes and signatures remain untouched.

### Exact JSON tool contract (#1201)

The source profile explicitly wraps its connector with the official SDK's `withExactParams` API. It rejects raw
`topic`/`replies` values `1.0`, `1e0` and `1.0000000000000001` before fetching, and accepts legal integer IDs
`9007199254740993` and `9223372036854775807` without Number rounding. Existing string IDs and links still work.
The old tool types stay unchanged; no copied SDK code is hand-edited and JSON.parse is not globally replaced.

Go's `limit` scalar preserves a previous value across null, while `replies` is a pointer that resets to its default
on null. Decode ordered raw occurrences accordingly; early typed errors remain errors. Topic is RawMessage, so
its last folded field wins. Check signed range before applying the original limit/reply clamps or converting to
Number; keep numeric topic token spelling, including the original rejection of `-0`.

The six lexical counterexamples and both large IDs run in every full suite and now pass. Fourteen additional
controls share inputs with the unchanged real Go tool handler. Local response/state integer tests still do not
substitute for raw RPC dispatch. Run the specific gate after syncing and rebuilding the official SDK:

```sh
node --test --test-name-pattern='upstream #1201' build/tests/program.test.mjs
```

### Cold legacy HTTP contract (#1184)

The existing `hot` native contribution uses the manager's owner-bound legacy bridge. Its one finite data load
uses the official `useLoadEffect` hook; the manager explicitly supplies snapshot transport rather than guessing
from UI version. With a synthetic 200 ms read, three mount/resume → snapshot → suspend turns show content with
one request, zero canceled requests and no active work. This counterexample remains in every full suite.
Native open-session behavior is tested separately; neither substitutes for a real installed package.

Installed-source development-hub verification now returns real content through the broker's normal HTTP/2
transport: the unchanged package's first Hot HTTP request returned 200 in 1.026 seconds with 27 topic rows.
Light/dark screenshots at wide/narrow sizes were inspected and contain real topics, with content ready in
308–468 ms on warm native opens. A real Latest callback returned an accepted receipt and a positive topic list;
return to Hot succeeded. Opening a topic produced its native opening-body nodes in 2.801 seconds, and its back
callback returned to the list. Browser errors were zero.

Earlier 502 readiness-timeout and upstream challenge failures remain retained evidence, not overwritten passes.
The broker-only fix did not change the source package, 10-second page ceiling, inherited 20-second network budget,
or sandbox permissions, and no host-downloaded feed was substituted. This development verification is neither
production resource-isolation acceptance nor an official default-package/catalog/signing release. Fresh-HOME
rebuild remains a separate, unverified gate where changing HOME has not been authorized.

Run the bundled counterexample and actual manager overlay against the matching official SDK/runtime:

```sh
node --test --test-name-pattern='upstream #1184' build/tests/program.test.mjs
node scripts/upstream-manager.mjs <absolute-main-checkout-path>
```

The overlay uses the actual source Program with a synthetic delayed transport, writes only local ignored test files,
and never starts a production hub. Both gates must pass before HTTP parity can be claimed. Native session tests are
controls, not a substitute for this gate.

These tests are not evidence of installation, HTTP fallback, screenshots or production isolation on a real hub.
An independent test hub must accept the source profile before that stage is complete. Switching the default source
package and marketplace indexes is a later release gate, after production isolation passes. Both markets must then
be signed with the new market key; verifying the existing indexes does not verify a new source release.
