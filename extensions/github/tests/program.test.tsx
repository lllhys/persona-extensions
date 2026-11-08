import { test } from "node:test";
import assert from "node:assert/strict";
import { connect, type Program } from "@persona/extension";
import type { Snapshot, UIEvent } from "@persona/extension/ui";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { createHash } from "node:crypto";
import { Fixture } from "./fixtures.ts";
async function setup() {
  const bundle = await import(pathToFileURL(resolve(".source-dist/github/bundle/main.mjs")).href);
  const f = new Fixture(), frames: any[] = []; let p: ReturnType<typeof connect>;
  p = connect(bundle.createProgram(f.fetch, { background: false, now: () => f.now }) as Program, line => {
    const frame = JSON.parse(line); frames.push(frame);
    if (frame.method?.startsWith("connector.secrets.") || frame.method === "connector.credentials.gh_token") void f.host.call(frame.method, frame.params).then(result => p.line(JSON.stringify({ id: frame.id, result })), error => p.line(JSON.stringify({ id: frame.id, error: { message: error.message, code: error.code } })));
  });
  let id = 0;
  const call = async (method: string, params: unknown = {}) => { const request = "t" + ++id; p.line(JSON.stringify({ id: request, method, params })); await p.drain(); return frames.find(frame => frame.id === request); };
  await call("initialize", { protocol: 1, config: { auth: { from_machine: "synthetic-computer" } }, active: { connectors: ["github"], ui: ["github"] }, hub: { locale: "en" }, granted: { gh_login: true } });
  await call("connector.machines.online", { contribution: "github", machine: "synthetic-computer" });
  return { f, frames, p, call, bundle };
}
async function until(read: () => Promise<any>, predicate: (value: any) => boolean) { const deadline = Date.now() + 3000; while (Date.now() < deadline) { const value = await read(); if (predicate(value)) return value; await new Promise(resolve => setTimeout(resolve, 2)); } throw new Error("Program session did not settle before timeout"); }
function event(snapshot: Snapshot, key: string, values: Record<string, string> = {}): UIEvent { const item = snapshot.view.sections.flatMap(s => s.items).find(i => i.key === key); assert.ok(item && "action" in item && item.action); return { generation: snapshot.generation, version: snapshot.version, key, actionId: item.action.id, eventId: crypto.randomUUID(), values }; }
const has = (frame: any, text: string) => JSON.stringify(frame?.result ?? {}).includes(text);
test("contained package has immutable released identity, offline source hash and complete files", async () => {
  const manifest = JSON.parse(await readFile(".source-dist/github/persona-extension.json", "utf8")), original = JSON.parse(await readFile("persona-extension.json", "utf8")), bytes = await readFile(".source-dist/github/" + manifest.runtime.source.bundle);
  assert.equal(manifest.runtime.mode, "deno"); assert.equal(manifest.runtime.command, undefined); assert.ok(original.runtime.command); assert.equal(manifest.version, original.version); assert.equal(manifest.runtime.source.sha256, createHash("sha256").update(bytes).digest("hex")); assert.deepEqual(manifest.permissions, original.permissions); assert.deepEqual(manifest.contributes.connectors, original.contributes.connectors); assert.equal(manifest.contributes.ui.filter((u: any) => u.placement === "nav_page").length, 1);
  for (const name of ["source/extensions/github/src/index.ts", "source/extensions/github/src/root.tsx", "source/extensions/github/src/tools.json", "source/extensions/github/src/locales/en.json", "source/extensions/github/src/locales/zh-Hans.json", "source/extensions/github/src/locales/ja.json", "source/extensions/github/package.json", "source/extensions/github/package-lock.json", "source/extensions/github/tests/parity.test.tsx", "source/extensions/github/tests/regressions.test.tsx", "source/extensions/github/scripts/test.mjs", "source/sdk/ts/src/index.ts", "persona-plugin.json"]) assert.ok((await readFile(".source-dist/github/" + name)).length);
  const bundle = await import(pathToFileURL(resolve(".source-dist/github/bundle/main.mjs")).href); assert.equal(bundle.default.id, "github"); assert.equal(bundle.default.fetch, undefined); assert.ok(bundle.default.namespaces.connector); assert.ok(bytes.length < (16 << 20));
});
test("separately bundled default Program initializes and calls existing encrypted credentials", async () => { const h = await setup(); try { assert.equal((await h.call("connector.status")).result.state, "ok"); assert.ok(h.f.calls.some(c => c.method === "connector.credentials.gh_token")); assert.equal((await h.call("connector.tools.list")).result.tools.length, 4); assert.equal((await h.call("connector.actions.list")).result.tools.length, 2); assert.equal((await h.call("connector.tools.call", { name: "github_read", args: { repo: "example/repo", number: 12 } })).result.text.includes("Synthetic pull request"), true); assert.equal((await h.call("connector.status", { contribution: "other" })).error.code, "no_contribution"); assert.equal(JSON.stringify(h.frames.filter(f => !f.method?.includes("credentials") && !f.method?.includes("secrets") && f.id?.startsWith("t"))).includes("synthetic-machine-token"), false); } finally { h.p.close(); } });
test("component sessions are isolated; callback tickets, replay and stale versions are enforced", async () => { const h = await setup(); try {
  assert.deepEqual((await h.call("ui.page.supported")).result.contributions, ["github"]);
  assert.equal((await h.call("ui.render", { contribution: "github", ui_version: 7 })).error.code, "unsupported");
  for (const session of ["one", "two"]) { await h.call("ui.page.mount", { session, params: { contribution: "github", locale: "en", ui_version: 7 } }); await until(() => h.call("ui.page.snapshot", { session }), f => has(f, "Synthetic pull request")); }
  let snapshot: Snapshot = (await h.call("ui.page.snapshot", { session: "one" })).result;
  const interaction = event(snapshot, "refresh");
  assert.ok((await h.call("ui.page.event", { session: "one", interaction })).error);
  const accepted = (await h.call("ui.page.event", { session: "one", ticket: "synthetic-host-ticket", interaction })).result; assert.equal(accepted.status, "accepted");
  const settled: Snapshot = (await until(() => h.call("ui.page.snapshot", { session: "one" }), f => has(f, "Synthetic pull request"))).result;
  const replay = (await h.call("ui.page.event", { session: "one", ticket: "synthetic-host-ticket", interaction })).result; assert.equal(replay.snapshot.version, settled.version);
  assert.equal((await h.call("ui.page.event", { session: "one", ticket: "synthetic-host-ticket", interaction: { ...interaction, eventId: crypto.randomUUID() } })).result.status, "ambiguous");
  assert.equal((await h.call("ui.page.event", { session: "two", ticket: "synthetic-host-ticket", interaction })).result.status, "ambiguous");
  await h.call("ui.page.dispose", { session: "one" });
  const remount = (await h.call("ui.page.mount", { session: "new", params: { contribution: "github", ui_version: 7 } })).result; assert.notEqual(remount.generation, snapshot.generation);
} finally { h.p.close(); } });
test("suspend cancels effects and resume preserves an unchanged callback table", async () => { const h = await setup(); try {
  await h.call("ui.page.mount", { session: "hidden", params: { contribution: "github", locale: "en", ui_version: 7 } });
  await until(() => h.call("ui.page.snapshot", { session: "hidden" }), f => has(f, "Synthetic pull request"));
  await h.call("ui.page.suspend", { session: "hidden" });
  const resumed: Snapshot = (await h.call("ui.page.resume", { session: "hidden" })).result;
  await new Promise(resolve => setTimeout(resolve, 20)); const settled: Snapshot = (await h.call("ui.page.snapshot", { session: "hidden" })).result;
  assert.equal(settled.version, resumed.version);
  assert.equal((await h.call("ui.page.event", { session: "hidden", ticket: "synthetic-host-ticket", interaction: event(resumed, "refresh") })).result.status, "accepted");
  await h.call("ui.page.dispose", { session: "hidden" });
} finally { h.p.close(); } });
test("cold native snapshot transport settles the actual contribution and saved preview callbacks", { timeout: 10000 }, async () => {
  const h = await setup();
  try {
    for (const ui_version of [1, 3, 5]) {
      const session = "cold-" + ui_version;
      const mounted = await h.call("ui.page.mount", { session, transport: "snapshot", params: { contribution: "github", ui_version, state: JSON.stringify({ r: "example/repo", n: 12, t: "discussion" }), locale: "zh-Hans" } });
      assert.ok(!mounted.error, JSON.stringify(mounted.error)); assert.ok(has(mounted, "Synthetic comment")); assert.ok(!has(mounted, '"key":"loading"'));
      await h.call("ui.page.dispose", { session });
      const write = "write-cold-" + ui_version;
      const initial = await h.call("ui.page.mount", { session: write, transport: "snapshot", params: { contribution: "github", ui_version, state: JSON.stringify({ v: "write", w: "comment", r: "example/repo", n: 12 }), locale: "en" } });
      const preview = await h.call("ui.page.event", { session: write, ticket: "synthetic-host-ticket", interaction: event(initial.result, "preview", { body: "Synthetic comment preview" }) });
      assert.equal(preview.result.status, "accepted"); assert.ok(JSON.stringify(preview.result).includes("confirm")); assert.ok(JSON.stringify(preview.result).includes("Synthetic comment preview"));
      await h.call("ui.page.dispose", { session: write });
    }
  } finally { h.p.close(); }
});
test("cold snapshot cancellation aborts the actual fetch and permits a clean remount", { timeout: 5000 }, async () => {
  const h = await setup(); let entered!: () => void, aborted = false;
  const started = new Promise<void>(resolve => { entered = resolve; });
  try {
    h.f.overrides = (url, init) => url.pathname === "/search/issues" ? new Promise<Response>((_resolve, reject) => {
      const signal = init.signal!; entered();
      const cancel = () => { aborted = true; reject(signal.reason); };
      if (signal.aborted) cancel(); else signal.addEventListener("abort", cancel, { once: true });
    }) : undefined;
    h.p.line(JSON.stringify({ id: "cancel-cold", method: "ui.page.mount", params: { session: "canceled", transport: "snapshot", params: { contribution: "github" } } }));
    await started; h.p.line(JSON.stringify({ event: "rpc.cancel", params: { id: "cancel-cold" } })); await h.p.drain();
    assert.equal(aborted, true); assert.ok(h.frames.find(frame => frame.id === "cancel-cold").error);
    h.f.overrides = undefined;
    const mounted = await h.call("ui.page.mount", { session: "canceled", transport: "snapshot", params: { contribution: "github" } });
    assert.ok(!mounted.error, JSON.stringify(mounted.error)); assert.ok(has(mounted, "Synthetic pull request"));
  } finally { h.p.close(); }
});
test("cold snapshot HTTP errors settle to the localized native error instead of Loading", { timeout: 5000 }, async () => {
  const h = await setup();
  try {
    h.f.overrides = url => url.pathname === "/search/issues" ? new Response("", { status: 503 }) : undefined;
    const result = await h.call("ui.page.mount", { session: "cold-error", transport: "snapshot", params: { contribution: "github", locale: "ja" } });
    assert.ok(!result.error, JSON.stringify(result.error)); assert.ok(has(result, '"key":"error"')); assert.ok(!has(result, '"key":"loading"'));
  } finally { h.p.close(); }
});
test("bundled Program callbacks expand a real diff and return correlated file context", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "diff", params: { contribution: "github", locale: "en", ui_version: 7, state: JSON.stringify({ r: "example/repo", n: 12, t: "files" }) } });
    let snapshot: Snapshot = (await until(() => h.call("ui.page.snapshot", { session: "diff" }), f => has(f, '"type":"diff"'))).result;
    let diff = snapshot.view.sections.flatMap(s => s.items).find(item => item.type === "diff"); assert.ok(diff?.type === "diff" && diff.context && diff.expand);
    const request: UIEvent = { generation: snapshot.generation, version: snapshot.version, key: diff.key!, actionId: diff.context.id, eventId: crypto.randomUUID(), values: { path: "file.ts", side: "old", from: "1", to: "2" } };
    const context = await h.call("ui.page.event", { session: "diff", ticket: "synthetic-host-ticket", interaction: request });
    assert.equal(context.result.status, "accepted"); assert.ok(JSON.stringify(context.result).includes('"start":1')); assert.ok(JSON.stringify(context.result).includes('"old"'));
    snapshot = (await h.call("ui.page.snapshot", { session: "diff" })).result;
    diff = snapshot.view.sections.flatMap(s => s.items).find(item => item.type === "diff"); assert.ok(diff?.type === "diff" && diff.expand);
    await h.call("ui.page.event", { session: "diff", ticket: "synthetic-host-ticket", interaction: { generation: snapshot.generation, version: snapshot.version, key: diff.key!, actionId: diff.expand.id, eventId: crypto.randomUUID(), values: { path: "file.ts" } } });
    const expanded = (await until(() => h.call("ui.page.snapshot", { session: "diff" }), f => has(f, '\\"v\\":\\"diff\\"'))).result;
    assert.ok(JSON.stringify(expanded).includes("file.ts"));
  } finally { h.p.close(); }
});
test("component page navigation and preview send preserve native confirmation metadata", async () => { const h = await setup(); try {
  await h.call("ui.page.mount", { session: "write", params: { contribution: "github", locale: "en", ui_version: 7, state: JSON.stringify({ v: "write", w: "comment", r: "example/repo", n: 12 }) } });
  let snapshot: Snapshot = (await until(() => h.call("ui.page.snapshot", { session: "write" }), f => has(f, '"key":"preview"'))).result;
  await h.call("ui.page.event", { session: "write", ticket: "synthetic-host-ticket", interaction: event(snapshot, "preview", { body: "Approved synthetic UI comment" }) });
  snapshot = (await until(() => h.call("ui.page.snapshot", { session: "write" }), f => has(f, '"key":"send"'))).result;
  const send = snapshot.view.sections.flatMap(s => s.items).find(i => i.key === "send"); assert.ok(send?.type === "button" && send.action.confirm);
  const interaction = event(snapshot, "send", { body: "Unapproved replacement" });
  assert.equal((await h.call("ui.page.event", { session: "write", ticket: "synthetic-host-ticket", interaction })).result.status, "accepted");
  await h.call("ui.page.event", { session: "write", ticket: "synthetic-host-ticket", interaction });
  const writes = h.f.hits.filter(h => h.method === "POST" && h.url.endsWith("/issues/12/comments")); assert.equal(writes.length, 1); assert.equal(JSON.parse(writes[0]!.body).body, "Approved synthetic UI comment");
} finally { h.p.close(); } });
