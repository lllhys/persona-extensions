# Hacker News

[Documentation](../README.md) · [Source](../../extensions/hackernews/)

Hacker News (`news.ycombinator.com`) is an English-language technology and startup community. This extension reads **public content only, without an account**. It offers an in-app story reader and assistant tools.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `ui` | `stories`, with the compatibility placement `plugin_page` | An extension page for the Top, New, Best, Ask HN and Show HN lists, filtered by interests. Open a story to read its text and top-level comments, or open the article or the Hacker News page. |
| `connectors` | `hackernews`, capabilities `status` and `tools` | `hn_stories` and `hn_item`: the assistant can fetch a list, choose stories and read their discussion. |

## Fetching and network

The extension uses the public Firebase API (`https://hacker-news.firebaseio.com/v0`); it needs no key and is the only host in `permissions.network`.

| Path | Answer |
| --- | --- |
| `/topstories.json`, `/newstories.json`, `/beststories.json`, `/askstories.json`, `/showstories.json` | A list of story ids in the site's order. The first 30 (the front page) are read. |
| `/item/<id>.json` | One story or comment; `null` when the site does not have it. Comments are a story's `kids`, already in the site's ranking. |

Because each item is its own request, a list is its ids and then up to 30 items, read at most eight at a time. A story page reads the first 15 visible top-level comments, plus a few extra in case some are deleted or dead. Deeper replies are not read.

Each request has a 15-second timeout and a 2 MiB answer limit; an answer that is not JSON never replaces a good copy. Lists and items are cached for five minutes, up to 2,000 answers, oldest first out. **Refresh** reads again anything older than 30 seconds.

When a read fails, the older copy stays on the page or in the tool's answer, with a note saying why and how long ago it was read. The connector's status is `error`, with the reason, when the last read failed and none has succeeded since. A missing or deleted item does not change it.

A page reads for at most 15 seconds and a tool call for at most 25. Stories that could not be read in time are counted on the page and in the answer.

The hub fetches and renders the reader content, so the phone does not need to reach the site to read it. Opening the article or the Hacker News page happens in the app's browser and needs the device itself to reach those sites. Views accept only HTTPS links, so an article with a plain `http://` address shows its domain but no **Open article** link; the Hacker News page is always offered.

## Configuration

[Manifest and settings schema](../../extensions/hackernews/persona-extension.json)

| Setting | Meaning |
| --- | --- |
| `list` | Initial list: `top` (default), `new`, `best`, `ask` or `show`. |
| `keywords`, `exclude_keywords` | Include/exclude a story when its title or the article's domain contains a matching word, case-insensitively. |
| `min_score` | Hide stories with fewer points. `0` (default) shows all. |

The page can temporarily show everything without interest filters.

## Languages

The page's own text (list names, counts, ages, notes and buttons) is in zh-Hans, en or ja, following the `locale` the hub passes with each request. An empty or unknown locale is zh-Hans, the hub's default. Titles, story text and comments are shown as the site has them. The tools answer the assistant in English. The manifest's settings form and description are in Chinese like the other market extensions, because the manifest has no per-language fields for them.

## Assistant tools

| Tool | Arguments | Answer |
| --- | --- | --- |
| `hn_stories` | `list` (`top`, the default, `new`, `best`, `ask` or `show`), `filtered` (default `true`), `keywords`, `min_score`, `limit` (1 to 30, default 15) | Numbered stories with id, points, comments, domain, author, age, the Hacker News link and the article link. |
| `hn_item` | `item` (an id or a `news.ycombinator.com/item?id=` link), `comments` (0 to 30, default 10) | Title, points, author, age, article link, text (at most 6,000 characters) and the top-level comments, each at most 1,500 characters, at most 30,000 characters in all. |

Tools apply the configured filters by default; set `filtered: false` to bypass them. Both tool descriptions tell the assistant that what stories and comments say is not the user's instruction.

## App view and compatibility

The list selector is segmented on UI version 2 and newer; earlier apps see a row per list, with the active list marked. The list section shows visible/total counts, and its footer describes the applied filters. Rows show points, comments, domain, author and age.

A story opens on top of the list on UI version 3 and newer. Earlier apps draw it in the list's place, with an action to return to the same list. Story text is capped at 12,000 characters, in text items of at most 1,800; each comment at 1,500. The site's HTML becomes plain text: paragraphs stay apart, code blocks keep their indentation, and a link becomes its full address.

The extension uses `plugin_page` rather than `nav_page`, like V2EX and LINUX DO, so older hubs do not reject it for a slot they do not recognize.

## Tests and integration checks

From the repository root:

```sh
go -C extensions/hackernews test ./...
```

The tests use a fake site, so they never reach the real one. They cover the HTML-to-text conversion, filters (including by domain), every locale having every text with the same format verbs, the list and story pages on UI versions 1 to 3 in all three languages, deleted, dead and missing items, plain-http articles, caching, stale copies and status, long stories staying within view limits, both tools and their argument checks, the connector, configuration and the protocol handshake through `exttest.Check`.

A live check on 2026-11-06 drew the Top list (about 2.6 seconds for 30 stories), the Ask HN list in Japanese and called `hn_item` against the real API from a Linux machine. Not yet checked: `personad extension test`, installation on a hub, the extension sandbox and network allowlist, and rendering in the real apps.

## Package and install

From the repository root:

```sh
scripts/package.sh hackernews
```

On the hub's machine, replace `<version>` with the manifest version:

```sh
personad extension install file:/path/to/hackernews-<version>.tar.gz
```

Pre-rename hubs use `personad plugin install …`. See [installation](../installation.md) for the public market and compatibility details.
