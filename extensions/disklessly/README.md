# Disklessly

[Documentation](../../docs/extensions/disklessly.md) · [All extensions](../../docs/README.md)

Disklessly integrates multi-machine disk watermark monitoring, safe developer cache reclamation, and coding-agent cold session tiering into Persona's machine management. It automatically discovers local and SSH machines from `~/.persona/config.json`, `~/.ssh/config`, and Disklessly's own configuration, probes root volume watermarks and six reclaimable categories in a single read-only pass, generates dry-run cleanup plans, measures actual freed bytes via `df` before and after cleanup, and has the `disklessly` CLI upload cold (>7 days) Claude Code and Codex session transcripts to its backup backend before their local copies are removed, skipping sessions that running agents, open files or herdr / Persona panes still reference (the extension probes references once before the run; the CLI re-reads that exclusion list and re-scans running processes and herdr's `session.json` before each delete). Session upload runs only when asked for explicitly, never as part of a one-tap safe cleanup. A local lightweight index of the archived sessions powers search, cloud location badges and one-tap restore in the **Agent 对话** page.

See the [full guide](../../docs/extensions/disklessly.md) for the scan categories, safety boundaries, UI views, and stdio MCP tools.

## Verify

```sh
go -C extensions/disklessly test -race -count=1 ./...
./scripts/package.sh disklessly
```
