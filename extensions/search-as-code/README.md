# Search as Code

Executable Search-as-Code pipelines over local coding-agent session transcripts (`~/.claude/projects`, `~/.codex/sessions`, and configured directories) and local repositories, inspired by Perplexity's *Rethinking Search as Code Generation* and CASS session search.

Instead of a single flat keyword lookup, complex investigative queries compile into a multi-step pipeline (`scan_sessions` / `scan_files` `|>` `exclude_tools` `|>` `match_regex` `|>` `correlate` `|>` `group_by` `|>` `rank` `|>` `limit`) with full step-by-step execution traces (`StepTraces`), role-attributed evidence snippets (`User`, `Assistant`, `Error`, `File`), cross-session file/project correlation, and reusable search recipes. A local MCP server (`search-as-code mcp`) exposes the same engine to the assistant on stdio.

Sessions that the disklessly CLI moved to the cloud stay searchable through the plain-text index it keeps on this computer (`<stateDir>/sessions/text/<entryId>.jsonl`): such hits are marked `已上云` and can be restored to their original path with one click (or `sac_session_restore`), which runs the local `disklessly` CLI. The restore runs in the background and its outcome appears in a `会话恢复` section on the page. An index whose upload never completed (empty `segment`/`archivedAt`) is marked `未确认上云` and is not offered for restore.

Optionally, it searches the other computers picked in its settings too: the hub fans each search out over Persona's node connection, each computer runs the pipeline over its own transcripts and returns only the ranked evidence (labelled with the computer), and an offline computer is searched in a compact conversational index synced to the hub. The assistant reads other computers and projects through the gated `sac` command: the first read of a computer, project and scope shows an approval card, remembered per that target for the conversation.

- [Guide](../../docs/extensions/search-as-code.md)
- [Manifest](persona-extension.json)
- [Assistant skills](skills/search-as-code/SKILL.md), [cross-computer](skills/sac-remote/SKILL.md)
- [Development and tests](../../docs/development.md)

```sh
go -C extensions/search-as-code test -race ./...
go -C extensions/search-as-code run . mcp --data /tmp/search-as-code-data   # the MCP server on stdio
```

All data, saved recipes and the synced indexes of other computers stay in the extension's data directory on the hub (`search_as_code_state.json`, `machines/`): no outside service and no secrets (a restore is done by the `disklessly` CLI of the computer that holds the session).
