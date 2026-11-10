import { test } from "node:test";
import assert from "node:assert/strict";
import { budget, OpenAlex, ReadError } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { Fixture, response, until } from "./fixtures.ts";
const make = (fixture = new Fixture(), options = {}) => new OpenAlex(fixture.fetch, {}, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 }, locale: "en", ...options });
const path = "/works?search=synthetic&per-page=20&select=id%2Cdisplay_name%2Cpublication_year%2Cdoi%2Cprimary_location%2Copen_access%2Ccited_by_count";

test("research search uses bounded public requests and a cached validated answer", async () => {
  const fixture = new Fixture(), service = make(fixture);
  assert.equal((await service.search("synthetic")).works[0].citations, 8095);
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
  assert.equal(result.works[0].title, "A Survey of Large Language Models"); assert.equal(result.stale?.code, "http");
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

test("assistant tools retain research metadata and reject invalid or unknown calls", async () => {
  const service = make();
  assert.match(await callTool(service, "openalex_search", { query: "synthetic" }), /Large Language Models[\s\S]*8095 citations[\s\S]*Open access/);
  for (const args of [{ query: "" }, { query: "x".repeat(201) }, { query: "ok", limit: 0 }, { query: "ok", limit: 21 }, { query: "ok", limit: 1.5 }, { query: 2 }]) await assert.rejects(callTool(service, "openalex_search", args), ReadError);
  await assert.rejects(callTool(service, "unknown"), /unknown method/);
});
