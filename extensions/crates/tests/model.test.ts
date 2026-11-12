import { test } from "node:test";
import assert from "node:assert/strict";
import { crateName, cratePath, parseCrate, parseSearch, queryText, ReadError, searchPath } from "../src/model.ts";
import { crateTransition, encodeState, initialState, readState, transition } from "../src/page.tsx";
import { detailResponse, other, serde } from "./fixtures.ts";

test("official JSON requests bound queries and crate names", () => {
  const url = new URL("https://crates.io" + searchPath("serde json", 12));
  assert.equal(url.searchParams.get("q"), "serde json");
  assert.equal(url.searchParams.get("per_page"), "12");
  assert.equal(cratePath("serde"), "/api/v1/crates/serde");
  for (const value of ["", "a/b", "serde json", "a\n", "../secret", 42]) assert.throws(() => crateName(value), ReadError);
  for (const value of ["", "x\n", 42, "x".repeat(101)]) assert.throws(() => queryText(value), ReadError);
});

test("search keeps bounded metadata and rejects duplicate or invalid crates", () => {
  const result = parseSearch({ crates: [serde], meta: { total: 1 } }, 123);
  assert.equal(result.crates[0].name, "serde");
  assert.equal(result.crates[0].version, "1.0.229");
  assert.equal(result.crates[0].recentDownloads, 420047065);
  assert.equal(result.crates[0].repository, "https://github.com/serde-rs/serde");
  assert.equal(result.crates[0].license, "See details");
  for (const change of [{ name: "bad/name" }, { recent_downloads: -1 }, { default_version: "1.0.0\n" }]) {
    assert.throws(() => parseSearch({ crates: [{ ...serde, ...change }], meta: { total: 1 } }, 0), ReadError);
  }
  assert.throws(() => parseSearch({ crates: [serde, serde], meta: { total: 2 } }, 0), ReadError);
  // Older crates still link http (nickel: http://nickel-org.github.io/, objc: http://github.com/SSheldon/rust-objc).
  const withHttp = { ...serde, repository: "http://github.com/SSheldon/rust-objc", homepage: "http://nickel-org.github.io/", documentation: "http://ssheldon.github.io/rust-objc/objc/" };
  const parsed = parseSearch({ crates: [withHttp], meta: { total: 1 } }, 0);
  assert.equal(parsed.crates[0].repository, undefined);
  assert.equal(parsed.crates[0].homepage, undefined);
  assert.equal(parsed.crates[0].documentation, undefined);
  assert.equal(parsed.crates[0].url, "https://crates.io/crates/serde");
});

test("default version selects exactly one matching release with its license and yanked state", () => {
  const result = parseCrate(detailResponse, 0);
  assert.equal(result.install, "cargo add serde@1.0.229");
  assert.equal(result.license, "MIT OR Apache-2.0");
  assert.equal(result.yanked, false);
  assert.equal(result.versionDownloads, 236925653);
  assert.equal(result.published, "2026-07-18T23:05:13.266456Z");
});

test("missing default release, mismatched crate and yanked versions are explicit", () => {
  assert.throws(() => parseCrate({ ...detailResponse, versions: [detailResponse.versions[1]] }, 0), ReadError);
  assert.throws(() => parseCrate({ ...detailResponse, versions: [{ ...detailResponse.versions[0], crate: "other" }] }, 0), ReadError);
  const yanked = parseCrate({ ...detailResponse, versions: [{ ...detailResponse.versions[0], yanked: true }] }, 0);
  assert.equal(yanked.yanked, true);
  assert.throws(() => parseCrate({ ...detailResponse, versions: [{ ...detailResponse.versions[0], license: ["MIT"] }] }, 0), ReadError);
});

test("page state accepts only validated search results and HTTPS metadata", () => {
  const state = { q: "serde", found: true };
  const result = parseSearch({ crates: [serde, other], meta: { total: 2 } }, 0);
  const detail = crateTransition(state, result, "crate:serde-saphyr");
  assert.equal(detail.crate?.name, "serde-saphyr");
  assert.equal(crateTransition(state, result, "crate:other"), state);
  assert.equal(transition(state, "crate:serde"), state);
  assert.deepEqual(readState(encodeState(detail)), detail);
  assert.deepEqual(readState(encodeState({ ...detail, crate: { ...detail.crate!, repository: "javascript:alert(1)" } })), initialState);
  assert.equal(transition(state, "search", { query: "serde" }).found, true);
});
