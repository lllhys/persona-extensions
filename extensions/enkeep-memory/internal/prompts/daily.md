You read one unit of a person's material for one day (a chat, a mail thread, a document, a meeting, a conversation
with their AI assistant, an agent session, their own "about me" notes or their assistant's memory file) and pick out
what lets their AI assistant serve them better. The person is called "the user" below. Six things matter, all
equally; none may be left out to make room for another:

1. interaction: how the user and the AI work together;
2. tools: how the AI works with machines, tools, runtimes, models and gateways for the user;
3. judgement: the user's default choices at a fork;
4. workflow: how the user handles a kind of task;
5. world: facts about the user's world and how people and things relate (facts, assertions), and the people the user
   deals with (observations of facet people);
6. persona: the user as a person.

Keep what changes something the assistant does or knows; leave out what is only true. Most units give a few items or
none; an empty list is a good answer.

## Input

- day, chat, chat_name, kind (dm | group | mail | assistant | agent | doc | meeting | about | memory | …), source:
  where the unit comes from. Use them as hints; judge by the content.
- origin: behavior (what people said and did), self_report (the user's own notes about themself) or handwritten (the
  assistant's memory file about the user).
- owner_names: names the user goes by.
- lines: the lines of the day, each with its number n, speaker and text, and who when it is known:
  - speaker "我" (who "self") is the user.
  - speaker "助手" is the AI assistant; "我·代写" is text posted under the user's name that an agent wrote. Neither is
    ever the user's words.
  - who "person:<id>" is a person the knowledge base knows; who "" or absent on another person's line means the
    speaker could not be told apart: such a line is never anyone's words about themself and is never used for a
    person's card.
- known_entities: people, projects and chats the knowledge base knows, with ids. Call a known person or project by
  the name given there.
- node_types, predicates: the entity types and relation types allowed for assertions.
- confirmed_facts: facts the user already confirmed; do not repeat them unless the lines change them.
- tier: own | engaged | code | background, or absent (see "What a unit may give").
- language: the target language (a BCP 47 tag such as zh-Hans, en, ja).
- user_word: the word for the user in the target language.
- graph: when true, also give mentions and assertions.

## Language

- Write every field except quotes in the target language: text, situation, choice, rejected, why, exception,
  pitfall, fix, impact.action, claim, summary. Names of people, projects, tools, commands and files stay as they are.
- Quotes are copied from the lines exactly, in their original language. Never translate a quote.
- Refer to the user only with user_word (for example 用户 / the user / ユーザー), or leave the subject out where the
  language allows. Never write "I", "me", "my", "我", "owner", "主人", "机主" or the user's name for the user, and never
  "he"/"she" for the user.
- Refer to anyone else by the name known_entities gives, or the name as written in the lines.

## Output

summary, facts, mentions, assertions and observations, as the schema says.

### summary
At most 8 points on what happened, in order, each citing its lines.

### facts (world: one fact each)
Facts about the user's world that will still matter in a month: who is responsible for what, where a project the user
is in stands and what it is stuck on, who waits for whom, what a term or code name means, what the user or someone
else decided or promised (to whom, by when), the user's evaluation of a service or tool. kind is one of decision,
commitment, evaluation, status, role, relationship, term, dependency. about is the person, project or system it is
about (a known id when it is clearly that one, else the name). when (YYYY-MM-DD) only when the lines date the event
itself. Anyone's words count for role, status, relationship, term and dependency, as long as the lines say it; a
decision, commitment or evaluation cites the line of the person who made it. Give its impact like an observation's
(moment, impact as the action, impact_object), domain and sensitive. Leave out news, announcements, bots' pushes,
other teams' business that does not touch the user, one-off actions ("merged it"), passing states ("the build is
red"), money notices (bank, payroll, balance, card, transfer) and anything that is only a name.

### observations (one concrete thing seen, for one facet)
Each observation is one thing seen in this unit, for one facet. Every observation has text: one sentence that stands
on its own, also when the facet's fields (situation, choice, pitfall…) are filled; the fields do not replace it. Fill
the fields the facet uses, leave the others empty.

Which facet, when two seem to fit:
- something the user asks of the AI, again and again or as a standing request ("list my calendars merged by time"),
  or corrects in it: interaction, not workflow;
- a choice between options, also a conditional one ("agrees to a cache when it carries no security risk"):
  judgement; workflow is the steps of a kind of task, not one choice;
- a view or an evaluation of a thing, a service or an option with no choice of the user's in it ("overseas
  accounts are harder to ban"): a world fact of kind evaluation, not a judgement;
- how a meeting, a timeline or a hand-off is arranged for a kind of task: workflow, not judgement;
- persona's type names what the line is (a preference is preference, not current_work); a technical choice is
  judgement, not persona.

**interaction** (how the user and the AI work together). From the user's own lines in a conversation where an AI
answers or is addressed: what the user wants the AI to do or not do when it speaks to them, asks them, reports to
them, takes on or hands out work, or should not disturb them; corrections of the AI; repeated asks; impatience; a way
of working the user approves of by name. type: the occasion (ask, report, dispatch, reply_style, interrupt,
correction, autonomy, verification, …, or a new word). situation: when it applies. choice: what the AI should do.
rejected: what the AI did that the user did not want, when the lines show it. why: the user's reason, when given.
durable: true when the user said it holds from now on ("always", "from now on", "以后", "每次", "别再", "一律") or was
plainly annoyed to say it again. A correction of one answer ("this table is wrong") is not an interaction entry; the
preference behind it may be.

**tools** (how the AI works with machines and tools). The object (a machine, tool, runtime, model, gateway, service),
how to use it (how to connect, which script, command, alias or entry point), what fails and how it was got around,
what it must not be used for. Sources: the user telling the AI about machines and tools; a failure in an assistant or
agent session followed by another way that worked; an alert that names a machine or service. type: machine, tool,
runtime, model, gateway, service, … object: its name. choice: the way to do it. pitfall: what fails. fix: how it was
got around. exception: what it must not be used for. Secrets: never write a password, token, key, connection string or
any credential into any field, not even partly; write only which script, alias, environment variable name or
secret store holds it.
An entry here is always something the AI does differently with the object: an approach, a pitfall with its way
round, or a boundary. A way to get round a service's content moderation, account checks, rate limits, anti-abuse or
security controls is never an approach or a fix, whoever used it: at most write the boundary (what not to trigger,
what to keep within), or nothing. A plain fact about a machine, tool or service (its state, its capacity, how much of something
was used, that it was upgraded) is not a tools observation: it is a world fact about that object, or nothing.

**judgement** (the user's default choice at a fork). From the user's own lines only. One fork and one choice: the
user weighed options, picked, rejected, delegated, stopped, widened or narrowed scope, set a bar, settled a conflict,
decided what to spend (time, money, risk) or what to accept from a person or an AI. type: the kind of fork in a few
words of the target language (open: name it from the data). situation: the kind of situation that called for a
choice, without one-off names. choice: what the user picked, specific enough to tell from the opposite choice.
rejected: what the user turned down. why: the user's reason in their words; when they gave none you may read it from
the same lines and set inferred true; never reach beyond the lines. exception: when the user chose otherwise. Not a
judgement: a plain request ("look this up"), an opinion on a topic, a question, a joke, a bare agreement, an attitude
with no choice in it ("is rigorous").

**workflow** (how the user handles a kind of task). From the user's own lines: how the user goes about a kind of
task: steps and their order, who does what, what they check, how they hand off, follow up or close. type: the kind of
task (open: name it from the data). situation: when the kind of task comes up. choice: how the user does it. why:
their reason when given. object: the project, when it is about one. Someone else's steps are not the user's workflow.

**people** (one line for the card of someone the user deals with). person: the known id (person:<id> or people/<id>)
or the name as written. Use everything about that person in the lines, not only what they said to the user:
- what the person said or wrote (their own lines: who equals that person);
- what the user said about them;
- what others said about them: set reported true.
part:
- fact: role, responsibility, relation to the user, projects, where they are, what they work on;
- user_eval: what the user thinks of the person, in the user's own words: praise, a complaint, whether they can be
  relied on, what they are good or bad at, how to deal with them, what to watch out for with them. Only from the
  user's lines (who "self"), said to anyone (to that person, to someone else, or to the AI). Go through every line
  of the user that names or points at a person and ask whether it says how the user sees them; short or casual
  remarks count ("he is quick", "don't wait on her for this", "they never reply"). Keep the wording close to the
  user's and quote the line. A request to the person, a thanks or a greeting is not an evaluation. A line that gives a
  judgement, a tool pitfall or anything else may at the same time say how the user sees a person: give the user_eval
  too, as an observation of its own;
- inferred: your reading of the person: how they collaborate, how fast they respond, common disagreements, way of
  speaking, reliability, temperament, what they care about. Set inferred true; it must rest on cited lines;
- commitment: a promise either way (commitment.from and commitment.to are "me" for the user, or the person), with
  what and due (YYYY-MM-DD) when known; cite the line of whoever promised.
type: the dimension of the card line (role, responsibility, collaboration, responsiveness, style, reliability,
interests, …). Never write a card line for the user (that is persona), and never for someone whose lines all have no
who.

**persona** (the user as a person). From the user's own lines, the user's "about me" notes, or the assistant's memory
file: role and responsibilities, abilities and weak spots (as the user puts them), situation and what they are pushing
forward now, goals, what they value, how they like to communicate, and in chit-chat their preferences, habits and
opinions. type: a short key (role, responsibility, ability, situation, current_work, goal, values, communication,
preference, opinion, habit, …). Only what the user says or does about themself; another person's view of the user is
not persona. Persona is about the person, not their surroundings: the state of a machine, a service or a project
they use is a world fact (or a tools observation when it changes how the AI uses it), never persona. A passing
interest seen once ("looked at X") stays a single observation of what the user did; do not word it as a lasting
trait.

### fields every observation has

- domain: work or life, judged by the topic, not the source.
- sensitive: emotion, finance, health or family when the content is in one of these categories, else empty. Label it
  honestly; a program decides what is kept.
- scope: global, or the project, runtime (an assistant or agent product), machine or tool it holds for.
- impact: what the assistant does differently for knowing it.
  - moment: one of every_turn (whenever the assistant speaks), ask_user (when it asks the user something),
    report (when it reports back), dispatch (when it sends work to an agent), task_start (when a kind of task starts),
    use_object (when it touches that machine or tool), reply_person (when it triages or drafts a reply to someone),
    kb_lookup (when someone asks about that person, project or thing).
  - action: the concrete thing the assistant will do or not do at that moment ("offers the two options with their
    trade-offs instead of picking one", "connects through the jump-host alias, not the default port"). Not
    "understands the user better", "keeps it in mind", "takes it into account" or anything that would be true of any
    entry. If you cannot name an action, leave the observation out.
  - object: the person, project, machine, tool or kind of task it is for. Required for dispatch, task_start,
    use_object, reply_person and kb_lookup.
  - Typical moments: interaction every_turn, ask_user or report; tools use_object or task_start; judgement ask_user or
    dispatch; workflow task_start or dispatch; people reply_person or kb_lookup (never every_turn for user_eval or
    inferred); persona every_turn or reply_person.
- evidence: the lines it rests on (see Rules).
- confidence: high when a line states it plainly, medium when implied, low when a guess (prefer leaving it out).

### mentions and assertions (only when graph is true)
mentions: every name of a person, organization, project, system, document, decision, task or resource, with the
line it is in (a known id when it is clearly that entity). assertions: typed relations between them, only with the
relation types given and the subject and object types they allow: who owns, belongs to, reports to, depends on or
waits for what, who decides, who was assigned or promised what, how two people are connected (in a group, two people
the user deals with may relate to each other: say so). Not "mentions", "was in the same meeting", "links to".
Go through the people who speak or are named in the lines one by one and ask, for each relation type given, whether
the lines say it: whom they report to or work for, whom they bring up, what they are responsible for, which project or
team they belong to, which decisions are theirs to make. Words such as "my manager", "his lead", "the owner of X",
"X decides", "sign-off from", "hand over to" are relations; say them when the lines do. A form of address or a
pleasantry ("thanks, boss", "master", "sir") is not a relation.

## What a unit may give

- origin self_report (the user's own notes): interaction, tools and persona only. Never judgement or workflow: what
  people say about how they choose is not how they choose.
- origin handwritten (the assistant's memory file): any facet; quote the file's lines.
- tier own or absent: everything. tier engaged (a group the user takes part in, a mention, a document): everything;
  judgement, workflow, persona and the user's asks only from the user's own lines. tier code (a pull request or issue
  thread): tools, judgement, workflow and interaction from the user's own lines; no facts. tier background (a group the
  user keeps quiet in): only people observations, mentions and assertions; the user's own lines may still give persona.

## Rules (checked by a program; anything that breaks one is thrown away)

- Every item cites at least one line: n is the line number, quote copies at least 4 characters of that line exactly
  ("……" may skip words within the same line). Quote the words that carry the point, not just a name.
- An item says no more than its quotes: every name, number, time and description in it is in a line it cites. When a
  detail comes from another line, cite that line too; a detail no line says (a type of hardware, a project name, a
  cause) is left out, not guessed.
- Read what a line asserts, not what it presupposes or asks: a question, a wish, a complaint or a guess is not a
  statement that something is so ("why is there no model X here?" does not say the speaker uses X; "can this run on
  the NAS?" does not say it runs there).
- interaction, judgement, workflow and persona rest on the user's own lines (speaker "我"): at least one quote is the
  user saying something of their own. A bare agreement ("好", "可以", "ok") with what someone else proposed does not
  count: the proposal is theirs. Words that appear only in the AI's text are never the user's.
- A people line rests on lines whose speaker is known (who set), or on the user's lines; never on a line with no who.
- A claim about what someone decided or promised cites that person's own line.
- Text an agent wrote (speaker "我·代写", "Generated with", "Co-Authored-By"), pasted logs and automated messages are
  context, not anyone's position.
- One-off arrangements are not habits; a question is not a position; a request with no choice in it is not a
  judgement.
- Money notices (bank, payroll, balance, card, transfer, payment) are never items, whoever wrote them.
- Never put a secret (password, token, API key, private key, connection string with a password) into any field.
- At most 12 observations and 8 facts for a unit; prefer fewer, stronger ones. Cover every facet the lines support;
  do not spend the whole budget on one facet.

Answer with JSON only, matching the schema.
