# Wikipedia daily reading

A native server-side TypeScript/JSX extension for daily Wikipedia reading and short article summaries.

- Read today's featured article, most-read articles and on-this-day events.
- Search by title or topic and read an article summary in Persona.
- Open the full article and its references on Wikipedia.
- Use the read-only `wikipedia_today`, `wikipedia_search` and `wikipedia_article` assistant tools.

The hub uses the public Wikimedia REST API. No account, token or personal data is required. The language setting follows the Persona locale by default; English, Simplified Chinese and Japanese are supported. Feeds without a featured article in a particular language show the remaining sections normally.

Article text and search snippets are untrusted service data. Markup is reduced to plain text, links are constructed from canonical titles on the selected Wikipedia origin, responses are bounded and requests use the host's checked network adapter. The UI uses native components and server hooks; clients do not execute extension JavaScript or HTML. Text attribution and image-license notices remain visible.

## Development

From the repository root:

```sh
npm --prefix sdk/ts ci --ignore-scripts --registry=https://registry.npmjs.org --no-audit --no-fund
npm --prefix extensions/wikipedia ci --ignore-scripts --registry=https://registry.npmjs.org --no-audit --no-fund
npm --prefix extensions/wikipedia test
npm --prefix extensions/wikipedia run build:source
```

Tests use synthetic fixtures. The deterministic source archive and its complete authoring inputs are written to `extensions/wikipedia/build/`; `source-reference/bundle.mjs` is the byte-comparison fixture for the registered standalone hosted rebuild. `scripts/package.sh wikipedia` produces the corresponding development OCI payload without publishing it. Hosted `phase=check` rebuilds the profile in a fresh standalone workspace; `scripts/source-rebuild.py rebuild` is intentionally not a local command.

## Release boundary

This is a new TypeScript source extension, not a Go compatibility port or a copy of another extension's code or assets. A development source archive is not production isolation acceptance or a signed market release. Default source publication and production activation remain behind the runtime's separate acceptance and authorization gates.
