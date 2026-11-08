import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { test } from "node:test";

// The caller emits the official host SDK and default Program separately. There is
// no copied SDK source, custom parser, Bun global, or authorization bypass here.
const { connect, ExactJSON } = await import(pathToFileURL(process.argv[2]));
const { default: defaultProgram } = await import(pathToFileURL(process.argv[3]));
const cases = JSON.parse(readFileSync(new URL("../src/testdata/int64-cases.json", import.meta.url), "utf8"));
assert.equal(typeof globalThis.Bun, "undefined");

async function dispatch(raw, program = defaultProgram) {
  const frames = [];
  const conn = connect(program, text => frames.push(JSON.parse(text)));
  try {
    conn.line('{"id":"init","method":"initialize","params":{"protocol":1}}');
    await conn.drain();
    conn.line(raw);
    await conn.drain();
    return frames.at(-1);
  } finally { await conn.shutdown(); }
}

for (const c of cases) {
  test(`separately bundled source raw int64: ${c.name}`, { timeout: 1000 }, async () => {
    const frame = await dispatch(`{"id":"case","method":"connector.tools.call","params":{"name":"example_topic","args":${c.raw}}}`);
    assert.equal(frame.id, "case");
    if (c.error) assert.equal(frame.error.code, "invalid_params");
    else assert.deepEqual(frame.result, { topic: c.topic, replies: c.replies, raw: c.raw.trim() });
  });
}

test("source RawMessage field folding, escaped keys, last duplicate and null", { timeout: 1000 }, async () => {
  for (const [params, expected] of [
    ['{"a\\u0072gs":{"topic":1.0},"ARGS":{"TOPIC":6,"replie\\u017f":6}}', { topic: "6", replies: "6", raw: '{"TOPIC":6,"replie\\u017f":6}' }],
    ['{"args":{"topic":6},"ARGS":null}', { topic: "0", replies: "0", raw: "null" }],
    ['{"args":null,"ARGS":{"topic":6}}', { topic: "6", replies: "0", raw: '{"topic":6}' }],
    ['{}', { topic: "0", replies: "0" }],
  ]) {
    assert.deepEqual((await dispatch(`{"id":"case","method":"connector.tools.call","params":${params}}`)).result, expected);
  }
  assert.deepEqual((await dispatch('{"id":"case","method":"connector.tools.call","params":{"args":{"topic":1.0}},"pa\\u0072ams":{"args":{"topic":6}}}')).result,
    { topic: "6", replies: "0", raw: '{"topic":6}' });
  assert.deepEqual((await dispatch('{"id":"case","method":"connector.tools.call","params":{"args":{"topic":1.0}},"PARAMS":{"args":{"topic":6}}}')).result,
    { topic: "6", replies: "0", raw: '{"topic":6}' });
  assert.deepEqual((await dispatch('{"id":"case","method":"connector.tools.call","params":{"args":{"topic":6}},"Params":null}')).result,
    { topic: "0", replies: "0" });
});

test("source opt-in retains original tokens while legacy namespaces keep two arguments and Number", { timeout: 1000 }, async () => {
  const counts = [];
  const program = { ...defaultProgram, namespaces: { ...defaultProgram.namespaces,
    legacy: function (_method, params) { counts.push(arguments.length); return params; },
  } };
  for (const token of ["1.0", "1e0", "1.0000000000000001", "9007199254740993", "9223372036854775807"]) {
    assert.equal((await dispatch(`{"id":"case","method":"legacy.get","params":{"topic":${token}}}`, program)).result.topic, JSON.parse(token));
  }
  assert.deepEqual(counts, [2, 2, 2, 2, 2]);
});

test("deep unknown raw values do not recurse or pollute the prototype", { timeout: 1000 }, async () => {
  const raw = `{"unknown":${"[".repeat(9000)}0${"]".repeat(9000)},"__proto__":{"polluted":true},"topic":6}`;
  const frame = await dispatch(`{"id":"case","method":"connector.tools.call","params":{"args":${raw}}}`);
  assert.equal(frame.result.topic, "6");
  assert.equal(Object.hasOwn(Object.prototype, "polluted"), false);
  const fields = ExactJSON.parse('{"__proto__":6}').int64Fields(["__proto__"]);
  assert.equal(Object.getPrototypeOf(fields), null);
  assert.equal(fields.__proto__, 6n);
});

test("JSON syntax errors never invoke the source or legacy handler", { timeout: 1000 }, async () => {
  let calls = 0;
  const frames = [];
  const conn = connect({ ...defaultProgram, namespaces: { legacy: () => { calls++; }, connector: defaultProgram.namespaces.connector } },
    text => frames.push(JSON.parse(text)));
  conn.line('{"id":"init","method":"initialize","params":{"protocol":1}}');
  await conn.drain();
  for (const ns of ["connector", "legacy"]) {
    for (const token of ["01", "+1", "1.", ".1", "1e", "NaN", "Infinity", "-01", "--1"]) {
      conn.line(`{"id":"bad","method":"${ns}.get","params":{"args":{"topic":${token}}}}`);
    }
  }
  await conn.shutdown();
  assert.equal(calls, 0);
  assert.equal(frames.length, 1);
});
