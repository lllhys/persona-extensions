# Search as Code

[Documentation](../README.md) · [Source](../../extensions/search-as-code/)

Search as Code brings Perplexity's *Rethinking Search as Code Generation* paradigm and CASS-style coding-agent session search into Persona. Instead of issuing isolated single-turn keyword lookups, a multi-step investigation is compiled into and executed as a deterministic search program (`SearchProgram`) over local coding-agent transcripts (`~/.claude/projects/**/*.jsonl`, `~/.codex/sessions/**/*.jsonl`, and configured directories) and local code repositories.

Every run produces:

1. **The executable Search-as-Code program (`CodeListing`)**: a composable pipeline of source, filtering, correlation, aggregation, and ranking operators connected by `|>`.
2. **A transparent step execution trace (`StepTraces`)**: the input count, output count, execution time, and summary note for each operator in the pipeline.
3. **Role-attributed evidence cards (`Hits` and `Groups`)**: ranked sessions and files with `User`, `Assistant`, `Error`, and `File` line-numbered snippets, `Without tools` conversational noise reduction, and cross-session file/project correlations.

All indexing and execution happen locally on the machine; the extension makes no network calls and requires no secrets.

## Pipeline operators

| Operator | Arguments | Behavior |
| --- | --- | --- |
| `scan_sessions` | `query`, `scope` (`conversation`, `all`, `user`, `assistant`, `errors`), `project`, `harness`, `model`, `since` | Scans recent Claude Code, Codex, and custom JSONL transcripts, extracting turn-level snippets, error classifications, and touched file paths. |
| `scan_files` | `pattern`, `glob` | Scans configured local repository directories (`repo_dirs`) for matching lines and file paths. |
| `where` | `project`, `harness`, `model`, `role`, `since`, `has_error` | Filters the current candidate set by metadata predicates or snippet role. |
| `exclude_tools` | none | CASS-style `Without tools` filter: strips noisy intermediate `tool_use` and `tool_result` JSON dumps, keeping conversational (`User` / `Assistant`) and `Error` turns. |
| `match_regex` | `pattern` | Filters hits and snippets against a case-insensitive regular expression. |
| `correlate` | `by` (`file`, `project`, `branch`) | Cross-session and cross-source join: links each hit to other sessions or repository files that touched the same file, project, or branch. |
| `dedup` | `by` (`session`, `file`, `title`) | Deduplicates the candidate set by session ID, primary file path, or normalized title. |
| `group_by` | `field` (`project`, `harness`, `model`, `role`, `error_kind`) | Aggregates hits into summary buckets with total counts, error counts, and sample titles. |
| `rank` | `by` (`relevance_freshness`, `freshness`, `errors_first`) | Scores and sorts hits by term match density, conversational weight, correlation bonus, error count, and recency decay. |
| `limit` | `n` (1 to 100, default 25) | Truncates the ranked hit list to the top `n` results. |

### Natural-language to Search-as-Code compilation

When given a natural-language query (with optional inline tokens such as `project:persona`, `harness:claude-code`, `model:claude-opus-5-5`, `since:7d`, `regex:panic|timeout`, `file:*.go`, `group:model`, `correlate:file`, `without:tools`), the compiler automatically synthesizes a multi-step pipeline:

```text
scan_sessions(query="quota cooldown", scope="conversation", project="persona")
|> exclude_tools()
|> correlate(by="file")
|> group_by(field="project")
|> rank(by="relevance_freshness")
|> limit(n=25)
```

## Built-in recipes

| ID | Name | Default scope | Pipeline |
| --- | --- | --- | --- |
| `error-triage` | 近期报错会话分诊 | `errors` | `scan_sessions(scope="errors") \|> exclude_tools() \|> correlate(by="file") \|> group_by(field="project") \|> rank(by="errors_first") \|> limit(n=25)` |
| `cross-session-context` | 跨会话决策与上下文溯源 | `conversation` | `scan_sessions(scope="conversation") \|> exclude_tools() \|> correlate(by="project") \|> dedup(by="session") \|> group_by(field="project") \|> rank(by="relevance_freshness") \|> limit(n=25)` |
| `model-harness-audit` | 模型与 Harness 产出审计 | `all` | `scan_sessions(scope="all") \|> correlate(by="file") \|> group_by(field="model") \|> rank(by="freshness") \|> limit(n=30)` |

Custom recipes can be created, edited, and deleted from the page or through the MCP server.

## The page

| Contribution | Behavior |
| --- | --- |
| `ui` `search-as-code` (`nav_page`) | Search bar, scope filter (`conversation`, `all`, `user`, `assistant`, `errors`), tabs (`证据与结果`, `代码与轨迹`, `搜索配方`, `会话与数据源`), hit detail view, and recipe editor. |
| `ui` `search-as-code-card` (`overview_card`) | Quick overview of saved Search-as-Code recipes and indexed session statistics. |
| `skills` `search-as-code` | Guides the assistant on composing `\|>` pipelines, using `conversation` scope to avoid tool noise, and saving reusable recipes. |
| `mcp_servers` `search-as-code` | `search-as-code mcp` over stdio. Read tools (`sac_execute`, `sac_session_search`, `sac_recipes_list`) are in `allow`; write tools (`sac_recipe_upsert`, `sac_recipe_delete`) follow the conversation's permission policy. |

## The MCP server

`search-as-code mcp` speaks MCP over stdio (JSON-RPC 2.0, one message per line: `initialize`, `ping`, `tools/list`, `tools/call`). `search-as-code --help` names `mcp`, which the manifest's `requires` checks. Set `PERSONA_SEARCH_AS_CODE_BIN` to the binary path when `search-as-code` is not on `PATH` or in `~/bin`.

| Tool | Access | What it does |
| --- | --- | --- |
| `sac_execute` | read, allowed | Compiles and executes explicit `code`, a named `recipe`, or a natural-language `query` with optional `scope`, `project`, `harness`, `model`, `since`, and `limit`. Returns `program` (`code_listing`), `step_traces`, `groups`, and `hits`. |
| `sac_session_search` | read, allowed | CASS-style multi-scope session search (`query`, `scope`, `project`, `harness`, `model`, `since`, `exclude_tools`, `limit`). |
| `sac_recipes_list` | read, allowed | Lists built-in and custom Search-as-Code recipes, available pipeline operators, scopes, and discovered session source statistics. |
| `sac_recipe_upsert` | write, asks | Creates or updates a reusable Search-as-Code recipe after validating its pipeline syntax. |
| `sac_recipe_delete` | write, asks | Deletes a custom recipe by `id`. |

## Storage

The extension and `search-as-code mcp` share `search_as_code_state.json` synchronized via an exclusive file lock on `.search_as_code.lock` and atomic rename. `search-as-code mcp` resolves its data directory from `--data`, else `PERSONA_SEARCH_AS_CODE_DATA`, else `<user config dir>/persona-search-as-code/data-dir` written by the hub extension, else `<user config dir>/persona-search-as-code/data`.

## Tests

```sh
go -C extensions/search-as-code test -race ./...
```

The test suite verifies protocol conformance and manifest schema validation, Claude Code and Codex JSONL session parsing, `Without tools` (`conversation`) noise filtering vs `all` / `user` / `assistant` / `errors` scopes, natural-language to Search-as-Code compilation, multi-step pipeline execution (`where`, `exclude_tools`, `match_regex`, `correlate`, `dedup`, `group_by`, `rank`, `limit`), local repository file scanning, UI rendering and actions across all tabs and detail views (`wire.CheckView`), and all five MCP tools over stdio.
