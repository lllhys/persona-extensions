import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { SentryService } from "../src/service.ts";
import { sample } from "./fixtures.ts";
/** Local synthetic TCP receipt, not a Deno broker or real-hub acceptance test. */
test("native fetch posts a self-hosted HTTP envelope only with explicit matching configuration", async () => {
  const receipts: { path: string; auth: string; type: string; body: string }[] = [];
  const server = createServer(async (request, response) => {
    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));
    receipts.push({ path: request.url!, auth: String(request.headers["x-sentry-auth"]), type: String(request.headers["content-type"]), body: Buffer.concat(chunks).toString() });
    response.end('{"id":"synthetic-receipt"}');
  });
  await new Promise<void>((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolve); });
  const origin = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  const dsn = origin.replace("http://", "http://fixture-key@") + "/prefix/42";
  const app = new SentryService({ fetch: (input, init) => globalThis.fetch(input, init), timeout: 1000 });
  try {
    assert.throws(() => app.configure({}, dsn));
    app.configure({ allow_http: true }, dsn);
    app.deliver([sample()]); assert.equal(await app.flush(2000), true); assert.equal(app.state().sent, 1);
    assert.equal(receipts.length, 1); assert.equal(receipts[0].path, "/prefix/api/42/envelope/");
    assert.equal(receipts[0].type, "application/x-sentry-envelope"); assert.ok(receipts[0].auth.includes("sentry_key=fixture-key"));
    const lines = receipts[0].body.trimEnd().split("\n"); assert.equal(lines.length, 3);
    assert.ok(!lines[0].includes("fixture-key")); assert.equal(JSON.parse(lines[1]).length, Buffer.byteLength(lines[2]));
  } finally {
    await app.stop(1000); server.closeAllConnections(); await new Promise<void>(resolve => server.close(() => resolve()));
  }
});
test("native fetch refuses redirects instead of disclosing the Sentry public key", async () => {
  const paths: string[] = [];
  const server = createServer((request, response) => {
    paths.push(request.url!);
    if (request.url === "/api/42/envelope/") { response.statusCode = 302; response.setHeader("Location", "/collect-key"); }
    response.end();
  });
  await new Promise<void>((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolve); });
  const origin = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  const app = new SentryService({ fetch: (input, init) => globalThis.fetch(input, init), timeout: 1000 });
  try {
    app.configure({ allow_http: true }, origin.replace("http://", "http://fixture-key@") + "/42");
    app.deliver([sample()]); assert.equal(await app.flush(2000), true);
    assert.equal(app.state().dropped, 1); assert.equal(app.state().sent, 0); assert.deepEqual(paths, ["/api/42/envelope/"]);
  } finally {
    await app.stop(1000); server.closeAllConnections(); await new Promise<void>(resolve => server.close(() => resolve()));
  }
});
