# Internet Archive

Search public Internet Archive collections in Persona. No account is required.

- Search by title, creator or subject, with optional text, audio, movie or image filters.
- See title, creator, year, media type, download count and canonical item links.
- Ask the assistant with `archive_search`.

File format, media type and download count do not establish permission for reuse. Check the linked item's rights information before downloading or reusing material.

The native TypeScript/JSX program runs on the hub with the official `@persona/extension` SDK. Requests, result counts, response bodies, caches and cancellation are bounded. No arbitrary files are downloaded.

This source package is an original implementation for Persona. It does not contain Raycast code.

## Development

```sh
npm ci
npm test
npm run build:source
```

The build checks the official source bundle and writes a deterministic development archive. Hosted rebuild inputs are in `source-rebuild.json` and `source-reference/`. This does not sign a catalog or activate the extension in production.
