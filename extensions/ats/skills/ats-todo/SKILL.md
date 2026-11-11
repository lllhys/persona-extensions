---
name: ats-todo
description: 用 ats 命令查待办背后的 Agent 任务推进到哪、卡在哪、有没有验收证据，或者替用户给待办派一个 ATS 任务。用户问「这个待办做到哪了」「真做完了吗」「为什么卡住」「ATS 里在跑什么」，或者说「把这个交给 ATS 做」时用。
---

# 用 ATS 看待办的进度和证据、给待办派任务

ATS 是 Persona 里的 Agent 任务队列：一个任务就是在用户选的电脑上、某个仓库的独立 git 工作目录里跑的一个 Agent 会话，做完后跑验收命令，结论写回待办。一个待办可以有多个任务。

命令只有一个：`ats METHOD PATH [JSON]`。读的直接执行；派任务（`POST /tasks`）每次都会先问用户。

| 做什么 | 命令 |
| --- | --- |
| 列任务 | `ats GET '/tasks?todo_id=<待办 id>'`，还可以加 `state=queued,running,needs_input,succeeded,failed,canceled`、`priority=p0..p3`、`limit=`（默认 20） |
| 看一个任务 | `ats GET /tasks/<任务 id>` |
| 看证据 | `ats GET /tasks/<任务 id>/evidence` |
| 派任务 | `ats POST /tasks '<JSON>'` |

1. 说进度时按状态归纳：`queued` 排队中（有 `not_before` 的是失败后在等重试）、`running` 在跑（`phase` 是 `preparing` 准备工作目录、`working` Agent 在做、`accepting` 在跑验收命令）、`needs_input` 等用户处理（`waiting` 里写了等什么，用户要去那个 Agent 会话里回答或批准）、`succeeded`、`failed`、`canceled`。列表里的 `scheduler` 是队列现在的档位（全部放行 / 只放行 P0、P1 / 额度用完暂停）和剩余额度。
2. 判断「做完没」只看证据：`/evidence` 里 `verified` 为 true（`succeeded` 且 `verdict` 是 `pass`）才算做完并通过验收。`succeeded` 但 `verdict` 是 `unverified` 的，只能说「跑完了、没有验收」。`last_report` 里有验收命令的退出码、输出末尾和留下的分支、提交。
3. 失败或卡住时，说清是第几次尝试、`failure_reason` 是什么、报告里的原话，再给一句下一步建议。不要自己重派；重试和取消在「ATS 队列」页由用户点。
4. 命令报错就直说原因，不要编进度：`too_many_queued`、`too_many_for_todo`（一个待办最多 5 个没结束的任务）、`repos_not_configured` 和 `repo_not_allowed`（让用户在扩展设置的「允许的仓库目录」里加上）、`not_found`。
5. 派任务只在用户明确要把某件事交给 ATS 时做，一次一个，不要连发，也不要在用户没开口时自己派。JSON 的字段：
   - `todo_id`（必填）：待办 id，任务的结论会写回这个待办；
   - `prompt`（必填）：交给 Agent 的完整说明，它看不到这次对话，要写清目标、范围和怎样算完成；
   - `repo`（必填）：仓库在那台电脑上的绝对路径或 `~/` 开头的路径，必须在用户允许的目录里；`ref` 可选，分支、标签或提交，默认 `HEAD`；
   - `acceptance`：一条验收用的 shell 命令（在任务的工作目录里跑，退出码 0 算通过）。尽量给，没有验收命令的任务事后只能说「跑完了」；
   - `permission_profile`：默认 `readonly`（Agent 只调查、不改东西）；要改代码才传 `default`，并在用户确认时说清这是可写任务。可写任务的改动提交在那台电脑上的 `ats/<任务>-<次数>` 分支，不会 push；
   - `priority`：`p0` 紧急、`p1` 高、`p2` 默认、`p3` 后台；`difficulty`：`easy`、`medium`（默认）、`hard`、`extreme`，决定用哪一档模型；
   - `machine`：只想在某台电脑上跑时填电脑的名字；`title`：队列页显示的标题；`idempotency_key`：同一个 key 重复提交只会有一个任务。

例子：

```
ats POST /tasks '{"todo_id":"todo_123","title":"修复登录页的空指针","prompt":"……","repo":"~/code/app","acceptance":"go test ./...","permission_profile":"default","priority":"p1","difficulty":"medium"}'
```
