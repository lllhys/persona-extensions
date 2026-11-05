# GitHub

[Documentation](../README.md) · [Source](../../extensions/github/)

This extension brings GitHub into Persona. It is the GitHub connector that Persona's hub used to have built in, moved out of the hub, plus a page of what needs you on GitHub. The connector id is still `github`. A hub that had its own connector therefore keeps its sign-in, inbox items, knowledge-base conversations, connector switches and inbox options when the extension takes over. While the extension is on, its connector stands in for the hub's own. When the extension is switched off or removed, the hub's own connector comes back.

| Extension point | Contribution | Default | Behavior |
| --- | --- | --- | --- |
| `connectors` | `github` | on | Inbox, knowledge-base archive, assistant tools, gated writes, thread pages and sign-in; see [the connector](#the-connector). |
| `ui` | `github` (`nav_page`) | off | The GitHub page in the apps' navigation; see [the page](#the-page). Switch it on under Settings → Extensions → GitHub. |

Switching the extension on turns on what the hub already did before (the connector). The page is new, so it starts off.

## The connector

- **Inbox.** It polls GitHub's notifications at the pace GitHub asks for (`X-Poll-Interval`, at least a minute) and keeps one item per PR or issue. Kinds:
  - `review_requested`, `mention`, `team_mention`, `assign`;
  - `author`: new comments or reviews on your own PR or issue;
  - `ci_failed`: a failing GitHub Actions run of yours, followed up until the branch is green, merged or deleted.

  Each item carries a fallback tier from the connector's rules: a review request is "needs you", a bot's comment is "FYI", and a person's ordinary comment falls back to "FYI". Items therefore never wait for a triage model. Your own rules (`triage_rules`) win over the inbox's global rules. Muted repositories, which CI failures to flag and which branches to ignore are connector options. You change them in the app (Settings → Connectors → GitHub), and the hub hands them to the extension.
- **Knowledge base.** PRs and issues you are involved in (`involves:@me`) are archived with their comments and reviews as conversations `github-<owner>-<repo>-<number>`. The first run looks back 30 days.
- **Assistant tools.** These read without asking: `github_search`, `github_read`, `github_diff`, `github_checks`. `github_comment` and `github_review` are writes. They go through the hub's gate and are confirmed on the phone by default. Replying from an inbox item is the user's own send.
- **Thread page.** An inbox item opens as the PR's or issue's page in the app: comments, reviews, line comments with their diff, timeline events and failed checks with the end of their logs.
- **Sign-in.** Choose one of the two under Settings → Connectors → GitHub:
  - **A computer's `gh` login.** The hub asks that computer for the token through `connector.credentials.gh_token`. This needs the extension's `gh_login` permission. The token stays in the extension's memory: it is never stored, logged or shown.
  - **The device flow.** The token is sealed in the hub's connector secret.

  The hub keeps the choice and hands it to the extension (`connector.auth.set`).

## The page

The page reads with the connector's own sign-in, so it shows only what the connector may read. It never writes. It has five tabs:

| Tab | What it lists |
| --- | --- |
| 待 review | Open PRs requesting your review (`is:open is:pr review-requested:@me archived:false`), with CI rollup and review decision. |
| 我的 PR | Your open PRs, with CI rollup (passed, failed, running), review decision (approved, changes requested) and draft state. |
| 和我有关 | Open issues and PRs assigned to you or mentioning you, listed once each. |
| 通知 | Unread notifications with their reason. When nothing is unread, the latest notifications are shown. |
| 动态 | Recent activity from repositories and people you follow (`received_events`): pushes, PRs opened or merged, issues, comments, releases, new branches and tags, stars and forks. |

PR lists use one GraphQL search for CI and reviews. If GraphQL fails, the page uses REST search without those badges. Each list is cached for a minute; the refresh button reads it again. When the hour's rate limit is nearly used up, the page shows what it read before and keeps the remaining requests for the inbox. A search box filters the current tab by title, repository and author.

A PR or issue opens on top of the list with:

- its state, branches, labels and assignees;
- the head commit's checks (counts, and the names of failed ones);
- each reviewer's latest review, and requested reviewers who have not answered;
- the description and the newest five comments;
- a link to GitHub, and the name of its knowledge-base conversation.

Anything else opens on GitHub. The navigation entry's badge counts PRs waiting for your review. It is refreshed every ten minutes while the page is on.

The page is a declarative view. Apps from before UI version 3 get rows instead of the segmented tabs, a search field instead of the search box, and a back button.

Limits of declarative views:

- An extension view cannot open a Persona conversation or an inbox item. The detail page names the knowledge-base conversation instead.
- There are no tabs beyond the five-choice segmented control.
- Images are limited to avatars.

## Configuration

[Manifest and settings schema](../../extensions/github/persona-extension.json)

| Setting | Meaning |
| --- | --- |
| `triage_rules` | Your own rules for sorting GitHub's inbox items, in your words; empty uses the inbox's global rules. |
| `host` | GitHub's host, `github.com` by default. GitHub Enterprise is not finished: only `github.com` works today. |
| `proxy` | An `http://[user:pass@]host:port` proxy for everything sent to GitHub (sign-in, API, CI logs). A malformed value fails every request rather than going direct. Empty uses the environment's proxy, or goes direct. A hub that had `github.proxy` / `github.auth.host` in its own configuration moves them here when the extension takes over. |

Network access: `github.com`, `api.github.com`, `avatars.githubusercontent.com`, and the hosts GitHub redirects CI logs to (`*.actions.githubusercontent.com`, `*.blob.core.windows.net`).

## Protocol additions

Besides the connector protocol's methods, the program answers these. The hub calls them only for the capabilities declared (`options`, `settle`, `machines`), and calls `auth.set` and `options.set` after every start:

- `connector.options.get`, `connector.options.set`: the user's inbox options;
- `connector.inbox.settle`: closing open items the rules now consider over;
- `connector.machines.online`: a computer connected, so its `gh` login is tried again;
- `connector.auth.get`, `connector.auth.set`: where the sign-in comes from.

It also calls the hub's `connector.credentials.gh_token`. A hub without it makes the computer sign-in report that the hub is too old. The device flow works on any hub that runs the extension.

## Tests

From the repository root:

```sh
go test ./extensions/github/...
```

The tests cover:

- the connector's own suite, carried over from the hub: inbox rules, CI follow-up, archive, tools, writes, thread page, device flow, proxy and token scrubbing;
- the protocol additions;
- the fallback tier and rules on every inbox item;
- the page's five tabs against a fake GitHub, checked as the apps read views;
- search, caching and refresh, the detail page, signed-out and older-app views;
- manifest/program agreement and the SDK's conformance check.
