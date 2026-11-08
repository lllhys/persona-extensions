import { test } from "node:test";
import assert from "node:assert/strict";
import { ExtError, JSONIntegerError } from "@persona/extension";
import { Renderer } from "@persona/extension/ui";
import { Fixture, issue, pull, comment, notification } from "./fixtures.ts";
import { preview, perform, root } from "../src/root.tsx";
import { WriteReceipts, writeArgs } from "../src/connector.ts";
import { loadPage, readState } from "../src/page-model.ts";
import { ThreadReader } from "../src/thread.ts";
import { b64decode, type Notification } from "../src/model.ts";
import { translations } from "../src/localization.ts";
const coded = (code: string) => (error: unknown) => (error instanceof ExtError || error instanceof JSONIntegerError) && error.code === code;
const deferred = <T,>() => { let resolve!: (value: T) => void; const promise = new Promise<T>(yes => resolve = yes); return { resolve, promise }; };
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
const until = async (condition: () => boolean) => { const end = Date.now() + 2000; while (!condition()) { assert.ok(Date.now() < end, "Fixture wait timed out"); await tick(); } };
const ciNotice = (id: string, workflow: string, status: string, at: string): Notification => ({ ...notification, id, reason: "ci_activity", updated_at: at, subject: { title: `${workflow} workflow run ${status} for main branch`, type: "CheckSuite", url: "" } });

test("round2-1: logout revokes immediately and orders its durable clear after an outstanding device save", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup(), entered = deferred<void>(), release = deferred<void>(), oldCall = f.host.call;
  await h.auth.start(); f.deviceResponses.push({ access_token: "synthetic-late-device-token" });
  let saves = 0;
  f.host.call = async <T,>(method: string, params?: unknown): Promise<T> => {
    if (method === "connector.secrets.save" && ++saves === 1) { entered.resolve(); await release.promise; }
    return oldCall<T>(method, params);
  };
  const login = h.auth.step("device"), rejected = assert.rejects(login, coded("auth_changed")); await entered.promise;
  let completed = false; const logout = h.auth.logout().then(() => { completed = true; });
  assert.equal(h.auth.status.state, "not_signed_in"); await tick(); assert.equal(completed, false);
  await assert.rejects(() => h.auth.token(), coded("not_signed_in"));
  release.resolve(); await rejected; await logout;
  assert.equal(h.auth.status.state, "not_signed_in"); assert.deepEqual(JSON.parse(b64decode(f.secret)), {});
  assert.equal((await f.setup()).auth.status.state, "not_signed_in");
  assert.equal(f.hits.some(hit => hit.headers.get("authorization") === "Bearer synthetic-late-device-token"), false);
});

test("round2-2: wrong write field types are rejected by describe and do before any HTTP request", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup(); f.hits = [];
  for (const bad of [{ number: "12" }, { number: true }, { number: 12.5 }, { number: Infinity }, { body: { unexpected: true } }, { body: ["text"] }, { text: true }, { repo: ["example/repo"] }, { event: {} }, { chat: 12 }]) {
    for (const method of ["actions.describe", "actions.do"]) await assert.rejects(() => h.connector.handle(method, { name: "github_comment", request_id: "synthetic-invalid", args: { repo: "example/repo", number: 12, body: "Synthetic body", ...bad } }), coded("invalid_params"));
  }
  assert.equal(f.hits.length, 0);
  assert.throws(() => writeArgs("github_comment", Object.create({ repo: "example/repo", number: 12, body: "Inherited body" })), coded("invalid_params"));
  assert.deepEqual(writeArgs("github_comment", { chat: "example/repo#12", text: " Synthetic reply " }), { repo: "example/repo", number: 12, body: "Synthetic reply", event: "" });
  assert.deepEqual(writeArgs("github_comment", { repo: "example/repo", number: 12, body: null, text: "Synthetic reply" }), { repo: "example/repo", number: 12, body: "Synthetic reply", event: "" });
});

test("round2-3: comment 1001 from another account cannot falsely settle the inbox as replied", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup();
  const comments = Array.from({ length: 1001 }, (_, i) => ({ ...comment, id: i + 1, user: { login: i === 1000 ? "other-reader" : "reader" }, created_at: i === 1000 ? "2026-01-03T00:00:00Z" : comment.created_at, body: i === 1000 ? "Synthetic latest question" : "Synthetic older reply" }));
  f.overrides = url => url.pathname.endsWith("/issues/12") ? Response.json({ ...issue, comments: 1001 }) : url.pathname.endsWith("/comments") ? Response.json(comments.slice((Number(url.searchParams.get("page")) - 1) * 100, Number(url.searchParams.get("page")) * 100)) : undefined;
  await h.inbox.poll(); const thing = h.inbox.collect()[0]!;
  assert.equal(thing.done, undefined); assert.equal(thing.sender, "other-reader"); assert.equal(thing.preview, "Synthetic latest question"); assert.ok(thing.context!.includes("Synthetic latest question"));
  assert.ok(f.hits.some(hit => hit.url.includes("/issues/12/comments?per_page=100&page=11")));
});

test("round2-4: nullable actors preserve event text, sender fallback, thread and discussion", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup();
  f.overrides = url => url.pathname.endsWith("/events") ? Response.json([{ id: 901, event: "assigned", actor: null, assignee: { login: "synthetic-assignee" }, created_at: comment.created_at }]) : undefined;
  const thread = await h.connector.thread({ contribution: "github", thread: "example/repo#12" });
  const event = thread.messages.find(m => m.id === "e901")!; assert.ok(event.text!.includes(translations().unknownActor)); assert.equal(event.sender!.name, translations().deletedUser);
  const discussion = await loadPage(h.service, { r: "example/repo", n: 12, t: "discussion" }); assert.equal(discussion.kind, "detail");
  if (discussion.kind === "detail") assert.ok(discussion.comments.some(c => c.id === -901 && c.user?.login === ""));
});

test("round2-5: optional non-Range 9 MiB logs retain the complete github_checks result and blocker", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup();
  f.overrides = url => url.pathname.endsWith("/check-runs") ? Response.json({ total_count: 2, check_runs: [{ id: 71, name: "Synthetic failing Build", status: "completed", conclusion: "failure", app: { slug: "github-actions" } }, { id: 72, name: "Synthetic successful Docs", status: "completed", conclusion: "success" }] }) : url.pathname.endsWith("/logs") ? new Response(null, { headers: { "content-length": String(9 << 20) } }) : undefined;
  const result = await h.connector.handle("tools.call", { name: "github_checks", args: { repo: "example/repo", number: 12 } }) as { text: string };
  assert.ok(result.text.includes("Synthetic failing Build: failure")); assert.ok(result.text.includes("Synthetic successful Docs: success")); assert.ok(result.text.includes(translations().logBlocked));
  await assert.rejects(() => h.client.log("example/repo", 71), coded("bounded_log_unsupported"));
});

test("round2-6: refreshing repository B neither rejects nor poisons repository A's normal read", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup(), delayed = deferred<Response>();
  f.overrides = url => url.pathname === "/repos/example/a/issues/12" ? delayed.promise : undefined;
  const a = h.client.json("/repos/example/a/issues/12"); await until(() => f.hits.some(hit => hit.url.includes("/repos/example/a/issues/12")));
  h.client.invalidate("example/b"); delayed.resolve(Response.json({ title: "Synthetic A" }, { headers: { etag: "a" } })); assert.deepEqual(await a, { title: "Synthetic A" });
  f.overrides = () => { throw new Error("A should be cached"); }; assert.deepEqual(await h.client.json("/repos/example/a/issues/12"), { title: "Synthetic A" });
});

test("round2-6: an in-flight response for the refreshed repository cannot refill its stale cache", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup(), delayed = deferred<Response>();
  f.overrides = url => url.pathname.endsWith("/issues/12") ? delayed.promise : undefined;
  const old = h.client.json("/repos/example/repo/issues/12"); await tick(); h.client.invalidate("example/repo"); delayed.resolve(Response.json({ title: "Synthetic old" }, { headers: { etag: "old" } })); await old;
  f.overrides = url => url.pathname.endsWith("/issues/12") ? Response.json({ title: "Synthetic new" }) : undefined;
  assert.deepEqual(await h.client.json("/repos/example/repo/issues/12"), { title: "Synthetic new" });
});

test("round2-7: two failed workflows remain open until both later succeed, by notifications", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup(); f.notifications = [ciNotice("build", "Build", "failed", "2026-01-03T00:00:00Z"), ciNotice("docs", "Documentation", "failed", "2026-01-03T00:01:00Z")];
  await h.inbox.poll(); assert.equal(h.inbox.collect().length, 1);
  f.notifications[1] = ciNotice("docs", "Documentation", "succeeded", "2026-01-03T00:02:00Z"); await h.inbox.poll(); assert.equal(h.inbox.collect()[0]!.done, undefined);
  f.notifications[0] = ciNotice("build", "Build", "succeeded", "2026-01-03T00:03:00Z"); await h.inbox.poll(); assert.equal(h.inbox.collect()[0]!.done, "read");
});

test("round2-7: CI follow-up checks every failed workflow even when only Documentation is newest", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup(); f.notifications = [ciNotice("build", "Build", "failed", "2026-01-03T00:00:00Z"), ciNotice("docs", "Documentation", "failed", "2026-01-03T00:01:00Z")];
  await h.inbox.poll(); let buildSuccess = false;
  f.overrides = url => url.pathname.endsWith("/actions/runs") ? Response.json({ workflow_runs: [{ name: "Documentation", status: "completed", conclusion: "success" }, { name: "Build", status: "completed", conclusion: buildSuccess ? "success" : "failure" }] }) : undefined;
  f.now += 600001; await h.inbox.poll(); assert.equal(h.inbox.collect()[0]!.done, undefined);
  buildSuccess = true; f.now += 600001; await h.inbox.poll(); assert.equal(h.inbox.collect()[0]!.done, "read");
});

test("round2-8: line and draft previews pin the viewed commit and reject a changed head before writing", { timeout: 5000 }, async () => {
  for (const kind of ["line", "draft"]) {
    const f = new Fixture(), h = await f.setup(), receipts = new WriteReceipts();
    const state = await preview(h.service, { v: "write", w: kind, r: "example/repo", n: 12, p: "file.ts", l: 1, s: "new", head: "head-one" }, { body: "Synthetic inline body" }); assert.equal(state.head, "head-one");
    f.head = "head-two"; f.hits = []; await assert.rejects(() => perform(h.service, receipts, readState(JSON.stringify(state)), "send", {}), coded("head_changed"));
    assert.equal(f.hits.some(hit => hit.method !== "GET"), false);
  }
  const f = new Fixture(), h = await f.setup(), state = await preview(h.service, { v: "write", w: "line", r: "example/repo", n: 12, p: "file.ts", l: 1, s: "new", head: "head-one" }, { body: "Synthetic pinned body" });
  await perform(h.service, new WriteReceipts(), state, "send", {}); assert.equal(JSON.parse(f.hits.find(hit => hit.method === "POST" && hit.url.endsWith("/comments"))!.body).commit_id, "head-one");
});

test("KNOWN round2-9 inherited Go defect: equal-timestamp full discovery page cannot reach issue 51", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup(); const issues = Array.from({ length: 51 }, (_, i) => ({ ...issue, number: i + 1, pull_request: undefined }));
  f.overrides = url => url.pathname === "/search/issues" ? Response.json({ total_count: 51, items: issues.slice(0, 50) }) : url.pathname.endsWith("/comments") ? Response.json([]) : undefined;
  const first = await h.archive.changes("", 50), second = await h.archive.changes(first.next, 50);
  assert.equal(first.batches!.length, 50); assert.deepEqual(second.batches, []); assert.equal(second.more, true); assert.equal(second.next, first.next);
});

test("round2-10: authentication switch clears account-local inbox, seen, polling, rate and avatar caches", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup(); await h.inbox.poll(); assert.equal(h.inbox.collect().length, 1);
  const reader = new ThreadReader(h.service, h.auth); reader.person({ login: "synthetic-person", avatar_url: "https://avatars.githubusercontent.com/u/123?v=4" });
  h.client.rateLow = true; h.client.rateReset = f.now + 600000;
  await h.auth.set({ contribution: "github", source: "machine", machine: "computer-b" }); assert.deepEqual(h.inbox.collect(), []); assert.equal(h.client.low(), false);
  f.hits = []; await h.inbox.poll(); assert.equal(h.inbox.collect().length, 1); assert.equal(f.hits.find(hit => hit.url.includes("/notifications"))!.headers.has("if-modified-since"), false);
  f.overrides = url => url.pathname.startsWith("/users/") ? Response.json({ login: "synthetic-person", avatar_url: "https://avatars.githubusercontent.com/u/456?v=1" }) : undefined;
  assert.ok((await reader.avatar("avatar:synthetic-person:4")).includes("/u/456"));
});

test("round2-10: a delayed A notification body cannot refill B or carry A's condition headers", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup(), release = deferred<void>(); let opened = false;
  f.overrides = url => url.pathname === "/notifications" ? new Response(new ReadableStream({ async start(controller) { opened = true; await release.promise; controller.enqueue(new TextEncoder().encode(JSON.stringify([notification]))); controller.close(); } }), { headers: { "last-modified": "synthetic-account-a" } }) : undefined;
  const a = h.inbox.poll(), rejected = assert.rejects(a, coded("auth_changed")); await until(() => opened);
  await h.auth.set({ contribution: "github", source: "machine", machine: "computer-b" }); f.overrides = undefined; release.resolve(); await rejected; assert.deepEqual(h.inbox.collect(), []);
  await h.inbox.poll(); assert.equal(f.hits.at(-1)!.headers.has("if-modified-since"), false);
});

test("round2-10: automatic credential-account change also clears old collection", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup(); await h.inbox.poll();
  f.overrides = url => url.pathname === "/user" ? Response.json({ login: "synthetic-new-reader" }) : undefined;
  await h.auth.check(); assert.deepEqual(h.inbox.collect(), []); assert.equal(h.auth.status.account, "synthetic-new-reader");
});

test("round2-10: old-account previews and visible page content cannot survive authentication change", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup(), receipts = new WriteReceipts(), state = await preview(h.service, { v: "write", w: "comment", r: "example/repo", n: 12 }, { body: "Synthetic A preview" });
  const renderer = new Renderer({ authorize: () => true });
  try {
    renderer.mount(root({ service: h.service, receipts, state: { r: "example/repo", n: 12 } })); await until(() => JSON.stringify(renderer.current().view).includes("Synthetic description"));
    await h.auth.set({ contribution: "github", source: "machine", machine: "computer-b" });
    await until(() => !JSON.stringify(renderer.current().view).includes("Synthetic description"));
    f.hits = []; await assert.rejects(() => perform(h.service, receipts, state, "send", {}), coded("auth_changed")); assert.equal(f.hits.some(hit => hit.method === "POST"), false);
  } finally { renderer.dispose(); }
});

test("round2-10: old request receipts and write-preparation continuations cannot authorize the next account", { timeout: 5000 }, async () => {
  const f = new Fixture(), h = await f.setup(), args = { name: "github_comment", request_id: "synthetic-account-bound-id", args: { repo: "example/repo", number: 12, body: "Synthetic A send" } };
  await h.connector.handle("actions.do", args);
  await h.auth.set({ contribution: "github", source: "machine", machine: "computer-b" }); f.hits = [];
  await assert.rejects(() => h.connector.handle("actions.do", args), coded("auth_changed")); assert.equal(f.hits.length, 0);
  const delayed = deferred<Response>(); f.overrides = url => url.pathname.endsWith("/pulls/12") ? delayed.promise : undefined;
  const write = h.service.write("example/repo", 12, "line", { body: "Synthetic old line", path: "file.ts", line: "1", side: "new" }, "head-one"), rejected = assert.rejects(write, coded("auth_changed")); await tick();
  await h.auth.set({ contribution: "github", source: "machine", machine: "computer-c" }); delayed.resolve(Response.json(pull)); await rejected;
  assert.equal(f.hits.some(hit => hit.method === "POST"), false);
});

test("round2-11 inherited cache defect is fixed: invalid JSON never enters the ETag cache", { timeout: 5000 }, async () => {
  for (const invalid of ["not-json", '{"number":01}', '{"number":1.}', '[1,]', '{"number":NaN}', '{"number":1} trailing']) {
    const f = new Fixture(), h = await f.setup(); let attempts = 0;
    f.overrides = url => url.pathname === "/synthetic-json" ? (++attempts === 1 ? new Response(invalid, { headers: { etag: "invalid" } }) : Response.json({ valid: true }, { headers: { etag: "valid" } })) : undefined;
    await assert.rejects(() => h.client.json("/synthetic-json"), SyntaxError); assert.deepEqual(await h.client.json("/synthetic-json"), { valid: true }); assert.equal(attempts, 2);
    assert.equal(f.hits.at(-1)!.headers.has("if-none-match"), false); assert.deepEqual(await h.client.json("/synthetic-json"), { valid: true }); assert.equal(attempts, 2);
  }
});

test("legacy Number reconstruction still loses lexemes; real RPC must use the exact opt-in", () => {
  for (const lexeme of ["12.0", "1.2e1"]) {
    const decoded = JSON.parse(`{"repo":"example/repo","number":${lexeme},"body":"Synthetic comment"}`);
    assert.equal(writeArgs("github_comment", decoded).number, 12); // Go's integer decoder rejects these spellings.
  }
  const decoded = JSON.parse('{"id":9223372036854775807}');
  assert.notEqual(JSON.stringify(decoded), '{"id":9223372036854775807}');
  assert.throws(() => writeArgs("github_comment", { repo: "example/repo", number: decoded.id, body: "Synthetic comment" }), coded("invalid_params"));
  // This safety rejection is also a parity gap for a legal Go int64, not proof of exact decoding.
});
