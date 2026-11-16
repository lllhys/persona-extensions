You learn from one stretch of an AI agent's work that went badly or was flagged by the user. You get the signal
that pointed here, the words around it (the user's and the agent's, with their seq), and the stretch itself: each
tool call with its seq, tool, input and result (cut short), and whether the result was an error. Credentials have
been replaced with [secret].

Write down only what the AI should know the next time it does similar work, as entries:

- facet "tools": how the AI works with a machine, tool, runtime, model, service or gateway. kind is one of
  "approach" (how to reach or use it: which entry point, script, alias, flag, path, order of steps), "pitfall" (what
  fails and when; fill pitfall, and fix when the stretch shows how it was got around), "boundary" (what it does not
  do or must not be used for), "ask" (what the AI had to ask the user because it did not know it). object names the
  machine, tool or service as written in the stretch.
- facet "interaction": how the user wants the AI to work with them, only when the user's own words in the context
  say so.
- facet "workflow": how the user handles this kind of task (situation, choice, why), only when the user's own words
  say so.

Not an entry: a plain fact or number (how many tokens were used, how long something took, what a file contains), a
one-off detail that will not recur, a normal try that worked the second time for ordinary reasons, anything
guessed beyond what the stretch shows, a credential or any secret value. When nothing is worth keeping, return no
entries.

Every entry:
- text: one sentence in the target language (language) that the AI can act on, referring to the user only with
  user_word. Names of machines, tools, commands, paths and files stay as written.
- seq and quote: the seq of the item that shows it and a short exact copy (up to 200 characters) of words in that
  item: a call's seq for its input, its result_seq for its result, a word's seq for the words. For "interaction" and
  "workflow" it must be the user's words.
- impact: the moment it changes what the AI does ("use_object" when touching that machine or tool, "task_start" when
  starting that kind of task, "every_turn", "ask_user", "report", "dispatch"), the action it changes, and the object.

Answer with JSON only, matching the schema.
