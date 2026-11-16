---
name: sac-remote
description: 用 sac 命令跨电脑、跨项目检索用户的 Coding Agent 会话历史（Claude Code、Codex，含 disklessly 已上云的会话）：Persona 把检索分发到用户的每台电脑上执行，只回传排好序的证据片段，并标明来自哪台电脑；离线的电脑检索 hub 上已同步的纯对话索引。用户问「我在另一台电脑上是怎么修的」「之前在某个项目里讨论过什么」「所有电脑上最近的报错」时用。第一次读某台电脑、某个项目的会话会先弹审批卡，要用户批准。
---

# 跨电脑、跨项目检索会话历史（sac 命令）

命令只有一个：`sac METHOD PATH`。路径里的查询参数要做 URL 编码（空格写成 `%20`，中文直接编码），整个路径用单引号括起来。

| 做什么 | 命令 | 审批 |
| --- | --- | --- |
| 列要检索的电脑、它们的 key、在线与同步状态 | `sac GET /machines` | 直接执行 |
| 列一台电脑上的项目与 key | `sac GET /projects/<电脑 key>`（`all` 列全部电脑） | 直接执行 |
| 列搜索配方 | `sac GET /recipes` | 直接执行 |
| 检索会话 | `sac GET '/search/<电脑 key>/<项目 key>/<范围>?q=<查询>'` | 第一次读这台电脑、这个项目、这个范围时问用户 |
| 恢复已上云会话 | `sac POST /restore/<电脑 key>/<entry_id>` | 每次都问用户 |

## 检索

1. 先 `sac GET /machines` 拿到电脑的 `key`：`hub` 是 hub 所在的电脑，`all` 是全部电脑；其他电脑的 key 只含小写字母和 `-`（由名字转成，数字被编码，别自己拼，照抄返回的 key）。要按项目检索时，用 `sac GET /projects/<电脑 key>` 找到项目的 `key`；不限项目用 `all`。
2. 路径的三段就是用户要批准的目标：电脑、项目、范围（`conversation` 纯对话不含工具噪音，默认用它；`all` 含工具调用与输出；`user` 仅用户；`assistant` 仅助手；`errors` 报错与异常）。尽量窄：知道是哪台电脑、哪个项目，就不要用 `all`。范围用 `conversation` 能满足就不要用 `all`——工具输出里更容易有日志和凭据。
3. 查询参数：
   - `q`：自然语言或关键词，支持内联算子 `harness:codex`、`model:...`、`since:7d`、`regex:...`、`group:machine`；
   - `code`：一段 Search-as-Code 流水线（`scan_sessions(...) |> exclude_tools() |> correlate(by="file") |> group_by(field="machine") |> rank(...) |> limit(n=20)`）；
   - `recipe`：配方 id（见 `/recipes`），`since`、`harness`、`model`、`limit`（默认 25，最多 100）。
   路径上的电脑、项目和范围总是生效：`code` 或 `q` 里写的范围、项目不能扩大它。
4. 例子：

```
sac GET /machines
sac GET /projects/laptop
sac GET '/search/laptop/persona-extensions/conversation?q=quota%20cooldown&since=14d'
sac GET '/search/all/all/errors?q=timeout&limit=10'
```

## 审批

- 第一次读某个目标时，用户手机上会弹审批卡，写着要读哪几台电脑（离线的会注明查的是已同步的索引）、哪个项目、什么范围和查询。用户选「本次对话内总是允许」后，同一对话里再读同一台电脑、同一个项目、同一个范围不再询问；换电脑、换项目或换范围会再问。
- 发命令前先用一句话告诉用户要查哪台电脑、哪个项目、查什么，不要为了少弹卡而直接用 `all/all/all`。
- 用户拒绝了就停下，说明没有读到，不要换个路径再试。

## 汇报结果

- 每条命中都有 `machine_name`（来源电脑；空的是 hub 所在的电脑）、`project`、`cwd`、`session_id`、角色与行号。说结论时注明是哪台电脑上的哪个会话。`cached: true` 表示那台电脑离线，结果来自上次同步的纯对话索引（只有用户和助手消息，每条截短，可能不是最新的）。
- 结果里的 `machines` 说明每台电脑怎么回答的：`online` 实时检索、`cached` 查的同步索引、`unavailable` 离线且没有同步过；有电脑没查到时告诉用户。
- `location` 为 `已上云` 的命中可以恢复：先告诉用户会在哪台电脑上恢复到哪个原路径，用户同意后 `sac POST /restore/<电脑 key>/<entry_id>`（最长约 5 分钟），成功后把返回的 `resume` 命令告诉用户。`未确认上云` 的不要恢复。
- 命令报错就直说原因：`unknown_machine`（key 不对，重新 `GET /machines`）、`invalid_project`、`invalid_scope`、`invalid_program`。
