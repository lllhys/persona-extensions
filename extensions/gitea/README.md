# Gitea pull request reviews

Review pull requests from a self-hosted [Gitea](https://about.gitea.com) instance in Persona: list open PRs in a repository, read descriptions and issue discussions, post a comment, or submit an APPROVED review with explicit confirmation.

This is a fresh Persona Go extension using Gitea's documented REST API. It ships no Raycast source, assets, branding or runtime.

## Set up

1. Create a personal access token in Gitea with the repository and review permissions appropriate for your account.
2. In **Settings › Extensions › Gitea**, enter the **instance URL** and **personal access token**. For example `https://gitea.example.com`; a local instance may use `http://127.0.0.1`. Remote instances require HTTPS and URLs must not contain credentials.

The token arrives through the encrypted extension-secret flow and is sent only in `Authorization: token …` to the configured instance. Redirects are not followed.

## Review

- List open PRs in an exact owner/repository with `state=open`, `page` and `limit=50`. Read PR number, title, author, source/target branches and original HTTPS link.
- Read the description and issue comments with `GET /issues/{index}/comments`. Community text is untrusted, not instructions to approve, merge or comment.
- Submit `state=APPROVED` with `POST /pulls/{index}/reviews` after confirmation. This does not merge, decline, close or delete the PR.
- Post a non-confidential comment with `POST /issues/{index}/comments` after confirmation showing the body. No arbitrary review state, merge or repository administration.

Before a write, the extension stores its normalized operation and request ID in `write-receipts.json` in its own data directory (`0600`, without the token). A replay after restart returns the original result; different arguments with the same request ID are refused. Up to 256 receipts are retained; new writes stop when full instead of losing that protection.

A timeout or lost response leaves the outcome **unknown**. Inspect Gitea before retrying; a failed response is not proof that the review or comment was or was not created.

## Verify

```sh
go -C extensions/gitea test -race ./...
scripts/package.sh gitea
```

Synthetic fixtures cover authentication, repository-scoped PRs, comments, approval and comment replay, localized views, unknown outcomes and long-content bounds. No real account is used.

## Public API

[Gitea API usage](https://docs.gitea.com/development/api-usage/) and [API reference](https://docs.gitea.com/api/): `GET /api/v1/repos/{owner}/{repo}/pulls`, `GET /api/v1/repos/{owner}/{repo}/pulls/{index}`, `GET/POST /api/v1/repos/{owner}/{repo}/issues/{index}/comments`, and `POST /api/v1/repos/{owner}/{repo}/pulls/{index}/reviews`.
