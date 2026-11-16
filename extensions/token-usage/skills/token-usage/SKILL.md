---
name: token-usage
description: 用户的 token 用量：每台电脑上各模型、各启动器每天用了多少 token，以及用户设的预算还剩多少。用户问「今天用了多少 token」「这个月哪个模型最费」「哪台电脑用量最高」「预算还剩多少」「帮我设一个每天的上限」时用。通过 token-usage MCP 工具读取，预算的改动用 budget_set 和 budget_delete。
---

# Token 用量（token-usage 工具）

用量按分钟记在每台电脑上，扩展自己留了 400 天。数字是 token 数，包含输入、输出和缓存读写。页面和总览卡片读的是同一份记录。

工具有五个。`usage_summary`、`usage_top`、`budget_status` 只读；`budget_set` 和 `budget_delete` 会改预算。

## 工作顺序

1. **先看总量**：`usage_summary`，`range` 取 `today`、`7d` 或 `30d`。它给出这段的总量、上一段的总量（用来算环比）和按模型、机器、启动器、天的分布。
2. **要排名时用 `usage_top`**：`dim` 取 `model`、`machine` 或 `launcher`，`n` 是要几项。
3. **预算用 `budget_status`**：列出每个预算已用多少、占上限的比例。设预算用 `budget_set`（`span` 是 `day` 或 `week`，`limit` 是 token 数，`model` 省略表示全部模型）。删预算用 `budget_delete`，删之前向用户确认。
4. **不编造数字**：用量只来自工具的返回。没有记录就说还没有，不要估。

## 工具

- `usage_summary`（只读）：`{"range": "7d"}`，可加 `group_by`（`model`、`machine`、`launcher` 或 `day`）只取一个维度。
- `usage_top`（只读）：`{"range": "30d", "dim": "model", "n": 5}`
- `budget_status`（只读）：`{}`
- `budget_set`：`{"span": "day", "limit": 1000000}`，可加 `model`。
- `budget_delete`：`{"span": "day"}`，可加 `model`。先确认。

## 说明的口径

- 环比是这段总量相对上一段的变化，由两个数相减得到，不要另算。
- 回答用户时用模型名和机器名；没有名字的启动器是终端里直接开的会话，说成「其他」。
- 预算提醒由扩展自己发：到上限的八成和全部各一次，同一天不重复。某个模型一个小时的用量超过过去 7 天同一小时均值的 5 倍时也会提醒一次。
- 示例里的数字只用来示范；用户的用量只来自他的记录。
