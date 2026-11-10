import { test } from "node:test";
import assert from "node:assert/strict";
import { budget, ReadError, Wikipedia } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { Fixture, syntheticArticle } from "./fixtures.ts";
const make = (fixture = new Fixture(), options = {}) => new Wikipedia(fixture.fetch, {}, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 }, locale: "en", ...options });
test("feed, search and summary share bounded public requests and cached responses", async () => {
  const fixture = new Fixture(), service = make(fixture);
  assert.equal((await service.today()).featured?.heading, "Featured article");
  assert.equal((await service.search("synthetic"))[0].title, "Found_article");
  assert.equal((await service.article("Featured article")).extract, "A detailed synthetic introduction.");
  await service.today(); await service.search("synthetic"); await service.article("Featured_article");
  assert.equal(fixture.hits.get("/api/rest_v1/feed/featured/2026/11/09"), 1);
  assert.equal(fixture.hits.get("/w/rest.php/v1/search/page?q=synthetic&limit=30"), 1);
  assert.equal(fixture.hits.get("/api/rest_v1/page/summary/Featured_article"), 1);
  assert.equal(fixture.requests[0].init.redirect, "error");
  assert.equal(fixture.requests[0].init.headers && (fixture.requests[0].init.headers as Record<string, string>).Authorization, undefined);
  assert.equal(service.status().state, "ok"); assert.deepEqual(service.cacheStats().entries, 3);
});
test("failed refresh returns a stale validated answer instead of erasing it", async () => {
  const fixture = new Fixture(), service = make(fixture);
  await service.today();
  fixture.put("/api/rest_v1/feed/featured/2026/11/09", {}, { status: 503 });
  const result = await service.today(undefined, true);
  assert.equal(result.featured?.heading, "Featured article"); assert.equal(result.stale?.code, "http");
  assert.equal(service.status().state, "error");
});
test("malformed, oversized and rate-limited responses fail without caching", async () => {
  for (const response of [{ body: "{bad" }, { body: "null" }]) {
    const fixture = new Fixture(); fixture.answers.set("/api/rest_v1/feed/featured/2026/11/09", response);
    await assert.rejects(make(fixture).today(), ReadError);
  }
  const fixture = new Fixture(); fixture.answers.set("/api/rest_v1/feed/featured/2026/11/09", { body: " ".repeat(4 << 20) });
  await assert.rejects(make(fixture).today(), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
  const limited = new Fixture(); limited.put("/api/rest_v1/feed/featured/2026/11/09", {}, { status: 429 });
  await assert.rejects(make(limited).today(), (error: unknown) => error instanceof ReadError && error.code === "rate_limited");
});
test("cancellation and deadline propagate through fetch and remove pending work", async () => {
  const fixture = new Fixture(); fixture.put("/api/rest_v1/feed/featured/2026/11/09", { tfa: syntheticArticle() }, { delay: 1000 });
  const service = make(fixture), controller = new AbortController();
  const pending = service.today(undefined, false, controller.signal);
  await new Promise(resolve => setTimeout(resolve, 20));
  controller.abort();
  await assert.rejects(pending, (error: unknown) => error instanceof Error && error.name === "AbortError");
  assert.equal(fixture.cancelled, 1);
  const timed = budget(undefined, 1);
  await assert.rejects(make(new Fixture()).today(undefined, false, timed.signal), (error: unknown) => error instanceof ReadError && error.code === "slow");
  timed.dispose();
});
test("assistant tools retain content and reject invalid or unknown calls", async () => {
  const service = make();
  assert.match(await callTool(service, "wikipedia_today"), /Featured today[\s\S]*Popular article[\s\S]*1969/);
  assert.match(await callTool(service, "wikipedia_search", { query: "synthetic" }), /Found article/);
  assert.match(await callTool(service, "wikipedia_article", { title: "Featured_article" }), /detailed synthetic introduction/);
  for (const args of [{ query: "" }, { query: "x".repeat(201) }, { query: "ok", limit: 0 }, { query: "ok", limit: 31 }, { query: "ok", limit: 1.5 }, { query: 2 }]) await assert.rejects(callTool(service, "wikipedia_search", args), ReadError);
  await assert.rejects(callTool(service, "wikipedia_article", { title: "bad#target" }), ReadError);
  await assert.rejects(callTool(service, "unknown"), /unknown method/);
});
test("language config chooses only registered origins", async () => {
  for (const [locale, language] of [["zh-Hans", "zh"], ["ja", "ja"], ["en", "en"]] as const) {
    const fixture = new Fixture(), service = new Wikipedia(fixture.fetch, {}, { locale, limits: { gap: 0 }, now: () => fixture.clock });
    await service.today(); assert.equal(fixture.requests[0].url.hostname, `${language}.wikipedia.org`);
  }
  const fixture = new Fixture(), service = new Wikipedia(fixture.fetch, { language: "ja" }, { locale: "zh", limits: { gap: 0 }, now: () => fixture.clock });
  await service.today(); assert.equal(fixture.requests[0].url.hostname, "ja.wikipedia.org");
});
