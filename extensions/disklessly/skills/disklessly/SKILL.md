---
name: disklessly
description: 用户的 Persona 机器磁盘与 Coding Agent 冷会话管理（经本机 disklessly MCP 服务器）：自动接入本机与 ~/.persona/config.json、~/.ssh/config 中的 SSH 远程机器，查看多机根分区水位（80% 橙色预警、90% 红色告警）、扫描可回收缓存（Go、Node/Python、Docker、Rust/Xcode、系统与临时日志）与 Claude Code / Codex 会话冷热分布；生成 Dry-Run 安全清理计划，按机器或分类执行清理并记录 df 实测释放空间，或一键归档 >7 天冷会话。只读用 mcp__disklessly__disklessly_machines_status / disklessly_scan / disklessly_history；清理与机器节点变更按对话权限先请用户确认。
---

# Disklessly 机器磁盘与冷会话管理（mcp__disklessly__* 工具）

Disklessly 插件把本机和 SSH 远程机器的磁盘水位监控、可重建缓存清理与 Coding Agent 冷会话归档整合进 Persona 机器管理：

- **可回收分类（Categories）**：
  - `go-cache`：Go 编译构建缓存（`go-build`）与模块下载缓存（`go/pkg/mod`）
  - `node-python-cache`：`npm`、`pnpm`、`yarn`、`pip`、`uv` 包与轮子缓存
  - `docker-unused`：Docker 悬空镜像（dangling images）与未引用的 Buildx 构建缓存
  - `rust-xcode-build`：Cargo registry/git、Xcode DerivedData、SwiftPM、Clang 模块、Gradle、Playwright 浏览器缓存
  - `tmp-logs`：`systemd journal`（>7 天）、`/var/log` 已轮转归档日志、`/tmp` 陈旧构建与临时产物（>7 天）
  - `cold-agent-sessions`：超过 7 天未修改的 Claude Code 与 Codex 会话记录（归档压缩至 `~/.disklessly/session-archives`）
- **安全保护红线**：
  - 绝不触碰运行中的服务容器、活跃 git 仓库未提交改动、7 天内活跃的 Agent 热会话或任何凭据密钥（`~/.ssh`、`~/.gnupg`、`~/.persona` 等）。
  - 清理缓存时自动检测正在运行的工具链进程（`go`、`pnpm`、`cargo`、`swift` 等）及 `/proc/*/maps` 映射，占用中的子项自动跳过。

## 先读后写工作流

1. `disklessly_machines_status {refresh?}`：查看所有纳管机器的磁盘水位（`ok` / `warning` / `critical` / `offline`）、已用/可用空间、总可回收空间与最近清理记录。
2. `disklessly_scan {machine?, cached?}`：对指定机器（如 `local`、`opc`、`gcp`、`mm-2`）执行只读扫描，获取根分区 `df` 水位、6 大分类明细、Agent 冷热会话统计以及 Dry-Run 清理计划。
3. `disklessly_history {machine?, limit?}`：查看各机器历史清理台账（每次清理前后 `df` 实测可用空间对比，如 `5.4 GiB → 18.0 GiB (+12.6 GiB)`）。

只读工具直接运行；需要真正释放空间或修改机器列表时，先向用户展示扫描出的 Dry-Run 计划明细和预计释放体积，再用写入工具：

- `disklessly_clean {machine?, categories?, items?, include_review?, dry_run?}`：
  - 默认只清理 `safe` 等级项；若需清理 `review` 等级项（如 Playwright 浏览器缓存），传 `include_review: true` 或在 `items` 中明确指定。
  - 若只想归档压缩 >7 天的 Claude Code / Codex 冷会话，传 `categories: ["cold-agent-sessions"]`。
  - 返回清理前后 `df` 实测可用空间变化并记录到历史清理台账。
- `disklessly_machine_upsert {host, id?, name?, user?, port?, identity_file?}`：新增或修改一台 SSH 远程机器节点。
- `disklessly_machine_delete {machine}`：移除一台远程机器节点（本机 `local` 不可移除）。
