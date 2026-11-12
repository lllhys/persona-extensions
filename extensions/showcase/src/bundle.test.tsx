import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync, readdirSync } from "node:fs";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import { connect } from "@persona/extension";

const root = fileURLToPath(new URL("../", import.meta.url));
const artifact = fileURLToPath(new URL("../.source-dist/showcase/bundle/main.mjs", import.meta.url));
function build() {
  const result = spawnSync(process.execPath, ["scripts/build-source.mjs"], { cwd: root, encoding: "utf8", timeout: 30000 });
  assert.equal(result.status, 0, result.error?.message || result.stderr);
}
test("locked source bundle is reproducible, offline and portable without package resolution", async () => {
  build();
  const first = readFileSync(artifact), archive = readFileSync(root + ".source-dist/showcase-source.tar.gz");
  build();
  assert.deepEqual(readFileSync(artifact), first);
  assert.deepEqual(readFileSync(root + ".source-dist/showcase-source.tar.gz"), archive);
  const manifest = JSON.parse(readFileSync(root + ".source-dist/showcase/persona-extension.json", "utf8"));
  assert.deepEqual(manifest.runtime, { mode: "deno", source: { entry: "src/main.tsx", bundle: "bundle/main.mjs", sha256: createHash("sha256").update(first).digest("hex") } });
  assert.equal(manifest.version, JSON.parse(readFileSync(root + "persona-extension.json", "utf8")).version); assert.equal(manifest.permissions, undefined); assert.equal(manifest.tags, undefined);
  assert.equal(manifest.contributes.ui.length, 3);
  assert.equal(manifest.contributes.ui[0].placement, "nav_page");
  assert.ok(manifest.contributes.ui.slice(1).every((ui: any) => ui.opens === "showcase"));
  assert.equal(manifest.contributes.shortcuts[0].ui, "showcase");
  assert.doesNotMatch(first.toString(), /\b(?:require|import)\s*\(/);
  const compiled = await import(artifact);
  assert.equal(compiled.default.id, "showcase");
  // The bundled SDK owns its element symbols; use its own system-snapshot renderer.
  const view = await compiled.default.ui["showcase-widget"].render({ size: "large", locale: "ja" });
  assert.equal(view.sections[0].items.length, 4);
  const replies: any[] = [], protocol = connect(compiled.default, line => replies.push(JSON.parse(line)));
  try {
    protocol.line(JSON.stringify({ id: "1", method: "initialize", params: { protocol: 1, hub: { build: 1, extension_points: {}, locale: "en" }, granted: {} } }));
    await protocol.drain(); assert.equal(replies[0].result.id, "showcase");
    protocol.line(JSON.stringify({ id: "2", method: "shortcut.run", params: { contribution: "showcase-count", values: { n: "3", unit: "star" }, locale: "en" } }));
    await protocol.drain(); assert.equal(replies[1].result.text, "Counted 3 stars");
  } finally { protocol.close(); }
});

test("APP fixtures are retained unchanged in the source package", () => {
  build();
  const source = root + "src/legacy-snapshots/", packaged = root + ".source-dist/showcase/src/legacy-snapshots/";
  assert.deepEqual(readdirSync(packaged).sort(), readdirSync(source).sort());
  for (const name of readdirSync(source)) assert.deepEqual(readFileSync(packaged + name), readFileSync(source + name));
});

test("actual bundled default Program mounts its own renderer and retains independent hooks through bridge lifecycle", async () => {
  build();
  const compiled = await import(artifact + "?lifecycle"), messages: any[] = [];
  const protocol = connect(compiled.default, line => messages.push(JSON.parse(line)));
  let id = 0;
  async function request(method: string, params: unknown = {}) {
    const call = String(++id);
    protocol.line(JSON.stringify({ id: call, method, params }));
    await protocol.drain();
    const reply = messages.find(message => message.id === call);
    assert.ok(reply && !reply.error, JSON.stringify(reply));
    return reply.result;
  }
  const mount = (session: string, state = {}, ui_version = 3) => request("ui.page.mount", {
    session, params: { contribution: "showcase", state: JSON.stringify(state), locale: "en", ui_version },
  });
  const node = (frame: any, key: string) => key === "showcase-view" ? frame.view : frame.view.sections.flatMap((s: any) => [s, ...s.items]).find((n: any) => n.key === key);
  async function event(session: string, key: string, values = {}, slot = "action") {
    const frame = await request("ui.page.snapshot", { session });
    const action = slot.split(".").reduce((value: any, name) => value?.[name], node(frame, key));
    assert.ok(action?.id, key);
    // Bridge-only host ticket fixture: real manager presentation gates are tested separately.
    return request("ui.page.event", { session, ticket: "synthetic-host-ticket", interaction: {
      generation: frame.generation, version: frame.version, eventId: `bundle-${id}`, key, actionId: action.id, values,
    } });
  }
  try {
    await request("initialize", { protocol: 1, hub: { locale: "en", timezone: "UTC", build: 1, extension_points: {} }, granted: {} });
    assert.deepEqual(await request("ui.page.supported"), { contributions: ["showcase"] });
    const catalog = await mount("catalog");
    assert.equal(catalog.view.sections[1].items.length, 17);
    const listPush = (await event("catalog", "catalog-list")).result.push;
    assert.deepEqual(JSON.parse(listPush.state), { p: "list" });
    await mount("list", JSON.parse(listPush.state));
    assert.equal((await event("list", "list-items", {}, "more.action")).snapshot.view.sections[0].items.length, 40);
    assert.equal((await event("list", "showcase-view", { q: "1" }, "search.action")).snapshot.view.sections[0].items.length, 15);
    await event("list", "showcase-view", { q: "", kind: "chat" }, "filter.action");
    assert.equal((await request("ui.page.snapshot", { session: "list" })).view.sections[0].items.length, 20);
    const detailPush = (await event("list", "item-2")).result.push;
    await mount("detail", JSON.parse(detailPush.state));
    const editPush = (await event("detail", "detail-edit")).result.push;
    await mount("edit", JSON.parse(editPush.state));
    assert.equal((await event("edit", "edit-save", { title: " " })).result.toast, "Title cannot be empty");
    const saved = await event("edit", "edit-save", { title: "Updated synthetic item", note: "Demo", kind: "mail", done: "true" });
    assert.equal(saved.result.pop, true);
    await new Promise(resolve => setTimeout(resolve, 10));
    assert.equal((await request("ui.page.snapshot", { session: "detail" })).view.title, "Updated synthetic item");
    await mount("segments", { p: "segmented" }); await mount("other", { p: "segmented" });
    await event("segments", "range", { range: "1d" });
    assert.equal(node(await request("ui.page.snapshot", { session: "other" }), "range").value, "7d");
    await request("ui.page.suspend", { session: "segments" });
    assert.equal(node(await request("ui.page.resume", { session: "segments" }), "range").value, "1d");
    for (const ui_version of [1, 2]) {
      const frame = await mount(`old-${ui_version}`, { p: "list", q: "Sample", shown: 40 }, ui_version);
      assert.equal(frame.view.search, undefined); assert.equal(frame.view.filter, undefined);
      assert.ok(node(frame, "list-more"));
    }
    const malformed = await mount("malformed", { p: "list", q: 10, shown: "bad" });
    assert.equal(malformed.view.sections[0].items.length, 20);
    for (const session of ["catalog", "list", "detail", "edit", "segments", "other", "old-1", "old-2", "malformed"]) await request("ui.page.dispose", { session });
  } finally { await protocol.shutdown(); }
});
