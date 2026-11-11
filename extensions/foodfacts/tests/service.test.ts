import { test } from "node:test";
import assert from "node:assert/strict";
import { budget, FoodFacts, ReadError } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { Fixture, productPath, searchPath, searchResponse, until } from "./fixtures.ts";
const make = (fixture = new Fixture(), options = {}) => new FoodFacts(fixture.fetch, {}, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 }, locale: "en", ...options });

test("food search uses bounded public requests and a cached validated answer", async () => {
  const fixture = new Fixture(), service = make(fixture);
  assert.equal((await service.search("chocolate")).products[0].name, "Nutella");
  await service.search("chocolate");
  assert.equal(fixture.hits.get(searchPath), 1);
  assert.equal(fixture.requests[0].init.redirect, "error");
  assert.equal(fixture.requests[0].init.headers && (fixture.requests[0].init.headers as Record<string, string>).Authorization, undefined);
  assert.equal(service.status().state, "ok"); assert.deepEqual(service.cacheStats().entries, 1);
});

test("failed refresh returns a stale validated answer instead of erasing it", async () => {
  const fixture = new Fixture(), service = make(fixture);
  await service.search("chocolate");
  fixture.put(searchPath, {}, { status: 503 });
  const result = await service.search("chocolate", 12, true);
  assert.equal(result.products[0].name, "Nutella"); assert.equal(result.stale?.code, "http");
  assert.equal(service.status().state, "error");
});

test("malformed, oversized and rate-limited responses fail without caching", async () => {
  for (const answer of [{ body: "{bad" }, { body: "null" }]) {
    const fixture = new Fixture(); fixture.answers.set(searchPath, answer);
    await assert.rejects(make(fixture).search("chocolate"), ReadError);
  }
  const fixture = new Fixture(); fixture.answers.set(searchPath, { body: " ".repeat(1 << 20) });
  await assert.rejects(make(fixture).search("chocolate"), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
  const limited = new Fixture(); limited.put(searchPath, {}, { status: 429 });
  await assert.rejects(make(limited).search("chocolate"), (error: unknown) => error instanceof ReadError && error.code === "rate_limited");
});

test("cancellation and deadline propagate through fetch and remove pending work", async () => {
  const fixture = new Fixture(); fixture.put(searchPath, searchResponse, { delay: 1000 });
  const service = make(fixture), controller = new AbortController();
  const pending = service.search("chocolate", 12, false, controller.signal);
  await until(async () => fixture.requests.length, length => length > 0);
  controller.abort();
  await assert.rejects(pending, (error: unknown) => error instanceof Error && error.name === "AbortError");
  assert.equal(fixture.cancelled, 1);
  const timed = budget(undefined, 1);
  const slow = make(fixture);
  const request = slow.search("chocolate", 12, false, timed.signal);
  const rejected = assert.rejects(request, (error: unknown) => error instanceof Error && error.name === "AbortError" && timed.signal.reason instanceof ReadError && timed.signal.reason.code === "slow");
  await until(async () => timed.signal.aborted, aborted => aborted);
  await rejected; timed.dispose();
});

test("assistant tools retain product metadata and reject invalid or unknown calls", async () => {
  const service = make();
  assert.match(await callTool(service, "foodfacts_search", { query: "chocolate" }), /Nutella[\s\S]*milk, nuts, soybeans[\s\S]*ODbL/);
  assert.match(await callTool(service, "foodfacts_product", { code: "3017620422003" }), /Nutella[\s\S]*world.openfoodfacts.org\/product\/3017620422003/);
  for (const args of [{ query: "" }, { query: "x".repeat(121) }, { query: "ok", limit: 0 }, { query: "ok", limit: 21 }, { query: "ok", limit: 1.5 }, { query: 2 }]) await assert.rejects(callTool(service, "foodfacts_search", args), ReadError);
  await assert.rejects(callTool(service, "foodfacts_product", { code: "bad" }), ReadError);
  await assert.rejects(callTool(service, "unknown"), /unknown method/);
});
