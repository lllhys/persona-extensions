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

Run these commands from this extension directory after installing the official SDK
and extension dependencies:

```sh
npm ci --registry=https://registry.npmjs.org --ignore-scripts --no-audit --no-fund
npm run typecheck
npm test
npm run test:bun
npm run build:source
python3 -I scripts/test-go-reference.py
python3 -I scripts/test-broker-reference.py <public Persona checkout>
```

Node 22, Node 24 and Bun are supported. Canonical and archived npm locks are
checked for public registry origins, integrity, credentials and the unchanged
`file:../../sdk/ts` SDK link. The copied official SDK is never edited here.
`source-rebuild.json` registers the profile with the repository's hosted clean
runner check, which rebuilds the committed `source-reference/bundle.mjs` both from
source and from the extracted complete authoring archive. Local builds are not a
substitute for that hosted proof.

The build produces:

- `build/package/`: a complete source-install layout containing the outer source
  manifest, import-free Deno bundle, extension authoring inputs, the unchanged
  official TypeScript SDK and the original Go/Go-SDK oracle inputs.
- `build/sentry-source.tar.gz`: the same complete layout as a deterministic
  authoring archive. Build again from `extensions/sentry` after extraction.

The external source manifest contains the digest of the final bundle. Optional
`Program.manifest` is absent: SDK `manifest` RPC therefore says `unknown_method`
("it is in the package"). No self-referential digest, placeholder hash or embedded
Go-runtime claim is substituted. The released manifest is read from the extension
directory for differential tests; the archive does not replace it.

## Behavior and evidence boundaries

The only shipped namespace is `error_reports`. The default Program retains the
service, DSN secret, HTTP opt-in, event IDs/conversion, log filtering, sampling
clamps, 200-entry queue, 60/minute rate, Sentry limits and sent/dropped accounting.
Reports are already scrubbed by the host; the extension does **not** redact report
text itself. Only declared report fields enter an envelope. SDK IP inference is
`never`; arbitrary user, host and IP fields are not forwarded. The source logger
emits only a neutral successful receipt, never report data, destinations or keys.

The lazy branded namespace handler uses official `withExactParams` for requests
and notifications. `withExactInit` supplies exact configuration tokens for actual
initialization RPCs. Direct/manual authoring initializer calls have no raw frame
and use the ordinary parsed configuration, never a fabricated raw string. The
typed decoder preserves Go scalar/null/pointer rules, struct field types,
case-folded names, ordered duplicates, reused slice elements, string maps and
exact signed int64 tokens. Event integers are serialized as JSON integers, not
rounded Numbers or quoted decimal strings.

Failed, offline, timed-out and non-2xx deliveries are dropped, never retried or
persisted. Local `flush` and idempotent `stop` are embedding/test controls, not
invented host RPCs. A stop deadline or external abort fences non-cancellable digest
work and accounts accepted pending reports once. Late results cannot fetch, log
or change counts. The SDK has no Program shutdown hook; real process/SIGTERM
queue flushing is not claimed.

Sentry never had an extension page. Native keyed snapshots, saved callbacks,
host tickets, replay/stale actions, visibility, cancellation and cold/navigation
finite loads are exercised only by synthetic SDK fixtures. They are not management
screenshots, installation or host HTTP-bridge acceptance. Do not add a diagnostic
page to a headless extension for acceptance.

`python3 -I scripts/test-broker-reference.py <public Persona checkout>` exercises
the unchanged official source-manifest validator and runtime-fetch broker using
synthetic DNS/dial fixtures. The original wildcard admits one declared subdomain
and rejects the bare suffix, lookalikes and unrelated self-hosted hosts. No grant
or configuration-based destination is added to make a fixture green. This proves
synthetic broker policy only, not real Sentry connectivity or installed-hub
acceptance. Self-hosted destinations outside the original grant remain denied.

## Acceptance gates

- `scripts/check-blockers.mjs` runs the actual notification/initialization and DSN
  builder counterexamples as part of `npm test` and `npm run test:bun`; it is not
  a skipped test. Current source requires the official SDK with notification exact
  parameters, not an SDK patched in this repository.
- Noncanonical DSN builder output preserves Go's decoded path without resolving
  dot segments. Trusted `Request`/URL processing can still canonicalize that path
  on the wire; builder parity is not claimed as complete wire-path parity.
- Hosted ordinary CI, isolated source/archive rebuilding, required trusted hosted
  privacy verification, installed synthetic delivery/scrubbing and management-page
  acceptance remain explicit gates. Source publication and activation remain a
  separate later phase.

All accounts and reports are synthetic. Original Go and official SDK inputs retain
their existing illustrative URL/key fixtures; that is not proof of actual report
scrubbing or a replacement for the hosted private-policy scan. No real account,
conversation, credential, production hub or simulator is required for these tests.
