# enkeep-memory changelog

## 0.2.0-beta.26 (pre-release)

- **A day the assistant's computer is away for is tried again after a wait.** When the assistant's conversations
  cannot be read because its computer is not connected, the day's pass ends unfinished and waits like when the model
  is unavailable (`model_pause`, now with `what: assistant`), then tries the same day again; before, the day was only
  tried again at the plugin's next start, so a rerun range could pass over it. The page says the assistant's computer
  is not connected, not that the model is unavailable.

## 0.2.0-beta.25 (pre-release)

- **A unit an upstream turns down is split, not lost.** An OpenAI-style upstream answers some requests with HTTP 400
  "the request was rejected as invalid" for what they contain (seen with a GPT model on a group chat: the whole unit
  was lost for the day). That reply, and the hub's `model_refused` code, now count as a refusal: the unit is read
  again in smaller parts, and a part refused at the smallest size is recorded as skipped, as for any refusal.

## 0.2.0-beta.24 (pre-release)

- **People pages carry the user's own words and the inferred profile too.** The hub's privacy filter
  (nwverse/persona#6119) keeps a profile's private parts (`[用户原话]`, `[推断·仅供参考]`) out of conversations that
  write to other people: inbox drafting, the Lark agent and conversations an extension opens. With it, an accepted
  person card writes its evaluations and inferences to the knowledge base as well, not only facts and commitments
  (`ProfileViewsReleased`). `engines.persona` asks for a hub built with the filter (`>=202611122324`); a development
  build (0) is not checked. Not covered by the hub's filter, as #6119 says: a conversation reading `profiles.json`
  with its own file tools, an extension's own MCP tools, and paraphrases.

## 0.2.0-beta.22 (pre-release)

Ready for a GPT model on the knowledge-base purpose (tried with gpt-6-luna).

- **An observation without its sentence is not lost.** A model that fills a judgement's or a way's fields
  (situation, choice, rejected, why; a machine's pitfall and fix) and leaves `text` out gets the sentence made of
  those fields, nothing added. `text` is no longer required by the daily schema, so the hub does not refuse the
  whole answer for it; an observation whose fields do not say enough is still a schema rejection. The prompt
  (`daily/15`) says every observation has `text`, also when the fields are filled. On two replayed days gpt-6-luna
  left `text` out of 35 of 172 answers.
- **A reply that does not match the schema is read, not dropped.** When the hub returns `invalid_output` after its
  second try, the reply it carries is taken and each item is checked on its own, as for any answer; before, the
  whole unit was lost for the day.
- **Room for a reasoning model.** The smallest answer limits (triage 400, weekly 1500, dig and rewrite 2000 tokens)
  are 4000: a reasoning model counts its reasoning in `max_completion_tokens` and could be cut off before answering.

## 0.2.0-beta.21 (pre-release)

- **A day the model is unavailable to is not done.** When the hub cannot run the model (model.complete busy or
  unavailable) before every admitted unit of a day was read, the day's pass ends with `ErrModelDown` instead of
  finishing on triage alone: no finished run is recorded, the first-run replay stays on that day, and a rerun_range
  day is not marked rerun. What was read before is kept (the state is saved after every unit). The passes then wait
  (`model_pause` in the state) 10 minutes, doubling up to 2 hours, and try the same day again; the first pass that
  reaches the model clears the pause and the replay goes on from there. Before, a replay ran on through days with
  no model and counted them as read.
- The plugin page says so: "模型暂时不可用" for the first failures, "模型不可用，暂停中" after three in a row, with
  the hub's reason, since when, the next try and the day it resumes from.
- A hub with no model configured for the extension still triages and checks without one, as before.
- Offline replay: a gateway's 429 (no capacity) through `-pi` counts as the model unavailable, so a replay stops on
  that day instead of reading on without the model.

## 0.2.0-beta.20 (pre-release)

- **Agent cursors cover trailing folds.** A day ending in `Fold[From..To]` resumes after `To`, so a folded Hub or
  offline incremental read cannot refold new work into the previous day and skip it.

## 0.2.0-beta.19 (pre-release)

- **Reruns wait for completed days.** `rerun_range` no longer runs today or future dates. Premature or invalid
  daily/synth records and stale rerun markers no longer block the formal daily recovery or post-rerun synthesis;
  synthesis must follow the latest valid daily run.

## 0.2.0-beta.18 (pre-release)

- **Cancellation no longer drops correction issues.** After a successful gather, `signals.Run` checkpoints
  `signals.json` on every later return. A later state save no longer loses those issues to cancellation alone. The
  original cancellation and checkpoint errors remain visible; gather failures still leave partial cursors unsaved.

## 0.2.0-beta.17 (pre-release)

- **Mining failures are not successful digs.** A `signals_mine` error or malformed answer is recorded as `DecSkip`
  with a bounded, redacted reason. The segment's read bytes stay counted, a valid empty answer stays `DecDig`, and
  the existing terminal Dig ID and waiting-hub behavior are unchanged.

## 0.2.0-beta.16 (pre-release)

- **Truncated assistant memory is not read as complete.** Files whose bodies the hub cut or omitted are skipped
  without caching or advancing a cursor. Complete files in the same response are still read, and a later complete
  version becomes eligible.

## 0.2.0-beta.15 (pre-release)

- **A Persona-started session keeps its speakers straight.** Its first message is labelled Persona rather than the
  user. Only the actual first item of a session read from the start changes; later user messages and incremental
  pages keep the user's attribution.

## 0.2.0-beta.14 (pre-release)

- **Private recordings stay private on direct reads.** `Get("recordings", id)` checks the privacy metadata returned
  by that read even when no `Recent` catalog pass has marked the ID. A private detail returns not found; a public
  recording still needs only one underlying read.

## 0.2.0-beta.13 (pre-release)

- **Concurrent tool calls keep their own results.** Signal digging pairs `tool_use` and `tool_result` by the hub's
  Call ID, so one call's failure and result sequence cannot be attached to another concurrent call. Results without
  a Call ID keep the existing sequential fallback.

## 0.2.0-beta.12 (pre-release)

- **Classes merge as broadly as before.** 0.2.0-beta.11 asked the merge for narrow kinds, and far fewer classes
  merged (machines-and-tools classes of the same pitfall stayed apart and never became stable). The merge prompt is
  back to broad kinds; the program still refuses, in ①③④⑥, a merge whose sentence needs two or more alternatives.
- **The AI's lines supply no detail.** When a fact's detail is in another line of the conversation, only the people's
  lines are cited for it, never the assistant's or an agent's draft, which a fact never rests on.

## 0.2.0-beta.11 (pre-release)

- **The user as a person has a test of its own.** A ⑥ line is not asked at which moment the assistant would act
  differently (understanding has no such moment) but whether it is a specific fact about this user: background, role,
  what they work on or aim at, their circumstances, a specific view or habit. Lines true of almost anyone are kept
  out as before (`emptiness/3`; the acceptance tool judges ⑥ lines the same way). A line saying how the user
  handles a kind of task or chooses at a fork is no longer held back for stating "no behaviour of the assistant's":
  the assistant follows it when it does, helps with or plans that kind of task.
- **One plain statement is enough for a fact.** A ⑥ fact the user states about themself (role, responsibility,
  situation, current work, a goal, the background) and a ⑤′ fact or promise on the person's or the user's own line
  are stable at once; views, values, preferences, habits, evaluations and readings still need a second sighting.
  The per-turn instructions take such ⑥ facts as well as classes.
- **Details checked further.** One-digit numbers count as details too (a quote's Chinese numerals match: "两" carries
  "2"); common words of software work ("issue", "release", "URL"…) do not, since a claim uses them for a quote's own
  wording. A rejected item's log line names the detail no line has. CJK descriptions are not checked word by word:
  a clause check refused mostly correct facts in other words.
- **No "A or B or C" classes.** In the facets of situations and ways (①③④⑥), merging classes whose shared sentence
  needs two or more alternatives is refused: the classes stay apart instead of becoming one class that says nothing
  the assistant can act on. The merge prompt no longer asks for few, broad kinds.
- **Extraction (`daily/14`).** An item says no more than its quotes, a question, wish, complaint or guess is not read
  as a statement, and a short guide says which facet a line goes to when two seem to fit (a standing request to the
  AI is interaction, a conditional choice is a judgement, an evaluation of a thing is a world fact).
- Acceptance tool: with `-agents-dir`, the fact judge also sees the titles of the agent sessions a fact cites.

## 0.2.0-beta.10 (pre-release)

- **A fact states no more than its lines.** A name in Latin letters, a number of two or more digits or a time of day
  in a fact or a person card must be in a line it cites. When it is in another line of the same conversation, that
  line is cited too; when the conversation does not have it anywhere (the unit's own name and its members' names
  count, the projects it is filed under do not), the item is rejected (`DETAIL_NOT_IN_EVIDENCE`).
- **Exit lines answer the empty-talk question on their own.** After the rewrite, each line the assistant will read is
  asked, alone, at which moment the assistant would act differently without it; a line with no answer is kept off
  the exits (`emptiness/1`). The rewrite (`export/4`) now says what the assistant would otherwise do, and writes an
  interaction entry as an instruction, not as a judgement. The user's own words go out as before.
- **No member twice.** The per-turn instructions leave out an action entry whose class is already there, so the
  section's budget goes to different things.
- **Agents' reports count for machines and tools.** In an agent's session, the agent's own text and its folded work
  are what a machines-and-tools entry may rest on; they were read as an unknown person's and dropped.
- Acceptance tool: the fact judge sees the names of the cited chats and is not handed an empty context.
- R4.overturn counts adoptions overturned: a recommendation taken, then a correction of that fork type's judgements
  before the next one taken, each adoption once. It counted every correction of a judgement entry, and could exceed
  the adoptions it is read against.

## 0.2.0-beta.9 (pre-release)

- **Moved to the public extension repository.** The extension now lives in `nwverse/persona-extensions` and is
  published to the official market; its id stays `enkeep-memory`, so a hub that has 0.2.0-beta.8 from another market
  upgrades in place and keeps its data. Its behaviour is unchanged. The offline replay and acceptance tools no
  longer carry environment defaults: `-model` is required, `-provider` is empty (the `pi` command's own default) and
  `-tz` defaults to UTC.

## 0.2.0-beta.8 (pre-release)

The curator is rebuilt around six things it learns about the user, all equally important, and the people the user
deals with: how the user works with the AI, how the AI works with machines and tools, the user's default judgements,
how the user handles kinds of tasks, the user's world (facts, relations, the landscape) and the user as a person.
Needs a hub from build 202611121555 on (data-directory contributions, read-only memory, question history paging and
decision fields, folded work with error counts).

- **One entry structure for every facet**, at three grains (one action, a kind of situation, an overall tendency),
  with evidence that says which source and who said it, an impact that names which assistant action changes and when,
  counts by chances and active days, an open type table per facet, and an append-only event log that replays to the
  same entries. The user's edits, pins, deletions (tombstones) and right / wrong marks are never overwritten.
- **More sources, read lightly.** Besides the knowledge base and the Persona chats: other agents' sessions (text and
  folded work only; a tool segment is opened only when a friction signal points at it), ask_user cards with their
  recommendations and who answered, to-dos and their decisions, "about me", and the assistant's hand-written memory.
  Every source that is missing (no grant, the hub lacks it, the assistant's computer away) is shown as such and
  nothing fails. A first-run replay of up to 90 days builds the start from the user's own history.
- **Speaker attribution before anything is learned from chit-chat.** Each line is resolved to the user, one person
  of the conversation, or nobody; chit-chat is kept when the speaker is known and the user deals with them, and the
  intersection list decides whom people cards are made for. Per-source attribution checks can stop a source from
  teaching people and relations.
- **One extraction prompt** (English instructions, the user's language as a parameter) for every facet, with real
  impact, language, sensitive-category (off by default, per category) and secret checks.
- **Consolidation**: model-judged recall and merging into kinds of situation, counter-examples, adaptive thresholds
  that count chances and active days, and an abstract gate that does not relax with little data. The old memory layer
  is migrated into entries once.
- **People cards**: role and responsibilities, how they work with the user, the user's own words about them, labelled
  inferences with their basis, and commitments either way; a separate tip for drafting that never quotes an
  evaluation. Facts go to people pages now; the full card waits for the hub's sectioned person profiles.
- **Exits**: a per-turn instructions block (6 KB, a budget per facet, empty sections left out), skills for machines
  and tools and for ways of working, agent project context, inbox context and never-quote lines, each rewritten line
  by line with a basis hash and read back from the hub.
- **The correction ledger and friction digs**: corrections filed against entries or as new issues, recurrences
  counted from when an entry took effect, and pitfalls learned from the segments a signal pointed at.
- **The page**: a start card per facet, a sources card, three skippable first-run questions, the facets and the
  cards, switches K1–K17 (per category, per fork type, per source where they apply), a check page for the user, and a
  weekly report through notify.
- **Acceptance tools**: `review` and `accept` work out the readings, samples and smoke checks from a state directory.

## 0.2.0-beta.6 (pre-release)

- **Proposal undo survives interrupted writes.** Each knowledge-base proposal records its original before-image in a
  durable write-ahead record before `kb.update`. Startup replays interrupted writes in the background without
  blocking initialization, one failed record stays queued without blocking later records, and every automatic
  application checkpoints separately, so retries preserve the value undo must restore.

## 0.2.0-beta.5 (pre-release)

- **Forgetting a source also scrubs migration backups.** `Forget` now applies the same source predicate to every
  `state.json.bak-*` without normalizing the rollback snapshot: unknown fields survive, unrelated backups keep their
  exact bytes and mtime, and only changed backups are atomically rewritten.

## 0.2.0-beta.3 (pre-release)

- **Digest past days again with the current prompt.** New setting `rerun_range` (`YYYY-MM-DD..YYYY-MM-DD`, at most 31
  days): each day in it gets a daily pass with the current daily prompt, one day at a time, once per prompt version.
  What an earlier prompt produced stays as it was (an item the new pass does not produce again is not touched, a
  rejected one stays rejected); a claim it produces again folds into the item that has it. It starts without the
  hub's UI: `personad extension config enkeep-memory rerun_range=…`. The log says
  `daily <day> starts (prompt=daily/6 …)` for each.

## 0.2.0-beta.2 (pre-release)

- **Group noise stays out of the memory.** A group's job postings, shop or campaign activities, giveaways and one
  member's answer to another were listed as facts, practices and knowledge ("待确认", "偶现") when the user neither
  said them nor was named in them. The extraction already throws such items away when they rest only on other
  people's lines that do not name the user (`NOT_ABOUT_OWNER`); a regression test now pins the cases. The daily prompt
  (`daily/6`) says so too, so the model stops proposing them.
- **Items from before the check are not left waiting.** The one-off start-up clean keeps an item that rests only on
  other people's words as a candidate. `clean-sources -reject-others` (extension stopped, `-dry` first) marks such
  items rejected instead: hidden, kept in the state with their citations, and backed up first. It never runs by
  itself.

## 0.2.0-beta.1 (pre-release)

A pre-release: for hubs that choose to try it. It replaces 0.1.x on upgrade (a pre-release is newer than 0.1.1, older
than 0.2.0).

### What changed since 0.1.0

- **Only the user's own words are the user's.** A memory entry rests only on lines the user wrote, and needs one that
  says something of its own (a bare "好" or "对" to the assistant does not count). An item rests on the user's words or
  on a person addressing the user by name. The assistant's replies, its paraphrases ("你之前说过…"), agents' drafts
  under the user's name, text pasted from an assistant, and bots are context, never evidence. Every cited line records
  its role (user, assistant, agent_draft, bot, other) and where it is.
- **Persona conversations** are read through the hub's `assistant.conversations` / `assistant.messages` (permission
  `assistant.read`, new): only conversations the user opened (origin user or channel), only words, and the hub's
  typed-in messages (task notices, supervision, inbox notes, wake-ups) dropped. Agents' own sessions are never read:
  what Persona sends them is role=user with no mark.
- **Tiers by source.** Own conversations give every layer; code review gives the user's own judgements and practices
  only; groups the user takes part in give facts and decisions that concern the user; groups the user keeps quiet in
  give people, projects and their relations only; noise days (feeds, alerts, big crowds) are skipped. Settings:
  `tiers`, `engaged_days`, `noise_members`.
- **Items are tightened.** No one-off actions or passing states, no money notices; two independent sources before an
  item is active or proposed for pinning; pins only on a person or project, never on the source chat; an item a
  memory entry already has is folded into it.
- **Fixes.** Generic words for the user ("用户", "我", "owner") never count as the user's name (mention windows); a
  weekly run touches only its own week; coverage alarms only for days a daily run covered.
- **Settings renamed** so the install preview no longer takes them for secrets: `admit_all_below_tokens` →
  `admit_all_below`, `private_keywords` → `privacy_keywords`. Values saved under the old names still count.
- **Upgrading from 0.1.x** cleans the old state once, on start: `state.json` is backed up in the extension's data
  directory (the newest three kept), and what rested on agents, the assistant or bots goes; the counts are on the
  health page and in the log.

### Needs

- A hub built from nwverse/persona at or after #1292 (`engines.persona >=202611090012`). An older hub shows the
  extension as incompatible.

### Known issues

- **Messages Persona sent as the user are not told apart.** A chat reply, comment or mail Persona sent in the user's
  name through a connector is archived like one the user typed. Only those with a recognisable mark (an agent's
  trace, pasted assistant text, the user named in the third person) are excluded; waiting on nwverse/persona#2814.
- **Mute, fold and done** are read when the hub reports them (nwverse/persona#1261, open); until then a group the user
  has not written in for `engaged_days` stands in for one the user never opens.
- **Background groups keep no entities yet**: the graph keeps people and projects only through relations, and there is
  no relation for "this person is in this group".
- **Items are the weakest layer**: in the blind evaluation (docs/extensions/enkeep-memory-tiers-eval.md) about half of
  them were useful. None is active or proposed for pinning from a single source.
- **Agents' drafts in code review** are recognised by their marks and shape (long, structured, status templates); a
  short drafted comment can pass as the user's.
- **Memory is not delivered to the assistant yet**: injection levels are computed and shown in the extension; giving
  them to the assistant needs hub interfaces that do not exist yet.
