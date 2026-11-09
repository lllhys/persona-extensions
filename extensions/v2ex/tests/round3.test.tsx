import { test } from "node:test";
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { connect, type Program } from "@persona/extension";
import { Renderer, type Snapshot } from "@persona/extension/ui";
import { decodeStruct, equalFold, filter, parseJSON, parseReplies, parseTopics, topicPath } from "../src/model.ts";
import simpleFold from "../src/simple-fold.json";
import { encodeState, page, readState } from "../src/page.tsx";
import { root } from "../src/root.tsx";
import { V2EX } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { Fixture, interaction, rawTopic, tick, until } from "./fixtures.ts";

const setup = (config: unknown = {}) => {
  const f = new Fixture(), s = new V2EX(f.fetch, config, { now: () => f.clock, monotonic: () => f.clock, locale: "en", limits: { gap: 0 } });
  return { f, s };
};
const badDuplicates = ['[{"id":"invalid","id":1}]', '[{"id":1.00000000000000001,"id":1}]', '[{"id":1,"ID":1e0}]', '[{"created":true,"created":1}]', '[{"node":{"name":1},"node":{"name":"go"}}]', '[{"member":{"username":1},"Member":{"username":"fixture"}}]'];

test("every known typed occurrence is checked, even when overwritten, deleted or case-varied", () => {
  for (const body of badDuplicates) {
    assert.throws(() => parseTopics(body), { code: "bad_answer" });
    assert.throws(() => parseTopics(body.replace('}]', ',"Deleted":true}]')), { code: "bad_answer" });
  }
  for (const body of ['[{"id":"invalid","id":1}]', '[{"created":1.5,"created":1}]', '[{"member":{"username":1},"member":{"username":"fixture"}}]']) assert.throws(() => parseReplies(body), { code: "bad_answer" });
  assert.equal(parseTopics('[{"id":1,"unused":"invalid","unused":1}]')[0].id, "1");
});

test("struct duplicates merge and scalar nulls preserve prior values; raw deletion uses final matching lexeme", () => {
  const topic = parseTopics('[{"id":1,"id":null,"TITLE":"retained","Title":null,"Node":{"name":"go"},"node":null,"node":{"title":"Go"},"Member":{"username":"fixture"},"member":null}]')[0];
  assert.equal(topic.id, "1"); assert.equal(topic.title, "retained"); assert.deepEqual(topic.node, { name: "go", title: "Go" }); assert.equal(topic.author, "fixture");
  for (const body of ['[{"id":1,"Deleted":true}]', '[{"id":1,"deleted":0,"Deleted":true}]', '[{"ID":1,"DELETED":-0}]', '[{"id":1,"Deleted":"\\u0030"}]']) assert.equal(parseTopics(body).length, 0);
  assert.equal(parseTopics('[{"ID":1,"Title":"fixture","Deleted":true,"deleted":null}]')[0].title, "fixture");
  assert.equal(parseTopics('[{"id":1,"last_touched":1,"laſt_touched":2}]')[0].lastTouched, 2);
});

test("duplicate type errors cannot replace good cache, advance sync or bypass cold validation", async () => {
  for (const body of badDuplicates) {
    const { f, s } = setup(), first = await s.list("hot"), sync = s.status().last_sync, stats = s.cacheStats();
    f.clock += 300000; f.answers.set("/topics/hot.json", { body });
    const stale = await s.list("hot");
    assert.equal(stale.stale?.code, "bad_answer"); assert.deepEqual(stale.topics, first.topics); assert.equal(stale.at, first.at);
    assert.equal(s.status().last_sync, sync); assert.equal(s.status().state, "error"); assert.deepEqual(s.cacheStats(), stats);
    const cold = setup(); cold.f.answers.set("/topics/hot.json", { body });
    await assert.rejects(cold.s.list("hot"), { code: "bad_answer" }); assert.equal(cold.s.cacheStats().entries, 0);
  }
});

test("refusal decoding retains partial integer values and merges repeated rate_limit objects exactly like Go", async () => {
  for (const body of ['{"rate_limit":{"used":1},"rate_limit":{"quota":1}}', '{"rate_limit":{"used":2,"used":1.5,"quota":1}}', '{"rate_limit":{"used":2,"used":null,"quota":1}}', '{"RATE_LIMIT":{"USED":2},"rate_limit":{"Quota":1}}', '{"message":"rate limit","message":false}']) {
    const { f, s } = setup(); f.answers.set("/topics/hot.json", { body });
    await assert.rejects(s.list("hot"), { code: "rate_limited" }); assert.equal(s.status().state, "rate_limited"); assert.equal(s.cacheStats().entries, 0);
  }
  const decoded = decodeStruct(parseJSON('{"used":2,"used":1.5,"used":"invalid","quota":1}'), { used: "integer", quota: "integer" });
  assert.equal(decoded.errors, 2); assert.equal(decoded.value.used, 2);
});

test("raw state rejects any known type error across all occurrences, preserving exact valid int64 navigation", () => {
  const { s } = setup({ list: "hot" });
  for (const raw of ['{"l":"latest","t":1,"a":"invalid"}', '{"l":1,"t":1}', '{"t":1.5,"t":1}', '{"t":1,"T":1e0}', '{"a":1,"a":true,"t":1}', '{"l":true,"l":"latest","t":1}', '{"t":"1"}', '{"t":1,"p":"invalid"}']) assert.deepEqual(readState(raw, s), { l: "hot", t: 0, a: false });
  assert.deepEqual(readState('{"L":"latest","T":1,"A":true}', s), { l: "latest", t: "1", a: true });
  assert.deepEqual(readState('{"l":"latest","l":null,"t":1,"t":null,"a":true,"a":null}', s), { l: "latest", t: "1", a: true });
  const exact = { l: "latest", t: "9223372036854775807", a: true } as const;
  assert.ok(encodeState(exact).includes('"t":9223372036854775807')); assert.deepEqual(readState(encodeState(exact), s), exact);
});

test("node exclusion covers all 1512 mappings from the genuine Go Unicode 17 oracle without full folding", () => {
  assert.equal(Object.keys(simpleFold).length, 1512);
  for (const [cp, representative] of Object.entries(simpleFold)) assert.ok(equalFold(String.fromCodePoint(Number(cp)), String.fromCodePoint(representative)), cp);
  for (const [a, b] of [["Σ", "ς"], ["K", "K"], ["s", "ſ"], ["ẞ", "ß"]]) assert.ok(equalFold(a, b));
  for (const [a, b] of [["İ", "i"], ["ı", "I"], ["ß", "ss"], ["ﬀ", "ff"], ["é", "é"]]) assert.equal(equalFold(a, b), false);
  assert.equal(filter(parseTopics('[{"id":1,"node":{"title":"ς"}}]'), { exclude_nodes: ["Σ"] }).length, 0);
});

test("annotations have their own bounded budget and cannot swallow topic IDs or URLs", async () => {
  const { f, s } = setup({ exclude_keywords: ["x".repeat(35000)] }); f.put("/topics/hot.json", [rawTopic(1)]);
  const text = await callTool(s, "v2ex_hot"); assert.ok(text.includes("(id 1)")); assert.ok(text.includes("https://www.v2ex.com/t/1"));
  f.put("/topics/latest.json", Array.from({ length: 30 }, (_, i) => rawTopic(i + 1, { title: "😀".repeat(300), content: "😀".repeat(160), node: { title: "😀".repeat(1000) } })));
  const all = await callTool(s, "v2ex_hot", { source: "latest", limit: 30 });
  assert.equal((all.match(/\(id \d+\)/g) ?? []).length, 30); assert.ok(all.includes("https://www.v2ex.com/t/30"));
});

test("epoch zero remains the oldest successful merged read and is displayed, not treated as missing", async () => {
  const { f, s } = setup({ nodes: ["go", "python"] }); f.clock = 0; await s.node("go"); f.clock = 100;
  const listing = await s.list("nodes"); assert.equal(listing.at, 0);
  const renderer = new Renderer({ authorize: () => false });
  try { assert.ok(JSON.stringify(renderer.mount(page({ service: s, state: { l: "nodes", t: 0, a: false }, loaded: { kind: "list", listing } })).view).includes("Read at")); }
  finally { renderer.dispose(); }
});

const oversized = () => Array.from({ length: 200 }, (_, i) => rawTopic(i + 1, { title: "😀".repeat(300), content: "", replies: 0, node: { name: "go", title: "😀".repeat(1900) } }));

test("only genuinely oversized lists paginate; every legal topic is reachable through live callbacks without disposal", async () => {
  for (const version of [1, 3, 5]) {
    const { f, s } = setup(); f.put("/topics/hot.json", oversized());
    const renderer = new Renderer({ authorize: () => true }), seen = new Set<string>();
    try {
      renderer.mount(root({ service: s, uiVersion: version }));
      await until(() => renderer.current(), snap => snap.view.sections.some(s => s.key === "pagination"));
      for (let n = 0; n < 200; n++) {
        const snapshot = renderer.current(); assert.ok(new TextEncoder().encode(JSON.stringify(snapshot)).length < 1048576);
        for (const item of snapshot.view.sections.flatMap(s => s.items)) if (item.key?.startsWith("topic-")) { seen.add(item.key); assert.ok("action" in item && item.action); }
        const next = snapshot.view.sections.flatMap(s => s.items).find(i => i.key === "next-batch");
        if (!next) break;
        await renderer.event(interaction(snapshot, "next-batch")); await tick();
      }
      assert.equal(seen.size, 200); assert.equal(f.hits.get("/topics/hot.json"), 1);
      assert.equal(renderer.takeErrors().length, 0);
      const last = renderer.current(), lastState = JSON.parse(last.view.state!);
      await renderer.event(interaction(last, "previous-batch")); await tick(); assert.equal(JSON.parse(renderer.current().view.state!).p, lastState.p - 1);
      renderer.suspend(); renderer.resume(); await tick(10); assert.equal(renderer.takeErrors().length, 0);
    } finally { renderer.dispose(); }
  }
  const { s } = setup(), listing = await s.list("hot"), renderer = new Renderer({ authorize: () => false });
  try { const snap = renderer.mount(page({ service: s, state: readState({}, s), loaded: { kind: "list", listing } })); assert.ok(!snap.view.sections.some(s => s.key === "pagination")); assert.equal(snap.view.sections.flatMap(s => s.items).filter(i => i.key?.startsWith("topic-")).length, 3); }
  finally { renderer.dispose(); }
});

test("actual default Program bundle survives the aggregate-frame counterexample, pages all topics and retains annotations", async () => {
  const bundle = await import(pathToFileURL(resolve("build/v2ex.mjs")).href), f = new Fixture(), frames: any[] = [];
  f.put("/topics/hot.json", oversized()); f.put(topicPath("200"), [rawTopic(200, { content: "last legal topic body", replies: 0 })]);
  const p = connect(bundle.createProgram(f.fetch, { limits: { gap: 0 } }) as Program, line => frames.push(JSON.parse(line))); let id = 0;
  const rpc = async (method: string, params: unknown = {}) => { const request = String(++id); p.line(JSON.stringify({ id: request, method, params })); await p.drain(); const frame = frames.find(f => f.id === request); assert.ok(frame); assert.equal(frame.error, undefined); return frame.result; };
  try {
    await rpc("initialize", { protocol: 1, config: { exclude_keywords: ["x".repeat(35000)] }, hub: { locale: "en" } });
    const text = (await rpc("connector.tools.call", { name: "v2ex_hot", args: {} })).text; assert.ok(text.includes("(id 1)")); assert.ok(text.includes("https://www.v2ex.com/t/1"));
    await rpc("ui.page.mount", { session: "budget", params: { contribution: "topics", ui_version: 1 } });
    let snapshot: Snapshot = await until(() => rpc("ui.page.snapshot", { session: "budget" }), s => s.view.sections.some((s: any) => s.key === "pagination"));
    const seen = new Set<string>();
    for (let n = 0; n < 200; n++) {
      for (const item of snapshot.view.sections.flatMap(s => s.items)) if (item.key?.startsWith("topic-")) seen.add(item.key);
      if (!snapshot.view.sections.flatMap(s => s.items).some(i => i.key === "next-batch")) break;
      const before = JSON.parse(snapshot.view.state!).p;
      const result = await rpc("ui.page.event", { session: "budget", ticket: "fixture-ticket", interaction: interaction(snapshot, "next-batch") }); assert.equal(result.status, "accepted");
      snapshot = await until(() => rpc("ui.page.snapshot", { session: "budget" }), s => JSON.parse(s.view.state).p === before + 1);
    }
    assert.equal(seen.size, 200); assert.equal((await rpc("connector.status")).state, "ok");
    await rpc("ui.page.event", { session: "budget", ticket: "fixture-ticket", interaction: interaction(snapshot, "topic-200") });
    await until(() => rpc("ui.page.snapshot", { session: "budget" }), s => JSON.stringify(s.view).includes("last legal topic body"));
    await rpc("ui.page.suspend", { session: "budget" }); await rpc("ui.page.resume", { session: "budget" }); await tick(10);
    assert.ok(JSON.stringify((await rpc("ui.page.snapshot", { session: "budget" })).view).includes("last legal topic body"));
  } finally { p.close(); }
});
