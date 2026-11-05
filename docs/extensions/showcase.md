# Showcase

[Documentation](../README.md) · [Source](../../extensions/showcase/)

A page for every component and state of an extension view. Extension authors can inspect rendering, and web, Android, iOS and Mac clients can use the same views for screenshot alignment. All data is local: no network, no secrets, and `permissions` is empty.

## Contributions and pages

| Contribution | Behavior |
| --- | --- |
| `ui` `showcase` (`nav_page`) | A component catalog; each row pushes its example page. |
| `ui` `showcase-card` (`overview_card`) | A card with side-by-side metrics and a small bar chart; opens the catalog. |
| `ui` `showcase-widget` (`widget`) | Separate views for small, medium, large and the three accessory widget/watch-face families. |
| `shortcuts` `showcase-echo`, `showcase-count` | Echo text or return a count result card; examples of text, number, choice and toggle parameters. |

The pages cover text styles and long text, metrics, progress, rows (badges, icons, avatars and thumbnails), buttons (including destructive confirmation), links, five field kinds with submission, bar/stacked/line charts and negative values, stat grids of 1–6 items, segmented selection, safe-subset Markdown, narrow/eight-column tables, empty/loading states and dividers.

The list demonstrates search, filtering and pagination: 60 items, 20 at a time. Its navigation flow is list → detail → edit → save → pop, with the saved detail drawn again. The invalidate page answers with loading immediately and sends `ui.invalidate` three seconds later to redraw the visible page.

Before UI version 3, list controls use fields and buttons; the SDK converts push navigation to drawing the new page in place. Each page includes a return-to-catalog button for that fallback.

## Shared view snapshots

[Snapshots](../../extensions/showcase/snapshots/) contain each page's UI version 3 View as `snapshots/<page>.json`. Client screenshot tests can render these directly without a hub. The host's screenshot suite also keeps a corresponding copy in its `schema/extension/examples/showcase/` directory; that is a host-side path, not a directory in this checkout.

Run from this repository's root:

```sh
go test ./extensions/showcase -run TestSnapshots -update   # Regenerate snapshots after a view change.
go test ./extensions/showcase                             # Validate views, snapshots and component coverage.
```

Review snapshot diffs after a behavioral change. Coordinate updating the host/client copy of `snapshots/` through its own workflow so screenshot inputs remain aligned. Do not update snapshots merely to translate documentation; their localized content is runtime/test data.

Protocol conformance can also be checked with a compatible `personad`:

```sh
personad extension test extensions/showcase
```

## Package and install

Use the [public market](../installation.md), or package locally from the repository root:

```sh
scripts/package.sh showcase
```

The [manifest](../../extensions/showcase/persona-extension.json) declares its contributions and platforms. The extension's UI text remains localized; this guide does not change view behavior.
