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
| working | The dispatcher starts a Persona agent session in the worktree, attached to the to-do, and watches it. A session that waits for you (a question, an approval) or ends its turn saying it is blocked shows as **needs you** on the queue page; answer in the session and the task goes on. |
| accepting | The computer's program runs the acceptance command in the worktree. Exit code 0 is a pass. A task with no acceptance command ends as "ran, not verified". |
| finished | The worktree is removed (kept when it holds uncommitted work); the branch, the commit and the session stay as evidence. The outcome is written to the to-do as a decision, once (`ref: ats:<task>:<state>[:<verdict>]`). |

A failed attempt (the acceptance command failed, a timeout, the computer went away, a rate limit) is retried with a growing delay, up to `max_retries` times, as a new attempt with a new worktree. Each attempt carries a fence token, so a late report of an old attempt never overwrites a newer one. The dispatcher keeps all of its state in the queue file, so a restart picks every attempt up where it was.

## Safeguards

- **The user confirms every task.** The assistant reaches the extension through a gated command; `POST /tasks` asks every time, reads pass, everything else is refused. The credential stays out of the assistant's sight.
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
| `GET /tasks/{id}/evidence` | State, verdict, `verified`, the acceptance command, the last report (exit code, output tail, artifacts) and the reference of the decision written to the to-do |
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

Version 0.4.0 replaces the stdio MCP server and the external worker interface of 0.3.0, which could queue tasks but had nothing to run them.
