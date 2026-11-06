/** @jsxImportSource @persona/extension */
import { expect, test } from "bun:test";
import { connect } from "./index.ts";
import { Carousel, Grid, Section, View, mount, useFetch, useEffect, type StateAdapter } from "./ui.ts";

const allow = { authorize: () => true };
const settle = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(yes => { resolve = yes; });
  return { resolve, promise };
}

test("tile arrays bound every slot before allocation or reading tiles", () => {
  for (const Tiles of [Grid, Carousel]) {
    const page = (tiles: Parameters<typeof Grid>[0]["tiles"]) => <View key="root"><Section key="main"><Tiles key="tiles" tiles={tiles} /></Section></View>;
    expect(() => mount(page(Array(33)), { ...allow, limits: { nodes: 16 } })).toThrow("node limit");
    expect(() => mount(page(Array(65)), { ...allow, limits: { props: 32 } })).toThrow("prop limit");
    const wide = Array(100);
    let reads = 0;
    Object.defineProperty(wide, 0, { get() { reads++; throw new Error("tile read before bounds"); } });
    expect(() => mount(page(wide), { ...allow, limits: { nodes: 16 } })).toThrow("node limit");
    expect(reads).toBe(0);
    expect(() => mount(page(Array(1)), allow)).toThrow("UI key");
    expect(() => mount(page([{ key: "tile", title: "fixture" }, ,] as never), allow)).toThrow("UI key");
    const empty = mount(page([]), allow);
    expect(empty.current().view.sections[0].items[0]).toMatchObject({ tiles: [] });
    empty.dispose();
  }
});

test("changed fetch URL/dependencies/null are current in the first published snapshot without render-time writes", async () => {
  const requests: { signal: AbortSignal; result: ReturnType<typeof deferred<Response>> }[] = [];
  let rendering = false;
  const state: StateAdapter = { create<T>(initial: T) {
    let value = initial;
    return { get: () => value, set: (next: T) => { expect(rendering).toBe(false); value = next; } };
  } };
  function Page({ url, resource }: { url: string | null; resource: number }) {
    rendering = true;
    const result = useFetch<string>(url, { dependencies: [resource] });
    rendering = false;
    return <View key="root" title={JSON.stringify(result)} />;
  }
  const fetch = (_: string, init: RequestInit & { signal: AbortSignal }) => {
    const result = deferred<Response>(); requests.push({ signal: init.signal, result }); return result.promise;
  };
  const session = mount(<Page key="page" url="https://example.com/a" resource={1} />, { ...allow, fetch, state });
  requests[0].result.resolve(Response.json("a")); await settle();
  expect(JSON.parse(session.current().view.title!)).toEqual({ status: "success", data: "a" });
  expect(JSON.parse(session.render(<Page key="page" url="https://example.com/b" resource={1} />).view.title!)).toEqual({ status: "loading" });
  requests[1].result.resolve(Response.json("b")); await settle();
  expect(JSON.parse(session.current().view.title!)).toEqual({ status: "success", data: "b" });
  expect(JSON.parse(session.render(<Page key="page" url="https://example.com/b" resource={2} />).view.title!)).toEqual({ status: "loading" });
  const canceled = requests[2];
  expect(JSON.parse(session.render(<Page key="page" url={null} resource={2} />).view.title!)).toEqual({ status: "idle" });
  expect(canceled.signal.aborted).toBe(true);
  canceled.result.resolve(Response.json("late")); await settle();
  expect(JSON.parse(session.current().view.title!)).toEqual({ status: "idle" });
  expect(JSON.parse(session.render(<Page key="page" url="https://example.com/a" resource={1} />).view.title!)).toEqual({ status: "loading" });
  requests[3].result.resolve(Response.json("new-a")); await settle();
  expect(JSON.parse(session.current().view.title!)).toEqual({ status: "success", data: "new-a" });
  expect(session.takeErrors()).toEqual([]); session.dispose();
});

test("fetch identity remains current when effect capacity delays a replacement and URL returns", async () => {
  const blocker = deferred<void>();
  let block = false;
  function Page({ url }: { url: string }) {
    useEffect(() => block ? blocker.promise : undefined, [block]);
    const result = useFetch<string>(url);
    return <View key="root" title={JSON.stringify(result)} />;
  }
  const session = mount(<Page key="page" url="https://example.com/a" />, { ...allow, limits: { pendingEffects: 1 }, fetch: async () => Response.json("a") });
  await settle();
  expect(JSON.parse(session.current().view.title!)).toEqual({ status: "success", data: "a" });
  block = true;
  expect(JSON.parse(session.render(<Page key="page" url="https://example.com/b" />).view.title!)).toEqual({ status: "loading" });
  expect(JSON.parse(session.render(<Page key="page" url="https://example.com/a" />).view.title!)).toEqual({ status: "loading" });
  blocker.resolve(); await settle();
  expect(JSON.parse(session.current().view.title!)).toEqual({ status: "success", data: "a" });
  session.dispose();
});

test("fetch execution and state writes wait for publication and failed renders cancel old work", async () => {
  let published = false, writes = 0;
  const requests: { signal: AbortSignal; result: ReturnType<typeof deferred<Response>> }[] = [];
  const state: StateAdapter = { create<T>(initial: T) {
    let value = initial;
    return { get: () => value, set: (next: T) => { expect(published).toBe(true); writes++; value = next; } };
  } };
  function Page({ url, invalid = false }: { url: string; invalid?: boolean }) {
    published = false;
    const result = useFetch<string>(url);
    return <View key="root" title={JSON.stringify(result)} {...(invalid ? { unknown: "fixture" } : {})} />;
  }
  const session = mount(<Page key="page" url="https://example.com/a" />, {
    ...allow, state, onRender: () => { published = true; },
    fetch: (_, { signal }) => { expect(published).toBe(true); const result = deferred<Response>(); requests.push({ signal, result }); return result.promise; },
  });
  expect(requests).toHaveLength(1);
  const before = writes;
  expect(() => session.render(<Page key="page" url="https://example.com/b" invalid />)).toThrow("unknown native component prop");
  expect(requests).toHaveLength(1); expect(writes).toBe(before); expect(requests[0].signal.aborted).toBe(true);
  requests[0].result.resolve(Response.json("late")); await settle();
  expect(writes).toBe(before); expect(session.disposed).toBe(true);
});

test("tile traversal uses bounded numeric slots instead of custom iterators", () => {
  for (const Tiles of [Grid, Carousel]) {
    const tiles = [{ key: "tile", title: "fixture" }, ,];
    let iterations = 0;
    Object.defineProperty(tiles, Symbol.iterator, { value: function* () { iterations++; yield tiles[0]!; } });
    expect(() => mount(<View key="root"><Section key="main"><Tiles key="tiles" tiles={tiles as never} /></Section></View>, allow)).toThrow("UI key");
    expect(iterations).toBe(0);
  }
});

test("fetch freshness survives state adapters that clone values", async () => {
  const state: StateAdapter = { create<T>(initial: T) {
    let value = structuredClone(initial);
    return { get: () => structuredClone(value), set: (next: T) => { value = structuredClone(next); } };
  } };
  function Page({ url }: { url: string | null }) {
    return <View key="root" title={JSON.stringify(useFetch<string>(url))} />;
  }
  const session = mount(<Page key="page" url="https://example.com/a" />, { ...allow, state, fetch: async url => Response.json(url.endsWith("/b") ? "b" : "a") });
  await settle();
  expect(JSON.parse(session.current().view.title!)).toEqual({ status: "success", data: "a" });
  expect(JSON.parse(session.render(<Page key="page" url="https://example.com/b" />).view.title!)).toEqual({ status: "loading" });
  await settle();
  expect(JSON.parse(session.current().view.title!)).toEqual({ status: "success", data: "b" });
  expect(JSON.parse(session.render(<Page key="page" url={null} />).view.title!)).toEqual({ status: "idle" });
  session.dispose();
});

test("incomplete replies retain pending calls for valid results or errors", async () => {
  const output: any[] = [];
  const conn = connect({ id: "example", version: "1.0.0" }, line => output.push(JSON.parse(line)));
  const pending = conn.hub.call("thing.read");
  const id = output.at(-1).id;
  let answered = false;
  pending.then(() => { answered = true; });
  for (const frame of [{ id }, { id, error: null }, { id, params: {} }]) conn.line(JSON.stringify(frame));
  await settle(); expect(answered).toBe(false);
  conn.line(JSON.stringify({ id, result: null })); expect(await pending).toBeNull();
  const failed = conn.hub.call("thing.read");
  conn.line(JSON.stringify({ id: output.at(-1).id, error: { message: "fixture failure", code: "unsupported" } }));
  await expect(failed).rejects.toThrow("fixture failure");
  conn.line(JSON.stringify({ id: "init", method: "initialize", params: { protocol: 1 } })); await conn.drain();
  conn.line(JSON.stringify({ id: "later", method: "ping" })); await conn.drain();
  expect(output.at(-1)).toEqual({ id: "later", result: {} }); conn.close();
});

test("invalid protocol frames cannot crash dispatch or consume pending replies", async () => {
  const output: any[] = [], events: unknown[] = [];
  const conn = connect({ id: "example", version: "1.0.0", namespaces: { thing: (method, params) => { events.push([method, params]); } } }, line => output.push(JSON.parse(line)));
  const malformed: unknown[] = [null, true, 1, "frame", [], {}, { method: true }, { method: 1 }, { event: {} }, { event: 1 }, { id: 1, method: "ping" }];
  for (const frame of malformed) expect(() => conn.line(JSON.stringify(frame))).not.toThrow();
  conn.line("{");
  conn.line(JSON.stringify({ id: "init", method: "initialize", params: { protocol: 1 } }));
  conn.line(JSON.stringify({ id: "ping", method: "ping" }));
  await conn.drain();
  expect(output).toEqual([{ id: "init", result: { protocol: 1, id: "example", version: "1.0.0" } }, { id: "ping", result: {} }]);
  for (const frame of malformed) expect(() => conn.line(JSON.stringify(frame))).not.toThrow();
  conn.line(JSON.stringify({ event: "thing.changed", params: { n: 1 } }));
  await settle(); expect(events).toEqual([["changed", { n: 1 }]]);
  const pending = conn.hub.call("thing.read");
  const id = output.at(-1).id;
  let answered = false;
  pending.then(() => { answered = true; });
  for (const error of [true, [], { message: 1 }, { message: "bad", code: {} }]) conn.line(JSON.stringify({ id, error }));
  await settle(); expect(answered).toBe(false);
  conn.line(JSON.stringify({ id, result: { value: "current" } }));
  expect(await pending).toEqual({ value: "current" });
  conn.line(JSON.stringify({ id: "later", method: "ping" })); await conn.drain();
  expect(output.at(-1)).toEqual({ id: "later", result: {} }); conn.close();
});
