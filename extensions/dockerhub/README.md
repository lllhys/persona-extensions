# Docker Hub

Search public Docker Hub repositories from Persona. Check an image's official status, publisher namespace, pull activity, selected tag digests and supported platforms before choosing a deployment dependency.

## Usage

1. Enter an image name or keyword.
2. Open a result to read repository metadata and selected active tags.
3. Review the `docker pull <namespace>/<name>` command as text. The extension never runs it.

Repository details use `namespace/name`; official images use `library/name`, for example `library/nginx`.

The UI and assistant tools are localized in English, Simplified Chinese and Japanese.

### Assistant tools

- `dockerhub_search`: search public repositories with a bounded result limit.
- `dockerhub_repository`: inspect a repository and its selected tag metadata.

## Network and privacy

The hub uses anonymous read-only `GET` requests to `https://hub.docker.com/v2/search/repositories/`, `https://hub.docker.com/v2/repositories/<namespace>/<name>/` and its `tags?page_size=10` endpoint, with the descriptive `persona-dockerhub` User-Agent. No account, token, browser cookie, private registry, HTML scraping or container daemon is used. The search query or repository name is sent to Docker Hub.

No image pull, manifest download, container execution, image build or registry write is performed. Only bounded metadata is parsed and cached after validation. Requests are serialized and limited in size, result count, memory and time. Transient failures may show a previously validated cached result with an explicit error.

## Security and provenance

Repository names, response identities, tag names, digests, counts, dates and platform identifiers are strictly validated. The tag digest is the tag's reported manifest or index digest. Platform-specific image digests are validated but not substituted for the multi-platform pull reference. Non-executable `unknown/unknown` metadata entries are omitted. Official status and pull activity do not prove image safety or maintenance quality; review the publisher, tag and image contents before executing.

## Development

```sh
npm ci --registry=https://registry.npmjs.org
npm test
npm run build:source
```

The deterministic source archive includes the bundle, source, tests, npm 11-compatible public-registry lockfile and official SDK authoring closure. Native TypeScript/JSX runs on the hub; clients do not execute extension JavaScript. Local packaging does not sign a market catalog, publish an OCI package or activate production isolation.
