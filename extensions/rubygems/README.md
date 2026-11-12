# RubyGems

Search the official RubyGems registry from Persona. Compare a gem's current version, license, runtime dependencies, release date, download activity, documentation and repository before choosing a Ruby dependency.

## Usage

1. Enter a gem name or keyword.
2. Open a result to read its current-release metadata and any yanked warning.
3. Review the exact-version `gem install <name> -v <version>` command as text. The extension never runs it.

The UI and assistant tools are localized in English, Simplified Chinese and Japanese.

### Assistant tools

- `rubygems_search`: search by name or keyword with a bounded result limit.
- `rubygems_gem`: inspect a gem's current version, runtime dependencies and release metadata.

## Network and privacy

The hub uses anonymous read-only `GET` requests to `https://rubygems.org/api/v1/search.json` and `https://rubygems.org/api/v1/gems/<name>.json`, with the descriptive `persona-rubygems` User-Agent. No account, token, browser cookie, private registry, package author list or HTML scraping is used. The search query or gem name is sent to RubyGems.

No gem archive, extension compilation, installation, dependency resolution or lifecycle script is executed. Only bounded metadata is parsed and cached after validation. Requests are serialized and limited in size, result count, memory and time. Transient failures may show a previously validated cached result with an explicit error.

## Security and provenance

Gem names, versions, dates, counts, license declarations and HTTPS links are strictly validated. The response's canonical RubyGems project URL must match the gem name. Optional third-party HTTP or dangerous links are omitted rather than upgraded to a different origin. Yanked releases are explicitly marked. Download activity and a declared license do not prove safety, maintenance quality or the exact terms of every included file. Review gem code, maintainers, dependencies and its own license before installing.

## Development

```sh
npm ci --registry=https://registry.npmjs.org
npm test
npm run build:source
```

The deterministic source archive includes the bundle, source, tests, npm 11-compatible public-registry lockfile and official SDK authoring closure. Native TypeScript/JSX runs on the hub; clients do not execute extension JavaScript. Local packaging does not sign a market catalog, publish an OCI package or activate production isolation.
