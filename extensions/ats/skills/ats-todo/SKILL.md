---
name: ats-todo
description: 用 ATS 的工具查待办背后的 Agent 任务推进到哪、卡在哪、有没有验收证据，或者替用户给待办派一个带优先级与难度标签的 ATS 任务。用户问「这个待办做到哪了」「真做完了吗」「为什么卡住」「ATS 里在跑什么」，或者说「把这个交给 ATS 做」时用。
---

# 用 ATS 看待办的进度和证据与综合派活

ATS 是自包含运行在本地的 Agent 任务调度器。一个待办可以对应多个 ATS 任务，对应关系是任务上的标签 `persona_todo=<待办 id>`。ATS 支持通过标签区分任务**优先级（`priority=p0|p1|p2|p3`）**与**难度（`difficulty=easy|medium|hard|extreme`，对应模型档位 `fast|standard|strong`）**，并结合可用机器槽位与通用 Quota Provider 实时额度水位自动进行离线计算队列式综合调度（全量放行 / 仅 P0-P1 保底 / 额度冷却暂停）。

1. 先用 `list_tasks` 拿任务：有待办 id 时传 `todo_id`；也可按 `status` / `states`、`priority`（`p0`–`p3`）、`difficulty`（`easy`/`medium`/`hard`/`extreme`）、`machine` 过滤，同时查看返回结果里的 `scheduler`（当前准入档位、剩余额度百分比与各优先级排队数）。
2. 说进度时按状态与调度档位归纳：排队中（含因退避冷却 `notBefore` 或额度保底暂缓的任务）、运行中（含机器、模型档位与 R2 fence 代次）、成功、失败、已取消。
3. 判断「做完没」只看证据：用 `get_evidence` 看 `state`、`verdict`、验收命令和最后一份报告（退出码、产物、以及写回待办的幂等 `decisionRef`）。`succeeded` 但 `verdict` 不是 `pass` 的，只能说「跑完了、没验收」，不能说做完了。
4. 失败或卡住时，用 `get_task` 和 `get_evidence` 说清是哪次尝试（`attemptNo` / `fenceToken`）、什么错误类别、报告里的原话，再给一句下一步建议；不要自己重试或取消。
5. 工具报错（如队列达到 `max_queued` 准入上限返回 `too_many_queued` 429，或任务不存在）就直说，不要编进度。
6. 派活用 `enqueue_task`，只在用户明确要把某件事交给 ATS 时用：
   - 必须带待办 id（`todo_id`），工具会自动打上 `persona_todo=<待办 id>` 标签（调用方不能自行覆盖该标签）；
   - `repo` 用仓库的绝对路径；
   - 根据任务紧急程度与复杂度设置 `priority`（`p0` 紧急、`p1` 高优、`p2` 默认、`p3` 后台批处理）与 `difficulty`（`easy` 轻量、`medium` 标准、`hard` 复杂、`extreme` 极高复杂度），需要特定机器时传 `machine`；
   - 尽量给一条 `acceptance` 验收命令，没有验收命令的任务事后只能说「跑完了」；
   - 默认只读权限（`permission_profile: "readonly"`）；要改代码、写文件才传 `permission_profile: "default"`，并在请求确认时跟用户说清。每次调用都会让用户确认，不要批量连发，也不要在用户没开口时自己派活。
