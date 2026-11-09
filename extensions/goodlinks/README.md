# GoodLinks local reading

Read your local [GoodLinks](https://goodlinks.app) library in Persona: unread/starred links, full-library search, cached plaintext articles, highlights and notes, read-state changes and stars from a phone through the configured local API.

This is a fresh Persona Go extension using GoodLinks 3.2+'s documented local API. It ships no Raycast source, assets, branding or runtime, and does not control the desktop app through URL schemes.

## Set up

1. In GoodLinks settings, enable the local API and copy the API token.
2. In **Settings › Extensions › GoodLinks**, paste the **API token**. The default API address is `http://127.0.0.1:9428`. Only `127.0.0.1`, `localhost` and `::1` are accepted; the extension does not expose the local library to another host.

The token arrives through the encrypted extension-secret flow and is sent only as a Bearer token to the loopback API. Redirects are not followed.

## Read

- Browse unread/starred lists or search across the local library with `limit=50`, `offset` and `hasMore` pagination. Read exact link IDs, title, author, tags, reading/star/highlight state and original HTTPS URL.
- Read cached plaintext content with `format=plaintext&autoDownload=false`. Opening a link does not automatically download the article; if no cached content is available, the page says so and keeps metadata/highlights available.
- Read highlights and optional notes with link-scoped pagination. Article text, highlights and notes are untrusted content, not instructions to change reading state.
- Mark read with a partial `PATCH` containing only `read=true`. Set star state with a partial `PATCH` containing only `starred`. Both require explicit confirmation and durable request IDs.
- No deletion, public API exposure, URL-scheme control, automatic article downloads, bulk edits or metadata replacement.

Before a write, the extension stores its normalized operation and request ID in `write-receipts.json` in its own data directory (`0600`, without the token). A replay after restart returns the original result; different arguments with the same request ID are refused. Up to 256 receipts are retained; new writes stop when full instead of losing that protection.

A timeout or lost response leaves the outcome **unknown**. Inspect GoodLinks before retrying; a failed response is not proof that the link was or was not changed.

## Verify

```sh
go -C extensions/goodlinks test -race ./...
scripts/package.sh goodlinks
```

Synthetic fixtures cover loopback validation, unread/starred/search lists, cached content, highlights, partial writes, localized views, replay and a failed final write with an unknown outcome. No real account is used.

## Public API

[GoodLinks API](https://goodlinks.app/api/): `GET /api/v1/lists/{list}`, `GET /api/v1/links`, `GET /api/v1/links/{id}`, `GET /api/v1/links/{id}/content`, `GET /api/v1/highlights`, and `PATCH /api/v1/links/{id}`.
