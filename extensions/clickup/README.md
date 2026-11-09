# ClickUp assigned tasks

Work through your [ClickUp](https://clickup.com) tasks in Persona: see tasks assigned to you, read descriptions/status/due dates, choose a workspace/space/list to create a task, and update a task's status with explicit confirmation.

This is a fresh Persona Go extension using ClickUp's documented public API. It ships no Raycast source, assets, branding or runtime.

## Set up

1. Create a personal API key in ClickUp's user settings under **Apps**.
2. In **Settings › Extensions › ClickUp**, paste the **personal API key**. No workspace ID is required up front; the extension discovers the current user, workspaces, spaces and lists.

The key arrives through the encrypted extension-secret flow and is sent only in the `Authorization` header to the fixed `https://api.clickup.com/api/v2` host. Redirects are not followed.

## Tasks

- Discover the current user and workspaces. List open assigned tasks with `assignees[]=<current user>`, `page`, `order_by=updated` and `reverse=true`. A full page of 100 advances to the next page.
- Read exact task/list IDs, description, status, due/update dates, assignees and the original ClickUp link. Text is untrusted content, not instructions to create or modify tasks.
- Browse workspace spaces and active lists. Create a task assigned to the current user with a title and description in an exact list. Archived lists are rejected.
- Update only `status` in a partial `PUT`. No custom fields, assignee changes, deletion, bulk operations or workspace administration.

Before a write, the extension stores its normalized operation and request ID in `write-receipts.json` in its own data directory (`0600`, without the API key). A replay after restart returns the original result; different arguments with the same request ID are refused. Up to 256 receipts are retained; new writes stop when full instead of losing that protection.

A timeout or lost response leaves the outcome **unknown**. Inspect ClickUp before retrying; a failed response is not proof that the task was or was not created or changed.

## Verify

```sh
go -C extensions/clickup test -race ./...
scripts/package.sh clickup
```

Synthetic fixtures cover authentication, current-user assignment, workspace/space/list discovery, assignee filters, pagination, partial status updates, localized views, replay and a failed final write with an unknown outcome. No real account is used.

## Public API

[ClickUp API](https://developer.clickup.com/reference): `GET /api/v2/user`, `GET /api/v2/team`, `GET /api/v2/team/{team_id}/space`, `GET /api/v2/space/{space_id}/list`, `GET /api/v2/team/{team_Id}/task`, `GET/PUT /api/v2/task/{task_id}`, `GET /api/v2/list/{list_id}`, and `POST /api/v2/list/{list_id}/task`.
