# Search as Code

Executable Search-as-Code pipelines over local coding-agent session transcripts (`~/.claude/projects`, `~/.codex/sessions`, and configured directories) and local repositories, inspired by Perplexity's *Rethinking Search as Code Generation* and CASS session search.

Instead of a single flat keyword lookup, complex investigative queries compile into a multi-step pipeline (`scan_sessions` / `scan_files` `|>` `exclude_tools` `|>` `match_regex` `|>` `correlate` `|>` `group_by` `|>` `rank` `|>` `limit`) with full step-by-step execution traces (`StepTraces`), role-attributed evidence snippets (`User`, `Assistant`, `Error`, `File`), cross-session file/project correlation, and reusable search recipes. A local MCP server (`search-as-code mcp`) exposes the same engine to the assistant on stdio.

Sessions that the disklessly CLI moved to the cloud stay searchable through the plain-text index it keeps on this computer (`<stateDir>/sessions/text/<entryId>.jsonl`): such hits are marked `已上云` and can be restored to their original path with one click (or `sac_session_restore`), which runs the local `disklessly` CLI. The restore runs in the background and its outcome appears in a `会话恢复` section on the page. An index whose upload never completed (empty `segment`/`archivedAt`) is marked `未确认上云` and is not offered for restore.

- [Guide](../../docs/extensions/search-as-code.md)
- [Manifest](persona-extension.json)
- [Assistant skill](skills/search-as-code/SKILL.md)
- [Development and tests](../../docs/development.md)

```sh
go -C extensions/search-as-code test -race ./...
go -C extensions/search-as-code run . mcp --data /tmp/search-as-code-data   # the MCP server on stdio
```

All data and saved recipes stay local in the extension's data directory (`search_as_code_state.json`): no network requests and no secrets required (a restore is done by the `disklessly` CLI).
