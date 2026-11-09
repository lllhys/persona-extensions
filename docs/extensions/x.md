# X

[Documentation](../README.md) · [Source](../../extensions/x/)

This extension brings X (formerly Twitter) into Persona in one of two ways, both optional:

- **X API**: your own X account through the official X API v2 (`api.x.com`), with keys of a developer app you own. It brings your mentions into the inbox, keeps a few days of your home timeline (or chosen lists) within a daily budget, and turns them into an organized digest on an X page and for the assistant.
- **Grok**: no X account sign-in and no X keys. If the official xAI CLI `grok` (Grok Build) is installed on the hub machine and signed in with your SuperGrok or X Premium+ subscription, the extension runs it headless to search X for the accounts and topics you choose and organizes what it finds. It rides on your subscription.

Either way it only reads: it never posts, likes, follows or reads direct messages.

## Two ways

| | X API | Grok |
| --- | --- | --- |
| Cost | Pay-per-use credits on your developer app (about $30–60 a month with the defaults, see below) | Your SuperGrok or X Premium+ subscription; no extra charge from the extension, within xAI's own (unpublished) limits |
| Needs | A developer app at console.x.com, credits, four keys | `grok` on the hub machine, `grok login` with the subscription |
| What it reads | Your home timeline or lists, complete within the daily budget | Whatever Grok's X search returns for the watched accounts and topics: a search, not a feed |
| Mentions in the inbox | Yes | No (search cannot list your mentions reliably) |
| Who you follow | Read from X | Not readable: list the accounts in `handles` (or sign in to the X API once) |
| Live tools | `x_post` (one post by id) | `x_search` (search X for anything) |

`source` picks one: `auto` (the default) uses the X API when you are signed in, else Grok when `grok` is on the hub machine; `x_api` never runs grok; `grok` takes the digest from Grok even when the X API is signed in (which then only brings mentions and who you follow, not the paid timeline).

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `connectors` | `x`, capabilities `status`, `tools`, `inbox`, `login`, `fallback` | Sign-in, the inbox (mentions), and the read tools `x_digest`, `x_feed`, `x_following`, `x_post`, `x_search`. |
| `ui` | `x` (`nav_page`) | The X page: today's or the last three days' digest (or Grok's), not a timeline. |
| `assistant` | `x` (skill `skills/x`) | Tells the assistant how to write a daily X digest with the tools, and the rules below. |

## Cost and X's terms

X sells API access as pay-per-use credits, billed to the developer app; there is no free read tier. The extension runs on your own app, so your credits pay for what it reads:

- **Home timeline and lists**: X bills about $0.005 per post and $0.010 per user returned. The extension counts every post and every user in each answer (`data`, `includes.tweets`, `includes.users`) against `daily_post_budget` per UTC day. Before each request it sizes the page so that its worst case (6 resources a post: the post, two posts it references, three authors) fits what is left, and holds that worst case until the answer is counted, so the day's count never passes the budget, even with `x_post` running at the same time. When it is spent the feed stops until 00:00 UTC. The default of 200 costs about $1–2 a day (about $30–60 a month), depending on how many of the 200 are users, before X's same-day deduplication. Reading a few lists instead of the home timeline costs less.
- **Mentions, your own posts**: X's "Owned Reads" (the app owner reading their own data, $0.001 per resource), not counted against the budget. Mentions are read at most 300 per sync (three pages of 100); a larger backlog comes in on the next syncs.
- **Who you follow**: read whole on sign-in and then once a week (up to 20,000 accounts), not counted against the budget. At Owned Reads prices each refresh costs about $1 per 1,000 accounts you follow (5,000 follows: about $5 a week, about $20 a month). It is not confirmed that Owned Reads apply to OAuth 1.0a requests; at the $0.010 Following: Read price it would be about $10 per 1,000 accounts per refresh. Check the usage in the X console after the first day, and set a spending limit there.
- **`x_post`**: a live read of one post, counted against the budget (and saved at once, so a restart does not forget it), refused when less than one post's worst case is left.

X's developer terms shape what the extension does:

- **Official API only.** No scraping of x.com, no browser cookies, no unofficial GraphQL endpoints, not even as a fallback.
- **Short retention.** X requires stored copies of posts to be deleted when they are deleted on X. The hub's knowledge base cannot delete a post, so posts never go there: they stay in the extension's own data directory for `retention_days` (1 to 7, default 3), mentions for 8 days, and everything is deleted when you sign out.
- **No training, no profiling.** X forbids using its content to train models and inferring sensitive traits (health, politics, religion, ethnicity, sexuality, finances, union membership, crimes) about people. The assistant skill and every tool result say so, and mark post text as untrusted third-party content.
- **Your own app.** X's policy discourages tools that require end users to register developer apps. Persona is self-hosted: you are the developer and the only user of your app, and the extension is a personal reader. Describe your app's use case accordingly in the console. X has not confirmed this reading.

## X API setup

1. Sign in at [console.x.com](https://console.x.com) and create a project and an app.
2. Buy pay-per-use credits for the account (and set a spending limit if you like).
3. In the app's settings, set the app permission to **Read**.
4. Under **Keys and tokens**, copy the **API Key** and **API Key Secret**, then generate the **Access Token** and **Access Token Secret** for your own account.
5. In Persona, open **Settings → Connectors → X**, sign in and paste the four values. The extension checks them with `GET /2/users/me` and shows your `@username`.

The keys are sealed in the hub's connector secret. They never appear in logs, statuses, errors, tool results or the page. Signing out deletes the secret and everything the extension stored.

Requests are signed with OAuth 1.0a (HMAC-SHA1, user context), implemented in the extension and tested against the RFC 5849 / OAuth Core 1.0 signature vectors.

## Grok setup

1. On the hub machine, as the user the hub runs as, install the CLI: `curl -fsSL https://x.ai/cli/install.sh | bash` (it goes to `~/.grok/bin/grok`).
2. Run `grok login` and sign in with an account that has a SuperGrok or X Premium+ subscription. Sign-ins expire after 7 days: run it again when the X page says so.
3. In the extension's settings, list the accounts to watch in `handles` (usernames without `@`) and, if you like, `topics`. Without `handles`, the accounts you follow are used when the X API has read them once (the 100 most recently active, then those with the most followers).
4. Leave `source` on `auto` (or set it to `grok`). The extension finds `grok` in `PATH`, then `~/.grok/bin/grok`, `~/.local/bin/grok`, `/usr/local/bin/grok` and `/opt/homebrew/bin/grok`; set `grok_path` if it is elsewhere. A `grok` that is missing or cannot start is reported as such, not as an expired sign-in or a rate limit, whatever its path is called.

The extension must run without the hub's sandbox (`personad extension sandbox x off`; the default for extensions from the official marketplace): in the sandbox the home directory is invisible, so it cannot find `grok` in `~/.grok/bin` or read its sign-in in `~/.grok/auth.json`, and its network is limited to the hosts in `permissions.network`. The manifest lists `x.ai` and `*.x.ai` there, which is where grok talks to, but the sandbox still hides the sign-in. grok gets the extension's environment, which for an installed extension is the hub's allowlist (`PATH`, `HOME`, `USER`, `LANG`, `TZ`, `TMPDIR`, a few proxy and certificate variables, and what the hub passes through with `host.PassEnv`; see the main repository's `docs/extensions.md`), not the hub's whole environment. So `XAI_API_KEY` reaches grok only if the hub passes it through; then grok uses it instead of the sign-in and bills the xAI API, not the subscription.

How Grok runs, every `grok_every_hours` (default 6):

- `grok -p <prompt> --output-format json --disallowed-tools <every local tool> --deny Bash --deny Read --deny Edit --deny Grep --deny WebFetch --deny MCPTool --deny AgentMessage --max-turns 6 --cwd <a new empty directory>`. X search (`x_search`) and web search are hosted tools that xAI runs server side, not local ones, and grok offers them only while its `--tools` allowlist is empty or names them. So the run never passes `--tools`: an allowlist without `x_search` turns X search off, and one with it does worse, since `x_search` is not a local tool and grok then ignores the whole allowlist and keeps every local tool. Instead `--disallowed-tools` removes every local tool by name (the shell, file reading, editing and search, listing, fetching, subagents, tasks and schedules, media generation, memory, MCP tool search; the list is `grokLocalTools` in `internal/x/grok.go`), and the `--deny` permission rules block the same kinds of tools, and every MCP tool, in case a newer grok adds a tool the list misses. Headless grok also cancels any tool call that would ask for permission.
- The accounts go 20 a run (X search's limit per search), at most 5 runs a refresh; the next refresh goes on with the next accounts. Then one run for `topics`. Each run asks for the last 24 hours and at most 15 posts.
- Every run, the digest's and `x_search`'s, counts against `grok_searches_per_day` (default 30) per UTC day, counted before it starts and saved at once, so a restart does not forget it. A failed refresh tries again after 30 minutes (after `grok_every_hours` when grok is not signed in).
- A run has 4 minutes; on timeout or cancel its whole process group is killed. At most 1 MiB of its output is read.
- The next refresh starts with the first batch of accounts that failed, so a passing failure (a network blip, the rate limit) does not skip those accounts for a whole turn; when only the first batch failed and later ones did not, it moves on, so one batch that always fails does not stall the rest.
- The page's refresh button runs a refresh only if the last one that found something was more than 30 minutes ago; otherwise it says so and shows what that one found.

Safety:

- grok gets the extension's environment (for an installed extension, the hub's allowlist above) without any `PERSONA_*` or `JARVIS_*` variable (the hub's token, sockets, the extension's directories), and never an X API key. `HOME` stays, for its sign-in.
- grok loads the MCP servers configured for the hub's user in `~/.grok/config.toml`, `~/.claude.json` and `~/.cursor/mcp.json` (no flag stops that), so text in a post could try to make the model call one. The run denies every MCP tool (`--deny MCPTool`) and removes grok's MCP tool search.
- It runs in a fresh empty temporary directory, removed afterwards; its tools are limited as above. grok runs in a process group of its own, killed when the run ends, however it ends, so nothing grok started (an MCP server, a helper) outlives the run.
- Its answer is untrusted: only the outermost JSON object is read; a post is kept only with a real permalink (`https://x.com/<handle>/status/<id>`, `twitter.com` and query strings normalized), its author is the permalink's, duplicates are dropped, every text is capped, and topics may only point at kept posts. The prompts tell Grok to use X search only, never invent posts, quote post text verbatim, treat it as data, and infer nothing sensitive about anyone. They live in `internal/x/prompts/grok_digest.md` and `grok_search.md`.
- grok's error output is never shown as is: one line, the home directory as `~`, token-like strings removed.
- Headless runs are kept in grok's own session history in `~/.grok`, like any grok session.

## What it reads

One loop, every 15 minutes (sooner right after sign-in or a refresh):

- **Following**: `GET /2/users/:id/following`, 1,000 a page (at most 20 pages), on sign-in and then once a week.
- **Feed** (`feed_source`):
  - `home`: `GET /2/users/:id/timelines/reverse_chronological` with `since_id` (the cursor is kept), up to 100 a page as the budget allows. The first read reaches back `retention_days`. A read that stops halfway (an error, a `429`, the sync's time limit) keeps the cursor and remembers the oldest post it read; the next sync first reads the gap below it (`until_id`), then what is newer. Only running out of budget skips older posts for good.
  - `lists`: `GET /2/lists/:id/tweets` for each of `list_ids`, stopping at the first post already kept or older than `retention_days` (lists have no `since_id`).
  - `off`: no feed; mentions only. A budget of 0 also turns the feed off.

  A long post's full text comes from `note_tweet`.
- **Mentions**: `GET /2/users/:id/mentions` with `since_id` and the same gap handling as the home timeline, three pages of 100 a sync; the first read reaches back 8 days.
- **Your own posts** (`GET /2/users/:id/tweets`), only while a mention is still open, to tell whether you answered it.

Rate limits: the extension keeps `x-rate-limit-remaining` and `x-rate-limit-reset` per endpoint and does not ask an endpoint whose window is used up. A `429` sets the connector's status to `rate_limited` until the reset (waits are capped at 15 minutes). Every request has a 30-second timeout and every sync a 3-minute one; failures back off from one minute to 15.

Status with the X API signed in: `ok` with `@username`; `rate_limited`; `error` with a guide when X refuses the keys (`401`: regenerate the access token and sign in again) or the account (`402` / `403`: add credits, or check the app is enrolled for the X API).

Status without the X API: with `source: x_api`, `not_signed_in`. Otherwise Grok's, answered from memory (the loop looks for `grok` every 15 minutes): `ok` with account `Grok` via "grok on the hub machine" when grok is there and its last run went well (or none ran yet); `not_configured` with how to set up either way when grok is not found; `not_signed_in` when grok said it is not signed in or its sign-in expired (run `grok login`); `rate_limited` when it reported a rate limit or quota; `error` with grok's scrubbed line otherwise.

## The inbox

Only with the X API: Grok's search cannot list your mentions reliably, so in Grok mode the inbox stays empty.

One item per mention (a post that mentions you or replies to you), kind `mention`:

- the thread is the post's conversation, the sender `@username`, the link the post's permalink (`https://x.com/<username>/status/<id>`);
- the connector's rule tier is "needs you" (`action`), also stamped as the fallback tier (`extra.rule_tier`), so items never wait for a triage model; your `triage_rules` are carried on every item;
- an item is done once you posted in that conversation after the mention;
- mentions older than 8 days are not collected.

Replying from the inbox is not supported (the extension does not write to X). Open the post on X to answer.

## The page and the digest

The X page is a digest, not a timeline. Its header shows the account, posts read today against the budget, the last sync and the state; a segmented control switches between 24 hours and 3 days (rows on apps before UI version 2), and a refresh button syncs now. Signed out, it shows how to sign in.

The digest is computed the same way every time from what is kept, without a model. Each section shows at most 10:

1. **Top posts**: original posts, reposts collapsed into the original (with who reposted it). Score: likes + 2 × reposts + 2 × quotes + replies, divided by log2(author's followers + 2); ties go to the most recent.
2. **Most shared links**: expanded links from the posts, grouped by a normalized URL (host without `www.`, no fragment or tracking parameters, posts on X itself skipped), ranked by how many accounts shared them.
3. **Threads**: an author's chain of three or more replies to themself in one conversation.
4. **Most active accounts you follow**: posts in the window per followed account.

Plus totals: posts seen and the budget used today. Each post shows "Name @username", its full unaltered text (long posts in pieces, up to about 10,000 characters, with the rest on X), its counts and time, and a link to the post. Page text is localized (zh-Hans, en, ja).

In Grok mode the page shows Grok's digest instead: a header (source Grok, the last refresh, Grok runs today against the cap, the state), a note that it was found by Grok's search and may be incomplete, Grok's summary (written in the hub's language), its topics with their posts, then the other posts of the last 24 hours, each as "Name @handle", the text as Grok quoted it, why it matters and a link to the post; refresh asks Grok now, within the cap. With neither way set up, the page explains both.

## Assistant tools

All read-only. Every result starts with a note that post text and bios are third-party content.

| Tool | Arguments | What it returns |
| --- | --- | --- |
| `x_digest` | `window`: `24h` (default) or `3d` | The digest as text with permalinks. No X request. |
| `x_feed` | `query`, `author`, `hours` (default 24), `limit` (≤ 50) | Kept posts matching (case-insensitive substrings), newest first. No X request. |
| `x_following` | `query`, `limit` (≤ 100) | Followed accounts: name, `@username`, bio, followers, posts in the last 3 days. No X request. |
| `x_post` | `id` | One post live (`GET /2/tweets/:id`) with the post it reposts, quotes or replies to. Counted against the budget. Needs the X API. |
| `x_search` | `query` (required, ≤ 300 characters), `handles` (≤ 20), `from`, `to` (`YYYY-MM-DD`) | A live Grok search of X: Grok's summary and up to 15 posts with permalinks. One Grok run, counted against `grok_searches_per_day`; refused when grok is missing, `source` is `x_api`, or the cap is reached. |

In Grok mode `x_digest` returns Grok's digest, `x_feed` searches the posts Grok found, and `x_following` lists the accounts Grok watches.

## Configuration

[Manifest and settings schema](../../extensions/x/persona-extension.json)

| Setting | Meaning |
| --- | --- |
| `triage_rules` | Your own rules for sorting X's inbox items; empty uses the inbox's global rules. |
| `feed_source` | `home` (default), `lists` or `off`. |
| `list_ids` | Numeric list ids (from `x.com/i/lists/<id>`), read when `feed_source` is `lists`. |
| `daily_post_budget` | Posts and users read for the feed per UTC day, 0 to 5,000 (default 200). 0 turns the feed off. |
| `retention_days` | Days posts are kept, 1 to 7 (default 3). Mentions are kept 8 days. |
| `proxy` | An `http://`, `https://`, `socks5://` or `socks5h://` proxy (`[user:pass@]host:port`) for every X API request. A malformed value fails every request rather than going direct. Empty uses the environment's proxy, or goes direct. grok does not use it. |
| `source` | `auto` (default), `x_api` or `grok`, see "Two ways". Setting `x_api` deletes what Grok found. |
| `grok_path` | Full path of `grok`; empty looks it up (see "Grok setup"). |
| `handles` | Accounts Grok watches, usernames (up to 200). Each must match `^@?[A-Za-z0-9_]{1,15}$` and exact repeats are not allowed: the settings form refuses the save otherwise. A leading `@` is dropped, and repeats that differ only in case count once. |
| `topics` | Up to 10 topics (each up to 100 characters) Grok also searches. |
| `grok_every_hours` | How often Grok's digest refreshes, 1 to 24 (default 6). |
| `grok_searches_per_day` | Grok runs per UTC day, digest and `x_search` together, 0 to 200 (default 30). 0 turns Grok off. |

Network access: `api.x.com`, `x.com` and `*.twimg.com` for the X API; `x.ai` and `*.x.ai` for grok.

## Privacy

- Keys: only in the hub's sealed connector secret and the extension's memory.
- Data: `following.json`, `posts.json`, `mentions.json` and `state.json` in the extension's data directory, each written atomically (a temporary file renamed over it). Nothing goes to the knowledge base. Sign-out deletes them all.
- Grok's data: `grok_digest.json` (what Grok found, kept `retention_days`, and the day's run count), written the same way. It does not come from the X sign-in, so signing out of the X API keeps it; it is deleted when `source` is set to `x_api`, or with the extension's data.
- Nothing is shared beyond your hub; X's content stays scoped to the account that authorized it.

## Limits and not yet

- **Writes**: no posting, replying, liking or reposting.
- **Direct messages**: not read (they need express consent and cost more).
- **Knowledge-base ingestion**: posts are not archived to the knowledge base until the hub can delete a post when X does.
- **Sign-in**: pasting the four keys is the only way today. X's OAuth 1.0a PIN flow and OAuth 2.0 need a sign-in step that shows a link and a field together, or a redirect into the hub, which the apps do not have yet.
- **Deleted posts**: kept posts are not re-checked against X; they simply expire after `retention_days`.
- **Media**: images and videos are not shown; the page links to the post.
- The X brand mark is not shown; the apps use the generic connector icon.
- **Grok** is a search, not a feed: it may miss posts, a run covers at most 20 accounts, and the digest covers the last 24 hours. Whether every post it returns exists is only checked by the shape of its permalink. The subscription's quota for automated headless use is not published; `grok_searches_per_day` is only your own cap. Sign-ins expire after 7 days. Whether xAI's subscription terms allow this kind of automated personal use is not confirmed.

## Tests

From the repository root:

```sh
go test ./extensions/x/...
```

The tests run against a fake X API (`internal/xtest`, synthetic accounts and keys only) and cover the signature vectors, sign-in (success, `401`, `402`), following pagination, home timeline `since_id` resume, resuming after a failed page and the budget's ceiling (also with concurrent `x_post` calls, and across a restart), lists stopping at a known post, mentions as inbox items with their tier and a backlog larger than one sync, `429` handling, error statuses, retention pruning, sign-out wiping the data directory (also with a save in flight), the digest's ranking, links, threads and active accounts, the tools, the page (signed out and in, every UI version and language) against the SDK's and the hub's view checks, the manifest against the program, the SDK's conformance check, and that no key appears in any status, tool result, error or page.

The Grok tests never run a real grok (the lookup is replaced in every test binary): a fake runner covers source selection, batches of 20 accounts and their rotation (also after failed batches), the daily cap (also across a restart and the UTC day, and with `x_search` or an X sign-out before the kept state was read), the refresh cadence and retries, auth and rate-limit statuses, keeping the digest across an X sign-out and deleting it for `x_api`, retention, the tools (`x_search` arguments, cap and refusals, `x_post` in Grok mode), using who you follow, the loop and the refresh button's 30-minute gap, and the Grok page in every UI version and language. The test binary itself stands in for the `grok` program to check the real exec path: the arguments (no `--tools`, every local tool in `--disallowed-tools`, the `--deny` rules including `MCPTool`, an empty `--cwd` removed afterwards), the environment without `PERSONA_*` / `JARVIS_*`, success JSON, error JSON (auth), stderr (rate limit), scrubbing, the output cap, and that a timeout, and also a normal end, kills grok and the process it started. Answers are parsed with code fences and leading text, invented or malformed permalinks dropped, authors fixed from the permalink, duplicates removed and texts capped. The lookup order of `grok` is tested too.
