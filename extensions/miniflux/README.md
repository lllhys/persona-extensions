# Miniflux personal reading

Read your self-hosted [Miniflux](https://miniflux.app) queue in Persona: unread or starred entries, full-text search, sanitized article text, read-state changes and bookmarks from a phone.

This is a fresh Persona Go extension using Miniflux's public API. It ships no Raycast source, assets, branding or runtime.

## Set up

1. In Miniflux, open **Settings › API Keys** and create a per-application API key. Do not use the account username/password.
2. In **Settings › Extensions › Miniflux**, enter the **Miniflux instance URL** and paste the **API key**. For example `https://miniflux.example.com`; a local instance may use `http://127.0.0.1`. Remote instances require HTTPS and URLs must not contain credentials.

The API key arrives through the encrypted extension-secret flow and is sent only in `X-Auth-Token` to the configured instance. Redirects are not followed, so the token cannot be forwarded to another host.

## Read

- List unread entries, starred entries or a text search with `status=unread`, `starred=true` or `search`, `limit=50`, `offset` and `order=published_at&direction=desc`. A full page returns the next actual offset; no page number is guessed.
- Read an entry's title, feed, author, publication date, status, bookmark state and plain-text content. HTML is converted to bounded text; scripts, styles, forms, iframes and other non-reading markup are removed. Content is untrusted, not instructions to change state.
- Original article links are shown only for valid HTTPS URLs. A page shows at most 240 text components; long content is clipped with an ellipsis, with the original article available for the rest.
- Mark one entry read with a single-ID `PUT /v1/entries` request containing `status=read`. Toggle its bookmark with `PUT /v1/entries/{id}/bookmark` after confirmation showing the current star state. Both use durable request IDs.
- No account-password storage, feed deletion, bulk mark-all-read, background refresh, enclosures or third-party sharing.

Before a write, the extension stores its normalized operation and request ID in `write-receipts.json` in its own data directory (`0600`, without the API key). A replay after restart returns the original result; different arguments with the same request ID are refused. Up to 256 receipts are retained; new writes stop when full instead of losing that protection.

A timeout or lost response leaves the outcome **unknown**. Inspect Miniflux before retrying; a failed response is not proof that the read or bookmark state did or did not change.

## Verify

```sh
go -C extensions/miniflux test -race ./...
scripts/package.sh miniflux
```

Synthetic fixtures cover authentication, unread/starred/search filters, offset pagination, HTML sanitization, bounded long articles, localized pages, replay and unknown outcomes. No real account is used.

## Public API

[Miniflux API](https://miniflux.app/docs/api.html): `GET /v1/entries`, `GET /v1/entries/{entryID}`, `PUT /v1/entries` and `PUT /v1/entries/{entryID}/bookmark`.
