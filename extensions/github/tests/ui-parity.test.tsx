import { test } from "node:test";
import assert from "node:assert/strict";
import { connect, type Program } from "@persona/extension";
import type { Snapshot } from "@persona/extension/ui";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { Fixture } from "./fixtures.ts";
import { b64encode, bytes } from "../src/model.ts";
import { contentPages, errorLines, logPages } from "../src/content-pages.ts";

async function native() {
  const bundle = await import(pathToFileURL(resolve(".source-dist/github/bundle/main.mjs")).href);
  const f = new Fixture(), frames: any[] = [];
  const p = connect(bundle.createProgram(f.fetch, { background: false, now: () => f.now }) as Program, line => {
    const frame = JSON.parse(line); frames.push(frame);
    if (frame.method?.startsWith("connector.secrets.") || frame.method === "connector.credentials.gh_token") void f.host.call(frame.method, frame.params).then(result => p.line(JSON.stringify({ id: frame.id, result })));
  });
  let serial = 0;
  const call = async (method: string, params: unknown = {}) => {
    const id = "parity-" + ++serial; p.line(JSON.stringify({ id, method, params })); await p.drain();
    const frame = frames.find(frame => frame.id === id); assert.ok(frame && !frame.error, JSON.stringify(frame?.error)); return frame.result;
  };
  await call("initialize", { protocol: 1, config: { auth: { from_machine: "synthetic-computer" } }, active: { connectors: ["github"], ui: ["github"] }, granted: { gh_login: true } });
  await call("connector.machines.online", { contribution: "github", machine: "synthetic-computer" });
  const mount = (session: string, state: unknown, ui_version = 7, locale = "en"): Promise<Snapshot> => call("ui.page.mount", { session, transport: "snapshot", params: { contribution: "github", state: JSON.stringify(state), ui_version, locale } });
  const click = async (session: string, snapshot: Snapshot, key: string, field = "action", values: Record<string, string> = {}) => {
    const item = items(snapshot).find(item => item.key === key) as any; assert.ok(item?.[field], "Missing native callback: " + key + "." + field);
    const result = await call("ui.page.event", { session, ticket: "synthetic-host-ticket", interaction: { generation: snapshot.generation, version: snapshot.version, key, actionId: item[field].id, eventId: crypto.randomUUID(), values } });
    assert.equal(result.status, "accepted"); return result;
  };
  return { f, p, call, mount, click };
}
const items = (snapshot: Snapshot) => snapshot.view.sections.flatMap(section => section.items);
const bounded = { timeout: 10000 };

test("native commit callbacks open the original public Go destination from PR and repository lists", bounded, async () => {
  const h = await native();
  try {
    for (const [session, state] of [["pr-commits", { r: "example/repo", n: 12, t: "commits" }], ["repo-commits", { v: "repo", r: "example/repo", t: "commits" }]] as const) {
      const snapshot = await h.mount(session, state), commit = items(snapshot).find(item => item.type === "row" && item.title === "Synthetic commit"); assert.ok(commit?.key);
      const result = await h.click(session, snapshot, commit.key);
      assert.equal(result.result.open_url, "https://github.com/example/repo/commit/head-one");
      assert.deepEqual(JSON.parse(result.snapshot.view.state), JSON.parse(snapshot.view.state!));
    }
    assert.equal(h.f.hits.filter(hit => hit.method !== "GET").length, 0);
  } finally { h.p.close(); }
});

test("native check navigation selects the original last-error location instead of the cleanup tail", bounded, async () => {
  const h = await native();
  try {
    const lines = Array.from({ length: 700 }, (_, index) => "2026-01-01T00:00:00Z " + (index === 300 ? "##[error]Synthetic failure" : "Synthetic log line " + index));
    h.f.overrides = url => url.pathname.endsWith("/check-runs") ? Response.json({ total_count: 1, check_runs: [{ id: 71, name: "Synthetic CI", status: "completed", conclusion: "failure", app: { slug: "github-actions" } }] }) : url.pathname.endsWith("/check-runs/71") ? Response.json({ id: 71, name: "Synthetic CI", status: "completed", conclusion: "failure", app: { slug: "github-actions" } }) : url.pathname.endsWith("/logs") ? new Response(lines.join("\n")) : undefined;
    const list = await h.mount("checks", { r: "example/repo", n: 12, t: "checks" });
    const row = items(list).find(item => item.type === "row" && item.title === "Synthetic CI"); assert.ok(row?.key);
    const result = await h.click("checks", list, row.key), shown = items(result.snapshot).find(item => item.key === "log") as any;
    assert.equal(shown.text, lines.slice(151, 301).map(line => line.replace(/^2026-01-01T00:00:00Z /, "")).join("\n"));
    const errors = items(result.snapshot).find(item => item.key === "log-errors") as any; assert.equal(errors.text, "Synthetic failure");
    const reads = h.f.hits.length;
    let snapshot = (await h.click("checks", result.snapshot, "log-next")).snapshot;
    assert.ok((items(snapshot).find(item => item.key === "log") as any).text.includes("Synthetic log line 301"));
    snapshot = (await h.click("checks", snapshot, "log-location")).snapshot;
    assert.equal((items(snapshot).find(item => item.key === "log") as any).text, shown.text);
    while (items(snapshot).some(item => item.key === "log-previous")) snapshot = (await h.click("checks", snapshot, "log-previous")).snapshot;
    const windows: string[] = [];
    while (true) {
      assert.ok(bytes(JSON.stringify(snapshot)) <= 1 << 20); windows.push((items(snapshot).find(item => item.key === "log") as any).text);
      if (!items(snapshot).some(item => item.key === "log-next")) break;
      assert.ok(windows.length < 20); snapshot = (await h.click("checks", snapshot, "log-next")).snapshot;
    }
    assert.equal(windows.join("\n"), lines.map(line => line.replace(/^2026-01-01T00:00:00Z /, "")).join("\n"));
    assert.equal(h.f.hits.length, reads, "Location selection must not reread the log");
  } finally { h.p.close(); }
});

test("bundled native Code.load keeps every large-source byte accessible within unchanged budgets", bounded, async () => {
  const h = await native();
  try {
    const text = Array.from({ length: 4500 }, (_, index) => `Synthetic source ${index}: ${"界".repeat(8)}`).join("\n") + "\n";
    h.f.overrides = url => url.pathname.includes("/contents/") ? Response.json({ name: "file.ts", path: "file.ts", type: "file", sha: "synthetic-blob", size: bytes(text), encoding: "base64", content: b64encode(text) }) : undefined;
    let snapshot = await h.mount("source", { v: "file", r: "example/repo", p: "file.ts" }), collected = "", pages = 0;
    while (true) {
      assert.ok(bytes(JSON.stringify(snapshot)) <= 1 << 20);
      const code = items(snapshot).find(item => item.type === "code") as any; assert.ok(code); assert.ok(bytes(code.text) <= 100000);
      assert.equal(code.start_line, collected.split("\n").length); collected += code.text; pages++;
      if (!code.load) break;
      assert.equal(code.truncated, true); assert.ok(pages < 20, "Source pagination did not terminate");
      snapshot = (await h.click("source", snapshot, code.key, "load", { path: "forged.ts", from: "999999" })).snapshot;
    }
    assert.ok(pages > 1); assert.equal(collected, text);
    assert.equal(h.f.hits.filter(hit => hit.url.includes("/contents/")).length, 1, "Page-only navigation must retain the loaded revision");
  } finally { h.p.close(); }
});

test("older native clients can load the remaining source without an enlarged frame", bounded, async () => {
  const h = await native();
  try {
    const text = Array.from({ length: 900 }, (_, index) => "Synthetic legacy line " + index).join("\n");
    h.f.overrides = url => url.pathname.includes("/contents/") ? Response.json({ type: "file", sha: "synthetic-blob", size: bytes(text), encoding: "base64", content: b64encode(text) }) : undefined;
    for (const version of [1, 3]) {
      let snapshot = await h.mount("legacy-" + version, { v: "file", r: "example/repo", p: "file.ts" }, version), collected = "", pages = 0;
      while (true) {
        const code = items(snapshot).find(item => item.key === "file") as any; assert.ok(code); collected += code.text; pages++;
        assert.ok(bytes(JSON.stringify(snapshot)) <= 1 << 20);
        if (!items(snapshot).some(item => item.key === "source-next")) break;
        assert.ok(pages < 10); snapshot = (await h.click("legacy-" + version, snapshot, "source-next")).snapshot;
      }
      assert.ok(pages > 1); assert.equal(collected, text);
    }
  } finally { h.p.close(); }
});

test("source windows preserve Unicode, long lines, empty lines and trailing newlines", () => {
  for (const text of ["", "\n", "a\n\n", "a\r\nb\r\n", "界\u{1d11e}é".repeat(20000), "short\n" + "界".repeat(70000) + "\nlast\n", "\u0001\\\"".repeat(50000)]) {
    const pages = contentPages(text, 400); assert.equal(pages.map(page => page.text).join(""), text);
    let before = "";
    for (const page of pages) { assert.ok(bytes(page.text) <= 100000); assert.equal(page.startLine, before.split("\n").length); assert.ok(!page.text.includes("�")); before += page.text; }
  }
});

test("actual source callbacks expose oversized Unicode and JSON-escaped lines without clipping", bounded, async () => {
  const h = await native();
  try {
    for (const [index, text] of ["界\u{1d11e}é".repeat(20000), "\u0001\\\"".repeat(50000), "short\n" + "界".repeat(70000) + "\nlast\n"].entries()) {
      h.f.overrides = url => url.pathname.includes("/contents/") ? Response.json({ type: "file", sha: "synthetic-blob-" + index, size: bytes(text), encoding: "base64", content: b64encode(text) }) : undefined;
      const session = "boundary-" + index;
      let snapshot = await h.mount(session, { v: "file", r: "example/repo", p: "boundary-" + index + ".ts" }), collected = "", count = 0;
      while (true) {
        const code = items(snapshot).find(item => item.type === "code") as any; assert.ok(code); collected += code.text;
        assert.ok(bytes(JSON.stringify(snapshot)) <= 1 << 20); assert.ok(bytes(code.text) <= 100000);
        if (!code.load) break;
        assert.ok(++count < 20); snapshot = (await h.click(session, snapshot, code.key, "load")).snapshot;
      }
      assert.equal(collected, text); assert.ok(count > 0);
      const last = snapshot;
      snapshot = (await h.click(session, snapshot, "source-previous")).snapshot;
      assert.notEqual(snapshot.version, last.version); assert.ok(items(snapshot).some(item => item.key === "source-next"));
    }
  } finally { h.p.close(); }
});

test("log location and error extraction match original fallback and ordered deduplication", () => {
  const log = Array.from({ length: 400 }, (_, index) => "line " + index).join("\n") + "\n";
  const fallback = logPages(log); assert.equal(fallback.pages[fallback.focus]!.text, log.trimEnd().split("\n").slice(-150).join("\n"));
  assert.equal(errorLines("##[error]same\n##[error]same\nfatal: failed\n--- FAIL: test\nordinary\nfile.ts: error detail"), "same\nfatal: failed\n--- FAIL: test\nfile.ts: error detail");
  assert.equal(errorLines(Array.from({ length: 30 }, (_, index) => "##[error]error " + index).join("\n")).split("\n").length, 20);
});

test("bundled diff-to-source callbacks pin the displayed head across load-more and refresh", bounded, async () => {
  const h = await native();
  try {
    const text = Array.from({ length: 4500 }, (_, index) => "Synthetic pinned source " + index).join("\n");
    h.f.overrides = url => url.pathname.includes("/contents/") ? Response.json({ type: "file", sha: "synthetic-pinned-blob", size: bytes(text), encoding: "base64", content: b64encode(text) }) : undefined;
    let snapshot = await h.mount("pinned", { r: "example/repo", n: 12, t: "files" });
    const source = items(snapshot).find(item => item.type === "button" && item.title === "Source"); assert.ok(source?.key);
    h.f.head = "head-two";
    snapshot = (await h.click("pinned", snapshot, source.key)).snapshot;
    assert.equal(JSON.parse(snapshot.view.state!).ref, "head-one");
    const code = items(snapshot).find(item => item.type === "code") as any; assert.ok(code.line_url.includes("/head-one/"));
    snapshot = (await h.click("pinned", snapshot, code.key, "load")).snapshot;
    snapshot = (await h.click("pinned", snapshot, "refresh")).snapshot;
    assert.equal(JSON.parse(snapshot.view.state!).ref, "head-one");
    const reads = h.f.hits.filter(hit => hit.url.includes("/contents/")); assert.equal(reads.length, 2);
    assert.ok(reads.every(hit => new URL(hit.url).searchParams.get("ref") === "head-one"));
  } finally { h.p.close(); }
});

test("large Markdown retains preview/raw navigation and all source windows in three locales", bounded, async () => {
  const h = await native();
  try {
    const text = Array.from({ length: 4200 }, (_, index) => "Synthetic Markdown line " + index).join("\n");
    h.f.overrides = url => url.pathname.includes("/contents/") ? Response.json({ type: "file", sha: "synthetic-markdown", size: bytes(text), encoding: "base64", content: b64encode(text) }) : undefined;
    for (const locale of ["en", "zh-Hans", "ja"]) {
      const session = "markdown-" + locale;
      let snapshot = await h.mount(session, { v: "file", r: "example/repo", p: "README.md" }, 7, locale), collected = "";
      while (true) {
        collected += (items(snapshot).find(item => item.key === "file-markdown") as any).text;
        const next = items(snapshot).find(item => item.key === "source-next") as any;
        if (!next) break;
        assert.ok(!next.title.includes("\n")); snapshot = (await h.click(session, snapshot, "source-next")).snapshot;
      }
      assert.equal(collected, text);
      snapshot = (await h.click(session, snapshot, "raw")).snapshot;
      assert.ok(items(snapshot).some(item => item.type === "code"));
    }
  } finally { h.p.close(); }
});
