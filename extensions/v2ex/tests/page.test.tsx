import { test } from "node:test";
import assert from "node:assert/strict";
import { Renderer, type Snapshot } from "@persona/extension/ui";
import { Hub } from "@persona/extension";
import { V2EX } from "../src/service.ts";
import { encodeState, page, readState, transition, loadPage } from "../src/page.tsx";
import { snapshotPage } from "../src/snapshot.ts";
import { root } from "../src/root.tsx";
import { callTool, connector, toolsFor } from "../src/tools.ts";
import { length, nodePath, topicPath } from "../src/model.ts";
import { Fixture, interaction, rawReplies, rawTopic, tick, until } from "./fixtures.ts";
const setup = (config: unknown = {}, extra: ConstructorParameters<typeof V2EX>[2] = {}) => {
  const fixture = new Fixture(); const service = new V2EX(fixture.fetch, config, { now: () => fixture.clock, monotonic: () => fixture.clock, locale: "en", ...extra, limits: { gap: 0, ...extra.limits } });
  return { fixture, service };
};
const body = (snapshot: Snapshot) => JSON.stringify(snapshot.view);
const items = (snapshot: Snapshot) => snapshot.view.sections.flatMap(s => s.items);
test("state defaults, list selection, all/filter and inline back preserve list identity", () => {
  const { service: s } = setup({ list: "latest" });
  const initial = readState("broken", s); assert.deepEqual(initial, { l: "latest", t: 0, a: false });
  assert.deepEqual(readState('{"l":"nodes"}', s), initial);
  const all = transition(initial, "all", s); const topic = transition(all, "topic:1", s);
  assert.deepEqual(transition(topic, "back", s), all);
  assert.deepEqual(transition(topic, "list", s, { list: "hot" }), { l: "hot", t: 0, a: true });
  assert.deepEqual(transition(topic, "topic:bad", s), topic);
  assert.deepEqual(readState(encodeState({ l: "latest", t: "9223372036854775807", a: true }), s), { l: "latest", t: "9223372036854775807", a: true });
});
test("snapshot export uses the original page ID contract and UI 0/1/2/3/5 navigation branches", async () => {
  const { fixture: f, service: s } = setup({ keywords: ["Go"], list: "latest", nodes: ["go", "../invalid"] });
  f.put("/topics/latest.json", [rawTopic(1, { title: "Synthetic Go article" }), rawTopic(2)]);
  const snapshot = snapshotPage(s), hub = new Hub(() => {});
  for (const version of [0, 1, 2, 3, 5]) {
    const req = { contribution: "topics", placement: "plugin_page", size: "", ui_version: version, hub, locale: "en", state: encodeState({ l: "latest", t: 0, a: false }) };
    const view = await snapshot.render(req);
    assert.equal(view.sections[0].items[0].type, version < 2 ? "row" : "segmented");
    const row = view.sections.flatMap(sec => sec.items).find(item => item.type === "row" && item.title === "Synthetic Go article");
    assert.ok(row && "action" in row); assert.equal(!!row.action?.push, version >= 3);
    const opened = await snapshot.action!({ ...req, action: "topic:1" });
    assert.ok(opened && opened.view); assert.ok(JSON.stringify(opened.view).includes("First paragraph\\nSecond paragraph"));
    assert.equal(JSON.stringify(opened.view).includes('"back"'), version < 3);
    const returned = await snapshot.action!({ ...req, state: opened.view.state ?? "", action: "back" });
    assert.ok(returned && returned.view); assert.equal(readState(returned.view.state, s).l, "latest");
    assert.ok(JSON.stringify(view).includes("Invalid node names"));
  }
});
test("list empty, filtered-empty, stale and partial-node notes remain distinct", async () => {
  const { fixture: f, service: s } = setup({ nodes: ["go", "missing"], keywords: ["no-match"] });
  const state = readState({ l: "nodes" }, s), render = (loaded: Awaited<ReturnType<typeof loadPage>>) => {
    const renderer = new Renderer({ authorize: () => false });
    try { return body(renderer.mount(page({ service: s, state, loaded, uiVersion: 5 }))); } finally { renderer.dispose(); }
  };
  const loaded = await loadPage(s, state); const filtered = render(loaded);
  assert.ok(filtered.includes("No topics match")); assert.ok(filtered.includes("Could not read these nodes"));
  f.put(nodePath("go"), []); f.clock += 300000; assert.ok(render(await loadPage(s, state)).includes("No readable topics"));
});
test("long Unicode topics, recent replies, duplicate IDs and text components fit native budgets", async () => {
  const { fixture: f, service: s } = setup();
  f.put(topicPath("1"), [rawTopic(1, { title: "😀".repeat(400), content: "😀".repeat(33000), replies: 50 })]);
  f.put("/replies/show.json?topic_id=1", rawReplies(50, 3000));
  const view = await snapshotPage(s).render({ contribution: "topics", placement: "plugin_page", size: "", ui_version: 5, hub: new Hub(() => {}), state: encodeState({ l: "hot", t: "1", a: false }) });
  assert.equal(length(view.title!), 201);
  assert.equal(view.sections[0].items.reduce((n, item) => n + (item.type === "text" && item.style === "body" ? length(item.text!) : 0), 0), 12001);
  assert.ok(JSON.stringify(view).includes("#31 fixture-user-31")); assert.ok(JSON.stringify(view).includes("#50 fixture-user-50")); assert.ok(!JSON.stringify(view).includes("#30 fixture-user-30"));
  for (const item of view.sections.flatMap(s => s.items)) if (item.type === "text") assert.ok(length(item.text!) <= 1901);
  assert.ok(!JSON.stringify(view).includes("DO-NOT-PROJECT-PROFILE"));
  f.put("/topics/hot.json", [rawTopic(1), rawTopic(1)]);
  const renderer = new Renderer({ authorize: () => false });
  try { assert.equal(items(renderer.mount(page({ service: s, state: readState({}, s), loaded: await loadPage(s, readState({}, s)) }))).filter(it => it.key === "topic-1").length, 1); }
  finally { renderer.dispose(); }
});
test("topic error retains URL/refresh, reply error retains body, page deadline cancels real work", async () => {
  const { fixture: f, service: s } = setup({}, { limits: { page: 10 } });
  const snapshot = snapshotPage(s), req = { contribution: "topics", placement: "plugin_page", size: "", ui_version: 1, hub: new Hub(() => {}), state: encodeState({ l: "hot", t: "99", a: false }) };
  const missing = await snapshot.render(req); assert.ok(JSON.stringify(missing).includes("https://www.v2ex.com/t/99")); assert.ok(JSON.stringify(missing).includes('"back"'));
  f.put("/replies/show.json?topic_id=1", {}, { status: 502 });
  const partial = await snapshot.render({ ...req, state: encodeState({ l: "hot", t: "1", a: false }) });
  assert.ok(JSON.stringify(partial).includes("First paragraph")); assert.ok(JSON.stringify(partial).includes("Could not read replies"));
  f.put("/topics/hot.json", [rawTopic()], { delay: 100 }); const start = Date.now();
  const loaded = await loadPage(s, readState({}, s)); assert.equal(loaded.kind, "error"); assert.ok(Date.now() - start < 200); assert.equal(f.active, 0);
});
test("root rejects stale list results and preserves server-side all/filter while opening inline", async () => {
  const { fixture: f, service: s } = setup({ keywords: ["Go"] });
  f.put("/topics/hot.json", [rawTopic(1, { title: "Synthetic Go article" }), rawTopic(2)], { delay: 100 });
  f.put("/topics/latest.json", [rawTopic(2, { title: "Synthetic topic 2 Go" })]);
  const renderer = new Renderer({ authorize: () => true });
  try {
    const initial = renderer.mount(root({ service: s, uiVersion: 1 }));
    await until(() => f.active, n => n === 1);
    await renderer.event(interaction(initial, "list-latest"));
    await until(() => renderer.current(), snap => body(snap).includes("Synthetic topic 2"));
    assert.equal(f.cancelled, 1); assert.equal(readState(renderer.current().view.state, s).l, "latest");
    assert.ok(!body(renderer.current()).includes("Synthetic Go article"));
  } finally { renderer.dispose(); }
});
test("unchanged cached resume keeps callback version stable; suspend/dispose cancel page work", async () => {
  const { fixture: f, service: s } = setup({ keywords: ["no-match"] });
  const renderer = new Renderer({ authorize: () => true });
  renderer.mount(root({ service: s, uiVersion: 1 }));
  await until(() => renderer.current(), snap => body(snap).includes('"all"'));
  renderer.suspend(); const resumed = renderer.resume(); await tick(10);
  assert.equal(renderer.current().version, resumed.version);
  await renderer.event(interaction(resumed, "all"));
  await until(() => renderer.current(), snap => body(snap).includes("topic-1")); renderer.dispose();
  const pending = setup(); pending.fixture.put("/topics/hot.json", [rawTopic()], { delay: 100 });
  const suspended = new Renderer({ authorize: () => true }); suspended.mount(root({ service: pending.service }));
  await until(() => pending.fixture.active, n => n === 1); suspended.suspend(); await until(() => pending.fixture.active, n => n === 0); suspended.dispose();
  assert.equal(pending.fixture.cancelled, 1); assert.equal(f.hits.get("/topics/hot.json"), 1);
});
test("all tools retain schema, filters and limits without exposing member profiles", async () => {
  const { fixture: f, service: s } = setup({ keywords: ["Go"], exclude_keywords: ["blocked"] });
  f.put("/topics/hot.json", [rawTopic(1, { title: "Go database" }), rawTopic(2, { title: "Go UI" }), rawTopic(3, { title: "Python database" })]);
  const filtered = await callTool(s, "v2ex_hot", { keywords: ["database"] }); assert.ok(filtered.includes("Go database")); assert.ok(!filtered.includes("Go UI")); assert.ok(!filtered.includes("Python database"));
  assert.ok((await callTool(s, "v2ex_hot", { filtered: false })).includes("Python database"));
  assert.ok((await callTool(s, "v2ex_hot", { source: "go" })).includes("V2EX node go"));
  const api = connector(s); assert.equal((await api("status", undefined) as any).state, "ok");
  assert.deepEqual((await api("tools.list", {}) as any).tools.map((t: any) => t.name), ["v2ex_hot", "v2ex_topic"]);
  await assert.rejects(Promise.resolve(api("status", { contribution: "missing" })), { code: "no_contribution" });
  await assert.rejects(Promise.resolve(api("unsupported", {})), { code: "unknown_method" });
  for (const args of [{ source: 1 }, { limit: 1.5 }, { keywords: [1] }, { source: "../bad" }]) await assert.rejects(callTool(s, "v2ex_hot", args));
  await assert.rejects(callTool(s, "v2ex_hot", { source: "nodes" }));
  assert.ok(!filtered.includes("DO-NOT-PROJECT-PROFILE"));
  for (const locale of ["en", "zh-Hans", "ja"]) assert.ok(toolsFor(locale)[1].description.length > 30);
});
test("tool limits clamp positive values but nonpositive defaults to 15", async () => {
  const { fixture: f, service: s } = setup(); f.put("/topics/hot.json", Array.from({ length: 40 }, (_, i) => rawTopic(i + 1)));
  const count = (text: string) => (text.match(/\(id \d+\)/g) ?? []).length;
  assert.equal(count(await callTool(s, "v2ex_hot", { limit: 99 })), 30); assert.equal(count(await callTool(s, "v2ex_hot", { limit: 0 })), 15); assert.equal(count(await callTool(s, "v2ex_hot", { limit: 1 })), 1);
});
test("tool reply budgets retain the newest floor under maximum Unicode title, metadata and body", async () => {
  const { fixture: f, service: s } = setup();
  f.put(topicPath("1"), [rawTopic(1, { title: "😀".repeat(500), content: "😀".repeat(10000), node: { name: "go", title: "😀".repeat(1000) }, replies: 100 })]);
  f.put("/replies/show.json?topic_id=1", rawReplies(100, 5000));
  const text = await callTool(s, "v2ex_topic", { topic: "/t/1", replies: 100 });
  assert.ok(length(text) <= 30000); assert.ok(text.includes("#51 fixture-user-51")); assert.ok(text.includes("#100 fixture-user-100")); assert.ok(text.trimEnd().endsWith("…"));
  assert.ok(!text.includes("#50 fixture-user-50"));
  assert.ok(!(await callTool(s, "v2ex_topic", { topic: 1, replies: 0 })).includes("#"));
});
test("tool deadline keeps partial nodes and reports stale/reply failures", async () => {
  const { fixture: f, service: s } = setup({ nodes: ["go", "python"] }, { limits: { tool: 10 } });
  f.put(nodePath("python"), [rawTopic(2)], { delay: 100 });
  const text = await callTool(s, "v2ex_hot", { source: "nodes" }); assert.ok(text.includes("Synthetic topic 1")); assert.ok(text.includes("Could not read these nodes")); assert.equal(f.active, 0);
  f.put("/replies/show.json?topic_id=1", {}, { status: 502 }); const topic = await callTool(s, "v2ex_topic", { topic: "1" }); assert.ok(topic.includes("First paragraph")); assert.ok(topic.includes("Could not read replies"));
});
