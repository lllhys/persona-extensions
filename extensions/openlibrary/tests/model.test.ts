import { test } from "node:test";
import assert from "node:assert/strict";
import { parseBook, parseSearch, parseWork, ReadError, searchPath, workPath, workKey } from "../src/model.ts";
import { detail, publicBook, sample } from "./fixtures.ts";
import { encodeState, readState, transition } from "../src/page.tsx";

test("search and work paths validate identifiers and query bounds", () => {
  assert.equal(workKey("OL59800W"), "OL59800W");
  assert.equal(workPath("OL59800W"), "/works/OL59800W.json");
  for (const key of ["https://attacker.invalid", "OL1W/../../x", "OL0W", "", "OL1A"]) assert.throws(() => workPath(key), ReadError);
  for (const query of ["", " ", "x".repeat(201), "bad\n"]) assert.throws(() => searchPath(query), ReadError);
  assert.match(searchPath("Pride and Prejudice"), /^\/search\.json\?q=Pride\+and\+Prejudice&limit=20&fields=/);
  assert.throws(() => searchPath("valid", 0), ReadError);
  assert.throws(() => searchPath("valid", 31), ReadError);
});

test("book metadata produces only canonical catalog and validated public edition links", () => {
  const restricted = parseBook(sample);
  assert.equal(restricted.url, "https://openlibrary.org/works/OL59800W");
  assert.equal(restricted.readingURL, undefined);
  assert.equal(restricted.access, "printdisabled");
  const open = parseBook(publicBook);
  assert.equal(open.readingURL, "https://openlibrary.org/books/ia:synapseml_gutenberg_pride_and_prejudice_by_jane_austen");
  assert.equal(parseBook({ ...publicBook, ia: ["https://attacker.invalid"] }).readingURL, undefined);
  assert.equal(parseBook({ ...publicBook, ia: ["javascript:alert(1)"] }).readingURL, undefined);
  assert.equal(parseBook({ ...publicBook, public_scan_b: false }).readingURL, undefined);
  const result = parseSearch({ numFound: 2, docs: [sample, sample, publicBook] }, 1000);
  assert.equal(result.books.length, 2);
  const work = parseWork(detail, restricted, 1000);
  assert.equal(work.description, "A synthetic description of a novel.");
  assert.deepEqual(work.subjects, ["Science fiction", "Human nature"]);
  assert.equal(parseWork({ ...detail, description: "<script>alert(1)</script>" }, restricted, 1).description, "<script>alert(1)</script>");
  assert.throws(() => parseWork({ ...detail, title: "Different book" }, restricted, 1), ReadError);
  assert.throws(() => parseWork({ ...detail, key: "/works/OL66554W" }, restricted, 1), ReadError);
});

test("service responses cannot substitute URLs, types or excessive metadata", () => {
  for (const raw of [null, [], {}, { numFound: 1 }, { numFound: 1, docs: [null] },
    { numFound: 1, docs: [{ ...sample, title: null }] }, { numFound: -1, docs: [] }]) assert.throws(() => parseSearch(raw, 0), ReadError);
  assert.throws(() => parseWork({ description: { type: "/type/html", value: "unsafe" } }, parseBook(sample), 0), ReadError);
});

test("state round-trip preserves only validated server-owned book navigation", () => {
  const initial = readState(undefined);
  assert.equal(initial.v, "discover");
  const searched = transition(initial, "search", '{"query":"Pride and Prejudice"}');
  assert.equal(searched.v, "search");
  const selected = parseBook(publicBook);
  const work = transition(searched, `work:${selected.key}`);
  const withBook = { ...work, b: selected };
  const opened = transition(withBook, `work:${selected.key}`);
  assert.equal(opened.v, "work");
  const state = readState(encodeState(opened));
  assert.equal(state.b?.url, selected.url);
  assert.equal(transition(opened, "back").v, "search");
  for (const values of ["Earth", "not-json", "null", "[]", 1, { query: ["Earth"] }]) assert.equal(transition(initial, "search", values).v, "discover");
  const bad = { ...opened, b: { ...selected, url: "https://attacker.invalid" } };
  assert.equal(readState(JSON.stringify(bad)).v, "discover");
  const external = { ...opened, b: { ...selected, readingURL: "https://attacker.invalid/x" } };
  assert.equal(readState(JSON.stringify(external)).v, "discover");
});
