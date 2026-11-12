import { test } from "node:test";
import assert from "node:assert/strict";
import { budget, ReadError, StackExchange } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { answer, answerPath, Fixture, question, questionPath, until } from "./fixtures.ts";
import { searchPath } from "../src/model.ts";
const make = (fixture = new Fixture(), options = {}) => new StackExchange(fixture.fetch, {}, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 }, locale: "en", ...options });

test("anonymous search and answer use bounded official GET requests and validated caching", async () => {
  const fixture = new Fixture(), service = make(fixture);
  assert.equal((await service.search("typescript")).questions[0].id, 70794241);
  assert.equal((await service.answers(70794241)).answers[0].accepted, true);
  await service.search("typescript", "stackoverflow", "", 12, true);
  await service.answers(70794241, "stackoverflow", true);
  assert.equal(fixture.hits.get(questionPath), 1);
  assert.equal(fixture.hits.get(answerPath), 1);
  for (const request of fixture.requests) {
    assert.equal(request.init.method, "GET");
    assert.equal(request.init.redirect, "error");
    assert.equal(request.init.headers && (request.init.headers as Record<string, string>).Authorization, undefined);
    assert.equal(request.url.searchParams.has("access_token"), false);
  }
  assert.equal(service.status().state, "ok");
  assert.equal(service.cacheStats().entries, 2);
});

test("identical requests are never repeated within a minute, even at zero cache TTL", async () => {
  const fixture = new Fixture(), service = make(fixture, { limits: { gap: 0, fresh: 0 } });
  await service.search("typescript", "stackoverflow", "", 12, true);
  await service.search("typescript", "stackoverflow", "", 12, true);
  assert.equal(fixture.hits.get(questionPath), 1);
  fixture.clock += 61_000;
  await service.search("typescript", "stackoverflow", "", 12, true);
  assert.equal(fixture.hits.get(questionPath), 2);
});

test("a transient failure serves stale validated content, not malformed replacement data", async () => {
  const fixture = new Fixture(), service = make(fixture);
  await service.search("typescript");
  fixture.put(questionPath, { items: [] }, { status: 503 });
  fixture.clock += 61_000;
  const result = await service.search("typescript", "stackoverflow", "", 12, true);
  assert.equal(result.questions[0].id, 70794241);
  assert.equal(result.stale?.code, "http");
  assert.equal(service.status().state, "error");
});

test("malformed, oversized and rate-limited responses fail closed without caching", async () => {
  for (const response of [{ body: "{bad" }, { body: "null" }, { body: JSON.stringify({ error_id: 400, error_name: "bad_parameter", error_message: "bad" }) }]) {
    const fixture = new Fixture(); fixture.responses.set(questionPath, response);
    await assert.rejects(make(fixture).search("typescript"), ReadError);
  }
  const oversized = new Fixture(); oversized.responses.set(questionPath, { body: " ".repeat(2 << 20) });
  await assert.rejects(make(oversized).search("typescript"), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
  const limited = new Fixture(); limited.put(questionPath, {}, { status: 429 });
  await assert.rejects(make(limited).search("typescript"), (error: unknown) => error instanceof ReadError && error.code === "rate_limited");
});

test("server backoff is shared per method and excessive waits fail without a request", async () => {
  const fixture = new Fixture(); fixture.put(questionPath, { items: [], backoff: 1 });
  fixture.put(searchPath("typescript", "stackoverflow", "javascript", 12), { items: [], backoff: 0 });
  const service = make(fixture);
  await service.search("typescript");
  const pending = service.search("typescript", "stackoverflow", "javascript", 12);
  await new Promise(resolve => setTimeout(resolve, 30));
  assert.equal(fixture.requests.length, 1);
  fixture.clock += 1_001;
  await pending;
  assert.equal(fixture.requests.length, 2);
  const long = new Fixture(); long.put(questionPath, { items: [], backoff: 31 });
  const throttled = make(long);
  await throttled.search("typescript");
  await assert.rejects(throttled.search("typescript", "stackoverflow", "javascript"), (error: unknown) => error instanceof ReadError && error.code === "rate_limited");
  assert.equal(long.requests.length, 1);
});

test("cancellation and deadlines propagate and do not swallow parent AbortError", async () => {
  const fixture = new Fixture(); fixture.put(questionPath, { items: [] }, { delay: 1000 });
  const service = make(fixture), controller = new AbortController();
  const pending = service.search("typescript", "stackoverflow", "", 12, false, controller.signal);
  await until(async () => fixture.requests.length, length => length > 0);
  controller.abort();
  await assert.rejects(pending, (error: unknown) => error instanceof Error && error.name === "AbortError");
  assert.equal(fixture.cancelled, 1);
  const timed = budget(undefined, 1), slow = make(fixture);
  const request = slow.search("typescript", "stackoverflow", "", 12, false, timed.signal);
  const rejected = assert.rejects(request, (error: unknown) => error instanceof Error && error.name === "AbortError" && timed.signal.reason instanceof ReadError && timed.signal.reason.code === "slow");
  await until(async () => timed.signal.aborted, aborted => aborted);
  await rejected; timed.dispose();
});

test("malformed responses never become stale cached content", async () => {
  const fixture = new Fixture(), service = make(fixture);
  fixture.put(questionPath, { items: [{ ...question, link: "https://attacker.invalid/questions/70794241" }] });
  await assert.rejects(service.search("typescript"), ReadError);
  assert.equal(service.cacheStats().entries, 0);
  fixture.put(questionPath, {}, { status: 503 });
  await assert.rejects(service.search("typescript"), ReadError);
  assert.equal(service.cacheStats().entries, 0);
});

test("assistant tools retain source links, code and attribution, and reject invalid arguments", async () => {
  const service = make();
  assert.match(await callTool(service, "stackexchange_search", { query: "typescript" }), /70794241[\s\S]*CC BY-SA/);
  assert.match(await callTool(service, "stackexchange_answer", { question_id: 70794241 }), /Accepted answer[\s\S]*Example Answerer[\s\S]*CC BY-SA[\s\S]*const value/);
  for (const args of [{ query: "" }, { query: "x".repeat(201) }, { query: "ok", limit: 0 }, { query: "ok", limit: 21 }, { query: "ok", limit: 1.5 }, { query: 2 }, { query: "ok", site: "__proto__" }]) await assert.rejects(callTool(service, "stackexchange_search", args), ReadError);
  await assert.rejects(callTool(service, "stackexchange_answer", { question_id: 0 }), ReadError);
  await assert.rejects(callTool(service, "unknown"), /unknown method/);
});
