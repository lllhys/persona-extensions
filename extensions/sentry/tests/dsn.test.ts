import { test } from "node:test";
import assert from "node:assert/strict";
import { parseDSN } from "../src/dsn.ts";
import { send } from "../src/transport.ts";

const origin = "https://errors.example.com";
test("DSN builder preserves Go decoded dot paths, repeated slashes and escaped separators", () => {
  for (const [input, path] of [
    ["/prefix/../42", "/prefix/.."],
    ["/prefix/./42", "/prefix/."],
    ["/prefix/%2e%2E/42", "/prefix/.."],
    ["/prefix//../42", "/prefix//.."],
    ["/../42", "/.."],
    ["/prefix/%2f../42", "/prefix//.."],
  ]) {
    const dsn = parseDSN(`https://<key>@errors.example.com${input}`);
    assert.equal(dsn.path, path);
    assert.equal(dsn.project, "42");
    assert.equal(dsn.safe, `${origin}${path}/42`);
    assert.equal(dsn.endpoint, `${origin}${path}/api/42/envelope/`);
    assert.ok(!dsn.safe.includes("<key>"));
    assert.ok(!dsn.endpoint.includes("<key>"));
  }
});
test("DSN builder ignores query and fragment and removes only the final slash", () => {
  const dsn = parseDSN(" https://<key>@errors.example.com/prefix/../42/?ignored=/elsewhere#fragment/3 ");
  assert.equal(dsn.path, "/prefix/..");
  assert.equal(dsn.safe, `${origin}/prefix/../42`);
  assert.equal(dsn.endpoint, `${origin}/prefix/../api/42/envelope/`);
  assert.throws(() => parseDSN("https://<key>@errors.example.com/prefix/../42//"));
});
test("transport passes the raw Go endpoint to the adapter; trusted Request canonicalizes it", async () => {
  const dsn = parseDSN("https://<key>@errors.example.com/prefix/../42");
  let adapterInput: string | undefined, requestURL: string | undefined;
  const result = await send(async (input, init) => {
    adapterInput = input;
    requestURL = new Request(input, init).url;
    return new Response(null, { status: 200 });
  }, dsn, "persona-sentry/0.1.2", new Uint8Array(), 0, new AbortController().signal, 1000);
  assert.deepEqual(result, { status: "sent" });
  assert.equal(adapterInput, `${origin}/prefix/../api/42/envelope/`);
  // This documents the host-facing limitation, not a wire-parity pass or receipt.
  assert.equal(requestURL, `${origin}/api/42/envelope/`);
  assert.equal(new URL(dsn.endpoint).pathname, "/api/42/envelope/");
});
