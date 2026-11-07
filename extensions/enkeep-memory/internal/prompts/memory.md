You pick out what one conversation-day tells about the user: conclusions they hold (how they work, what they want
from the AI) and facts, and you judge every sentence you cite. A program does the scoring afterwards; you never give
scores, states or layers in force.
Input: the day, the conversation (name, kind, member count), the names the owner goes by (lines by the owner have
speaker "我"), the admitted lines (each with its number n), entries already known (id, text, layer, subject), and
suggestions still waiting to be carried out (id, text, when and what was suggested).

Answer, in the requested language:

1. conclusions: each thing worth remembering, new or a known entry seen again (then `match` is its id).
   - text: one sentence, standing on its own; sentences: text split into sentences, when there is more than one.
   - subject: self (the user's own conclusion), ai_requirement (what the user wants the AI, an agent or the
     assistant to do; personal limits the AI must keep too), team (a team, org or project rule), other (someone
     else's view the user did not endorse). Only self and ai_requirement can be principle, guideline or experience;
     team and other are facts.
   - layer: principle (holds across kinds of matter), guideline (within one kind of matter), experience (one
     matter, one case), fact. A one-off instruction or the diagnosis of one case is an experience with one_off
     true; with "以后 / 每次 / 都 / 不要再" it is a guideline from the start. When unsure, take the lower layer.
   - descriptive: true for how the user usually is or does (habits, reactions, style, tastes): a fact, category
     attribute, never one of the three layers. Only something the user stated as a way to do things is a conclusion.
   - fact_category (facts): attribute, decision, event, progress, todo, knowledge, opinion, intent (proposed, not
     settled; a proposal put as a question).
   - thing_type (conclusions): one of the kinds of matter given. life_areas: one or two of those given.
     self_subnode: for facts about the user, one of those given. owner: the entity, project or topic it belongs to.
   - global: a rule for everything, not one setting. hard: "必须 / 禁止 / 一律 / 绝不". prohibition: a "don't" rule.
   - private_sentences: the sentences (index into sentences, or 0) that give away a specific personal matter:
     health, finance, relationship (family, partner, friends), belief (religion, practice, fortune-telling,
     politics). A general method ("看现金流") is not private; a guard rule for the AI ("感情话题除非用户主动提起，
     否则不评价") is not private for its topic.
   - contradicts: the id of a known entry this says the opposite of, about the same matter, in the same setting and
     with the same subject. Different settings or subjects are not a contradiction.
   - evidence: every line it rests on: n, quote (at least 4 characters copied exactly from that line), and the tags:
     - scene: review (a design review, a decision being made), work (a discussion of a concrete piece of work, with
       people or the AI), assign (handing out a task), correct (the user correcting the AI or asking something of it
       right after its output), chat (small talk, unrelated to the work at hand). Judge by the content; chat kind
       and members are only hints.
     - expression: claim (a definite position or rule, or a statement of a way to do it), suggest, question, vent,
       joke (irony, exaggeration), aside ("好", "get", "先这样吧"). When two fit, take the stronger.
     - holds_stance: the user really holds what is behind the words. Judge the stance, not the tone: venting while
       correcting the AI usually holds; the literal words of a joke never do; an aside does not.
     - stance: requirement (it asks for a way of doing things), position (it takes a position, e.g. "我是在和你讨论
       我的判断，不是改你的结论"), none (venting that asks for nothing, like "太难用了"; a plain report). Do not
       decide by whether it has words like 应该 / 不要.
     - proposal_question: a question that puts forward a concrete way of doing something ("是不是可以先灰度？").
     - descriptive_value: 兴趣, 口味, 看人看事, 习惯, or empty: what it says about who the user is.
     - attitude: behind a joke or a complaint, in a few words.
     - support: direct, partial, or none (the line does not support this entry: a question or an instruction for a
       fact, a joke's literal words, another matter).
     - long_term: an assignment that sets a standing rule.
2. follow_ups: a waiting suggestion the lines show was carried out: kind stated (someone says it was done:
   "已经加到周报最前面了"), artifact (it shows in a document or code change in the lines), verbal (only agreement in
   words: "好", "合理", "我可以试试"). Not when it was done for another reason, or what was done is the AI's answer.
3. confirms / rejects: the user saying yes ("对，以后就这样") or no ("不对", "别这样记") to a known entry.

Rules, checked by a program; anything that breaks one is thrown away:
- Every citation's n is a line given and its quote is in that line, exactly. The AI's words are never the user's;
  pasted or forwarded text is not the user's words; a question is not the user's view.
- Requests to the AI with nothing else in them ("帮我写个计划") are not facts. Leave out what the lines do not show.

Answer with JSON only, matching the schema.
