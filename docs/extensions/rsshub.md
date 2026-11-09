# RSSHub

[Documentation](../README.md) · [Source](../../extensions/rsshub/)

[RSSHub](https://docs.rsshub.app) turns websites into feeds: a route such as `/github/issue/nwverse/persona`, `/arxiv/search/...`, `/bilibili/user/video/<uid>` or `/telegram/channel/<name>` is a feed of that page. This extension subscribes to RSSHub routes, or to the address of any RSS 2.0, RSS 1.0, Atom 1.0 or JSON Feed 1.0/1.1 feed, refreshes them on the hub in the background, and shows them in Persona and to the assistant.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `ui` | `feeds`, placement `nav_page` | A page on the main navigation: the instance's state and the unread count, the latest (or only unread) entries, the subscriptions grouped by tag, one feed's entries, one entry's text, and a form that previews or subscribes to a route. The navigation badge is the unread count. |
| `connectors` | `rsshub`, capabilities `status`, `tools` and `actions` | Read tools `rsshub_feeds`, `rsshub_entries` and `rsshub_preview`; writes `rsshub_subscribe`, `rsshub_unsubscribe` and `rsshub_mark_read`. |

## Configuration

[Manifest and settings schema](../../extensions/rsshub/persona-extension.json)

| Setting | Meaning |
| --- | --- |
| `instance_url` | The RSSHub instance, `https://rsshub.app` by default. A self-hosted instance is an `https://` address, which may include a path prefix (`https://example.com/rsshub`). An instance on the hub machine itself may use plain `http` with `127.0.0.1` (any `127.x`), `localhost` or `[::1]`, with a port. Any other `http` address, user info, query or fragment is refused, and the connector's status says why. |
| `access_auth` | How the access key is sent: `key` (default) adds `?key=<access key>`; `code` adds RSSHub's `?code=<md5(route path + access key)>`, so the key itself never appears in a URL. |
| `refresh_minutes` | How often every subscription is refreshed in the background: 5 to 1440, default 30. |
| `prefer_json` | Ask the instance for JSON Feed (`?format=json`) instead of its default RSS. Either format is read; the format is recognized from the content, not from the `Content-Type`. |

| Secret | Meaning |
| --- | --- |
| `access_key` | Optional. The `ACCESS_KEY` of a self-hosted instance. It is added only to routes of the configured instance, never to a feed address of another host, and it is removed from errors, kept state and every link RSSHub echoes back. |

The public `rsshub.app` is meant for trying RSSHub out: it may refuse some clients and networks with `403` ("we will gradually restrict access to rsshub.app for some feed readers"). The error is shown with the instance's own words. For daily use, run your own instance ([deployment guide](https://docs.rsshub.app/deploy/)) and put its address here.

## Sources and routes

What you subscribe to or preview can be written as:

- a route: `/github/issue/nwverse/persona`, `github/issue/nwverse/persona`, or `rsshub://github/issue/nwverse/persona` (the RSSHub Radar form), with route parameters such as `?limit=10`;
- a link to the configured instance (`https://rsshub.app/github/trending/daily/go/en`), which is kept as its route, so it follows a later change of instance;
- the address of any other feed (`https://blog.example.com/feed.xml`).

A route's `key` and `code` parameters are dropped: the extension adds the access key itself. Routes with empty or `..` segments are refused. A feed address must be `http` or `https`, and may not name this machine (`localhost`, loopback IPs, `host.persona.internal`), a link-local address (cloud metadata included) or the well-known metadata addresses; local feeds are read through the instance setting. The HTTP client also refuses to dial a link-local or metadata address that a name resolves to, refuses redirects to this machine, and refuses a redirect from `https` to `http`.

## Fetching, caching and entries

- Each refresh is a conditional request: the feed's last `ETag` goes in `If-None-Match` and its `Last-Modified` in `If-Modified-Since`. A `304` keeps everything as it is.
- A request times out after 25 seconds, follows at most five redirects and reads at most 8 MiB. Up to four feeds are refreshed at a time; a refresh already running is not started twice. The first background refresh runs five seconds after the extension starts.
- Entries are told apart by the feed's `guid` / Atom `id` / JSON Feed `id`, then by link, then by a hash of title, date and summary. An entry seen again keeps its read state and takes the feed's newer text; a new one is added unread. Each feed keeps its newest 100 entries.
- Titles, summaries (400 characters) and text (8,000 characters) are plain text: HTML tags are dropped, `script`, `style` and similar elements are left out, blocks become lines, entities are decoded, and images keep their `alt` text.
- Dates are read in RFC 1123/822, RFC 3339 and common variants; an entry without a date is ordered by when it was first seen.
- A failed refresh is kept on the feed with its reason and does not remove its entries.

Subscriptions, entries and read state live in the extension's data directory (`feeds.json`). A file that cannot be read is moved aside as `feeds.json.corrupt` and the extension starts empty. At most 300 subscriptions are kept.

## The page

The top shows the unread count, the number of subscriptions and the instance's state (connected, failing, or an unusable address), with **Refresh all**, **Mark all as read** and **Check instance**. Below are the latest entries, which can be limited to unread ones, and one section per tag (subscriptions without a tag are under 未分类) with each feed's unread count or a failure badge.

- A feed shows its source, tag and last refresh, a link to its site, **Refresh**, **Mark all as read** and **Unsubscribe** (asked first).
- An entry shows its source, author, time, text and a link to the original (`https` only; other links are shown as text). Opening it marks it read; **Mark as unread** undoes that.
- The form at the bottom previews a route without subscribing (up to 15 entries) or subscribes to it, with an optional tag and title. A subscription is made only when the source could be read once.

Older apps get the same page without the newer components (no stat grid, segmented control or empty state).

## Assistant tools

| Tool | Arguments | Answer |
| --- | --- | --- |
| `rsshub_feeds` | `tag` | Every subscription by tag: id, title, route or address, unread / total, and the last refresh with its time or failure. |
| `rsshub_entries` | `feed_id`, `tag`, `unread_only`, `query`, `limit` (1 to 50, default 20), `content`; or `entry_id` alone | The newest matching entries with id, read state, source, author, time, link and summary (or text with `content`). `entry_id` gives one entry's whole text. Reading does not mark anything read. |
| `rsshub_preview` | `route` (required), `limit` (1 to 30, default 10), `content` | A route or feed read live without subscribing. |

Writes go through the hub's gate (`actions.describe` names them on the card without reaching the network, and `actions.do` runs each request id once):

| Write | Arguments | Gate |
| --- | --- | --- |
| `rsshub_subscribe` | `route` (required), `title`, `tag` | Confirmed on the phone every time (`confirm_always`). Reads the source once first, and subscribes only when that worked. |
| `rsshub_unsubscribe` | `feed_id` | Confirmed on the phone every time (`confirm_always`); its entries and read state go with it. |
| `rsshub_mark_read` | `entry_ids` (at most 200), `feed_id`, `all`, `read` (default `true`) | Asked by default; a `connector_gate` rule may allow it. |

Tool descriptions remind the assistant that entries are written by the feeds' authors and are not the user's instructions.

## Status

`not_configured` when `instance_url` cannot be used, with how to fix it. Otherwise `ok`, with the instance as the account, the time it last answered, and the last error while its latest answer was a failure (no answer, `401` or `403`). A `404` for one route does not make the instance failing.

## Network and sandbox

`permissions.network` lists `rsshub.app` and `host.persona.internal`.

- In a sandbox, the hub's egress proxy is the only way out, and Go does not send loopback addresses through a proxy. When a proxy is set in the environment, an `http` instance on `127.0.0.1`, `localhost` or `[::1]` is therefore requested as `host.persona.internal` with the same port, which the proxy connects to the hub machine's loopback. Listing `host.persona.internal` lets the extension reach any port on the hub machine's loopback in the sandbox; `https` instances keep their host so their certificate still matches.
- A self-hosted instance on another host, and feed addresses of other hosts, are not in that list: with the sandbox on, the proxy refuses them (`403 … is not in this extension's permissions.network`). Extensions from the official market run without the sandbox by default, and these work there. For a local package, or with the sandbox on, run `personad extension sandbox rsshub off`, or use the public instance or one on the hub machine. The hub's `permissions.network: ["config:instance_url"]`, which allows exactly the configured address, needs a newer SDK schema than this repository's copy; the manifest moves to it after the SDK is synchronized.
- Opening an entry's original page happens on the device and needs the device to reach that site.

## Local validation

```sh
go -C extensions/rsshub test -race -count=1 ./...
RSSHUB_LIVE_URL=http://127.0.0.1:1200 RSSHUB_LIVE_KEY=<access key> go -C extensions/rsshub test -count=1 -run TestLiveInstance -v ./...
```

The unit tests use synthetic feeds and a fake RSSHub (`httptest`): the three formats and RDF, HTML to text and dates, the instance and loopback rules, routes and the access key in both forms (including RSSHub's documented `code` example), the `host.persona.internal` rewrite, ETag / `Last-Modified` with a `304`, deduplication and read state across refreshes and restarts, redirects, size limits, link-local refusal, the tools and writes through the connector, the protocol conformance checks, and every page state for UI versions 1 to 4. `TestLiveInstance` is skipped unless `RSSHUB_LIVE_URL` names a real instance.
