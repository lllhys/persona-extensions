import { test } from "node:test";
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { connect, type Program } from "@persona/extension";
import { cleanNodes, configFrom, filter, parseJSON, parseTopics } from "../src/model.ts";
import { Fixture, rawTopic } from "./fixtures.ts";

const ids = (topics: ReturnType<typeof parseTopics>) => topics.map(topic => topic.id);
const deep = '[{"id":1,"unused":' + "[".repeat(9000) + "0" + "]".repeat(9000) + "}]";

test("keyword lowercase is context-free, not final-sigma folding or expansion", () => {
  const topics = parseTopics(JSON.stringify([rawTopic(1, { title: "ΟΣ", content: "" })]));
  assert.deepEqual(ids(filter(topics, { keywords: ["σ"] })), ["1"]);
  assert.deepEqual(ids(filter(topics, { exclude_keywords: ["σ"] })), []);
  assert.deepEqual(ids(filter(topics, { keywords: ["ς"] })), []);
  assert.deepEqual(ids(filter(topics, { exclude_keywords: ["ς"] })), ["1"]);
  const dotted = parseTopics('[{"id":2,"title":"İ"}]');
  assert.deepEqual(ids(filter(dotted, { keywords: ["i"] })), ["2"]);
  assert.deepEqual(ids(filter(dotted, { keywords: ["i̇"] })), []);
});

test("configuration matches folded field names without hiding ordered type errors", () => {
  assert.deepEqual(configFrom({ List: "nodes", Nodes: ["go"], Keywords: ["database"], EXCLUDE_NODES: ["jobs"], EXCLUDE_KEYWORDS: ["blocked"], TZ: "UTC" }),
    { list: "nodes", nodes: ["go"], keywords: ["database"], exclude_nodes: ["jobs"], exclude_keywords: ["blocked"], tz: "UTC" });
  assert.deepEqual(configFrom({ "liſt": "latest", ignored: 1 }), { list: "latest" });
  for (const raw of ['{"Nodes":1}', '{"List":false}', '{"nodes":1,"Nodes":["go"]}', '{"nodes":[1],"nodes":["go"]}']) {
    assert.throws(() => configFrom(parseJSON(raw)), { code: "invalid" });
  }
  assert.deepEqual(configFrom(parseJSON('{"List":"nodes","list":null,"Nodes":["go"],"nodes":null}')), { list: "nodes" });
});

test("node trimming accepts Go White_Space but does not strip a BOM or zero-width space", () => {
  assert.deepEqual(cleanNodes(["\u0085go\u0085", " python　", "\u0085", "go"]), { good: ["go", "python"], bad: [] });
  for (const edge of ["﻿", "​"]) assert.deepEqual(cleanNodes([edge + "go" + edge]), { good: [], bad: [edge + "go" + edge] });
});

test("lossless topic parsing handles the 18,021-byte unknown 9,000-deep array", () => {
  assert.equal(Buffer.byteLength(deep), 18021);
  assert.deepEqual(ids(parseTopics(deep)), ["1"]);
  for (const suffix of [',"id":1e0}', ',"id":9223372036854775808}', ',"id":"1","id":1}']) {
    assert.throws(() => parseTopics(deep.slice(0, -2) + suffix + "]"), { code: "bad_answer" });
  }
  assert.throws(() => parseTopics(deep.slice(0, -1)), { code: "bad_answer" });
});

for (const [name, config, body, args, expected, missing] of [
  ["sigma", { keywords: ["σ"] }, '[{"id":1,"title":"ΟΣ"}]', {}, "id 1", "id 2"],
  ["folded config", { List: "nodes", Nodes: ["go"], Keywords: ["database"] }, '[{"id":1,"title":"database"},{"id":2,"title":"unrelated"}]', { source: "nodes" }, "id 1", "id 2"],
  ["Go trim", { nodes: ["\u0085go\u0085"] }, '[{"id":1}]', { source: "nodes" }, "id 1", "id 2"],
  ["deep unknown", {}, deep, {}, "id 1", "id 2"],
] as const) test(`actual bundled Program retains the focused ${name} case`, async () => {
  const bundle = await import(pathToFileURL(resolve("build/v2ex.mjs")).href), fixture = new Fixture(), frames: any[] = [];
  fixture.answers.set("/topics/hot.json", { body }); fixture.answers.set("/topics/show.json?node_name=go", { body });
  const p = connect(bundle.createProgram(fixture.fetch, { limits: { gap: 0 } }) as Program, line => frames.push(JSON.parse(line)));
  try {
    p.line(JSON.stringify({ id: "init", method: "initialize", params: { protocol: 1, config } })); await p.drain();
    assert.equal(frames.find(frame => frame.id === "init").error, undefined);
    p.line(JSON.stringify({ id: "read", method: "connector.tools.call", params: { name: "v2ex_hot", args } })); await p.drain();
    const frame = frames.find(frame => frame.id === "read"); assert.equal(frame.error, undefined);
    assert.ok(frame.result.text.includes(expected)); assert.ok(!frame.result.text.includes(missing));
    assert.equal(fixture.requests.length, 1);
    p.line('{"id":"status","method":"connector.status"}'); await p.drain();
    assert.equal(frames.find(frame => frame.id === "status").result.state, "ok");
  } finally { p.close(); }
});
