import { test } from "node:test";
import assert from "node:assert/strict";
import { connect } from "@persona/extension";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { Fixture, sample, syntheticDSN } from "./fixtures.ts";

// Genuine SDK notification/initialization counterexamples, not regression skips.
// The DSN builder assertion is separate from trusted fetch wire-path parity.
test("DSN path parity: Go preserves dot segments rather than normalizing the destination", async () => {
  const imported = await import(pathToFileURL(resolve("build/package/extensions/sentry/build/sentry.mjs")).href);
  assert.equal(imported.parseDSN("https://<key>@errors.example.com/prefix/../42").endpoint, "https://errors.example.com/prefix/../api/42/envelope/");
});
for (const field of ["count", "line", "column"]) for (const token of ["1.0", "1e0", "-1.0", "-1e0", "9007199254740993", "9223372036854775807", "-9223372036854775808"]) {
  test(`SDK notification parity: ${field}:${token}`, async () => {
    const imported = await import(pathToFileURL(resolve("build/package/extensions/sentry/build/sentry.mjs")).href);
    const fixture = new Fixture(), runtime = imported.createRuntime({ fetch: fixture.fetch }), conn = connect(runtime.program, () => {});
    try {
      conn.line(JSON.stringify({ id: "init", method: "initialize", params: { protocol: 1, config: {}, secrets: { dsn: syntheticDSN } } })); await conn.drain();
      const payload = field === "count" ? JSON.stringify(sample()).replace('"count":3', `"count":${token}`)
        : JSON.stringify({ ...sample(), exceptions: [{ type: "SyntheticError", frames: [{ [field]: 1 }] }] }).replace(`"${field}":1`, `"${field}":${token}`);
      conn.line(`{"event":"error_reports.deliver","params":{"reports":[${payload}]}}`); await conn.drain(); await runtime.service.flush(1000);
      if (/\.|e/.test(token)) assert.equal(fixture.hits.length, 0, "Go rejects fractional/exponent spellings, including mathematical integers");
      else {
        assert.equal(fixture.hits.length, 1, "Go accepts the entire legal signed int64 range");
        const name = field === "count" ? "count" : field === "line" ? "lineno" : "colno";
        if (field === "count" || BigInt(token) > 0n) assert.ok(fixture.hits[0].body.includes(`"${name}":${token}`));
      }
    } finally { await runtime.service.stop(1000); await conn.shutdown(); }
  });
}
test("SDK initialization parity: invalid earlier config duplicate is not erased", async () => {
  const imported = await import(pathToFileURL(resolve("build/package/extensions/sentry/build/sentry.mjs")).href);
  const runtime = imported.createRuntime({ fetch: new Fixture().fetch }), frames: any[] = [], conn = connect(runtime.program, line => frames.push(JSON.parse(line)));
  try {
    conn.line(`{"id":"init","method":"initialize","params":{"protocol":1,"config":{"include_log_lines":"bad","include_log_lines":true},"secrets":{"dsn":${JSON.stringify(syntheticDSN)}}}}`); await conn.drain();
    assert.equal(frames[0].error?.code, "invalid_params", "Go validates every config occurrence; SDK init receives only JSON.parse's final value");
  } finally { await runtime.service.stop(1000); await conn.shutdown(); }
});
