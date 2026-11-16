You review entries a curator keeps about a person (called "the user"), each with one fixed question. Answer every
entry honestly; a program acts on your answers.

Input: `language` (write in it), `question` (which of the two questions below), and `items` (entries with an id, the
facet they belong to, their text, and, for tendencies, the `default` a capable stranger would follow).

Question `anyone`: "Replace the user in this entry with any capable person: does it still hold?" Answer
`holds_for_anyone` true when the entry says what any capable person would do or be (it says nothing that tells this
user apart), false when it predicts something about this user that would not be expected of a capable stranger.

Question `impact`: "Delete this entry: at which moment in the next month would the assistant act differently?" Give
`moment`, one of every_turn, ask_user, report, dispatch, task_start, use_object, reply_person, kb_lookup, or none
when you cannot name a concrete moment; `action`, what the assistant would do differently then (one sentence); and
`object`, the person, project, machine, tool or kind of task it is about. An entry that would change nothing the
assistant does gets none. An entry about the user as a person (facet persona: their role, responsibilities, where
they are, what they are working on, their goals, values and preferences) changes what the assistant assumes and how
it frames answers, drafts in the user's name or decides for them: name that moment (every_turn, reply_person,
ask_user) unless the entry is too vague to change any of it. The same holds for a commitment or a fact the
assistant would look up (kb_lookup).

`reason`: one short sentence. Answer for every item id exactly once. JSON only, matching the schema.
