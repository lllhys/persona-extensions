import { test } from "node:test";
import assert from "node:assert/strict";
import { Renderer } from "@persona/extension/ui";
import { Fixture, issue, pull, comment, notification } from "./fixtures.ts";
import { preview, perform, root } from "../src/root.tsx";
import { loadPage, transition } from "../src/page-model.ts";
import { snapshotPage } from "./legacy-page.ts";
import { glob } from "../src/model.ts";
const bounded = { timeout: 5000 };
const coded = (code: string) => (e: unknown) => (e as { code?: string }).code === code;
const posts = (f: Fixture) => f.hits.filter(h => h.method === "POST");
const until = async (condition: () => boolean) => { const end = Date.now() + 2000; while (!condition()) { assert.ok(Date.now() < end, "Fixture deadline exceeded"); await new Promise(resolve => setTimeout(resolve, 0)); } };

test("round3-1: snapshot redraw cannot renew A approval under B; fresh B preview is required", bounded, async () => {
  const f = new Fixture(), h = await f.setup(), snapshot = snapshotPage(h.service, h.connector.receipts);
  const old = await preview(h.service, { v: "write", w: "comment", r: "example/repo", n: 12 }, { body: "Synthetic A preview" });
  const req = { contribution: "github-snapshot", placement: "extension_page", ui_version: 3, state: JSON.stringify(old) } as any;
  await h.auth.set({ contribution: "github", source: "machine", machine: "computer-b" }); f.hits = [];
  await assert.rejects(async () => snapshot.action!({ ...req, action: "send" }), coded("auth_changed"));
  await assert.rejects(async () => snapshot.render(req), coded("auth_changed")); assert.equal(posts(f).length, 0);
  const fresh = await preview(h.service, { v: "write", w: "comment", r: "example/repo", n: 12 }, { body: "Synthetic B preview" });
  const view = await snapshot.render({ ...req, state: JSON.stringify(fresh) });
  await snapshot.action!({ ...req, state: view.state, action: "send", values: {} }); assert.equal(posts(f).length, 1);
  assert.equal(JSON.parse(posts(f)[0]!.body).body, "Synthetic B preview");
  const unbound = { ...fresh, authEpoch: undefined };
  await assert.rejects(async () => snapshot.render({ ...req, state: JSON.stringify(unbound) }), coded("auth_changed"));
});

test("round3-2: empty 200 JSON bodies are rejected and never admitted to ETag cache", bounded, async () => {
  for (const empty of ["", " ", "\n\t"]) {
    const f = new Fixture(), h = await f.setup(); let attempts = 0;
    f.overrides = url => url.pathname === "/synthetic-empty" ? (++attempts === 1 ? new Response(empty, { status: 200, headers: { etag: "synthetic-empty" } }) : Response.json({ valid: true }, { headers: { etag: "valid" } })) : undefined;
    await assert.rejects(() => h.client.json("/synthetic-empty"), SyntaxError);
    assert.deepEqual(await h.client.json("/synthetic-empty"), { valid: true }); assert.equal(attempts, 2);
    assert.equal(f.hits.at(-1)!.headers.has("if-none-match"), false);
    assert.deepEqual(await h.client.json("/synthetic-empty"), { valid: true }); assert.equal(attempts, 2);
  }
  const f = new Fixture(), h = await f.setup(); f.overrides = url => url.pathname.endsWith("/comments") ? new Response(null, { status: 204 }) : undefined;
  await h.service.write("example/repo", 12, "comment", { body: "Synthetic body" });
});

test("round3-3: nullable issue, comment and review authors retain tools, inbox, archive and UI", bounded, async () => {
  for (const where of ["issue", "comment", "review", "all"]) {
    const f = new Fixture(), h = await f.setup();
    f.notifications = [{ ...notification, reason: "mention" }];
    f.overrides = url => (where === "issue" || where === "all") && url.pathname.endsWith("/issues/12") ? Response.json({ ...issue, user: null }) : (where === "comment" || where === "all") && url.pathname.endsWith("/comments") ? Response.json([{ ...comment, user: null }]) : (where === "review" || where === "all") && url.pathname.endsWith("/reviews") ? Response.json([{ ...comment, id: 32, user: null, state: "APPROVED", submitted_at: "2026-01-03T00:00:00Z" }]) : undefined;
    assert.ok((await h.service.readTool("github_read", { repo: "example/repo", number: 12 })).includes(issue.title));
    await h.inbox.poll(); assert.equal(h.inbox.collect().length, 1); assert.equal(h.inbox.collect()[0]!.done, undefined);
    const result = await h.archive.changes(); assert.ok(result.batches?.length); assert.equal(result.more, false);
    const timeline = await h.connector.thread({ contribution: "github", thread: "example/repo#12" }); assert.ok(timeline.messages.length);
    await snapshotPage(h.service, h.connector.receipts).render({ contribution: "github-snapshot", placement: "extension_page", state: JSON.stringify({ r: "example/repo", n: 12 }), ui_version: 7 } as any);
  }
});

test("round3-4: Discussion and Commit subjects keep non-CI identity and settle only when read or muted", bounded, async () => {
  for (const [type, url] of [["Discussion", ""], ["Discussion", "https://api.github.com/repos/example/repo/discussions/1"], ["Commit", "https://api.github.com/repos/example/repo/commits/abc123"]]) {
    const f = new Fixture(), h = await f.setup(); f.notifications = [{ ...notification, reason: "mention", subject: { type: type!, url: url!, title: "Synthetic subject" } }];
    await h.inbox.poll(); const thing = h.inbox.collect()[0]!; assert.equal(thing.thread, "subject:example/repo@n1");
    assert.deepEqual(h.inbox.settled([thing.thread]), {});
    h.inbox.setOptions({ muted_repos: ["example/repo"] }); assert.ok(h.inbox.settled([thing.thread])[thing.thread]);
    assert.ok(h.inbox.settled(["notification:legacy"])["notification:legacy"]);
  }
});

test("round3-5: every cursor timestamp is strict RFC3339 and validated before any request", bounded, async () => {
  const invalid = ["2026-02-30T00:00:00Z", "2025-02-29T00:00:00Z", "2026-04-31T00:00:00Z", "2026-13-01T00:00:00Z", "2026-01-00T00:00:00Z", "2026-01-01", "2026-01-01T24:00:00Z", "2026-01-01T00:60:00Z", "2026-01-01T00:00:60Z", "2026-01-01T00:00:00+24:00", "2026-01-01T00:00:00+00:60", "2026-01-01 00:00:00Z"];
  const f = new Fixture(), h = await f.setup(); f.hits = [];
  for (const at of invalid) for (const cursor of [{ since: at, threads: {} }, { since: "2025-12-01T00:00:00Z", threads: { "example/repo#12": at } }, { since: "2025-12-01T00:00:00Z", threads: {}, positions: { "example/repo#12": { at, ids: [] } } }]) {
    await assert.rejects(() => h.archive.changes(JSON.stringify(cursor)), coded("invalid_params")); assert.equal(f.hits.length, 0);
  }
  for (const threads of [[], "invalid", { "example/repo#12": 1 }]) await assert.rejects(() => h.archive.changes(JSON.stringify({ since: "2026-01-01T00:00:00Z", threads })), coded("invalid_params"));
  for (const at of ["2024-02-29T00:00:00Z", "2026-01-01T00:00:00.123456789Z", "2026-01-01T00:00:00+09:00"]) await h.archive.changes(JSON.stringify({ since: at, threads: {} }));
});

test("round3-6: displayed A diff remains pinned through edit, edit-preview and send for line and draft", bounded, async () => {
  for (const kind of ["line", "draft"]) {
    const f = new Fixture(), h = await f.setup(), snapshot = snapshotPage(h.service, h.connector.receipts);
    const req = { contribution: "github-snapshot", placement: "extension_page", ui_version: 7 } as any;
    const view = await snapshot.render({ ...req, state: JSON.stringify({ r: "example/repo", n: 12, t: "files" }) });
    const displayed = JSON.parse(view.state!); assert.equal(displayed.head, "head-one");
    const edited = await snapshot.action!({ ...req, state: view.state, action: "edit:" + kind, values: { path: "file.ts", line: "1", side: "new" } }) as any;
    const edit = JSON.parse(edited.view.state); assert.equal(edit.head, "head-one");
    f.head = "head-two"; f.hits = [];
    await assert.rejects(() => preview(h.service, edit, { body: "Synthetic comment on A" }), coded("head_changed")); assert.equal(posts(f).length, 0);
    f.head = "head-one"; const approved = await preview(h.service, edit, { body: "Synthetic pinned comment" });
    const revised = transition(approved, "edit_preview"); assert.equal(revised.head, "head-one");
    f.head = "head-two"; await assert.rejects(() => preview(h.service, revised, {}), coded("head_changed"));
    f.head = "head-one"; await perform(h.service, h.connector.receipts, approved, "send", {});
    if (kind === "line") assert.equal(JSON.parse(posts(f)[0]!.body).commit_id, "head-one");
    await assert.rejects(() => preview(h.service, { ...edit, head: undefined }, { body: "Synthetic unbound comment" }), coded("invalid_params"));
  }
});

test("round3-6: component edit callback binds the loaded diff rather than a submitted head", bounded, async () => {
  for (const kind of ["line", "draft"]) {
    const f = new Fixture(), h = await f.setup(), renderer = new Renderer({ authorize: () => true });
    try {
      renderer.mount(root({ service: h.service, receipts: h.connector.receipts, state: { r: "example/repo", n: 12, t: "files" } }));
      await until(() => renderer.current().view.sections.some(s => s.items.some(i => i.type === "diff")));
      const diff = renderer.current().view.sections.flatMap(s => s.items).find(i => i.type === "diff") as any;
      const handle = kind === "line" ? diff.comment : diff.draft;
      const frame = renderer.current();
      await renderer.event({ generation: frame.generation, version: frame.version, key: diff.key, actionId: handle.id, eventId: crypto.randomUUID(), values: { path: "file.ts", line: "1", side: "new", head: "synthetic-forged-head" } });
      await until(() => JSON.parse(renderer.current().view.state!).v === "write");
      assert.equal(JSON.parse(renderer.current().view.state!).head, "head-one");
      f.head = "head-two"; f.hits = [];
      const frame2 = renderer.current(), button = frame2.view.sections.flatMap(s => s.items).find(i => i.key === "preview") as any;
      await renderer.event({ generation: frame2.generation, version: frame2.version, key: button.key, actionId: button.action.id, eventId: crypto.randomUUID(), values: { body: "Synthetic comment about displayed A" } });
      assert.equal(JSON.parse(renderer.current().view.state!).pv, false); assert.equal(posts(f).length, 0);
    } finally { renderer.dispose(); }
  }
});

test("round3-6: head changes during diff loading do not present a mismatched selectable diff", bounded, async () => {
  const f = new Fixture(), h = await f.setup(); let reads = 0;
  f.overrides = url => url.pathname.endsWith("/pulls/12") ? Response.json({ ...pull, head: { ...pull.head, sha: ++reads === 1 ? "head-one" : "head-two" } }) : undefined;
  const loaded = await loadPage(h.service, { r: "example/repo", n: 12, t: "files" });
  assert.equal(loaded.kind, "error"); if (loaded.kind === "error") assert.ok(coded("head_changed")(loaded.error)); assert.equal(posts(f).length, 0);
});

test("round3-7: changing tabs clears query and tab-local filter before list requests", bounded, async () => {
  for (const fvalue of ["notify", "activity", "example"]) {
    const f = new Fixture(), h = await f.setup(), state = transition({ t: "involved", f: fvalue, q: "synthetic-query" }, "tab", { t: "mine" }); f.hits = [];
    assert.equal(state.q, undefined); assert.equal(state.f, undefined); await h.service.list(state);
    const url = new URL(f.hits[0]!.url); assert.equal(url.pathname, "/search/issues"); assert.equal(url.searchParams.get("q"), "is:pr is:open author:@me");
  }
});

test("round3-8: Go path glob is case-sensitive, slash-bounded and supports escaped classes", bounded, async () => {
  for (const [pattern, value, expected] of [["train/*", "TRAIN/feature", false], ["train/*", "train/feature", true], ["train/*", "train/a/b", false], ["train/?", "train/é", true], ["train/[a-c]", "train/b", true], ["train/[^a-c]", "train/d", true], ["train/\\*", "train/*", true], ["[", "x", false]] as const) assert.equal(glob(pattern, value), expected, pattern + " " + value);
  const f = new Fixture(), h = await f.setup(); f.notifications = [{ ...notification, reason: "ci_activity", subject: { type: "CheckSuite", url: "", title: "Build workflow run failed for TRAIN/feature branch" } }];
  await h.inbox.poll(); assert.equal(h.inbox.collect().length, 1); assert.deepEqual(h.inbox.settled(["ci:example/repo@TRAIN/feature"]), {});
  h.inbox.setOptions({ ignore_branches: ["TRAIN/[a-z]*"] }); assert.ok(h.inbox.settled(["ci:example/repo@TRAIN/feature"])["ci:example/repo@TRAIN/feature"]);
});

test("round3 adjacent inherited 401 guard: same-principal refresh retries; different principal never replays write", bounded, async () => {
  for (const account of ["reader", "synthetic-other-reader"]) {
    const f = new Fixture(), h = await f.setup({ auth: { from_machine: "computer-a" } }), call = f.host.call;
    f.host.call = async <T,>(method: string, params?: unknown): Promise<T> => method === "connector.credentials.gh_token" ? { token: "synthetic-refreshed-token" } as T : call<T>(method, params);
    let attempts = 0; f.overrides = (url, init) => url.pathname === "/user" ? Response.json({ login: account }) : url.pathname.endsWith("/issues/12/comments") && init.method === "POST" && ++attempts === 1 ? new Response(null, { status: 401 }) : undefined;
    const request = () => h.connector.handle("actions.do", { name: "github_comment", request_id: "synthetic-refresh", args: { repo: "example/repo", number: 12, body: "Synthetic approved body" } });
    if (account === "reader") { await request(); assert.equal(attempts, 2); }
    else { await assert.rejects(request, coded("auth_changed")); assert.equal(attempts, 1); }
  }
});

test("round3 adjacent guard: transport errors with unknown write effect are never replayed", bounded, async () => {
  const f = new Fixture(), h = await f.setup(); let attempts = 0;
  f.overrides = (url, init) => { if (url.pathname.endsWith("/issues/12/comments") && init.method === "POST") { attempts++; throw new Error("Synthetic unknown effect"); } return undefined; };
  await assert.rejects(() => h.service.write("example/repo", 12, "comment", { body: "Synthetic approved body" }), coded("transport_error")); assert.equal(attempts, 1);
});
