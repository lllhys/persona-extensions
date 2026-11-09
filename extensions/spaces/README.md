# Spaces

Spaces whose structure you define. Each space declares its types (typed fields, optional statuses and a view: list, board, progress, table or feed); entities and the relations between them live in one store, and the page is drawn from those definitions. A local MCP server (`spaces mcp`) lets the assistant define types and read and write entities in a conversation.

- [Guide](../../docs/extensions/spaces.md)
- [Manifest](persona-extension.json)
- [Assistant skill](skills/spaces/SKILL.md)
- [Development and tests](../../docs/development.md)

```sh
go -C extensions/spaces test ./...
go -C extensions/spaces run . mcp --data /tmp/spaces-data   # the MCP server on stdio
```

All data is local, in the extension's data directory (`spaces_state.json`). It starts empty: no network, no secrets. A new space can start blank or from a starter template (项目与目标, 阅读与知识, 清单与物品) that only fills in its types.
