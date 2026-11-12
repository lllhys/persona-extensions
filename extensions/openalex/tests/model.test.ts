import { test } from "node:test";
import assert from "node:assert/strict";
import { parseWorks, queryText, ReadError, searchPath } from "../src/model.ts";
import { response } from "./fixtures.ts";
import { encodeState, readState, transition } from "../src/page.tsx";

test("research metadata retains citation, open access and canonical work links", () => {
  const result = parseWorks(response, 1000);
  assert.equal(result.works[0].title, "A Survey of Large Language Models");
  assert.equal(result.works[0].citations, 8095);
  assert.equal(result.works[0].openAccess, true);
  assert.equal(result.works[0].id, "https://openalex.org/W4362515116");
  assert.equal(result.works[0].license, "cc-by");
  assert.equal(result.works[1].oaURL, undefined);
  assert.equal(result.works[1].openAccess, false);
  const original = response.results[0];
  assert.throws(() => parseWorks({ meta: { count: 1 }, results: [{ ...original, id: "https://attacker.invalid/W1" }] }, 0), ReadError);
  assert.throws(() => parseWorks({ meta: { count: 1 }, results: [{ ...original, doi: "http://attacker.invalid" }] }, 0), ReadError);
  assert.throws(() => parseWorks({ meta: { count: 1 }, results: [original, original] }, 0), ReadError);
  // A publisher's plain-http open-access link (the first CRISPR result's is http://www.cell.com/…) is left out, not a failure.
  const cell = { ...original, open_access: { ...original.open_access, oa_url: "http://www.cell.com/article/S0092867415017055/pdf" } };
  const listed = parseWorks({ meta: { count: 2 }, results: [cell, response.results[1]] }, 0);
  assert.equal(listed.works.length, 2);
  assert.equal(listed.works[0].oaURL, undefined);
  assert.equal(listed.works[0].doi, original.doi);
  for (const oa_url of ["https://user:secret@example.com/paper.pdf", "not a url", "ftp://example.com/paper.pdf"]) {
    assert.throws(() => parseWorks({ meta: { count: 1 }, results: [{ ...original, open_access: { ...original.open_access, oa_url } }] }, 0), ReadError);
  }
});

test("queries and results are bounded and control characters are rejected", () => {
  for (const query of ["", " ", "x".repeat(201), "bad\n"]) assert.throws(() => queryText(query), ReadError);
  assert.equal(queryText(" large language models "), "large language models");
  assert.throws(() => searchPath("valid", 0), ReadError);
  assert.throws(() => searchPath("valid", 21), ReadError);
});

test("state and legacy callback values preserve validated research searches", () => {
  const initial = readState(undefined);
  const found = transition(initial, "search", '{"query":"large language models"}');
  assert.equal(found.found, true);
  assert.equal(readState(encodeState(found)).q, "large language models");
  for (const values of ["models", "not-json", "null", "[]", 1, { query: ["models"] }]) assert.equal(transition(initial, "search", values).found, false);
  assert.equal(transition(found, "home").found, false);
});
