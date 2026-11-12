import { test } from "node:test";
import assert from "node:assert/strict";
import { budget, RubyGems, ReadError } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { detailPath, detailResponse, Fixture, searchPathFixture, until } from "./fixtures.ts";
const make = (fixture = new Fixture(), options = {}) => new RubyGems(fixture.fetch, {}, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 }, locale: "en", ...options });

test("anonymous RubyGems search and detail use validated bounded requests", async () => {
  const fixture = new Fixture(), service = make(fixture);
  assert.equal((await service.search("rails")).gems[0].name, "rails");
  assert.equal((await service.gem("rails")).gem.install, "gem install rails -v 8.1.4.1");
  assert.equal((await service.search("rails", 1)).gems.length, 1);
  await service.search("rails");
  await service.gem("rails");
  assert.equal(fixture.hits.get(searchPathFixture), 1);
  assert.equal(fixture.hits.get(detailPath), 1);
  for (const request of fixture.requests) {
    assert.equal(request.init.method, "GET");
    assert.equal(request.init.redirect, "error");
    assert.equal(request.url.searchParams.has("access_token"), false);
    assert.match((request.init.headers as Record<string, string>)["User-Agent"], /persona-rubygems/);
  }
  assert.equal(service.status().state, "ok");
});

test("transient failures return only previously validated stale data", async () => {
  const fixture = new Fixture(), service = make(fixture);
  await service.search("rails");
  fixture.put(searchPathFixture, {}, { status: 503 });
  fixture.clock += 16 * 60_000;
  const result = await service.search("rails", 12, true);
  assert.equal(result.gems[0].name, "rails");
  assert.equal(result.stale?.code, "http");
  assert.equal(service.status().state, "error");
});

test("malformed responses never enter the stale cache", async () => {
  const fixture = new Fixture(); fixture.put(detailPath, { ...detailResponse, dependencies: { runtime: [] }, project_uri: "https://attacker.invalid/gem" });
  const service = make(fixture);
  await assert.rejects(service.gem("rails"), ReadError);
  assert.equal(service.cacheStats().entries, 0);
  fixture.put(detailPath, {}, { status: 503 });
  await assert.rejects(service.gem("rails"), ReadError);
  assert.equal(service.cacheStats().entries, 0);
});

test("oversized, malformed and rate-limited responses fail closed", async () => {
  for (const response of [{ body: "{bad" }, { body: "null" }]) {
    const fixture = new Fixture(); fixture.responses.set(searchPathFixture, response);
    await assert.rejects(make(fixture).search("rails"), ReadError);
  }
  const oversized = new Fixture(); oversized.responses.set(searchPathFixture, { body: " ".repeat(1 << 20) });
  await assert.rejects(make(oversized).search("rails"), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
  const limited = new Fixture(); limited.put(searchPathFixture, {}, { status: 429 });
  await assert.rejects(make(limited).search("rails"), (error: unknown) => error instanceof ReadError && error.code === "rate_limited");
});

test("cancellation and deadlines propagate through registry requests", async () => {
  const fixture = new Fixture(); fixture.put(searchPathFixture, [], { delay: 1000 });
  const service = make(fixture), controller = new AbortController();
  const pending = service.search("rails", 12, false, controller.signal);
  await until(async () => fixture.requests.length, length => length > 0);
  controller.abort();
  await assert.rejects(pending, (error: unknown) => error instanceof Error && error.name === "AbortError");
  assert.equal(fixture.cancelled, 1);
  const timed = budget(undefined, 1), slow = make(fixture);
  const request = slow.search("rails", 12, false, timed.signal);
  const rejected = assert.rejects(request, (error: unknown) => error instanceof Error && error.name === "AbortError" && timed.signal.reason instanceof ReadError && timed.signal.reason.code === "slow");
  await until(async () => timed.signal.aborted, aborted => aborted);
  await rejected; timed.dispose();
});

test("assistant tools preserve exact versions, licenses, dependencies and canonical links", async () => {
  const service = make();
  assert.match(await callTool(service, "rubygems_search", { query: "rails" }), /rails@8.1.4.1[\s\S]*github.com\/rails\/rails/);
  assert.match(await callTool(service, "rubygems_gem", { name: "rails" }), /3 runtime dependencies: actionpack \(= 8.1.4.1\)[\s\S]*gem install rails -v 8.1.4.1[\s\S]*api.rubyonrails.org/);
  for (const args of [{ query: "" }, { query: "x".repeat(101) }, { query: "ok", limit: 0 }, { query: "ok", limit: 21 }, { query: "ok", limit: 1.5 }, { query: 2 }]) await assert.rejects(callTool(service, "rubygems_search", args), ReadError);
  for (const name of ["../secret", "ruby rails", "a\n"]) await assert.rejects(callTool(service, "rubygems_gem", { name }), ReadError);
  await assert.rejects(callTool(service, "unknown"), /unknown method/);
});
