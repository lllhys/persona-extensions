# enkeep memory

[Documentation](../README.md) · [Source](../../extensions/enkeep-memory/)

This extension learns about you from what you do in Persona, so that the assistant works more smoothly with you and
understands you better. It keeps six facets, all equally important, and a card for each person you deal with:

| Facet | What it keeps | Where it is used |
| --- | --- | --- |
| How you work with the AI | what you ask of the AI, corrections, how you want reports and questions | the assistant's per-turn instructions |
| How the AI works with machines and tools | approaches, pitfalls and their way round, boundaries, per machine or tool | a skill the assistant and agents load when they touch that machine or tool |
| Your default judgements | at a kind of fork, what you usually choose over what, and why | the per-turn instructions; a suggestion when the assistant asks you |
| How you handle kinds of tasks | your way of doing a kind of task, and why | skills per kind of task; agent project context |
| Your world | facts, who is responsible for what, relations, commitments | the knowledge base's people and projects |
| You as a person | role, current work, goals, preferences you stated | the per-turn instructions; inbox drafting |
| People you deal with | facts, your own words about them, labelled inferences, commitments either way | people pages (see below); a communication tip for inbox drafting |

Each entry rests on quoted lines a program checks against the original, names the moment the assistant acts
differently because of it, and is counted by how often the kind of situation came up on the days you were active.
You can pin, edit, delete (it is not made again) or mark any entry right or wrong on the plugin page; wrong takes it
out of everything the assistant reads at once.

## Requirements

- A hub from build `202611121555` on: data-directory instructions, skills, project and inbox context with read-back,
  the read-only assistant memory, question history paging with decision fields, and folded agent work with error
  counts.
- Scopes: `kb.read`, `kb.write`, `models`, `assistant.read`, `agents.read`, `todos.read`, `notify`, `assistant.ask`,
  `assistant.memory.read`. A scope you do not grant is a source the extension goes without; the sources card says so.
- A model for it under Settings › Model configuration. The extension never sees the key.

## Sources

It reads, through the hub only: your chats with the assistant, other agents' sessions (their text and folded work; a
tool segment is opened only when a sign of friction points at it, such as an error followed by a retry or a
correction), the cards agents raised and how you answered them, to-dos and their decisions, your "about me" notes,
the assistant's memory, and the knowledge base's connected sources (chats, mail, code review, documents, meetings,
recordings). On install it replays up to 90 days of your history (`backfill_days`), oldest first, before it writes
anything for the assistant. A source you do not have is shown as missing on the sources card; nothing fails.

## Privacy boundaries

- Emotion, finance, health and family content is not extracted unless you turn its category on (per category). Even
  then it reaches only your own plugin page and knowledge base, never anything the assistant reads every turn, a
  skill, an agent's context or the inbox.
- Before learning anything about people from chit-chat, each line is attributed to you, to one person of that
  conversation, or to nobody; unattributed lines teach nothing about anyone. Per-source attribution checks on the
  check page can stop a source from teaching people and relations.
- People cards keep facts, your own words, inferences (always labelled, with their basis) and commitments apart.
  Evaluations and inferences never go into a draft sent to someone: the inbox gets only a communication tip, and the
  lines a draft must never repeat. People pages carry all four parts; the hub keeps evaluations and inferences out
  of the conversations that write to other people for you (inbox drafting, the Lark agent, conversations an
  extension opens), so this version needs a hub with that filter. The filter does not cover a conversation reading
  the profile file with its own file tools, an extension's own tools, or a paraphrase.
- Credentials, tokens and connection strings are never extracted.
- Everything stays in your own hub: the extension's data directory and your knowledge base.

## The plugin page

A start card (per facet: what is in use, what is building up, which source is missing), a sources card (read volume,
attribution checks), three skippable first-run questions, the facets and the people cards, switches K1–K17 (per
category, per fork type and per source where they apply), a check page, and whether each exit reached the assistant
(the hub's read-back). A weekly report goes to your devices when something changed.

## Development

See the extension's [README](../../extensions/enkeep-memory/README.md) for the offline replay and acceptance tools.
