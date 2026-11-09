# Raindrop bookmarks

Browse and search your personal [Raindrop](https://raindrop.io) library in Persona.
Read notes and highlights, open the original link in the app, or ask the assistant to save
an article with a collection and tags.

This is a new Persona Go extension using the public Raindrop REST API, not a desktop UI
compatibility layer. No Raycast source, assets, branding or runtime is shipped.

## Set up

1. Open [Raindrop's integrations settings](https://app.raindrop.io/settings/integrations)
   and create an application in **For Developers**.
2. Create a **Test Token** for that application. Test tokens have access to your account;
   do not put one in a chat, tool argument or configuration field.
3. In Persona's **Settings › Extensions › Raindrop**, paste it into **Raindrop access token**.
   It is delivered through the encrypted extension-secret flow and held only in the running
   program's memory. API requests send it solely as the `Authorization: Bearer` header to
   `api.raindrop.io`.
4. Optionally set the numeric collection ID used when the page opens. `0` means all bookmarks
   and `-1` means Unsorted.

Only that one network host is requested. The extension does not read browser history, local
bookmark files, the original article or Raindrop's permanent-copy redirect. Bookmark content
is untrusted text, not an instruction to perform actions.

## Use

The **Bookmarks** page offers recent bookmarks, full-text search, collection filtering and
progressive loading of up to six pages. The collection filter shows up to 20 destinations;
every collection remains available in the save form and through the assistant.

Ask the assistant, for example:

- “Find the articles I saved about local-first software.”
- “What did I highlight in that bookmark?”
- “Save `https://example.com/article` to Research / AI with the tags `reading` and `local-first`.”

`raindrop_search` returns numeric bookmark IDs and URLs. `raindrop_read` reads notes and
highlights without fetching a third-party page. `raindrop_collections` resolves full parent
paths before a named destination is used. Exact tag filters use Raindrop's quoted tag syntax.

Saving creates remote data. The assistant uses `raindrop_save`, declared in `confirm_always`,
so Persona's connector gate must approve it. The confirmation describes the URL, destination,
tags and any title or note. A named collection must have been read first; the confirmation
validation is memory-only and verifies the name/ID pair.

The page's save form first shows a review screen. **Confirm save** executes only after that
screen and the client's explicit confirmation. Unsorted is the default; `0` (All bookmarks)
is never accepted as a save destination, and `-99` (Trash) is not accepted for reading or
saving.

## Save delivery and data

Before contacting Raindrop, the extension atomically stores the request ID and normalized
arguments in `save-receipts.json` in its own data directory (`0600`, without the token).
A duplicate request returns the original bookmark instead of creating another one. Reusing
an ID with different arguments is refused. Up to 256 receipts are kept; the extension stops
accepting new saves when full rather than silently losing the replay guarantee.

A timeout or an interrupted process leaves the request marked **unknown**. The extension asks
the user to inspect Raindrop before retrying instead of assuming the write failed. This is a
replay guard, not a claim of exactly-once delivery across a lost network response or deletion
of the extension's data directory. The assistant skill describes the same boundary.

## Verify

```sh
go -C extensions/raindrop test -race ./...
scripts/package.sh raindrop
```

Tests use synthetic API responses, including an integer ID beyond JavaScript's exact-Number
range, and never need a real Raindrop account. They cover SDK conformance, nested collection
resolution, bounded zero-based pagination, tag matching, URL and text validation, long notes,
localized views, write-gate arguments and persisted replay handling.

## Public API

- [Authentication and authorized calls](https://developer.raindrop.io/v1/authentication/calls)
- [Collections](https://developer.raindrop.io/v1/collections/methods)
- [Single bookmark and save](https://developer.raindrop.io/v1/raindrops/single)
- [Multiple bookmarks and pagination](https://developer.raindrop.io/v1/raindrops/multiple)
- [Highlights](https://developer.raindrop.io/v1/highlights)
- [API terms and guidelines](https://developer.raindrop.io/terms)
