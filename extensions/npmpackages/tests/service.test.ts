import { test } from "node:test";
import assert from "node:assert/strict";
import { budget, NpmPackages, ReadError } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { downloadsScopedPath, Fixture, scoped, scopedPath, typescriptPath, until } from "./fixtures.ts";
const make = (fixture = new Fixture(), options = {}) => new NpmPackages(fixture.fetch, {}, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 }, locale: "en", ...options });

test("anonymous registry search, scoped details and downloads use bounded validated requests", async () => {
  const fixture = new Fixture(), service = make(fixture);
  assert.equal((await service.search("typescript")).packages[0].name, "typescript");
  const detail = await service.package("@babel/preset-typescript");
  assert.equal(detail.package.install, "npm install @babel/preset-typescript@8.0.7");
  assert.equal(detail.package.weeklyDownloads, 73675116);
  assert.equal(detail.downloadsStart, "2026-11-03");
  await service.search("typescript");
  await service.package("@babel/preset-typescript");
  assert.equal(fixture.hits.get("https://registry.npmjs.org" + typescriptPath), 1);
  assert.equal(fixture.hits.get("https://registry.npmjs.org" + scopedPath), 1);
  assert.equal(fixture.hits.get("https://api.npmjs.org" + downloadsScopedPath), 1);
  for (const request of fixture.requests) {
    assert.equal(request.init.method, "GET");
    assert.equal(request.init.redirect, "error");
    assert.equal(request.url.searchParams.has("access_token"), false);
  }
  assert.equal(service.status().state, "ok");
});

test("transient failures return only previously validated stale data", async () => {
  const fixture = new Fixture(), service = make(fixture);
  await service.search("typescript");
  fixture.put(typescriptPath, {}, { status: 503 });
  fixture.clock += 16 * 60_000;
  const result = await service.search("typescript", 12, true);
  assert.equal(result.packages[0].name, "typescript");
  assert.equal(result.stale?.code, "http");
  assert.equal(service.status().state, "error");
});

test("malformed responses never enter the stale cache", async () => {
  const fixture = new Fixture(); fixture.put(typescriptPath, { objects: [{ package: { ...scoped, links: { npm: "https://attacker.invalid" } } }], total: 1 });
  const service = make(fixture);
  await assert.rejects(service.search("typescript"), ReadError);
  assert.equal(service.cacheStats().entries, 0);
  fixture.put(typescriptPath, {}, { status: 503 });
  await assert.rejects(service.search("typescript"), ReadError);
  assert.equal(service.cacheStats().entries, 0);
});

test("oversized, malformed and rate-limited responses fail closed", async () => {
  for (const response of [{ body: "{bad" }, { body: "null" }]) {
    const fixture = new Fixture(); fixture.responses.set(typescriptPath, response);
    await assert.rejects(make(fixture).search("typescript"), ReadError);
  }
  const oversized = new Fixture(); oversized.responses.set(typescriptPath, { body: " ".repeat(1 << 20) });
  await assert.rejects(make(oversized).search("typescript"), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
  const limited = new Fixture(); limited.put(typescriptPath, {}, { status: 429 });
  await assert.rejects(make(limited).search("typescript"), (error: unknown) => error instanceof ReadError && error.code === "rate_limited");
});

test("cancellation and deadlines propagate through registry requests", async () => {
  const fixture = new Fixture(); fixture.put(typescriptPath, { objects: [], total: 0 }, { delay: 1000 });
  const service = make(fixture), controller = new AbortController();
  const pending = service.search("typescript", 12, false, controller.signal);
  await until(async () => fixture.requests.length, length => length > 0);
  controller.abort();
  await assert.rejects(pending, (error: unknown) => error instanceof Error && error.name === "AbortError");
  assert.equal(fixture.cancelled, 1);
  const timed = budget(undefined, 1), slow = make(fixture);
  const request = slow.search("typescript", 12, false, timed.signal);
  const rejected = assert.rejects(request, (error: unknown) => error instanceof Error && error.name === "AbortError" && timed.signal.reason instanceof ReadError && timed.signal.reason.code === "slow");
  await until(async () => timed.signal.aborted, aborted => aborted);
  await rejected; timed.dispose();
});

test("assistant tools preserve exact versions and canonical links and reject invalid input", async () => {
  const service = make();
  assert.match(await callTool(service, "npmpackages_search", { query: "typescript" }), /typescript@7.0.2[\s\S]*github.com\/microsoft\/TypeScript/);
  assert.match(await callTool(service, "npmpackages_package", { name: "@babel/preset-typescript" }), /npm install @babel\/preset-typescript@8.0.7[\s\S]*CC|npm install @babel\/preset-typescript@8.0.7/);
  for (const args of [{ query: "" }, { query: "x".repeat(201) }, { query: "ok", limit: 0 }, { query: "ok", limit: 21 }, { query: "ok", limit: 1.5 }, { query: 2 }]) await assert.rejects(callTool(service, "npmpackages_search", args), ReadError);
  for (const name of ["../secret", "@scope", "UPPERCASE", "a b"]) await assert.rejects(callTool(service, "npmpackages_package", { name }), ReadError);
  await assert.rejects(callTool(service, "unknown"), /unknown method/);
});
