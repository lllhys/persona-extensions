# npm Packages

Search the public npm registry from Persona. Compare the latest version, release date, license, bounded download activity, engines, dependency count and canonical links before choosing a dependency.

## Usage

1. Enter a package name, scoped name or keyword.
2. Open a result for the latest release metadata.
3. Review the exact-version install command as text. The extension never runs it.

The UI and assistant tools are localized in English, Simplified Chinese and Japanese.

### Assistant tools

- `npmpackages_search`: search by name or keyword with a bounded result limit.
- `npmpackages_package`: inspect the latest version of an exact package name, including scoped packages such as `@babel/preset-typescript`.

## Network and privacy

The hub uses anonymous read-only `GET` requests to:

- `https://registry.npmjs.org/-/v1/search` for package search.
- `https://registry.npmjs.org/<name>/latest` for the latest release metadata.
- `https://api.npmjs.org/downloads/point/last-week/<name>` for the public download count.

No account, token, browser cookie, private registry or package archive is used. The search query or package name is sent to npm. No package installation, dependency resolution, lifecycle script or third-party code execution occurs.

Responses are validated before caching. Requests are serialized and bounded in size, result count, memory and time. Transient failures can show a previously validated cached result with an explicit error. Missing download metadata is not presented as zero downloads or as a quality verdict.

## Security and provenance

Package names, versions, dates, counts and HTTPS links are strictly validated. Scoped package names are encoded correctly in registry paths. A result's npm link must match its package name; repository and homepage links must be HTTPS.

Download counts and a declared license do not prove safety, maintenance quality or the exact terms of every included file. Review package code, maintainers, dependencies and the package's own license before installing. A command such as `npm install <name>@<version>` is displayed only for the user to review and run deliberately.

## Development

```sh
npm ci --registry=https://registry.npmjs.org
npm test
npm run build:source
```

The deterministic source archive includes the bundle, source, tests, npm 11-compatible lockfile and official SDK authoring closure. Native TypeScript/JSX runs on the hub; clients do not execute extension JavaScript. Local packaging does not sign a market catalog, publish an OCI package or activate production isolation.
