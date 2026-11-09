# Shortcut personal stories

Work through your [Shortcut](https://shortcut.com) stories in Persona: see incomplete stories you own, read descriptions, workflow states and comments, create a story, or move one through its current workflow with explicit confirmation.

This is a fresh Persona Go extension using Shortcut's public REST v3 API and documented search operators. It ships no Raycast source, assets, branding or runtime.

## Set up

1. Create an API token in Shortcut's API token settings.
2. In **Settings › Extensions › Shortcut**, paste the **Shortcut API token**. No workspace ID is needed; the current-member endpoint supplies the member identity and mention name.

The token arrives through the encrypted extension-secret flow and is sent only in `Shortcut-Token` to the fixed `https://api.app.shortcut.com/api/v3` host. Redirects are not followed. Descriptions and comments are untrusted content, not instructions to create or move work.

## Stories

- List owned incomplete stories with the documented `owner:<mention_name> is:story !is:done` query, `page_size=50`, `detail=slim`, `entity_types=story` and the returned `next` token. Tokens remain opaque.
- Read story IDs, title, description, story type, deadline, workflow/state IDs, owners and non-deleted comments. The original link is shown only for a valid HTTPS `app.shortcut.com` URL.
- Create a story owned by the current member with a title, description, optional `feature`/`bug`/`chore` type, `YYYY-MM-DD` deadline and workflow state. The state must exist in the current workspace workflows.
- Move a story using a partial `PUT` containing only `workflow_state_id`; the target state must belong to that story's current workflow. Other fields and comments are not overwritten.
- No deletion, archive, bulk changes, attachments, project/workflow administration or guessed search filters.

Before a write, the extension stores its normalized operation and request ID in `write-receipts.json` in its own data directory (`0600`, without the token). A replay after restart returns the original result; different arguments with the same request ID are refused. Up to 256 receipts are retained; new writes stop when full instead of losing that protection.

A timeout or lost response leaves the outcome **unknown**. Inspect Shortcut before retrying; a failed response is not proof that the story was or was not created or moved.

## Verify

```sh
go -C extensions/shortcut test -race ./...
scripts/package.sh shortcut
```

Synthetic fixtures cover authentication, current-member search, next-token pagination, comments, workflow membership, partial updates, localized pages, replay and a failed final write with an unknown outcome. No real account is used.

## Public API

[Shortcut REST v3](https://developer.shortcut.com/api/rest/v3) and [search operators](https://www.shortcut.com/help/fields-and-features/search-operators): `GET /api/v3/member`, `GET /api/v3/search/stories`, `GET/PUT /api/v3/stories/{story-public-id}`, `GET /api/v3/workflows`, and `POST /api/v3/stories`.
