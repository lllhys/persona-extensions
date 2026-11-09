# Search as Code

Executable Search-as-Code pipelines over local coding-agent session transcripts (`~/.claude/projects`, `~/.codex/sessions`, and configured directories) and local repositories, inspired by Perplexity's *Rethinking Search as Code Generation* and CASS session search.

Instead of a single flat keyword lookup, complex investigative queries compile into a multi-step pipeline (`scan_sessions` / `scan_files` `|>` `exclude_tools` `|>` `match_regex` `|>` `correlate` `|>` `group_by` `|>` `rank` `|>` `limit`) with full step-by-step execution traces (`StepTraces`), role-attributed evidence snippets (`User`, `Assistant`, `Error`, `File`), cross-session file/project correlation, and reusable search recipes. A local MCP server (`search-as-code mcp`) exposes the same engine to the assistant on stdio.

- [Guide](../../docs/extensions/search-as-code.md)
- [Manifest](persona-extension.json)
- [Assistant skill](skills/search-as-code/SKILL.md)
- [Development and tests](../../docs/development.md)

```sh
go -C extensions/search-as-code test -race ./...
go -C extensions/search-as-code run . mcp --data /tmp/search-as-code-data   # the MCP server on stdio
```

All data and saved recipes stay local in the extension's data directory (`search_as_code_state.json`): no network requests and no secrets required.
