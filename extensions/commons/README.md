# Wikimedia Commons

Find reference images in Persona through public Wikimedia Commons APIs. No account is required.

- Search by subject, place or file title.
- See descriptions, artists, license names and canonical Commons file pages.
- Display service-provided thumbnails through the official SDK's image components.
- Ask the assistant with `commons_search`, returning attribution and license links.

License metadata is displayed as supplied by the service. Categories alone do not establish permission, and a missing license name is never described as public domain. Check the linked file page before reuse.

The native TypeScript/JSX program runs on the hub with the official `@persona/extension` SDK. Remote markup becomes plain text, never executable HTML or client JavaScript. Requests, result counts, response bodies, caches and cancellation are bounded.

This source package is an original implementation for Persona. It does not contain Raycast code.

## Development

```sh
npm ci
npm test
npm run build:source
```

The build checks the official source bundle and writes a deterministic development archive. Hosted rebuild inputs are in `source-rebuild.json` and `source-reference/`. This does not sign a catalog or activate the extension in production.
