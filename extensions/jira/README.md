# Jira issues

Work through your existing [Jira](https://www.atlassian.com/software/jira) issues in
Persona: see what is assigned to you, understand project/workflow context, read the
discussion, create an issue or add an explicitly requested comment. This is not an
arbitrary JQL or server-administration extension.

This is a fresh Persona Go extension using Jira's public REST v3 API. It ships no Raycast
source, assets, branding or runtime.

## Set up

1. Create a personal access token in Jira. The extension uses Bearer authentication, not
   your Jira account password.
2. In **Settings › Extensions › Jira**, set the **Jira instance URL** (for example
   `https://jira.example.com`, with a context path if used) and paste the **Jira personal
   access token**. Local development may use `http://127.0.0.1:8080`; remote instances
   require HTTPS.
3. The extension appends `/rest/api/3` when needed. Do not include credentials in the URL.

The token arrives through the encrypted extension-secret flow and is used only as a Bearer
token to the configured instance. Redirects are not followed.

## Read and plan

- The fixed query is `assignee = currentUser() AND resolution = Unresolved ORDER BY updated
  DESC`, so ordinary search text cannot become an unreviewed JQL operation.
- See project, workflow status, priority, assignee, issue key and pagination with the API's `nextPageToken`.
- Open an issue for its description and comments. Atlassian document paragraphs/text are
  flattened into untrusted text; no embedded instruction is executed.
- `jira_projects` resolves accessible project and issue-type IDs. Creating with a type that
  does not belong to the selected project, or with a subtask type, is refused.

## Confirmed changes

- `jira_create`: create with project, issue type, summary and optional description/assignee.
- `jira_comment`: add an explicitly supplied comment body to an existing issue.
- The page's create form shows the selected project/type and summary in a review step
  before confirmed creation. No bulk edit, deletion or arbitrary workflow mutation is
  included.

Before a write, the extension stores the normalized operation and request ID in
`write-receipts.json` in its own data directory (`0600`, without the token). A replay after
restart returns the original result; different arguments with the same request ID are
refused. Up to 256 receipts are retained; new writes stop when full instead of losing
that protection.

A timeout or lost response leaves the outcome **unknown**. Inspect Jira before retrying;
a failed response is not proof that no issue or comment was written.

## Verify

```sh
go -C extensions/jira test -race ./...
scripts/package.sh jira
```

Synthetic fixtures cover fixed JQL, rich-text comments, project/type validation, localized
views and persistent replay. No real workspace content is used.

## Public API

[Jira REST API v3](https://developer.atlassian.com/cloud/jira/platform/rest/v3/intro):
`/rest/api/3/search/jql`, `/rest/api/3/issue`, `/rest/api/3/issue/{key}/comment` and
`/rest/api/3/project`/`issuetype`.
