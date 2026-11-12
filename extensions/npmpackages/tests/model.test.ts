import { test } from "node:test";
import assert from "node:assert/strict";
import { downloadsPath, packageName, packagePath, parseDownloads, parsePackage, parseSearch, queryText, ReadError, searchPath } from "../src/model.ts";
import { encodeState, initialState, packageTransition, readState, transition } from "../src/page.tsx";
import { scoped, typescript } from "./fixtures.ts";

test("registry requests bound search input and encode scoped package names", () => {
  const url = new URL("https://registry.npmjs.org" + searchPath("typescript language", 12));
  assert.equal(url.searchParams.get("text"), "typescript language");
  assert.equal(url.searchParams.get("size"), "12");
  assert.equal(packagePath("@babel/preset-typescript"), "/@babel%2Fpreset-typescript/latest");
  assert.equal(downloadsPath("@babel/preset-typescript"), "/downloads/point/last-week/%40babel%2Fpreset-typescript");
  for (const name of ["", "@scope", "@scope/", "a/b/c", "a b", "../secret", "a\n", 42]) assert.throws(() => packageName(name), ReadError);
  // Legacy uppercase names are still served by the registry.
  for (const name of ["JSONStream", "Base64", "@Scope/Pkg", "a.B-C_d"]) assert.equal(packageName(name), name);
  for (const value of ["", "x\n", 42, "x".repeat(201)]) assert.throws(() => queryText(value), ReadError);
});

test("search keeps bounded metadata and rejects duplicate or malicious packages", () => {
  const result = parseSearch({ objects: [{ package: typescript, downloads: { weekly: 42, monthly: 84 } }], total: 1 }, 123);
  assert.equal(result.packages[0].name, "typescript");
  assert.equal(result.packages[0].repository, "https://github.com/microsoft/TypeScript");
  assert.equal(result.packages[0].weeklyDownloads, 42);
  for (const change of [
    { links: { npm: "https://attacker.invalid/package/typescript" } },
    { links: { npm: "https://www.npmjs.com.evil.invalid/package/typescript" } },
    { name: "../secret" },
    { version: "1.0.0\n" },
    { license: { type: ["MIT"] } }
  ]) assert.throws(() => parseSearch({ objects: [{ package: { ...typescript, ...change } }], total: 1 }, 0), ReadError);
  assert.throws(() => parseSearch({ objects: [{ package: typescript }, { package: typescript }], total: 2 }, 0), ReadError);
});

test("a git+ssh, git or scp-style repository is shown as https, and an unreadable link is left out, not a failed search", () => {
  // As @socket.io/redis-adapter links it in GET /-/v1/search?text=socket.io.
  const links = (repository: unknown, homepage?: unknown) => ({ ...typescript, repository, homepage, links: { npm: typescript.links.npm } });
  const result = parseSearch({ objects: [
    { package: { ...links("git+ssh://git@github.com/socketio/socket.io-redis-adapter.git", "http://socket.io/"), name: "a" } },
    { package: { ...links("ssh://git@gitlab.com/group/project.git"), name: "b" } },
    { package: { ...links("git://github.com/owner/old.git"), name: "c" } },
    { package: { ...links("git@github.com:owner/scp.git"), name: "d" } },
    { package: { ...links({ url: "javascript:alert(1)" }, "javascript:alert(1)"), name: "e" } },
  ], total: 5 }, 0);
  assert.deepEqual(result.packages.map(pkg => [pkg.name, pkg.repository, pkg.homepage]), [
    ["a", "https://github.com/socketio/socket.io-redis-adapter", undefined],
    ["b", "https://gitlab.com/group/project", undefined],
    ["c", "https://github.com/owner/old", undefined],
    ["d", "https://github.com/owner/scp", undefined],
    ["e", undefined, undefined],
  ]);
});

test("latest package metadata produces a precise install command without executing anything", () => {
  const result = parsePackage(typescript, 0);
  assert.equal(result.install, "npm install typescript@7.0.2");
  assert.equal(result.dependencies, 1);
  assert.equal(result.engines, "node >=16.20.0");
  assert.equal(result.types, true);
  assert.equal(result.unpackedSize, 2497498);
  const scopedResult = parsePackage(scoped, 0);
  assert.equal(scopedResult.install, "npm install @babel/preset-typescript@8.0.7");
  assert.equal(scopedResult.url, "https://www.npmjs.com/package/@babel/preset-typescript");
});

test("download responses are scoped to the exact requested package", () => {
  assert.deepEqual(parseDownloads({ downloads: 12, start: "2026-11-03", end: "2026-11-09", package: "typescript" }, "typescript"), { count: 12, start: "2026-11-03", end: "2026-11-09" });
  for (const change of [{ package: "other" }, { downloads: -1 }, { downloads: 1.5 }, { start: "yesterday" }]) assert.throws(() => parseDownloads({ downloads: 12, start: "2026-11-03", end: "2026-11-09", package: "typescript", ...change }, "typescript"), ReadError);
});

test("page state accepts only validated results and canonical package URLs", () => {
  const state = { q: "typescript", found: true };
  const result = parseSearch({ objects: [{ package: scoped }], total: 1 }, 0);
  const detail = packageTransition(state, result, "package:@babel/preset-typescript");
  assert.equal(detail.package?.name, "@babel/preset-typescript");
  assert.equal(packageTransition(state, result, "package:other"), state);
  assert.equal(transition(state, "package:typescript"), state);
  assert.deepEqual(readState(encodeState(detail)), detail);
  assert.deepEqual(readState(encodeState({ ...detail, package: { ...detail.package!, url: "https://attacker.invalid/package/@babel/preset-typescript" } })), initialState);
  assert.deepEqual(readState(encodeState({ ...detail, package: { ...detail.package!, repository: "javascript:alert(1)" } })), initialState);
  assert.equal(transition(state, "search", { query: "typescript" }).found, true);
});
