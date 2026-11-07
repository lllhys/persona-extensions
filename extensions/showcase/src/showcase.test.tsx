import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { connect } from "@persona/extension";
import { Renderer, View, Section, Progress, Code, Table, FileTree, CommentThread } from "@persona/extension/ui";
import { createRoot as createPage, createCard, createWidget, createShortcut, snapshot, componentCount } from "./ui.tsx";
import { ShowcaseStore, runShortcut, summarize, type Clock, type PageState } from "./services.ts";
import { catalog } from "./examples.tsx";
import { locales, translator, locale } from "./locale.ts";
import { createProgram } from "./main.tsx";

const fixtures = fileURLToPath(new URL("./legacy-snapshots/", import.meta.url));
const fixture = (name: string) => JSON.parse(readFileSync(`${fixtures}${name}.json`, "utf8"));
const settle = async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); };
function mount(state: PageState = {}, store = new ShowcaseStore(), version = 3, language = "zh-Hans", timeZone = "UTC") {
  // Trusted authorization is a test fixture, never extension permission or production consent.
  const renderer = new Renderer({ authorize: () => true });
  renderer.mount(createPage(store, { state, language, uiVersion: version, timeZone }));
  return renderer;
}
function find(renderer: Renderer, key: string): any {
  if (key === "showcase-view") return renderer.current().view;
  return renderer.current().view.sections.flatMap(section => [section, ...section.items]).find(node => node.key === key);
}
let event = 0;
async function click(renderer: Renderer, key: string, values: Record<string, string> = {}, slot = "action") {
  const current = renderer.current(), action = slot.split(".").reduce((value: any, name) => value?.[name], find(renderer, key));
  assert.ok(action?.id, `missing action on ${key}/${slot}`);
  const result = await renderer.event({ generation: current.generation, version: current.version, eventId: `test-${++event}`, key, actionId: action.id, values });
  await settle();
  return result as any;
}
function normalize(value: any): any {
  if (Array.isArray(value)) return value.map(normalize);
  if (value && typeof value === "object") return Object.fromEntries(Object.entries(value)
    .filter(([key, item]) => !["key", "id"].includes(key) && !(key === "state" && item === "{}"))
    .map(([key, item]) => [key, key === "state" ? JSON.parse(String(item)) : normalize(item)]));
  if (typeof value === "string" && (value.startsWith("https://raw.githubusercontent.com/nwverse/persona-extensions/main/extensions/showcase/assets/") || value.includes("avatars.githubusercontent.com") || value.includes("picsum.photos") || value.includes("example.invalid"))) return "<synthetic-image>";
  return value;
}
const staticPages = ["text", "metric", "progress", "row", "button", "link", "field", "chart", "stat_grid", "markdown", "table", "empty", "loading", "divider"];
test("official canonical View JSON fixtures preserve zero and required empty native values on Node and Bun", () => {
  const canonical = (name: string) => JSON.parse(readFileSync(new URL(`../../../sdk/ts/fixtures/UIStreamSnapshot.${name}.json`, import.meta.url), "utf8")).view;
  assert.deepEqual(snapshot(<View key="page" title="Example"><Section key="main">
    <Progress key="counter" label="Count" ratio={0} />
  </Section></View>), canonical("keyed"));
  assert.deepEqual(snapshot(<View key="page"><Section key="main">
    <Code key="code" text="" /><Table key="table" columns={[{ title: "Name" }]} rows={[]} />
    <FileTree key="files" entries={[]} /><CommentThread key="thread" comments={[]} />
  </Section></View>), canonical("required-empty"));
});
for (const page of staticPages) test(`JSX preserves every ${page} example and state`, () => {
  const renderer = mount({ p: page });
  try { assert.deepEqual(normalize(renderer.current().view), normalize(fixture(page))); } finally { renderer.dispose(); }
});
test("APP fixtures are retained unchanged in the source package", () => {
  const old = fileURLToPath(new URL("../snapshots/", import.meta.url));
  assert.deepEqual(readdirSync(fixtures).sort(), readdirSync(old).sort());
  for (const name of readdirSync(fixtures)) assert.deepEqual(readFileSync(fixtures + name), readFileSync(old + name));
});
test("catalog, card, all widget families and shortcut snapshots preserve payloads", () => {
  const renderer = mount();
  try { assert.deepEqual(normalize(renderer.current().view), normalize(fixture("home"))); } finally { renderer.dispose(); }
  assert.deepEqual(normalize(snapshot(createCard("zh-Hans"))), normalize(fixture("card")));
  for (const family of ["small", "medium", "large", "accessory_circular", "accessory_rectangular", "accessory_inline"]) {
    assert.deepEqual(normalize(snapshot(createWidget(family, "zh-Hans"))), normalize(fixture(`widget-${family}`)));
  }
  const result = createShortcut("run", "showcase-count", { n: "3", unit: "star" }, "zh-Hans");
  assert.deepEqual(normalize("view" in result ? result.view : undefined), normalize(fixture("shortcut-count")));
});
test("list/search/filter/more/detail/edit/segments and invalidate fixtures survive", () => {
  const states: Record<string, PageState> = {
    list: { p: "list" }, "list-search": { p: "list", q: "1" }, "list-filter": { p: "list", kind: "chat" },
    "list-more": { p: "list", shown: 40 }, "list-none": { p: "list", q: "没有这一条" },
    detail: { p: "detail", id: "3" }, edit: { p: "edit", id: "3" },
    segmented: { p: "segmented" }, "segmented-30d": { p: "segmented", range: "30d", answer: "no" },
    invalidate: { p: "invalidate" }, "invalidate-loading": { p: "invalidate" }, "invalidate-done": { p: "invalidate" },
  };
  for (const [name, state] of Object.entries(states)) {
    const store = new ShowcaseStore();
    if (name === "invalidate-loading" || name === "invalidate-done") store.asked = Date.UTC(2026, 10, 5, 8);
    if (name === "invalidate-done") store.came = store.asked! + 3000;
    const renderer = mount(state, store);
    try { assert.deepEqual(normalize(renderer.current().view), normalize(fixture(name)), name); } finally { renderer.dispose(); store.dispose(); }
  }
});
test("every original component still has an example", () => {
  const seen = new Set<string>();
  for (const page of catalog(translator())) {
    const renderer = mount({ p: page.id });
    for (const section of renderer.current().view.sections) for (const item of section.items) seen.add(item.type);
    renderer.dispose();
  }
  assert.equal(seen.size, componentCount);
});
test("list hooks paginate, reset on search/filter, and retain the other control", async () => {
  const renderer = mount({ p: "list" });
  await settle();
  try {
    for (const count of [40, 60]) {
      await click(renderer, "list-items", {}, "more.action");
      assert.equal(renderer.current().view.sections[0].items.length, count);
      assert.equal(!!renderer.current().view.sections[0].more, count < 60);
    }
    const current = renderer.current(), action = current.view.filter!.action;
    await renderer.event({ generation: current.generation, version: current.version, eventId: `test-${++event}`, key: current.rootKey, actionId: action.id, values: { kind: "doc", q: "示例" } });
    await settle();
    assert.equal(renderer.current().view.sections[0].items.length, 20);
    assert.equal(renderer.current().view.filter!.value, "doc");
    assert.equal(renderer.current().view.search!.value, "示例");
  } finally { renderer.dispose(); }
});
test("native pushes, save/pop, validation and refreshed detail preserve edits", async () => {
  const store = new ShowcaseStore(), list = mount({ p: "list", shown: 40, q: "示例" }, store);
  const push = (await click(list, "item-3")).push;
  assert.deepEqual(JSON.parse(push.state), { p: "detail", id: "3" });
  const detail = mount(JSON.parse(push.state), store), editPush = (await click(detail, "detail-edit")).push;
  const edit = mount(JSON.parse(editPush.state), store);
  try {
    assert.deepEqual(await click(edit, "edit-save", { title: " " }), { toast: "标题不能是空的" });
    assert.deepEqual(await click(edit, "edit-save", { title: "Updated demo", kind: "mail", note: "A synthetic note", done: "true" }), { toast: "保存了", pop: true });
    detail.render();
    assert.equal(detail.current().view.title, "Updated demo");
    assert.equal(find(detail, "detail-done").value, "完成了");
    assert.equal(list.current().view.sections[0].items.length, 40);
  } finally { for (const renderer of [list, detail, edit]) renderer.dispose(); store.dispose(); }
});
for (const version of [1, 2]) test(`UI v${version} keeps list context through detail/edit/save/back`, async () => {
  const initial = { p: "list", q: "示例", shown: 40 }, renderer = mount(initial, new ShowcaseStore(), version);
  try {
    assert.equal(renderer.current().view.search, undefined);
    await click(renderer, "item-3");
    await click(renderer, "detail-edit");
    await click(renderer, "edit-save", { title: "Edited demo", kind: "mail", note: "Demo", done: "true" });
    assert.equal(renderer.current().view.title, "Edited demo");
    assert.deepEqual(JSON.parse(renderer.current().view.state!), { ...initial, p: "detail", id: "3" });
    await click(renderer, "detail-back");
    assert.deepEqual(JSON.parse(renderer.current().view.state!), initial);
    assert.equal(find(renderer, "list-q").value, initial.q);
    assert.equal(find(renderer, "list-items").items.filter((item: any) => item.type === "row").length, 40);
    assert.ok(find(renderer, "list-more"));
    await click(renderer, "item-6"); await click(renderer, "detail-edit"); await click(renderer, "edit-back");
    assert.deepEqual(JSON.parse(renderer.current().view.state!), initial);
    await click(renderer, "home"); assert.equal(renderer.current().view.sections[1].items.length, 17);
  } finally { renderer.dispose(); }
});
test("segment callbacks change selected values and the chart", async () => {
  const renderer = mount({ p: "segmented" });
  try {
    await click(renderer, "range", { range: "1d" });
    assert.equal(find(renderer, "range-chart").series[0].points.length, 1);
    await click(renderer, "answer", { answer: "no" });
    assert.equal(find(renderer, "answer").value, "no");
  } finally { renderer.dispose(); }
});
test("service timers reject stale asks; mounted hook subscriptions clean up", async () => {
  let now = 0;
  const timers: (() => void)[] = [], canceled: unknown[] = [];
  const clock: Clock = { now: () => now, later: fn => { timers.push(fn); return timers.length; }, cancel: id => canceled.push(id) };
  const store = new ShowcaseStore(clock), renderer = mount({ p: "invalidate" }, store);
  await settle();
  await click(renderer, "ask"); assert.ok(find(renderer, "ask-loading"));
  now = 2000; await click(renderer, "ask"); assert.deepEqual(canceled, [1]);
  now = 3000; timers[0](); await settle(); assert.ok(find(renderer, "ask-loading"));
  now = 5000; timers[1](); await settle(); assert.equal(find(renderer, "ask-data").detail, "等了 3.0 秒");
  renderer.dispose(); store.ask(); store.dispose(); timers[2](); assert.equal(store.came, undefined);
});
test("independent UI session state, suspend/resume and no network adapter", async () => {
  const store = new ShowcaseStore(), first = mount({ p: "segmented" }, store), second = mount({ p: "segmented" }, store);
  try {
    await click(first, "range", { range: "1d" }); assert.equal(find(second, "range").value, "7d");
    first.suspend(); first.resume(); assert.equal(find(first, "range").value, "1d");
    await assert.rejects(first.fetch("https://example.com/", { signal: new AbortController().signal }), /sanctioned fetch/);
  } finally { first.dispose(); second.dispose(); store.dispose(); }
});
test("confirmation, submit and opaque event replay semantics are preserved", async () => {
  const renderer = mount({ p: "button" });
  try {
    const action = find(renderer, "button-button-item-0-1").action;
    assert.ok(action.confirm); assert.match(action.id, /^cb\./);
    const current = renderer.current(), input = { generation: current.generation, version: current.version, eventId: "same", key: "button-button-item-0-1", actionId: action.id, values: {} };
    assert.deepEqual(await renderer.event(input), { toast: "删掉了（其实没有）" });
    assert.deepEqual(await renderer.event(input), { toast: "删掉了（其实没有）" });
    assert.deepEqual(await click(renderer, "button-button-item-0-2"), { open_url: "https://example.com/" });
  } finally { renderer.dispose(); }
  const fields = mount({ p: "field" });
  try {
    assert.equal(find(fields, "field-field-item-1-0").action.submit, true);
    assert.equal(find(fields, "field-field-item-0-2").value, undefined);
    const result = await click(fields, "field-field-item-1-0", { text: "demo", secret: "synthetic", number: "42" });
    assert.equal(result.toast, "提交了：number=42，secret=••••，text=demo");
    assert.equal(summarize({ z: "1", a: "2" }), "a=2，z=1");
  } finally { fields.dispose(); }
});
test("locales are complete, deterministic, synthetic and negotiated", () => {
  for (const language of ["en", "zh-Hans", "ja"] as const) {
    assert.equal(Object.keys(locales[language]).length, 232);
    assert.deepEqual(Object.keys(locales[language]).sort(), Object.keys(locales.en).sort());
    for (const page of catalog(translator(language))) {
      const renderer = mount({ p: page.id }, new ShowcaseStore(), 3, language);
      assert.equal(typeof renderer.current().view.title, "string"); renderer.dispose();
    }
  }
  assert.equal(locale("ja-JP"), "ja"); assert.equal(locale("zh-CN"), "zh-Hans"); assert.equal(locale("fr"), "en");
  assert.equal(new ShowcaseStore().matching({ q: "Sample item" }, translator()).length, 60);
  assert.equal(new ShowcaseStore().matching({ q: "サンプル" }, translator("ja")).length, 60);
});
test("shortcut parsing preserves integer bounds, units, echo and errors", () => {
  assert.deepEqual(createShortcut("run", "showcase-echo", { text: "Demo" }, "en"), { text: "You said: Demo" });
  for (const n of ["0", "+3", " 1000 ", "-0"]) assert.ok(createShortcut("run", "showcase-count", { n }));
  for (const n of ["x", "", "1.1", "1e2", "0x10", "-1", "1001", "9007199254740993"]) assert.throws(() => runShortcut("run", "showcase-count", { n }), { code: "invalid" });
  assert.throws(() => runShortcut("missing", "showcase-echo", {}), { code: "unknown_method" });
  assert.throws(() => runShortcut("run", "missing", {}), { code: "no_contribution" });
  assert.equal(createShortcut("run", "showcase-count", { n: "3", unit: "apple", loud: "true" }, "zh-Hans").text, "数到了 3 个苹果！");
});
test("navigation normalization preserves decoded page and item targets", () => {
  const action = (state: string) => ({ id: "opaque", push: { state, title: "Demo" } });
  assert.deepEqual(normalize(action('{"p":"detail","id":"3"}')), normalize(action('{"id":"3","p":"detail"}')));
  assert.notDeepEqual(normalize(action('{"p":"detail","id":"3"}')), normalize(action('{"p":"detail","id":"4"}')));
  assert.notDeepEqual(normalize(action('{"p":"detail","id":"3"}')), normalize(action('{"p":"edit","id":"3"}')));
});
test("saving a removed item does not report success or pop the editor", async () => {
  const store = new ShowcaseStore(), renderer = mount({ p: "edit", id: "3" }, store);
  try {
    store.items.splice(store.items.findIndex(item => item.id === "3"), 1);
    assert.equal(store.save("3", { title: "Discarded change" }), false);
    assert.deepEqual(await click(renderer, "edit-save", { title: "Discarded change" }), { toast: "这一条不见了" });
  } finally { renderer.dispose(); store.dispose(); }
});
test("unrelated store changes do not stale native confirmation", async () => {
  let allow!: (value: boolean) => void;
  const gate = new Promise<boolean>(resolve => { allow = resolve; });
  const store = new ShowcaseStore({ now: () => 0, later: () => 1, cancel: () => {} });
  const renderer = new Renderer({ authorize: () => gate });
  renderer.mount(createPage(store, { state: { p: "button" }, language: "en" }));
  try {
    const frame = renderer.current(), action = find(renderer, "button-button-item-0-1").action;
    const result = renderer.event({ generation: frame.generation, version: frame.version, eventId: "confirmed", key: "button-button-item-0-1", actionId: action.id, values: {} });
    await settle();
    store.ask(); store.save("1", { title: "Updated sample" }); await settle();
    assert.equal(renderer.current().version, frame.version);
    allow(true);
    assert.deepEqual(await result, { toast: "Deleted (not really)" });
  } finally { allow(false); renderer.dispose(); store.dispose(); }
});
test("subscriptions only publish changes for the relevant page data", async () => {
  const store = new ShowcaseStore({ now: () => 0, later: () => 1, cancel: () => {} });
  const list = mount({ p: "list" }, store), data = mount({ p: "invalidate" }, store);
  try {
    const listVersion = list.current().version;
    store.ask(); await settle();
    assert.equal(list.current().version, listVersion);
    const dataVersion = data.current().version;
    store.save("1", { title: "Updated sample" }); await settle();
    assert.equal(data.current().version, dataVersion);
    assert.equal(find(list, "item-1").title, "Updated sample");
  } finally { list.dispose(); data.dispose(); store.dispose(); }
});
test("arrival time uses the requested time zone", () => {
  const store = new ShowcaseStore();
  store.asked = Date.UTC(2026, 10, 5, 8);
  store.came = store.asked + 3000;
  const renderer = mount({ p: "invalidate" }, store, 3, "en", "Asia/Shanghai");
  try { assert.equal(find(renderer, "ask-data").value, "16:00:03"); }
  finally { renderer.dispose(); store.dispose(); }
});
test("non-list pages never scan the list collection", () => {
  const store = new ShowcaseStore();
  store.matching = () => { throw new Error("unnecessary list scan"); };
  for (const state of [{}, { p: "button" }, { p: "detail", id: "3" }, { p: "edit", id: "3" }, { p: "invalidate" }]) {
    const renderer = mount(state, store);
    renderer.dispose();
  }
  store.dispose();
});
test("official connect advertises full localized source contributions and the owner-bound navigation bridge", async () => {
  const answers: any[] = [], conn = connect(createProgram(), line => answers.push(JSON.parse(line)));
  async function request(method: string, params: unknown) {
    const id = String(answers.length + 1); conn.line(JSON.stringify({ id, method, params })); await conn.drain(); return answers.at(-1);
  }
  try {
    assert.equal((await request("ping", {})).error.code, "not_initialized");
    const init = await request("initialize", { protocol: 1, extension: { id: "showcase", version: "0.1.3" }, hub: { locale: "ja", build: 1, extension_points: {} }, granted: {} });
    assert.equal(init.result.contributes.ui.length, 3);
    assert.equal(init.result.contributes.ui[0].placement, "nav_page");
    assert.ok(init.result.contributes.ui.slice(1).every((ui: any) => ui.opens === "showcase"));
    assert.equal(init.result.contributes.shortcuts[0].ui, "showcase");
    assert.deepEqual((await request("ui.page.supported", {})).result, { contributions: ["showcase"] });
    assert.equal((await request("shortcut.run", { contribution: "showcase-echo", values: { text: "Demo" } })).result.text, "入力：Demo");
    const card = await request("ui.render", { contribution: "showcase-card", ui_version: 3, size: "compact" });
    assert.equal(card.result.sections[0].items[0].stats[0].label, "コンポーネント");
    assert.equal((await request("ui.render", { contribution: "showcase", ui_version: 3 })).error.code, "unsupported");
    assert.equal((await request("shortcut.run", { contribution: "missing" })).error.code, "no_contribution");
  } finally { conn.close(); }
});
