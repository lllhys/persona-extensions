import { test } from "node:test";
import assert from "node:assert/strict";
import { budget, Commons, ReadError } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { Fixture, response, until } from "./fixtures.ts";
const make = (fixture = new Fixture(), options = {}) => new Commons(fixture.fetch, {}, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 }, locale: "en", ...options });
const path = "/w/api.php?action=query&generator=search&gsrsearch=File%3AEiffel+Tower&gsrnamespace=6&gsrlimit=20&prop=imageinfo&iiprop=url%7Cextmetadata%7Csize&iiurlwidth=640&format=json";

test("image metadata uses bounded public requests and a cached validated answer", async () => {
  const fixture = new Fixture(), service = make(fixture);
  assert.equal((await service.search("Eiffel Tower")).images[0].artist, "Example Photographer");
  await service.search("Eiffel Tower");
  assert.equal(fixture.hits.get(path), 1);
  assert.equal(fixture.requests[0].init.redirect, "error");
  assert.equal(fixture.requests[0].init.headers && (fixture.requests[0].init.headers as Record<string, string>).Authorization, undefined);
  assert.equal(service.status().state, "ok"); assert.deepEqual(service.cacheStats().entries, 1);
});

test("failed refresh returns a stale validated answer instead of erasing it", async () => {
  const fixture = new Fixture(), service = make(fixture);
  await service.search("Eiffel Tower");
  fixture.put(path, {}, { status: 503 });
  const result = await service.search("Eiffel Tower", 20, true);
  assert.equal(result.images[0].artist, "Example Photographer"); assert.equal(result.stale?.code, "http");
  assert.equal(service.status().state, "error");
});

test("malformed, oversized and rate-limited responses fail without caching", async () => {
  for (const answer of [{ body: "{bad" }, { body: "null" }]) {
    const fixture = new Fixture(); fixture.answers.set(path, answer);
    await assert.rejects(make(fixture).search("Eiffel Tower"), ReadError);
  }
  const fixture = new Fixture(); fixture.answers.set(path, { body: " ".repeat(2 << 20) });
  await assert.rejects(make(fixture).search("Eiffel Tower"), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
  const limited = new Fixture(); limited.put(path, {}, { status: 429 });
  await assert.rejects(make(limited).search("Eiffel Tower"), (error: unknown) => error instanceof ReadError && error.code === "rate_limited");
});

test("cancellation and deadline propagate through fetch and remove pending work", async () => {
  const fixture = new Fixture(); fixture.put(path, response, { delay: 1000 });
  const service = make(fixture), controller = new AbortController();
  const pending = service.search("Eiffel Tower", 20, false, controller.signal);
  await until(async () => fixture.requests.length, length => length > 0);
  controller.abort();
  await assert.rejects(pending, (error: unknown) => error instanceof Error && error.name === "AbortError");
  assert.equal(fixture.cancelled, 1);
  const timed = budget(undefined, 1);
  const slow = make(fixture);
  const request = slow.search("Eiffel Tower", 20, false, timed.signal);
  const rejected = assert.rejects(request, (error: unknown) => error instanceof Error && error.name === "AbortError" && timed.signal.reason instanceof ReadError && timed.signal.reason.code === "slow");
  await until(async () => timed.signal.aborted, aborted => aborted);
  await rejected; timed.dispose();
});

test("assistant tools retain attribution and reject invalid or unknown calls", async () => {
  const service = make();
  assert.match(await callTool(service, "commons_search", { query: "Eiffel Tower" }), /Example Photographer[\s\S]*Public domain[\s\S]*Wikimedia_Commons/);
  for (const args of [{ query: "" }, { query: "x".repeat(161) }, { query: "ok", limit: 0 }, { query: "ok", limit: 21 }, { query: "ok", limit: 1.5 }, { query: 2 }]) await assert.rejects(callTool(service, "commons_search", args), ReadError);
  await assert.rejects(callTool(service, "unknown"), /unknown method/);
});
