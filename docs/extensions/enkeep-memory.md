# enkeep memory (enkeep-memory)

[Documentation](../README.md) · [Source](../../extensions/enkeep-memory/)

The hub's own pipeline summarizes chats as they arrive. This extension is a second pass on top of it, run on a schedule:

- once a day for the day before;
- once a week, on Monday, for the ISO week before.

It keeps its own state in its data directory and touches the knowledge base only through the knowledge-base RPC (`kb.read`, `kb.write`), and calls models through the hub (`models`). It needs a hub with attributed extension writes (nwverse/persona#6110) and model calls for extensions (nwverse/persona#6102). Every write it makes is either a proposal you accepted or a kind of proposal you allowed it to apply by itself.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `ui` | `enkeep-memory` (`nav_page`) | Tabs: proposals to review, the memory (every conclusion and fact with its layer, state, score and injection level; confirm or reject one), facts, practices, knowledge, the graph, and run reports. Each item opens with its citations. Proposals can be accepted, edited, rejected or postponed. Accepted proposals can be undone. |
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

## Memory

From everything you say (chats, your corrections of the AI, documents, meetings, and what enkeep held before), the extension keeps what you hold: principles, guidelines, experiences, how you are, and facts. The model extracts them and tags every sentence they rest on; everything after that is deterministic and lives in `internal/memory`, with every number in one place (`memory.Params`).

**What an entry is.**

- **Layer**: principle (holds across kinds of matter), guideline (within one kind), experience (one matter), or fact. Facts have a category: attribute, decision, event, progress, to-do, knowledge, opinion, intent.
- **Subject**: your own conclusion, a requirement on the AI, a team rule, or someone else's view. Only the first two can be principles, guidelines or experiences; team rules and others' views are facts on the team, project or person.
- **Use**: for the AI to follow, to help the AI understand you, or for reference. How you usually are or do is a descriptive attribute (a fact), never one of the three layers.
- Labels: one of nine kinds of matter, one or two of twelve life areas, and for facts about you one of eight sub-nodes.

**Rule D: setting × expression.** Each of your sentences is tagged with the setting it was said in (a review, a work discussion, an assignment, a correction of the AI, small talk) and how it was said (a definite position, a suggestion, a question, venting, a joke, an aside), and with flags: whether you really hold the stance behind it, whether it expresses a requirement or a position, whether a question proposes something, what it says about you, and whether it actually supports the entry it is attached to. The pair decides the weight: high (60), mid (35), low (15) or none.

- A position in a serious setting is high. A correction of the AI is high whatever its wording: "I was discussing my judgement, not changing your conclusion" counts like "always check first". Venting while correcting the AI is high too, because you mean it.
- A suggestion is mid; an assignment is a one-off experience until the same meaning comes again.
- Small talk adds nothing to the three layers, only to descriptive attributes. A joke's literal words are never a fact; only its attitude counts. A proposal put as a question becomes an intent, for retrieval only.
- Other people's words, AI summaries and old imports are low. Facts start elsewhere: 50 for a quote, 25 for an AI summary, 15 for an old import, 35 for an intent.

**Rule B: by stance, not by wording.** A guideline needs one sentence that expresses a requirement or a position, said in a serious setting, that supports it. There is no list of normative words and no special threshold for corrections stated as facts: they accumulate like any other evidence. Only venting that asks for nothing ("this is so hard to use") cannot set up a guideline. A suggestion repeated in a second week, a confirmation, a follow-through, or a rule confirmed in enkeep also satisfy rule B.

**Confidence.** Every independent occurrence (one per day and session; the same words copied into another session count once) adds `weight × k × (1 − score/100)`, with `k = 1` in a week the entry was not seen in and `0.5` otherwise. After 7 days without an occurrence the score halves every `14 × (1 + weeks seen)` days, at most 84. Facts do not decay.

- **States**: candidate; active at 50 with a high source or two weeks (descriptive attributes: two weeks; principles: 70, three weeks, two settings and one sentence of yours that satisfies rule B); dormant under 15; obsolete when rejected, replaced, or lost and gone.
- **Guard rails**: an active "don't" rule or hard rule for the AI stops decaying at 50.
- **You decide only when you want to**: a confirmation counts as a high occurrence and makes the entry active that day; a rejection zeroes it, makes it obsolete, and later evidence for it is ignored.
- **Contradictions**: compared once a day; the newer side wins once its score is above the older one's (a tie for facts). Until then it is held as a candidate; the loser stops being injected and decays four times faster.
- **Follow-throughs**: a suggestion later carried out is backfilled to the serious band on that day, automatically, but only when someone states it was done or it shows in a work product (a document, a code change). Words of agreement do not count, and follow-throughs are not put in the report for you to confirm.

**Privacy (rule C).** A sentence that gives away a specific matter of health, finance, relationships or beliefs is never injected; the rest of the entry is. A guard rule for the AI is not private for its topic.

**Injection.** Resident (active principles, the top 25 within 1,500 characters; active global hard rules for the AI, within 2,000), recalled by setting (other active guidelines, descriptive attributes), retrieval only (experiences and facts), or none (private, candidate, dormant, obsolete, lost). Persona cannot yet take dynamic instructions or search an extension's data, so the levels are computed and shown in the extension and the reports; delivering them to the assistant waits for those hub interfaces.

**From enkeep, once.** `enkeep-memory import-enkeep -file export.json -state <data dir> -day <YYYY-MM-DD>` reads an export of enkeep's rules, patterns, knowledge entries, profile facts and workspace state. Their history is accumulated with the same weights, without decay before the import day. States carry over: confirmed rules are guidelines even when their evidence is only venting (the migration exemption, once); what enkeep retired comes in obsolete and stays so; a rejection carries its veto; legacy imports without quotes start at 15 and fall dormant on their own.

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

The curator asks the hub to run models through `model.complete` (permission `models`): the triage tag, the daily digest, the daily memory pass and the weekly consolidation. The hub runs each call on the curator's own model purpose in Settings › Model configuration ("enkeep 记忆" under Extensions), which follows "Information processing" until you pick another model for it; the curator never holds a key. Answers come back checked against each call's JSON schema. When the hub has no model for it (an older hub, nothing set up, the connection paused for quota), the curator runs only its deterministic steps (triage rules, health checks, reconciliation) and the report says why. There is no budget cap: usage is recorded per day and step and shown in the reports. Prompts live in `internal/prompts`, one file per call, with versions pinned by a golden test.

## Configuration

[Manifest and settings schema](../../extensions/enkeep-memory/persona-extension.json). Every setting has a working default; the descriptions are in the manifest.

## Local validation

```sh
go -C extensions/enkeep-memory test -race ./...
go -C extensions/enkeep-memory run . dryrun -fixture testdata/sample -from 2026-11-03 -to 2026-11-04 -out /tmp/enkeep-memory-dry
```

The dry run uses a synthetic knowledge base (`testdata/sample`) and a deterministic stub model. Its writes change only an in-memory copy, and it prints the writes it would make. Tests never use real accounts or data.

To try a day on your own knowledge base with the real model, open 记忆体检 (the extension's page) and use 试跑: it runs that day's daily pass with the model the hub gives the curator, on a fresh temporary state, records knowledge-base writes instead of making them, and saves nothing. The report goes to the extension's data directory (`dryruns/daily-<day>.md`) and its summary (model calls, tokens, proposals) shows on the page.
