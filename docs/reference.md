# Manifest, configuration and protocol reference

[Documentation](README.md) · [Development](development.md)

## Manifests and configuration

Each extension is rooted at `extensions/<id>/` and has one manifest, `persona-extension.json`. [scripts/manifest.py](../scripts/manifest.py) checks directory/id agreement, versions and tags, that every file the manifest references is in the extension under `skills/` or `assistant/` (the directories packaging copies), and that `extension_pack` members without their own `market` are extensions in this repository; it rejects a committed `persona-plugin.json` or `market.json`. [tools/manifests/manifests_test.go](../tools/manifests/manifests_test.go) validates every manifest, with and without `tags`, against the SDK `Manifest` schema.

| Field or file | Purpose |
| --- | --- |
| `manifest_version`, `id`, `name`, `version` | Manifest format and extension identity; version is `MAJOR.MINOR.PATCH`, optionally with a prerelease suffix. |
| `engines` | Host/protocol compatibility requirements. `plugin_protocol` remains a compatibility identifier. |
| `runtime.command` | Platform-to-binary map; package paths are `bin/<id>-<platform>`. |
| `contributes` | Connectors, app views, assistant skills and other contributions supported by the host. |
| `configuration` | JSON Schema for the app's settings form, in the subset supported by Persona. These titles and descriptions are runtime UI text. |
| `permissions` | Requested access, such as network hosts; the hub presents permissions during installation. |
| `tags` | Tags shown and searched in the market: at most 16, each 1–24 characters. They live only in the source manifest; see [Compatibility](#compatibility). |

Examples: [LINUX DO manifest](../extensions/linuxdo/persona-extension.json), [Telegram manifest](../extensions/telegram/persona-extension.json). Settings and defaults for each are explained in their [LINUX DO](extensions/linuxdo.md#configuration) and [Telegram](extensions/telegram.md#inbox-triage) guides. Credentials are not configuration examples: use the hub's encrypted connector-secret flow, not manifests or repository files.

The [published manifest schema](../sdk/go/wire/schema/manifest.schema.json) describes the fields and supported contribution shapes for the recorded SDK revision. It is not a substitute for checking host-version requirements: do not assume a schema-valid new contribution is supported by an older hub. The public checkout deliberately does not copy private host documentation or test fixtures.

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

- New hubs read `persona-extension.json` and `persona-extensions.json` (`extensions` entries).
- Pre-rename hubs read `persona-plugin.json` and `persona-plugins.json` (`plugins` entries), and use `personad plugin …`.
- Packages include both manifest names. [scripts/package.sh](../scripts/package.sh) writes both from `persona-extension.json`; the legacy file is never committed.
- Hubs from before `tags` read manifests strictly and refuse unknown fields. Packages and index entries therefore carry the manifest without `tags` (`scripts/manifest.py packaged`); the index entry carries the tags in its own `tags` field, and the SDK drops them when a program answers `manifest`. `scripts/index.py check` rejects an index manifest that still has them.
- Both indexes list equivalent extension ids, versions and packages; their metadata also agrees. Each is signed separately over its exact bytes.
- Protocol names and placements such as `plugin_protocol` and LINUX DO's `plugin_page` remain intentional. The host may expose a newer UI name without making the older manifest identifier invalid.

The legacy copies follow the compatibility period of nwverse/persona#1843: packaging keeps generating `persona-plugin.json`, and the index keeps `persona-plugins.json`, until at least 2027-05-05 and until the hub logs no calls to the old interfaces for 30 consecutive days. Stop generating them in a separate change after that. Stripping `tags` from packaged manifests lasts until the market no longer serves hubs from before `tags`.

Do not remove old files or rename protocol identifiers as part of documentation cleanup. See [releases](releases.md) for index and signing details.

## SDK ownership

[sdk/VERSION](../sdk/VERSION) records the source revision. The entire `sdk/go/` directory is copied from the official Persona SDK by the upstream `scripts/gen/sync-sdk.sh`; it is not maintained independently here. There is no module at the repository root: each module that uses the SDK (every Go extension, [pluginkit](../pluginkit/go.mod) and [tools](../tools/go.mod)) resolves `github.com/nwverse/persona/sdk/go` to that local copy with its own `replace` directive.

Do not hand-edit synced code, schema descriptions or generated wire types to translate documentation. Changes belong upstream and arrive through a new sync. This includes a small number of Chinese upstream section-title references; runtime labels and synthetic Chinese fixtures in the SDK are also intentionally preserved. Manually maintained guides in this repository use English and link to the copied schemas instead of duplicating them.
