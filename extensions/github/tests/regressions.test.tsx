import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { ExtError, type UIRequest } from "@persona/extension";
import { Renderer } from "@persona/extension/ui";
import { Fixture, issue, pull, comment, notification } from "./fixtures.ts";
import { ThreadReader } from "../src/thread.ts";
import { snapshotPage } from "./legacy-page.ts";
import { WriteReceipts } from "../src/connector.ts";
import { transition, loadPage, patchPages } from "../src/page-model.ts";
import { page } from "../src/page.tsx";
import { bytes, type Comment, type File } from "../src/model.ts";
import { translations } from "../src/localization.ts";
const request = (values: Partial<UIRequest>): UIRequest => ({ contribution: "github-snapshot", placement: "extension_page", size: "full", ui_version: 7, state: "{}", hub: new Fixture().host as UIRequest["hub"], ...values });
const coded = (code: string) => (error: unknown) => error instanceof ExtError && error.code === code;
const deferred = <T,>() => { let resolve!: (value: T) => void; const promise = new Promise<T>(yes => resolve = yes); return { resolve, promise }; };
const tick = () => new Promise(resolve => setTimeout(resolve, 0));

test("R1: every archive and authored lock URL is public npm or the exact local SDK link", async () => {
  for (const path of ["package-lock.json", "node_modules/.package-lock.json", ".source-dist/github/source/extensions/github/package-lock.json"]) {
    const lock = JSON.parse(await readFile(path, "utf8")); let remote = 0;
    for (const entry of Object.values(lock.packages) as any[]) {
      if (!entry.resolved) continue;
      if (entry.link) { assert.equal(entry.resolved, "../../sdk/ts"); continue; }
      const url = new URL(entry.resolved); assert.equal(url.protocol, "https:"); assert.equal(url.hostname, "registry.npmjs.org");
      assert.equal(url.username, ""); assert.equal(url.password, ""); assert.equal(url.search, ""); assert.ok(entry.integrity); remote++;
    }
    assert.ok(remote > 0);
  }
});

test("R2: a delayed computer A credential cannot refill computer B or authorize its write", async () => {
  const f = new Fixture(), h = await f.setup({ auth: { from_machine: "computer-a" } }), pending = deferred<{ token: string }>();
  const oldCall = f.host.call; let entered = false, bCalls = 0;
  f.host.call = async <T,>(method: string, params?: unknown): Promise<T> => {
    if (method === "connector.credentials.gh_token") {
      if ((params as any).machine === "computer-a") { entered = true; return pending.promise as Promise<T>; }
      bCalls++; return { token: "synthetic-b-credential" } as T;
    }
    return oldCall<T>(method, params);
  };
  const old = h.auth.refresh(); const rejection = assert.rejects(old, coded("auth_changed"));
  while (!entered) await tick();
  await h.auth.set({ contribution: "github", source: "machine", machine: "computer-b" });
  pending.resolve({ token: "synthetic-a-credential" }); await rejection;
  await h.service.write("example/repo", 12, "comment", { body: "Synthetic B comment" });
  assert.equal(h.auth.machine, "computer-b"); assert.equal(bCalls, 1);
  assert.equal(f.hits.at(-1)!.headers.get("authorization"), "Bearer synthetic-b-credential");
});

test("R2: switching sources before dispatch also invalidates an already cached token", async () => {
  const f = new Fixture(), h = await f.setup();
  const oldWrite = h.service.write("example/repo", 12, "comment", { body: "Synthetic old-account comment" });
  const rejected = assert.rejects(oldWrite, coded("auth_changed"));
  await h.auth.set({ contribution: "github", source: "machine", machine: "computer-b" }); await rejected;
  assert.equal(f.hits.some(hit => hit.method === "POST" && hit.url.endsWith("/comments")), false);
});

test("R2: an old API response cannot restore invalidated account caches or status", async () => {
  const f = new Fixture(), h = await f.setup(), pending = deferred<Response>();
  f.overrides = url => url.pathname === "/synthetic-account" ? pending.promise : undefined;
  const old = h.client.json("/synthetic-account"), rejection = assert.rejects(old, coded("auth_changed"));
  await tick(); await h.auth.set({ contribution: "github", source: "machine", machine: "computer-b" });
  pending.resolve(Response.json({ account: "synthetic-old-account" }, { headers: { etag: "old" } })); await rejection;
  f.overrides = url => url.pathname === "/synthetic-account" ? Response.json({ account: "synthetic-new-account" }) : undefined;
  assert.deepEqual(await h.client.json("/synthetic-account"), { account: "synthetic-new-account" });
});

test("R3: nullable descriptions and review bodies remain readable in tools, threads, inbox and archive", async () => {
  const f = new Fixture(), h = await f.setup();
  f.overrides = url => url.pathname.endsWith("/issues/12") ? Response.json({ ...issue, body: null }) : url.pathname.endsWith("/reviews") ? Response.json([{ ...comment, id: 92, state: "COMMENTED", body: null }]) : undefined;
  assert.ok((await h.service.readTool("github_read", { repo: "example/repo", number: 12 })).includes("(no description)"));
  const thread = await new ThreadReader(h.service, h.auth).page({ contribution: "github", thread: "example/repo#12" });
  assert.equal(thread.chat.body, ""); assert.ok(thread.messages.length);
  await h.inbox.poll(); assert.equal(h.inbox.collect().length, 1);
  const messages = await h.archive.messages({ ...issue, body: null }, "example/repo");
  assert.equal(JSON.stringify(messages).includes("null"), false);
});

test("R4: comment 1001 and same-timestamp late comments are archived after a restart using since", async () => {
  const f = new Fixture(), h = await f.setup(); let comments: Comment[] = Array.from({ length: 1000 }, (_, i) => ({ ...comment, id: i + 1, created_at: "2026-01-02T00:00:00Z" }));
  f.overrides = url => {
    if (!url.pathname.endsWith("/comments")) return undefined;
    if (url.pathname.includes("/pulls/")) return Response.json([]);
    const since = url.searchParams.get("since"), filtered = comments.filter(c => !since || Date.parse(c.created_at) >= Date.parse(since));
    const page = Number(url.searchParams.get("page") || 1); return Response.json(filtered.slice((page - 1) * 50, page * 50));
  };
  const first = await h.archive.changes(); assert.equal(first.batches![0]!.messages!.filter(m => m.id !== "body").length, 1000);
  comments = [...comments, { ...comment, id: 1001, created_at: "2026-01-04T00:00:00Z" }];
  const restarted = await f.setup(), second = await restarted.archive.changes(first.next);
  assert.deepEqual(second.batches![0]!.messages!.map(m => m.id), ["c1001"]);
  assert.ok(f.hits.some(hit => new URL(hit.url).searchParams.has("since")));
  comments.push({ ...comment, id: 1002, created_at: "2026-01-04T00:00:00Z" });
  const third = await restarted.archive.changes(second.next);
  assert.deepEqual(third.batches![0]!.messages!.map(m => m.id), ["c1002"]);
});

test("R5: persisted legacy notification, CI branch and muted threads settle with empty memory", async () => {
  const f = new Fixture(), h = await f.setup(); h.inbox.setOptions({ ci: "off", muted_repos: ["example/repo"], ignore_branches: ["train/*"] });
  const threads = ["notification:old", "ci:other/repo@main", "ci:other/repo@train/one", "example/repo#12", "subject:example/repo@discussion"];
  assert.equal(h.inbox.collect().length, 0); assert.equal(Object.keys(h.inbox.settled(threads)).length, threads.length);
  assert.deepEqual(h.inbox.settled(["other/repo#12"]), {});
});

test("R6: Documentation success cannot settle Build failure, via notification or follow-up", async () => {
  const f = new Fixture(), h = await f.setup(); f.notifications = [{ ...notification, reason: "ci_activity", subject: { title: "Build workflow run failed for main branch", type: "CheckSuite", url: "" } }];
  await h.inbox.poll(); f.now += 600001;
  f.notifications[0] = { ...f.notifications[0]!, updated_at: "2026-01-04T00:00:00Z", subject: { title: "Documentation workflow run succeeded for main branch", type: "CheckSuite", url: "" } };
  let buildSuccess = false;
  f.overrides = url => url.pathname.endsWith("/actions/runs") ? Response.json({ workflow_runs: [{ name: "Documentation", status: "completed", conclusion: "success" }, { name: "Build", status: "completed", conclusion: buildSuccess ? "success" : "failure" }] }) : undefined;
  await h.inbox.poll(); assert.equal(h.inbox.collect()[0]!.done, undefined);
  buildSuccess = true; f.now += 600001; await h.inbox.poll(); assert.equal(h.inbox.collect()[0]!.done, "read");
});

test("R7: unsupported optional 9 MiB logs report a blocker without swallowing the PR thread", async () => {
  const f = new Fixture(), h = await f.setup();
  f.overrides = url => url.pathname.endsWith("/check-runs") ? Response.json({ total_count: 1, check_runs: [{ id: 71, name: "Build", status: "completed", conclusion: "failure", app: { slug: "github-actions" } }] }) : url.pathname.endsWith("/logs") ? new Response(null, { headers: { "content-length": String(9 << 20) } }) : undefined;
  const thread = await h.connector.thread({ contribution: "github", thread: "example/repo#12", limit: 100 });
  assert.equal(thread.chat.body, issue.body); assert.ok(thread.messages.some(m => m.id === "c31"));
  const check = thread.messages.find(m => m.id === "ck71")!; assert.equal(check.check!.log, undefined); assert.equal(check.text, translations("en").logBlocked);
  await assert.rejects(() => h.client.log("example/repo", 71), coded("bounded_log_unsupported"));
});

test("R8: the original 50 x 24000 diff counterexample fits the entire old-client frame", async () => {
  const f = new Fixture(), h = await f.setup();
  const patch = "@@ -1 +1 @@\n" + "+synthetic line\n".repeat(1800);
  f.overrides = url => url.pathname.endsWith("/files") ? Response.json(Array.from({ length: 50 }, (_, i) => ({ filename: `file-${i}.ts`, sha: `blob-${i}`, status: "added", additions: 1800, deletions: 0, patch }))) : undefined;
  for (const ui_version of [1, 3, 4, 7]) {
    const view = await snapshotPage(h.service, new WriteReceipts()).render(request({ state: JSON.stringify({ r: "example/repo", n: 12, t: "files" }), ui_version }));
    assert.ok(bytes(JSON.stringify(view)) < (1 << 20));
  }
});

test("R8: every diff line remains reachable across pages with correct side line coordinates", () => {
  const lines = Array.from({ length: 6000 }, (_, i) => `+synthetic-${i}`), pages = patchPages("@@ -1,0 +1,6000 @@\n" + lines.join("\n"));
  assert.ok(pages.length > 1); assert.deepEqual(pages.flatMap(page => page.flatMap(hunk => hunk.lines)), lines);
  let next = 1; for (const page of pages) { assert.equal(page[0]!.new_start, next); next += page.flatMap(h => h.lines).length; }
});

test("R9: the GitHub back action actually returns from a PR and repository to the top list", async () => {
  const f = new Fixture(), h = await f.setup(), snapshot = snapshotPage(h.service, new WriteReceipts());
  for (const state of [transition({}, "thread:example/repo#12"), transition({}, "repo:example/repo")]) {
    assert.deepEqual(transition(state, "back"), {});
    const result = await snapshot.action!(request({ state: JSON.stringify(state), action: "back", ui_version: 7 }));
    assert.ok(result && result.view); assert.equal(JSON.parse(result.view.state!).r, undefined); assert.ok(result.view.tabs!.items.some(i => i.value === "review"));
  }
});

test("UI: check details retain annotations and steps even when the optional log is blocked", async () => {
  const f = new Fixture(), h = await f.setup();
  f.overrides = url => url.pathname.endsWith("/check-runs/71") ? Response.json({ id: 71, name: "Build", status: "completed", conclusion: "failure", app: { slug: "github-actions" }, output: { summary: "Synthetic summary" } }) : url.pathname.endsWith("/annotations") ? Response.json([{ path: "file.ts", start_line: 2, message: "Synthetic annotation", annotation_level: "failure" }]) : url.pathname.endsWith("/jobs/71") ? Response.json({ run_id: 81, steps: [{ number: 1, name: "Synthetic step", status: "completed", conclusion: "failure" }] }) : url.pathname.endsWith("/logs") ? new Response(null, { headers: { "content-length": String(9 << 20) } }) : undefined;
  const data = await loadPage(h.service, { v: "check", r: "example/repo", id: 71 }); assert.equal(data.kind, "check");
  const renderer = new Renderer({ authorize: () => false });
  try { const view = renderer.mount(page({ loaded: data, state: { v: "check", r: "example/repo", id: 71 }, action: id => ({ id }) })).view; const encoded = JSON.stringify(view); assert.ok(encoded.includes("Synthetic annotation") && encoded.includes("Synthetic step") && encoded.includes(translations().logBlocked)); } finally { renderer.dispose(); }
});

test("UI: overview preserves metadata and draft/locked write eligibility", async () => {
  const f = new Fixture(), h = await f.setup();
  f.overrides = url => url.pathname.endsWith("/issues/12") ? Response.json({ ...issue, locked: true, labels: [{ name: "synthetic-label" }] }) : url.pathname.endsWith("/pulls/12") ? Response.json({ ...pull, draft: true, changed_files: 3, additions: 20, deletions: 2, commits: 4, mergeable_state: "draft", milestone: { title: "Synthetic milestone" } }) : undefined;
  const data = await loadPage(h.service, { r: "example/repo", n: 12, t: "overview" });
  const renderer = new Renderer({ authorize: () => false });
  try { const view = renderer.mount(page({ loaded: data, state: { r: "example/repo", n: 12, t: "overview" }, action: id => ({ id }) })).view; const encoded = JSON.stringify(view); assert.ok(encoded.includes("Synthetic milestone") && encoded.includes("synthetic-label")); const composer = view.sections.flatMap(s => s.items).find(c => c.type === "composer"); assert.ok(composer?.type === "composer"); assert.ok(composer.buttons.some(b => b.action.id === "edit:review")); assert.ok(!composer.buttons.some(b => ["edit:comment", "edit:merge"].includes(b.action.id))); } finally { renderer.dispose(); }
});

test("UI: failed jobs rerun once per workflow rather than once per check", async () => {
  const f = new Fixture(), h = await f.setup(); f.overrides = url => url.pathname.endsWith("/check-runs") ? Response.json({ total_count: 2, check_runs: [71, 72].map(id => ({ id, name: `Synthetic ${id}`, status: "completed", conclusion: "failure", app: { slug: "github-actions" } })) }) : /\/jobs\/7[12]$/.test(url.pathname) ? Response.json({ run_id: 81 }) : undefined;
  assert.equal(await h.service.rerunFailed("example/repo", 12), 1); assert.equal(f.hits.filter(hit => hit.method === "POST" && hit.url.endsWith("/rerun-failed-jobs")).length, 1);
});

test("UI: legacy load_diff returns native diff items and expand reaches the rest of the patch", async () => {
  const f = new Fixture(), h = await f.setup();
  const result = await snapshotPage(h.service, new WriteReceipts()).action!(request({ state: JSON.stringify({ r: "example/repo", n: 12, t: "files" }), action: "load_diff", values: { path: "file.ts" }, ui_version: 5 }));
  assert.ok(result); assert.equal(result.items?.length, 1); assert.equal(result.items![0]!.type, "diff");
  assert.equal(transition({ r: "example/repo", n: 12, t: "files" }, "diff:file.ts").v, "diff");
});

test("UI: expanding a diff does not trap navigation in the diff view", () => {
  const expanded = transition({ r: "example/repo", n: 12, t: "files" }, "diff:file.ts");
  const overview = transition(expanded, "tab", { t: "overview" });
  assert.equal(overview.v, undefined); assert.equal(overview.t, "overview"); assert.equal(overview.r, "example/repo");
});

test("UI: preview rejects overlong writes, nonexistent diff lines and stale merge eligibility", async () => {
  const f = new Fixture(), h = await f.setup();
  const { preview } = await import("../src/root.tsx");
  await assert.rejects(() => preview(h.service, { v: "write", w: "comment", r: "example/repo", n: 12 }, { body: "x".repeat(2001) }), coded("invalid_params"));
  await assert.rejects(() => preview(h.service, { v: "write", w: "line", r: "example/repo", n: 12, p: "file.ts", s: "new", l: 99 }, { body: "Synthetic inline comment" }), coded("invalid_params"));
  f.overrides = url => url.pathname.endsWith("/pulls/12") ? Response.json({ ...pull, draft: true }) : undefined;
  await assert.rejects(() => preview(h.service, { v: "write", w: "merge", r: "example/repo", n: 12 }, { event: "merge" }), coded("invalid_params"));
});

// Installed-hub HTTP and controlled upstream Range/ETag evidence are acceptance gates,
// not skipped synthetic tests. scripts/acceptance-gates.mjs keeps them explicitly blocked.
