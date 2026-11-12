# NuGet Packages

Search the official NuGet registry from Persona. Compare the current stable version, license, release date, target frameworks, dependency count and canonical links before choosing a .NET dependency.

## Usage

1. Enter a package ID or keyword.
2. Open a result to read its current stable release metadata and any unlisted warning.
3. Review the exact-version `dotnet add package <id> --version <version>` command as text. The extension never runs it.

The UI and assistant tools are localized in English, Simplified Chinese and Japanese.

### Assistant tools

- `nuget_search`: search by package ID or keyword with a bounded result limit.
- `nuget_package`: inspect the current stable version of an exact package ID, including target frameworks and dependency counts.

## Network and privacy

The hub uses anonymous read-only `GET` requests to:

- `https://azuresearch-usnc.nuget.org/query` for package search.
- `https://api.nuget.org/v3/registration5-semver1/<id>/<version>.json` for the exact stable release leaf.
- `https://api.nuget.org/v3-flatcontainer/<id>/<version>/<id>.nuspec` for the exact stable release metadata, following only NuGet's official redirect to `nuget.azure.cn`.

No account, token, browser cookie or private registry is used. The query or package ID is sent to NuGet. No package archive, restore, build, installation or lifecycle script is executed.

The exact stable version from the search result is used; the highest prerelease in registration history is not mistaken for the current stable release. Responses are validated before caching. Redirects are followed only to the official `api.nuget.org` or `nuget.azure.cn` HTTPS metadata endpoints; other hosts, insecure URLs, credentials, ports and non-metadata paths are refused. Requests are serialized and bounded in size, result count, memory and time. Transient failures may show a previously validated cached result with an explicit error.

## Security and provenance

Package IDs, versions, dates, counts, catalog URLs and HTTPS links are strictly validated. Only the official NuGet catalog entry referenced by the selected release is accepted. An unlisted release is explicitly marked. Download activity and a declared license do not prove safety, maintenance quality or the exact terms of every included file. Review package code, maintainers, dependencies and its own license before installing.

## Development

```sh
npm ci --registry=https://registry.npmjs.org
npm test
npm run build:source
```

The deterministic source archive includes the bundle, source, tests, npm 11-compatible public-registry lockfile and official SDK authoring closure. Native TypeScript/JSX runs on the hub; clients do not execute extension JavaScript. Local packaging does not sign a market catalog, publish an OCI package or activate production isolation.
