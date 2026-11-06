# LINUX DO

[Documentation](../README.md) · [Source](../../extensions/linuxdo/)

LINUX DO (`linux.do`) is a Chinese-language technology community running Discourse. This extension reads **public content only, without an account**. It offers an in-app topic reader and assistant tools.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `ui` | `hot`, with the compatibility placement `plugin_page` | An extension settings page for hot, daily, weekly and latest topics, filtered by interests. Open a topic in the same page to read its body and popular replies, or open the original website. |
| `connectors` | `linuxdo`, capabilities `status` and `tools` | `linuxdo_hot` and `linuxdo_topic`: the assistant can fetch a list, choose topics, read them and summarize. |

## Fetching, network and rate limits

LINUX DO's firewall often requires browser verification for JSON list endpoints (`/latest.json`, `/top.json`) from server addresses, while RSS remains accessible. The extension therefore uses:

- `/hot.rss`, `/top.rss?period=daily`, `/top.rss?period=weekly`, `/latest.rss`: up to 30 topics each, including title, author, category, time, summary, post count and participant count.
- `/t/topic/<id>.json`: the topic and its first 20 posts.

Requests are serialized with at least two seconds between them. Lists are cached for five minutes and topics for ten. A `429` response pauses requests for ten minutes unless `Retry-After` specifies the delay; stale cached data remains available during the pause. A firewall challenge is recognized only on a non-`200` response whose page says "Just a moment", so a topic or feed that merely mentions the phrase still loads. A `403`/`404` on a topic reads as "this topic is gone"; the same status on a list feed is an ordinary fetch failure — the connector shows an error and the stale cached list still stands in. Whenever a cached copy stands in for a failed fetch, the connector status keeps the failure and `last_sync` stays at the last successful read.

The hub fetches and renders the reader content, so the phone does not need its own proxy to read it. Opening the original web page happens in the app's browser and requires the device itself to reach `linux.do`. This distinction matters on networks where the site is not directly accessible, including mainland China networks.

## Configuration

[Manifest and settings schema](../../extensions/linuxdo/persona-extension.json)

| Setting | Meaning |
| --- | --- |
| `list` | Initial list: `hot` (default), `daily`, `weekly` or `latest`. |
| `categories`, `exclude_categories` | Include/exclude category names. Use the site's actual names, such as `开发调优`, `前沿快讯` or `搞七捻三`; these are data values, not translated documentation labels. |
| `keywords`, `exclude_keywords` | Include/exclude a topic when its title or summary contains a matching keyword, case-insensitively. |
| `hide_nsfw` | Hide titles marked NSFW; enabled by default. |
| `hide_pinned` | Hide pinned site announcements; enabled by default. RSS exposes `discourse:topicPinned`; old announcements can otherwise dominate hot lists. |
| `tz` | Time zone used for timestamps in the view and tools. |

The page can temporarily show everything without interest filters. Tools apply the configured filters by default; set `filtered: false` to bypass them. A `linuxdo_hot` call's `keywords` narrow the configured result further rather than adding to the configured keywords.

## App view and compatibility

The list selector is segmented on UI version 2 and newer. Earlier apps see four rows, with the active list marked as selected. The list section shows the selected list and visible/total counts (for example, 12 of 30); its footer describes the applied filters. Rows show category, author, posts, participants and relative time. The view also shows when it was read and a refresh action.

Timestamps use the configured `tz`, then the user's time zone supplied by the hub (`tz` in UI version 2 requests), then the hub machine's time zone.

A topic view contains title, author, time, views, likes, posts, participants and tags; body text is capped at 12,000 characters and 200 text items (each code block starts a new item), then a caption points to the website. Code blocks retain code styling and images become textual placeholders. The ten highest-scoring replies from the first 20 posts are shown, followed by actions to open the website or return to the list.

Views follow the host's `plugin.CheckView` limits: at most 300 items per page, 2,000 characters per item text field and HTTPS links only. Tests exercise equivalent validation.

The extension keeps `plugin_page` rather than adding `nav_page`: older hubs do not recognize the newer slot and could reject the whole extension. Host support for extension pages, UI slots and iOS/Mac rendering was introduced in separate stages; retaining the compatible placement avoids requiring all those changes on an older hub.

## Tests and previous integration checks

From the repository root:

```sh
go -C extensions/linuxdo test ./...
```

Coverage includes RSS parsing and filtering; body-to-text conversion (lists, code, images, emoji, avatars, link previews and quotes); popular-reply ordering; list switching, unfiltered views, topic navigation, refresh, caching, missing topics and long-topic truncation. It also checks firewall responses, `429`, stale-cache fallback, connector status/tools, ids/links, manifest consistency, protocol handshake, `ui.render`, `ui.action`, `no_contribution`, `unknown_method`, segmented controls and user time zones.

Historical verification on 2026-11-04 used Persona's hub at revision `f850c8b2` and an extension pointed at a fake site. It covered a `dir:` preview (program execution and `linux.do` access permissions), installation with default settings, settings-form validation, page listing/rendering, topic opening through `plugin.CheckView`, connector status and `linuxdo_hot` through the hub. Those temporary host integration tests are not part of this repository's test suite.

A live-site check on the same date read hot, daily and weekly lists plus a topic from a server, without a firewall challenge; connector status was `ok`. That check motivated `hide_pinned` after old pinned announcements appeared first. These are historical results, not a guarantee that every network will remain accessible.

## Package and install

From the repository root:

```sh
scripts/package.sh linuxdo
```

On the hub's machine, replace `<version>` with the manifest version:

```sh
personad extension install file:/path/to/linuxdo-<version>.tar.gz
```

Pre-rename hubs use `personad plugin install …`. See [installation](../installation.md) for the public market and compatibility details.
