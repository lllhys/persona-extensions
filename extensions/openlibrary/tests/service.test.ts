import { test } from "node:test";
import assert from "node:assert/strict";
import { budget, OpenLibrary, ReadError } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { Fixture, sample, until } from "./fixtures.ts";
const make = (fixture = new Fixture(), options = {}) => new OpenLibrary(fixture.fetch, {}, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 }, locale: "en", ...options });

test("search and work details share bounded public requests and cached responses", async () => {
  const fixture = new Fixture(), service = make(fixture);
  const result = await service.search("synthetic");
  assert.equal(result.books[0].title, sample.title);
  const work = await service.work("OL59800W", result.books[0]);
  assert.equal(work.description, "A synthetic description of a novel.");
  await service.search("synthetic"); await service.work("OL59800W", result.books[0]);
  assert.equal(fixture.hits.get("/search.json?q=synthetic&limit=20&fields=key%2Ctitle%2Cauthor_name%2Cfirst_publish_year%2Cedition_count%2Cebook_access%2Cpublic_scan_b%2Cia"), 1);
  assert.equal(fixture.hits.get("/works/OL59800W.json"), 1);
  assert.equal(fixture.requests[0].init.redirect, "error");
  assert.equal(fixture.requests[0].init.headers && (fixture.requests[0].init.headers as Record<string, string>).Authorization, undefined);
  assert.equal(service.status().state, "ok"); assert.deepEqual(service.cacheStats().entries, 2);
});

test("failed refresh returns a stale validated answer instead of erasing it", async () => {
  const fixture = new Fixture(), service = make(fixture);
  await service.search("synthetic");
  fixture.put("/search.json?q=synthetic&limit=20&fields=key%2Ctitle%2Cauthor_name%2Cfirst_publish_year%2Cedition_count%2Cebook_access%2Cpublic_scan_b%2Cia", {}, { status: 503 });
  const result = await service.search("synthetic", 20, true);
  assert.equal(result.books[0].title, sample.title); assert.equal(result.stale?.code, "http");
  assert.equal(service.status().state, "error");
});

test("malformed, oversized and rate-limited responses fail without caching", async () => {
  const path = "/search.json?q=synthetic&limit=20&fields=key%2Ctitle%2Cauthor_name%2Cfirst_publish_year%2Cedition_count%2Cebook_access%2Cpublic_scan_b%2Cia";
  for (const response of [{ body: "{bad" }, { body: "null" }]) {
    const fixture = new Fixture(); fixture.answers.set(path, response);
    await assert.rejects(make(fixture).search("synthetic"), ReadError);
  }
  const fixture = new Fixture(); fixture.answers.set(path, { body: " ".repeat(2 << 20) });
  await assert.rejects(make(fixture).search("synthetic"), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
  const limited = new Fixture(); limited.put(path, {}, { status: 429 });
  await assert.rejects(make(limited).search("synthetic"), (error: unknown) => error instanceof ReadError && error.code === "rate_limited");
});

test("cancellation and deadline propagate through fetch and remove pending work", async () => {
  const fixture = new Fixture(); fixture.put("/works/OL59800W.json", { ...sample, title: sample.title }, { delay: 1000 });
  const service = make(fixture), controller = new AbortController();
  const pending = service.work("OL59800W", { key: "OL59800W", title: sample.title, authors: [], access: "no_ebook", url: "https://openlibrary.org/works/OL59800W" }, false, controller.signal);
  await until(async () => fixture.requests.length, length => length > 0);
  controller.abort();
  await assert.rejects(pending, (error: unknown) => error instanceof Error && error.name === "AbortError");
  assert.equal(fixture.cancelled, 1);
  fixture.put("/search.json?q=synthetic&limit=20&fields=key%2Ctitle%2Cauthor_name%2Cfirst_publish_year%2Cedition_count%2Cebook_access%2Cpublic_scan_b%2Cia", { numFound: 1, docs: [sample] }, { delay: 1000 });
  const timed = budget(undefined, 1);
  const slow = make(fixture);
  const timedRequest = slow.search("synthetic", 20, false, timed.signal);
  const rejected = assert.rejects(timedRequest, (error: unknown) => error instanceof Error && error.name === "AbortError" && timed.signal.reason instanceof ReadError && timed.signal.reason.code === "slow");
  await until(async () => timed.signal.aborted, aborted => aborted);
  await rejected;
  timed.dispose();
});

test("assistant tools retain book details and reject invalid or unknown calls", async () => {
  const service = make();
  assert.match(await callTool(service, "openlibrary_search", { query: "synthetic" }), /Left Hand of Darkness[\s\S]*Pride and Prejudice[\s\S]*Public edition/);
  assert.match(await callTool(service, "openlibrary_work", { work: "OL59800W" }), /synthetic description[\s\S]*Science fiction/);
  for (const args of [{ query: "" }, { query: "x".repeat(201) }, { query: "ok", limit: 0 }, { query: "ok", limit: 31 }, { query: "ok", limit: 1.5 }, { query: 2 }]) await assert.rejects(callTool(service, "openlibrary_search", args), ReadError);
  for (const work of ["bad", "https://attacker.invalid", "OL59800W/../../"]) await assert.rejects(callTool(service, "openlibrary_work", { work }), ReadError);
  await assert.rejects(callTool(service, "unknown"), /unknown method/);
});
