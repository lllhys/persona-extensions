/** @jsxImportSource @persona/extension */
import { describe, expect, test } from "bun:test";
import {
  Button, Grid, Row, Section, Text, View, Fragment, callback, mount, useEffect, useInterval, useState,
  type Renderer, type UIEvent, type StateAdapter, type TimerAdapter,
} from "./ui.ts";

const allow = { authorize: () => true };
const settle = async () => { for (let i = 0; i < 16; i++) await Promise.resolve(); };
function button(session: Renderer, key = "press") {
  return session.current().view.sections.flatMap(section => section.items).find(item => item.key === key) as { action: { id: string } };
}
function request(session: Renderer, eventId: string = crypto.randomUUID(), values: unknown = {}, key = "press"): UIEvent {
  return { generation: session.generation, version: session.current().version, eventId, values, key, actionId: button(session, key).action.id };
}
function page(show = true) {
  return <View key="root"><Section key="main">{show ? <Button key="press" title="fixture" action={callback(() => {})} /> : null}</Section></View>;
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(yes => { resolve = yes; });
  return { resolve, promise };
}

describe("callback identity and deterministic replay", () => {
  test("ordinary renders preserve action IDs and button JSON while updating captured closures", async () => {
    let set!: (value: number) => void;
    let calls = 0;
    function Page() {
      const [count, update] = useState(0); set = update;
      return <View key="root"><Section key="main"><Text key="count" text={String(count)} />
        <Button key="press" title="fixture" action={callback(() => { calls++; update(count + 1); }, { confirm: "fixture", submit: true, values: { saved: "value" } })} />
      </Section></View>;
    }
    const session = mount(<Page key="page" />, allow);
    const before = JSON.stringify(button(session)); const firstId = button(session).action.id;
    set(7); set(8); await settle();
    expect(button(session).action.id).toBe(firstId);
    expect(JSON.stringify(button(session))).toBe(before);
    const event = request(session, "write-once");
    const result = session.event(event); await result; await settle();
    expect(session.current().view.sections[0].items[0]).toMatchObject({ text: "9" });
    expect(button(session).action.id).toBe(firstId);
    // The accepted retry is older than the current frame but must never repeat the write.
    expect(session.event({ ...event })).toBe(result); await session.event(event);
    expect(calls).toBe(1);
    await expect(session.event({ ...event, values: { changed: "body" } })).rejects.toThrow("body mismatch");
    await expect(session.event({ ...event, eventId: "new-stale-event" })).rejects.toThrow("stale");
    session.dispose();
  });

  test("removal, remount, ancestor function replacement and native type replacement rotate IDs", async () => {
    const session = mount(page(), allow); const first = request(session);
    session.render(page(false)); session.render(page());
    expect(button(session).action.id).not.toBe(first.actionId);
    await expect(session.event({ ...first, version: session.current().version })).rejects.toThrow("callback");
    session.dispose();
    function First() { return page(); }
    function Second() { return page(); }
    const functions = mount(<First key="page" />, allow); const id = button(functions).action.id;
    functions.render(<Second key="page" />); expect(button(functions).action.id).not.toBe(id); functions.dispose();
    const kinds = mount(page(), allow); const buttonId = button(kinds).action.id;
    kinds.render(<View key="root"><Section key="main"><Row key="press" title="fixture" action={callback(() => {})} /></Section></View>);
    expect(button(kinds).action.id).not.toBe(buttonId); kinds.dispose();
  });

  test("keyed function and tile reorders preserve callbacks; action slots stay distinct", () => {
    function Child({ id }: { id: string }) { return <Button key={id} title="fixture" action={callback(() => {})} />; }
    const children = (ids: string[]) => <View key="root"><Section key="main">{ids.map(id => <Child key={id} id={id} />)}</Section></View>;
    const session = mount(children(["a", "b"]), allow);
    const ids = [button(session, "a").action.id, button(session, "b").action.id];
    session.render(children(["b", "a"]));
    expect([button(session, "a").action.id, button(session, "b").action.id]).toEqual(ids); session.dispose();
    const tiles = (keys: string[]) => <View key="root"><Section key="main"><Grid key="grid" tiles={keys.map(key => ({ key, title: "fixture", action: callback(() => {}) }))} />
      <Row key="row" title="fixture" action={callback(() => {})} actions={[{ title: "fixture", action: callback(() => {}) }]} />
    </Section></View>;
    const grid = mount(tiles(["a", "b"]), allow);
    const before = grid.current().view.sections[0].items[0] as { tiles: { key: string; action: { id: string } }[] };
    const row = grid.current().view.sections[0].items[1] as { action: { id: string }; actions: { action: { id: string } }[] };
    expect(row.action.id).not.toBe(row.actions[0].action.id);
    grid.render(tiles(["b", "a"]));
    const after = grid.current().view.sections[0].items[0] as typeof before;
    expect(after.tiles.map(tile => tile.action.id)).toEqual(before.tiles.toReversed().map(tile => tile.action.id));
    grid.dispose();
  });

  test("canonical retries replay successes and failures without authorizing twice", async () => {
    let authorizations = 0; let writes = 0;
    const session = mount(<View key="root"><Section key="main"><Button key="press" title="fixture" action={callback(() => { writes++; throw new Error("write failed"); })} /></Section></View>,
      { authorize: () => { authorizations++; return true; }, limits: { events: 1 } });
    const event = request(session, "receipt", { b: "2", a: "1" });
    const result = session.event(event); await expect(result).rejects.toThrow("write failed");
    const retry = session.event({ ...event, values: { a: "1", b: "2" } });
    expect(retry).toBe(result); await expect(retry).rejects.toThrow("write failed");
    expect(writes).toBe(1); expect(authorizations).toBe(1);
    await expect(session.event({ ...event, eventId: "overflow" })).rejects.toThrow("stale");
    await expect(session.event(request(session, "new-revision"))).rejects.toThrow("write failed");
    await expect(session.event(event)).rejects.toThrow("stale");
    expect(writes).toBe(2); session.dispose();
  });

  test("authorization is mandatory for unconfirmed callbacks and saved metadata is immutable", async () => {
    expect(() => mount(page(), {} as never)).toThrow("authorize");
    let calls = 0;
    const session = mount(<View key="root"><Section key="main"><Button key="press" title="fixture" action={callback(() => { calls++; }, { submit: true, values: { saved: "value" } })} /></Section></View>,
      { authorize: (action, event) => { expect(action).toMatchObject({ submit: true, values: { saved: "value" } }); expect(Object.isFrozen(action)).toBe(true); expect(Object.isFrozen(action.values)).toBe(true); expect(Object.isFrozen(event)).toBe(true); return false; } });
    await expect(session.event(request(session))).rejects.toThrow("gate"); expect(calls).toBe(0); session.dispose();
    const native = mount(<View key="root"><Section key="main"><Button key="press" title="fixture" action={{ id: "native" }} /></Section></View>, allow);
    await expect(native.event(request(native))).rejects.toThrow("callback"); native.dispose();
  });

  test("saved action values override forged submitted values and preserve submit metadata", async () => {
    let received: unknown;
    const session = mount(<View key="root"><Section key="main"><Button key="press" title="fixture" action={callback(values => { received = values; }, { submit: true, values: { target: "saved" } })} /></Section></View>,
      { authorize: action => { expect(action.submit).toBe(true); expect(action.values).toEqual({ target: "saved" }); return true; } });
    await session.event(request(session, "saved-values", { target: "forged", field: "submitted" }));
    expect(received).toEqual({ target: "saved", field: "submitted" }); expect(Object.isFrozen(received)).toBe(true); session.dispose();
  });

  test("valid action-carried field names are data, not executable HTML", async () => {
    let received: unknown;
    const values = { html: "fixture", dangerouslySetInnerHTML: "fixture" };
    const session = mount(<View key="root"><Section key="main"><Button key="press" title="fixture"
      action={callback(data => { received = data; }, { values })} /></Section></View>, allow);
    await session.event(request(session));
    expect(received).toEqual(values);
    expect(button(session)).toMatchObject({ action: { values } });
    session.dispose();
  });

  test("awaited authorization rejects a changed version even if the callback ID is stable", async () => {
    const gate = deferred<boolean>(); let calls = 0;
    const session = mount(<View key="root"><Section key="main"><Button key="press" title="fixture" action={callback(() => { calls++; })} /></Section></View>, { authorize: () => gate.promise });
    const event = request(session); const result = session.event(event); await settle(); session.render();
    expect(button(session).action.id).toBe(event.actionId); gate.resolve(true);
    await expect(result).rejects.toThrow("stale"); expect(calls).toBe(0); session.dispose();
  });

  test("ongoing callbacks survive ordinary rendering but abort on unmount and suspension", async () => {
    const work = deferred<void>(); let signal!: AbortSignal; let set!: (n: number) => void;
    function Page({ show }: { show: boolean }) {
      const [n, update] = useState(0); set = update;
      return <View key="root"><Section key="main"><Text key="count" text={String(n)} />{show ? <Button key="press" title="fixture" action={callback(async (_, context) => { signal = context.signal; set(1); await work.promise; })} /> : null}</Section></View>;
    }
    const session = mount(<Page key="page" show={true} />, allow); const result = session.event(request(session)); await settle();
    expect(signal.aborted).toBe(false); session.render(<Page key="page" show={false} />); expect(signal.aborted).toBe(true);
    work.resolve(); await result; session.dispose();
    const pending = deferred<void>(); let active!: AbortSignal;
    const hidden = mount(<View key="root"><Section key="main"><Button key="press" title="fixture" action={callback(async (_, context) => { active = context.signal; await pending.promise; })} /></Section></View>, allow);
    const old = request(hidden); const promise = hidden.event(old); await settle(); hidden.suspend(); expect(active.aborted).toBe(true);
    hidden.resume(); expect(button(hidden).action.id).toBe(old.actionId);
    await expect(hidden.event(request(hidden))).rejects.toThrow("already running");
    pending.resolve(); await promise; hidden.dispose();
  });
});

describe("bounded hook and render lifecycle", () => {
  test("fragments and deeply nested keyed functions retain bounded identity paths", async () => {
    function Nested({ depth }: { depth: number }): ReturnType<typeof View> {
      return depth ? <Nested key={`level-${depth}`} depth={depth - 1} /> :
        <View key="root"><><Fragment key="group"><Section key="main"><Button key="press" title="fixture" action={callback(() => {})} /></Section></Fragment></></View>;
    }
    const session = mount(<Nested key="page" depth={28} />, allow);
    const id = button(session).action.id; session.render(); expect(button(session).action.id).toBe(id);
    await session.event(request(session)); session.dispose();
  });

  test("delayed render scheduling coalesces updates and drops obsolete work on suspend", () => {
    const tasks: (() => void)[] = []; let set!: (n: number) => void; let effects = 0;
    function Page() { const [n, update] = useState(0); set = update; useEffect(() => { effects++; }, [n]); return <View key="root" title={String(n)} />; }
    const session = mount(<Page key="page" />, { ...allow, schedule: task => { tasks.push(task); } });
    set(1); set(2); expect(tasks).toHaveLength(1); tasks.shift()!(); expect(session.current().view.title).toBe("2"); expect(effects).toBe(2);
    set(3); session.suspend(); set(4); tasks.shift()!(); expect(session.current().view.title).toBe("2");
    session.resume(); expect(session.current().view.title).toBe("4"); expect(effects).toBe(3); session.dispose();
  });

  test("coalescing preserves all effects and diagnostics even when publication throws", async () => {
    const starts: string[] = [];
    function Page() {
      const [n, set] = useState(0);
      useEffect(() => { starts.push("state"); set(1); }, []);
      useEffect(() => { starts.push("failure"); throw new Error("effect failed"); }, []);
      useEffect(() => { starts.push("last"); }, []);
      return <View key="root"><Section key="main"><Text key="value" text={String(n)} /></Section></View>;
    }
    const session = mount(<Page key="page" />, { ...allow, onRender: () => { throw new Error("transport failed"); }, onError: () => { throw new Error("reporter failed"); } });
    await settle(); expect(starts).toEqual(["state", "failure", "last"]); expect(session.disposed).toBe(false);
    expect(session.current().view.sections[0].items[0]).toMatchObject({ text: "1" });
    expect(session.takeErrors().map(error => (error as Error).message)).toEqual(["transport failed", "effect failed", "transport failed"]); session.dispose();
  });

  test("enforces callbacks, props, frames, hook cells, pending effects and bounded diagnostics", async () => {
    expect(() => mount(<View key="root"><Section key="main"><Row key="row" title="fixture" action={callback(() => {})} actions={[{ title: "fixture", action: callback(() => {}) }]} /></Section></View>, { ...allow, limits: { callbacks: 1 } })).toThrow("callback limit");
    expect(() => mount(<View key="root" title="fixture" />, { ...allow, limits: { props: 1 } })).toThrow("prop limit");
    expect(() => mount(<View key="root" title={"x".repeat(1024)} />, { ...allow, limits: { frameBytes: 256 } })).toThrow("frame limit");
    function Hooks() { useState(0); useState(0); return <View key="root" />; }
    expect(() => mount(<Hooks key="page" />, { ...allow, limits: { hooks: 1 } })).toThrow("hook limit");
    const pending = deferred<void>(); let ran = 0;
    function Effects() { useEffect(() => pending.promise, []); useEffect(() => { ran++; }, []); return <View key="root" />; }
    const effects = mount(<Effects key="page" />, { ...allow, limits: { pendingEffects: 1 } });
    expect(ran).toBe(0); expect(effects.takeErrors()[0]).toBeInstanceOf(Error); pending.resolve(); await settle();
    expect(ran).toBe(1);
    for (let n = 0; n < 100; n++) effects.report(new Error("fixture")); expect(effects.takeErrors()).toHaveLength(64); expect(effects.takeErrors()).toEqual([]); effects.dispose();
  });

  test("child traversal bounds include empty, sparse and fragment-only arrays", () => {
    const children = (nodes: ReturnType<typeof Fragment>) => <View key="root"><Section key="main">{nodes}</Section></View>;
    const options = { ...allow, limits: { props: 32 } };
    expect(() => mount(children(Array(100).fill(null)), options)).toThrow("child traversal limit");
    expect(() => mount(children(Array(100)), options)).toThrow("child traversal limit");
    expect(() => mount(children(Array.from({ length: 20 }, () => <Fragment />)), options)).toThrow("child traversal limit");
    const session = mount(children([null, false, <Fragment />]), options);
    expect(session.current().view.sections[0].items).toEqual([]);
    session.dispose();
  });

  test("JSON array and record widths are bounded before allocating copies or visiting values", async () => {
    const options = { ...allow, limits: { props: 32 } };
    const sparse = Array(100);
    expect(() => mount(<View key="root"><Section key="main"><Row key="row" title="fixture" spark={{ values: sparse }} /></Section></View>, options)).toThrow("prop limit");
    let reads = 0;
    const wide: Record<string, unknown> = {};
    for (let i = 0; i < 100; i++) Object.defineProperty(wide, String(i), { enumerable: true, get() { reads++; return "fixture"; } });
    const session = mount(page(), options);
    await expect(session.event(request(session, "wide", wide))).rejects.toThrow("value limit");
    expect(reads).toBe(0);
    await expect(session.event(request(session, "sparse", sparse))).rejects.toThrow("value limit");
    const parser = callback<unknown[]>(() => {}, {}, value => value as unknown[]);
    const arrays = mount(<View key="root"><Section key="main"><Button key="press" title="fixture" action={parser} /></Section></View>, allow);
    await expect(arrays.event(request(arrays, "holes", Array(1)))).rejects.toThrow("acyclic JSON");
    await arrays.event(request(arrays, "dense", [null]));
    session.dispose(); arrays.dispose();
  });

  test("frame budgets bound strings before copying and still check exact JSON bytes", async () => {
    expect(() => mount(<View key="root" title={"x".repeat(2048)} />, { ...allow, limits: { frameBytes: 512 } })).toThrow("frame limit");
    expect(() => mount(<View key="root" title={"\n".repeat(300)} />, { ...allow, limits: { frameBytes: 512 } })).toThrow("frame limit");
    const session = mount(page(), { ...allow, limits: { frameBytes: 512 } });
    const data = { a: "x".repeat(2048), get z() { throw new Error("oversized data was still copied"); } };
    await expect(session.event(request(session, "oversized", data))).rejects.toThrow("frame limit");
    await expect(session.event({ ...request(session), actionId: "x".repeat(2048) })).rejects.toThrow("invalid callback");
    await expect(session.event(request(session, "escaped", { a: "\n".repeat(300) }))).rejects.toThrow("frame limit");
    session.dispose();
  });

  test("pending-effect capacity is retried in order and does not restart work while hidden", async () => {
    const first = deferred<void>(), second = deferred<void>();
    const starts: string[] = [];
    let intervalStarts = 0;
    const timers: TimerAdapter = { setInterval: fn => { intervalStarts++; return fn; }, clearInterval: () => {} };
    function Page() {
      useEffect(() => { starts.push("first"); return first.promise; }, []);
      useEffect(() => { starts.push("second"); return second.promise; }, []);
      useInterval(() => {}, 100);
      return <View key="root" />;
    }
    const session = mount(<Page key="page" />, { ...allow, timers, limits: { pendingEffects: 1 } });
    expect(starts).toEqual(["first"]); expect(intervalStarts).toBe(0);
    first.resolve(); await settle();
    expect(starts).toEqual(["first", "second"]); expect(intervalStarts).toBe(0);
    second.resolve(); await settle(); expect(intervalStarts).toBe(1);
    session.dispose();

    let ran = 0;
    const pending = deferred<void>();
    function Hidden() { useEffect(() => pending.promise, []); useEffect(() => { ran++; }, []); return <View key="root" />; }
    const hidden = mount(<Hidden key="page" />, { ...allow, limits: { pendingEffects: 1 } });
    hidden.suspend(); pending.resolve(); await settle(); expect(ran).toBe(0);
    hidden.resume(); await settle(); expect(ran).toBe(1);
    hidden.dispose();
  });

  test("rejects cyclic/deep/oversized event data before authorization", async () => {
    let authorized = 0;
    const session = mount(page(), { authorize: () => { authorized++; return true; }, limits: { depth: 8, frameBytes: 512 } });
    const cycle: Record<string, unknown> = {}; cycle.self = cycle;
    await expect(session.event(request(session, "cycle", cycle))).rejects.toThrow("acyclic JSON");
    let deep: unknown = {}; for (let i = 0; i < 12; i++) deep = { deep };
    await expect(session.event(request(session, "deep", deep))).rejects.toThrow("value limit");
    await expect(session.event(request(session, "large", { value: "x".repeat(1024) }))).rejects.toThrow("frame limit");
    await expect(session.event(request(session, "nan", NaN))).rejects.toThrow("acyclic JSON");
    expect(authorized).toBe(0); session.dispose();
  });

  test("custom state cells are allocated per component and never retained after remount", async () => {
    let cells = 0; const setters: ((n: number) => void)[] = [];
    const state: StateAdapter = { create<T>(initial: T) { cells++; let value = initial; return { get: () => value, set: (next: T) => { value = next; } }; } };
    function Page() { const [n, set] = useState(0); setters.push(set); return <View key="root" title={String(n)} />; }
    const a = mount(<Page key="page" />, { ...allow, state }); const b = mount(<Page key="page" />, { ...allow, state });
    setters[0](4); await settle(); expect(a.current().view.title).toBe("4"); expect(b.current().view.title).toBe("0"); expect(cells).toBe(2);
    a.dispose(); b.dispose(); const next = mount(<Page key="page" />, { ...allow, state }); expect(next.current().view.title).toBe("0"); expect(cells).toBe(3); next.dispose();
  });

  test("rejected effects abort their own work without stopping a replacement generation", async () => {
    const signals: AbortSignal[] = [];
    const failures: ((error: unknown) => void)[] = [];
    function Page({ n }: { n: number }) {
      useEffect(signal => {
        signals.push(signal);
        return new Promise<void>((_, reject) => { failures.push(reject); });
      }, [n]);
      return <View key="root" />;
    }
    const session = mount(<Page key="page" n={0} />, allow);
    failures[0](new Error("first failed")); await settle();
    expect(signals[0].aborted).toBe(true);
    session.render(<Page key="page" n={1} />);
    session.render(<Page key="page" n={2} />);
    failures[1](new Error("old generation failed")); await settle();
    expect(signals[1].aborted).toBe(true);
    expect(signals[2].aborted).toBe(false);
    expect(session.takeErrors().map(error => (error as Error).message)).toEqual(["first failed", "old generation failed"]);
    session.dispose(); failures[2](new Error("disposed generation failed")); await settle();
    expect(signals[2].aborted).toBe(true);
  });

  test("interval work cannot overlap and rejection is reported after cancellation", async () => {
    let tick!: () => void; let calls = 0; let stops = 0;
    const timers: TimerAdapter = { setInterval: fn => { tick = fn; return fn; }, clearInterval: () => { stops++; } };
    const work = deferred<void>(); let active!: AbortSignal;
    function Page() { useInterval(async signal => { active = signal; calls++; await work.promise; throw new Error("interval failed"); }, 10); return <View key="root" />; }
    const session = mount(<Page key="page" />, { ...allow, timers }); tick(); tick(); await settle(); expect(calls).toBe(1);
    session.suspend(); expect(active.aborted).toBe(true); tick(); session.resume(); tick(); await settle(); expect(calls).toBe(1);
    work.resolve(); await settle(); expect(calls).toBe(1); expect(stops).toBe(1);
    expect(session.takeErrors().map(error => (error as Error).message)).toEqual(["interval failed"]); session.dispose(); expect(stops).toBe(2);
  });
});
