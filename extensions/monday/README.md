# monday boards and items

Work through your [monday.com](https://monday.com) boards in Persona: browse accessible boards, read item status/column values, create an item, or update an existing status label with explicit confirmation.

This is a fresh Persona Go extension using monday's documented GraphQL API. It ships no Raycast source, assets, branding or runtime, and does not accept arbitrary GraphQL from tools.

## Set up

1. Get your personal V2 API token in monday's Developer Center.
2. In **Settings › Extensions › monday**, paste the **personal V2 API token**.

The token arrives through the encrypted extension-secret flow and is sent only in the `Authorization` header to the fixed `https://api.monday.com/v2` host. Redirects are not followed. GraphQL `errors` are treated as failures even when the HTTP status is 200.

## Work

- List accessible boards and their exact IDs. Read `items_page` with `limit=50`, the returned cursor and item IDs/names/status/column values. No arbitrary query or filter is supplied by tools.
- Read board columns and status settings before editing. Status updates are limited to an existing label on an existing status column; a new label is never created implicitly.
- Create an item with only `board_id` and `item_name` after confirming the board. Update a status with `change_multiple_column_values` using the documented JSON string format for status labels.
- No arbitrary GraphQL, label creation, arbitrary column edits, deletion, board/workspace administration or bulk operations.

Before a write, the extension stores its normalized operation and request ID in `write-receipts.json` in its own data directory (`0600`, without the token). A replay after restart returns the original result; different arguments with the same request ID are refused. Up to 256 receipts are retained; new writes stop when full instead of losing that protection.

A timeout or lost response leaves the outcome **unknown**. Inspect monday before retrying; a failed response is not proof that the item was or was not created or changed.

## Verify

```sh
go -C extensions/monday test -race ./...
scripts/package.sh monday
```

Synthetic GraphQL fixtures cover authentication, board info, cursor pagination, HTTP-200 application errors, existing-label validation, partial writes, localized views, replay and a failed final write with an unknown outcome. No real account is used.

## Public API

[Authentication](https://developer.monday.com/api-reference/docs/authentication), [board items](https://developer.monday.com/api-reference/docs/get-board-items), [create item](https://developer.monday.com/api-reference/docs/create-item), [column updates](https://developer.monday.com/api-reference/docs/change-item-column-values), and [error handling](https://developer.monday.com/api-reference/docs/error-handling): `me`, `boards`, `items_page`, `create_item`, and `change_multiple_column_values` on `https://api.monday.com/v2`.
