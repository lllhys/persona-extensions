import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { connect, type Program } from "@persona/extension";
import { createRuntime } from "../src/index.ts";
import { Fixture, origin, sample, syntheticDSN, until, deferred } from "./fixtures.ts";
async function harness(runtime = createRuntime({ fetch: new Fixture().fetch })) {
  const frames: any[] = []; const conn = connect(runtime.program, line => frames.push(JSON.parse(line))); let next = 0;
  const call = async (method: string, params: unknown = {}) => {
    const id = String(++next); conn.line(JSON.stringify({ id, method, params })); await conn.drain(); return frames.find(frame => frame.id === id);
  };
  const initialize = (params: Record<string, unknown> = {}) => call("initialize", {
    protocol: 1, hub: { locale: "en", build: 1, extension_points: {} }, config: {}, secrets: { dsn: syntheticDSN },
    granted: { network: ["*.sentry.io"], error_reports: true }, ...params,
  });
  return { runtime, frames, conn, call, initialize, close: async () => { await runtime.service.stop(1000); await conn.shutdown(); } };
}
test("source archive package default Program and hashes preserve Go release identity without touching default manifest", async () => {
  const root = resolve("build/package"); const manifest = JSON.parse(await readFile(resolve(root, "persona-extension.json"), "utf8"));
  const releasedText = await readFile("RELEASED-MANIFEST.reference.json", "utf8").catch(error => {
    if (error.code !== "ENOENT") throw error;
    return readFile("persona-extension.json", "utf8");
  });
  const released = JSON.parse(releasedText);
  const bytes = await readFile(resolve(root, manifest.runtime.source.bundle));
  assert.equal(manifest.runtime.mode, "deno"); assert.equal(manifest.runtime.command, undefined);
  assert.equal(manifest.runtime.source.entry, "extensions/sentry/src/index.ts"); assert.equal(manifest.runtime.source.sha256, createHash("sha256").update(bytes).digest("hex"));
  assert.ok(bytes.length > 0 && bytes.length < (16 << 20));
  assert.equal(manifest.version, released.version); assert.ok(released.runtime.command); assert.equal(released.runtime.mode, undefined);
  assert.deepEqual(manifest.contributes, released.contributes); assert.equal(manifest.contributes.ui, undefined);
    assert.deepEqual(manifest.permissions, released.permissions);
  assert.deepEqual(manifest.configuration, released.configuration);
  assert.deepEqual(manifest.secrets, released.secrets);
  assert.deepEqual(manifest.activation_events, released.activation_events);
  assert.deepEqual(manifest.engines, released.engines);
  assert.equal(manifest.description, released.description);
  assert.deepEqual(manifest.tags, released.tags);
  for (const path of ["extensions/sentry/src/index.ts", "extensions/sentry/src/locales/en.json", "extensions/sentry/src/locales/zh-Hans.json", "extensions/sentry/src/locales/ja.json", "extensions/sentry/scripts/build-source.mjs", "extensions/sentry/tests/parity.test.ts", "extensions/sentry/package.json", "extensions/sentry/package-lock.json", "extensions/sentry/tsconfig.json", "sdk/ts/package.json", "sdk/go/go.mod"]) await readFile(resolve(root, path));
  assert.ok(!/\b(?:require|import)\s*\(/.test(bytes.toString()));
  const imported = await import(pathToFileURL(resolve(root, manifest.runtime.source.bundle)).href);
  assert.equal(imported.default.id, "sentry"); assert.equal(imported.default.version, "0.1.2");
  assert.equal(imported.default.manifest, undefined, "final digest lives in the external manifest, never a self-referential embedded copy");
  const frames: any[] = [], conn = connect(imported.default, line => frames.push(JSON.parse(line)));
  try {
    conn.line(JSON.stringify({ id: "manifest", method: "manifest" })); await conn.drain();
    assert.equal(frames[0].error.code, "unknown_method");
    assert.ok(frames[0].error.message.includes("in the package"));
  } finally { await conn.shutdown(); }
  for (const property of ["fetch", "ui", "shortcuts"]) assert.equal(imported.default[property], undefined);
  assert.deepEqual(Object.keys(imported.default.namespaces), ["error_reports"]);
});
test("actual separately contained bundle receives real namespace method and notification frames", async () => {
  const imported = await import(pathToFileURL(resolve("build/package/extensions/sentry/build/sentry.mjs")).href);
  const fixture = new Fixture();
  const h = await harness(imported.createRuntime({ fetch: fixture.fetch }) as ReturnType<typeof createRuntime>);
  try {
    assert.equal((await h.initialize()).result.id, "sentry");
    const report = sample();
    h.conn.line(JSON.stringify({ event: "error_reports.deliver", params: { contribution: "sentry", reports: [report] } }));
    await until(() => fixture.hits.length === 1); await h.runtime.service.flush(1000);
    assert.equal(h.runtime.service.state().sent, 1);
    assert.ok(fixture.hits[0].body.includes(report.message!));
    await h.call("error_reports.deliver", { contribution: "sentry", reports: [sample("second")] }); await h.runtime.service.flush(1000);
    assert.equal(fixture.hits.length, 2); assert.equal(h.runtime.service.state().sent, 2);
    assert.ok(!h.frames.some(frame => frame.method), "no invented Hub.call callback or external companion");
  } finally { await h.close(); }
});
test("initialize precedes delivery; Go nullable structs and ignored contribution remain supported", async () => {
  const h = await harness();
  try {
    assert.equal((await h.call("error_reports.deliver", { reports: [sample()] })).error.code, "not_initialized");
    await h.initialize();
    assert.equal((await h.call("error_reports.status")).error.code, "unknown_method");
    for (const params of [null, {}, { reports: null }, { contribution: "other", reports: [] }]) assert.equal((await h.call("error_reports.deliver", params)).error, undefined);
    for (const reports of ["bad", [{ ...sample(), tags: ["bad"] }], [{ ...sample(), exceptions: [{ type: "x", frames: [{ line: "1" }] }] }], [{ ...sample(), breadcrumbs: [{ category: 1 }] }]]) {
      assert.equal((await h.call("error_reports.deliver", { contribution: "sentry", reports })).error.code, "invalid_params");
    }
    assert.equal(h.runtime.service.state().sent, 0);
    assert.equal((await h.call("error_reports.deliver", { contribution: "other", reports: Array.from({ length: 21 }, (_, i) => sample(String(i))) })).error, undefined);
    await h.runtime.service.flush(1000); assert.equal(h.runtime.service.state().sent, 21);
  } finally { await h.close(); }
});
test("missing secrets and inactive contribution do not collect; repeated initialize does not reconfigure", async () => {
  for (const params of [{ secrets: {} }, { active: {} }, { active: { error_reports: ["other"] } }]) {
    const f = new Fixture(), h = await harness(createRuntime({ fetch: f.fetch }));
    try {
      assert.deepEqual((await h.initialize(params)).result.contributes, { error_reports: [] });
      await h.call("error_reports.deliver", { contribution: "sentry", reports: [sample()] }); await h.runtime.service.flush(); assert.equal(f.hits.length, 0);
    } finally { await h.close(); }
  }
  const f = new Fixture(), h = await harness(createRuntime({ fetch: f.fetch }));
  try {
    await h.initialize(); await h.initialize({ config: { destination: "https://unknown.example.com" }, secrets: { dsn: "invalid" } });
    await h.call("error_reports.deliver", { contribution: "sentry", reports: [sample()] }); await h.runtime.service.flush(); assert.equal(f.hits.length, 1);
  } finally { await h.close(); }
});
test("management-page-only policy: old fullView UI, callbacks, hidden/resume all reject a fabricated contribution", async () => {
  const h = await harness();
  try {
    await h.initialize(); assert.deepEqual((await h.call("ui.page.supported")).result, { contributions: [] });
    for (const version of [1, 3, 5, 6]) {
      assert.equal((await h.call("ui.render", { contribution: "sentry", ui_version: version })).error.code, "unknown_method");
      assert.equal((await h.call("ui.action", { contribution: "sentry", action: "diagnostic" })).error.code, "unknown_method");
    }
    const rejected = await h.call("ui.page.mount", { session: "fabricated", params: { contribution: "sentry" } }); assert.ok(rejected.error);
    for (const method of ["snapshot", "event", "suspend", "resume"]) assert.ok((await h.call(`ui.page.${method}`, { session: "fabricated", ticket: "synthetic-ticket" })).error);
  } finally { await h.close(); }
});
test("invalid original DSN/config echo neither DSN nor public key in protocol errors", async () => {
  for (const config of [{ allow_http: "yes" }, { sample_rate: "1" }, { environment: [] }]) {
    const h = await harness();
    try { const result = await h.initialize({ config }); assert.equal(result.error.code, "invalid_params"); assert.ok(!JSON.stringify(result).includes("fixture-key")); }
    finally { await h.close(); }
  }
});
test("bundled delivery accepts merged reports above the separate HTTP ingress byte limit", async () => {
  const imported = await import(pathToFileURL(resolve("build/package/extensions/sentry/build/sentry.mjs")).href);
  const reports = ["merged-a", "merged-b"].map(id => ({
    ...sample(id), fingerprint: id,
    exceptions: Array.from({ length: 3 }, () => ({
      type: "SyntheticError", value: "synthetic failure",
      frames: Array.from({ length: 64 }, () => ({ function: "f".repeat(256), module: "m".repeat(256), file: "s".repeat(256), line: 1, column: 1, in_app: true })),
    })),
  }));
  for (const report of reports) assert.ok(Buffer.byteLength(JSON.stringify({ reports: [report] })) < 256 << 10);
  const params = { contribution: "sentry", reports };
  assert.ok(Buffer.byteLength(JSON.stringify(params)) > 256 << 10);
  // The same reports succeed separately and after the host's count/time batching.
  for (const merged of [false, true]) {
    const f = new Fixture(), h = await harness(imported.createRuntime({ fetch: f.fetch }));
    try {
      await h.initialize();
      if (merged) {
        h.conn.line(JSON.stringify({ event: "error_reports.deliver", params }));
        await h.conn.drain();
      } else {
        for (const report of reports) assert.equal((await h.call("error_reports.deliver", { contribution: "sentry", reports: [report] })).error, undefined);
      }
      await h.runtime.service.flush(1000);
      assert.equal(f.hits.length, 2);
      assert.equal(h.runtime.service.state().sent, 2);
      assert.equal(h.runtime.service.state().dropped, 0);
      for (const hit of f.hits) assert.equal(JSON.parse(hit.body.trimEnd().split("\n")[2]).exception.values[0].stacktrace.frames.length, 64);
    } finally { await h.close(); }
  }
});
test("bundled external cancellation interrupts an existing graceful stop and discards its queue", async () => {
  const imported = await import(pathToFileURL(resolve("build/package/extensions/sentry/build/sentry.mjs")).href);
  const f = new Fixture(), cancel = new AbortController();
  f.gate = deferred<Response>();
  const runtime = imported.createRuntime({ fetch: f.fetch, signal: cancel.signal, timeout: 2000 }) as ReturnType<typeof createRuntime>;
  const h = await harness(runtime);
  try {
    await h.initialize();
    await h.call("error_reports.deliver", { contribution: "sentry", reports: [sample("blocked"), sample("queued")] });
    await until(() => f.hits.length === 1);
    const stopping = runtime.service.stop(800);
    assert.equal(runtime.service.state().phase, "stopping");
    cancel.abort();
    assert.equal(f.hits[0].signal?.aborted, true, "external cancel must abort synchronously, not wait for graceful deadline");
    assert.equal(runtime.service.state().queued, 0);
    assert.equal(runtime.service.stop(800), stopping);
    assert.equal(await stopping, false);
    assert.equal(runtime.service.state().phase, "stopped");
    assert.equal(runtime.service.state().sent, 0);
    assert.equal(runtime.service.state().dropped, 2);
    f.gate.resolve(new Response("late receipt"));
    await new Promise(resolve => setTimeout(resolve, 10));
    assert.equal(f.hits.length, 1);
    assert.equal(runtime.service.state().sent, 0);
    assert.equal(runtime.service.state().dropped, 2);
  } finally { f.gate.resolve(new Response("cleanup")); await h.close(); }
});
