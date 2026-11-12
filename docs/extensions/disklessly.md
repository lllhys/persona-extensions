# Disklessly

[Documentation](../README.md) · [Source](../../extensions/disklessly/)

Disklessly connects multi-machine disk watermark inspection, rebuildable developer cache cleanup, and coding-agent cold session archiving to Persona's machine management. It manages the local computer alongside remote SSH hosts discovered from `~/.persona/config.json`, `~/.ssh/config`, or added from the extension page and MCP tools.

All state is stored locally in the extension's data directory as `disklessly_state.json`, shared between the hub extension process and `disklessly mcp` over stdio.

## Machine discovery and fleet watermarks

On startup and on demand, Disklessly merges machines from:

1. **This machine (`local`)**: always present and enabled first.
2. **Persona machine configuration**: `~/.persona/config.json` and `~/.config/persona/config.json` (`machines` or `nodes`).
3. **OpenSSH client configuration**: concrete `Host` blocks in `~/.ssh/config` (wildcard patterns such as `*` apply as defaults; negated or wildcard tokens are skipped).
4. **Manual SSH nodes**: added or edited from the page or with `disklessly_machine_upsert`.

Each machine's root volume (`/System/Volumes/Data` on macOS, `/` on Linux) is evaluated into a health watermark:

| Level | Condition | Badge & progress bar |
| --- | --- | --- |
| `ok` | Reachable and below warning thresholds | Green (`positive`) |
| `warning` | Used ≥ 80% or free < 25 GiB (on ≥ 50 GiB disks) | Orange (`caution`) |
| `critical` | Used ≥ 90% or free < 10 GiB (on ≥ 20 GiB disks) | Red (`danger`) |
| `offline` | Last SSH probe failed | Caution (`offline`) |

## Scan categories and safety redlines

A scan runs a single read-only POSIX shell probe locally (`/bin/sh`) or over OpenSSH (`BatchMode=yes`, `ConnectTimeout=12`). When the target host has the `disklessly` CLI installed (`disklessly`, `~/.disklessly/bin/disklessly`, or `/Applications/Disklessly.app/Contents/MacOS/disklessly`), its version is recorded and the probe also reads `disklessly --json sessions` (the CLI's own cold-session plan), `disklessly --json sessions status` (backup backend reachability, index counts and archived bytes). After every successful scan of a machine with the CLI, the extension refreshes that machine's cold index (see below). Without the CLI the built-in shell probe still measures caches and estimates hot and cold sessions, but cold sessions are not archivable.

| Category | ID | What it probes | Safety |
| --- | --- | --- | --- |
| Go build & module cache | `go-cache` | `GOCACHE` (`~/.cache/go-build`) and `GOMODCACHE` (`~/go/pkg/mod`) | `safe` |
| Node & Python caches | `node-python-cache` | `~/.npm`, `~/.local/share/pnpm/store`, `~/.cache/pnpm`, `~/.cache/yarn`, `~/.cache/pip`, `~/.cache/uv` | `safe` |
| Docker unused artifacts | `docker-unused` | Dangling images (`dangling=true`) and unused BuildKit build cache (`docker system df`) | `safe` |
| Rust, Xcode & compiler caches | `rust-xcode-build` | `~/.cargo/registry`, `~/.cargo/git`, `~/Library/Developer/Xcode/DerivedData`, `~/.cache/org.swift.swiftpm`, `~/.cache/clang`, `~/.gradle/caches`, and `~/.cache/ms-playwright` (`review`) | `safe` / `review` |
| System logs & stale `/tmp` | `tmp-logs` | `journalctl` logs older than 7 days, rotated `/var/log/*.gz` / `*.1` / `*.old`, and `/tmp` build/test directories older than 7 days | `safe` |
| Cold coding-agent sessions | `cold-agent-sessions` | Claude Code (`~/.claude/projects`) and Codex (`~/.codex/sessions`) transcripts the `disklessly` CLI plans to archive (idle over 7 days, not open, not excluded) | explicit only (uploaded, then removed) |

### Safety redlines

- **Running containers are never touched**: only dangling images and unreferenced BuildKit caches are cleaned.
- **Active git repositories and uncommitted changes are never touched**.
- **Cold sessions are archived only by the `disklessly` CLI**: the cleaner runs `disklessly --json sessions --apply --days 7 --exclude-from <list>` once (the scan's plan is `disklessly --json sessions --days 7`; the 7-day threshold is always passed, so the CLI's own default does not apply) and never deletes a transcript itself. The CLI uploads each session to the backup backend and removes the local copy only after the upload is committed. Hot sessions (modified within 7 days), open sessions and excluded sessions stay in place. There is no local tar fallback: if the CLI is missing, the backend is unreachable (exit 4, `backend unreachable`) or the run fails, the clean reports an error and no session is uploaded or deleted. The page offers archiving only when the last scan found the CLI and a reachable backend. Because sessions leave the machine, they are never part of a generic safe cleanup (**一键安全清理**, the plan's run-all-safe button, or `disklessly_clean` without `categories`/`items`) and do not count in the safe totals; they run only when `cold-agent-sessions` or a session item is named, or through **冷会话上云** / `disklessly_sessions_offload`. One CLI run covers every agent (Claude Code, Codex, pi, Gemini, Qwen, …), so naming one session item selects them all, and archives of agents without a scan item are recorded as `sessions-cold-other`.
- **Referenced sessions are excluded**: `disklessly_clean` and `disklessly_sessions_offload` accept `exclude_sessions` (absolute transcript paths or session ids: the Claude file stem or the trailing UUID of a Codex rollout file). They are combined with the target machine's optional `~/.disklessly/session-excludes` file and with the reference probe's findings, passed as `--exclude-from`. The extension builds this list once, before the CLI starts; the CLI then re-reads the exclusion list and runs its own scan of running processes and herdr's `session.json` right before each local delete, so a session that becomes referenced during the run is still kept.
- **Reference probe (fail closed)**: once, before `--apply` starts, a POSIX shell probe on the target machine (Linux and macOS, also over SSH) adds every session something still uses: session UUIDs after `--resume`, `-r` or `--session-id`, and every UUID in a `claude` or `codex` command line (so `codex resume [OPTIONS] <id>` and `codex exec resume` count), in running processes (`/proc/*/cmdline` on Linux, `ps -axww -o pid= -o args=` on macOS); for a `claude` or `codex` process without a session id in its argv (`claude -c`, the `--resume` picker, `codex resume --last`), every transcript of its working directory (`/proc/<pid>/cwd` or `lsof -d cwd`): the Claude project folder for that cwd and the Codex rollouts whose `session_meta` names it; every open `*.jsonl` (`/proc/*/fd` on Linux, `lsof -u <uid>` on macOS); and every UUID and absolute `*.jsonl` path in `~/.config/herdr/session.json` (where Persona keeps resumable agents as `agent_resume` argv `--resume <id>`) and in `herdr pane list` (5 s limit, only when `herdr` is on `PATH`). The probe adds what the CLI's own pre-delete check does not cover (open files via `/proc/*/fd` or `lsof`, `herdr pane list`, the caller's `exclude_sessions`); it does not run again during the CLI run. Over-inclusion only keeps a file local. If neither `/proc` nor `ps` can list processes, the run stops before the CLI starts and nothing is uploaded or deleted. The number of sessions kept as referenced is recorded in the history ledger.
- **Credentials and keys are strictly refused**: `CheckSafeCleanTarget` blocks system roots, user home roots, `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.persona`, and `~/.disklessly/keys`.
- **In-use process guard**: before deleting cache children, the cleaner checks running toolchain processes (`go`, `gopls`, `pnpm`, `npm`, `cargo`, `rustc`, `swift`, `clang`, `xcodebuild`, `pip`, `uv`, `GradleDaemon`) and `/proc/*/maps` / `/proc/*/exe`, skipping any entry in use and reporting it as `@@BUSY`.

## The page

| Contribution | Placement | Behavior |
| --- | --- | --- |
| `ui` `disklessly` | `nav_page` | Multi-machine selector filter, four tabs (`多机总览`, `可回收明细`, `清理计划`, `会话与台账`), search, category checklists, and SSH machine forms. |
| `ui` `disklessly-sessions` | `nav_page` | **Agent 对话**: the cold-session backup page on its own (machine filter, cloud status, search, results, restore). |
| `ui` `disklessly-card` | `overview_card` | Fleet watermark summary for up to four machines, total reclaimable bytes, and the latest `df`-measured cleanup result. |
| `skills` `disklessly` | skill | Guides the assistant to inspect machine status and dry-run scan plans before running confirmed cleanups. |
| `mcp_servers` `disklessly` | stdio MCP | `disklessly mcp`. Read-only tools are in `allow`; cleanup and machine-node mutations ask for confirmation. |

The navigation page provides four tabs for the selected machine (and across the fleet):

1. **多机总览 (Fleet Overview)**: stat grid of managed machines, watermark alerts, total reclaimable cache, and cumulative freed bytes; per-machine watermark progress bars (`positive` / `caution` / `danger`) with one-tap **扫描**, **生成清理计划**, and **一键安全清理**.
2. **可回收明细 (Reclaimable Items)**: category summary table with one-tap per-category cleanup, plus a volume-sorted item table showing path, category, size, and `safe` / `review` badge.
3. **清理计划 (Dry-Run Plan)**: compares current available space with projected post-cleanup free space, provides interactive category toggles (`cat_<id>`) and a `review` toggle, and lists every planned target.
4. **会话与台账 (Sessions & History)**: the cold-session backup view (below), hot (≤7d) and cold session counts and sizes for Claude Code and Codex, followed by the cleanup history ledger recording `before → after (+freed)` measured by `df` and the number of sessions kept as referenced.

### Cold-session backup and search

The **会话与台账** tab and the **Agent 对话** page share one view: a search box (`搜索会话：标题、首条消息、项目、关键词`), the cloud status (indexed sessions, sessions in the cloud, unknown / missing, backend reachability per machine, last check), the buttons **冷会话上云（读回校验后删本地）**, **刷新云端索引与状态** and, with a query, **全文搜索**, and the results. Without a query it lists the most recently archived sessions. Each result row shows the title, machine · Claude Code/Codex · project · date, the first prompt, and a location badge; rows not already local have a **恢复** action that restores the transcript to its original path and shows the resume command (`claude --resume <id>` or `codex resume <id>`). Page state holds only the machine and the query.

**Local cold index.** After a scan, after an offload and on refresh, the extension runs `disklessly --json sessions list --state all --limit 5000` and `disklessly --json sessions status --check-segments` on the machine (a scan does so only when the index is older than six hours; refresh and offload always do), plus `sessions list --state all --digest --limit 5000` only when the index has rows it has no keywords for yet, and stores, per machine, the backend (`id`, `type`, `reachable` — unknown when the check failed —, latency, error, check time), the missing and unchecked segments, and up to 5,000 entries, newest first (`capped` is shown when the status counts are higher): id, session id, tool, path, cwd, project, month, mtime, archive time, bytes, state, title (≤120 characters), first prompt (≤240 characters), segment, and up to 16 keywords extracted locally from the digest (Latin and digit words of 3 to 24 characters without stopwords and hex-like hashes, CJK runs clipped to 2–6 characters, by frequency). The digest itself is never stored.

**Location.** `在云端` needs state `archived` or `remote` (or `pendingDelete` when its segment also holds an `archived` or `remote` row, since only those segments are checked), a backend check from the last 24 hours that found it reachable, a known segment that is neither missing nor unchecked, and a reachable machine. `云端缺失` means the segment was reported missing; `本地` means `restored` or `keptLocal`; anything else (backend unreachable or unchecked, never or not recently checked, machine offline) is `未知`, which is never treated as absent.

**Search.** Every whitespace-separated token must occur, case-insensitively (CJK as a substring), in the title, first prompt, keywords, cwd, project, path, session id, tool or machine name; results are newest first. Queries are cut to 200 characters. Another device's rows (state `remote` after `sessions reindex`) are shown only under their own machine when it is managed here, and are marked 来自其他设备. **全文搜索** (and `deep: true` over MCP) also runs `disklessly --json sessions search WORD… --limit N` on the machines (leading dashes dropped, since the CLI reads them as options) and merges the hits by entry id. Restoring runs `disklessly --json sessions restore ID` (also for a full-text hit outside the local index), checks that the returned path is back and not empty, marks the entry local and records it in the history. Another device's row is not restored: its original path belongs to that device, so the extension points to the CLI's `--to`. The resume hint is `claude --resume <id>` or `codex resume <id>`; other agents get none.

## The MCP server

`disklessly mcp` speaks MCP over stdio (JSON-RPC 2.0, one message per line: `initialize`, `ping`, `tools/list`, `tools/call`). `disklessly --help` names `mcp`, which the manifest's `requires` checks. Set `PERSONA_DISKLESSLY_BIN` when `disklessly` is not on `PATH` or in `~/bin`.

| Tool | Access | What it does |
| --- | --- | --- |
| `disklessly_machines_status` | read, allowed | Lists all enabled machines with root volume watermarks (`ok`, `warning`, `critical`, `offline`), reclaimable bytes, session tiering summary, and cumulative freed space. Pass `refresh: true` to re-scan `~/.persona/config.json` and `~/.ssh/config`. |
| `disklessly_scan` | read, allowed | Runs a read-only probe on `machine` (default `local`, or `cached: true`), returning root mount capacity, reclaimable items sorted by size, session tiering, and the dry-run plan. |
| `disklessly_history` | read, allowed | Returns the cleanup history ledger (optionally filtered by `machine`, up to `limit`) with `df`-measured `before → after (+freed)` records and cumulative freed bytes. |
| `disklessly_sessions_search` | read, allowed | Searches the local cold index (`query` required; optional `machine`, `limit`, and `deep: true` for the CLI's full-text search), returning machine, tool, project, date, first prompt, location and the resume command. |
| `disklessly_sessions_status` | read, allowed | Per-machine cloud backup status: backend reachability and check time, indexed and total sessions, counts by location, missing and unchecked segments. `refresh: true` re-reads the index and checks the backend first. |
| `disklessly_clean` | write, asks | Executes a cleanup plan on `machine` for selected `categories`, `items`, or all `safe` items (`include_review: true` includes `review` items; cold sessions only when named; `dry_run: true` previews only), measures freed space via `df`, and records the result in the history ledger. |
| `disklessly_machine_upsert` | write, asks | Adds or updates a managed SSH machine node (`host`, optional `id`, `name`, `user`, `port`, `identity_file`). |
| `disklessly_machine_delete` | write, asks | Removes a remote machine node (or disables a discovered host so it is not re-added automatically). `local` cannot be deleted. |
| `disklessly_sessions_offload` | write, asks (destructive) | Uploads `machine`'s cold sessions through the CLI with the reference probe and `exclude_sessions`; the local copy is deleted only after the upload is read back and verified. `dry_run: true` returns the plan and the sessions currently referenced. |
| `disklessly_sessions_restore` | write, asks | Restores entry `id` of `machine` to its original path and returns the resume command. |

## Storage

The extension and `disklessly mcp` share `disklessly_state.json` in the extension's data directory. Every read reloads the file; every write acquires an exclusive `flock` on `.disklessly.lock`, applies the change, and atomically renames a temporary file over `disklessly_state.json`. The extension watches the file every two seconds and calls `hub.Invalidate("")` when an external MCP write updates it. Each machine's cold index is a separate compact file, `disklessly_cold/<machine id>.json`, written under the same lock only when it changed and read only by the session views and tools (parsed copies are cached by file time and size).

`disklessly mcp` resolves its data directory from `--data`, else `PERSONA_DISKLESSLY_DATA`, else the path written by the hub extension to `<user config dir>/persona-disklessly/data-dir`, else `<user config dir>/persona-disklessly/data`.

## Tests

```sh
go -C extensions/disklessly test -race -count=1 ./...
```

The test suite covers SDK protocol conformance, manifest and `mcp.allow` verification, SSH and Persona config discovery, health watermark thresholds (`ok`, `warning`, `critical`, `offline`), probe output parsing, safety redline enforcement, dry-run plan generation, `df`-measured cleanup and cold-session archiving, the reference probe under `/bin/sh` (synthetic herdr state, resumed processes, open transcripts, and the fail-closed path), the cold index (keywords, caps, locations, search, deep search, restore), multi-process store locking, all UI tabs/forms/cards and the Agent 对话 page validated with `wire.CheckView`, and all MCP tools.
