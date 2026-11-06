# Sentry

[Documentation](../README.md) · [Source](../../extensions/sentry/)

Sends the errors and crashes of every part of Persona to your Sentry project, so problems show up there grouped, with their stack traces, releases and how often they happen.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `error_reports` | `sentry`, all sources | Each report the hub gives (`error_reports.deliver`) becomes one Sentry event posted to the project's envelope endpoint. |

Needs a hub with the `error_reports` extension point (Persona #5533).

## What it sends, and what it never sees

The hub scrubs every report before an extension gets it: no message text, tokens, user names in paths, machine names, IP addresses or URL hosts and queries (see Persona's `docs/extensions.md`, "Error reports"). The hub also deduplicates retries, folds the same problem within 10 minutes into one report with a count, and rate limits each source.

Each event carries:

- `event_id`: derived from the report's source and id, so a report sent twice is one event;
- `release` `persona-<source>@<version>+<build>` and `dist` `<build>`, `environment` from the settings;
- `platform` by source (`go` for the hub and the manager, `javascript` for the web, `java` for Android, `cocoa` for Apple);
- tags `source` and `kind` (`exception`, `crash`, `anr`, `panic`, `log`), plus the report's own tags;
- the exceptions with their stack traces (innermost cause first, as Sentry lists them), breadcrumbs, the OS and the device model;
- `fingerprint`: the hub's, so Sentry groups by the same problem the hub folds by;
- `extra`: `count` (how many the hub folded into it), `previous_launch`, `received_at`.

No `server_name` and no user are set.

## Settings

| Setting | Default | |
| --- | --- | --- |
| DSN (secret, required) | — | The project's client key: Settings › Projects › your project › Client Keys (DSN). Must be https. |
| `environment` | `production` | The events' environment. |
| `sample_rate` | `1` | 0 to 1: the share of reports sent, at random. |
| `include_log_lines` | on | The hub's and the manager's log lines that read as errors (`kind: log`). Off: only exceptions, crashes, ANRs and panics. |

Without a DSN the hub does not start the extension, the `error_reports` feature stays off, and nothing is collected by the hub or the apps.

## Limits and network

- At most 60 events a minute, and a queue of 200; beyond that reports are dropped (the hub has its own limits before).
- Sentry's rate limits are honored (`X-Sentry-Rate-Limits`, `Retry-After` on 429): reports until then are skipped, not queued.
- `permissions.network` is `*.sentry.io`, which covers the ingest hosts of sentry.io (`o<org>.ingest.sentry.io`, `o<org>.ingest.us.sentry.io`). A self-hosted Sentry on another domain is reachable only while the extension's sandbox is off (the default for the official marketplace).
- Its log says each event id it sent (`sentry: sent event <id>`), never the DSN's key.
