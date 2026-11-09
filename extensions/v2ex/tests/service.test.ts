import { test } from "node:test";
import assert from "node:assert/strict";
import { V2EX, budget, limitPause } from "../src/service.ts";
import { ReadError, limits, nodePath, topicPath, repliesPath } from "../src/model.ts";
import { Fixture, rawTopic, tick, until } from "./fixtures.ts";
const hot = "/topics/hot.json", latest = "/topics/latest.json";
const setup = (config: unknown = {}, options: ConstructorParameters<typeof V2EX>[2] = {}) => {
  const fixture = new Fixture();
  const service = new V2EX(fixture.fetch, config, { now: () => fixture.clock, monotonic: () => fixture.clock, locale: "en", ...options, limits: { gap: 0, ...options.limits } });
  return { fixture, service };
};
test("production timing, resource and quota limits are not weakened", () => {
  assert.deepEqual(limits, { gap: 1000, refresh: 30000, list: 300000, topic: 600000, request: 20000, page: 15000, tool: 25000, pause: 600000, pauseMost: 3600000, entries: 200, bytes: 32 << 20, response: 8 << 20, quota: 20 });
});
test("TTL and refresh floor apply to lists, topics and replies", async () => {
  const { fixture: f, service: s } = setup();
  const first = await s.list("hot"); assert.equal(f.hits.get(hot), 1);
  f.clock += 29000; await s.list("hot", true); assert.equal(f.hits.get(hot), 1);
  f.clock += 1000; await s.list("hot", true); assert.equal(f.hits.get(hot), 2);
  f.clock += 299999; assert.equal((await s.list("hot")).at, first.at + 30000);
  f.clock++; await s.list("hot"); assert.equal(f.hits.get(hot), 3);
  await s.thread("1"); f.clock += 599999; await s.thread("1"); assert.equal(f.hits.get(topicPath("1")), 1);
  f.clock++; await s.thread("1"); assert.equal(f.hits.get(topicPath("1")), 2); assert.equal(f.hits.get(repliesPath("1")), 2);
  f.clock += 30000; await s.thread("1", true, true); assert.equal(f.hits.get(topicPath("1")), 3); assert.equal(f.hits.get(repliesPath("1")), 3);
});
test("one shared cancellable FIFO gate rechecks the cache after waiting", async () => {
  const { fixture: f, service: s } = setup();
  f.put(hot, [rawTopic()], { delay: 25 });
  await Promise.all(Array.from({ length: 12 }, () => s.list("hot")));
  assert.equal(f.hits.get(hot), 1); assert.equal(f.mostActive, 1);
  f.clock += 300000; const first = s.list("hot"); await until(() => f.active, n => n === 1);
  const cancel = new AbortController(); const queued = s.list("latest", false, cancel.signal);
  cancel.abort(); await assert.rejects(queued, { name: "AbortError" });
  await first; assert.equal(f.hits.get(latest), undefined);
  await s.list("latest"); assert.equal(f.hits.get(latest), 1); assert.equal(s.status().state, "ok");
});
test("requests are serialized, paced, read-only and restricted to the known public origin", async () => {
  const { fixture: f, service: s } = setup({}, { limits: { gap: 15 } });
  await Promise.all([s.list("hot"), s.list("latest"), s.node("go")]);
  assert.equal(f.mostActive, 1);
  for (const [i, r] of f.requests.entries()) {
    assert.equal(r.init.method, "GET"); assert.equal(r.init.redirect, "error");
    assert.equal(new Headers(r.init.headers).get("Accept"), "application/json");
    assert.equal(new Headers(r.init.headers).get("User-Agent"), "persona-v2ex/0.1.0");
    if (i) assert.ok(r.start - f.requests[i - 1].end! >= 12);
  }
  await assert.rejects(s.node("../unsafe"), { code: "no_node" }); assert.equal(f.requests.length, 3);
});
test("waiting for pacing can be cancelled without a request or status failure", async () => {
  const { fixture: f, service: s } = setup({}, { limits: { gap: 100 } });
  await s.list("hot"); const controller = new AbortController();
  const pending = s.list("latest", false, controller.signal); await tick(4); controller.abort();
  await assert.rejects(pending, { name: "AbortError" }); assert.equal(f.hits.get(latest), undefined); assert.equal(s.status().state, "ok");
});
test("timeouts abort actual network work and classify slow; caller cancellation is not stale fallback", async () => {
  const { fixture: f, service: s } = setup({}, { limits: { request: 10 } });
  await s.list("hot"); f.clock += 300000; f.put(hot, [rawTopic()], { delay: 100 });
  const stale = await s.list("hot"); assert.equal(stale.stale?.code, "slow"); assert.equal(f.cancelled, 1); assert.equal(f.active, 0);
  f.clock += 1; const controller = new AbortController(); const pending = s.list("hot", true, controller.signal);
  await until(() => f.active, n => n === 1); controller.abort();
  await assert.rejects(pending, { name: "AbortError" }); assert.equal(f.active, 0);
  const cancelled = new AbortController(); cancelled.abort(); await assert.rejects(s.list("hot", false, cancelled.signal), { name: "AbortError" });
});
test("body reading respects cancellation and maximum response bytes", async () => {
  let bodyCancelled = false;
  const s = new V2EX(async () => new Response(new ReadableStream<Uint8Array>({
    start(controller) { controller.enqueue(new TextEncoder().encode("[")); }, cancel() { bodyCancelled = true; },
  })), {}, { limits: { gap: 0, request: 10 } });
  await assert.rejects(s.list("hot"), { code: "slow" }); assert.ok(bodyCancelled);
  const { fixture: f, service: bounded } = setup({}, { limits: { response: 200 } });
  f.put(hot, []); await bounded.list("hot"); f.clock += 300000; f.put(hot, [rawTopic(1, { content: "x".repeat(1000) })]);
  const stale = await bounded.list("hot"); assert.equal(stale.stale?.code, "bad_answer"); assert.equal(bounded.cacheStats().bytes, 2);
});
test("invalid arrays, truncated JSON and failed responses never overwrite a valid cached answer", async () => {
  for (const [body, status, code] of [["<html>maintenance</html>", 200, "bad_answer"], ['[{"id":1,"title":', 200, "bad_answer"], ['{"status":"ok"}', 200, "bad_answer"], ['[{"id":"1"}]', 200, "bad_answer"], ["Just a moment", 403, "challenged"], ["Just a moment", 200, "challenged"], ["{}", 502, "http"]] as const) {
    const { fixture: f, service: s } = setup(); const first = await s.list("hot"); f.clock += 300000;
    f.answers.set(hot, { body, status }); const stale = await s.list("hot");
    assert.equal(stale.stale?.code, code); assert.equal(stale.at, first.at); assert.equal(stale.topics.length, 3); assert.equal(s.status().state, "error");
    assert.equal(!!s.status().guide, code === "challenged");
    f.clock++; f.put(hot, [rawTopic(7)]); const live = await s.list("hot");
    assert.equal(live.stale, undefined); assert.equal(live.topics[0].id, "7"); assert.equal(s.status().state, "ok");
  }
});
test("404 and cancellation cannot use old data or change connector failure state", async () => {
  const { fixture: f, service: s } = setup(); await s.list("hot"); const sync = s.status().last_sync; f.clock += 300000;
  f.put(hot, {}, { status: 404 }); await assert.rejects(s.list("hot"), { code: "not_found" });
  assert.equal(s.status().state, "ok"); assert.equal(s.status().last_sync, sync);
  await assert.rejects(s.thread("999"), { code: "no_topic" }); await assert.rejects(s.node("missing"), { code: "no_node" });
  assert.equal(s.status().state, "ok");
});
test("last_sync moves only on a live read and cache hits do not erase newer failures", async () => {
  const { fixture: f, service: s } = setup(); await s.list("hot"); const sync = s.status().last_sync;
  f.clock += 1000; f.put(latest, {}, { status: 502 }); await assert.rejects(s.list("latest"));
  await s.list("hot"); assert.equal(s.status().last_sync, sync); assert.equal(s.status().state, "error");
  f.clock++; f.put(latest, [rawTopic()]); await s.list("latest"); assert.equal(s.status().state, "ok"); assert.notEqual(s.status().last_sync, sync);
});
test("refusal priority, Retry-After/reset bounds and expired pauses match the old quota policy", async () => {
  const now = 1700000000000;
  for (const [retry, reset, expected] of [["", 0, 600000], ["120", (now + 1200000) / 1000, 120000], ["5", 0, 60000], ["86400", 0, 3600000], ["Wed, 21 Oct 2026 07:28:00 GMT", 0, 600000], ["", (now + 1200000) / 1000, 1200000], ["", (now + 18000000) / 1000, 3600000], ["", (now - 60000) / 1000, 600000]] as const)
    assert.equal(limitPause(new Headers({ "Retry-After": retry }), reset, now), expected);
  for (const [status, value] of [[429, []], [403, { message: "Rate Limit Exceeded" }], [200, { message: "rate limit exceeded" }], [404, { message: "Object Not Found", rate_limit: { quota: 600, used: 600 } }]] as const) {
    const { fixture: f, service: s } = setup(); f.put(hot, value, { status, headers: { "Retry-After": "120" } });
    await assert.rejects(s.list("hot"), { code: "rate_limited" }); assert.equal(s.status().state, "rate_limited");
    assert.equal(s.cacheStats().pausedUntil - f.clock, 120000);
    await assert.rejects(s.list("latest"), { code: "rate_limited" }); assert.equal(f.hits.get(latest), undefined);
    f.clock += 120000; await s.list("latest"); assert.equal(s.status().state, "ok");
  }
});
test("quota headers only apply to origin responses and never invalidate the current success", async () => {
  for (const [cdn, left, paused] of [["", "5", true], ["MISS", "19", true], ["BYPASS", "3", true], ["DYNAMIC", "0", true], ["EXPIRED", "3", true], ["HIT", "1", false], ["STALE", "1", false], ["", "20", false], ["", "", false]] as const) {
    const { fixture: f, service: s } = setup(); f.put(hot, [rawTopic()], { headers: { "Cf-Cache-Status": cdn, "X-Rate-Limit-Remaining": left, "X-Rate-Limit-Reset": String((f.clock + 900000) / 1000) } });
    assert.equal((await s.list("hot")).topics.length, 1); assert.equal(s.status().state === "rate_limited", paused);
    if (paused) { await assert.rejects(s.list("latest"), { code: "rate_limited" }); assert.equal(f.hits.get(latest), undefined); }
  }
});
test("paused refresh uses stale data, while younger cached data remains usable", async () => {
  const { fixture: f, service: s } = setup(); const first = await s.list("hot"); f.clock += 300000;
  f.put(hot, [], { status: 429, headers: { "Retry-After": "120" } });
  const stale = await s.list("hot"); assert.equal(stale.at, first.at); assert.equal(stale.stale?.code, "rate_limited");
  const count = f.requests.length; await s.list("hot", true); assert.equal(f.requests.length, count);
});
test("node lists merge sequentially, deduplicate, sort stably and retain the oldest part/missed nodes", async () => {
  const { fixture: f, service: s } = setup({ nodes: ["go", "python", "missing"] });
  const first = await s.node("go"); f.clock += 1000; const merged = await s.list("nodes");
  assert.deepEqual(merged.topics.map(t => t.id), ["4", "2", "1"]); assert.equal(merged.at, first.at);
  assert.deepEqual(merged.missed.map(m => m.name), ["missing"]); assert.equal(f.hits.get(nodePath("go")), 1);
  f.clock += 300000; f.put(nodePath("go"), [rawTopic(8, { last_touched: 42 }), rawTopic(7, { last_touched: 42 })]); f.put(nodePath("python"), [rawTopic(6, { last_touched: 42 })]);
  assert.deepEqual((await s.list("nodes")).topics.map(t => t.id), ["8", "7", "6"]);
});
test("multi-node deadline yields partial data, cancels remaining network and the next attempt advances", async () => {
  const { fixture: f, service: s } = setup({ nodes: ["go", "python", "n1", "n2", "n3"] });
  for (const name of ["go", "python", "n1", "n2", "n3"]) f.put(nodePath(name), [rawTopic()], { delay: name === "go" ? 0 : 100 });
  const request = budget(undefined, 20); let merged;
  try { merged = await s.list("nodes", false, request.signal); } finally { request.dispose(); }
  assert.equal(merged.topics.length, 1); assert.deepEqual(merged.missed.map(m => m.name), ["python", "n1", "n2", "n3"]);
  assert.ok(merged.missed.every(m => m.error.code === "slow")); assert.equal(f.requests.length, 2); assert.equal(f.active, 0);
  for (const name of ["python", "n1", "n2", "n3"]) f.put(nodePath(name), [rawTopic()]);
  assert.equal((await s.list("nodes")).missed.length, 0); assert.equal(f.hits.get(nodePath("go")), 1);
});
test("thread keeps its body with missing or stale replies and skips unwanted/zero replies", async () => {
  const { fixture: f, service: s } = setup(); await s.thread("1", false); assert.equal(f.hits.get(repliesPath("1")), undefined);
  f.put(repliesPath("1"), {}, { status: 502 }); const thread = await s.thread("1"); assert.equal(thread.topic.id, "1"); assert.equal(thread.repliesErr?.code, "http");
  f.put(topicPath("2"), [rawTopic(2, { replies: 0 })]); await s.thread("2"); assert.equal(f.hits.get(repliesPath("2")), undefined);
  f.clock++; f.put(repliesPath("1"), []); await s.thread("1"); f.clock += 600000; f.put(repliesPath("1"), {}, { status: 502 });
  const stale = await s.thread("1"); assert.equal(stale.stale?.code, "http"); assert.equal(stale.repliesErr, undefined);
});
test("cache count, bytes, replacement accounting and oversized single entries stay bounded", async () => {
  const { fixture: f, service: s } = setup({}, { limits: { entries: 3, bytes: 600 } });
  for (const name of ["a", "b", "c", "d"]) { f.put(nodePath(name), []); await s.node(name); f.clock++; }
  assert.deepEqual(s.cacheStats(), { entries: 3, bytes: 6, pausedUntil: 0 });
  f.put(nodePath("d"), [rawTopic(1, { content: "x".repeat(100) })]); await s.node("d", 0);
  assert.ok(s.cacheStats().bytes <= 600); assert.ok(s.cacheStats().bytes > 100);
  f.put(nodePath("d"), []); await s.node("d", 0); assert.equal(s.cacheStats().bytes, 6);
  f.put(nodePath("d"), [rawTopic(1, { content: "x".repeat(1000) })]); await s.node("d", 0);
  assert.equal(s.cacheStats().entries, 2); assert.equal(s.cacheStats().bytes, 4);
  const count = f.hits.get(nodePath("a"))!; await s.node("a"); assert.equal(f.hits.get(nodePath("a")), count + 1);
});
test("configuration fallback and timezone priority do not depend on the host filesystem", () => {
  const { service: s } = setup({ list: "nodes", nodes: ["../bad"] }, { timezone: "UTC" });
  assert.equal(s.offers("nodes"), false); assert.equal(s.zoneFor("Asia/Shanghai"), "Asia/Shanghai"); assert.equal(s.zoneFor("Mars/Olympus"), "UTC");
  const { service: configured } = setup({ tz: "UTC" }, { timezone: "Asia/Tokyo" }); assert.equal(configured.zoneFor("Asia/Shanghai"), "UTC");
});
