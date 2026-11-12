import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { cp, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { connect, type Program } from "@persona/extension";
import type { Snapshot, UIEvent } from "@persona/extension/ui";
import { Fixture, rss } from "./fixtures.ts";

interface Frame { id?: string; event?: string; result?: any; error?: { code?: string; message: string } }
const bundle = () => import(pathToFileURL(resolve("build/linuxdo.mjs")).href);
async function setup(config: unknown = { keywords: ["Rust"] }, initialize = true) {
  const module = await bundle(), fixture = new Fixture(), frames: Frame[] = [];
  // connect and the Program have separately bundled SDK instances. The Program
  // must construct its own renderer or server hooks cannot share their context.
  const p = connect(module.createProgram(fixture.fetch, { gap: 0, now: () => fixture.time }) as Program, line => frames.push(JSON.parse(line)));
  let id = 0;
  const call = async (method: string, params: unknown = {}): Promise<Frame> => {
    const request = String(++id);
    p.line(JSON.stringify({ id: request, method, params })); await p.drain();
    const frame = frames.find(frame => frame.id === request); assert.ok(frame); return frame;
  };
  if (initialize) assert.ok((await call("initialize", { protocol: 1, config, hub: { timezone: "Asia/Tokyo", locale: "en" } })).result);
  return { module, fixture, frames, p, call };
}
async function until(read: () => Promise<Frame>, predicate: (snapshot: Snapshot) => boolean): Promise<Snapshot> {
  const deadline = Date.now() + 3000;
  while (Date.now() < deadline) {
    const frame = await read(); assert.equal(frame.error, undefined);
    if (predicate(frame.result)) return frame.result;
    await new Promise(resolve => setTimeout(resolve, 2));
  }
  throw new Error("Program session did not settle");
}
const body = (snapshot: Snapshot) => JSON.stringify(snapshot.view);
function interaction(snapshot: Snapshot, key: string, values: Record<string, string> = {}): UIEvent {
  const item = snapshot.view.sections.flatMap(section => section.items).find(item => item.key === key);
  assert.ok(item && "action" in item && item.action);
  return { generation: snapshot.generation, version: snapshot.version, key, actionId: item.action.id, eventId: crypto.randomUUID(), values };
}
test("exact raw tools retain scalar/pointer null rules, ordered errors, mixed topic strings and signed bounds", async () => {
  const h = await setup({});
  const cases = JSON.parse(await readFile("tests/exact-tool-cases.json", "utf8")) as { args: string; ok: boolean }[];
  try {
    for (const [index, tc] of cases.entries()) {
      const id = `exact-control-${index}`;
      h.p.line(`{"id":"${id}","method":"connector.tools.call","params":{"name":"linuxdo_topic","args":${tc.args}}}`);
      await h.p.drain();
      const frame = h.frames.find(frame => frame.id === id); assert.ok(frame);
      assert.equal(!frame.error, tc.ok, `exact tool case ${index}`);
    }
    console.log(JSON.stringify({ contract: "#1201", originalToolControls: cases.length, skipped: 0 }));
  } finally { h.p.close(); }
});

const pause = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));

test("local source profile: released identity/config/grants unchanged, import-free default Program and hash", async () => {
  const manifest = JSON.parse(await readFile("build/package/persona-extension.json", "utf8"));
  const released = JSON.parse(await readFile("persona-extension.json", "utf8"));
  const bytes = await readFile("build/package/" + manifest.runtime.source.bundle);
  assert.equal(manifest.runtime.source.sha256, createHash("sha256").update(bytes).digest("hex"));
  assert.equal(manifest.runtime.mode, "deno"); assert.equal(manifest.runtime.command, undefined);
  if (released.runtime.mode === "deno") assert.equal(released.runtime.command, undefined);
  else { assert.ok(released.runtime.command); assert.equal(released.runtime.mode, undefined); }
  for (const key of Object.keys(released).filter(key => !["runtime", "tags", "replaces", "icon"].includes(key))) assert.deepEqual(manifest[key], released[key]);
  for (const key of ["tags", "replaces", "icon"]) assert.equal(manifest[key], undefined);
  assert.deepEqual(manifest.contributes.ui.map((ui: { id: string }) => ui.id), ["hot"]);
  const module = await bundle();
  assert.equal(module.default.id, released.id); assert.equal(module.default.version, released.version);
  assert.equal(module.default.fetch, undefined); assert.equal(typeof module.default.init, "function");
  assert.deepEqual(Object.keys(module.default.ui), ["hot"]); assert.equal(module.default.ui.hot.componentPage, true);
  assert.deepEqual(Object.keys(module.default.namespaces), ["connector"]);
  assert.ok(bytes.length < 8 << 20);
  for (const path of ["src/index.ts", "package.json", "package-lock.json", "src/locales/en.json", "src/locales/zh-Hans.json", "src/locales/ja.json"]) await readFile("build/package/extensions/linuxdo/" + path);
});

test("upstream #1184 integration: cold HTTP turns must eventually show topics", async () => {
  const h = await setup({}); h.fixture.delay = 200;
  try {
    const shown: Snapshot[] = [];
    // Exact mount/resume -> snapshot -> suspend lifecycle independently reproduced
    // through the real manager HTTP bridge. ui_version is not a transport selector.
    for (let turn = 0; turn < 3; turn++) {
      const response = turn === 0
        ? await h.call("ui.page.mount", { session: "cold-http", transport: "snapshot", params: { contribution: "hot", locale: "en", ui_version: 1 } })
        : await h.call("ui.page.resume", { session: "cold-http" });
      assert.equal(response.error, undefined); shown.push(response.result);
      await h.call("ui.page.suspend", { session: "cold-http" }); await pause(250);
    }
    assert.ok(shown.every(snapshot => body(snapshot).includes("topic-1")),
      `cold HTTP never resolved: requests=${h.fixture.hits.get("/hot.rss") ?? 0}, cancelled=${h.fixture.cancelled}, active=${h.fixture.active}`);
    assert.equal(h.fixture.hits.get("/hot.rss"), 1); assert.equal(h.fixture.cancelled, 0); assert.equal(h.fixture.active, 0);
    console.log(JSON.stringify({ contract: "#1184", contentTurns: shown.length, requests: h.fixture.hits.get("/hot.rss"), cancelled: h.fixture.cancelled, active: h.fixture.active }));
  } finally { h.p.close(); }
});

test("upstream #1201 numeric lexemes: raw fractional/exponent tool arguments must be rejected", async () => {
  const h = await setup({}); const accepted: string[] = [];
  try {
    for (const token of ["1.0000000000000001", "1e0", "1.0"]) {
      for (const field of ["topic", "replies"]) {
        const id = `raw-${field}-${token}`;
        const args = field === "topic" ? `{"topic":${token},"replies":1}` : `{"topic":1,"replies":${token}}`;
        const raw = `{"id":${JSON.stringify(id)},"method":"connector.tools.call","params":{"name":"linuxdo_topic","args":${args}}}`;
        h.p.line(raw);
        await h.p.drain();
        const frame = h.frames.find(frame => frame.id === id); assert.ok(frame);
        console.log(JSON.stringify({ contract: "#1201", raw, expected: "integer decoder error before topic fetch", actual: frame }));
        if (!frame.error) accepted.push(`${field}=${token}`);
      }
    }
    const precisionBlocked: string[] = [];
    for (const token of ["9007199254740993", "9223372036854775807"]) {
      const id = `raw-int64-${token}`;
      const doc = JSON.stringify(h.fixture.bodies.get("/t/topic/1.json")).replace('"id":1,', `"id":${token},`);
      h.fixture.bodies.set(`/t/topic/${token}.json`, doc);
      const raw = `{"id":${JSON.stringify(id)},"method":"connector.tools.call","params":{"name":"linuxdo_topic","args":{"topic":${token}}}}`;
      h.p.line(raw);
      await h.p.drain();
      const frame = h.frames.find(frame => frame.id === id); assert.ok(frame);
      console.log(JSON.stringify({ contract: "#1201", raw, expected: `success with /t/topic/${token}`, actual: frame }));
      if (frame.error || !frame.result?.text.includes(`/t/topic/${token}`)) precisionBlocked.push(token);
    }
    console.log(`upstream numeric gate: accepted=${accepted.length}/6; ${accepted.join(", ")}; legal int64 blocked=${precisionBlocked.length}/2`);
    assert.deepEqual(accepted, [], "SDK already discarded the lexemes before extension validation");
    assert.deepEqual(precisionBlocked, [], "a conservative safe-ID guard is not full original int64 RPC support");
  } finally { h.p.close(); }
});

test("bundled protocol initialization/manifest/contributions and normal direct component-page unsupported", async () => {
  const h = await setup({}, false);
  try {
    for (const method of ["ping", "connector.status", "ui.page.supported"]) assert.equal((await h.call(method)).error?.code, "not_initialized");
    assert.equal((await h.call("initialize", { protocol: 999 })).error?.code, "wrong_protocol");
    assert.ok((await h.call("initialize", { protocol: 1, config: { tz: "Invalid/Zone" } })).error);
    const result = (await h.call("initialize", { protocol: 1, config: {}, hub: { locale: "en", timezone: "UTC" } })).result;
    const manifest = JSON.parse(await readFile("persona-extension.json", "utf8"));
    assert.equal(result.id, manifest.id); assert.equal(result.version, manifest.version); assert.deepEqual(result.contributes, manifest.contributes);
    const packaged = JSON.parse(await readFile("build/package/persona-extension.json", "utf8"));
    delete packaged.runtime.source.sha256; delete packaged.tags; delete packaged.replaces; delete packaged.icon;
    assert.deepEqual((await h.call("manifest")).result, packaged);
    assert.deepEqual((await h.call("ping")).result, {});
    assert.deepEqual((await h.call("ui.page.supported")).result, { contributions: ["hot"] });
    for (const version of [1, 3, 5]) {
      for (const method of ["ui.render", "ui.action"]) assert.equal((await h.call(method, { contribution: "hot", ui_version: version })).error?.code, "unsupported");
    }
    // Legacy HTTP fallback is a manager responsibility; this SDK test is not an
    // installed hub acceptance claim and does not add alternate navigation IDs.
    assert.equal((await h.call("ui.render", { contribution: "other" })).error?.code, "no_contribution");
    assert.equal((await h.call("connector.status", { contribution: "other" })).error?.code, "no_contribution");
    for (const method of ["connector.inbox.collect", "ui.missing", "missing"]) assert.equal((await h.call(method)).error?.code, "unknown_method");
    assert.equal(h.fixture.calls.length, 0);
  } finally { h.p.close(); }
});

test("same native hot page preserves UI 1/2/3/5 inline topic/back state and request timezone", async () => {
  const h = await setup({});
  try {
    for (const version of [1, 2, 3, 5]) {
      const session = `version-${version}`;
      await h.call("ui.page.mount", { session, params: { contribution: "hot", locale: "en", ui_version: version, tz: "Asia/Shanghai", state: '{"l":"latest","a":true}' } });
      let snapshot = await until(() => h.call("ui.page.snapshot", { session }), s => body(s).includes("Latest · all 5"));
      assert.equal(snapshot.view.sections[0].items[0].type, version < 2 ? "row" : "segmented");
      const forward = (await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "topic-1") })).result;
      assert.equal(forward.status, "accepted"); assert.equal(forward.result.push, undefined);
      snapshot = await until(() => h.call("ui.page.snapshot", { session }), s => body(s).includes("Popular replies"));
      assert.ok(body(snapshot).includes("11-03 17:37")); assert.ok(body(snapshot).includes("Back to list"));
      assert.deepEqual(JSON.parse(snapshot.view.state!), { l: "latest", a: true, t: "1" });
      assert.equal((await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "back") })).result.status, "accepted");
      snapshot = await until(() => h.call("ui.page.snapshot", { session }), s => body(s).includes("Latest · all 5"));
      assert.deepEqual(JSON.parse(snapshot.view.state!), { l: "latest", a: true, t: "0" });
      await h.call("ui.page.dispose", { session });
    }
    assert.equal(h.fixture.hits.get("/latest.rss"), 1); assert.equal(h.fixture.hits.get("/t/topic/1.json"), 1);
  } finally { h.p.close(); }
});

test("two bundled sessions, pure snapshot and both connector tools share one cache/queue/config/status", async () => {
  const h = await setup(); h.fixture.delay = 10;
  try {
    await h.call("ui.page.mount", { session: "first", params: { contribution: "hot", locale: "en", ui_version: 2 } });
    await h.call("ui.page.mount", { session: "second", params: { contribution: "hot", locale: "en", ui_version: 5 } });
    let snapshot = await until(() => h.call("ui.page.snapshot", { session: "first" }), s => body(s).includes("Hot · 1 / 5"));
    await until(() => h.call("ui.page.snapshot", { session: "second" }), s => body(s).includes("Hot · 1 / 5"));
    assert.ok(h.frames.some(frame => frame.event === "ui.page.snapshot"));
    assert.equal((await h.call("connector.status", { contribution: "linuxdo" })).result.state, "ok");
    assert.deepEqual((await h.call("connector.tools.list")).result.tools.map((tool: { name: string }) => tool.name), ["linuxdo_hot", "linuxdo_topic"]);
    const list = (await h.call("connector.tools.call", { name: "linuxdo_hot", args: {} })).result.text;
    assert.ok(list.includes("Synthetic Rust")); assert.ok(!list.includes("Synthetic GPU"));
    const thread = (await h.call("connector.tools.call", { name: "linuxdo_topic", args: { topic: 1, replies: 1 } })).result.text;
    assert.ok(thread.includes("High score")); assert.ok(thread.includes("11-03 18:37"));
    await h.call("ui.page.event", { session: "first", ticket: "host-ticket", interaction: interaction(snapshot, "topic-1") });
    await until(() => h.call("ui.page.snapshot", { session: "first" }), s => body(s).includes("Popular replies"));
    assert.equal(h.fixture.hits.get("/hot.rss"), 1); assert.equal(h.fixture.hits.get("/t/topic/1.json"), 1); assert.equal(h.fixture.peak, 1);
    // Pure snapshot helper is testable, not registered as an extra UI contribution.
    const local = new h.module.LinuxDo(h.fixture.fetch, {}, "UTC", "en", { gap: 0 });
    assert.ok(JSON.stringify(await h.module.snapshotPage(local).render({ locale: "en", state: '{"t":1}', ui_version: 1 })).includes("Back to list"));
  } finally { h.p.close(); }
});

test("bundled Program preserves topic body/stale status for malformed floors across tools and native hot sessions", async () => {
  const h = await setup({});
  try {
    const good = (await h.call("connector.tools.call", { name: "linuxdo_topic", args: { topic: 1 } })).result.text;
    assert.ok(good.includes("Synthetic opening")); h.fixture.time += h.module.limits.topic;
    const malformed: any = structuredClone(h.fixture.bodies.get("/t/topic/1.json"));
    malformed.post_stream.posts[0].post_number = 1.5; malformed.post_stream.posts[0].cooked = "<p>Bad replacement</p>";
    h.fixture.bodies.set("/t/topic/1.json", malformed);
    const reading = (await h.call("connector.tools.call", { name: "linuxdo_topic", args: { topic: 1 } })).result.text;
    assert.ok(reading.includes("Synthetic opening")); assert.ok(!reading.includes("Bad replacement"));
    assert.equal((await h.call("connector.status")).result.state, "error");
    await h.call("ui.page.mount", { session: "stale-body", params: { contribution: "hot", state: '{"t":1}', locale: "en", ui_version: 2 } });
    const snapshot = await until(() => h.call("ui.page.snapshot", { session: "stale-body" }), s => body(s).includes("Synthetic opening"));
    assert.ok(body(snapshot).includes('"stale"')); assert.ok(!body(snapshot).includes("Bad replacement"));
    assert.equal((await h.call("connector.status")).result.state, "error");
  } finally { h.p.close(); }
});

test("bundled Program oversized 429 refresh pauses every contribution and preserves cached listing", async () => {
  const h = await setup({});
  try {
    await h.call("ui.page.mount", { session: "rate", params: { contribution: "hot", locale: "en", ui_version: 2 } });
    const snapshot = await until(() => h.call("ui.page.snapshot", { session: "rate" }), s => body(s).includes("topic-1"));
    h.fixture.bodies.set("/hot.rss", new Response("body", { status: 429, headers: { "Retry-After": "123", "Content-Length": String(h.module.limits.response + 1) } }));
    await h.call("ui.page.event", { session: "rate", ticket: "host-ticket", interaction: interaction(snapshot, "refresh") });
    const stale = await until(() => h.call("ui.page.snapshot", { session: "rate" }), s => body(s).includes('"stale-note"'));
    assert.ok(body(stale).includes("topic-1")); assert.equal((await h.call("connector.status")).result.state, "rate_limited");
    const calls = h.fixture.calls.length;
    assert.ok((await h.call("connector.tools.call", { name: "linuxdo_hot", args: { list: "daily" } })).error);
    assert.equal(h.fixture.calls.length, calls); assert.equal((await h.call("connector.status")).result.state, "rate_limited");
    h.fixture.time += 123_000;
    assert.ok((await h.call("connector.tools.call", { name: "linuxdo_hot", args: { list: "daily" } })).result);
    assert.equal((await h.call("connector.status")).result.state, "ok");
  } finally { h.p.close(); }
});

test("bundled service pending cached read cancellation cannot recover a connector failure", async () => {
  const h = await setup({});
  try {
    const service = new h.module.LinuxDo(h.fixture.fetch, {}, "UTC", "en", { gap: 0, now: () => h.fixture.time });
    await service.listing("hot"); h.fixture.time++;
    h.fixture.bodies.set("/hot.rss", new Response("temporarily broken", { status: 503 }));
    await service.listing("hot", true); const before = service.status(); h.fixture.time++;
    const controller = new AbortController(), pending = service.listing("hot", false, controller.signal); controller.abort();
    await assert.rejects(pending, { name: "AbortError" }); assert.deepEqual(service.status(), before);
  } finally { h.p.close(); }
});

test("host tickets, duplicate replay, stale version/generation and remount isolation", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "owned", params: { contribution: "hot", locale: "en", ui_version: 2 } });
    let snapshot = await until(() => h.call("ui.page.snapshot", { session: "owned" }), s => body(s).includes("Hot · 1 / 5"));
    const event = interaction(snapshot, "all");
    assert.ok((await h.call("ui.page.event", { session: "owned", interaction: event })).error);
    assert.equal((await h.call("ui.page.snapshot", { session: "owned" })).result.version, snapshot.version);
    const accepted = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
    assert.equal(accepted.status, "accepted"); assert.ok(body(accepted.snapshot).includes("all 5"));
    const replay = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
    assert.equal(replay.snapshot.version, accepted.snapshot.version); assert.equal(replay.status, "accepted");
    const stale = { ...event, eventId: crypto.randomUUID() };
    assert.equal((await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: stale })).result.status, "ambiguous");
    await h.call("ui.page.suspend", { session: "owned" }); snapshot = (await h.call("ui.page.resume", { session: "owned" })).result;
    assert.equal(snapshot.generation, accepted.snapshot.generation);
    await h.call("ui.page.dispose", { session: "owned" });
    const remount = (await h.call("ui.page.mount", { session: "owned", params: { contribution: "hot", locale: "en", ui_version: 2 } })).result;
    assert.notEqual(remount.generation, snapshot.generation);
    assert.equal((await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result.status, "ambiguous");
    assert.ok((await h.call("ui.page.mount", { session: "owned", params: { contribution: "hot" } })).error);
    assert.ok((await h.call("ui.page.mount", { session: "unknown", params: { contribution: "other" } })).error);
  } finally { h.p.close(); }
});

test("bundled cached resume and refresh retain stable effects/callbacks and localized refresh toast", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "cached", params: { contribution: "hot", locale: "ja", ui_version: 2 } });
    let snapshot = await until(() => h.call("ui.page.snapshot", { session: "cached" }), s => body(s).includes("topic-1"));
    const result = (await h.call("ui.page.event", { session: "cached", ticket: "host-ticket", interaction: interaction(snapshot, "refresh") })).result;
    assert.equal(result.result.toast, "更新しました");
    await until(() => h.call("ui.page.snapshot", { session: "cached" }), s => body(s).includes("topic-1") && h.fixture.hits.get("/hot.rss") === 2);
    await h.call("ui.page.suspend", { session: "cached" }); snapshot = (await h.call("ui.page.resume", { session: "cached" })).result;
    await pause(20);
    assert.equal((await h.call("ui.page.snapshot", { session: "cached" })).result.version, snapshot.version);
    assert.equal(h.fixture.hits.get("/hot.rss"), 2);
    assert.equal((await h.call("ui.page.event", { session: "cached", ticket: "host-ticket", interaction: interaction(snapshot, "all") })).result.status, "accepted");
  } finally { h.p.close(); }
});

test("bundled old-list cancellation, fast navigation, suspend/resume/dispose and connector state", async () => {
  const h = await setup(); h.fixture.delay = 50;
  try {
    await h.call("ui.page.mount", { session: "moving", params: { contribution: "hot", locale: "en", ui_version: 2 } });
    let snapshot: Snapshot = (await h.call("ui.page.snapshot", { session: "moving" })).result;
    await pause(5);
    await h.call("ui.page.event", { session: "moving", ticket: "host-ticket", interaction: interaction(snapshot, "list", { list: "daily" }) });
    snapshot = await until(() => h.call("ui.page.snapshot", { session: "moving" }), s => body(s).includes("No topics match"));
    assert.ok(body(snapshot).includes("Daily top")); assert.ok(!body(snapshot).includes("Hot ·")); assert.ok(h.fixture.cancelled > 0);
    await h.call("ui.page.event", { session: "moving", ticket: "host-ticket", interaction: interaction(snapshot, "list", { list: "latest" }) });
    await pause(5); await h.call("ui.page.suspend", { session: "moving" }); await pause(5); assert.equal(h.fixture.active, 0);
    h.fixture.delay = 0; await h.call("ui.page.resume", { session: "moving" });
    snapshot = await until(() => h.call("ui.page.snapshot", { session: "moving" }), s => body(s).includes("Latest · 1 / 5"));
    const keys = snapshot.view.sections.flatMap(section => section.items).map(item => item.key); assert.equal(new Set(keys).size, keys.length);
    h.fixture.delay = 50; await h.call("ui.page.event", { session: "moving", ticket: "host-ticket", interaction: interaction(snapshot, "list", { list: "weekly" }) });
    await pause(5); await h.call("ui.page.dispose", { session: "moving" }); await pause(5); assert.equal(h.fixture.active, 0);
    assert.equal((await h.call("connector.status")).result.state, "ok");
  } finally { h.p.close(); }
});

test("public lock provenance rejects private sources, credentials and unapproved links independently of terms", async () => {
  const { verifyPublicLock } = await import(pathToFileURL(resolve("scripts/public-lock.mjs")).href);
  const original = JSON.parse(await readFile("package-lock.json", "utf8"));
  const sdkPath = original.packages["node_modules/@persona/extension"].resolved;
  assert.deepEqual(await verifyPublicLock("package-lock.json", sdkPath), { packages: 41, publicResolved: 38, localSDKLinks: 1 });
  assert.deepEqual(await verifyPublicLock("build/package/extensions/linuxdo/package-lock.json", sdkPath), await verifyPublicLock("package-lock.json", sdkPath));
  assert.deepEqual(await verifyPublicLock("build/package/sdk/ts/package-lock.json", null), { packages: 8, publicResolved: 7, localSDKLinks: 0 });
  const dir = await mkdtemp(resolve("build/tests/lock-")), path = resolve(dir, "package-lock.json");
  try {
    const credentialSource = new URL("https://registry.npmjs.org/entities/-/entities-6.0.1.tgz");
    credentialSource.username = "fixture";
    credentialSource.password = "fixture";
    assert.equal(credentialSource.origin, "https://registry.npmjs.org");
    assert.equal(credentialSource.username, "fixture");
    assert.equal(credentialSource.password, "fixture");
    for (const resolved of ["https://registry.example.invalid/entities/-/entities-6.0.1.tgz", credentialSource.href, "https://registry.npmjs.org/entities/-/entities-6.0.1.tgz?token=fixture", "http://registry.npmjs.org/entities/-/entities-6.0.1.tgz", "../unapproved-sdk"]) {
      const lock = structuredClone(original); lock.packages["node_modules/entities"].resolved = resolved;
      await writeFile(path, JSON.stringify(lock)); await assert.rejects(verifyPublicLock(path));
    }
    const lock = structuredClone(original); lock.packages["node_modules/@persona/extension"].resolved = "../unapproved-sdk";
    await writeFile(path, JSON.stringify(lock)); await assert.rejects(verifyPublicLock(path));
    const credentials = structuredClone(original); credentials.packages[""]._authToken = "synthetic-placeholder";
    await writeFile(path, JSON.stringify(credentials)); await assert.rejects(verifyPublicLock(path));
  } finally { await rm(dir, { recursive: true, force: true }); }
});

test("bundled native pagination reaches the 271st topic and preserves page after back/resume", async () => {
  const h = await setup({});
  h.fixture.bodies.set("/hot.rss", rss(Array.from({ length: 271 }, (_, i) => ({ id: i + 1 }))));
  h.fixture.bodies.set("/t/topic/271.json", { id: 271, title: "Synthetic tail topic", post_stream: { posts: [{ id: 27100, post_number: 1, cooked: "Tail opening" }] } });
  try {
    for (const version of [1, 2, 3, 5]) {
      const session = `pagination-${version}`;
      await h.call("ui.page.mount", { session, params: { contribution: "hot", locale: "en", ui_version: version } });
      let snapshot = await until(() => h.call("ui.page.snapshot", { session }), s => body(s).includes("next-page"));
      assert.ok(!body(snapshot).includes('"topic-271"'));
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "next-page") });
      snapshot = await until(() => h.call("ui.page.snapshot", { session }), s => body(s).includes('"topic-271"'));
      assert.equal(JSON.parse(snapshot.view.state!).p, 1);
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "topic-271") });
      snapshot = await until(() => h.call("ui.page.snapshot", { session }), s => body(s).includes("Tail opening"));
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "back") });
      snapshot = await until(() => h.call("ui.page.snapshot", { session }), s => body(s).includes('"topic-271"'));
      await h.call("ui.page.suspend", { session });
      await h.call("ui.page.resume", { session });
      snapshot = await until(() => h.call("ui.page.snapshot", { session }), s => body(s).includes('"topic-271"'));
      assert.equal(JSON.parse(snapshot.view.state!).p, 1);
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "previous-page") });
      snapshot = await until(() => h.call("ui.page.snapshot", { session }), s => body(s).includes("next-page"));
      assert.equal(JSON.parse(snapshot.view.state!).p, undefined);
      await h.call("ui.page.dispose", { session });
    }
    assert.equal(h.fixture.hits.get("/hot.rss"), 1);
    assert.equal(h.fixture.hits.get("/t/topic/271.json"), 1);
  } finally { h.p.close(); }
});

test("source profile rejects missing bundle, tampering, symlinks, runtime imports and missing default", async () => {
  const { verifySourceProfile } = await import(pathToFileURL(resolve("scripts/source-profile.mjs")).href);
  await mkdir("build/tests", { recursive: true });
  const dir = await mkdtemp(resolve("build/tests/profile-"));
  try {
    await cp("build/package", dir, { recursive: true });
    const manifest = JSON.parse(await readFile(resolve(dir, "persona-extension.json"), "utf8"));
    const path = resolve(dir, manifest.runtime.source.bundle), original = await readFile(path);
    await verifySourceProfile(dir);
    await rm(path); await assert.rejects(verifySourceProfile(dir), { code: "ENOENT" });
    await writeFile(path, "export default {};"); await assert.rejects(verifySourceProfile(dir), /hash mismatch/u);
    await rm(path); await writeFile(resolve(dir, "actual.mjs"), original); await symlink("../actual.mjs", path);
    await assert.rejects(verifySourceProfile(dir), /symlinks/u); await rm(path);
    for (const code of ['import "fixture-package"; export default {};', 'export { default } from "fixture-package";', 'const target = "fixture-package"; import(target); export default {};', 'export const program = {};']) {
      const bytes = Buffer.from(code); manifest.runtime.source.sha256 = createHash("sha256").update(bytes).digest("hex");
      await writeFile(path, bytes); await writeFile(resolve(dir, "persona-extension.json"), JSON.stringify(manifest));
      await assert.rejects(verifySourceProfile(dir), code.includes("import") || code.includes("from") ? /runtime imports/u : /default Program/u);
    }
    manifest.runtime.source.entry = "../../outside.ts"; await writeFile(resolve(dir, "persona-extension.json"), JSON.stringify(manifest));
    await assert.rejects(verifySourceProfile(dir), /escapes package/u);
  } finally { await rm(dir, { recursive: true, force: true }); }
});
