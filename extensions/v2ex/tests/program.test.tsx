import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { createHash } from "node:crypto";
import { connect, type Program } from "@persona/extension";
import type { Snapshot } from "@persona/extension/ui";
import { Fixture, interaction, tick, until } from "./fixtures.ts";
async function setup(initialize = true) {
  // Exercise the actual import-free artifact, not a tree-shaken source-only facsimile.
  const bundle = await import(pathToFileURL(resolve("build/v2ex.mjs")).href);
  const fixture = new Fixture(), frames: any[] = [];
  const p = connect(bundle.createProgram(fixture.fetch, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 } }) as Program, line => frames.push(JSON.parse(line)));
  let id = 0;
  const call = async (method: string, params: unknown = {}) => {
    const request = String(++id); p.line(JSON.stringify({ id: request, method, params })); await p.drain();
    const frame = frames.find(frame => frame.id === request); assert.ok(frame); return frame;
  };
  if (initialize) await call("initialize", { protocol: 1, config: { keywords: ["no-match"], list: "latest" }, hub: { locale: "en", timezone: "UTC" } });
  return { bundle, fixture, frames, p, call };
}
const body = (snapshot: Snapshot) => JSON.stringify(snapshot.view);
const settled = async (h: Awaited<ReturnType<typeof setup>>, session: string, token: string): Promise<Snapshot> =>
  (await until(() => h.call("ui.page.snapshot", { session }), frame => JSON.stringify(frame.result).includes(token))).result;
test("source artifact has a default Program, verified import-free hash and unchanged released Go identity/grants", async () => {
  const released = JSON.parse(await readFile("persona-extension.json", "utf8"));
  const manifest = JSON.parse(await readFile("build/package/persona-extension.json", "utf8"));
  const bytes = await readFile("build/package/" + manifest.runtime.source.bundle);
  assert.equal(manifest.runtime.mode, "deno"); assert.equal(manifest.runtime.command, undefined); assert.ok(released.runtime.command); assert.equal(released.runtime.mode, undefined);
  assert.equal(manifest.runtime.source.sha256, createHash("sha256").update(bytes).digest("hex"));
  assert.equal(manifest.version, released.version); assert.deepEqual(manifest.permissions, released.permissions); assert.deepEqual(manifest.configuration, released.configuration);
  assert.deepEqual(manifest.contributes, released.contributes); assert.deepEqual(manifest.contributes.ui.map((ui: any) => ui.id), ["topics"]);
  assert.equal(manifest.contributes.ui[0].placement, "plugin_page"); assert.ok(bytes.length > 0 && bytes.length < (8 << 20));
  for (const path of ["src/index.ts", "package.json", "package-lock.json", "tsconfig.json", "SOURCE-PROFILE.txt"]) await readFile("build/package/" + path);
  const bundle = await import(pathToFileURL(resolve("build/v2ex.mjs")).href); assert.equal(bundle.default.id, "v2ex"); assert.equal(bundle.default.version, released.version); assert.equal(bundle.default.fetch, undefined);
  assert.deepEqual(Object.keys(bundle.default.ui), ["topics"]);
});
test("protocol gating, initialization failures, manifest and contribution validation remain SDK-owned", async () => {
  const h = await setup(false);
  try {
    assert.equal((await h.call("connector.status")).error.code, "not_initialized");
    assert.equal((await h.call("initialize", { protocol: 2 })).error.code, "wrong_protocol");
    assert.ok((await h.call("initialize", { protocol: 1, config: { tz: "Mars/Olympus" } })).error);
    assert.equal((await h.call("initialize", { protocol: 1, config: {}, hub: { locale: "ja" } })).result.id, "v2ex");
    assert.deepEqual((await h.call("ping")).result, {});
    assert.equal((await h.call("manifest")).result.tags, undefined);
    assert.equal((await h.call("connector.status", { contribution: "missing" })).error.code, "no_contribution");
    assert.equal((await h.call("connector.unsupported", {})).error.code, "unknown_method");
    assert.ok((await h.call("connector.tools.list", {})).result.tools[0].description.includes("第三者"));
    assert.ok((await h.call("ui.page.mount", { session: "bad", params: { contribution: "missing" } })).error);
  } finally { h.p.close(); }
});
test("UI 0/1/2 stays inline with back; UI 3/5 pushes to the same contribution without changing parent list/filter", async () => {
  const h = await setup();
  try {
    for (const version of [0, 1, 2, 3, 5]) {
      const session = `parent-${version}`;
      await h.call("ui.page.mount", { session, params: { contribution: "topics", locale: "en", ui_version: version } });
      let snapshot = await settled(h, session, '"all"');
      assert.equal(snapshot.view.sections[0].items[0].type, version < 2 ? "row" : "segmented");
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "all") });
      snapshot = await settled(h, session, "topic-1");
      const topicRow: any = snapshot.view.sections.flatMap(s => s.items).find(i => i.key === "topic-1"); assert.equal(!!topicRow.action.push, version >= 3);
      const forward = (await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "topic-1") })).result;
      assert.equal(forward.status, "accepted");
      if (version >= 3) {
        assert.equal(forward.result.push.ui, "topics");
        const parent: Snapshot = (await h.call("ui.page.snapshot", { session })).result;
        assert.deepEqual(JSON.parse(parent.view.state!), { l: "latest", t: 0, a: true });
        const childSession = `child-${version}`;
        await h.call("ui.page.mount", { session: childSession, params: { contribution: "topics", locale: "en", ui_version: version, state: forward.result.push.state } });
        const child = await settled(h, childSession, "reply-meta-124-25");
        assert.ok(!body(child).includes('"back"')); assert.equal(JSON.parse(child.view.state!).t, 1);
        await h.call("ui.page.dispose", { session: childSession });
        const restored: Snapshot = (await h.call("ui.page.snapshot", { session })).result;
        assert.deepEqual(JSON.parse(restored.view.state!), { l: "latest", t: 0, a: true });
      } else {
        const opened = await settled(h, session, "reply-meta-124-25");
        assert.ok(body(opened).includes('"back"'));
        assert.equal((await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(opened, "back") })).result.status, "accepted");
        snapshot = await settled(h, session, "topic-1"); assert.deepEqual(JSON.parse(snapshot.view.state!), { l: "latest", t: 0, a: true });
      }
      await h.call("ui.page.dispose", { session });
    }
  } finally { h.p.close(); }
});
test("connector, native sessions, tools and pure snapshots share a single service/cache", async () => {
  const h = await setup();
  try {
    assert.deepEqual((await h.call("ui.page.supported")).result, { contributions: ["topics"] });
    // Normal SDK behavior. The real manager's HTTP bridge mounts this same contribution.
    assert.equal((await h.call("ui.render", { contribution: "topics", ui_version: 5 })).error.code, "unsupported");
    assert.equal((await h.call("ui.action", { contribution: "topics", ui_version: 1, action: "all" })).error.code, "unsupported");
    for (const session of ["one", "two"]) await h.call("ui.page.mount", { session, params: { contribution: "topics", locale: "en", ui_version: 5 } });
    await settled(h, "one", '"all"'); await settled(h, "two", '"all"');
    const tool = await h.call("connector.tools.call", { contribution: "v2ex", name: "v2ex_hot", args: { source: "latest", filtered: false } });
    assert.ok(tool.result.text.includes("Synthetic topic 1")); assert.equal(h.fixture.hits.get("/topics/latest.json"), 1);
    const status = await h.call("connector.status", {}); assert.equal(status.result.state, "ok"); assert.ok(status.result.last_sync);
    assert.ok(h.frames.some(f => f.event === "ui.page.snapshot"));
    const local = new h.bundle.V2EX(h.fixture.fetch, {}, { now: () => h.fixture.clock, monotonic: () => h.fixture.clock, limits: { gap: 0 } });
    const pure = h.bundle.snapshotPage(local);
    await pure.render({ contribution: "topics", locale: "en", ui_version: 1 });
    await h.bundle.callTool(local, "v2ex_hot", {});
    assert.equal(h.fixture.hits.get("/topics/hot.json"), 1);
  } finally { h.p.close(); }
});
test("trusted ticket gating, replay receipts and stale/cross-generation events fail closed", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "owned", params: { contribution: "topics", locale: "en", ui_version: 5 } });
    const snapshot = await settled(h, "owned", '"all"'), event = interaction(snapshot, "all");
    assert.ok((await h.call("ui.page.event", { session: "owned", interaction: event })).error);
    const accepted = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result; assert.equal(accepted.status, "accepted");
    const replay = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
    assert.equal(replay.snapshot.version, accepted.snapshot.version);
    assert.equal((await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: { ...event, eventId: crypto.randomUUID() } })).result.status, "ambiguous");
    await h.call("ui.page.dispose", { session: "owned" });
    const remount: Snapshot = (await h.call("ui.page.mount", { session: "new-owned", params: { contribution: "topics", ui_version: 5 } })).result;
    assert.notEqual(remount.generation, snapshot.generation);
    assert.equal((await h.call("ui.page.event", { session: "new-owned", ticket: "host-ticket", interaction: event })).result.status, "ambiguous");
  } finally { h.p.close(); }
});
test("Program cached resume does not republish unchanged effects or invalidate saved callbacks", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "cached", params: { contribution: "topics", ui_version: 1, locale: "en" } }); await settled(h, "cached", '"all"');
    await h.call("ui.page.suspend", { session: "cached" });
    const resumed: Snapshot = (await h.call("ui.page.resume", { session: "cached" })).result; await tick(10);
    const current: Snapshot = (await h.call("ui.page.snapshot", { session: "cached" })).result;
    assert.equal(current.version, resumed.version); assert.equal(h.fixture.hits.get("/topics/latest.json"), 1);
    assert.equal((await h.call("ui.page.event", { session: "cached", ticket: "host-ticket", interaction: interaction(resumed, "all") })).result.status, "accepted");
  } finally { h.p.close(); }
});
