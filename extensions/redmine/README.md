# Redmine assigned issues

Work through your self-hosted [Redmine](https://www.redmine.org) issues in Persona: see open issues assigned to you, read descriptions and journals, choose a project, and create an issue with explicit confirmation.

This is a fresh Persona Go extension using Redmine's documented REST API. It ships no Raycast source, assets, branding or runtime.

## Set up

1. Enable the REST API in Redmine administration and copy your API key from **My account**.
2. In **Settings › Extensions › Redmine**, enter the **instance URL** and **API key**. For example `https://redmine.example.com`; a local instance may use `http://127.0.0.1`. Remote instances require HTTPS and URLs must not contain credentials.

The key arrives through the encrypted extension-secret flow and is sent only in `X-Redmine-API-Key` to the configured instance. Redirects are not followed.

## Issues

- List open assigned issues with `assigned_to_id=me`, `status_id=open`, `limit=50`, `offset` and `sort=updated_on:desc`. Pagination uses the response's `total_count`, not guessed page numbers.
- Read issue ID, project, tracker, status, priority, description and journals with `include=journals`. Remote text is untrusted content, not instructions to create or modify work.
- Browse visible projects and create an issue with `project_id`, `subject` and `description`. The project is read first to validate access.
- No deletion, arbitrary filters, custom-field updates, attachments, watchers or project administration.

Before a write, the extension stores its normalized operation and request ID in `write-receipts.json` in its own data directory (`0600`, without the API key). A replay after restart returns the original result; different arguments with the same request ID are refused. Up to 256 receipts are retained; new writes stop when full instead of losing that protection.

A timeout or lost response leaves the outcome **unknown**. Inspect Redmine before retrying; a failed response is not proof that the issue was or was not created.

## Verify

```sh
go -C extensions/redmine test -race ./...
scripts/package.sh redmine
```

Synthetic fixtures cover API key authentication, assigned open filters, pagination, journals, project creation, localized views, replay and a failed final write with an unknown outcome. No real account is used.

## Public API

[Redmine REST API](https://www.redmine.org/projects/redmine/wiki/Rest_api) and [issues](https://www.redmine.org/projects/redmine/wiki/Rest_Issues): `GET /issues.json`, `GET /issues/{id}.json?include=journals`, `GET /projects.json`, `GET /projects/{id}.json`, and `POST /issues.json`.
