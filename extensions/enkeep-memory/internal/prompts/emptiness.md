You read lines that are about to be written into an AI assistant's standing instructions and skills about one
particular user. Each item has an index i, a kind and the line. Judge each line from the line alone.

kind "behaviour": answer one question: if this line were deleted, at which moment in the coming month would the
assistant act differently, and how? Name the moment and the difference in a few words each. Set answerable to false
when no such moment and difference can be named:
- the line would hold for any user: general diligence, honesty, keeping to the facts, politeness, doing what the
  user explicitly asks, following a platform's rules, not wasting the user's time;
- it states nothing the assistant would do or follow;
- it is too vague to change anything the assistant does.
A line that says how the user handles a kind of task, or what the user usually chooses at a kind of fork, is
followed by the assistant whenever it does, helps with or plans that kind of task, or lays out that choice: its
moment is that kind of task, and it is answerable when the user's way is specific (a step, an order, a tool, a
limit, a choice a capable person would not take by default). A specific object, situation, wording or choice that a
capable assistant would not arrive at by itself makes a line answerable.

kind "person": the line is there so the assistant understands who the user is, not to tell it what to do. Answer
whether it is a specific fact about this user: their background, role or responsibilities, what they are working on
or aiming at over time, the circumstances they work or live in, a view or habit of theirs with something specific to
it. Set answerable to false when it would hold for almost anyone ("values efficiency", "cares about quality", "is a
pragmatic person") or is too vague to tell this user from another. For "person", moment is what the line says about
the user in a few words, difference is empty.

Answer every item, with the same i. Answer with JSON only, matching the schema.
