# enkeep-memory v4: entry structure and package contracts

The curator collects everything the user does in Persona and turns it into entries for six facets, all equally
important, plus cards for the people the user deals with. This file is the contract between the work packages.
Changing a type or a file format here means telling every package that uses it.

## Facets and grains

| Facet (`entry.Facet`) | What | Grains |
|---|---|---|
| `interaction` ① | how the user and the AI work together | action / class / abstract |
| `tools` ② | how the AI works with machines, tools, runtimes | action / class / abstract |
| `judgement` ③ | the user's default judgements at a fork | action / class / abstract |
| `workflow` ④ | how the user handles a kind of task | action / class / abstract |
| `world` ⑤ | facts, relations, the landscape | action (one fact) / class (relation) / abstract |
| `people` ⑤′ | one card per person entry: fact, user_eval, inferred, commitment | action / class / abstract |
| `persona` ⑥ | the user as a person | action (evidence only) / class / abstract |

- Action entries come from the daily extraction (`schema.Observation`, and `schema.Item` facts for ⑤) with quotes.
- Class entries are made by consolidation from ≥1 action (thresholds adapt to data volume, design 4.4).
- Abstract entries must pass the abstract gate (5.3): `Default` set and differing, ≥3 member classes from ≥2
  situations and ≥2 active days, `Situations` ≥2. The gate never relaxes with little data.
- ⑤′ and ⑥ action entries are evidence only (no exit), except commitments.

## Flow and ownership

```
sources (P1)      units: kb chats/docs/meetings/recordings + persona-* (S1) + agent-* (S3) + ask-* (S2) + todo-* (S5)
  │               catalogs: about-me (S7), memory files (S6)
speaker (P4)      Unit.Who[n] = self | person:<id> | ""   (3.2)
triage (P4)       drop only: unresolved speaker, no intersection, bots/alerts/broadcasts (3.3)
extract (P2)      one prompt, English instructions, target language parameter → schema.DailyOutput
                  .Facts (⑤) .Assertions (⑤ relations) .Observations (①②③④⑤′⑥)
checks (P2/P4)    quotes, self-only for ③④⑥ and ①'s asks, impact, language, secrets, K13
entries (P3)      State.Entries; events.jsonl; recall → judge → file/counter/new; counts; status; abstract gate
signals (P5)      State.Corrections (① ledger), State.Digs (friction → segment → ② entries)
people (P9)       State.Contacts (intersection list), card entries (facet people), commitments
exits (P6)        files under the data dir (below); State.Exits
page (P7)         start card, sources card, facets, cards, check page, switches, onboarding, weekly report
replay (P8)       offline.go: history replay, readings, samples, cold start, smoke
```

| Package | Owns (writes) | Reads |
|---|---|---|
| P1 `internal/hubapi`, `internal/sources`, `internal/convsrc` | units of S1–S7, S11 filters; `State.Sources` availability and volumes; `State.Cursors`; `State.Backfill` | — |
| P2 `internal/prompts`, `internal/schema`, `internal/verify`, `internal/norm`, `curate/judge.go`, `curate/rules.go` checks | `schema.Observation` contract, checks, rejections | `Unit.Who`, switches K13 |
| P3 `curate/entries.go`, `curate/synth.go`, `curate/own.go`, `curate/thresholds.go`, `internal/store` | `State.Entries`, `Tombstones`, `Types`, `ActiveDays`, `events.jsonl` | observations |
| P4 `internal/speaker`, `internal/triage`, `internal/graph`, `curate/daily.go` gates, `curate/proposals.go` | `Unit.Who`, `State.SpeakerChecks`, `SourceStatus.LearnPeople`, kb writes for ⑤ | `kb.owner`, members |
| P5 `internal/signals` | `State.Corrections`, `State.Digs` | S1–S3 items via `hubapi.Hub`, entries |
| P6 `internal/export` | files below, `State.Exits`, `Entry.Export`, `Entry.Effective` | entries, switches |
| P7 `ui.go` | `State.Onboarding`, `State.Switches` (user), verdicts, user edits | everything |
| P8 `offline.go` | readings, samples (files under the replay output dir only) | everything |
| P9 `internal/people` | `State.Contacts`, `State.PersonMerges`, people entries | `Unit.Who`, evidence |

Every package writes entries only through the P3 functions (`curate.(*Curator).Observe`, `File`, `UserEdit`…) so
that every change is an event.

## Ids and keys

- Entry id: `<facet initial>-<sha1(facet, grain, normalized text, person)[:10]>`, e.g. `j-3f2a…`.
- Person: `person:<kb id>`; project: `project:<kb id>`.
- Unit refs: `chats/<id>`, `docs/<id>`, `recordings/<id>`, `chats/persona-<id>` (S1), `agents/<machine>/<session>`
  (S3), `asks/<day>` (S2), `todos/<id>` (S5).
- Switch keys: `K1`…`K17`, per item `K2.<fork type>`, `K13.<category>`, `K16.<source>` (`entry.Switch`).

## Exit files (data directory; contract with the hub, design §12)

| Exit | Path | Max | Hub change |
|---|---|---|---|
| A per-turn block | `assistant/instructions.md` | 6 KB, sections ① 1.8 ② 0.2 ③ 1.5 ④ 0.9 ⑤ 0.6 ⑥ 0.9 KB | H1 |
| B skills | `assistant/skills/machines-and-tools/SKILL.md`, `assistant/skills/ways-of-working-<slug>/SKILL.md` | 256 KB total | H1 |
| C knowledge base | `kb.update` (people pages, project timelines, the user's own entry) | one sentence each | — |
| D agent project context | `agent/projects/<project slug>.md` | 1.5 KB each | H4 |
| E inbox | `inbox/context.md`, `inbox/never_quote.txt` (one line each, not put in any prompt) | 1 KB / 64 KB | H5 |
| F ask_user suggestions | a fixed line in exit A telling the assistant to add "you would usually pick X because …" | — | H1 |
| G page and weekly report | plugin page; weekly report via `notify` | — | — |
| manifest | `exits.json`: exit → hash, bytes, entry ids | — | — |

Never in A, B, D: K13 categories, ⑤′ user evaluations and inferred lines. Never in A: ⑤′ at all.
Exit A carries the fixed line: entries marked inferred or evaluation on people pages are for reference only and
are not to be passed on.

Until the hub has the change, an exit's files are written all the same, `State.Exits.Waiting[exit] = "H1"` and the
page says "waiting for the hub". Nothing fails.

## Hub interfaces (stubs until the hub ships them)

- `hubapi.Hub` (P1): `Sessions`, `Items` (`fold_work=1`, `after_seq`, `from_seq`/`through_seq`), `Interactions`
  (`since`/`before` need H3), `Todos`, `TodoDecisions`, `AboutMe`, `Memories` (H2), `Adoptions` (H1/H4/H5
  readback), `Notify`. Errors: `hubapi.ErrNoScope`, `hubapi.ErrWaitingHub` (`*WaitingHub{Change}`).
- `Fold.Errors`, `Fold.Tools` come with H3; without them `Fold.Known` is false and the screen does not read "0
  errors" as "no errors".
