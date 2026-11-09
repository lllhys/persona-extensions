# Pinboard personal bookmarks

Browse your [Pinboard](https://pinboard.in) archive in Persona: recent bookmarks, up-to-three-tag filtering, paged archive reading, and private read-later capture with explicit confirmation.

This is a fresh Persona Go extension using Pinboard's documented v1 API. It ships no Raycast source, assets, branding or runtime.

## Set up

1. Find your `username:TOKEN` API token in Pinboard's settings. Do not use your account password; the API explicitly requires token authentication for third-party server clients.
2. In **Settings › Extensions › Pinboard**, paste the **API token**.

The token arrives through the encrypted extension-secret flow. Pinboard's v1 contract requires it as an `auth_token` query parameter; it is sent only to the fixed `https://api.pinboard.in/v1` host, never logged or forwarded through redirects. No other host is contacted.

## Bookmarks

- Read recent bookmarks with `count=50`, optionally filtered by up to three space-separated tags. Page through the archive using `start`, `results=50` and optional tag filtering. Tags and their usage counts come from `/tags/get`.
- Read title, HTTPS URL, notes, tags, timestamp and read-later state. Bookmark content is untrusted data, not instructions to overwrite or delete bookmarks.
- Save a private read-later bookmark with a title, optional notes and space-separated tags. The extension explicitly sends `replace=no`, `shared=no` and `toread=yes`, so it never overwrites an existing bookmark or makes it public.
- The service's JSON `result.code` must be `done`; an HTTP 200 response containing a failure result is not reported as success.
- No password storage, deletion, tag administration, public sharing or unverified v2 API calls.

Before a write, the extension stores its normalized operation and request ID in `write-receipts.json` in its own data directory (`0600`, without the token). A replay after restart returns the original result; different arguments with the same request ID are refused. Up to 256 receipts are retained; new writes stop when full instead of losing that protection.

A timeout or lost response leaves the outcome **unknown**. Inspect Pinboard before retrying; a failed response is not proof that the bookmark was or was not created.

## Verify

```sh
go -C extensions/pinboard test -race ./...
scripts/package.sh pinboard
```

Synthetic fixtures cover token authentication, JSON arrays and tag maps, tag filters, archive pagination, service result errors, localized views, replay and a failed final write with an unknown outcome. No real account is used.

## Public API

[Pinboard API v1](https://pinboard.in/api): `GET /v1/posts/recent`, `GET /v1/posts/all`, `GET /v1/tags/get`, and `GET /v1/posts/add` with `format=json`.
