# openGym

[Documentation](../README.md) · [Source](../../extensions/opengym/)

[openGym](https://opengym.ch) ([docs](https://opengym.ch/docs.html), [source](https://github.com/DuarteSantos8/openGym)) is a workout log and AI coach for iPhone and Apple Watch that runs on the phone alone or with a self-hosted server. This extension reads and writes the profile on **a self-hosted openGym server** through its scoped API tokens. Design and research: [#586](https://github.com/nwverse/persona-extensions/issues/586).

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `ui` | `opengym`, placement `nav_page` ("健身") | Tab 总览: this week's sessions, volume (with a 12-week sparkline), time and body weight; the next planned session with its exercises; a bar chart of the last 12 weeks' volume; the last 10 workouts (tap one for its exercises and sets, PRs marked); a 90-day body-weight line. Tab 计划: the next session, the next 7 days and why each is planned, the running coach week, and every routine. |
| `ui` | `opengym-card`, placement `overview_card` | This week's sessions and volume against last week, and the next session. Opens the page. |
| `connectors` | `opengym`, capabilities `status`, `tools` and `actions` | Read tools `opengym_summary`, `opengym_workouts`, `opengym_plan`, `opengym_bodyweight`, `opengym_exercises`; writes `opengym_log_workout`, `opengym_create_routine`, `opengym_schedule_day`, all confirmed on the phone every time. |
| `skills` | `opengym` | When the assistant uses which tool, units, and the confirmation rules. |

## What openGym offers another program

| Surface | Used | Why |
| --- | --- | --- |
| Self-hosted server, scoped API tokens: `GET /api/token/me`, `GET /api/token/data`, `GET /api/token/data/rev`, `PUT /api/token/data` | Yes | The only routes openGym meant for third-party programs: a token per program, scopes per category, revocable. |
| Calendar feed (`/api/calendar/<token>.ics`), webhooks (`workout.finished`, `workout.pr`, `weighin.logged`) | Not yet | Less detail than the data route, or push only (the openGym server would have to reach the hub). |
| JSON backup, daily WebDAV backup, CSV export | Not yet | The phone-only option: a day late and read-only. |
| Apple Health | No | openGym writes workouts and weigh-ins to HealthKit, which only apps on the phone can read; the hub cannot. |
| Phone-only openGym, live | No | Without a server the data lives only in the app. |

## Configuration

[Manifest and settings schema](../../extensions/opengym/persona-extension.json)

| Setting | Meaning |
| --- | --- |
| `server_url` | The openGym server the hub's machine reaches, with its `BASE_PATH` if it has one (`https://gym.example.com`, `https://example.com/gym`). Plain `http` only for addresses that stay on the local network: loopback, private and link-local IPs, a name without a dot, or a name under `.local`, `.lan`, `.home.arpa` or `.internal`. User info, a query or a fragment is refused. |
| `refresh_minutes` | How often the background loop checks the revision, 5 to 1440, default 15. The whole document is read only when the revision moved. |
| `exercise_names` | Show exercises by their Chinese names (default on): openGym's public catalogue names (`catalogue/i18n/zh.json` on GitHub) are downloaded at most once a week, conditionally, and only id → name is kept in the data directory. Off or unreachable: the profile's own exercises by name, others as `动作 <id>`. |

| Secret | Meaning |
| --- | --- |
| `api_token` | An openGym API token (`ogt_…`): in the app, 设置 › 集成 › API 令牌 (Settings → Integrations → API tokens), shown once. Fill it in on the extension's settings card on the phone, never in a chat. Scopes: 训练 (workouts), 计划 (plans) and 体重 (bodyweight) at 查看 (view) to read, at 应用 (apply) for the assistant's writes; 测量 and 笔记 are not used. It is sent only to `server_url` (redirects to another host or from https to http are refused) and never appears in logs, errors, status or tool output. |

openGym limits a token to 120 requests a minute and 60 writes an hour. The extension makes a revision check per `refresh_minutes`, a page or tool reads at most once a minute, and each write is one `PUT` (two after a conflict) plus a read back.

## Network and sandbox

`permissions.network` is `raw.githubusercontent.com` (the exercise names) and `host.persona.internal`. The server is the user's own, so the manifest cannot list it. An openGym on the hub's own machine at `http://127.0.0.1:<port>` works in the sandbox: behind the sandbox's proxy the extension asks `host.persona.internal:<port>` instead. Any other server needs the sandbox off when installed from a local package: `personad extension sandbox opengym off`.

## Reading

- `GET /api/token/me` gives the account name and the token's scopes; tools and the page say which scope is missing instead of failing.
- The document is kept in memory; the page, the card and the tools answer from it and check the revision when it is over a minute old. When the server cannot be reached, they keep showing what was last read and say so.
- Volume is the workout's stored `vol`, else Σ weight × reps over done sets that are not warm-ups; timed and cardio sets add none. Weeks start on Monday in the hub's time zone (the app's on the page). Weights are in the profile's unit (`kg` or `lb`).
- The plan follows openGym's rules (its `api/queue.js`, implemented anew here): a date's own entry in `dayPlan` (`rest` or a routine) wins; a live coach week (`queue`) gives its sessions in order on whatever day the user trains, a session pinned to a date belongs to that date, and a session counts as done once a workout on its routine started after the week was applied (or, unless strict, one dated within the week whose name names it); then the weekly plan, without the coach week's routines while that week is live. The next session is today's pinned or planned one while nothing was logged today, else the coach week's next, else the first planned date within two weeks.
- Never readable by a token: the workout in progress, photos, the coach conversation and linked services.

## Writing

All writes are `confirm_always`. `actions.describe` checks the arguments against the cached document and names the exercises, sets and volume without reaching the network; `actions.do` reads the document again and sends `PUT /api/token/data` with `baseRev`/`baseWid`, replacing only the top-level keys it changes. Items it does not change are sent back byte for byte. A `409` conflict is built once more over the document the conflict carries; a second one gives up. Each request id runs once.

| Write | Arguments | What it changes |
| --- | --- | --- |
| `opengym_log_workout` | `date` (today by default, not later), `name`, `start_time`, `duration_minutes`, `note`, `exercises[]` of `exercise` (id or exact name), `create_custom`, `body_part`, `note`, `sets[]` of `reps` + `weight`, `seconds`, or `minutes`, and `warmup` | Adds a finished workout to `workouts` in date order, with its volume; a new custom exercise goes to `customEx`. The same workout on the same day is not added twice. openGym's PR badges and confirmed weights are not recomputed. Needs workouts at apply. |
| `opengym_create_routine` | `name`, `date`, `exercises[]` of `exercise`, `create_custom`, `sets` (1–20), `reps` (1–100) or `seconds`, `weight`, `rest_seconds`, `note` | Adds a routine to `routines`, and with `date` plans it there (`dayPlan`). Needs plans at apply. |
| `opengym_schedule_day` | `date` (today or later) and one of `routine_id`, `rest: true`, `clear: true` | Sets or removes one date in `dayPlan`. Needs plans at apply. |

An exercise is an id the profile used, its own, or a catalogue id, or a name that matches exactly one exercise; anything else is refused with candidates (never guessed), unless `create_custom` makes it a custom exercise.

## Status

`not_configured` without a usable `server_url` or without `api_token`, with what to do. `error` while the last check failed (a refused token says to make a new one; `404` says to check the address and `BASE_PATH`), otherwise `ok` with the account name and the last check.
