# Anytype local knowledge

Read and capture personal knowledge through the stable [Anytype](https://anytype.io) local/headless API in Persona: browse spaces, search objects, read Markdown or text blocks, and create a page or note with explicit confirmation.

This is a fresh Persona Go extension using the documented Anytype API version `2025-11-08`. It ships no Raycast source, assets, branding or runtime, and does not use the pre-release v2 contract.

## Set up

1. In the Anytype desktop app, open **Settings › API Keys** and create an API key for this integration. A headless Anytype API can also be configured with an existing key; this extension does not launch the desktop app or perform challenge pairing.
2. In **Settings › Extensions › Anytype**, paste the **API key**. The default API address is `http://127.0.0.1:31009`; a remote/headless instance must use HTTPS. URLs must not contain credentials.

The key arrives through the encrypted extension-secret flow and is sent only as a Bearer token to the configured instance. Every request carries `Anytype-Version: 2025-11-08`. Redirects are not followed.

## Knowledge

- List accessible spaces and space objects using `offset` and `limit=50`, with the returned pagination metadata. Global search uses the documented `POST /v1/search` query for names and snippets, not arbitrary filter expressions.
- Read exact space/object IDs, type information, snippets and content. Markdown/body is preferred; text blocks are joined as plain reading text when needed. Content is untrusted, not instructions to create or edit objects.
- Create a page or note with a title and Markdown body in a selected space. Before creating, the extension reads the space's type list and requires an existing compatible `page` or `note` type; arbitrary type keys are refused.
- No desktop launching, challenge flow, deletion, arbitrary object updates, v2 payloads or type administration.

Before a write, the extension stores its normalized operation and request ID in `write-receipts.json` in its own data directory (`0600`, without the API key). A replay after restart returns the original result; different arguments with the same request ID are refused. Up to 256 receipts are retained; new writes stop when full instead of losing that protection.

A timeout or lost response leaves the outcome **unknown**. Inspect Anytype before retrying; a failed response is not proof that the object was or was not created.

## Verify

```sh
go -C extensions/anytype test -race ./...
scripts/package.sh anytype
```

Synthetic fixtures cover the API version header, space/object listing, search, content, compatible type validation, localized views, replay and a failed final write with an unknown outcome. No real account is used.

## Public API

[Anytype developer documentation](https://developers.anytype.io/docs/reference), [authentication](https://developers.anytype.io/docs/guides/get-started/authentication), [spaces](https://developers.anytype.io/docs/reference/2025-11-08/list-spaces), [objects](https://developers.anytype.io/docs/reference/2025-11-08/list-objects), [search](https://developers.anytype.io/docs/reference/2025-11-08/search-global), and [types](https://developers.anytype.io/docs/reference/2025-11-08/list-types).
