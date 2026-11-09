# Markets

Market prices in Persona behind pluggable sources (OKX's public cryptocurrency market, and A-share, Hong Kong and US stocks from Tencent's quote endpoints, all without an account): a watchlist on the main navigation, search, price and volume charts, and assistant tools.

- [Extension guide and configuration](../../docs/extensions/markets.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)

## Local source authoring profile (unreleased)

The TypeScript profile uses the official `sdk/ts` dependency, a default stdio
`Program`, and native JSX. It is an independent local artifact, not a version
bump or a market release. The released Go manifest, configuration, grants,
`watchlist` navigation ID and `card` overview ID remain unchanged.

The [hosted rebuild profile](source-rebuild.json) registers the unchanged reference bundle in
`source-reference/bundle.mjs`. The shared CI-only gate introduced by merged #167 is now in
`main`: both isolated source and independently extracted-authoring rebuilds must match that
reference byte-for-byte on the current PR head. Local synthetic tests do not claim a hosted
result or installed-hub acceptance and do not authorize HOME substitution.

From this extension directory:

```sh
npm ci --registry=https://registry.npmjs.org --ignore-scripts
npm run typecheck
npm test
npm run pack:source
```

The package is `build/markets-source.tar.gz`. The official installable manifest and
`build/markets.mjs` are at its root; `runtime.source.entry` points to the included
`extensions/markets/src/index.ts`. Complete markets authoring lives at
`extensions/markets/`, alongside the complete unchanged official SDK authoring
closure at `sdk/ts/` (exports, compatibility/exact-JSON modules, fixtures, tests,
checker scripts and standalone Go oracle tooling). The existing `file:../../sdk/ts` dependency
and lock remain valid after standalone extraction. Dependencies and generated
SDK outputs are excluded; the original markets Go implementation stays in the
repository. Markets' source, tests, provider fixtures and scripts are included.
Rebuilding this source package does not require a checkout outside the extracted
directory. Import-free
bundle execution and standalone authoring rebuild are separate verification gates.
The archive is deterministic POSIX ustar/gzip written with Node's standard library;
it needs no GNU-only tar options. Its gzip stream uses fixed stored DEFLATE blocks:
compressed zlib output differed between Node and Bun even when the ustar payload
was identical, so runtime-specific compression cannot establish archive byte parity.
The larger standard gzip stream keeps rebuilds byte-identical without changing any
runtime or response limit. macOS/BSD tar execution is not verified here.

Its package manifest selects Deno and pins the final `build/markets.mjs` bytes by
SHA-256. The optional `Program.manifest` is deliberately absent: the official host
validates the package manifest and final bundle digest; SDK `manifest` answers
`unknown_method`. A bundle cannot truthfully embed its own final digest. No fake,
zero or pre-build digest, and no Go runtime metadata, is advertised by the Program.
The builder verifies an import-free bundle and prints archive and bundle hashes.
No SDK copy is edited; dependencies and the lockfile are extension-local. Lockfile
URLs must use the public npm registry;
local registry settings must not leak into a public source artifact. The builder
rejects unapproved origins, URL credentials and alternate SDK links before
packaging. Tests exercise this refusal without printing rejected URLs. These
commands do not sign, upload, modify a market index or switch the default package.

Run the provenance/privacy/freeze gate with the private terms file supplied only as
an argument (it is not bundled):

```sh
python3 -I scripts/verify-source.py --terms <private-PCRE-terms-file>
```

It scans authoring files, stage and archive, checks every nonempty PCRE rule
(including comment-like lines), and fails on grep exit 2 rather than treating an
invalid rule as a clean scan. Dependency origins are checked in canonical, staged,
archived and installed hidden locks. `--extracted <unpacked-root>` additionally
checks cold-install locks and byte-identical standalone authoring rebuild.

One shared service powers the connector's three read tools, native watchlist
sessions, overview snapshots and the minute-based price-alert loop. Tests cover
GBK quote decoding, stock-code normalization, exchange suffixes, timezone/DST
conversion, forward-adjusted bars, cumulative intraday volumes, source-group
sorting, bounded caches and queues, cancellation, partial failures, quotas,
state rollback and alert retention. Both production and development JSX modes
have positive and negative typechecks. The separately bundled Program is tested
for host-ticket gating, saved-event replay, stale revisions, remount generations,
inline detail/back navigation and stable cached effects on resume.

The main page mounts through `ui.page.mount`. Direct SDK `ui.render`/`ui.action`
for that component page intentionally answer `unsupported`; compatibility HTTP
render/action routes are supplied by the manager for the **same** contribution.
No duplicate navigation pages are added. Cold bundle tests explicitly select the
SDK's top-level `transport: "snapshot"`: finite `useLoadEffect` initial and
navigation reads settle before the request is suspended. Long-running subscriptions
and intervals stay in visibility-managed `useEffect`/`useInterval`. Native
`Renderer.resume()` always consumes a fresh callback revision; it is distinct from
the manager's wire version, which coalesces unchanged views. Quote/error
fingerprints compare keys, never asynchronous Map insertion order: otherwise an
equal resume read can unexpectedly consume another revision and stale the callback
just returned to the host. Synthetic tests reproduce that extra-render failure
and verify unchanged cached reads retain the returned revision.

The connector is an SDK-branded lazy namespace using `withExactParams`, preserving
the original raw JSON through dispatch. Complete Go-shaped envelope and argument
schemas retain ordered duplicates, scalar/slice null rules, folded field names,
and earlier type errors. Exact signed int64 tokens are checked before the original
business clamps. Fraction/exponent tokens, including negative and mathematically
integral forms, remain invalid; legal int64 extremes retain the default limit.
The unchanged Go implementation is tested through an overlay oracle. No transport
is inferred from `ui_version`, and no local navigation shim is added. These local
SDK tests are not an actual manager HTTP or installed-hub acceptance result.
Visible native roots use SDK lifecycle intervals (30 seconds for active markets,
five minutes for idle markets), aborting reads and clearing timers while hidden.
Native snapshots omit legacy `refresh_after`; legacy card/snapshot consumers keep
their original timing hint. Native UI versions 1, 3 and 5 retain
metric/row, chart/segmented and search branches respectively. The stateless
`snapshotPage` export reuses the same JSX; the original `card` contribution uses
it. The charts retain the existing native single-price-line and separate-volume
forms, with no custom palette or new visual style. `markets_candles` also exposes
the underlying OHLCV values. A disabled overview card still answers `unsupported`.
Provider success envelopes are validated through their consumed record structures
before cache admission. Failed refreshes preserve an existing good cached response.
Body limits also apply on both cache-hit paths. Raw state and Tencent JSON syntax
are validated before retaining integer lexemes; malformed JSON is never repaired.
OKX integer timestamp strings are parsed across the full signed int64 range and
kept exact (BigInt where needed). Gregorian formatting uses exact milliseconds;
Intl supplies zone offsets, using TZif's recurring future rules outside Date's
range and historical offsets before its first transition. Synthetic oracle checks
run the original Go ticker/candle/tool implementation via an overlay without
changing it (`tests/go-boundaries.txt` is the overlay test input). They cover UTC,
New York DST, Hong Kong and Shanghai at extreme timestamps; differing host tzdata
versions and other zones are not a claim of universal live parity. `Local` and
missing old-hub timezone values retain the guest's local fallback. Calendar and
wall-clock validation rejects nonexistent dates/times. Stored alert timestamps are
validated as RFC3339 calendars before conversion, storage and load; Date.parse is
not used as a validator. Local raw-JSON decoding retains field occurrences until
the consumed Go-shaped schemas have checked them, so an earlier integer or string
type error cannot disappear behind a valid duplicate. Struct fields merge across
valid duplicates, map elements replace rather than merge, and ASCII schema field
matching preserves Go's long-s/Kelvin simple-fold equivalents. All known OKX ticker
and envelope fields are checked before cache admission; its raw-message data field
uses only the final occurrence, as in the original Go envelope.

Alert input and provider candle/quote strings use the original Go floating-point
grammar: decimal digit separators and hexadecimal floats with a binary exponent
remain legal; JavaScript-only hexadecimal integers, binary and octal prefixes do
not. Hexadecimal conversion bounds its mantissa work and uses nearest-even rounding,
including subnormal and overflow boundaries. The local parser control explicitly
checks TypeScript parseDiagnostics and Node `--check`, with malformed-source negative
controls. It is not a replacement for the official SDK authoring checker, included
unchanged in the complete authoring closure:

```sh
npm --prefix ../../sdk/ts ci --registry=https://registry.npmjs.org --ignore-scripts
node ../../sdk/ts/scripts/check-source-bundle.mjs --root "$PWD/build/package" \
  --bundle build/markets.mjs --manifest persona-extension.json --probe-trusted \
  --timeout-ms 2000
```

The source PR targets real merged `main`. Official SDK delivery #165 followed readiness
#1570; the subsequent delivery #173 supplies the already merged exact-initialization SDK
through the same unchanged upstream helper. Both deliveries passed their own current-head
hosted checks before merging. The copied SDK is not handpatched, and Markets' executable
bundle remains byte-identical after adopting it. The formerly failing standalone Go-oracle
layout was fixed separately by #166; the old failure remains recorded, not waived.
Bun verification invokes its actual `bun test` runner, not Bun's script-mode `--test`.

Retarget every open stacked extension PR to merged `main` before deleting its SDK base
branch. GitHub closes a PR when its base disappears; preserve the existing issue/PR rather
than opening a replacement to hide that workflow error.

The original Go overlay oracle is repository-only (the authoring archive intentionally
omits Go). Supply the real cached declared toolchain rather than editing go.mod or
rewriting WaitGroup.Go to accommodate an older compiler:

```sh
python3 -I scripts/verify-go.py --go <cached-Go-1.27.1-bin-go>
```

The runner selects the supplied binary's matching GOROOT, disables toolchain and
module downloads, records uncached race results, and compares original Go/mod hashes.
Only the original root package is tested; generated build overlays are not Go
packages. Test inputs are `tests/go-boundaries.txt` and `tests/round3-go.txt`.
The retained numeric-wire and cold-snapshot tests apply to the byte-identical canonical
SDK; actual manager and installed-hub acceptance remain separate evidence. Source-loading
and SIGTERM process-lifetime acceptance remain applicable migration gates. Nothing here
permits production installation or treating an authoring VM as production isolation.

### Persistent-state upgrade boundary

The source profile keeps watchlist and alerts in the hub's encrypted connector
secret store as one versioned UTF-8/base64 blob. It never reads or writes guest
files. Load failures and corrupt blobs fail initialization rather than replacing
existing state with defaults. Failed user changes roll back; triggered alerts
remain in memory and notify the Inbox even if saving fails, matching the Go
behavior (a restart can repeat an alert after such a failure).

Existing Go installations have `watchlist.json` and `alerts.json`. They cannot be
silently imported by the confined guest. An explicit offline converter is
provided, to be run against reviewed copies before any upgrade:

```sh
node scripts/convert-state.mjs <watchlist.json> <alerts.json> <new-output-file>
```

It retains the originals, refuses an existing output, writes a new mode-0600
host-call payload and preserves pending and triggered alert fields. Unknown
formats and over-quota state require operator review, not truncation. Its output
contains user state: keep it private and import it through the authorized host's
encrypted connector storage. The converter does **not** install the extension or
perform that host import. Importing real existing state and verifying a restart
are outstanding upgrade acceptance steps.

Process restart and uninstall/reinstall are different lifecycle checks. The existing
manager cleanup can delete connector secrets even with `keep_data=1`, so this source's
sealed watchlist/alert envelope is not retained by that uninstall. This is a known main
issue, tracked in nwverse/persona#1187; per the maintainer's decision it does not block this
source-profile migration. The failure stays recorded separately from successful process
restart. Do not fix it in this lane, reseed data, weaken credential teardown or call it a
passed retention check.

### Acceptance and publishing boundary

Synthetic SDK/bundle tests are not evidence of an actual test-hub installation,
manager HTTP compatibility, or simulator screenshots. Those require a dedicated,
independent test hub, including alert restart, visibility/cancellation and native
light/dark presentation checks. The coordinating session installs one extension
instance at a time and owns that acceptance and merge. This draft source profile
does not claim installed-hub or production approval.

Switching default packages or publishing to GHCR waits for the production
isolation gate and a separate release decision. The existing signed catalog,
release version and default Go package stay unchanged. Future catalog generation
and signing use the repository's normal publication tools; this unsigned source
archive is not a catalog release.
