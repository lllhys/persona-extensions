import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { Renderer, type Snapshot } from "@persona/extension/ui";
import { connect, type Program } from "@persona/extension";
import { V2EX } from "../src/service.ts";
import { parseReplies, parseTopics, topicPath } from "../src/model.ts";
import { time } from "../src/i18n.ts";
import { root } from "../src/root.tsx";
import { Fixture, interaction, rawReplies, rawTopic, tick, until } from "./fixtures.ts";
import dateOracle from "./date-oracle.json";

const body = (snapshot: Snapshot) => JSON.stringify(snapshot.view);
const setup = () => {
  const fixture = new Fixture();
  const service = new V2EX(fixture.fetch, {}, { now: () => fixture.clock, monotonic: () => fixture.clock, locale: "en", limits: { gap: 0 } });
  return { fixture, service };
};

test("refresh is consumed once; list and topic resumes return to their original TTL", async () => {
  for (const topic of [false, true]) {
    const { fixture: f, service: s } = setup(), renderer = new Renderer({ authorize: () => true });
    const path = topic ? topicPath("1") : "/topics/hot.json";
    try {
      renderer.mount(root({ service: s, uiVersion: 1, state: topic ? { t: 1 } : undefined }));
      await until(() => renderer.current(), snap => body(snap).includes(topic ? "reply-meta-124-25" : "topic-1"));
      f.clock += 40000;
      await renderer.event(interaction(renderer.current(), "refresh"));
      await until(() => renderer.current(), snap => body(snap).includes('"refresh"') && !body(snap).includes('"loading"'));
      assert.equal(f.hits.get(path), 2);
      for (let i = 0; i < 3; i++) {
        renderer.suspend(); f.clock += 40000; renderer.resume(); await tick(15);
        assert.equal(f.hits.get(path), 2, "a consumed refresh must not survive resume");
      }
      renderer.suspend(); f.clock += topic ? 600000 : 300000; renderer.resume();
      await until(() => f.hits.get(path), n => n === 3);
    } finally { renderer.dispose(); }
  }
});

test("cancelled refresh stays pending, but a completed retry does not become a permanent policy", async () => {
  const { fixture: f, service: s } = setup(), renderer = new Renderer({ authorize: () => true });
  try {
    renderer.mount(root({ service: s })); await until(() => renderer.current(), snap => body(snap).includes("topic-1"));
    f.clock += 40000; f.put("/topics/hot.json", [rawTopic()], { delay: 100 });
    await renderer.event(interaction(renderer.current(), "refresh")); await until(() => f.active, n => n === 1);
    renderer.suspend(); await until(() => f.active, n => n === 0);
    f.put("/topics/hot.json", [rawTopic()]); renderer.resume();
    await until(() => renderer.current(), snap => body(snap).includes("topic-1"));
    assert.equal(f.hits.get("/topics/hot.json"), 3);
    renderer.suspend(); f.clock += 40000; renderer.resume(); await tick(15);
    assert.equal(f.hits.get("/topics/hot.json"), 3);
  } finally { renderer.dispose(); }
});

test("integer fields reject decimal and exponent lexemes before rounding, including deleted topics", () => {
  for (const token of ["1.00000000000000001", "1e3", "1.0", "0e0", "-0.0", "1E+0", "9007199254740991.1"]) {
    for (const field of ["id", "replies", "created", "last_touched"]) {
      assert.throws(() => parseTopics(`[{"${field}":${token}}]`), { code: "bad_answer" });
      assert.throws(() => parseTopics(`[{"deleted":true,"${field}":${token}}]`), { code: "bad_answer" });
    }
    for (const field of ["id", "created"]) assert.throws(() => parseReplies(`[{"${field}":${token}}]`), { code: "bad_answer" });
    for (const invalid of [`[${token}]`, `[{"node":${token}}]`, `[{"member":${token}}]`]) assert.throws(() => parseTopics(invalid), { code: "bad_answer" });
    for (const invalid of [`[${token}]`, `[{"member":${token}}]`]) assert.throws(() => parseReplies(invalid), { code: "bad_answer" });
  }
  // Unknown JSON numbers remain legal, just as Go ignores unprojected fields.
  assert.equal(parseTopics('[{"id":1,"unused":1e999,"title":"@v2ex-int:1e3"}]')[0].id, "1");
  assert.equal(parseTopics('[{"id":-0}]')[0].id, "0");
});

test("the original decimal/exponent counterexamples cannot replace good cache or report a live sync", async () => {
  for (const token of ["1.00000000000000001", "1e3"]) {
    const { fixture: f, service: s } = setup();
    const first = await s.list("hot"), sync = s.status().last_sync, bytes = s.cacheStats().bytes;
    f.clock += 300000;
    f.answers.set("/topics/hot.json", { body: `[{"id":${token}}]` });
    const stale = await s.list("hot");
    assert.equal(stale.stale?.code, "bad_answer"); assert.deepEqual(stale.topics, first.topics);
    assert.equal(stale.at, first.at); assert.equal(s.cacheStats().bytes, bytes);
    assert.equal(s.status().last_sync, sync); assert.equal(s.status().state, "error");
    const cold = setup(); cold.fixture.answers.set("/topics/hot.json", { body: `[{"id":${token}}]` });
    await assert.rejects(cold.service.list("hot"), { code: "bad_answer" });
    assert.equal(cold.service.cacheStats().entries, 0); assert.equal(cold.service.status().last_sync, undefined);
  }
});

test("extended timestamps match Go's Unix formatting across Date bounds, signs, precision and timezones", () => {
  // Generated by time.Unix(seconds, 0).In(location).Format("01-02 15:04").
  for (const row of dateOracle) assert.equal(time(BigInt(row.seconds) * 1000n, row.zone), row.formatted, `${row.seconds} ${row.zone}`);
});

test("actual bundle tools and native sessions retain out-of-Date-range topics and replies without disposal", async () => {
  const bundle = await import(pathToFileURL(resolve("build/v2ex.mjs")).href);
  const f = new Fixture(), frames: any[] = [];
  f.put(topicPath("1"), [rawTopic(1, { created: 9000000000000 })]);
  f.put("/replies/show.json?topic_id=1", rawReplies(1).map(reply => ({ ...reply, created: 9000000000000 })));
  const p = connect(bundle.createProgram(f.fetch, { now: () => f.clock, monotonic: () => f.clock, limits: { gap: 0 } }) as Program, line => frames.push(JSON.parse(line)));
  let id = 0;
  const call = async (method: string, params: unknown = {}) => {
    const request = String(++id); p.line(JSON.stringify({ id: request, method, params })); await p.drain();
    const frame = frames.find(frame => frame.id === request); assert.ok(frame); assert.equal(frame.error, undefined); return frame.result;
  };
  try {
    await call("initialize", { protocol: 1, config: {}, hub: { locale: "en", timezone: "UTC" } });
    const text = (await call("connector.tools.call", { name: "v2ex_topic", args: { topic: "1" } })).text;
    const expected = dateOracle.find(row => row.seconds === "9000000000000" && row.zone === "UTC")!.formatted;
    assert.ok(text.includes(expected)); assert.ok(text.includes("First paragraph")); assert.ok(text.includes("#1 fixture-user-1"));
    await call("ui.page.mount", { session: "extended-time", params: { contribution: "topics", state: '{"t":1}', locale: "en", ui_version: 1 } });
    const snapshot: Snapshot = await until(() => call("ui.page.snapshot", { session: "extended-time" }), snap => body(snap).includes("reply-meta-100-1"));
    assert.ok(body(snapshot).includes(expected));
    assert.equal((await call("ui.page.event", { session: "extended-time", ticket: "fixture-ticket", interaction: interaction(snapshot, "back") })).status, "accepted");
    await until(() => call("ui.page.snapshot", { session: "extended-time" }), snap => body(snap).includes("topic-1"));
    assert.equal((await call("connector.status")).state, "ok");
  } finally { p.close(); }
});

test("source manifest RPC describes the packaged Deno profile and never an absent Go executable", async () => {
  const bundle = await import(pathToFileURL(resolve("build/v2ex.mjs")).href), frames: any[] = [];
  const p = connect(bundle.default as Program, line => frames.push(JSON.parse(line)));
  try {
    p.line(JSON.stringify({ id: "manifest", method: "manifest" })); await p.drain();
    const result = frames.find(frame => frame.id === "manifest").result;
    const staged = JSON.parse(await readFile("build/package/persona-extension.json", "utf8"));
    const { sha256, ...descriptor } = staged.runtime.source;
    assert.equal(result.runtime.mode, "deno"); assert.equal(result.runtime.command, undefined);
    assert.deepEqual(result.runtime.source, descriptor); assert.match(sha256, /^[a-f0-9]{64}$/);
    for (const key of ["id", "version", "permissions", "configuration", "contributes"]) assert.deepEqual(result[key], staged[key]);
  } finally { p.close(); }
});

test("legacy immediate-suspend negative control cancels every cold read; uninterrupted resume settles", async () => {
  const { fixture: f, service: s } = setup(), renderer = new Renderer({ authorize: () => true });
  f.put("/topics/hot.json", [rawTopic()], { delay: 100 });
  let last: Snapshot | undefined;
  try {
    // Retain the four original premature suspensions. This raw Renderer is not
    // a snapshot host and must not manufacture topics from a cancelled read.
    for (let i = 0; i < 4; i++) {
      last = i ? renderer.resume() : renderer.mount(root({ service: s, uiVersion: 1 }));
      await until(() => f.active, active => active === 1);
      await tick(5); last = renderer.current(); renderer.suspend(); await tick(10);
      assert.ok(last && !body(last).includes("topic-1"));
      assert.equal(f.active, 0);
    }
    assert.equal(f.hits.get("/topics/hot.json"), 4); assert.equal(f.cancelled, 4);
    assert.equal(s.cacheStats().entries, 0);
    const pending = renderer.resume();
    assert.ok(!body(pending).includes("topic-1"));
    const settled = await until(() => renderer.current(), snapshot => body(snapshot).includes("topic-1"));
    assert.ok(settled.version > pending.version); assert.equal(f.hits.get("/topics/hot.json"), 5);
    assert.equal(f.cancelled, 4); assert.equal(f.active, 0);
    await renderer.event(interaction(settled, "topic-1"));
    await until(() => renderer.current(), snapshot => body(snapshot).includes("reply-meta-124-25"));
  } finally { renderer.dispose(); }
});
