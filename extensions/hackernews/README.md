# Hacker News

Read public Hacker News stories in Persona, without an account: the Top, New, Best, Ask HN and Show HN lists, an in-app story reader with top-level comments, and assistant tools.

- [Extension guide and configuration](../../docs/extensions/hackernews.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)

## Server JSX source development

The TypeScript sources in [src](src) port the read-only services and UI using the
upstream schema-derived SDK. Dependencies, the lockfile and the compiler config
belong to this extension. From this directory, run:

```sh
npm ci --ignore-scripts
npm test
npm run test:bun
npm run build:source
```

Tests use synthetic fixtures, never public authors or posts. They run real `tsc`
checks for valid and invalid native JSX in both JSX modes, then bundle the same
fixture suite for Node or Bun. The pure `page` factory and `snapshotPage` adapter
preserve older APP fitted snapshots; `root` mounts server state/effect hooks with
cancellation and read-only callbacks. The host supplies a sanctioned fetch adapter:
there is no ambient fetch fallback or added network permission.

The source build emits a self-contained, import-free module and a SHA-256 source
descriptor under ignored `build/`. It exports integration factories, **not a
runnable process entry point**. A source runtime must ultimately use the approved
`runtime.mode: deno` and `runtime.source: {entry, bundle, sha256}` contract, a locked
bundle, and a verified engine/auto-upgrade gate. The committed API of the process
UI bridge and common source packaging must be integrated first. Until then the
existing Go process, manifest and version remain unchanged; do not publish the
source module as a working replacement or remove the Go implementation/tests.

The official SDK sync includes its declared configs, scripts, typecheck fixtures
and canonical conformance fixtures, with upstream #6123/#6137/#6140 repairs.
Its package commands work after installing the SDK's own locked development
dependencies. These are build-time tools, not source-runtime dependency
resolution. Do not patch the copied SDK; use the official synchronization script.
Runtime adoption and module-owned hooks/Renderer integration remain separate gates.

A stream cancellation can wait for the other branch of a tee-backed `Response`
in Node. Initiate cancellation without waiting on that branch before releasing
the request gate; the fixture tests cover this portable cleanup behavior.
