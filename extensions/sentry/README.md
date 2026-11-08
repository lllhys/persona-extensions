# Sentry

Sends Persona errors and crashes to your Sentry project after the hub scrubs them;
nothing is sent until you provide the DSN.

- [Extension guide and configuration](../../docs/extensions/sentry.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Released manifest](persona-extension.json)

## Unreleased source authoring profile

The released Go files, manifest, version, contribution IDs, configuration, original
`*.sentry.io` network grant and catalog are unchanged. This is an additional source
profile, not a release or production activation. No destination setting, grant,
page, client JavaScript/HTML, signing change or automatic upgrade is added.

Run these commands from this extension directory:

```sh
npm ci --registry=https://registry.npmjs.org --ignore-scripts --no-audit --no-fund
npm run typecheck
npm test
npm run test:bun
npm run build:source
python3 -I scripts/test-go-reference.py
python3 -I scripts/test-authoring-rebuild.py
node scripts/check-blockers.mjs
```

`python3 -I scripts/ci-source.py` runs the local source checks with finite timeouts,
explicit verdicts, the public registry and disabled installation scripts. Bun is
required, not a green skip. Node 22 and Node 24 are supported. Canonical and hidden
npm locks are checked for public origins, integrity and credentials. This local
source gate is separate from the repository's required hosted CI.

The SDK dependency is the real `file:../../sdk/ts` module. `sdk-provenance.json`
pins all 74 official SDK payload files at dependency commit
`fe6e9d5eb4bffe40d50f604e93990615f3043e0f`, on
`fix/158-sdk-source-authoring-delivery` ([draft dependency PR](https://github.com/nwverse/persona-extensions/pull/165)).
The copied SDK is never hand-edited. That dependency is provisional, not released:
its official upstream synchronization, complete CI and merge remain prerequisites.

The build produces two deterministic Python-stdlib tar/gzip archives:

- `.source-dist/sentry-source.tar.gz`: contained import-free Deno bundle, source
  manifests and extension authoring inputs, rooted at `sentry` for installation.
- `.source-dist/sentry-authoring.tar.gz`: standalone repository layout rooted at
  `sentry-authoring`, containing `extensions/sentry`, the **complete** unchanged
  official TypeScript SDK module closure and the original Go/Go-SDK oracle inputs.

Extract the authoring archive and build in `extensions/sentry`; its relative SDK
link remains valid. The cold rebuild test supplies no external SDK and compares
the rebuilt bundle and both archives byte-for-byte. SDK tests, fixtures, exports,
configuration and authoring checker files are retained, not a hand-picked runtime
subset. Dependencies and build output are never committed.

The external source manifests contain the digest of the final bundle. Optional
`Program.manifest` is absent: SDK `manifest` RPC therefore says `unknown_method`
("it is in the package"). No self-referential digest, placeholder hash or embedded
Go-runtime claim is substituted. `RELEASED-MANIFEST.reference.json` preserves the
original manifest solely for authoring and oracle tests.

## Behavior and evidence boundaries

The only shipped namespace is `error_reports`. The default Program retains the
service, DSN secret, HTTP opt-in, event IDs/conversion, log filtering, sampling
clamps, 200-entry queue, 60/minute rate, Sentry limits and sent/dropped accounting.
Reports are already scrubbed by the host; the extension does **not** redact report
text itself. Only declared report fields enter an envelope. SDK IP inference is
`never`; arbitrary user, host and IP fields are not forwarded. The source logger
emits only a neutral successful receipt, never report data, destinations or keys.

The branded lazy namespace handler uses official `withExactParams` for requests.
The typed decoder preserves Go scalar/null/pointer rules, all struct field types,
case-folded names, ordered duplicates, reused slice elements, string maps and
exact signed int64 tokens. It does not add required fields, contribution-ID checks
or another ingress/batch cap absent from Go. Event integers are serialized as
JSON integers, not rounded Numbers or quoted decimal strings. The unchanged Go
race oracle and 95 typed differential cases cover legal large integers, negative
fraction/exponent spellings, null/pointer defaults, duplicates and original clamps.
These request/decoder checks are **not** notification parity.

Failed, offline, timed-out and non-2xx deliveries are dropped, never retried or
persisted. Local `flush` and idempotent `stop` are embedding/test controls, not
invented host RPCs. A stop deadline or external abort fences non-cancellable digest
work and accounts accepted pending reports once. Late results cannot fetch, log
or change counts. The SDK has no Program shutdown hook; real process/SIGTERM
queue flushing is not claimed.

Sentry never had an extension page. Native keyed snapshots, saved callbacks,
host tickets, replay/stale actions, visibility, cancellation and cold/navigation
finite loads are exercised only by synthetic SDK fixtures. Finite reads use
`useLoadEffect`; subscriptions use visibility-managed `useEffect`. Cold fixtures
explicitly set top-level `transport: "snapshot"`, never infer it from `ui_version`.
They are not management screenshots, installation or host HTTP-bridge acceptance.

`python3 -I scripts/test-broker-reference.py <public Persona checkout>` exercises
the unchanged official source-manifest validator and runtime-fetch broker using
synthetic DNS/dial fixtures. The original wildcard admits one declared subdomain
and rejects the bare suffix, lookalikes and unrelated self-hosted hosts. No grant
is added to make a fixture green. This proves synthetic broker policy only, not
real Sentry connectivity, installed Deno isolation or developer-hub acceptance.
Self-hosted destinations outside the original grant remain denied, as before.

## Failing and unverified gates

- **SDK notification exact parameters: FAIL.** Go delivers `error_reports.deliver`
  as a notification. Official `withExactParams` currently provides raw tokens only
  for requests. Notifications erase fractional/exponent spellings and duplicate
  fields; unsafe integers fail closed rather than being forwarded rounded.
  The full legal Go range is therefore not yet supported on the real delivery path.
- **SDK initialization duplicates: FAIL.** Invalid earlier configuration values
  followed by valid duplicate keys are erased before `init`. The local exact
  configuration decoder cannot recover metadata the SDK never supplies.
- `node scripts/check-blockers.mjs` (or `--bun`) runs those actual counterexamples
  independently and returns **FAIL/BLOCKED**, not a skip or full-parity pass.
- **Noncanonical DSN paths: FAIL.** For `https://<key>@errors.example.com/prefix/../42`,
  the unchanged Go oracle preserves `/prefix/../api/42/envelope/`, whereas the
  reviewed source URL parser normalizes it to `/api/42/envelope/`. Do not claim
  complete DSN URL normalization/escaping parity until this delivery path is fixed.
  Canonical DSNs have local regressions. Signed Go rate-limit duration wrapping
  and integer-only Retry-After behavior are retained and checked at millisecond precision.
- SDK dependency PR CI/layout currently fails on the official
  `sdk/ts/scripts/int64-oracle.go` layout rule (tracked separately). The SDK and
  repository guard are not edited or skipped here.
- Required hosted CI, installed-hub delivery/scrubbing/isolation, management-page
  interactions and inspected screenshots remain coordinating-agent gates.

All new accounts and reports are synthetic. Original Go and official SDK inputs
are preserved, including their existing illustrative URL/key/email fixtures;
preserving those baselines is not proof of zero scanner matches or actual report
scrubbing. No real account, conversation, credential, production hub or simulator
was used for these local checks. Production isolation, publication and fresh
marketplace signatures require separate later decisions.
