# enkeep memory (enkeep-memory)

[Documentation](../README.md) · [Source](../../extensions/enkeep-memory/)

The hub's own pipeline summarizes chats as they arrive. This extension is a second pass on top of it, run on a schedule:

- once a day for the day before;
- once a week, on Monday, for the ISO week before.

It keeps its own state in its data directory and touches the knowledge base only through the knowledge-base RPC (`kb.read`, `kb.write`), and calls models through the hub (`models`). It needs a hub with attributed extension writes (nwverse/persona#6110) and model calls for extensions (nwverse/persona#6102). Every write it makes is either a proposal you accepted or a kind of proposal you allowed it to apply by itself.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `ui` | `enkeep-memory` (`nav_page`) | Tabs: proposals to review, facts, practices, knowledge, the graph, and run reports. Each item opens with its citations. Proposals can be accepted, edited, rejected or postponed. Accepted proposals can be undone. |
| `ui` | `enkeep-memory-today` (`overview_card`) | Pending proposals and active items at a glance. |
| `ui` | `enkeep-memory-health` (`extension_page`) | Who "I" am (the names used to recognize you), the latest triage funnel, and buttons to run a pass now. |
| `events` | `enkeep-memory-kb`: `kb.changed`, `kb.forgotten` | A forgotten source's derived data (items, digests, raw answers, graph evidence) is deleted at once; the weekly pass also reconciles against entities that no longer exist. |
| `skills` | `enkeep-memory` | Tells the assistant what the curator does and how to cite the facts it recorded. |

## Daily pass

1. **One triage for every source.** One `kb.recent` call lists what came in from every source. Lark chats, mail, WeChat, GitHub, documents, meetings, events and recordings all go through the same rules. The source and kind of a chat are features of the rules, not separate pipelines. The rules are evaluated in this order:
   - Your pins and blocks.
   - Direct messages are read.
   - GitHub reviews and issues that involve you are read.
   - Mail: threads you took part in are read; bulk and no-reply senders are dropped; other mail is classified by the model, and marketing and newsletters are dropped.
   - Groups:
     - If you wrote in the group within `activity_days` (default 3), the whole day is read.
     - Otherwise, if someone named you, only `mention_window` lines either side of each mention are read.
     - Otherwise, "@everyone"-only days are kept for the week.
     - Otherwise, groups the hub ranks as relevant go to the model.
     - Every other quiet group is skipped.
   - Lines containing a `private_keywords` entry are never read or sent anywhere.
   - On a small day (`admit_all_below_tokens`), everything except blocks and bulk mail is read.
2. **A digest with citations.** Each admitted unit is sent to the model once, in a single turn, and the answer must be JSON. The answer contains:
   - a summary;
   - facts: dated, checkable statements;
   - practices: how things are done;
   - knowledge: things that stay true;
   - if the graph is on, typed relations between entities.
3. **Deterministic checks.** Every citation must hold:
   - The line number exists, and the line was shown to the model.
   - The quote is in that very line, compared after normalization (width, case, punctuation and spaces ignored).
   - A relation such as "decided" or "committed" cites its subject's own words.
   - At least one end of a relation is named in the cited line.

   Failures are rejected with a code and logged. Some things are flagged instead and never applied automatically:
   - a date that is neither the evidence day nor written in the line;
   - a claim about you on a day you neither spoke nor were named;
   - quotes from bots;
   - partial quotes;
   - low confidence.
4. **Items and the graph.**
   - An item's identity is its class, subject and claim in canonical form, so the same claim seen again strengthens the existing item.
   - Facts and knowledge become active when plainly stated, or once two independent sources support them.
   - Practices go from observed to recurring to established.
   - Graph relations are dated assertions:
     - A new value of a single-valued relation (for example, "reports to") supersedes the old one.
     - An older value is kept as history.
     - Two different values on the same day are both held for you to decide.

   A ledger keyed by content (unit, admitted lines, prompt version) makes the pass idempotent. Raw answers are kept, so a change to the checks can be replayed without calling the model again.

## Weekly pass

- **Promote and age items.** A fact nobody has mentioned for `stale_days` becomes stale. A practice unseen for eight weeks becomes dormant. Knowledge does not age; it is only replaced.
- **Merge and surface contradictions.** Alike items are merged after the model confirms they say the same thing. Contradictions become proposals; the model does not pick a side.
- **Clean the graph.** Relations nobody has mentioned for `stale_days` become stale. Nodes with the same name are proposed for merging; a merge can be undone.
- **Reconcile.** Everything derived from entities the knowledge base no longer has is removed.
- **Health checks (no model).**
  - Pseudo-people: system messages, bots, no-reply addresses.
  - You, recorded as a person.
  - Project aliases that are chat names or generic words.
  - Projects with too many members.
  - Projects with no activity for four weeks.
  - Coverage gaps.
- **Record what is ready.** Up to 30 proposals a week to add ready items to the confirmed facts (`pinned`) of the entity they concern. The hub never overwrites pinned facts and shows them to the assistant.
- **Spot check.** Ten items a week, picked deterministically, for you to mark right or wrong.

## Proposals, shadow mode and undo

- **Shadow mode.** For the first `shadow_days` (default 14), proposals are only judged right or wrong; nothing is written. After that, `mode: auto` sends them to review.
- **Automatic application.** Kinds listed in `auto_apply_kinds` are applied automatically only after at least 10 proposals of that kind have been judged, with precision at least `auto_apply_min_precision` (default 0.95). High-risk kinds are never applied automatically. Automatic writes do not lock the fields they touch, and they stay listed until you acknowledge them.
- **Expiry.** A proposal shown and left alone for `proposal_expiry_days` (default 14) expires, and nothing changes. The clock starts when the proposal is first shown.
- **Holders.** Accepted writes carry their citations as `evidence`. The curator then holds the fields it wrote, so the built-in pipeline no longer rewrites them. Writes the curator applied by itself use `lock: false`, so the pipeline may still replace them.
- **Conflicts.** If you or another extension hold a field, the hub refuses the whole write with `conflict`. The proposal is then marked as held, says who holds what, and is never retried.
- **Undo.** Every write records the previous values that `kb.update` returns in `before`, and a hash of the touched fields as written.
  - Undo first checks that those fields are still as the curator left them. If someone has changed them since, it stops and does not overwrite the change.
  - Otherwise it writes the previous values back, and hands back with `unlock` the fields nobody held before. Unlocking `pinned` clears it.
- **Blocked proposals.** Proposals that need a hub method that does not exist yet (for example, marking a person as not a person) are listed as blocked.

## Model calls

The curator asks the hub to run models through `model.complete` (permission `models`). The hub runs each call on the curator's own model purpose in Settings › Model configuration ("enkeep 记忆" under Extensions), which follows "Information processing" until you pick another model for it; the curator never holds a key. Answers come back checked against each call's JSON schema. When the hub has no model for it (an older hub, nothing set up, the connection paused for quota), the curator runs only its deterministic steps (triage rules, health checks, reconciliation) and the report says why. There is no budget cap: usage is recorded per day and step and shown in the reports. Prompts live in `internal/prompts`, one file per call, with versions pinned by a golden test.

## Configuration

[Manifest and settings schema](../../extensions/enkeep-memory/persona-extension.json). Every setting has a working default; the descriptions are in the manifest.

## Local validation

```sh
go -C extensions/enkeep-memory test -race ./...
go -C extensions/enkeep-memory run . dryrun -fixture testdata/sample -from 2026-11-03 -to 2026-11-04 -out /tmp/enkeep-memory-dry
```

The dry run uses a synthetic knowledge base (`testdata/sample`) and a deterministic stub model. Its writes change only an in-memory copy, and it prints the writes it would make. Tests never use real accounts or data.

To try a day on your own knowledge base with the real model, open 记忆体检 (the extension's page) and use 试跑: it runs that day's daily pass with the model the hub gives the curator, on a fresh temporary state, records knowledge-base writes instead of making them, and saves nothing. The report goes to the extension's data directory (`dryruns/daily-<day>.md`) and its summary (model calls, tokens, proposals) shows on the page.
