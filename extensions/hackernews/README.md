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
fixture suite for Node or Bun. The default export is a stdio `Program` consumed by the hub's trusted source
adapter, not a transport started by this extension. One initialized service/cache
is shared by connector tools, the `stories` server component page and the
`stories-snapshot` system widget. Hooks, effects, callbacks and reads stay on the
hub; clients receive native View data only, never HTML or client JavaScript.
Production fetch is the adapter's immutable, grant-checked broker; synthetic tests
inject an adapter without changing the network grants.

`build:source` emits a bounded, import-free bundle, its SHA-256 descriptor,
`build/package/` and `build/hackernews-source.tar.gz`. The explicit local package
profile uses `runtime.mode: deno` and `runtime.source: {entry, bundle, sha256}`,
a main-navigation page and system snapshot, retaining version 0.1.1 and the
released read-only permissions. It includes TypeScript authoring files and the
extension-owned lockfile. Installing the archive needs no npm dependency
resolution. Rebuilding from source uses the repository's officially synced SDK.
The released Go manifest, implementation, tests and market are unchanged; this
profile is not selected by the normal release packaging.

Only a source-capable Linux hub with a verified engine and the strict sandbox,
egress and hard memory/CPU/pids resource gates can run it in production. Node,
non-Linux hosts and older hubs do not support this source mode. Unsupported
installs/upgrades must be rejected before replacing a working Go install.
On a Linux development kernel lacking the production resource gate, tests may
use an isolated hub compiled with `persona_dev_resources` and explicitly opt in
with `PERSONA_DEV_SOURCE_RESOURCES=1`; each activation warns. That is not
production support and does not relax network permissions or sandbox authority.

Native sessions use the final SDK `componentPage` bridge, with owner-bound
callbacks, generation/version checks, replay protection and reconnect handling.
The current host serves older UI versions through fitted full Views with opaque
session checkpoints, rejecting stale actions. Legacy fitted Views omit native item
keys: send their returned opaque action IDs and checkpoints, rather than looking
up controls by source keys. The system snapshot uses the same JSX through the
stateless Page adapter and has no mounted callbacks.

The official SDK sync includes its declared configs, scripts, typecheck fixtures
and canonical conformance fixtures. Its package commands work after installing
the SDK's own locked development dependencies. These are build-time tools, not
source-runtime dependency resolution. Do not patch the copied SDK; use the
official synchronization script.

A stream cancellation can wait for the other branch of a tee-backed `Response`
in Node. Initiate cancellation without waiting on that branch before releasing
the request gate; the fixture tests cover this portable cleanup behavior.
