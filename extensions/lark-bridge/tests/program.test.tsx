import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { createHash } from "node:crypto";
import { connect, type Program } from "@persona/extension";
import type { Snapshot } from "@persona/extension/ui";
import { Fixture, interaction, until } from "./fixtures.ts";
async function setup(initialize = true) {
  const bundle = await import(pathToFileURL(resolve("build/lark-bridge.mjs")).href);
  const fixture = new Fixture(), frames: any[] = [];
  const p = connect(bundle.createProgram() as Program, line => {
    const frame = JSON.parse(line);
    if (typeof frame.id === "string" && frame.id.startsWith("p") && frame.method === "cli.call") {
      void fixture.call(frame.method, frame.params).then(result => p.line(JSON.stringify({ id: frame.id, result })), error => p.line(JSON.stringify({ id: frame.id, error: { code: error.code, message: error.message } })));
      return;
    }
    frames.push(frame);
  });
  let id = 0;
  const call = async (method: string, params: unknown = {}) => {
    const request = String(++id); p.line(JSON.stringify({ id: request, method, params })); await p.drain();
    const frame = frames.find(frame => frame.id === request); assert.ok(frame); return frame;
  };
  if (initialize) await call("initialize", { protocol: 1, config: {}, hub: { locale: "en", timezone: "UTC" } });
  return { bundle, fixture, frames, p, call };
}
const body = (snapshot: Snapshot) => JSON.stringify(snapshot.view);
const settled = async (h: Awaited<ReturnType<typeof setup>>, session: string, token: string): Promise<Snapshot> => {
  const frame = await until(() => h.call("ui.page.snapshot", { session }), frame => !!frame.error || JSON.stringify(frame.result).includes(token));
  if (frame.error) console.error("page did not settle", { session, token, frame, requests: h.fixture.requests });
  assert.equal(frame.error, undefined, JSON.stringify(frame.error)); return frame.result;
};
test("source artifact declares bounded CLI access and no write capability", async () => {
  const manifest = JSON.parse(await readFile("build/package/persona-extension.json", "utf8"));
  const bytes = await readFile("build/package/" + manifest.runtime.source.bundle);
  assert.equal(manifest.id, "lark-bridge"); assert.equal(manifest.runtime.mode, "deno"); assert.equal(manifest.runtime.command, undefined);
  assert.equal(manifest.runtime.source.sha256, createHash("sha256").update(bytes).digest("hex"));
  assert.deepEqual(manifest.host_integrations, [{ id: "lark-cli", version: ">=1.0.72 <2.0.0" }]);
  assert.equal(manifest.permissions, undefined); assert.equal(manifest.secrets, undefined);
  assert.deepEqual(manifest.contributes.connectors[0].capabilities, ["status", "tools"]);
  assert.ok(manifest.contributes.connectors[0].agent_tools.every((name: string) => !/send|reply|agent|write|read_receipt/.test(name)));
  assert.ok(bytes.length > 0 && bytes.length < (8 << 20));
  const bundle = await import(pathToFileURL(resolve("build/lark-bridge.mjs")).href);
  assert.equal(bundle.default.fetch, undefined); assert.equal(bundle.default.id, "lark-bridge");
  assert.ok(bundle.default.init); assert.ok(bundle.default.namespaces.connector); assert.ok(bundle.default.ui.larkbridge);
});
test("protocol initialization, localized tools and unknown contributions", async () => {
  const h = await setup(false);
  try {
    assert.equal((await h.call("connector.status")).error.code, "not_initialized");
    assert.equal((await h.call("initialize", { protocol: 2 })).error.code, "wrong_protocol");
    assert.equal((await h.call("initialize", { protocol: 1, config: {}, hub: { locale: "ja" } })).result.id, "lark-bridge");
    assert.deepEqual((await h.call("ping")).result, {});
    assert.equal((await h.call("connector.status", { contribution: "missing" })).error.code, "no_contribution");
    assert.equal((await h.call("connector.unsupported", {})).error.code, "unknown_method");
    assert.ok((await h.call("connector.tools.list", {})).result.tools[0].description.includes("読み取り"));
    assert.equal((await h.call("connector.tools.call", { name: "larkbridge_messages", args: { chat: "../other" } })).error.code, "invalid");
  } finally { h.p.close(); }
});
test("home, native search, message details and topic navigation use only fixture reads", async () => {
  const h = await setup();
  try {
    for (const version of [1, 3, 5]) {
      const session = `larkbridge-${version}`;
      await h.call("ui.page.mount", { session, params: { contribution: "larkbridge", locale: "en", ui_version: version } });
      let snapshot = await settled(h, session, "oc_example_group");
      assert.ok(body(snapshot).includes("Read only"));
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "larkbridge-submit", { query: "example", kind: "messages" }) });
      snapshot = await settled(h, session, "Example search result");
      assert.ok(body(snapshot).includes("Example Contact"));
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "larkbridge-back") });
      snapshot = await settled(h, session, "oc_example_dm");
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "read-oc_example_group") });
      snapshot = await settled(h, session, "Newer example message");
      if (version >= 3) {
        await h.call("ui.page.dispose", { session });
        const mounted = await h.call("ui.page.mount", { session: `topic-${version}`, params: { contribution: "larkbridge", locale: "en", ui_version: version, state: JSON.stringify({ mode: "thread", thread: "omt_example_thread", title: "Example Group" }) } });
        assert.equal(mounted.error, undefined);
        snapshot = await settled(h, `topic-${version}`, "Newer example message");
      }
      if (version === 1) {
        assert.equal(body(snapshot).includes("thread-1"), true);
        const event = interaction(snapshot, "thread-1");
        const answer = await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: event });
        assert.equal(answer.error, undefined, JSON.stringify(answer.error));
        snapshot = await settled(h, session, "Newer example message");
      }
      assert.equal(h.fixture.requests.some(request => request.operation === "messages" && request.thread === "omt_example_thread"), true);
      await h.call("ui.page.dispose", { session });
    }
  } finally { h.p.close(); }
});
test("document links, state validation and cancellation are safe", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "docs", params: { contribution: "larkbridge", locale: "zh-Hans", ui_version: 5, state: JSON.stringify({ mode: "search", kind: "docs", q: "Example" }) } });
    const snapshot = await settled(h, "docs", "Example design");
    assert.ok(body(snapshot).includes("https://example.feishu.cn/docx/example_token"));
    assert.ok(body(snapshot).includes("只读"));
    await h.call("ui.page.mount", { session: "invalid", params: { contribution: "larkbridge", ui_version: 1, state: JSON.stringify({ mode: "chat", chat: "../private" }) } });
    await settled(h, "invalid", "Example Group");
    const local = new h.bundle.LarkBridge(h.fixture);
    const pure = h.bundle.snapshotPage(local);
    const view = await pure.render({ contribution: "larkbridge", locale: "en", ui_version: 1, hub: {}, state: h.bundle.encodeState({ mode: "chat", chat: "oc_example_group", title: "Example Group" }) });
    assert.ok(JSON.stringify(view).includes("Newer example message"));
    assert.equal(h.fixture.requests.some(request => "command" in request || "shell" in request || "profile" in request), false);
    await h.call("ui.page.dispose", { session: "docs" });
  } finally { h.p.close(); }
});
test("connector tools reject writes and arbitrary host parameters", async () => {
  const h = await setup();
  try {
    const write = await h.call("connector.tools.call", { name: "larkbridge_send", args: {} });
    assert.equal(write.error.code, "unknown_method");
    assert.equal((await h.call("connector.tools.call", { name: "larkbridge_docs", args: { query: "Example", command: "logout" } })).error.code, "invalid");
    assert.equal(h.fixture.requests.some(request => /send|logout|write/.test(JSON.stringify(request))), false);
  } finally { h.p.close(); }
});
