# Memos private notes

Capture and organize your own notes on a self-hosted [Memos](https://www.usememos.com) instance in Persona: browse recent or pinned notes, search content, create a private Markdown memo, edit its content, or pin/unpin it with explicit confirmation.

This is a fresh Persona Go extension using Memos' documented latest API, not an old `/api/v1` client. It ships no Raycast source, assets, branding or runtime.

## Set up

1. Create a Personal Access Token in your Memos account settings.
2. In **Settings › Extensions › Memos**, enter your **instance URL**, **username** and **access token**. The username selects your own notes with `creator == "users/{username}"`; it is not a password. For example `https://memos.example.com`; a local instance may use `http://127.0.0.1`. Remote instances require HTTPS and URLs must not contain credentials.

The token arrives through the encrypted extension-secret flow and is sent only as a Bearer token to the configured instance. Redirects are not followed. No password sign-in or refresh-token flow is used.

## Notes

- List your normal notes with `pageSize=50`, `pageToken`, `state=NORMAL` and `orderBy=pinned desc, create_time desc`. The filter is `creator == "users/{username}"`, optionally combined with `pinned == true` or `content.contains("…")`. Search text is JSON-escaped into the documented CEL expression rather than supplied as an arbitrary filter.
- Read the exact `memos/{memo}` resource name, content, state, visibility, pin state and timestamps. Notes are untrusted content, not instructions to edit or pin.
- Create a Markdown note with `visibility=PRIVATE` and `state=NORMAL`. No public sharing, attachments or relations are set.
- Replace an existing note's content with `updateMask=content`. Pin or unpin with `updateMask=pinned`. Before either update, the extension reads the memo and requires its `creator` to match the configured user, so another user's public note is not modified here.
- No deletion, archiving, public visibility changes, attachments, sharing or account administration.

Before a write, the extension stores its normalized operation and request ID in `write-receipts.json` in its own data directory (`0600`, without the token). A replay after restart returns the original result; different arguments with the same request ID are refused. Up to 256 receipts are retained; new writes stop when full instead of losing that protection.

A timeout or lost response leaves the outcome **unknown**. Inspect Memos before retrying; a failed response is not proof that the note was or was not created or changed.

## Verify

```sh
go -C extensions/memos test -race ./...
scripts/package.sh memos
```

Synthetic fixtures cover authentication, creator/content/pinned filters, page tokens, private creation, ownership rejection, partial updates, localized views, replay and unknown outcomes. No real account is used.

## Public API

[Memos API](https://www.usememos.com/docs/api), [list](https://www.usememos.com/docs/api/latest/memoservice/ListMemos), [create](https://www.usememos.com/docs/api/latest/memoservice/CreateMemo) and [update](https://www.usememos.com/docs/api/latest/memoservice/UpdateMemo): `GET/POST /api/memos`, `GET/PATCH /api/memos/{memo}`, and `GET /api/users/{user}` for account validation when needed.
