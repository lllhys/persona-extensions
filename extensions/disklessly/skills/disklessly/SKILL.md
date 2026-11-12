---
name: disklessly
description: 用户的 Persona 机器磁盘与 Coding Agent 冷会话管理（经本机 disklessly MCP 服务器）：自动接入本机与 ~/.persona/config.json、~/.ssh/config 中的 SSH 远程机器，查看多机根分区水位（80% 橙色预警、90% 红色告警）、扫描可回收缓存（Go、Node/Python、Docker、Rust/Xcode、系统与临时日志）与 Claude Code / Codex 会话冷热分布；生成 Dry-Run 安全清理计划，按机器或分类执行清理并记录 df 实测释放空间，或把 >7 天冷会话上云；搜索已上云的对话并恢复。只读用 mcp__disklessly__disklessly_machines_status / disklessly_scan / disklessly_history / disklessly_sessions_search / disklessly_sessions_status；清理、上云、恢复与机器节点变更按对话权限先请用户确认。
---

# Disklessly 机器磁盘与冷会话管理（mcp__disklessly__* 工具）

Disklessly 插件把本机和 SSH 远程机器的磁盘水位监控、可重建缓存清理与 Coding Agent 冷会话归档整合进 Persona 机器管理：

- **可回收分类（Categories）**：
  - `go-cache`：Go 编译构建缓存（`go-build`）与模块下载缓存（`go/pkg/mod`）
  - `node-python-cache`：`npm`、`pnpm`、`yarn`、`pip`、`uv` 包与轮子缓存
  - `docker-unused`：Docker 悬空镜像（dangling images）与未引用的 Buildx 构建缓存
  - `rust-xcode-build`：Cargo registry/git、Xcode DerivedData、SwiftPM、Clang 模块、Gradle、Playwright 浏览器缓存
  - `tmp-logs`：`systemd journal`（>7 天）、`/var/log` 已轮转归档日志、`/tmp` 陈旧构建与临时产物（>7 天）
  - `cold-agent-sessions`：超过 7 天未修改的 Claude Code 与 Codex 会话记录（由目标机器上的 disklessly CLI 上传到备份后端并写入索引后才移除本地副本；未安装 CLI 或后端不可达时不会归档也不会删除）
- **安全保护红线**：
  - 绝不触碰运行中的服务容器、活跃 git 仓库未提交改动、7 天内活跃的 Agent 热会话或任何凭据密钥（`~/.ssh`、`~/.gnupg`、`~/.persona` 等）。
  - 清理缓存时自动检测正在运行的工具链进程（`go`、`pnpm`、`cargo`、`swift` 等）及 `/proc/*/maps` 映射，占用中的子项自动跳过。

## 先读后写工作流

1. `disklessly_machines_status {refresh?}`：查看所有纳管机器的磁盘水位（`ok` / `warning` / `critical` / `offline`）、已用/可用空间、总可回收空间与最近清理记录。
2. `disklessly_scan {machine?, cached?}`：对指定机器（如 `local`、`armbox`、`devvm`、`mini-2`）执行只读扫描，获取根分区 `df` 水位、6 大分类明细、Agent 冷热会话统计以及 Dry-Run 清理计划。
3. `disklessly_history {machine?, limit?}`：查看各机器历史清理台账（每次清理前后 `df` 实测可用空间对比，如 `5.4 GiB → 18.0 GiB (+12.6 GiB)`）。
4. `disklessly_sessions_search {query, machine?, limit?, deep?}`：用户想找回以前的 Agent 对话时，按标题、首条消息、项目、工作目录、关键词搜索已上云的会话；本地索引找不到时再用 `deep: true` 在机器上全文搜索摘要。结果里的位置：`在云端`（最近 24 小时检查过、可恢复）、`云端缺失`、`本地`、`未知`（后端不可达或未检查，不代表丢失）。
5. `disklessly_sessions_status {machine?, refresh?}`：查看各机器备份后端是否可达、已索引会话数和各位置统计；需要最新状态时传 `refresh: true`。

只读工具直接运行；需要真正释放空间或修改机器列表时，先向用户展示扫描出的 Dry-Run 计划明细和预计释放体积，再用写入工具：

- `disklessly_clean {machine?, categories?, items?, include_review?, dry_run?, exclude_sessions?}`：
  - 默认只清理 `safe` 等级项；若需清理 `review` 等级项（如 Playwright 浏览器缓存），传 `include_review: true` 或在 `items` 中明确指定。默认清理不包含冷会话上云。
  - 冷会话会被上传并删除本地副本，只有显式传 `categories: ["cold-agent-sessions"]` 或冷会话单项时才处理；一次运行覆盖所有 Agent（Claude Code、Codex 等）。更推荐用 `disklessly_sessions_offload`；仍被引用、必须留在本地的会话放进 `exclude_sessions`（会话文件绝对路径或会话 ID）。备份后端不可达时工具会报错，不会上传或删除任何会话。
  - 返回清理前后 `df` 实测可用空间变化并记录到历史清理台账。
- `disklessly_sessions_offload {machine, dry_run?, exclude_sessions?}`：把该机器 7 天外的冷会话由 disklessly 上传到备份后端，读回校验后才删除本地副本。运行中进程（`--resume`、`codex resume`）、打开中的会话文件、herdr / Persona 面板引用的会话自动保留（扩展在运行开始前探测一次并写入排除列表；disklessly 在每次删除前还会重读排除列表并自行重新检查运行中进程和 herdr 的 session.json）；无法列出进程或后端不可达时中止，不删除任何会话。先用 `dry_run: true` 给用户看计划和被引用的会话。
- `disklessly_sessions_restore {machine, id}`：把一个已上云的会话恢复到原路径，返回继续对话的命令（`claude --resume <id>` 或 `codex resume <id>`；其他 Agent 没有该命令），告诉用户在对应机器上运行。来自另一台设备的会话不会在本机恢复，请在原设备上恢复。
- `disklessly_machine_upsert {host, id?, name?, user?, port?, identity_file?}`：新增或修改一台 SSH 远程机器节点。
- `disklessly_machine_delete {machine}`：移除一台远程机器节点（本机 `local` 不可移除）。
