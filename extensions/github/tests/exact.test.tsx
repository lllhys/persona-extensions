import { test } from "node:test";
import assert from "node:assert/strict";
import { connect, ExactJSON, JSONIntegerError, type Program } from "@persona/extension";
import type { Snapshot, UIEvent } from "@persona/extension/ui";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { Fixture, issue, comment, notification } from "./fixtures.ts";
import { readArgs, strings, pointerInteger, stringArray, stringMapFields, optionArgs, parseJSON, stringify } from "../src/integers.ts";
import { readState, transition } from "../src/page-model.ts";
import oracleCases from "../../../sdk/ts/src/testdata/int64-cases.json";
const large = 9223372036854775807n, nearby = 9223372036854775806n;
const invalid = (error: unknown) => error instanceof JSONIntegerError && error.code === "invalid_params";
const response = (value: unknown) => new Response(stringify(value), { headers: { "content-type": "application/json" } });
async function harness() {
  const bundle = await import(pathToFileURL(resolve(".source-dist/github/bundle/main.mjs")).href);
  const fixture = new Fixture(), frames: any[] = []; let protocol: ReturnType<typeof connect>;
  protocol = connect(bundle.createProgram(fixture.fetch, { background: false }) as Program, line => {
    const frame = JSON.parse(line); frames.push(frame);
    if (frame.method?.startsWith("connector.secrets.") || frame.method === "connector.credentials.gh_token") void fixture.host.call(frame.method, frame.params).then(result => protocol.line(JSON.stringify({ id: frame.id, result })));
  });
  let serial = 0;
  const raw = async (method: string, params: string) => { const id = "exact-" + ++serial; protocol.line(`{"id":"${id}","method":${JSON.stringify(method)},"params":${params}}`); await protocol.drain(); const answer = frames.find(frame => frame.id === id); assert.ok(answer); return answer; };
  const call = (method: string, params: unknown = {}) => raw(method, stringify(params));
  await call("initialize", { protocol: 1, config: { auth: { from_machine: "synthetic-computer" } }, active: { connectors: ["github"], ui: ["github"] } });
  await call("connector.machines.online", { machine: "synthetic-computer" });
  return { fixture, frames, protocol, raw, call };
}
function interaction(snapshot: Snapshot, key: string, values: Record<string, string> = {}): UIEvent {
  const item = snapshot.view.sections.flatMap(section => section.items).find(item => item.key === key);
  assert.ok(item && "action" in item && item.action);
  return { generation: snapshot.generation, version: snapshot.version, key, actionId: item.action.id, eventId: crypto.randomUUID(), values };
}

test("official lexical int64 fixtures agree with the unchanged Go oracle dataset", () => {
  for (const item of oracleCases) {
    const decode = () => ExactJSON.parse(item.raw).int64Fields(["topic", "replies"]);
    if (item.error) assert.throws(decode, invalid, item.name);
    else { const decoded = decode(); assert.equal(String(decoded.topic), item.topic, item.name); assert.equal(String(decoded.replies), item.replies, item.name); }
  }
});

test("complete read struct validates unused fields, scalar nulls, folded keys and pointer null resets", () => {
  assert.deepEqual(readArgs(ExactJSON.parse('{"QUERY":"q","kind":null,"repo":"example/repo","number":9223372036854775807,"number":null,"limit":-9223372036854775808,"comments":3,"COMMENTS":null,"path":null,"ref":"main"}')), { query: "q", kind: "", repo: "example/repo", number: large, limit: -9223372036854775808n, comments: undefined, path: "", ref: "main" });
  assert.equal(pointerInteger(ExactJSON.parse('{"comments":null,"comments":9223372036854775807}'), "comments"), large);
  assert.equal(pointerInteger(ExactJSON.parse('{"comments":4,"comments":null,"comments":2}'), "comments"), 2);
  for (const field of ["query", "kind", "repo", "path", "ref"]) for (const value of ["4", "true", "[]", "{}"])
    assert.throws(() => readArgs(ExactJSON.parse(`{"${field}":${value},"${field}":"valid"}`)), invalid);
  for (const field of ["limit", "number", "comments"]) for (const value of ["12.0", "1.2e1", "-1.0", "-1e0", '"12"', "true", "[]", "{}", "9223372036854775808", "-9223372036854775809"])
    assert.throws(() => readArgs(ExactJSON.parse(`{"${field}":${value},"${field}":12}`)), invalid);
  for (const raw of ["[]", "true", '"string"']) assert.throws(() => readArgs(ExactJSON.parse(raw)), invalid);
  assert.equal(readArgs(ExactJSON.parse("null")).number, 0);
  assert.equal(readArgs(ExactJSON.parse('{"unknown":' + "[".repeat(9000) + "0" + "]".repeat(9000) + ',"number":12}')).number, 12);
});
test("Go string and container semantics retain ordered errors, null resets and case-sensitive map keys", () => {
  assert.deepEqual(strings(ExactJSON.parse('{"repo":"first","REPO":null,"re\\u0070o":"last"}'), ["repo"]), { repo: "last" });
  assert.deepEqual(stringArray(ExactJSON.parse('["first",null,"last"]')), ["first", "", "last"]);
  assert.equal(stringArray(ExactJSON.parse("null")), undefined);
  assert.deepEqual({ ...stringMapFields(ExactJSON.parse('{"values":{"x":"first","X":null},"VALUES":{"x":"last","y":"added"}}'), "values") }, { x: "last", X: "", y: "added" });
  assert.equal(stringMapFields(ExactJSON.parse('{"values":{"x":"first"},"values":null}'), "values"), undefined);
  for (const raw of ['{"values":{"x":2,"x":"last"}}', '{"values":[]}', '{"values":{"x":true},"values":null}']) assert.throws(() => stringMapFields(ExactJSON.parse(raw), "values"), invalid);
  assert.deepEqual(optionArgs(ExactJSON.parse('{"CI":"off","muted_repos":["example/repo"],"MUTED_REPOS":null,"ignore_branches":[]}')), { ci: "off", muted_repos: undefined, ignore_branches: [] });
  assert.throws(() => optionArgs(ExactJSON.parse('{"ci":3,"ci":"mine"}')), invalid);
});
test("raw API/state numeric IDs and string IDs coexist without collisions or marker substitution", () => {
  const data = parseJSON('{"id":9223372036854775807,"nested":{"id":"notification-9223372036854775807"},"items":[{"id":9223372036854775806},{"databaseId":9223372036854775807}]}') as any;
  assert.equal(data.id, large); assert.equal(data.items[0].id, nearby); assert.equal(data.nested.id, "notification-9223372036854775807");
  const marker = "\u0000persona-int64-0\u0000", json = stringify({ id: large, marker }); assert.equal((parseJSON(json) as any).marker, marker); assert.ok(json.includes(String(large)));
  assert.equal(readState(`{"n":${large},"id":${nearby}}`).id, nearby);
  assert.equal(transition({ n: large }, "edit:reply", { id: String(nearby) }).id, nearby);
  assert.equal(transition({}, "thread:example/repo#" + large).n, large);
  assert.equal(transition({}, "thread:example/repo#000" + large).n, large);
  assert.equal(transition({}, "edit:reply", { id: "+00012" }).id, 12);
  for (const token of ["1.0", "1e0", "-1.0", "9223372036854775808"]) assert.throws(() => parseJSON(`{"id":${token}}`), invalid);
});
test("separately bundled lazy namespace receives real lexical metadata before any read or write", { timeout: 10000 }, async () => {
  const h = await harness();
  try {
    const start = h.fixture.hits.length;
    for (const number of ["12.0", "1.2e1", "-1.0", "-1e0", "9223372036854775808", '"12"']) for (const method of ["connector.tools.call", "connector.actions.describe", "connector.actions.do"]) {
      const result = await h.raw(method, `{"name":"${method.includes("tools") ? "github_read" : "github_comment"}","request_id":"invalid","args":{"repo":"example/repo","number":${number},"body":"Synthetic body"}}`);
      assert.equal(result.error.code, "invalid_params");
    }
    assert.equal(h.fixture.hits.length, start);
    const unused = await h.raw("connector.tools.call", '{"name":"github_search","args":{"query":"synthetic","ref":4}}'); assert.equal(unused.error.code, "invalid_params");
    for (const params of ['{"step":3,"step":"device"}', '{"values":{"x":4,"x":"valid"}}']) assert.equal((await h.raw("connector.login.step", params)).error.code, "invalid_params");
    for (const method of ["connector.thread.page", "connector.archive.changes"]) assert.equal((await h.raw(method, '{"thread":"example/repo#12","limit":1e0}')).error.code, "invalid_params");
    assert.equal((await h.raw("connector.options.set", '{"options":{"ci":3,"ci":"mine"}}')).error.code, "invalid_params");
    for (const params of ['{"thread":"example/repo#12","text":"Synthetic body","item_id":3}', '{"thread":"example/repo#12","text":"Synthetic body","changes":[{"ref":3,"ref":"valid"}]}']) assert.equal((await h.raw("connector.inbox.reply", params)).error.code, "invalid_params");
    for (const params of ['{"source":3,"source":"device"}', '{"source":"device","machine_name":[]}', '{"SOURCE":true}']) assert.equal((await h.raw("connector.auth.set", params)).error.code, "invalid_params");
    assert.equal((await h.raw("connector.auth.set", '{"SOURCE":"device","MACHINE":null}')).result.source, "device");
    // Empty default fields decode a fresh Go struct, including nullable change elements.
    assert.ok((await h.raw("connector.inbox.reply", '{"thread":"example/repo#12","text":"Synthetic body","changes":[null,{"ref":"row","field":"status","value":"done"}],"sender_id":4}')).result);
    assert.equal((await h.raw("connector.inbox.settle", '{"threads":[2]}')).error.code, "invalid_params");
  } finally { h.protocol.close(); }
});
test("legal full int64 write previews, HTTP paths and replay receipts stay exact across bundled SDK copies", { timeout: 10000 }, async () => {
  const h = await harness();
  try {
    h.fixture.overrides = url => url.pathname.endsWith("/issues/" + large) ? response({ ...issue, number: large, pull_request: undefined }) : undefined;
    const params = `{"name":"github_comment","request_id":"large-write","args":{"repo":"example/repo","number":${large},"NUMBER":null,"body":"Synthetic exact body"}}`;
    const preview = await h.raw("connector.actions.describe", params); assert.ok(preview.result.summary.includes("#" + large));
    assert.ok((await h.raw("connector.actions.do", params)).result.text.includes("#" + large));
    await h.raw("connector.actions.do", params);
    assert.equal(h.fixture.hits.filter(hit => hit.method === "POST" && hit.url.endsWith(`/issues/${large}/comments`)).length, 1);
    assert.equal((await h.raw("connector.actions.do", params.replace(String(large), String(nearby)))).error.code, "invalid_params");
    // The outer args is json.RawMessage: only the last folded field is decoded.
    const selected = await h.raw("connector.actions.describe", `{"name":"github_comment","args":{"number":1.0},"ARGS":{"repo":"example/repo","number":${large},"body":"Synthetic body"}}`); assert.ok(selected.result.summary.includes(String(large)));
  } finally { h.protocol.close(); }
});
test("large numeric API IDs survive native cold navigation, saved callbacks and comment writes", { timeout: 10000 }, async () => {
  const h = await harness();
  try {
    const exactIssue = { ...issue, number: large, pull_request: undefined };
    h.fixture.overrides = url => url.pathname === "/search/issues" ? response({ total_count: 1, items: [exactIssue] }) : url.pathname.endsWith("/issues/" + large) ? response(exactIssue) : url.pathname.endsWith("/comments") ? response([{ ...comment, id: nearby }]) : undefined;
    const mounted = await h.call("ui.page.mount", { session: "large-native", transport: "snapshot", params: { contribution: "github", locale: "en" } }); assert.ok(!mounted.error, JSON.stringify(mounted.error));
    const first: Snapshot = mounted.result, row = first.view.sections.flatMap(section => section.items).find(item => item.type === "row" && item.subtitle?.includes(String(large))); assert.ok(row);
    const navigated = await h.call("ui.page.event", { session: "large-native", ticket: "synthetic-host-ticket", interaction: interaction(first, row.key!) });
    assert.equal(navigated.result.status, "accepted"); const next: Snapshot = navigated.result.snapshot;
    assert.equal(readState(next.view.state).n, large); assert.ok(JSON.stringify(next).includes(String(nearby))); assert.ok(!JSON.stringify(next).includes("9223372036854776000"));
  } finally { h.protocol.close(); }
});
test("inbox, archive and legacy thread cursors keep full numeric identities and string notifications", { timeout: 10000 }, async () => {
  const f = new Fixture(), h = await f.setup(), exactIssue = { ...issue, number: large, pull_request: undefined };
  f.notifications = [{ ...notification, id: "notification-large", subject: { ...notification.subject, type: "Issue", url: "https://api.github.com/repos/example/repo/issues/" + large } }];
  f.overrides = url => url.pathname === "/search/issues" ? response({ total_count: 1, items: [exactIssue] }) : url.pathname.endsWith("/issues/" + large) ? response(exactIssue) : url.pathname.endsWith("/comments") ? response([{ ...comment, id: nearby }]) : undefined;
  await h.inbox.poll(); assert.equal(h.inbox.collect()[0]!.thread, "example/repo#" + large);
  const archive = await h.archive.changes("", 50), batch = archive.batches?.[0]; assert.ok(batch?.chat && batch.messages); assert.ok(batch.chat.id.endsWith("-" + large)); assert.ok(batch.messages.some(message => message.id === "c" + nearby));
  const thread = await h.connector.handle("thread.page", { thread: "example/repo#" + large, limit: large }) as any; assert.equal(thread.chat.id, "example/repo#" + large); assert.ok(thread.messages.some((message: any) => message.id === "c" + nearby));
});
