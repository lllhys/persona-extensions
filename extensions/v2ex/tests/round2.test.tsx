import { test } from "node:test";
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { connect, type Program } from "@persona/extension";
import { Renderer } from "@persona/extension/ui";
import { int64, parseArray, parseReplies, parseTopics, topicPath } from "../src/model.ts";
import { readState } from "../src/page.tsx";
import { root } from "../src/root.tsx";
import { V2EX, budget, limitPause } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { meta, textsFor, time } from "../src/i18n.ts";
import { Fixture, tick, until } from "./fixtures.ts";
import oracle from "./date-oracle.json";

const hot = "/topics/hot.json";
const setup = () => {
  const f = new Fixture();
  const s = new V2EX(f.fetch, {}, { now: () => f.clock, monotonic: () => f.clock, locale: "en", limits: { gap: 0 } });
  return { f, s };
};

test("unmodified syntax is validated before lossless decoding; malformed JSON never changes good cache", async () => {
  for (const body of ['[{"id":1,2:3}]', '[{"id":1,-0:3}]', '[{"id":01}]', '[{"id":+1}]', '[{"id":1,}]', '[{"id":1}] trailing', '[{"id":NaN}]', '[{"id":1/*x*/}]']) {
    assert.throws(() => JSON.parse(body));
    assert.throws(() => parseArray(body), { code: "bad_answer" });
    const { f, s } = setup(), first = await s.list("hot"), sync = s.status().last_sync, stats = s.cacheStats();
    f.clock += 300000; f.answers.set(hot, { body });
    const stale = await s.list("hot");
    assert.equal(stale.stale?.code, "bad_answer"); assert.deepEqual(stale.topics, first.topics);
    assert.equal(stale.at, first.at); assert.equal(s.status().last_sync, sync); assert.deepEqual(s.cacheStats(), stats);
    const cold = setup(); cold.f.answers.set(hot, { body });
    await assert.rejects(cold.s.list("hot"), { code: "bad_answer" }); assert.equal(cold.s.cacheStats().entries, 0);
  }
});

test("raw state preserves full positive int64 IDs and rejects fractional/exponent or malformed JSON", () => {
  const { s } = setup();
  for (const token of ["1.00000000000000001", "1e3", "1.0", "-0", "9223372036854775808"]) assert.equal(readState(`{"t":${token}}`, s).t, 0);
  for (const token of ["1", "9007199254740992", "9007199254740993", "9223372036854775807"]) assert.equal(readState(`{"t":${token}}`, s).t, token);
  assert.equal(readState('{"t":1,2:3}', s).t, 0);
  assert.equal(readState('{"t":1,"t":9223372036854775807}', s).t, "9223372036854775807");
});

test("all timestamp and reply-count fields accept exact int64 edges, preserve sorting and reject overflow", async () => {
  for (const token of ["9007199254740992", "9007199254740993", "9223372036854775807", "-9223372036854775808"]) {
    const topic = parseTopics(`[{"id":1,"created":${token},"last_touched":${token},"replies":${token}}]`)[0];
    for (const v of [topic.created, topic.lastTouched, topic.replies]) assert.equal(BigInt(v), BigInt(token));
    assert.equal(BigInt(parseReplies(`[{"created":${token}}]`)[0].created), BigInt(token));
  }
  for (const token of ["9223372036854775808", "-9223372036854775809", "1e3", "1.00000000000000001", '"1"']) {
    for (const field of ["created", "last_touched", "replies"]) assert.throws(() => parseTopics(`[{"${field}":${token}}]`), { code: "bad_answer" });
    assert.throws(() => parseReplies(`[{"created":${token}}]`), { code: "bad_answer" });
  }
  const f = new Fixture(), s = new V2EX(f.fetch, { nodes: ["a", "b"] }, { limits: { gap: 0 } });
  f.answers.set("/topics/show.json?node_name=a", { body: '[{"id":1,"last_touched":9007199254740992}]' });
  f.answers.set("/topics/show.json?node_name=b", { body: '[{"id":2,"last_touched":9007199254740993}]' });
  assert.deepEqual((await s.list("nodes")).topics.map(t => t.id), ["2", "1"]);
});

test("deleted is a RawMessage comparison, including minus zero, escapes, duplicates and internal whitespace", () => {
  for (const token of ["-0", '"\\u0030"', "0e0", "0.0", '" 0"', "{}", "[]"]) assert.equal(parseTopics(`[{"id":1,"deleted":${token}}]`).length, 0);
  for (const token of ["0", '"0"', '""', "false", "null"]) assert.equal(parseTopics(`[{"id":1,"deleted": \n ${token} \t }]`).length, 1);
  assert.equal(parseTopics('[{"deleted":0,"deleted":-0}]').length, 0);
  assert.equal(parseTopics('[{"deleted":-0,"deleted":0}]').length, 1);
  assert.equal(parseTopics('[{"unused":{"deleted":-0},"deleted":0}]').length, 1);
});

test("refusal quota uses the original integer lexeme and range; wrong types cannot pause the connector", async () => {
  for (const token of ["1.5", "1.00000000000000001", "1e3", "9223372036854775808", '"2"', "true", "{}", "[]"]) {
    const { f, s } = setup(), first = await s.list("hot"), sync = s.status().last_sync;
    f.clock += 300000; f.answers.set(hot, { body: `{"rate_limit":{"used":${token},"quota":1}}` });
    const stale = await s.list("hot");
    assert.equal(stale.stale?.code, "bad_answer"); assert.deepEqual(stale.topics, first.topics);
    assert.equal(s.status().state, "error"); assert.equal(s.cacheStats().pausedUntil, 0); assert.equal(s.status().last_sync, sync);
    await s.list("latest"); assert.equal(s.status().state, "ok");
  }
  for (const token of ["9007199254740993", "9223372036854775807"]) {
    const { f, s } = setup(); f.answers.set(hot, { body: `{"rate_limit":{"used":${token},"quota":${token}}}` });
    await assert.rejects(s.list("hot"), { code: "rate_limited" });
  }
  const { f, s } = setup();
  assert.equal(limitPause(new Headers(), 9223372036854775807n, f.clock), 600000, "Go's signed internal Unix edge is past, not an hour-long future pause");
  assert.equal(limitPause(new Headers({ "Retry-After": "9223372036854775807" }), 0, f.clock), 3600000);
  assert.equal(limitPause(new Headers(), 9007199254740993n, f.clock), 3600000);
  f.answers.set(hot, { body: '{"message":"rate limit","rate_limit":{"reset_at":1e999}}' });
  await assert.rejects(s.list("hot"), { code: "rate_limited" }); assert.equal(s.cacheStats().pausedUntil - f.clock, 600000);
});

test("wall rollback/forward never affects pacing, cache TTL, eviction or pause duration; last_sync stays wall time", async () => {
  const f = new Fixture(); let elapsed = 0;
  const s = new V2EX(f.fetch, {}, { now: () => f.clock, monotonic: () => elapsed, locale: "en", limits: { gap: 15 } });
  const first = await s.list("hot"), sync = s.status().last_sync;
  f.clock -= 3600000; elapsed += 15;
  const request = budget(undefined, 200);
  try { await s.list("latest", false, request.signal); } finally { request.dispose(); }
  assert.equal(f.hits.get("/topics/latest.json"), 1);
  assert.equal(s.status().last_sync, new Date(f.clock).toISOString()); assert.notEqual(s.status().last_sync, sync);
  f.clock += 7200000; elapsed += 1;
  assert.equal((await s.list("hot")).at, first.at); assert.equal(f.hits.get(hot), 1);
  f.clock -= 7200000; elapsed = 300000;
  await s.list("hot"); assert.equal(f.hits.get(hot), 2);
  f.answers.set("/topics/latest.json", { body: "[]", status: 429 }); elapsed += 15;
  await assert.rejects(s.get("/topics/latest.json", 0).then(r => { if (r.stale) throw r.stale; }), { code: "rate_limited" });
  f.clock += 86400000; elapsed += 599999; assert.equal(s.status().state, "rate_limited");
  f.clock -= 172800000; elapsed++; assert.equal(s.status().state, "ok");
});

test("full int64 dates and saturated relative ages match the real Go oracle, not zero or a guessed cycle", () => {
  for (const row of oracle) {
    assert.equal(time(BigInt(row.seconds) * 1000n, row.zone), row.formatted, `${row.seconds} ${row.zone}`);
    if (BigInt(row.seconds) > 0n && Number(row.age_days) > 0) assert.ok(meta(textsFor("en"), parseTopics(`[{"created":${row.seconds}}]`)[0], 1700000000000).includes(`${row.age_days} days ago`));
  }
});

test("large exact fields survive actual bundle tools, native sessions and unchanged resume", async () => {
  const bundle = await import(pathToFileURL(resolve("build/v2ex.mjs")).href), f = new Fixture();
  const s = new bundle.V2EX(f.fetch, {}, { locale: "en", timezone: "UTC", limits: { gap: 0 } });
  f.answers.set(topicPath("1"), { body: '[{"id":1,"title":"Exact fixture","content":"retained body","replies":9007199254740993,"created":9223372036854775807}]' });
  f.answers.set("/replies/show.json?topic_id=1", { body: '[{"id":1,"content":"retained reply","created":-9223372036854775808}]' });
  const text = await bundle.callTool(s, "v2ex_topic", { topic: "1" });
  assert.ok(text.includes("9007199254740993")); assert.ok(text.includes("retained body")); assert.ok(text.includes("retained reply"));
  for (const seconds of ["9223372036854775807", "-9223372036854775808"]) assert.ok(text.includes(oracle.find(r => r.seconds === seconds && r.zone === "UTC")!.formatted));
  // Use the separately bundled SDK renderer via the Program bridge for instance isolation.
  const frames: any[] = [], p = connect(bundle.createProgram(f.fetch, { limits: { gap: 0 } }) as Program, line => frames.push(JSON.parse(line)));
  const rpc = async (id: string, method: string, params: unknown = {}) => { p.line(JSON.stringify({ id, method, params })); await p.drain(); const frame = frames.find(v => v.id === id); assert.equal(frame.error, undefined); return frame.result; };
  try {
    await rpc("init", "initialize", { protocol: 1, hub: { timezone: "UTC", locale: "en" } });
    await rpc("mount", "ui.page.mount", { session: "exact", params: { contribution: "topics", state: '{"t":1}', ui_version: 1 } });
    let i = 0;
    const snapshot = await until(() => rpc(`snap${i++}`, "ui.page.snapshot", { session: "exact" }), s => JSON.stringify(s.view).includes("retained reply"));
    await rpc("suspend", "ui.page.suspend", { session: "exact" });
    const resume = await rpc("resume", "ui.page.resume", { session: "exact" }); await tick(15);
    const resumed = await rpc("final", "ui.page.snapshot", { session: "exact" });
    assert.equal(resumed.version, resume.version); assert.ok(JSON.stringify(resumed.view).includes("9007199254740993"));
    f.answers.set(topicPath("9223372036854775807"), { body: '[{"id":9223372036854775807,"content":"exact state body"}]' });
    await rpc("max-state", "ui.page.mount", { session: "max-state", params: { contribution: "topics", state: '{"t":9223372036854775807}', ui_version: 1 } });
    await until(() => rpc(`maxsnap${i++}`, "ui.page.snapshot", { session: "max-state" }), s => JSON.stringify(s.view).includes("exact state body"));
    for (const token of ["1.00000000000000001", "1e3"]) {
      const session = `invalid-state-${token}`;
      const mounted = await rpc(session, "ui.page.mount", { session, params: { contribution: "topics", state: `{"t":${token}}`, ui_version: 1 } });
      assert.equal(JSON.parse(mounted.view.state).t, 0);
    }
  } finally { p.close(); }
});

test("Local and older hubs without timezone preserve the runtime's local fallback", () => {
  const f = new Fixture(), s = new V2EX(f.fetch), local = new Intl.DateTimeFormat().resolvedOptions().timeZone;
  assert.equal(s.timezone, "Local"); assert.equal(s.zoneFor(), "Local");
  assert.equal(time(1700000000000, "Local"), time(1700000000000, local));
  assert.equal(new V2EX(f.fetch, { tz: "Local" }).zoneFor("UTC"), "Local");
});

test("visible RPC counter types and int64 ranges remain validated without shrinking them to safe integers", async () => {
  const { s } = setup();
  for (const limit of [9007199254740992, -9223372036854775808]) assert.ok(await callTool(s, "v2ex_hot", { limit }));
  for (const limit of [1.5, "1", true, [], 9223372036854775808, -18446744073709551616]) await assert.rejects(callTool(s, "v2ex_hot", { limit }), { code: "invalid" });
  assert.throws(() => int64(9223372036854775808), { code: "bad_answer" });
});

for (const [name, token, tool, field, shouldReject] of [
  ["fractional topic", "1.00000000000000001", "v2ex_topic", "topic", true],
  ["exponent topic", "1e3", "v2ex_topic", "topic", true],
  ["fractional limit", "1.00000000000000001", "v2ex_hot", "limit", true],
  ["full int64 numeric topic", "9223372036854775807", "v2ex_topic", "topic", false],
] as const) test(`RPC numeric lexemes: ${name}`, async () => {
  const bundle = await import(pathToFileURL(resolve("build/v2ex.mjs")).href), f = new Fixture(), frames: any[] = [];
  f.answers.set(topicPath("9223372036854775807"), { body: '[{"id":9223372036854775807}]' });
  f.answers.set(topicPath("1000"), { body: '[{"id":1000}]' });
  const p = connect(bundle.createProgram(f.fetch, { limits: { gap: 0 } }) as Program, line => frames.push(JSON.parse(line)));
  try {
    p.line('{"id":"init","method":"initialize","params":{"protocol":1}}'); await p.drain();
    p.line(`{"id":"numeric","method":"connector.tools.call","params":{"name":"${tool}","args":{"${field}":${token}}}}`); await p.drain();
    const result = frames.find(frame => frame.id === "numeric");
    if (shouldReject) { assert.ok(result.error, `SDK accepted ${name}; network hits=${f.requests.length}`); assert.equal(f.requests.length, 0); }
    else { assert.equal(result.error, undefined); assert.ok(result.result.text.includes("9223372036854775807")); }
  } finally { p.close(); }
});
