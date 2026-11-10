# Open Library

Discover books in Persona through the public Open Library catalog. No account is required.

- Search by title, author, keywords or ISBN.
- See authors, first publication year, edition count and catalog availability.
- Read work descriptions and subjects, with canonical Open Library links.
- Public-reading links appear only when catalog metadata identifies a public Gutenberg scan. Restricted editions are not described as freely readable.
- Ask the assistant to search books or read a work's details with `openlibrary_search` and `openlibrary_work`.

The native TypeScript/JSX program runs on the hub with the official `@persona/extension` SDK. Remote text is data, not executable HTML or client JavaScript. Requests, results, response bodies and caches are bounded.

This source package is an original implementation for Persona. It does not contain Raycast code.

## Development

```sh
npm ci
npm test
npm run build:source
```

The build checks the official source bundle and writes a deterministic development archive. It does not sign a catalog or activate the extension in production.
