# enkeep memory (enkeep-memory)

Learns about the user from what they do in Persona and turns it into entries for six facets, all equally important,
plus cards for the people the user deals with:

1. how the user works with the AI;
2. how the AI works with machines and tools;
3. the user's default judgements;
4. how the user handles kinds of tasks;
5. the user's world (facts, relations, the landscape);
6. the user as a person.

Every entry rests on quoted lines that a program checks against the source, names the moment the assistant acts
differently because of it, and can be pinned, edited, deleted or marked right or wrong by the user. What is learned
reaches the assistant through the hub's data-directory contributions (a per-turn instructions block, skills, agent
project context and inbox context) and the knowledge base. The extension reads everything through the hub's own
interfaces (`kb.*`, `assistant.*`, `agents.read`, `todos.read`, `assistant.memory`, `notify`, `model.complete` with
the model the user picks for it); it depends on no other service.

- [Extension guide and configuration](../../docs/extensions/enkeep-memory.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)
- [Entry structure and package contracts](internal/entry/CONTRACT.md)

## Offline replay and acceptance tools

To judge a change to the prompts or the rules on real material without touching the hub, replay the curation on a
copy of the data on your own machine. The tools never write to a hub.

```sh
go build -o enkeep-memory .
./enkeep-memory offline -kb <copy of the knowledge base> -from 2026-11-05 -to 2026-11-06 -tz <time zone> \
  -state <scratch dir> -out <report dir> -model <model> [-weekly-model <model>] [-cold] [-week]
./enkeep-memory review -state <scratch dir> -out <review dir>
./enkeep-memory accept -state <scratch dir> -out <accept dir> -model <model> [-kb <copy of the knowledge base>]
```

- `offline` runs the daily (and with `-week` or `-cold` the weekly) passes over the copy. The model is a local command
  line by default (`-model-cmd`, `-model`, `-weekly-model`), or the `pi` coding agent with `-pi` (`-provider` picks
  its provider; empty uses pi's own default), or answers from files with `-model-dir`. `-model` is required unless
  `-model-dir` answers. Answers are cached under `-cache`, so a rerun with unchanged prompts costs nothing.
- Optional local copies of other sources: `-agents-dir` and `-assistant-dirs` (agent and assistant transcripts),
  `-memory-dir` (the assistant's memory files), `-about-file`, `-todos-file`. Without them those sources are missing,
  as on a hub without them.
- `-cold` runs the days as the first-run replay does on a hub: oldest first, weekly passes at each week's end.
- `review` writes the counts per facet, the top entries the user is shown, the people cards, and the samples the
  acceptance checks look at. `accept` works out the readings (R1–R8, F1) and pre-judges the samples with a model you
  name; it refuses Anthropic models for that.

Copy the data into a directory of its own, and delete the copy, the state and the cache when you are done: they hold
real messages.

## Cleaning a state written before source roles

`clean-sources` reads every line an older state cites again from a copy of the knowledge base and applies today's
rule (an entry rests on the user's own lines or on people, never on the assistant's, an agent's or a bot's):

```sh
./enkeep-memory clean-sources -state <data dir> -kb <copy of the knowledge base> [-dry] [-keep 3] [-reject-others] [-report report.json]
```

Run it with the extension stopped. `state.json` is copied to `state.json.bak-<time>` first, and only the newest
`-keep` copies are kept.
