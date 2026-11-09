# Development

[Documentation](README.md) · [Contributing](../CONTRIBUTING.md)

All commands below run from the repository root unless stated otherwise.

## Toolchain and layout

Use the Go version the modules declare (for example [pluginkit/go.mod](../pluginkit/go.mod)), Python 3 and OpenSSL. Packaging uses Bash, `tar` with `--no-xattrs` support, and `sha256sum`. Releasing additionally needs the GitHub CLI (`gh`) and explicit publication authorization.

Every program extension is its own Go module: a `main` package in `extensions/<id>/` with its own `go.mod` and `go.sum`, module path `github.com/nwverse/persona-extensions/extensions/<id>`. It builds, tests and packages on its own. Shared code is a module of its own that an extension pulls in with a `replace` directive: `pluginkit/` (`../../pluginkit`) and the copied SDK (`../../sdk/go`). The repository root holds no package-manager file (`go.mod`, `go.sum`, `go.work`, `package.json`, lockfiles, `node_modules`, `Cargo.toml`, `pyproject.toml`, `requirements.txt` and the like); [scripts/check-layout.py](../scripts/check-layout.py) fails when one appears.

The official TypeScript SDK also owns one standalone authoring oracle, `sdk/ts/scripts/int64-oracle.go`, which its portable checks invoke explicitly with `go run <file>`. It is not an extension runtime or a discovered Go module. The layout guard recognizes only that exact SDK-owned file; other Go files outside modules, including other SDK scripts, still fail. Synchronize the oracle unchanged with the SDK rather than adding an invented module or editing the copied SDK.

```text
extensions/<id>/           Manifest, source, tests and dependencies for one extension
  persona-extension.json   The only manifest, including the market tags
  go.mod, go.sum           The extension's own Go module (program extensions only)
pluginkit/                 Quota, UI and inbox helpers for the existing extension APIs (own module)
tools/                     Repository-level Go tests, such as every manifest against the SDK schema (own module)
sdk/go/                    Synced copy of the official Persona Go SDK; do not hand-edit
sdk/VERSION                Upstream source revision and sync ownership
scripts/                   Manifest checks, packaging, releases, indexes and signing
docs/                      Human-maintained English guides and references
persona-extensions.json    Single signed envelope: raw UTF-8 payload plus embedded signatures
```

The SDK implements the process protocol; `pluginkit` preserves the helpers used by existing extensions. See [reference](reference.md) for schema and configuration details.

## Add a program extension

1. Create `extensions/<id>/`. The directory name must match the manifest `id`, which matches `^[a-z0-9][a-z0-9_-]{0,31}$`.
2. Write `persona-extension.json`, with the market tags in its `tags` field. Do not add `persona-plugin.json` or `market.json`: packaging generates the legacy manifest, and `scripts/manifest.py check` rejects both files. Add `main.go` and tests. Use synthetic fixtures and public endpoints only.
   Give the extension its own module, next to its manifest:

   ```sh
   cd extensions/<id>
   go mod init github.com/nwverse/persona-extensions/extensions/<id>
   go mod edit -require=github.com/nwverse/persona/sdk/go@v0.0.0 -replace=github.com/nwverse/persona/sdk/go=../../sdk/go
   # Only when it imports pluginkit:
   go mod edit -require=github.com/nwverse/persona-extensions/pluginkit@v0.0.0 -replace=github.com/nwverse/persona-extensions/pluginkit=../../pluginkit
   go mod tidy
   ```
3. In `runtime.command`, list the program path for each supported platform: `linux-amd64`, `linux-arm64`, `darwin-arm64` and `darwin-amd64`. Use `bin/<id>-<platform>` paths. The package ships compiled programs; the hub installs no runtime.
4. Test the manifest against the program's advertised contributions and exercise the process protocol with the official SDK's `exttest` helpers. Follow the existing [LINUX DO tests](../extensions/linuxdo/plugin_test.go) or [Telegram tests](../extensions/telegram/plugin_test.go).
5. Run the affected local tests, lint and builds before preparing a pull request, then merge after that verification passes. Hosted CI runs only on explicit dispatch, not as a PR gate. A version bump selects work for a separately authorized [manual release](releases.md#automated-releases); it does not publish by itself.

See the [local reference](reference.md) and published SDK schemas for the process contract. For runtime-free extensions and packs, start with [starter-kit and starter-pack](extensions/starter-kit.md): omit `runtime`, provide the manifest and contribution files, and use the same packaging script. It skips compilation when there is no runtime and omits an empty `bin/` directory.

## Tests and packaging

From 2026-11-09, [CI](../.github/workflows/ci.yml) runs only through `workflow_dispatch`. PRs merge after affected local tests, lint and builds pass; do not wait for hosted CI, use admin merge or change rulesets. Manual checks retain every source-rebuild job and its budgets; disabling automatic triggers must not remove those checks or enable publication. For workflow changes, run [policy regressions](../scripts/test-ci-policy.py) and workflow lint locally as well as the checks below.

```sh
python3 -I scripts/test-ci-policy.py
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/ci.yml
```

A resident Persona node probes test listeners with an unauthenticated `GET /`. Counting it as a connector request can falsely fail a logout assertion. Synthetic fixtures must recognize only the complete known probe shape (method, raw target, public compatibility User-Agent, headers and empty body), never all root or unauthenticated traffic. Preserve the original no-request assertion and add credential/query/body negative controls; do not stop the node or raise waits/count budgets to hide the interference.

```sh
scripts/ci.sh
```

`scripts/ci.sh` checks local documentation links and their regression tests, the repository layout (`scripts/check-layout.py`: no package-manager file at the root, every Go extension its own module, no compiled program committed under `extensions/`), manifests (`scripts/manifest.py check`), Go formatting, the copied SDK's vet and full test suite, then for every other module (`scripts/check-layout.py modules`: `pluginkit`, `tools` and each extension) that `go mod tidy` has nothing to change, `go vet` and Go tests with the race detector (`go test -race`, which needs cgo and a C compiler), the [LarkAgentX companion's synthetic Python tests](extensions/larkagentx.md#local-validation-and-remaining-work), builds for every platform declared by each manifest, the single embedded catalog signature, and synthetic OCI publisher/identity/public-gate tests. It packages extensions locally and never publishes; publication arguments are rejected. Publication is separate and remains blocked until the cleaned repository completes cutover.

The copied SDK is a module of its own like the others; to run only that part:

```sh
go -C sdk/go vet ./...
go -C sdk/go test -count=1 -timeout=90s ./...
```

The `wire` tests read synthetic examples vendored under `schema/extension/examples/`, so the full suite runs in a public checkout. If it fails with "no examples" / "only 0 cases", those files are missing or moved; restore them rather than skipping the tests or importing private fixtures.

For a single extension, in its own module:

```sh
go -C extensions/linuxdo test ./...
go -C extensions/telegram test ./...
scripts/package.sh linuxdo
```

Packaging produces `dist/<id>-<version>.tar.gz` and a SHA-256 file. It includes both manifest names and any `skills/` or `assistant/` directories the extension ships. Files a manifest references (skill directories, instruction prompts, assistant skills, instructions and gates) must therefore live under `skills/` or `assistant/`; `scripts/manifest.py check` fails when one is elsewhere or missing. Every manifest, including those of extensions without a program, is also validated against the SDK `Manifest` schema as committed and as packaged, by `go -C tools test ./...`. Archives omit macOS AppleDouble metadata and extended attributes, which otherwise can hide the manifest from the hub's package reader.

See [installation](installation.md#install-a-local-package) to install the package on a hub. Extension tests use fake services; running them does not require real account credentials.

## Standalone source rebuilds on hosted CI

Each new executable source migration registers `extensions/<id>/source-rebuild.json` and commits its expected import-free bundle under `source-reference/`. The reference is a byte-comparison fixture, not a released default package. The profile names its source build script, generated bundle/archive, and the extension/SDK paths inside the standalone authoring archive. See the [LinuxDo profile](../extensions/linuxdo/source-rebuild.json).

[The source rebuild job](../.github/workflows/ci.yml) runs every registered profile on its own fresh GitHub-hosted runner, with pinned Node, no restored dependency cache and no HOME substitution. [The driver](../scripts/source-rebuild.py) copies only tracked extension and official SDK authoring inputs into a new tree, excluding the reference bundle; installs locked public dependencies; builds; and compares the result **byte-for-byte** with the committed reference. It then extracts the produced authoring archive into another new directory and independently installs and rebuilds it, repeating the same byte comparison. Archive traversal, links, special files, duplicate files and excessive size fail before extraction.

For explicit checks-only dispatches, `CI required` includes profile discovery and every source-rebuild matrix result. It is not a PR merge gate. New source authoring still cannot omit a rebuild profile. The pre-existing Hacker News and Showcase examples are not part of the new migration batch and supply no fresh-HOME proof unless they register a profile; their separate acceptance gates are not waived. [Pure guard regressions](../scripts/test-source-rebuild.py) run locally and in CI without executing a local standalone rebuild.

A successful manually dispatched hosted job supplies fresh-HOME proof; do not claim that proof before its actual result. A local fresh-HOME rebuild is not required and local permission restrictions remain unchanged: do not substitute HOME, use a peer or wrapper, or relabel a weaker local check as that proof. Hosted rebuilding proves contained authoring and bundle reproducibility, not installed-hub behavior, production sandbox isolation, source default publication or catalog signing.

Credential-negative lock fixtures use synthetic `URL.username` and `URL.password` values on the approved public registry origin. Assert that origin and retain the credential-rejection check, so an unrelated bad hostname cannot make the control pass. Do not add privacy exceptions, change the lock policy, or encode/split real private values to get them through a scan.

## Documentation checks

```sh
python3 -I scripts/check-docs.py
python3 -I scripts/test-docs.py
git diff --check
```

The checker validates explicit local Markdown links, directory targets and ATX heading anchors, including reference-style links, without fetching remote pages. Check external URLs separately when adding or moving references.

When moving a guide, update links from both READMEs and extension directories. Code examples run from the repository root and name the module with `go -C` (`go -C extensions/linuxdo test ./...`); do not retain pre-monorepo paths such as `go test ./linuxdo`. Keep the language policy in [CONTRIBUTING.md](../CONTRIBUTING.md) in mind: shipped prompts/skills, localization, meaningful fixtures and the upstream-owned SDK are not ordinary documentation cleanup targets.

For comment-only changes, compare Go syntax/tokens with comments removed and Python syntax trees with docstrings removed to verify executable code and string literals are unchanged. Formatting and passing tests alone do not prove that translation preserved behavior.
