import { afterAll, expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { connect, ExtError, type Program } from "./index.ts";

const dir = mkdtempSync(join(tmpdir(), "persona-sdk-transport-"));
afterAll(() => rmSync(dir, { recursive: true, force: true }));

// Build a Program with its own SDK, as a source extension passed to an external runner would be.
test("a separately bundled Program keeps public ExtError code/data", async () => {
  const entry = join(dir, "program.ts");
  writeFileSync(entry, `import { ExtError } from ${JSON.stringify(join(import.meta.dir, "index.ts"))};
export const error = new ExtError("not available", "unavailable", { retry: true });
export const program = { id: "example", version: "1", namespaces: { fixture: (method) => {
  if (method === "native") throw Object.assign(new Error("no file"), { name: "ExtError", code: "ENOENT", data: { private: "native" } });
  if (method === "plain") throw { name: "ExtError", message: "ordinary", code: "unavailable", data: { private: "ordinary" } };
  if (method === "cycle") { const data = {}; data.self = data; throw new ExtError("cycle", "unavailable", data); }
  throw error;
} } };
`);
  const built = await Bun.build({ entrypoints: [entry], target: "browser", outdir: dir, naming: "program.mjs" });
  expect(built.success).toBe(true);
  const bundled = await import(join(dir, "program.mjs")) as { error: Error; program: Program };
  expect(bundled.error instanceof ExtError).toBe(false);
  const frames: any[] = [];
  const conn = connect(bundled.program, line => frames.push(JSON.parse(line)));
  conn.line(JSON.stringify({ id: "i", method: "initialize", params: { protocol: 1 } }));
  await conn.drain();
  conn.line(JSON.stringify({ id: "e", method: "fixture.read" }));
  await conn.drain();
  conn.close();
  expect(frames.at(-1)).toEqual({ id: "e", error: { message: "not available", code: "unavailable", data: { retry: true } } });
  for (const [method, error] of [
    ["native", { message: "no file" }],
    ["plain", { message: "ordinary" }],
    ["cycle", { message: "cycle", code: "unavailable" }],
  ]) {
    conn.line(JSON.stringify({ id: method, method: `fixture.${method}` }));
    await conn.drain();
    expect(frames.at(-1)).toEqual({ id: method, error });
  }
});

test("a symbol-resistant native error still gets a message-only answer", async () => {
  const frames: any[] = [];
  const error = new Proxy(Object.assign(new Error("ordinary proxy"), { code: "ENOENT", data: { private: true } }), {
    get(target, key, receiver) {
      if (typeof key === "symbol") throw new Error("symbol access denied");
      return Reflect.get(target, key, receiver);
    },
  });
  const conn = connect({ id: "example", version: "1", namespaces: { fixture: method => {
    if (method === "hostile") throw new Proxy({}, { get() { throw new Error("access denied"); } });
    if (method === "native") throw Object.defineProperties(new Error("native getters"), {
      code: { get() { throw new Error("code must stay local"); } },
      data: { get() { throw new Error("data must stay local"); } },
    });
    throw error;
  } } }, line => frames.push(JSON.parse(line)));
  conn.line(JSON.stringify({ id: "i", method: "initialize", params: { protocol: 1 } }));
  await conn.drain();
  for (const [method, message] of [["read", "ordinary proxy"], ["hostile", "error"], ["native", "native getters"]]) {
    conn.line(JSON.stringify({ id: method, method: `fixture.${method}` }));
    await conn.drain();
    expect(frames.at(-1)).toEqual({ id: method, error: { message } });
  }
  await conn.shutdown();
});

test("a hub error with a nullable absent code retains public data", async () => {
  const frames: any[] = [];
  let hub!: ReturnType<typeof connect>["hub"];
  let started!: () => void;
  const active = new Promise<void>(resolve => { started = resolve; });
  const conn = connect({ id: "example", version: "1", init: (_init, h) => { hub = h; }, namespaces: {
    fixture: () => { const result = hub.call("fixture.read"); started(); return result; },
  } }, line => frames.push(JSON.parse(line)));
  conn.line(JSON.stringify({ id: "i", method: "initialize", params: { protocol: 1 } }));
  await conn.drain();
  conn.line(JSON.stringify({ id: "e", method: "fixture.read" }));
  await active;
  conn.line(JSON.stringify({ id: frames.at(-1).id, error: { message: "try later", code: null, data: { retry: true } } }));
  await conn.shutdown();
  expect(frames.at(-1)).toEqual({ id: "e", error: { message: "try later", data: { retry: true } } });
});

test("shutdown closes pending hub calls before draining accepted handlers", async () => {
  const frames: any[] = [];
  let started!: () => void;
  const active = new Promise<void>(resolve => { started = resolve; });
  let hub!: ReturnType<typeof connect>["hub"];
  let calls = 0;
  const conn = connect({ id: "example", version: "1", init: (_init, h) => { hub = h; }, namespaces: {
    fixture: () => { calls++; const result = hub.call("fixture.read"); started(); return result; },
  } }, line => frames.push(JSON.parse(line)));
  conn.line(JSON.stringify({ id: "i", method: "initialize", params: { protocol: 1 } }));
  await conn.drain();
  conn.line(JSON.stringify({ id: "w", method: "fixture.wait" }));
  await active;
  const shutdown = conn.shutdown();
  expect(conn.hub.closed).toBe(true);
  expect(conn.shutdown()).toBe(shutdown);
  conn.line(JSON.stringify({ id: "late", method: "fixture.wait" }));
  conn.line(JSON.stringify({ event: "fixture.changed" }));
  await shutdown;
  expect(calls).toBe(1);
  expect(frames.at(-1)).toEqual({ id: "w", error: { message: "the hub has gone" } });
  await expect(conn.hub.call("fixture.read")).rejects.toThrow("the hub has gone");
});

test("shutdown drains ordinary accepted work, including calls not yet dispatched", async () => {
  const frames: any[] = [];
  let finish!: (value: unknown) => void;
  const ordinary = new Promise(resolve => { finish = resolve; });
  const conn = connect({ id: "example", version: "1", namespaces: { fixture: () => ordinary } }, line => frames.push(JSON.parse(line)));
  conn.line(JSON.stringify({ id: "i", method: "initialize", params: { protocol: 1 } }));
  conn.line(JSON.stringify({ id: "o", method: "fixture.read" }));
  let drained = false;
  const shutdown = conn.shutdown().then(() => { drained = true; });
  await Promise.resolve();
  expect(drained).toBe(false);
  finish({ value: "finished" });
  await shutdown;
  expect(frames).toEqual([
    { id: "i", result: { protocol: 1, id: "example", version: "1" } },
    { id: "o", result: { value: "finished" } },
  ]);
});

test("shutdown releases initialization blocked on a hub reply without authorizing later work", async () => {
  const frames: any[] = [];
  let started!: () => void;
  const active = new Promise<void>(resolve => { started = resolve; });
  let calls = 0;
  const conn = connect({ id: "example", version: "1", init: (_init, hub) => {
    const result = hub.call("fixture.setup"); started(); return result as Promise<void>;
  }, namespaces: { fixture: () => { calls++; } } }, line => frames.push(JSON.parse(line)));
  conn.line(JSON.stringify({ id: "i", method: "initialize", params: { protocol: 1 } }));
  conn.line(JSON.stringify({ id: "behind", method: "fixture.read" }));
  await active;
  await conn.shutdown();
  expect(calls).toBe(0);
  expect(frames.find(frame => frame.id === "i")).toEqual({ id: "i", error: { message: "the hub has gone" } });
  expect(frames.find(frame => frame.id === "behind").error.code).toBe("not_initialized");
});

test("repeated close and replay cannot replace a completed hub reply or reopen outbound traffic", async () => {
  const frames: any[] = [];
  let hub!: ReturnType<typeof connect>["hub"];
  let started!: () => void;
  const active = new Promise<void>(resolve => { started = resolve; });
  const conn = connect({ id: "example", version: "1", init: (_init, h) => { hub = h; }, namespaces: {
    fixture: () => { const result = hub.call("fixture.read"); started(); return result; },
  } }, line => frames.push(JSON.parse(line)));
  conn.line(JSON.stringify({ id: "i", method: "initialize", params: { protocol: 1 } }));
  await conn.drain();
  conn.line(JSON.stringify({ id: "e", method: "fixture.read" }));
  await active;
  const id = frames.at(-1).id;
  conn.line(JSON.stringify({ id, result: { value: "completed" } }));
  conn.close();
  conn.close();
  conn.line(JSON.stringify({ id, error: { message: "late replay" } }));
  await conn.shutdown();
  expect(frames.at(-1)).toEqual({ id: "e", result: { value: "completed" } });
  hub.notify("fixture.changed");
  await expect(hub.call("fixture.read")).rejects.toThrow("the hub has gone");
  expect(frames.length).toBe(3);
});

test("outbound close is separate from inbound initialization and drain stays nonclosing", async () => {
  const frames: any[] = [];
  const conn = connect({ id: "example", version: "1", namespaces: { fixture: () => ({ value: "allowed" }) } }, line => frames.push(JSON.parse(line)));
  conn.line(JSON.stringify({ id: "before", method: "fixture.read" }));
  await conn.drain();
  expect(conn.hub.closed).toBe(false);
  expect(frames.at(-1).error.code).toBe("not_initialized");
  conn.close();
  conn.line(JSON.stringify({ id: "i", method: "initialize", params: { protocol: 1 } }));
  conn.line(JSON.stringify({ id: "after", method: "fixture.read" }));
  await conn.drain();
  expect(frames.at(-1)).toEqual({ id: "after", result: { value: "allowed" } });
  await conn.shutdown();
});
