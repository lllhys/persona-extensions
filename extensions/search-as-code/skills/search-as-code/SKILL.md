---
name: search-as-code
description: 用户的代码化搜索（Search as Code）与本机 Coding Agent 会话深度检索（经本机 search-as-code MCP 服务器）：把复杂的多步检索、跨会话溯源、报错排查或模块决策回溯编译为可执行的搜索流水线代码（|> 算子链），跨 Claude Code / Codex 会话历史（支持排除工具调用噪音的纯对话检索）与本地代码库执行过滤、正则匹配、跨文件关联、聚合与排序。只读检索（mcp__search-as-code__sac_execute / sac_session_search / sac_recipes_list）直接运行；保存或删除搜索配方（sac_recipe_upsert / sac_recipe_delete）按对话权限先请用户确认。
---

# 代码化搜索（mcp__search-as-code__* 工具）

受 Perplexity *Rethinking Search as Code Generation* 与 CASS 会话检索启发，`search-as-code` 将复杂的检索任务表达为一段可组合、可审计的搜索流水线程序（Search-as-Code Pipeline），而不是零散的单次关键词搜索。

## 核心算子（Pipeline Primitives）

流水线步骤用 `|>` 串联：

1. `scan_sessions(query="", scope="conversation", project="", harness="", model="", since="")`：
   - `scope` 支持：
     - `conversation`（默认，**Without tools 降噪模式**）：仅检索 `User` 与 `Assistant` 对话正文，剥离冗长的 `tool_use` / `tool_result` JSON 噪音；
     - `all`：包含所有对话与工具输入输出；
     - `user`：只看用户的提问与指令；
     - `assistant`：只看助手的回答与总结；
     - `errors`：专门检索包含报错、异常退出、超时、配额限制或崩溃堆栈的会话。
2. `scan_files(pattern="", glob="")`：扫描配置的本地代码与文档仓库目录，返回带行号的代码证据。
3. `where(project="", harness="", model="", role="", since="", has_error="true|false")`：二次条件过滤。
4. `exclude_tools()`：显式剥离工具调用噪音，只保留纯对话或有效报错证据。
5. `match_regex(pattern="...")`：对证据片段、标题与项目执行不区分大小写的正则表达式过滤。
6. `correlate(by="file|project|branch")`：跨会话 / 跨源关联——自动找出修改或讨论过**同一个代码文件**、**同一项目**或**同一分支**的其他历史会话与代码文件。
7. `dedup(by="session|file|title")`：按会话、文件或标题去重。
8. `group_by(field="project|harness|model|role|error_kind")`：按指定维度聚合统计命中分布与异常数。
9. `rank(by="relevance_freshness|freshness|errors_first")`：综合相关性、时间新鲜度、关联度与异常权重打分排序。
10. `limit(n=25)`：截取 Top N 条高置信度结构化证据。

## 使用流程

1. **查看可用配方与会话源状态**：调用 `sac_recipes_list` 了解预置配方（`error-triage` 近期报错分诊、`cross-session-context` 跨会话决策溯源、`model-harness-audit` 模型与 Harness 产出审计）以及已索引的会话数量。
2. **执行复杂检索或流水线**：
   - 直接运行多步代码：`sac_execute {code: "scan_sessions(query=\"quota\", scope=\"conversation\") |> exclude_tools() |> correlate(by=\"file\") |> group_by(field=\"project\") |> rank(by=\"relevance_freshness\") |> limit(n=15)"}`
   - 或传入自然语言与内联算子：`sac_execute {query: "quota cooldown project:persona without:tools", scope: "conversation"}`
   - 或一键运行配方并附加关键词：`sac_execute {recipe: "error-triage", query: "timeout"}`
3. **快速 CASS 会话检索**：调用 `sac_session_search {query, scope, project?, harness?, model?, since?, exclude_tools?}`。
4. **沉淀常用搜索配方**：当某段 Search-as-Code 流水线值得长期复用时，先向用户展示流水线代码与名称，用户同意后调用 `sac_recipe_upsert` 保存；删除自定义配方前先和用户确认，再调用 `sac_recipe_delete`。

## 汇报结果时的原则

- 向用户汇报检索结论时，附上**执行的 Search-as-Code 流水线代码摘要**、命中证据的**角色归属（`User` / `Assistant` / `Error`）与行号**，以及通过 `correlate` 发现的关联会话或文件，让检索过程透明可验证。
