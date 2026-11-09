# Disklessly

[Documentation](../../docs/extensions/disklessly.md) · [All extensions](../../docs/README.md)

Disklessly integrates multi-machine disk watermark monitoring, safe developer cache reclamation, and coding-agent cold session tiering into Persona's machine management. It automatically discovers local and SSH machines from `~/.persona/config.json`, `~/.ssh/config`, and Disklessly's own configuration, probes root volume watermarks and six reclaimable categories in a single read-only pass, generates dry-run cleanup plans, measures actual freed bytes via `df` before and after cleanup, and archives cold (>7 days) Claude Code and Codex session transcripts.

See the [full guide](../../docs/extensions/disklessly.md) for the scan categories, safety boundaries, UI views, and stdio MCP tools.

## Verify

```sh
go -C extensions/disklessly test -race -count=1 ./...
./scripts/package.sh disklessly
```
