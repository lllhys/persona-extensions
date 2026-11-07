import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";

// The checker emits the SDK with tsc and makes a second module copy before this read-only fixture runs.
const entry = pathToFileURL(process.argv[2]);
const { connect, ExtError } = await import(entry);
const { ExtError: SourceError } = await import(new URL("./index-copy.js", entry));
assert.equal(typeof globalThis.Bun, "undefined");
async function within(promise) {
  let timer;
  try {
    return await Promise.race([promise, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error("transport fixture timed out")), 1000);
    })]);
  } finally { clearTimeout(timer); }
}
const publicError = new SourceError("not available", "unavailable", { retry: true });
assert.equal(publicError instanceof ExtError, false);
const frames = [];
let hub;
let started;
const active = new Promise(resolve => { started = resolve; });
const conn = connect({ id: "example", version: "1", init: (_init, h) => { hub = h; }, namespaces: {
  fixture: method => {
    if (method === "public") throw publicError;
    if (method === "native") throw Object.assign(new Error("no file"), { name: "ExtError", code: "ENOENT", data: { private: "native" } });
    if (method === "plain") throw { name: "ExtError", message: "ordinary", code: "unavailable", data: { private: "ordinary" } };
    if (method === "proxy") throw new Proxy(new Error("ordinary proxy"), {
      get(target, key, receiver) { if (typeof key === "symbol") throw new Error("symbol access denied"); return Reflect.get(target, key, receiver); },
    });
    if (method === "hostile") throw new Proxy({}, { get() { throw new Error("access denied"); } });
    if (method === "nativeGetters") throw Object.defineProperties(new Error("native getters"), {
      code: { get() { throw new Error("code must stay local"); } },
      data: { get() { throw new Error("data must stay local"); } },
    });
    const result = hub.call("fixture.read"); started(); return result;
  },
} }, line => frames.push(JSON.parse(line)));
conn.line(JSON.stringify({ id: "i", method: "initialize", params: { protocol: 1 } }));
await within(conn.drain());
assert.equal(hub.closed, false);
for (const [method, error] of [
  ["public", { message: "not available", code: "unavailable", data: { retry: true } }],
  ["native", { message: "no file" }], ["plain", { message: "ordinary" }],
  ["proxy", { message: "ordinary proxy" }], ["hostile", { message: "error" }], ["nativeGetters", { message: "native getters" }],
]) {
  conn.line(JSON.stringify({ id: method, method: `fixture.${method}` }));
  await within(conn.drain());
  assert.deepEqual(frames.at(-1), { id: method, error });
}
conn.line(JSON.stringify({ id: "w", method: "fixture.wait" }));
await within(active);
const shutdown = conn.shutdown();
assert.equal(hub.closed, true);
assert.equal(conn.shutdown(), shutdown);
conn.line(JSON.stringify({ id: "late", method: "ping" }));
await within(shutdown);
assert.deepEqual(frames.at(-1), { id: "w", error: { message: "the hub has gone" } });
assert.equal(frames.some(frame => frame.id === "late"), false);

const queuedFrames = [];
const queued = connect({ id: "example", version: "1", namespaces: { fixture: async () => ({ value: "finished" }) } }, line => queuedFrames.push(JSON.parse(line)));
queued.line(JSON.stringify({ id: "i", method: "initialize", params: { protocol: 1 } }));
queued.line(JSON.stringify({ id: "q", method: "fixture.read" }));
await within(queued.shutdown());
assert.deepEqual(queuedFrames.at(-1), { id: "q", result: { value: "finished" } });
const nullableFrames = [];
let nullableStarted;
const nullableActive = new Promise(resolve => { nullableStarted = resolve; });
let nullableHub;
const nullable = connect({ id: "example", version: "1", init: (_init, h) => { nullableHub = h; }, namespaces: {
  fixture: () => { const reply = nullableHub.call("fixture.read"); nullableStarted(); return reply; },
} }, line => nullableFrames.push(JSON.parse(line)));
nullable.line(JSON.stringify({ id: "i", method: "initialize", params: { protocol: 1 } }));
await within(nullable.drain());
nullable.line(JSON.stringify({ id: "e", method: "fixture.read" }));
await within(nullableActive);
nullable.line(JSON.stringify({ id: nullableFrames.at(-1).id, error: { message: "try later", code: null, data: { retry: true } } }));
await within(nullable.shutdown());
assert.deepEqual(nullableFrames.at(-1), { id: "e", error: { message: "try later", data: { retry: true } } });
console.log("PASS: TypeScript-emitted ESM transport shutdown, duplicate-module public errors and native error confidentiality without Bun globals");
