import { test } from "node:test";
import assert from "node:assert/strict";
import { budget, CurrencyService, ReadError } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { Fixture, ratePath, response, until } from "./fixtures.ts";
const make = (fixture = new Fixture(), options = {}) => new CurrencyService(fixture.fetch, {}, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 }, locale: "en", ...options });

test("currency conversion uses only ECB reference rates and caches the validated answer", async () => {
  const fixture = new Fixture(), service = make(fixture);
  assert.equal((await service.rate("USD", "CNY")).date, "2026-11-10");
  await service.rate("USD", "CNY");
  assert.equal(fixture.hits.get(ratePath), 1);
  assert.equal(fixture.requests[0].init.redirect, "error");
  assert.equal(fixture.requests[0].init.headers && (fixture.requests[0].init.headers as Record<string, string>).Authorization, undefined);
  assert.equal(service.status().state, "ok"); assert.deepEqual(service.cacheStats().entries, 1);
});

test("a failed refresh retains the old dated answer and marks it stale", async () => {
  const fixture = new Fixture(), service = make(fixture);
  await service.rate("USD", "CNY");
  fixture.put(ratePath, {}, { status: 503 });
  const result = await service.rate("USD", "CNY", true);
  assert.equal(result.rate, "6.6703"); assert.equal(result.stale?.code, "http");
  assert.equal(service.status().state, "error");
});

test("malformed, oversized and rate-limited responses fail without caching", async () => {
  for (const answer of [{ body: "{bad" }, { body: "null" }]) {
    const fixture = new Fixture(); fixture.answers.set(ratePath, answer);
    await assert.rejects(make(fixture).rate("USD", "CNY"), ReadError);
  }
  const fixture = new Fixture(); fixture.answers.set(ratePath, { body: " ".repeat(256 << 10) });
  await assert.rejects(make(fixture).rate("USD", "CNY"), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
  const limited = new Fixture(); limited.put(ratePath, {}, { status: 429 });
  await assert.rejects(make(limited).rate("USD", "CNY"), (error: unknown) => error instanceof ReadError && error.code === "rate_limited");
});

test("cancellation and deadlines propagate through fetch", async () => {
  const fixture = new Fixture(); fixture.put(ratePath, response, { delay: 1000 });
  const service = make(fixture), controller = new AbortController();
  const pending = service.rate("USD", "CNY", false, controller.signal);
  await until(async () => fixture.requests.length, length => length > 0);
  controller.abort();
  await assert.rejects(pending, (error: unknown) => error instanceof Error && error.name === "AbortError");
  assert.equal(fixture.cancelled, 1);
  const timed = budget(undefined, 1);
  const slow = make(fixture);
  const request = slow.rate("USD", "CNY", false, timed.signal);
  const rejected = assert.rejects(request, (error: unknown) => error instanceof Error && error.name === "AbortError" && timed.signal.reason instanceof ReadError && timed.signal.reason.code === "slow");
  await until(async () => timed.signal.aborted, aborted => aborted);
  await rejected; timed.dispose();
});

test("assistant tools retain the date and perform unit conversion without network", async () => {
  const fixture = new Fixture(), service = make(fixture);
  assert.match(await callTool(service, "currency_convert", { amount: "100", from: "USD", to: "CNY" }), /100 USD = 667.03 CNY[\s\S]*2026-11-10[\s\S]*reference rates/);
  const hits = fixture.requests.length;
  assert.equal(await callTool(service, "unit_convert", { amount: "100", from: "C", to: "F" }), "100 °C = 212 °F\nLocal conversion · no network request");
  assert.equal(fixture.requests.length, hits);
  for (const args of [{ amount: "-1", from: "USD", to: "CNY" }, { amount: "1", from: "BTC", to: "CNY" }, { amount: "1e3", from: "USD", to: "CNY" }, { amount: "1", from: "USD", to: 2 }]) await assert.rejects(callTool(service, "currency_convert", args), ReadError);
  await assert.rejects(callTool(service, "unknown"), /unknown method/);
});
