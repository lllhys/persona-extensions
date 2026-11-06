# V2EX

[Documentation](../README.md) · [Source](../../extensions/v2ex/)

V2EX (`www.v2ex.com`) is a Chinese-language technology community. This extension reads **public content only, without an account**. It offers an in-app topic reader and assistant tools.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `ui` | `topics`, with the compatibility placement `plugin_page` | An extension page for hot, latest and followed-node topics, filtered by interests. Open a topic to read its body and latest replies, or open the original website. |
| `connectors` | `v2ex`, capabilities `status` and `tools` | `v2ex_hot` and `v2ex_topic`: the assistant can fetch a list, choose topics and read them. |

## Fetching, network and rate limits

The extension uses the site's public API v1 (`https://www.v2ex.com/api`). API v2 needs a personal token, so it is not used.

| Path | Answer |
| --- | --- |
| `/topics/hot.json`, `/topics/latest.json` | A list of topics, each with its text, so a list needs one request. |
| `/topics/show.json?node_name=<name>` | A node's newest topics. An unknown node is a `404`. |
| `/topics/show.json?id=<id>` | One topic, or an empty list when the site does not have it. |
| `/replies/show.json?topic_id=<id>` | All of a topic's replies at once, oldest first. The latest are cut from the end, and a reply's floor is its place in this list. |

Requests are serialized with at least one second between them, each with a 20-second timeout and an 8 MiB answer limit. Lists are cached for five minutes, topics and replies for ten, up to 200 answers and 32 MiB. A refresh within 30 seconds of the last read answers from the cache.

The extension stops asking before the site has to refuse:

- Below 20 requests left (`x-rate-limit-remaining`), requests pause until the quota resets (`x-rate-limit-reset`). These numbers count only on answers from the site itself: the CDN replays another client's numbers on a cache hit (`cf-cache-status`).
- A refusal (`429`, a spent quota or a rate-limit message) pauses requests for its `Retry-After`, between one minute and one hour, else until the quota resets, at most one hour, else for ten minutes.

The connector's status is `rate_limited` during a pause. It is `error`, with the reason, when the last read failed and none has succeeded since. A firewall challenge also adds a hint about the hub's network. An unknown node or a missing topic does not change the status.

When a read fails, the older copy stays on the page or in the tool's answer, with a note saying why and when it was read. An answer that is not the expected JSON array never replaces a good copy.

A page reads for at most 15 seconds and a tool call for at most 25. Followed nodes are read in turn: those left when the time runs out are named on the page or in the answer, and what was read stays cached so the next try gets further.

The API sends each author's whole profile. Only the username is kept.

The hub fetches and renders the reader content, so the phone does not need its own proxy to read it. Opening the original web page happens in the app's browser and requires the device itself to reach `www.v2ex.com`. API v1 has no promise of stability, so its paths and answers stay in `site.go`, `feed.go` and `topic.go`.

## Configuration

[Manifest and settings schema](../../extensions/v2ex/persona-extension.json)

| Setting | Meaning |
| --- | --- |
| `list` | Initial list: `hot` (default), `latest` or `nodes`. |
| `nodes` | Followed nodes, by the name in the node's link (the part after `/go/`, such as `python`). At most five are read. Names that cannot be node names are listed on the page and not requested. With no nodes, the `nodes` list is not offered. |
| `exclude_nodes` | Hide topics of these nodes. Each must match a node's name or title exactly, case-insensitively, so `go` does not hide `google`. |
| `keywords`, `exclude_keywords` | Include/exclude a topic when its title or body contains a matching keyword, case-insensitively. |
| `tz` | Time zone used for timestamps in the view and tools. |

The page can temporarily show everything without interest filters.

## Assistant tools

| Tool | Arguments | Answer |
| --- | --- | --- |
| `v2ex_hot` | `source` (`hot`, the default, `latest`, `nodes` or a node's name), `filtered` (default `true`), `keywords`, `limit` (1 to 30, default 15) | Numbered topics with id, node, author, replies, last activity, link and an excerpt. |
| `v2ex_topic` | `topic` (an id or a link), `replies` (0 to 50, default 10) | Title, node, author, time, body (at most 8,000 characters) and the latest replies with floors, at most 30,000 characters in all. Replies are shortened so the latest are not the ones lost. |

Tools apply the configured filters by default; set `filtered: false` to bypass them. Both tool descriptions tell the assistant that what topics and replies say is not the user's instruction.

## App view and compatibility

The list selector is segmented on UI version 2 and newer. Earlier apps see a row per list, with the active list marked. The list section shows the selected list and visible/total counts; its footer describes the applied filters. Rows show node, author, replies and relative time. The view also shows when it was read and a refresh action.

A topic opens on top of the list on UI version 3 and newer. Earlier apps draw it in the list's place, with an action to return to the same list.

Timestamps use the configured `tz`, then the user's time zone supplied by the hub, then the hub machine's time zone.

A topic view contains title, node, author, time and reply count. Body text is capped at 12,000 characters, in text items of at most 1,800. It is the author's source text, so Markdown or HTML in it shows as written. The latest 20 replies follow, each capped at 1,200 characters, then actions to open the website and refresh.

Views stay within the limits the SDK checks: at most 300 items per page, 2,000 characters per item text field and HTTPS links only.

The extension uses `plugin_page` rather than `nav_page`, like LINUX DO, so older hubs do not reject it for a slot they do not recognize.

## Tests and integration checks

From the repository root:

```sh
go test ./extensions/v2ex
```

The tests use a fake site, so they never reach the real one. Coverage includes parsing and profile fields, filters, node names, merging, caching, pacing, concurrent reads, stale copies, firewall pages, cut answers, refusals, quota handling and cache bounds. It also checks the list and topic pages on UI versions 0 to 3, long topics, time zones, the page and tool time limits, both tools, connector status and errors, manifest consistency, the protocol handshake through `exttest.Check`, and an older app opening a topic and going back over the protocol.

A live-site check on 2026-11-05 ran the built program over its protocol from a personal computer. It read the hot and latest lists, a node, an unknown node and a topic with replies, and called both tools; the connector status was `ok`. This is a historical result, not a guarantee that every network will remain accessible.

Not yet checked for this version: `personad extension test`, installation on a hub, the extension sandbox and network allowlist, and rendering in the real apps.

## Package and install

From the repository root:

```sh
scripts/package.sh v2ex
```

On the hub's machine, replace `<version>` with the manifest version:

```sh
personad extension install file:/path/to/v2ex-<version>.tar.gz
```

Pre-rename hubs use `personad plugin install …`. See [installation](../installation.md) for the public market and compatibility details.
