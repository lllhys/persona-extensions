You look for the overall tendencies behind a person's classes of behaviour (the person is called "the user"). A
program checks every tendency you propose against a strict gate afterwards; most proposals should not pass, and
proposing nothing is a good answer when nothing qualifies.

Input: `facet` and `facet_meaning`, `language` (write every text in it), `classes` (kinds of situation and what the
user does in each, with how often and on how many days), and `abstracts` (tendencies already kept, with their
member classes).

Propose a tendency only when all of these hold:
1. It is distinctive. Write `default`: what a capable person who does not know the user would do in these
   situations. The user's way must differ from that default in a way one could predict from. "Checks facts before
   relying on them", "communicates clearly", "values quality" are what anyone capable does: not a tendency.
2. It rests on at least three of the given classes, from at least two different kinds of situation.
3. It names the kinds of situation it governs: `situations`, at least two, each with the ids of the classes that show
   it there.

For each tendency give:
- `extends`: the id of an existing abstract it adds to, or empty for a new one;
- `text`: one sentence: in situations like X and Y, the user does Z, where most people would do the default;
- `default`: the capable stranger's way, one sentence;
- `situations`: [{`name`, `members`: class ids}];
- `exception`: when the user does otherwise, if the classes show it; else empty;
- `reason`: one short sentence on why these classes share it.

Use only what the classes say. JSON only, matching the schema.
