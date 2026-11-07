import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { connect, type Program } from "@persona/extension";
import type { Snapshot, UIEvent } from "@persona/extension/ui";
import { Fixture } from "./fixtures.ts";

async function setup() {
  const bundle = await import(pathToFileURL(resolve("build/hackernews.mjs")).href);
  assert.equal(bundle.default.id, "hackernews");
  assert.equal(bundle.default.version, "0.1.0");
  assert.equal(bundle.default.fetch, undefined);
  const fixture = new Fixture();
  const frames: any[] = [];
  const p = connect(bundle.createProgram(fixture.fetch) as Program, line => frames.push(JSON.parse(line)));
  let id = 0;
  const call = async (method: string, params: unknown = {}) => {
    const request = String(++id);
    p.line(JSON.stringify({ id: request, method, params })); await p.drain();
    return frames.find(frame => frame.id === request);
  };
  await call("initialize", { protocol: 1, config: { keywords: ["Rust"] } });
  return { fixture, frames, p, call };
}
async function until(read: () => Promise<any>, predicate: (value: any) => boolean) {
  const deadline = Date.now() + 3000;
  while (Date.now() < deadline) { const value = await read(); if (predicate(value)) return value; await new Promise(resolve => setTimeout(resolve, 2)); }
  throw new Error("Program session did not settle");
}
const body = (snapshot: Snapshot) => JSON.stringify(snapshot.view);
function interaction(snapshot: Snapshot, key: string, values: Record<string, string> = {}): UIEvent {
  const item = snapshot.view.sections.flatMap(section => section.items).find(item => item.key === key);
  assert.ok(item && "action" in item && item.action);
  return { generation: snapshot.generation, version: snapshot.version, key, actionId: item.action.id, eventId: crypto.randomUUID(), values };
}
test("offline profile preserves released Go identity, read-only grants and import-free default Program", async () => {
  const manifest = JSON.parse(await readFile("build/package/persona-extension.json", "utf8"));
  const released = JSON.parse(await readFile("persona-extension.json", "utf8"));
  const bytes = await readFile("build/package/" + manifest.runtime.source.bundle);
  const { createHash } = await import("node:crypto");
  assert.equal(manifest.runtime.source.sha256, createHash("sha256").update(bytes).digest("hex"));
  assert.equal(manifest.version, released.version); assert.deepEqual(manifest.permissions, released.permissions);
  assert.ok(released.runtime.command); assert.equal(released.runtime.mode, undefined);
  assert.equal(manifest.runtime.mode, "deno"); assert.equal(manifest.runtime.command, undefined);
  assert.equal(manifest.contributes.ui[0].placement, "nav_page");
  assert.equal(manifest.contributes.ui[1].placement, "widget");
  assert.ok(bytes.length < 8 * 1024 * 1024);
  for (const path of ["src/index.ts", "package.json", "package-lock.json"]) await readFile("build/package/" + path);
});
test("bundled component page preserves requested UI version and server-owned story/back navigation", async () => {
  const h = await setup();
  try {
    for (const version of [1, 3, 5]) {
      const session = `version-${version}`;
      await h.call("ui.page.mount", { session, params: { contribution: "stories", locale: "en", ui_version: version } });
      let snapshot: Snapshot = (await until(() => h.call("ui.page.snapshot", { session }), frame => JSON.stringify(frame.result).includes("Synthetic Rust article"))).result;
      const picker = snapshot.view.sections[0].items[0];
      assert.equal(picker.type, version < 2 ? "row" : "segmented");
      const forward = (await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "story-1") })).result;
      assert.equal(forward.status, "accepted");
      snapshot = (await until(() => h.call("ui.page.snapshot", { session }), frame => JSON.stringify(frame.result).includes("comment-meta-11"))).result;
      const back = (await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "back") })).result;
      assert.equal(back.status, "accepted");
      snapshot = (await until(() => h.call("ui.page.snapshot", { session }), frame => JSON.stringify(frame.result).includes("story-1"))).result;
      assert.ok(body(snapshot).includes("Synthetic Rust article"));
      await h.call("ui.page.dispose", { session });
    }
  } finally { h.p.close(); }
});

test("resuming a cached page does not publish an unchanged effect result over a saved callback", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "cached", params: { contribution: "stories", locale: "en", ui_version: 1 } });
    await until(() => h.call("ui.page.snapshot", { session: "cached" }), frame => JSON.stringify(frame.result).includes("Synthetic Rust article"));
    await h.call("ui.page.suspend", { session: "cached" });
    const resumed: Snapshot = (await h.call("ui.page.resume", { session: "cached" })).result;
    await new Promise(resolve => setTimeout(resolve, 10));
    const settled: Snapshot = (await h.call("ui.page.snapshot", { session: "cached" })).result;
    assert.equal(settled.version, resumed.version, "an unchanged cached read must not invalidate the resumed callback table");
    assert.equal(h.fixture.hits.get("/topstories.json"), 1);
    assert.equal((await h.call("ui.page.event", { session: "cached", ticket: "host-ticket", interaction: interaction(resumed, "all") })).result.status, "accepted");
  } finally { h.p.close(); }
});

test("actual separately bundled Program shares service/cache across native sessions, connector, tools and snapshots", async () => {
  const h = await setup();
  try {
    assert.deepEqual((await h.call("ui.page.supported")).result, { contributions: ["stories"] });
    assert.equal((await h.call("ui.render", { contribution: "stories", ui_version: 5 })).error.code, "unsupported");
    await h.call("ui.page.mount", { session: "owned", params: { contribution: "stories", locale: "en", ui_version: 6 } });
    let snapshot: Snapshot = (await until(() => h.call("ui.page.snapshot", { session: "owned" }), frame => JSON.stringify(frame.result).includes("Synthetic Rust article"))).result;
    assert.ok(h.frames.some(frame => frame.event === "ui.page.snapshot"));
    const tool = await h.call("connector.tools.call", { contribution: "hackernews", name: "hn_stories", args: {} });
    assert.ok(tool.result.text.includes("Synthetic Rust article"));
    assert.equal((await h.call("connector.status", { contribution: "hackernews" })).result.state, "ok");
    for (const version of [1, 3, 5]) {
      const view = (await h.call("ui.render", { contribution: "stories-snapshot", locale: "en", ui_version: version, placement: "widget", size: "large" })).result;
      assert.ok(JSON.stringify(view).includes("Synthetic Rust article"));
    }
    assert.equal(h.fixture.hits.get("/topstories.json"), 1);
    assert.equal(h.fixture.hits.get("/item/1.json"), 1);
    const event = interaction(snapshot, "all");
    assert.ok((await h.call("ui.page.event", { session: "owned", interaction: event })).error);
    const accepted = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
    assert.equal(accepted.status, "accepted"); assert.ok(body(accepted.snapshot).includes("all 3"));
    const replay = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
    assert.equal(replay.snapshot.version, accepted.snapshot.version);
    const stale = { ...event, eventId: crypto.randomUUID() };
    assert.equal((await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: stale })).result.status, "ambiguous");
    await h.call("ui.page.suspend", { session: "owned" });
    snapshot = (await h.call("ui.page.resume", { session: "owned" })).result;
    assert.equal(snapshot.generation, accepted.snapshot.generation);
    await h.call("ui.page.dispose", { session: "owned" });
    const remount = (await h.call("ui.page.mount", { session: "new-owned", params: { contribution: "stories", locale: "en", ui_version: 6 } })).result;
    assert.notEqual(remount.generation, snapshot.generation);
    assert.equal((await h.call("ui.page.event", { session: "new-owned", ticket: "host-ticket", interaction: event })).result.status, "ambiguous");
  } finally { h.p.close(); }
});
