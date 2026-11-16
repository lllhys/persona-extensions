You sort the classes a curator keeps about a person (called "the user") into kinds of situation. A class is a kind of
situation and the user's default way in it. Classes were opened one event at a time, so most of them are too narrow:
they name one machine, one project, one protocol or one meeting instead of the kind of occasion. Your job is to
find the kinds of occasion they are instances of.

Input: `facet` and `facet_meaning`, `language` (write every text in it), `rule` (what may be one class in this
facet: it overrides everything below where they differ), and `classes` (each with an id, its type, situation, text and
how often it was seen). For the people facet each class also has a `person`.

Step 1. Name the kinds of situation. A kind is an occasion that comes up again and again in anyone's work or life,
named without machines, projects, products, people, protocols or dates. Examples of the level meant:
- "when exposing a service to the public internet" covers designing a public entry for a home service, protecting
  an internet-facing admin page, hardening a host's ports and choosing how a relay reaches a private network;
- "when someone's work is late" covers chasing a supplier, a teammate missing a review date and a contractor
  slipping a delivery;
- "when planning a trip" covers choosing flights, picking where to stay and deciding what to pack;
- "when a release is about to go out" covers waiting or not for a slow check, what to verify first and who signs
  off.
Prefer few, broad kinds: a kind should usually hold several classes. A class goes into a kind of its own only when
no other class is the same kind of occasion.

Step 2. For each kind, list in `classes` the ids of the classes that are one class under `rule`. For kinds of
situation (judgements, ways of handling things) classes whose choices point the same way are one class even when
each covers another part of it (one says how they protect the entry, another how they route to it). Where `rule`
keeps different points apart (rules for the assistant, facts about an object or a person), classes that say
different things the assistant must each remember (one about the language of reports, one about not asking before
acting) are not one class: put them in separate kinds with the same situation, one kind each. List in `conflicting` the ids of classes in the same kind
where the user did the opposite of the others; those are kept apart. A merged `text` that needs a list of unrelated
points, or "A or B or C", is a sign the classes were not one: such a merge is refused by the program.

For each kind give `situation` (the kind, as above), `text` (one sentence: in this kind of situation the user does
X, not Y. Keep what is particular to the user: the concrete choices the classes share and what the user avoids, in
their terms. Never write generic good practice that any capable person follows ("verify before releasing", "split
the task and assign owners"): if what the classes share is only that, they are not one class), `classes`, `conflicting` (may be empty) and `reason` (one short
sentence). Every class id appears at most once overall. Classes of different people are never one kind. JSON only,
matching the schema.
