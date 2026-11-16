# ATS

An agent task queue for Persona. The assistant queues a task for a to-do (asking you first, every time); the queue picks the moment by priority, difficulty and remaining quota, runs the task as a Persona agent session in a git worktree of its own on a computer you chose, runs the task's acceptance command there, and writes the outcome with its evidence back to the to-do.

The extension is self-contained: one binary, no external scheduler, no account. It uses only the hub's public extension interfaces.

- [Manifest](persona-extension.json)
- [Skill](skills/ats-todo/SKILL.md)
- [Assistant gate](assistant/gate.json)
- [Installation](../../docs/installation.md)

## Set up

In the extension's settings:

1. **Computers** (`machines`): the computers tasks run on. The extension's program is installed on each. With none selected, tasks queue and do not run.
2. **Allowed repository directories** (`repos`): tasks run only in git repositories under these directories (absolute paths or `~/…`). Empty, no task can be queued.
3. Optional: the permission mode of tasks that may write (`write_permission_mode`, `acceptEdits` by default), tasks at a time per computer, timeouts, retry count, the quota reserve line and a model per tier.

## How a task runs

One binary has two roles. `ats hub` is the hub program: the queue (one JSON file in the extension's data directory, written by this program only), its dispatcher, the queue page and the HTTP API. `ats node` is the program on each selected computer: it makes and removes worktrees and runs acceptance commands.

| Step | What happens |
| --- | --- |
| queued | The scheduler admits tasks by priority (`p0`–`p3`), difficulty (model tier `fast`, `standard`, `strong`), waiting time, free slots on the online computers and quota. Below the reserve line only `p0` and `p1` go; with the quota used up nothing goes until it resets. |
| preparing | The computer's program resolves the ref to a commit and checks it out into a new worktree in its data directory (a detached checkout for a read-only task, a branch `ats/<task>-<attempt>` for one that may write). |
| working | The dispatcher starts a Persona agent session in the worktree, attached to the to-do, and watches it. A session that waits for you (a question, an approval) or ends its turn saying it is blocked shows as **needs you** at the top of the queue page, with what it asks for (the command it wants to run, its question, or what it said it is blocked on); answer in the session and the task goes on. |
| accepting | The computer's program runs the acceptance command in the worktree. Exit code 0 is a pass. A task with no acceptance command ends as "ran, not verified". |
| finished | Changes the agent of a writing task left uncommitted are committed by ATS on the task's branch `ats/<task>-<attempt>`, without running hooks or signing (nobody is there to answer them), and the worktree is removed (kept when that commit cannot be made); the branch, the commit and the session stay as evidence. The outcome is written to the to-do as a decision, once (`ref: ats:<task>:<state>[:<verdict>]`). |

A failed attempt is retried with a growing delay, up to `max_retries` times, as a new attempt with a new worktree, when another attempt can end differently: the acceptance command failed or the agent broke off (`task_failed`), a timeout (`timeout`), the computer or its environment (`infra_error`), a rate limit (`rate_limited`), a session that went away (`lost`). A failure that would repeat is not retried and the task ends at once: the model or the configuration is not available (`config_error`), the agent is signed out (`auth_expired`), the repository or the ref is wrong (`rejected`).

A retry does not start blind. Its prompt carries how the previous attempt ended (the reason and the end of the acceptance command's output), and a writing task's new branch starts from the previous attempt's commit, so the agent goes on from the work that is there. Every agent is asked to end its reply with a `Summary:` part; that part (or the beginning of the reply when there is none) is kept with the attempt and goes into the to-do decision.

Each attempt carries a fence token, so a late report of an old attempt never overwrites a newer one. The dispatcher keeps all of its state in the queue file, so a restart picks every attempt up where it was.

## The queue page

**ATS queue** in the main navigation. Tasks come first, in the order you act on them: **needs you** (each row says what the task waits for), **queued and running**, **recently finished**; the scheduler's numbers (admission mode, remaining quota, counts; the per-tier table only while a tier is held back) and the computers are below.

A row says the priority, whether the task may write, the repository and where it stands in plain words (which computer and attempt, when a failed attempt is retried, why a task failed). Tapping a row opens the task's page and never changes the task: its facts, what it is doing or waiting for, the acceptance command with its exit code and the end of its output, the agent's own summary, the branch and commit it left, every attempt, and the buttons **cancel** (asks first) and **retry**. Cancel and retry are also in a row's menu and under its swipe.

## What is written to the to-do

One decision per final outcome:

| Field | Content |
| --- | --- |
| question | `ATS 任务「<title>」的结果` |
| choice | Passed acceptance, ran with no acceptance command, failed with the reason in plain words and the number of attempts, canceled, or expired |
| reason | The agent's summary of what it did; the acceptance command and its exit code (only when it ran, otherwise what stopped the task); the branch and commit; the task's id |

The decision's `ref` makes the write idempotent: the same outcome is never written twice.

## Safeguards

- **The user confirms every task.** The assistant reaches the extension through a gated command; `POST /tasks` asks every time, reads pass, everything else is refused. The confirmation says that an agent will run unattended on your computer and shows the command; the skill has the assistant say first what it is about to queue (title, repository, read-only or writing, acceptance command). The credential stays out of the assistant's sight.
- **Read-only by default.** A task runs in the agent's plan mode unless it asks for `permission_profile: "default"`, which the confirmation shows. Writing tasks work on their own branch and never push.
- **Repository allowlist.** Checked when the task is queued, and again with real paths (symlinks resolved) by the program on the computer, whose answer counts.
- **Bounded.** At most `max_queued` queued tasks (`too_many_queued`), 5 unfinished tasks per to-do (`too_many_for_todo`), one task at a time per computer unless set otherwise, a timeout for the agent and for the acceptance command (killed with its process group).
- **A task always belongs to a to-do.** `todo_id` is required and the caller cannot set the `persona_todo` label itself.

## HTTP API

Served by the hub at `/api/v1/extensions/ats/…`.

| Request | Answer |
| --- | --- |
| `GET /tasks?todo_id=&state=&priority=&limit=` | Tasks, newest first, and the scheduler's state (admission mode, remaining quota, counts) |
| `GET /tasks/{id}` | The task with its prompt and attempts |
| `GET /tasks/{id}/evidence` | State, verdict, `verified`, the acceptance command, the last report (exit code, output tail, the agent's summary, artifacts) and the reference of the decision written to the to-do |
| `POST /tasks` | Queues a task: `todo_id`, `prompt`, `repo` (required); `ref`, `title`, `acceptance`, `permission_profile`, `priority`, `difficulty`, `machine`, `qos`, `pool`, `tags`, `labels`, `idempotency_key` |

## Permissions

| Permission | Used for |
| --- | --- |
| `todos.write` | Attaching a task's session to its to-do and writing the outcome decision |
| `agents.read`, `agents.control` | Starting, watching and interrupting the task's agent session |
| `machines.read` | Which of the selected computers are online |
| `extensions.call:ats` | The assistant's gated command calling this extension's API |
| `usage_gateways` | Reading the usage windows of the hub's gateway accounts for quota-aware admission |
| `node.permissions.files` | Worktrees and acceptance commands on the selected computers |

## Development

```sh
go -C extensions/ats test ./...
scripts/package.sh ats
```

Version 0.4.0 replaces the stdio MCP server and the external worker interface of 0.3.0, which could queue tasks but had nothing to run them. Version 0.5.0 stops retrying failures that would repeat, lets a retry go on from the previous attempt, adds the task page and rewords the queue page, the to-do decision and the confirmation.
