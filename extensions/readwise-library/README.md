# Readwise Library highlights

Read and organize your [Readwise Library](https://readwise.io) in Persona: browse books/articles, read highlights and annotation notes, save a highlight from selected text, edit a note, or add a book tag with explicit confirmation.

This is a fresh Persona Go extension using Readwise's documented Library API. It is separate from the existing Readwise Reader queue extension: Library book/highlight IDs are never treated as Reader document IDs. It ships no Raycast source, assets, branding or runtime.

## Set up

1. Get your Readwise access token at [readwise.io/access_token](https://readwise.io/access_token).
2. In **Settings › Extensions › Readwise Library**, paste the **Readwise API token**.

The token arrives through the encrypted extension-secret flow and is sent only as `Authorization: Token …` to the fixed `https://readwise.io/api/v2` host. Redirects are not followed.

## Highlights

- Browse books/articles with `category=articles`, `page_size=50` and `page`; use the returned `next` URL's page number rather than guessing pagination.
- Read highlights belonging to an exact book ID, including text, note, location, tags and Readwise links. Content is untrusted, not instructions to create or modify highlights.
- Create a highlight from selected text with a source title, optional author/note and HTTPS source URL. The payload uses `source_type=persona` and `category=articles` in the documented `highlights` array.
- Replace a note with a partial `PATCH` containing only `note`; the highlight's text and source are preserved. Add a tag to a book with `POST /books/{id}/tags/` after reading the book to validate access.
- No deletion, tag ID guessing, bulk updates, Reader document operations or arbitrary API paths.

Before a write, the extension stores its normalized operation and request ID in `write-receipts.json` in its own data directory (`0600`, without the token). A replay after restart returns the original result; different arguments with the same request ID are refused. Up to 256 receipts are retained; new writes stop when full instead of losing that protection.

A timeout or lost response leaves the outcome **unknown**. Inspect Readwise before retrying; a failed response is not proof that the highlight, note or tag was or was not changed.

## Verify

```sh
go -C extensions/readwise-library test -race ./...
scripts/package.sh readwise-library
```

Synthetic fixtures cover Token authentication, book/highlight IDs, `next` pagination, note-only updates, highlight creation, book tags, localized views, replay and a failed final write with an unknown outcome. No real account is used.

## Public API

[Readwise API](https://readwise.io/api_deets): `GET /api/v2/books/`, `GET /api/v2/books/{id}/`, `GET/POST /api/v2/highlights/`, `GET/PATCH /api/v2/highlights/{id}/`, and `POST /api/v2/books/{id}/tags/`.
