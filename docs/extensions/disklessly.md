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

A scan runs a single read-only POSIX shell probe locally (`/bin/sh`) or over OpenSSH (`BatchMode=yes`, `ConnectTimeout=12`). When the target host has the `disklessly` CLI installed (`disklessly`, `~/.disklessly/bin/disklessly`, or `/Applications/Disklessly.app/Contents/MacOS/disklessly`), its version is recorded and invoked during session archiving; otherwise the built-in shell probe handles everything without installing extra software.

| Category | ID | What it probes | Safety |
| --- | --- | --- | --- |
| Go build & module cache | `go-cache` | `GOCACHE` (`~/.cache/go-build`) and `GOMODCACHE` (`~/go/pkg/mod`) | `safe` |
| Node & Python caches | `node-python-cache` | `~/.npm`, `~/.local/share/pnpm/store`, `~/.cache/pnpm`, `~/.cache/yarn`, `~/.cache/pip`, `~/.cache/uv` | `safe` |
| Docker unused artifacts | `docker-unused` | Dangling images (`dangling=true`) and unused BuildKit build cache (`docker system df`) | `safe` |
| Rust, Xcode & compiler caches | `rust-xcode-build` | `~/.cargo/registry`, `~/.cargo/git`, `~/Library/Developer/Xcode/DerivedData`, `~/.cache/org.swift.swiftpm`, `~/.cache/clang`, `~/.gradle/caches`, and `~/.cache/ms-playwright` (`review`) | `safe` / `review` |
| System logs & stale `/tmp` | `tmp-logs` | `journalctl` logs older than 7 days, rotated `/var/log/*.gz` / `*.1` / `*.old`, and `/tmp` build/test directories older than 7 days | `safe` |
| Cold coding-agent sessions | `cold-agent-sessions` | Claude Code (`~/.claude/projects`) and Codex (`~/.codex/sessions`) transcripts older than 7 days | `safe` (archived) |

### Safety redlines

- **Running containers are never touched**: only dangling images and unreferenced BuildKit caches are cleaned.
- **Active git repositories and uncommitted changes are never touched**.
- **Hot coding-agent sessions (modified within 7 days) are never touched**: only cold sessions (>7 days) are archived into compressed tarballs under `~/.disklessly/session-archives` (and via `disklessly sessions --apply` when the CLI is installed).
- **Credentials and keys are strictly refused**: `CheckSafeCleanTarget` blocks system roots, user home roots, `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.persona`, and `~/.disklessly/keys`.
- **In-use process guard**: before deleting cache children, the cleaner checks running toolchain processes (`go`, `gopls`, `pnpm`, `npm`, `cargo`, `rustc`, `swift`, `clang`, `xcodebuild`, `pip`, `uv`, `GradleDaemon`) and `/proc/*/maps` / `/proc/*/exe`, skipping any entry in use and reporting it as `@@BUSY`.

## The page

| Contribution | Placement | Behavior |
| --- | --- | --- |
| `ui` `disklessly` | `nav_page` | Multi-machine selector filter, four tabs (`多机总览`, `可回收明细`, `清理计划`, `会话与台账`), search, category checklists, and SSH machine forms. |
| `ui` `disklessly-card` | `overview_card` | Fleet watermark summary for up to four machines, total reclaimable bytes, and the latest `df`-measured cleanup result. |
| `skills` `disklessly` | skill | Guides the assistant to inspect machine status and dry-run scan plans before running confirmed cleanups. |
| `mcp_servers` `disklessly` | stdio MCP | `disklessly mcp`. Read-only tools are in `allow`; cleanup and machine-node mutations ask for confirmation. |

The navigation page provides four tabs for the selected machine (and across the fleet):

1. **多机总览 (Fleet Overview)**: stat grid of managed machines, watermark alerts, total reclaimable cache, and cumulative freed bytes; per-machine watermark progress bars (`positive` / `caution` / `danger`) with one-tap **扫描**, **生成清理计划**, and **一键安全清理**.
2. **可回收明细 (Reclaimable Items)**: category summary table with one-tap per-category cleanup, plus a volume-sorted item table showing path, category, size, and `safe` / `review` badge.
3. **清理计划 (Dry-Run Plan)**: compares current available space with projected post-cleanup free space, provides interactive category toggles (`cat_<id>`) and a `review` toggle, and lists every planned target.
4. **会话与台账 (Sessions & History)**: shows hot (≤7d) vs. cold (>7d) and archived session counts and sizes for Claude Code and Codex with one-tap cold session archiving, followed by the cleanup history ledger recording `before → after (+freed)` measured by `df`.

## The MCP server

`disklessly mcp` speaks MCP over stdio (JSON-RPC 2.0, one message per line: `initialize`, `ping`, `tools/list`, `tools/call`). `disklessly --help` names `mcp`, which the manifest's `requires` checks. Set `PERSONA_DISKLESSLY_BIN` when `disklessly` is not on `PATH` or in `~/bin`.

| Tool | Access | What it does |
| --- | --- | --- |
| `disklessly_machines_status` | read, allowed | Lists all enabled machines with root volume watermarks (`ok`, `warning`, `critical`, `offline`), reclaimable bytes, session tiering summary, and cumulative freed space. Pass `refresh: true` to re-scan `~/.persona/config.json` and `~/.ssh/config`. |
| `disklessly_scan` | read, allowed | Runs a read-only probe on `machine` (default `local`, or `cached: true`), returning root mount capacity, reclaimable items sorted by size, session tiering, and the dry-run plan. |
| `disklessly_history` | read, allowed | Returns the cleanup history ledger (optionally filtered by `machine`, up to `limit`) with `df`-measured `before → after (+freed)` records and cumulative freed bytes. |
| `disklessly_clean` | write, asks | Executes a cleanup plan on `machine` for selected `categories`, `items`, or all `safe` items (`include_review: true` includes `review` items; `dry_run: true` previews only), measures freed space via `df`, and records the result in the history ledger. |
| `disklessly_machine_upsert` | write, asks | Adds or updates a managed SSH machine node (`host`, optional `id`, `name`, `user`, `port`, `identity_file`). |
| `disklessly_machine_delete` | write, asks | Removes a remote machine node (or disables a discovered host so it is not re-added automatically). `local` cannot be deleted. |

## Storage

The extension and `disklessly mcp` share `disklessly_state.json` in the extension's data directory. Every read reloads the file; every write acquires an exclusive `flock` on `.disklessly.lock`, applies the change, and atomically renames a temporary file over `disklessly_state.json`. The extension watches the file every two seconds and calls `hub.Invalidate("")` when an external MCP write updates it.

`disklessly mcp` resolves its data directory from `--data`, else `PERSONA_DISKLESSLY_DATA`, else the path written by the hub extension to `<user config dir>/persona-disklessly/data-dir`, else `<user config dir>/persona-disklessly/data`.

## Tests

```sh
go -C extensions/disklessly test -race -count=1 ./...
```

The test suite covers SDK protocol conformance, manifest and `mcp.allow` verification, SSH and Persona config discovery, health watermark thresholds (`ok`, `warning`, `critical`, `offline`), probe output parsing, safety redline enforcement, dry-run plan generation, `df`-measured cleanup and cold-session archiving, multi-process store locking, all UI tabs/forms/cards validated with `wire.CheckView`, and all six MCP tools over stdio.
