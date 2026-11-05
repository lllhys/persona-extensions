import { expect, test } from "bun:test";
import { connect, decodeState, encodeState, ExtError, isWidget, open, type Program } from "./index.ts";

// A hub's end of connect(): send a request, wait for its answer.
function harness(prog: Program) {
  const answers = new Map<string, (f: any) => void>();
  const sent: any[] = [];
  const conn = connect(prog, (l) => {
    const f = JSON.parse(l);
    sent.push(f);
    if (f.id && !f.method) answers.get(f.id)?.(f);
  });
  let n = 0;
  const call = (method: string, params: unknown = {}) =>
    new Promise<any>((resolve) => {
      const id = `h${++n}`;
      answers.set(id, resolve);
      conn.line(JSON.stringify({ id, method, params }));
    });
  return { conn, call, sent };
}

const prog = (): Program => ({
  id: "example",
  version: "1.0.0",
  init: (init) => {
    expect(init.info.id).toBe("example");
  },
  namespaces: {
    thing: () => {
      throw new ExtError("no such thing", "not_found", { id: "x" });
    },
  },
  ui: {
    home: {
      render: (r) => ({ title: `${r.action ?? ""}|${r.state ?? ""}`, sections: [] }),
      action: (r) => {
        if (r.action === "toast") return { toast: "好" };
        r.state = "next";
      },
    },
  },
});

test("the protocol", async () => {
  const { call } = harness(prog());
  expect((await call("ping")).error.code).toBe("not_initialized");
  expect((await call("initialize", { protocol: 9 })).error.code).toBe("wrong_protocol");
  // A hub from before the rename sends the extension under plugin.
  const init = await call("initialize", { protocol: 1, hub: { build: 1 }, plugin: { id: "example", version: "1.0.0" }, granted: {} });
  expect(init.result).toEqual({ protocol: 1, id: "example", version: "1.0.0" });
  expect(await call("ping")).toEqual({ id: "h4", result: {} });
  expect((await call("thing.get")).error).toEqual({ message: "no such thing", code: "not_found", data: { id: "x" } });
  expect((await call("ui.render", { contribution: "nope" })).error.code).toBe("no_contribution");
  expect((await call("ui.action", { contribution: "home", action: "more", state: "first" })).result.view.title).toBe("more|next");
  expect((await call("ui.action", { contribution: "home", action: "toast" })).result).toEqual({ toast: "好" });
  expect((await call("other.method")).error.code).toBe("unknown_method");
  expect((await call("manifest")).error.code).toBe("unknown_method");
});

test("manifest goes out without tags", async () => {
  const { call } = harness({ ...prog(), manifest: { id: "example", version: "1.0.0", tags: ["示例"] } });
  expect((await call("manifest")).result).toEqual({ id: "example", version: "1.0.0" });
});

test("calls to the hub", async () => {
  const { conn, sent } = harness(prog());
  const p = conn.hub.call<{ value: string }>("connector.secrets.load", { key: "a" });
  const req = sent.at(-1);
  expect(req.method).toBe("connector.secrets.load");
  conn.line(JSON.stringify({ id: req.id, result: { value: "v" } }));
  expect(await p).toEqual({ value: "v" });
  conn.hub.invalidate("home");
  expect(sent.at(-1)).toEqual({ event: "ui.invalidate", params: { contribution: "home" } });
  conn.close();
  await expect(conn.hub.call("x")).rejects.toThrow("the hub has gone");
});

test("state", () => {
  expect(decodeState({ state: encodeState({ page: 2 }) }, { page: 0, q: "" })).toEqual({ page: 2, q: "" });
  expect(decodeState({}, { page: 0 })).toEqual({ page: 0 });
  expect(decodeState({ state: "{" }, { page: 0 })).toEqual({ page: 0 });
});

test("push for apps before UI version 3", async () => {
  const { call } = harness({
    id: "example",
    version: "1.0.0",
    ui: {
      home: {
        render: (r) =>
          r.state
            ? { title: `详情 ${r.state}`, sections: [] }
            : { title: "列表", sections: [{ items: [{ type: "row", title: "第一条", action: open("open", "", "1", "第一条") }] }] },
        action: (r) => (r.action === "save" ? { pop: true, toast: "存好了" } : undefined),
      },
    },
  });
  await call("initialize", { protocol: 1, hub: { build: 0 }, granted: {} });
  const newer = await call("ui.render", { contribution: "home", ui_version: 3 });
  expect(newer.result.sections[0].items[0].action).toEqual({ id: "open", push: { state: '"1"', title: "第一条" } });
  const old = await call("ui.render", { contribution: "home", ui_version: 2 });
  const id = old.result.sections[0].items[0].action.id;
  expect(id.startsWith("ext.push.")).toBe(true);
  expect((await call("ui.action", { contribution: "home", ui_version: 2, action: id })).result.view.title).toBe('详情 "1"');
  expect((await call("ui.action", { contribution: "home", ui_version: 2, action: "save", state: '"1"' })).result).toEqual({
    view: { title: "列表", sections: [{ items: [{ type: "row", title: "第一条", action: { id: "ext.push.2", push: { state: '"1"', title: "第一条" } } }] }] },
    toast: "存好了",
  });
  expect((await call("ui.action", { contribution: "home", ui_version: 3, action: "save" })).result).toEqual({ pop: true, toast: "存好了" });
});

test("shortcuts and widgets", async () => {
  const { call } = harness({
    id: "example",
    version: "1.0.0",
    ui: { glance: { render: (r) => ({ title: isWidget(r) ? `widget ${r.size}` : "page", sections: [] }) } },
    shortcuts: { count: (r) => ({ text: `${r.values.what}：3 个` }) },
  });
  await call("initialize", { protocol: 1, hub: { build: 0 }, granted: {} });
  expect((await call("shortcut.run", { contribution: "count", values: { what: "苹果" } })).result).toEqual({ text: "苹果：3 个" });
  expect((await call("shortcut.run", { contribution: "nope" })).error.code).toBe("no_contribution");
  expect((await call("shortcut.list", {})).error.code).toBe("unknown_method");
  expect((await call("ui.render", { contribution: "glance", placement: "widget", size: "small", ui_version: 3 })).result.title).toBe("widget small");
});
