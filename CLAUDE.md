# CLAUDE.md

Instructions for AI agents (and people) changing this repository. [CONTRIBUTING.md](CONTRIBUTING.md) has the full workflow; this file holds the rules that must never be broken.

## No company-internal data, no personal data

This is a public repository. Never commit an extension, a file, a test fixture, a screenshot, a commit message or a pull request text that leaks:

- **Company-internal data:** internal systems and services, internal addresses (intranet domains, IPs, URLs, host names), internal APIs and their endpoints or schemas, internal tools and the data they return, private or internal repository and project names, and employee or organization information (names, departments, org charts, office locations).
- **Personal data:** real names, email addresses, phone numbers, accounts and user ids, tokens, passwords, API keys, private keys and key identifiers, and any real message, document or link.

Extensions here use public services and protocols only, and do not depend on company accounts, private networks or internal tools. An extension that needs any of the above belongs in the internal plugin repository on the company network, not here. When one is found here, move it there and remove it from this repository and its market index in the same change. If you cannot tell whether something is safe to publish, keep it out until that is settled.

Use synthetic fixtures and placeholders (`<hub address>`, `me@example.com`) instead. Specific values of a person's environment go in configuration, and secrets go through the hub's encrypted secret flow.

## No package-manager files at the root; each extension owns its dependencies

The repository root holds no package-manager or lockfile of any language: no `go.mod`, `go.sum`, `go.work`, `vendor/`, `package.json`, `package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`, `node_modules`, `Cargo.toml`, `pyproject.toml`, `requirements.txt` or the like. `scripts/check-layout.py` (run by `scripts/ci.sh`) fails when one appears.

- Every extension manages its own dependencies in `extensions/<id>/`. A Go extension is its own module (`extensions/<id>/go.mod` and `go.sum`, module path `github.com/nwverse/persona-extensions/extensions/<id>`). A dependency one extension needs goes into that extension's module only.
- Each extension builds, tests and packages on its own: `go -C extensions/<id> test ./...`, `scripts/package.sh <id>`. Keep `go mod tidy` clean in every module; CI checks it.
- Shared Go code is a module of its own that an extension pulls in with a `replace` directive, never through a root module: `pluginkit/` and the copied SDK `sdk/go/` (synced from upstream, not edited here). Repository-level Go tests live in the `tools/` module. Repository scripts (`scripts/`) use only the Python standard library, Bash and Go.
- Do not add a `go.work` to stitch modules together, and do not move an extension's dependency up to the root to share it.

## Other rules

- Never sign or edit the market indexes (`persona-extensions.json`, `persona-plugins.json`, their `.sig`) by hand: CI releases a changed version and re-signs them.
- A version bump merged to `main` releases that extension (docs/releases.md). Bump `version` in `persona-extension.json` when the package changes; a change that leaves packages as they are (tests, docs, repository tooling) bumps nothing.
- Check open issues and pull requests for related work first, and run `scripts/ci.sh` before opening a pull request ([CONTRIBUTING.md](CONTRIBUTING.md)).

## Language

English for documentation, code comments, commit messages and pull request texts. Shipped assistant skills, prompts and UI text keep their original language.
