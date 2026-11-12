import { test } from "node:test";
import assert from "node:assert/strict";
import { configFrom, definitionPath, entryURL, languageFor, parseEntry, plain, ReadError, word } from "../src/model.ts";
import { definitions } from "./fixtures.ts";
import { encodeState, readState, transition } from "../src/page.tsx";

test("configuration and language selection are bounded", () => {
  assert.deepEqual(configFrom(undefined), {}); assert.deepEqual(configFrom({ language: "auto" }), { language: "auto" });
  assert.equal(languageFor({}, "ja-JP"), "ja"); assert.equal(languageFor({}, "zh-Hans"), "zh"); assert.equal(languageFor({}, "en-US"), "en");
  assert.equal(languageFor({ language: "en" }, "ja"), "en");
  for (const value of [{ language: "fr" }, { language: 1 }, [], "en"]) assert.throws(() => configFrom(value), ReadError);
});

test("definition parser retains parts of speech and plain-text examples", () => {
  const entry = parseEntry(definitions, "en", "hello", 1000);
  assert.equal(entry.url, "https://en.wiktionary.org/wiki/hello");
  assert.equal(entry.groups[0].partOfSpeech, "Interjection");
  assert.equal(entry.groups[0].definitions[0].text, "A greeting said when meeting someone.");
  assert.equal(entry.groups[0].definitions[0].examples[0].text, "Hello, everyone.");
  assert.equal(entry.groups[1].partOfSpeech, "Noun");
  assert.ok(!entry.url.includes("attacker"));
  assert.throws(() => parseEntry({}, "en", "missing", 0), ReadError);
});

test("an empty or markup-only definition line is skipped, not the word", () => {
  const raw = { en: [
    { partOfSpeech: "Noun", language: "English", definitions: [{ definition: "" }, { definition: "<span class=\"usage-label-sense\"></span> " }, { definition: "A clear liquid." }] },
    { partOfSpeech: "Verb", language: "English", definitions: [{ definition: "" }] },
  ] };
  const entry = parseEntry(raw, "en", "water", 0);
  assert.deepEqual(entry.groups.map(group => [group.partOfSpeech, group.definitions.map(definition => definition.text)]), [["Noun", ["A clear liquid."]]]);
  assert.throws(() => parseEntry({ en: [raw.en[1]] }, "en", "water", 0), (error: unknown) => error instanceof ReadError && error.code === "not_found");
  assert.throws(() => parseEntry({ en: [{ partOfSpeech: "Noun", definitions: [{ definition: 1 }] }] }, "en", "water", 0), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
});

test("HTML snippets stay plain data with bounded entities", () => {
  assert.equal(plain(" <b>Hello</b>&nbsp; world &unknown; &#x1F600; "), "Hello world &unknown; 😀");
  assert.equal(plain("unterminated <tag"), "unterminated");
  assert.equal(plain("&#0; &#xD800;"), "� �");
});

test("words cannot select another URL authority", () => {
  assert.equal(word("hello world"), "hello world");
  assert.equal(entryURL("en", "hello world"), "https://en.wiktionary.org/wiki/hello_world");
  for (const value of ["", " ", "a#fragment", "a?query", "a<b", "x".repeat(121), "bad\n", "../outside", "bad\\path"]) assert.throws(() => definitionPath(value), ReadError);
  assert.equal(entryURL("en", "https://evil.invalid"), "https://en.wiktionary.org/wiki/https://evil.invalid");
});

test("state and legacy callback values preserve validated queries", () => {
  const initial = readState(undefined);
  const found = transition(initial, "lookup", '{"word":"hello"}');
  assert.equal(found.found, true);
  assert.equal(readState(encodeState(found)).q, "hello");
  for (const values of ["hello", "not-json", "null", "[]", 1, { word: ["hello"] }]) assert.equal(transition(initial, "lookup", values).found, false);
  assert.equal(transition(found, "home").found, false);
});
