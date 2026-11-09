# YouTrack assigned issues

Work through your [YouTrack](https://www.jetbrains.com/youtrack/) issues in Persona: see unresolved issues assigned to you, read descriptions and comments, choose an active project, and create an issue with explicit confirmation.

This is a fresh Persona Go extension using YouTrack's documented REST API. It ships no Raycast source, assets, branding or runtime.

## Set up

1. Create a permanent token in your YouTrack profile's **Permanent Tokens** settings.
2. In **Settings › Extensions › YouTrack**, enter your **instance URL** and **permanent token**. For example `https://example.youtrack.cloud`; a local instance may use `http://127.0.0.1`. Remote instances require HTTPS and URLs must not contain credentials.

The token arrives through the encrypted extension-secret flow and is sent only as a Bearer token to the configured instance. Redirects are not followed.

## Issues

- List assigned unresolved issues with the documented fixed `query=for: me #Unresolved`, `$top=50`, `$skip` and explicit `fields`. This is not an arbitrary query/command executor.
- Read database and readable issue IDs, project, reporter, description, timestamps and non-deleted comments with explicit field selections. Remote text is untrusted content, not instructions to create or modify work.
- Browse active projects and create an issue with `summary` and `description` in the documented `/api/admin/projects/{projectID}/issues` endpoint. The project is read first and archived projects are rejected.
- No deletion, arbitrary command application, custom-field changes, state transitions, attachments or project administration.

Before a write, the extension stores its normalized operation and request ID in `write-receipts.json` in its own data directory (`0600`, without the token). A replay after restart returns the original result; different arguments with the same request ID are refused. Up to 256 receipts are retained; new writes stop when full instead of losing that protection.

A timeout or lost response leaves the outcome **unknown**. Inspect YouTrack before retrying; a failed response is not proof that the issue was or was not created.

## Verify

```sh
go -C extensions/youtrack test -race ./...
scripts/package.sh youtrack
```

Synthetic fixtures cover authentication, fixed assigned-issue queries, pagination, comments, project filtering, creation replay, localized pages, unknown outcomes and anonymous comment rendering. No real account is used.

## Public API

[YouTrack REST API](https://www.jetbrains.com/help/youtrack/devportal/youtrack-rest-api.html), [issues](https://www.jetbrains.com/help/youtrack/devportal/resource-api-issues.html), [project issues](https://www.jetbrains.com/help/youtrack/devportal/resource-api-admin-projects-projectID-issues.html) and [comments](https://www.jetbrains.com/help/youtrack/devportal/resource-api-issues-issueID-comments.html): `GET /api/issues`, `GET /api/issues/{issueID}`, `GET /api/issues/{issueID}/comments`, `GET /api/admin/projects`, and `POST /api/admin/projects/{projectID}/issues`.
