# Showcase

Local-only examples of every extension view component and state, widgets and shortcuts, with shared view snapshots for client screenshot alignment.

- [Component, compatibility and snapshot guide](../../docs/extensions/showcase.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)

## Server-only TypeScript/JSX source (draft)

The released `0.1.3` manifest and Go process package are unchanged. The source
port for issue #303 is staged alongside them, not a release or a production
migration. Do not bump the version or publish the source draft until the
manager bridge and compatibility gates below have been verified.

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
  through `PageInput.media` for an isolated acceptance environment; this is not
  a newly invented hub media API. The defaults become fetchable when these
  assets merge to the public repository. Native wire validation and real
  network/image-load acceptance are separate checks. No network or secret
  permission was added. Links retain HTTPS targets for native in-app opening,
  not extension network access.
- `src/legacy-snapshots/` preserves all existing APP fixtures byte-for-byte.
  Semantic tests compare every source page/system surface with those fixtures,
  allowing only stable JSX keys, opaque action ids and the synthetic image
  substitution. Serialized navigation state is compared after decoding. The
  original Go snapshot tests remain.
- `src/main.tsx` default-exports an official stdio `Program` for the shortcuts
  and system snapshot surfaces. It deliberately does **not** advertise the
  navigation page before the committed manager/component-page bridge exists.
  The source host supplies the packaged source manifest; the Program does not
  answer the transitional process manifest as its runtime contract.

### Local verification and offline draft packaging

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
writes a draft archive under `.source-dist/`. The draft manifest follows
`runtime.mode = deno` and `runtime.source = {entry, bundle, sha256}`. It does not
modify the released manifest, indexes, signatures or any hub configuration.
The source package does not install or download dependencies at runtime.

The copied SDK is owned upstream and includes its declared development configs,
scripts, typecheck fixtures and canonical conformance fixtures. The official
sync now includes the fixes from upstream #6123, #6137 and #6140. Its advertised
checks work after installing the SDK's own locked development dependencies;
those are build-time dependencies, never source-runtime resolution. Do not
repair the generated SDK locally; use the official synchronization script.

### Acceptance gates before a release

1. Integrate the landed component-page/process bridge with `createPage` and a
   per-extension store; the manager owns authorization, subscriptions, native
   push/pop, visibility and stream negotiation. Connect service disposal to
   process shutdown. Never replace host authorization with a test fixture.
   Page factories and their Renderer must use the same bundled SDK instance:
   element symbols and hook contexts are instance-specific. The copied SDK now
   safely handles parsed null frames, unreachable hub calls at EOF and public
   error identity across bundles. The source adapter must adopt the verified
   shutdown order and preserve the module-owned Renderer during bridge mounting.
2. Run the actual pinned Deno bundle through the sanctioned independently
   isolated host, manager and WebSocket/client path. Verify every navigation,
   search, filter, pagination, edit, delayed update and system surface. Obtain
   real Web/Android dark/light evidence. No screenshot or E2E acceptance is
   claimed by the source unit fixtures.
3. Verify manifest schema support, offline bundle integrity and compatible
   engine selection in install and auto-upgrade. Protocol version 1 alone is
   **not** a source-runtime or UI-stream capability gate. Older hubs must not
   select an unsupported source package. Generic source release packaging is
   still separate from `scripts/package.sh` and market signing.
4. Only after those gates, switch the shipped runtime and bump the version in
   an authorized release PR. Keep the process implementation and APP fixtures
   until the fallback and upgrade behavior is verified.
