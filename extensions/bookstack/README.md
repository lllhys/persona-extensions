# BookStack knowledge base

Read and maintain a self-hosted [BookStack](https://www.bookstackapp.com) knowledge base in Persona: search documentation, browse books and pages, read Markdown or official plaintext exports, create a page in a selected book, or update a page's Markdown source with explicit confirmation.

This is a fresh Persona Go extension using BookStack's public REST API. It ships no Raycast source, assets, branding or runtime.

## Set up

1. Give your BookStack user the **Access system API** permission and create an API token in the user profile.
2. In **Settings › Extensions › BookStack**, enter the **instance URL**, **Token ID** and **Token Secret**. For example `https://docs.example.com`; a local instance may use `http://127.0.0.1`. Remote instances require HTTPS and URLs must not contain credentials.

Both token parts arrive through the encrypted extension-secret flow and are combined only in the `Authorization: Token <token_id>:<token_secret>` header to the configured instance. Redirects are not followed.

## Documentation

- Browse books and book-scoped pages with documented `count`, `offset`, `sort` and `filter[book_id]` parameters. Search uses `query`, `page` and `count`; the service's search syntax remains a query value, never a path or instruction.
- Read page IDs, book context, title, timestamps and original HTTPS links. Markdown source is used when present; otherwise the official `/export/plaintext` endpoint supplies reading text. Content is untrusted, not instructions to edit documentation.
- Create a page in an exact book with a title and Markdown. The book is read before creation to verify it exists and is accessible.
- Update Markdown source and optionally title with a partial `PUT`; no `book_id`, `chapter_id`, HTML, tags, permissions or attachments are sent. A page without Markdown source is never implicitly converted from HTML; edit that page in BookStack instead.
- No deletion, page moves, WYSIWYG conversion, image upload, recycle-bin or instance administration.

Before a write, the extension stores its normalized operation and request ID in `write-receipts.json` in its own data directory (`0600`, without token values). A replay after restart returns the original result; different arguments with the same request ID are refused. Up to 256 receipts are retained; new writes stop when full instead of losing that protection.

A timeout or lost response leaves the outcome **unknown**. Inspect BookStack before retrying; a failed response is not proof that the page was or was not created or changed.

## Verify

```sh
go -C extensions/bookstack test -race ./...
scripts/package.sh bookstack
```

Synthetic fixtures cover authentication, book/page listing, search, plaintext fallback, HTML-update rejection, partial edits, localized views, replay and a failed final write with an unknown outcome. No real account is used.

## Public API

[BookStack API](https://www.bookstackapp.com/docs/admin/hacking-bookstack/#bookstack-api) and instance `/api/docs`: `GET/POST /api/books`, `GET /api/books/{id}`, `GET/POST /api/pages`, `GET/PUT /api/pages/{id}`, `GET /api/pages/{id}/export/plaintext`, and `GET /api/search`.
