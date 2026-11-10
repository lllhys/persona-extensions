# Wiktionary

Look up multilingual definitions in Persona through public Wiktionary APIs. No account is required.

- English, Simplified Chinese and Japanese dictionaries, following the Persona locale by default.
- Definitions grouped by part of speech, with plain-text examples.
- Canonical Wiktionary entry links for authorship and licensing.
- Ask the assistant with `wiktionary_lookup`.

The native TypeScript/JSX program runs on the hub with the official `@persona/extension` SDK. Remote markup is projected to text, never executed as HTML or client JavaScript. Queries, response bodies, result counts, caches and cancellation are bounded.

This source package is an original implementation for Persona. It does not contain Raycast code.

## Development

```sh
npm ci
npm test
npm run build:source
```

The build checks the official source bundle and writes a deterministic development archive. Hosted rebuild inputs are in `source-rebuild.json` and `source-reference/`. This does not sign a catalog or activate the extension in production.
