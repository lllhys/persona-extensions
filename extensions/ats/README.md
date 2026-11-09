# ATS

Self-contained Go native Agent Task Scheduler (ATS) extension for Persona. A single binary runs both the Persona extension runtime (`ats hub`, providing the **ATS 队列** navigation page, generic Quota Provider sync and idempotent `todos.write` outcome decision write-back) and the stdio MCP server (`ats mcp --enqueue`, giving the Claude assistant read-only `list_tasks`, `get_task` and `get_evidence` tools plus the user-confirmed `enqueue_task` tool).

- [Manifest](persona-extension.json)
- [Skill](skills/ats-todo/SKILL.md)
- [Installation](../../docs/installation.md)

## Architecture and built-in safeguards

- **Self-contained persistent queue**: tasks transition through `queued` → `running` → `succeeded` / `failed` / `canceled`, recording verification `evidence` and output summaries in the extension's data directory without requiring an external scheduler daemon.
- **R1 (Read-only default security boundary)**: every task defaults to `permission_profile: "readonly"`. Write permissions are granted only when `permission_profile: "default"` is explicitly requested.
- **R2 (Worker lease and fenced write protection)**: each execution attempt receives a unique `attempt_id`, `worker_id` and monotonically increasing `fence_token` (`attempt_no`). Heartbeats and outcome reports validate both `worker_id` and `fence_token`, rejecting stale workers with `403` so an expired attempt never overwrites newer state.
- **R6 (Deterministic exponential backoff and jitter)**: retryable failures automatically re-enter the queue with a deterministic `not_before` cooldown computed from `sha256(task_id, attempt_no)` and soft worker anti-affinity.
- **R8 (Queue admission cap)**: the queue enforces `max_queued` (default `100`) and rejects excess submissions with `too_many_queued` (`429`).

## Tag-driven priority, difficulty and offline-compute scheduler

Tasks carry scheduling metadata through explicit parameters or tags/labels:

- **Priority**: `priority=p0|p1|p2|p3` (or `high`/`normal`/`low`, default `p2`).
- **Difficulty and model tier**: `difficulty=easy|medium|hard|extreme` (or `tier=fast|standard|strong`, default `medium`), mapping to model tiers (`easy` → `fast`, `medium` → `standard`, `hard`/`extreme` → `strong`) and quota cost weights (`1.0x` to `8.0x`).
- **Machine affinity**: optional `machine=<host>` constraint.

The multi-factor scheduler combines task priority, difficulty-to-tier mapping, online machine slot capacity (`running_slots / max_slots`), anti-starvation aging (`+0.5` score/min up to `+60`) and generic Quota Provider headroom into three admission modes:

1. **Full admission (`full` / 全量放行)**: when quota headroom is at or above `reserve_headroom_pct` (default `30%`), `p0`–`p3` and `hard`/`extreme` batch tasks are scheduled across the least-loaded online machines.
2. **Priority reserve (`priority_only` / 仅 P0-P1 保底)**: when quota drops below `reserve_headroom_pct`, `p2`/`p3` and high-consumption offline tasks are held so remaining headroom is reserved for `p0`/`p1` tasks (or routed to lighter tiers with ample quota).
3. **Quota cooldown (`paused_quota` / 额度冷却暂停)**: when the generic Quota Provider reports `exhausted` or an active `cooldown_until` / `reset_at` window, queue dispatch pauses until quota resets.

## MCP tools and security gate

| Tool | Access |
| --- | --- |
| `list_tasks` | Runs without asking (`allow`). Filters: `todo_id`, `status` / `states`, `priority`, `difficulty`, `machine`, `pool`, `source`, `limit`; also returns current scheduler mode and quota headroom |
| `get_task` | Runs without asking (`allow`). Returns task summary, priority/difficulty/tier tags, prompt preview, acceptance command and fenced attempts |
| `get_evidence` | Runs without asking (`allow`). Returns final state, verdict, failure reason, acceptance command, idempotent `decisionRef` and verification reports |
| `enqueue_task` | Asks the user every time (never listed in `allow`). Requires `todo_id`, `prompt` and `repo` (absolute path); forces `persona_todo=<todo_id>` (caller cannot override it), sets `source="assistant"`, `external_ref={system:"assistant", id:"persona", conversation:<todo_id>}`, and defaults `permission_profile` to `"readonly"` |

## Outcome decision write-back (`todos.write`)

When a task labelled `persona_todo=<todo_id>` reaches a terminal state, the extension posts an idempotent outcome decision to `POST /api/v1/todos/{todo_id}/decisions` using the `todos.write` scope with `ref: "ats:<task_id>:<state>[:<verdict>]"`. Replays with the same `ref` are deduplicated by the hub (`inserted: false`), and `404`/`403` responses are recorded and skipped without retry loops.
