# Rust Crates

Search the official crates.io registry from Persona. Compare a crate's default version, license, release date, recent download activity, documentation and repository before choosing a Rust dependency.

## Usage

1. Enter a crate name or keyword.
2. Open a result to read its default-release metadata and any yanked warning.
3. Review the exact-version `cargo add <name>@<version>` command as text. The extension never runs it.

The UI and assistant tools are localized in English, Simplified Chinese and Japanese.

### Assistant tools

- `crates_search`: search by name or keyword with a bounded result limit.
- `crates_crate`: inspect a crate's default version and its selected release metadata.

## Network and privacy

The hub uses anonymous read-only `GET` requests to the official `https://crates.io/api/v1/` endpoints with the descriptive `persona-crates` User-Agent. No account, token, browser cookie or private registry is used. The search query or crate name is sent to crates.io.

No crate archive, build, installation, dependency resolution or lifecycle script is executed. The full release history is not retained: only the selected default version's metadata is parsed and cached after validation. Requests are serialized and bounded in size, result count, memory and time. Transient failures may show a previously validated cached result with an explicit error.

## Security and provenance

Crate names, versions, dates, counts and HTTPS links are strictly validated. The selected default version must have exactly one matching version record. A yanked release is explicitly marked rather than silently recommended. Download activity and a declared license do not prove safety, maintenance quality or the exact terms of every included file. Review crate code, maintainers, dependencies and its own license before installing.

## Development

```sh
npm ci --registry=https://registry.npmjs.org
npm test
npm run build:source
```

The deterministic source archive includes the bundle, source, tests, npm 11-compatible public-registry lockfile and official SDK authoring closure. Native TypeScript/JSX runs on the hub; clients do not execute extension JavaScript. Local packaging does not sign a market catalog, publish an OCI package or activate production isolation.
