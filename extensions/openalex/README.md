# OpenAlex

Discover research papers in Persona through public OpenAlex metadata. No account is required.

- Search by topic, title or DOI.
- See publication year, citation count, source, license and explicit open-access metadata.
- Follow canonical OpenAlex, DOI and service-provided fulltext links.
- Ask the assistant with `openalex_search`.

Citation count does not establish fulltext access or permission to reuse a paper. Check the linked work and its license before downloading or reusing material.

The native TypeScript/JSX program runs on the hub with the official `@persona/extension` SDK. Requests, result counts, response bodies, caches and cancellation are bounded.

This source package is an original implementation for Persona. It does not contain Raycast code.

## Development

```sh
npm ci
npm test
npm run build:source
```

The build checks the official source bundle and writes a deterministic development archive. Hosted rebuild inputs are in `source-rebuild.json` and `source-reference/`. This does not sign a catalog or activate the extension in production.
