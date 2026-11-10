import { test } from "node:test";
import assert from "node:assert/strict";
import { Archive, budget, ReadError } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { Fixture, response, until } from "./fixtures.ts";
const make = (fixture = new Fixture(), options = {}) => new Archive(fixture.fetch, {}, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 }, locale: "en", ...options });
const path = "/advancedsearch.php?q=%28synthetic%29&fl=identifier%2Ctitle%2Ccreator%2Cyear%2Cmediatype%2Cdownloads%2Cdescription&rows=20&page=1&output=json";

test("catalog search uses bounded public requests and a cached validated answer", async () => {
  const fixture = new Fixture(), service = make(fixture);
  assert.equal((await service.search("synthetic")).items[0].title, "Pride and Prejudice");
  await service.search("synthetic");
  assert.equal(fixture.hits.get(path), 1);
  assert.equal(fixture.requests[0].init.redirect, "error");
  assert.equal(fixture.requests[0].init.headers && (fixture.requests[0].init.headers as Record<string, string>).Authorization, undefined);
  assert.equal(service.status().state, "ok"); assert.deepEqual(service.cacheStats().entries, 1);
});

test("failed refresh returns a stale validated answer instead of erasing it", async () => {
  const fixture = new Fixture(), service = make(fixture);
  await service.search("synthetic");
  fixture.put(path, {}, { status: 503 });
  const result = await service.search("synthetic", 20, true);
  assert.equal(result.items[0].title, "Pride and Prejudice"); assert.equal(result.stale?.code, "http");
  assert.equal(service.status().state, "error");
});

test("malformed, oversized and rate-limited responses fail without caching", async () => {
  for (const answer of [{ body: "{bad" }, { body: "null" }]) {
    const fixture = new Fixture(); fixture.answers.set(path, answer);
    await assert.rejects(make(fixture).search("synthetic"), ReadError);
  }
  const fixture = new Fixture(); fixture.answers.set(path, { body: " ".repeat(2 << 20) });
  await assert.rejects(make(fixture).search("synthetic"), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
  const limited = new Fixture(); limited.put(path, {}, { status: 429 });
  await assert.rejects(make(limited).search("synthetic"), (error: unknown) => error instanceof ReadError && error.code === "rate_limited");
});

test("cancellation and deadline propagate through fetch and remove pending work", async () => {
  const fixture = new Fixture(); fixture.put(path, response, { delay: 1000 });
  const service = make(fixture), controller = new AbortController();
  const pending = service.search("synthetic", 20, false, controller.signal);
  await until(async () => fixture.requests.length, length => length > 0);
  controller.abort();
  await assert.rejects(pending, (error: unknown) => error instanceof Error && error.name === "AbortError");
  assert.equal(fixture.cancelled, 1);
  const timed = budget(undefined, 1);
  const slow = make(fixture);
  const request = slow.search("synthetic", 20, false, timed.signal);
  const rejected = assert.rejects(request, (error: unknown) => error instanceof Error && error.name === "AbortError" && timed.signal.reason instanceof ReadError && timed.signal.reason.code === "slow");
  await until(async () => timed.signal.aborted, aborted => aborted);
  await rejected; timed.dispose();
});

test("assistant tools retain metadata and reject invalid or unknown calls", async () => {
  const service = make();
  assert.match(await callTool(service, "archive_search", { query: "synthetic" }), /Pride and Prejudice[\s\S]*Jane Austen[\s\S]*archive.org\/details/);
  for (const args of [{ query: "" }, { query: "x".repeat(161) }, { query: "ok", limit: 0 }, { query: "ok", limit: 21 }, { query: "ok", limit: 1.5 }, { query: 2 }]) await assert.rejects(callTool(service, "archive_search", args), ReadError);
  await assert.rejects(callTool(service, "unknown"), /unknown method/);
});

test("media configuration only changes the approved API filter", async () => {
  const fixture = new Fixture(), service = new Archive(fixture.fetch, { media: "texts" }, { locale: "en", limits: { gap: 0 }, now: () => fixture.clock });
  await service.search("synthetic");
  assert.equal(fixture.requests[0].url.searchParams.get("q"), "(synthetic) AND mediatype:texts");
});
