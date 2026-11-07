# Showcase

Local-only examples of every extension view component and state, widgets and shortcuts, with shared view snapshots for client screenshot alignment.

- [Component, compatibility and snapshot guide](../../docs/extensions/showcase.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)

## Server-only TypeScript/JSX source (unreleased profile)

The released `0.1.3` manifest and Go process package are unchanged. The source
profile for issue #303 is an independently installable authoring artifact, not
a market release, automatic upgrade or production migration. No version bump,
release, signing or deployment is part of this tooling change.

- `src/examples.tsx` uses the official schema-derived native JSX components for
  every existing component/state example. `src/ui.tsx` exports pure page,
  overview-card, widget-family and shortcut factories. State and subscription
  lifetimes use server hooks; callbacks never ship executable code to clients.
- `src/services.ts` owns the synthetic item data, editing, latest-request-wins
  delayed data and shortcut validation. Each mounted page has its own search,
  filter, pagination and segment state; the caller supplies its shared store
  and optional time zone. Pages subscribe only to their relevant service data.
- `src/locales/` contains English, Simplified Chinese and Japanese resources.
  Synthetic default titles and notes are localized per request. Edited text
  stays as entered. Image examples use repository-owned synthetic PNG assets
  through credential-free HTTPS URLs, including one deliberately missing file
  for native load-failure fallback. `scripts/make-demo-media.py` reproduces the
  assets without dependencies. A page caller can supply actual HTTPS media URLs
  through `PageInput.media` or the source profile's optional `media` configuration.
  This is not a hub media API: native images still require HTTPS and network
  availability, while the source runtime itself is fully offline. The defaults
  become fetchable when the assets merge to the public repository. No network
  or secret permission was added. Links retain HTTPS targets for native in-app
  opening, not extension network access.
- `src/legacy-snapshots/` preserves all existing APP fixtures byte-for-byte.
  Semantic tests compare every source page/system surface with those fixtures,
  allowing only stable JSX keys, opaque action ids and the synthetic image
  substitution. Serialized navigation state is compared after decoding. The
  original Go snapshot tests remain.
- `src/main.tsx` default-exports the complete stdio `Program`: navigation,
  overview card, every widget family and both shortcuts. `createPage` uses
  `componentPage`, capturing the Renderer from the same bundled SDK instance.
  Each page owns its hooks; one store shares service edits and delayed arrivals.
  The manager owns authorization, presentation gates, push/pop, visibility,
  snapshots and stream negotiation. Snapshot-only clients retain opaque host
  checkpoints; they do not receive or execute source callbacks.

### Local verification and offline source packaging

From this extension directory:

```sh
npm ci --ignore-scripts --registry=https://registry.npmjs.org
npm run typecheck
npm test
npm run test:bun
npm run build:source
```

The lockfile pins the extension-local tools and the official SDK snapshot via
`file:../../sdk/ts`. No package-manager files belong at the repository root.
Type checking runs both JSX runtimes and compiles invalid JSX fixtures with the
real TypeScript compiler. Portable Node and Bun tests exercise renderers,
hooks, services, system snapshots and the official transport-independent
`connect` protocol; they are not manager/Deno/client end-to-end tests.

`build:source` bundles all code and localization into one self-contained ESM
artifact, checks that it has no runtime imports or dependency resolution, and
writes `.source-dist/showcase-source.tar.gz`. The generated profile manifest
uses `runtime.mode = deno` and `runtime.source = {entry, bundle, sha256}` with
contained paths and the exact bounded artifact digest. It retains every native
and system contribution and includes authoring sources, all locale resources,
synthetic PNGs and dependency provenance. It does not modify the released
manifest, indexes, signatures or hub configuration. Dependencies are not
installed or downloaded at runtime. Use this archive only in a separately
configured source-capable hub; do not replace a working released installation
as part of source development.

The copied SDK is owned upstream and includes its declared development configs,
scripts, typecheck fixtures and canonical conformance fixtures. The official
sync now includes the fixes from upstream #6123, #6137 and #6140. Its advertised
checks work after installing the SDK's own locked development dependencies;
those are build-time dependencies, never source-runtime resolution. Do not
repair the generated SDK locally; use the official synchronization script.

### Release and compatibility boundaries

Legacy HTTP acceptance requires the upstream adapter repair in
`nwverse/persona#6305`: preserve the client's UI version for the source root
while fitting the host's private callback tree canonically. Without that repair,
UI versions 1–2 mount a modern page and drop native push/pop without applying
navigation to the snapshot-only session. The actual default Program and archive
are verified against the repaired backend; unit/bridge fixtures alone do not
establish this compatibility.

- Production source execution still requires the host's independent kernel
  confinement. An explicit development-resource build and opt-in can verify an
  isolated profile, but does not establish production resource isolation or
  authorize deployment. Keep native, egress, presentation and grant gates intact.
- Protocol version 1 is **not** a source-runtime or UI-stream capability gate.
  Pre-source hubs reject the unknown `runtime.mode` field before version
  selection; the profile has no executable command or JavaScript fallback. A failed preview or
  upgrade must preserve the working Go installation. No unverified minimum
  hub version or market selection gate is added here.
- `scripts/package.sh` and the default manifest continue to package Go. Source
  packaging is extension-owned, unreleased tooling; no market entry or automatic
  upgrade selects it. Production confinement and an explicitly authorized
  release/upgrade policy remain prerequisites for switching the shipped runtime.
