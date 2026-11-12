import { test } from "node:test";
import assert from "node:assert/strict";
import { budget, Nuget, ReadError } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { searchPath } from "../src/model.ts";
import { catalogPath, catalogResponse, Fixture, leafPath, leafResponse, nuspecResponse, search20Path, searchPathFixture, searchResponse, until } from "./fixtures.ts";
const make = (fixture = new Fixture(), options = {}) => new Nuget(fixture.fetch, {}, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 }, locale: "en", ...options });

test("anonymous search, exact registration leaf and catalog use validated requests", async () => {
  const fixture = new Fixture(), service = make(fixture);
  assert.equal((await service.search("Newtonsoft.Json")).packages[0].id, "Newtonsoft.Json");
  const result = await service.package("Newtonsoft.Json");
  assert.equal(result.package.install, "dotnet add package Newtonsoft.Json --version 13.0.4");
  assert.equal(result.package.license, "MIT");
  assert.equal(result.package.dependencies, 10);
  await service.package("Newtonsoft.Json");
  assert.equal(fixture.hits.get("https://azuresearch-usnc.nuget.org" + search20Path), 1);
  assert.equal(fixture.hits.get("https://api.nuget.org" + leafPath), 1);
  assert.equal(fixture.hits.get("https://api.nuget.org" + catalogPath), 1);
  for (const request of fixture.requests) {
    assert.equal(request.init.method, "GET");
    assert.equal(request.init.redirect, "follow");
    assert.equal(request.url.searchParams.has("access_token"), false);
  }
  assert.equal(service.status().state, "ok");
});

test("an ID in another letter case reads the package under its own spelling", async () => {
  const fixture = new Fixture(); fixture.put(searchPath("newtonsoft.json", 20), searchResponse);
  const result = await make(fixture).package("newtonsoft.json");
  assert.equal(result.package.id, "Newtonsoft.Json");
  assert.equal(result.package.install, "dotnet add package Newtonsoft.Json --version 13.0.4");
  assert.equal(result.package.url, "https://www.nuget.org/packages/Newtonsoft.Json/13.0.4");
});

test("transient failures return only previously validated stale data", async () => {
  const fixture = new Fixture(), service = make(fixture);
  await service.package("Newtonsoft.Json");
  fixture.put(search20Path, {}, { status: 503 });
  fixture.clock += 16 * 60_000;
  const result = await service.package("Newtonsoft.Json", true);
  assert.equal(result.package.version, "13.0.4");
  assert.equal(result.stale?.code, "http");
  assert.equal(service.status().state, "ok", "later successful catalog reads restore health");
  const again = await service.search("Newtonsoft.Json", 20, true);
  assert.equal(again.stale?.code, "http");
  assert.equal(service.status().state, "error");
  const fresh = new Fixture();
  fresh.put(searchPathFixture, {}, { status: 503 });
  await assert.rejects(make(fresh).search("Newtonsoft.Json", 12), ReadError);
});

test("malformed responses never enter the stale cache", async () => {
  const fixture = new Fixture(); fixture.put(catalogPath, nuspecResponse.replace("13.0.4", "14.0.1-beta2"));
  const service = make(fixture);
  await assert.rejects(service.package("Newtonsoft.Json"), ReadError);
  assert.equal(service.cacheStats().entries, 2);
  fixture.put(catalogPath, {}, { status: 503 });
  await assert.rejects(service.package("Newtonsoft.Json"), ReadError);
});

test("oversized, malformed and rate-limited responses fail closed", async () => {
  for (const response of [{ body: "{bad" }, { body: "null" }]) {
    const fixture = new Fixture(); fixture.responses.set(searchPathFixture, response);
    await assert.rejects(make(fixture).search("Newtonsoft.Json"), ReadError);
  }
  const oversized = new Fixture(); oversized.responses.set(searchPathFixture, { body: " ".repeat(1 << 20) });
  await assert.rejects(make(oversized).search("Newtonsoft.Json"), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
  const limited = new Fixture(); limited.put(searchPathFixture, {}, { status: 429 });
  await assert.rejects(make(limited).search("Newtonsoft.Json"), (error: unknown) => error instanceof ReadError && error.code === "rate_limited");
});

test("cancellation and deadlines propagate through registry requests", async () => {
  const fixture = new Fixture(); fixture.put(searchPathFixture, { data: [], totalHits: 0 }, { delay: 1000 });
  const service = make(fixture), controller = new AbortController();
  const pending = service.search("Newtonsoft.Json", 12, false, controller.signal);
  await until(async () => fixture.requests.length, length => length > 0);
  controller.abort();
  await assert.rejects(pending, (error: unknown) => error instanceof Error && error.name === "AbortError");
  assert.equal(fixture.cancelled, 1);
  const timed = budget(undefined, 1), slow = make(fixture);
  const request = slow.search("Newtonsoft.Json", 12, false, timed.signal);
  const rejected = assert.rejects(request, (error: unknown) => error instanceof Error && error.name === "AbortError" && timed.signal.reason instanceof ReadError && timed.signal.reason.code === "slow");
  await until(async () => timed.signal.aborted, aborted => aborted);
  await rejected; timed.dispose();
});

test("assistant tools preserve exact stable versions, licenses and canonical links", async () => {
  const service = make();
  assert.match(await callTool(service, "nuget_search", { query: "Newtonsoft.Json" }), /Newtonsoft.Json@13.0.4[\s\S]*newtonsoft.com\/json/);
  assert.match(await callTool(service, "nuget_package", { name: "Newtonsoft.Json" }), /dotnet add package Newtonsoft.Json --version 13.0.4[\s\S]*licenses.nuget.org\/MIT/);
  for (const args of [{ query: "" }, { query: "x".repeat(101) }, { query: "ok", limit: 0 }, { query: "ok", limit: 21 }, { query: "ok", limit: 1.5 }, { query: 2 }]) await assert.rejects(callTool(service, "nuget_search", args), ReadError);
  for (const name of ["../secret", "a b", "a..b", "a\n"]) await assert.rejects(callTool(service, "nuget_package", { name }), ReadError);
  await assert.rejects(callTool(service, "unknown"), /unknown method/);
});
