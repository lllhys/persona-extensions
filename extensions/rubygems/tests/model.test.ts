import { test } from "node:test";
import assert from "node:assert/strict";
import { gemName, gemPath, parseGem, parseSearch, queryText, ReadError, searchPath } from "../src/model.ts";
import { gemTransition, encodeState, initialState, readState, transition } from "../src/page.tsx";
import { detailResponse, rack, rails } from "./fixtures.ts";

test("official JSON requests bound queries and gem names", () => {
  const url = new URL("https://rubygems.org" + searchPath("ruby rails"));
  assert.equal(url.searchParams.get("query"), "ruby rails");
  assert.equal(gemPath("rails"), "/api/v1/gems/rails.json");
  for (const value of ["", "a/b", "ruby rails", "a\n", "../secret", 42]) assert.throws(() => gemName(value), ReadError);
  for (const value of ["", "x\n", 42, "x".repeat(101)]) assert.throws(() => queryText(value), ReadError);
});

test("search keeps bounded metadata and rejects duplicate or invalid gems", () => {
  const result = parseSearch([rails, rack], 123);
  assert.equal(result.gems[0].name, "rails");
  assert.equal(result.gems[0].version, "8.1.4.1");
  assert.equal(result.gems[0].totalDownloads, 815065467);
  assert.equal(result.gems[0].repository, "https://github.com/rails/rails/tree/v8.1.4.1");
  assert.equal(result.gems[1].repository, undefined, "an optional HTTP link is omitted, not upgraded");
  for (const change of [{ name: "bad/name" }, { project_uri: "https://attacker.invalid/gem" }, { downloads: -1 }, { version: "1.0.0\n" }, { licenses: [42] }]) {
    assert.throws(() => parseSearch([{ ...rails, ...change }], 0), ReadError);
  }
  assert.throws(() => parseSearch([rails, rails], 0), ReadError);
  assert.equal(parseSearch([{ ...rails, source_code_uri: "javascript:alert(1)" }], 0).gems[0].repository, undefined);
});

test("current version carries its license, release date, yanked state and dependencies", () => {
  const result = parseGem(detailResponse, 0);
  assert.equal(result.install, "gem install rails -v 8.1.4.1");
  assert.equal(result.license, "MIT");
  assert.equal(result.yanked, false);
  assert.equal(result.versionDownloads, 1388621);
  assert.equal(result.published, "2026-11-04T02:47:52.512Z");
  assert.deepEqual(result.dependencies[0], { name: "actionpack", requirements: "= 8.1.4.1" });
});

test("invalid dependency and missing current metadata fail closed", () => {
  assert.throws(() => parseGem({ ...detailResponse, dependencies: { runtime: [{ name: "other" }] } }, 0), ReadError);
  assert.throws(() => parseGem({ ...detailResponse, dependencies: { runtime: [{ name: "../secret", requirements: ">= 1" }] } }, 0), ReadError);
  assert.throws(() => parseGem({ ...detailResponse, project_uri: "https://rubygems.org/gems/other" }, 0), ReadError);
  const yanked = parseGem({ ...detailResponse, yanked: true }, 0);
  assert.equal(yanked.yanked, true);
  assert.equal(parseGem({ ...detailResponse, licenses: [] }, 0).license, "Unspecified");
});

test("page state accepts only validated search results and HTTPS metadata", () => {
  const state = { q: "rails", found: true };
  const result = parseSearch([rails, rack], 0);
  const detail = gemTransition(state, result, "gem:rack");
  assert.equal(detail.gem?.name, "rack");
  assert.equal(gemTransition(state, result, "gem:other"), state);
  assert.equal(transition(state, "gem:rails"), state);
  assert.deepEqual(readState(encodeState(detail)), detail);
  assert.deepEqual(readState(encodeState({ ...detail, gem: { ...detail.gem!, repository: "javascript:alert(1)" } })), initialState);
  assert.equal(transition(state, "search", { query: "rails" }).found, true);
});
