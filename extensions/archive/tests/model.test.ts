import { test } from "node:test";
import assert from "node:assert/strict";
import { configFrom, parseItems, queryText, ReadError, searchPath } from "../src/model.ts";
import { response } from "./fixtures.ts";
import { encodeState, readState, transition } from "../src/page.tsx";

test("configuration and media filters are bounded", () => {
  assert.deepEqual(configFrom(undefined), { media: "all" });
  assert.equal(configFrom({ media: "texts" }).media, "texts");
  for (const value of [{ media: "everything" }, { media: 1 }, [], "texts"]) assert.throws(() => configFrom(value), ReadError);
  assert.match(searchPath("Pride and Prejudice", "texts"), /AND\+mediatype%3Atexts/);
  assert.ok(!new URL("https://archive.org" + searchPath("Pride and Prejudice", "all")).searchParams.get("q")!.includes("mediatype"));
  assert.throws(() => searchPath("valid", "all", 0), ReadError);
  assert.throws(() => searchPath("valid", "all", 21), ReadError);
});

test("catalog items retain metadata and only canonical archive links", () => {
  const result = parseItems(response, 1000);
  assert.equal(result.items[0].title, "Pride and Prejudice");
  assert.equal(result.items[0].year, 1813);
  assert.equal(result.items[0].url, "https://archive.org/details/prideprejudice_0000");
  assert.equal(result.items[1].media, "audio");
  assert.equal(result.items[1].downloads, 42);
  assert.equal(parseItems({ response: { numFound: 1, docs: [{ ...response.response.docs[0], description: ["First paragraph", "Second paragraph"] }] } }, 0).items[0].description, "First paragraph · Second paragraph");
  const original = response.response.docs[0];
  assert.throws(() => parseItems({ response: { numFound: 1, docs: [{ ...original, identifier: "https://attacker.invalid" }] } }, 0), ReadError);
  assert.throws(() => parseItems({ response: { numFound: 1, docs: [{ ...original, mediatype: "unknown" }] } }, 0), ReadError);
  assert.throws(() => parseItems({ response: { numFound: 1, docs: [original, original] } }, 0), ReadError);
});

test("query validation and state preserve only valid user input", () => {
  for (const query of ["", " ", "x".repeat(161), "bad\n"]) assert.throws(() => queryText(query), ReadError);
  assert.equal(queryText(" Jane Austen "), "Jane Austen");
  const initial = readState(undefined);
  const found = transition(initial, "search", '{"query":"Jane Austen"}');
  assert.equal(found.found, true);
  assert.equal(readState(encodeState(found)).q, "Jane Austen");
  for (const values of ["Jane", "not-json", "null", "[]", 1, { query: ["Jane"] }]) assert.equal(transition(initial, "search", values).found, false);
  assert.equal(transition(found, "home").found, false);
});
