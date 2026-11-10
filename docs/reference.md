# Manifest, configuration and protocol reference

[Documentation](README.md) · [Development](development.md)

## Manifests and configuration

Each extension is rooted at `extensions/<id>/` and has one manifest, `persona-extension.json`. [scripts/manifest.py](../scripts/manifest.py) checks directory/id agreement, versions and tags, that every file the manifest references is in the extension under `skills/`, `assistant/` or `looks/` (the directories packaging copies), and that `extension_pack` members without their own `market` are extensions in this repository; it rejects a committed `persona-plugin.json` or `market.json`. [tools/manifests/manifests_test.go](../tools/manifests/manifests_test.go) validates source manifests after removing only author-owned `replaces`, and runtime projections without `tags` or `replaces`, against the unchanged SDK `Manifest` schema.

| Field or file | Purpose |
| --- | --- |
| `manifest_version`, `id`, `name`, `version` | Manifest format and extension identity; SemVer `MAJOR.MINOR.PATCH`, optionally with prerelease identifiers, no numeric leading zeros or build metadata, at most 96 ASCII characters. See [OCI naming](releases.md#oci-contract). |
| `engines` | Host/protocol compatibility requirements. `plugin_protocol` remains a compatibility identifier. |
| `runtime.command` | Platform-to-binary map; package paths are `bin/<id>-<platform>`. |
| `contributes` | Connectors, app views, assistant skills and other contributions supported by the host. |
| `configuration` | JSON Schema for the app's settings form, in the subset supported by Persona. These titles and descriptions are runtime UI text. |
| `permissions` | Requested access, such as network hosts; the hub presents permissions during installation. |
| `replaces` | Optional single former extension ID, declared only by the author and projected onto the signed market entry. It is never a runtime manifest field; see [Identity changes](#identity-changes). |
| `tags` | Tags shown and searched in the market: at most 16, each 1–24 characters. They live only in the source manifest; see [Compatibility](#compatibility). |

Examples: [LINUX DO manifest](../extensions/linuxdo/persona-extension.json), [Telegram manifest](../extensions/telegram/persona-extension.json). Settings and defaults for each are explained in their [LINUX DO](extensions/linuxdo.md#configuration) and [Telegram](extensions/telegram.md#inbox-triage) guides. Credentials are not configuration examples: use the hub's encrypted connector-secret flow, not manifests or repository files.

The [published manifest schema](../sdk/go/wire/schema/manifest.schema.json) describes the fields and supported contribution shapes for the recorded SDK revision. It is not a substitute for checking host-version requirements: do not assume a schema-valid new contribution is supported by an older hub. The public checkout deliberately does not copy private host documentation or test fixtures.

## Identity changes

To rename a market extension, move its source directory, change its `id` and binary paths, bump its version, and declare `replaces: "old-id"` in the source manifest. Only one current extension may claim a former ID; self-replacement, duplicate predecessors, cycles and a predecessor that is still a current catalog ID are rejected. `scripts/manifest.py packaged` strips `replaces` and `tags`; programs embedding the author manifest must apply the same projection before answering `manifest`. Do not add source-only fields to the copied SDK schema.

The publisher signs the declaration in the entry and in complete source-inventory provenance. A hub with identity-transition support upgrades the actual predecessor installation only within its persisted market source and after explicit confirmation. Refreshing a catalog or enabling auto-update cannot silently rename it. Configuration, encrypted secrets, grants, switches, contribution choices and data use the original storage identity; old owner-facing IDs resolve only to that committed installation. Program tokens do not gain authority through those aliases. A fresh install with no installed predecessor does not pre-claim its name.

Upgrade the hub and relevant nodes before confirming a rename. Installing a differently named local archive is not a migration and can create a separate installation. Keep the old package immutable for rollback; do not uninstall the predecessor to simulate an upgrade. See [agent-bypass-allow](extensions/agent-bypass-allow.md#upgrading-from-the-former-id) for its hook compatibility and [releases](releases.md) for the new-package visibility gate.

## Process protocol and views

The official Go SDK's [ext package](../sdk/go/ext/ext.go) implements the program side of the process protocol: handshake, requests, errors and hub callbacks. Tests use [exttest](../sdk/go/exttest/exttest.go) for conformance. The host delivers configuration and granted permissions through the SDK's [environment/configuration helpers](../sdk/go/ext/env.go).

- [Protocol schema](../sdk/go/wire/schema/protocol.schema.json)
- [View schema](../sdk/go/wire/schema/view.schema.json)
- [Wire types](../sdk/go/wire/wire_gen.go)
- [View validation](../sdk/go/wire/validate.go)

Extensions return declarative views; app clients render them. `ui.render` draws a contribution and `ui.action` handles its actions. The view schema and host validation limit content and links; extensions must preserve those bounds when truncating or paginating data. New UI capabilities need fallbacks for older clients, not just a manifest change.

Telegram's [serve adapter](../extensions/telegram/internal/serve/serve.go) dispatches `connector.*` methods to its connector. Secret access (`connector.secrets.*`) and change notifications (`connector.changed`) go back to the hub through the SDK. The [Telegram guide](extensions/telegram.md) explains its specific tools and write-confirmation boundary.

## Compatibility

The repository was created during the Plugin → Extension migration's public-repository split. Historical implementation names are not prose to translate or blindly rename.

- Current hubs need embedded-signature catalog and OCI support to read `persona-extensions.json`; its raw UTF-8 `payload` contains `extensions` entries. Upgrade the hub before switching the catalog format.
- Older hubs can still install local packages with the compatible `persona-plugin.json` manifest and `personad plugin …`; there is no legacy market catalog in this repository.
- Packages include both manifest names. [scripts/package.sh](../scripts/package.sh) writes both from `persona-extension.json`; the legacy file is never committed.
- Hubs from before `tags` read manifests strictly and refuse unknown fields. Packages and index entries therefore carry the manifest without source-only `tags` or `replaces` (`scripts/manifest.py packaged`); the index entry carries those fields separately, and embedded runtime responses must omit them. `scripts/index.py check` rejects an index manifest that still has them.
- The catalog has one embedded Ed25519 signature over the raw UTF-8 payload string. Its public key remains the approved cleaned repository key; no detached signature or standalone key file is served.
- Protocol names and placements such as `plugin_protocol` and LINUX DO's `plugin_page` remain intentional. The host may expose a newer UI name without making the older manifest identifier invalid.

The package manifest copies still follow the compatibility period of nwverse/persona#1843: packaging keeps generating `persona-plugin.json` until at least 2027-05-05 and until the hub logs no calls to the old interfaces for 30 consecutive days. The approved catalog format migration is a separate decision: it removes the legacy catalog and detached files now. Stripping `tags` from packaged manifests lasts until the market no longer serves hubs from before `tags`.

Do not rename protocol identifiers or remove the compatible package manifest as part of the catalog migration. See [releases](releases.md) for catalog signing, OCI publication and the cutover gate.

## SDK ownership

[sdk/VERSION](../sdk/VERSION) records the source revision. The entire `sdk/go/` directory is copied from the official Persona SDK by the upstream `scripts/gen/sync-sdk.sh`; it is not maintained independently here. There is no module at the repository root: each module that uses the SDK (every Go extension, [pluginkit](../pluginkit/go.mod) and [tools](../tools/go.mod)) resolves `github.com/nwverse/persona/sdk/go` to that local copy with its own `replace` directive.

Do not hand-edit synced code, schema descriptions or generated wire types to translate documentation. Changes belong upstream and arrive through a new sync. This includes a small number of Chinese upstream section-title references; runtime labels and synthetic Chinese fixtures in the SDK are also intentionally preserved. Manually maintained guides in this repository use English and link to the copied schemas instead of duplicating them.
