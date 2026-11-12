import { test } from "node:test";
import assert from "node:assert/strict";
import { answerMarkdown, answersPath, decodeEntities, parseAnswers, parseQuestions, queryText, questionID, ReadError, searchPath, selectedAnswer, siteID, tagText } from "../src/model.ts";
import { encodeState, initialState, questionTransition, readState, transition } from "../src/page.tsx";
import { answer, question, questionPath } from "./fixtures.ts";

test("bounded public search and answer paths validate all user-controlled fields", () => {
  const path = searchPath(" TypeScript & JSX ", "stackoverflow", "c#", 12);
  const url = new URL("https://api.stackexchange.com" + path);
  assert.equal(url.searchParams.get("q"), "TypeScript & JSX");
  assert.equal(url.searchParams.get("tagged"), "c#");
  assert.equal(url.searchParams.get("filter"), "!*10eWkBRxImNF3.k5tm)KG1");
  assert.equal(new URL("https://api.stackexchange.com" + answersPath(70794241, "unix")).searchParams.get("site"), "unix");
  for (const value of ["", "x\n", 42, "x".repeat(201)]) assert.throws(() => queryText(value), ReadError);
  for (const value of ["__proto__", "meta", "stackoverflow.com", 42]) assert.throws(() => siteID(value), ReadError);
  for (const value of ["typescript;foo", "c++ ", "x".repeat(61), "-tag"]) assert.throws(() => tagText(value), ReadError);
  for (const value of [0, -1, 1.5, 1e20, "1\n", {}]) assert.throws(() => questionID(value), ReadError);
  assert.equal(questionPath.includes("access_token"), false);
});

test("question parsing decodes entities and keeps only validated same-site links", () => {
  const result = parseQuestions({ items: [{ ...question, title: "C&amp;C &quot;generals&quot;", owner: { display_name: "A &lt;B&gt;" } }], quota_remaining: 17 }, "stackoverflow", 123);
  assert.equal(result.questions[0].title, 'C&C "generals"');
  assert.equal(result.questions[0].author, "A <B>");
  assert.equal(result.quotaRemaining, 17);
  assert.equal(result.at, 123);
  for (const change of [
    { link: "https://attacker.invalid/questions/70794241" },
    { link: "https://stackoverflow.com.evil.invalid/questions/70794241" },
    { question_id: -1 },
    { tags: ["typescript", "typescript"] },
    { content_license: "public domain" },
    { owner: { display_name: "Name", link: "https://attacker.invalid/users/123" } }
  ]) assert.throws(() => parseQuestions({ items: [{ ...question, ...change }] }, "stackoverflow", 0), ReadError);
  assert.throws(() => parseQuestions({ items: [question, question] }, "stackoverflow", 0), ReadError);
});

test("answer HTML becomes safe Markdown with code, entities and HTTPS references", () => {
  const text = answerMarkdown(answer.body);
  assert.match(text, /Use `<T>` & a spread/);
  assert.match(text, /```\nconst value = \{ \.\.\.input \};\nconsole\.log\(value\);\n```/);
  assert.match(text, /\[TypeScript docs\]\(https:\/\/www\.typescriptlang\.org\/docs\/\) > guide/);
  assert.doesNotMatch(text, /<(?:script|pre|code|a)\b/i);
  assert.equal(decodeEntities("&#x1F600; &#65; &unknown;"), "😀 A &unknown;");
  assert.match(answerMarkdown('<script>alert(1)</script><p>Safe</p>'), /Safe/);
  assert.doesNotMatch(answerMarkdown('<script>alert(1)</script><p>Safe</p>'), /alert/);
  assert.equal(answerMarkdown('<a href="javascript:alert(1)">label</a>'), "label");
  const code = answerMarkdown('<pre><code>```\n[bad](javascript:alert(1))\n```</code></pre>');
  assert.match(code, /^````\n```\n\[bad\]\(javascript:alert\(1\)\)\n```\n````$/);
  assert.ok(code.startsWith("````\n") && code.endsWith("\n````"), "a longer fence keeps code literal");
  assert.doesNotMatch(answerMarkdown('<a href="https://example.com"><strong>nested</strong></a>'), /<a|<strong/);
  assert.equal(answerMarkdown('<a href="https://example.com">[x](bad)</a>'), '[\\[x](bad)](https://example.com)');
});

test("accepted answer wins, then highest votes; empty answers are distinct from API errors", () => {
  const parsed = parseAnswers({ items: [answer], quota_remaining: 4 }, 70794241, "stackoverflow", 100);
  assert.equal(selectedAnswer(parsed.answers).accepted, true);
  const alternative = { ...answer, answer_id: 70794815, is_accepted: false, score: 99, link: "https://stackoverflow.com/questions/70794241/optional-properties/70794815#70794815" };
  assert.equal(selectedAnswer(parseAnswers({ items: [alternative] }, 70794241, "stackoverflow", 0).answers).accepted, false);
  assert.throws(() => selectedAnswer([]), (error: unknown) => error instanceof ReadError && error.code === "not_found");
  assert.equal(parseAnswers({ items: [{ ...answer, owner: null }] }, 70794241, "stackoverflow", 0).answers[0].author, "Anonymous");
  for (const change of [{ question_id: 1 }, { body: "" }, { answer_id: 0 }, { is_accepted: "yes" }]) {
    assert.throws(() => parseAnswers({ items: [{ ...answer, ...change }] }, 70794241, "stackoverflow", 0), ReadError);
  }
  assert.throws(() => parseAnswers({ items: [answer, answer] }, 70794241, "stackoverflow", 0), ReadError);
});

test("page state and question actions reject forged IDs, hosts and fields", () => {
  const state = { q: "typescript", site: "stackoverflow" as const, tag: "typescript", found: true };
  const results = parseQuestions({ items: [question] }, "stackoverflow", 0);
  assert.equal(questionTransition(state, results, "question:70794241").question?.id, 70794241);
  assert.equal(questionTransition(state, results, "question:1"), state);
  assert.equal(transition(state, "question:70794241"), state);
  const detail = questionTransition(state, results, "question:70794241");
  assert.deepEqual(readState(encodeState(detail)), detail);
  assert.deepEqual(readState(encodeState({ ...detail, question: { ...detail.question!, url: "https://attacker.invalid/questions/70794241" } })), initialState);
  assert.deepEqual(readState(encodeState({ ...detail, site: "unix" })), initialState);
  assert.deepEqual(readState('{"q":"bad\\n"}'), initialState);
  assert.equal(transition(state, "search", { query: "typescript", site: "__proto__" }), state);
  assert.equal(transition(state, "search", { query: "typescript", site: "unix", tag: "bash" }).site, "unix");
});
