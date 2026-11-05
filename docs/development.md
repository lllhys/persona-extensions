# Development

[Documentation](README.md) · [Contributing](../CONTRIBUTING.md)

All commands below run from the repository root unless stated otherwise.

## Toolchain and layout

Use the Go version declared in [go.mod](../go.mod), Python 3 and OpenSSL. Packaging uses Bash, `tar` with `--no-xattrs` support, and `sha256sum`. Releasing additionally needs the GitHub CLI (`gh`) and explicit publication authorization.

This is one Go module, `github.com/nwverse/persona-extensions`; each program extension is a `main` package under `extensions/<id>/`.

```text
extensions/<id>/           Manifest, source and tests for one extension
  persona-extension.json   The only manifest, including the market tags
pluginkit/                 Quota, UI and inbox helpers for the existing extension APIs
sdk/go/                    Synced copy of the official Persona Go SDK; do not hand-edit
sdk/VERSION                Upstream source revision and sync ownership
scripts/                   Manifest checks, packaging, releases, indexes and signing
docs/                      Human-maintained English guides and references
persona-extensions.json    Current market index, under the "extensions" key
persona-plugins.json       Equivalent legacy index, under the "plugins" key
*.json.sig                 Index signatures
market.pub                 Market public signing key
```

The SDK implements the process protocol; `pluginkit` preserves the helpers used by existing extensions. See [reference](reference.md) for schema and configuration details.

## Add a program extension

1. Create `extensions/<id>/`. The directory name must match the manifest `id`, which matches `^[a-z0-9][a-z0-9_-]{0,31}$`.
2. Write `persona-extension.json`, with the market tags in its `tags` field. Do not add `persona-plugin.json` or `market.json`: packaging generates the legacy manifest, and `scripts/manifest.py check` rejects both files. Add `main.go` and tests. Use synthetic fixtures and public endpoints only.
3. In `runtime.command`, list the program path for each supported platform: `linux-amd64`, `linux-arm64`, `darwin-arm64` and `darwin-amd64`. Use `bin/<id>-<platform>` paths. The package ships compiled programs; the hub installs no runtime.
4. Test the manifest against the program's advertised contributions and exercise the process protocol with the official SDK's `exttest` helpers. Follow the existing [LINUX DO tests](../extensions/linuxdo/plugin_test.go) or [Telegram tests](../extensions/telegram/plugin_test.go).
5. Run the checks below before preparing a pull request. A version bump merged to `main` triggers [release automation](releases.md#automated-releases).

See the [local reference](reference.md) and published SDK schemas for the process contract. For runtime-free extensions and packs, start with [starter-kit and starter-pack](extensions/starter-kit.md): omit `runtime`, provide the manifest and contribution files, and use the same packaging script. It skips compilation when there is no runtime and omits an empty `bin/` directory.

## Tests and packaging

```sh
go test ./...
scripts/ci.sh
```

`scripts/ci.sh` checks local documentation links and their regression tests, manifests (`scripts/manifest.py check`), Go formatting, `go vet`, Go tests, the [LarkAgentX companion's synthetic Python tests](extensions/larkagentx.md#local-validation-and-remaining-work), builds for every platform declared by each manifest, equivalent market entries and existing index signatures. It packages extensions locally, but without the `release` argument it does not publish anything.

The copied SDK is a nested module, so root `go test ./...` does not run its own test packages. To check that copy separately:

```sh
go -C sdk/go test ./...
```

The `ext` and `exttest` packages have self-contained tests. Some `wire` tests look for host-side example fixtures that are not distributed in this public checkout; without those, the full nested-module run fails with "no examples" / "only 0 cases". Report that limitation rather than importing private fixtures or claiming the full suite passed.

For a single extension:

```sh
go test ./extensions/linuxdo
go test ./extensions/telegram/...
scripts/package.sh linuxdo
```

Packaging produces `dist/<id>-<version>.tar.gz` and a SHA-256 file. It includes both manifest names and any `skills/` or `assistant/` directories the extension ships. Files a manifest references (skill directories, instruction prompts, assistant skills, instructions and gates) must therefore live under `skills/` or `assistant/`; `scripts/manifest.py check` fails when one is elsewhere or missing. Every manifest, including those of extensions without a program, is also validated against the SDK `Manifest` schema as committed and as packaged, by `go test ./extensions`. Archives omit macOS AppleDouble metadata and extended attributes, which otherwise can hide the manifest from the hub's package reader.

See [installation](installation.md#install-a-local-package) to install the package on a hub. Extension tests use fake services; running them does not require real account credentials.

## Documentation checks

```sh
python3 -I scripts/check-docs.py
python3 -I scripts/test-docs.py
git diff --check
```

The checker validates explicit local Markdown links, directory targets and ATX heading anchors, including reference-style links, without fetching remote pages. Check external URLs separately when adding or moving references.

When moving a guide, update links from both READMEs and extension directories. Code examples use repository-root paths; do not retain pre-monorepo paths such as `go test ./linuxdo`. Keep the language policy in [CONTRIBUTING.md](../CONTRIBUTING.md) in mind: shipped prompts/skills, localization, meaningful fixtures and the upstream-owned SDK are not ordinary documentation cleanup targets.

For comment-only changes, compare Go syntax/tokens with comments removed and Python syntax trees with docstrings removed to verify executable code and string literals are unchanged. Formatting and passing tests alone do not prove that translation preserved behavior.
