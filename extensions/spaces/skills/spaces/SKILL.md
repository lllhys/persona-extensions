---
name: spaces
description: 用户的 Persona 空间（经本机 spaces MCP 服务器）：每个空间由用户自己定义的类型（字段、状态、呈现方式）、记录和记录之间的关系组成，App 里的页面直接按这些结构生成。用户提到空间、想系统地记录和管理某类事情（例如读书、装修、旅行、收藏、习惯）、问某个空间里有什么，或者让你帮忙设计字段和类型时用。读用 mcp__spaces__space_list / space_schema_get / space_query；写入的工具会按对话的权限先请用户确认。
---

# 空间（mcp__spaces__* 工具）

一个空间是一块独立的数据：

- **类型**（TypeDef）：`key`、`label`、`fields`、可选的 `statuses`（依次流转，新记录从第一个开始），以及 `view` —— 页面怎样呈现这种类型：
  - `list` 列表；`board` 按状态分列的看板（需要 statuses）；`progress` 进度条（第一个 number 字段是当前值，第二个是目标值，没有就按 100）；`table` 表格；`feed` 按时间倒序的动态流。
- **字段**（FieldDef）：`key`（snake_case）、`label`、`kind`（`text`、`number`、`boolean`、`date`、`enum`、`url`）、enum 的 `options`、`required`。
- **记录**：`title`、`status`、`values`（字段 key → 值）。
- **关系**：从一条记录指向同一空间里另一条记录的有向边，带一个名字（例如「属于」「依赖」「参考」）。

## 先读，再写

1. `space_list`：有哪些空间、每个空间有哪些类型和多少记录。不确定用户指的是哪个空间时先看这个。
2. `space_schema_get {space_id}`：字段的 kind、选项、必填和状态。写入记录前一定先看。
3. `space_query {space_id, type?, id?, query?, status?, where?, limit?}`：查记录和它们的关系（`out` 是它指向的，`in` 是指向它的）。`where` 按字段精确匹配，要同时给 `type`。

只读工具直接运行；下面的写入工具会按对话的权限策略先问用户。

## 帮用户搭一个空间

用户想开始管理一件新的事情时，先看 `space_list` 返回的 `templates`（常见模板）有没有贴合的：

- `project` 项目与目标：点子（看板）、目标（进度）、资料库（列表）、动态（动态流）。
- `reading` 阅读与知识：书单（进度）、笔记（列表）、摘录与链接（表格）。
- `inventory` 清单与物品：物品（表格）、心愿单（看板）。
- `blank` 空白空间：不预置类型。

贴合时把模板的类型简单说给用户听，同意后 `space_upsert {template, name?, examples?}` 一步建好（名称、图标、说明不填就用模板的；`examples: true` 才会写入几条示例记录，默认不写），之后照用户的说法用 `space_schema_upsert` 增删字段。

没有贴合的模板时，不要硬套。先问清楚用户想记什么、关心哪些属性、需要怎样看（清单、进度、看板……），提出一个精简的方案（每种类型三到六个字段就够），用户同意后再写：

1. `space_upsert {name, icon?, description?}` 建空间（可以给一个小写的 `id`）。
2. `space_schema_upsert {space_id, key, label, view, statuses?, fields}` 定义每种类型。这是整体替换：`fields` 要列全，没列出的字段连同它的值会被删掉；已有记录不符合新定义时整个修改会被拒绝，错误里会说明是哪一条。
3. 用户给了现成的数据时，再用 `space_entity_upsert` 写进去。

## 写记录

- `space_entity_upsert`：不给 `id` 是新建（要 `type` 和 `title`），给 `id` 是修改（`values` 合并进去，值为 `null` 删掉一个）。`relations: [{type, to}]` 是它指向的全部关系，给了就整体替换，不给保持不变。
- `space_entity_derive {id, type, title?, relation?}`：从一条记录衍生出同一空间里另一种类型的新记录（例如把点子孵化成目标、把书衍生成一条笔记），带上两种类型共有的字段值，并以「源自」（或给定的 relation）指回原记录。
- `space_entity_delete {id}`：删除记录和它所有的关系。删除前先和用户确认是哪一条。

## 注意

- 值按字段的 kind 严格校验：text 字段的 `"007"` 就是文本 `"007"`；number 可以给数字或数字字符串；date 是 `YYYY-MM-DD`；enum 必须是选项之一；url 是 http(s) 链接。被拒绝时照错误信息改，不要换个字段名硬塞。
- 只有一个空间时 `space_id` 可以不填；有多个时必须指明。
- 转述时用用户能看懂的名字（空间名、类型名、字段名、状态），不要只报 id。
