import { test } from "node:test";
import assert from "node:assert/strict";
import { budget, Domains, ReadError } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { bootstrap, bootstrapURL, Fixture, registration, registryURL, until } from "./fixtures.ts";
const make = (fixture = new Fixture(), options = {}) => new Domains(fixture.fetch, {}, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 }, locale: "en", ...options });
test("bootstrap and registration reads share validated bounded cache", async () => {
  const fixture = new Fixture(), service = make(fixture);
  const result = await service.lookup("EXAMPLE.com");
  assert.equal(result.registrar, "Example Registrar");
  await service.lookup("example.com");
  assert.equal(fixture.hits.get(bootstrapURL), 1);
  assert.equal(fixture.hits.get(registryURL), 1);
  assert.equal(fixture.requests[0].init.redirect, "error");
  assert.equal(fixture.requests[0].init.headers && (fixture.requests[0].init.headers as Record<string, string>).Authorization, undefined);
  assert.equal(service.status().state, "ok");
});
test("transient failure serves a clearly marked old validated record", async () => {
  const fixture = new Fixture(), service = make(fixture);
  await service.lookup("example.com");
  fixture.put(registryURL, {}, { status: 503 });
  const result = await service.lookup("example.com", true);
  assert.equal(result.registrar, "Example Registrar");
  assert.equal(result.stale?.code, "http");
});
test("a semantically invalid 200 answer does not replace a validated cached record", async () => {
  const fixture = new Fixture(), service = make(fixture);
  await service.lookup("example.com");
  fixture.put(registryURL, {});
  const refreshed = await service.lookup("example.com", true);
  assert.equal(refreshed.registrar, "Example Registrar");
  assert.equal(refreshed.stale?.code, "bad_answer");
  // The invalid answer must not have replaced the valid record: a later lookup still reads it.
  const result = await service.lookup("example.com");
  assert.equal(result.registrar, "Example Registrar");
  assert.equal(result.stale, undefined);
});

test("malformed, oversized and rate-limited responses are never cached", async () => {
  for (const response of [{ body: "{bad" }, { body: "null" }]) {
    const fixture = new Fixture(); fixture.answers.set(registryURL, response);
    await assert.rejects(make(fixture).lookup("example.com"), ReadError);
  }
  const fixture = new Fixture(); fixture.answers.set(registryURL, { body: " ".repeat(1 << 20) });
  await assert.rejects(make(fixture).lookup("example.com"), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
  const limited = new Fixture(); limited.put(registryURL, {}, { status: 429 });
  await assert.rejects(make(limited).lookup("example.com"), (error: unknown) => error instanceof ReadError && error.code === "rate_limited");
});
test("explicit cancellation aborts a pending registry response", async () => {
  const fixture = new Fixture(); fixture.put(registryURL, registration, { delay: 1000 });
  const service = make(fixture), controller = new AbortController();
  const pending = service.lookup("example.com", false, controller.signal);
  await until(async () => fixture.requests.length, length => length > 1);
  controller.abort();
  await assert.rejects(pending, (error: unknown) => error instanceof Error);
  assert.ok(fixture.cancelled >= 1);
});
test("a request deadline aborts the fetch and carries the timeout reason", async () => {
  const service = new Domains(async (_input, init) => {
    const signal = init?.signal;
    if (!signal) throw new Error("request signal missing");
    await new Promise<void>((_resolve, reject) => {
      if (signal.aborted) reject(signal.reason);
      else signal.addEventListener("abort", () => reject(signal.reason), { once: true });
    });
    throw new Error("aborted response must not resolve");
  }, {}, { limits: { gap: 0, request: 5 } });
  await assert.rejects(service.lookup("example.com"), (error: unknown) => error instanceof Error && error.name === "AbortError");
});
test("assistant lookup returns lifecycle metadata and rejects unsafe inputs", async () => {
  const service = make();
  assert.match(await callTool(service, "domains_lookup", { domain: "example.com" }), /Example Registrar[\s\S]*client transfer prohibited[\s\S]*expiration/);
  for (const domain of ["", "https://example.com", "example", "user@example.com", "*.example.com", 42]) await assert.rejects(callTool(service, "domains_lookup", { domain }), ReadError);
  await assert.rejects(callTool(service, "unknown"), /unknown method/);
});
