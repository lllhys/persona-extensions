import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { checkLock, checkLockText } from "./check-lock.mjs";
const lockURL = new URL("../package-lock.json", import.meta.url);
const archivedURL = new URL("../.source-dist/sentry/package-lock.json", import.meta.url);

test("author and archived lock use only public npm and the approved relative SDK", async () => {
  for (const path of [lockURL, archivedURL]) assert.deepEqual(await checkLock(path), { remote: 34, links: 1 });
  assert.equal(await readFile(lockURL, "utf8"), await readFile(archivedURL, "utf8"));
});
test("unapproved resolved provenance is rejected even without a known-private term", async () => {
  const lock = JSON.parse(await readFile(lockURL, "utf8"));
  for (const source of ["https://registry.example.com/package.tgz", "http://registry.npmjs.org/package.tgz", "file:../../other-sdk", "https://registry.npmjs.org:444/package.tgz"]) {
    const polluted = structuredClone(lock);
    polluted.packages["node_modules/typescript"].resolved = source;
    assert.throws(() => checkLockText(JSON.stringify(polluted)));
  }
  const polluted = structuredClone(lock);
  polluted.packages["node_modules/@persona/extension"].resolved = "../../other-sdk";
  assert.throws(() => checkLockText(JSON.stringify(polluted)));
});
test("userinfo, credential query and credential metadata cannot enter an archive lock", async () => {
  const lock = JSON.parse(await readFile(lockURL, "utf8"));
  for (const source of ["https://<user>:<password>@registry.npmjs.org/package.tgz", "https://registry.npmjs.org/package.tgz?token=fixture-token"]) {
    const polluted = structuredClone(lock);
    polluted.packages["node_modules/typescript"].resolved = source;
    assert.throws(() => checkLockText(JSON.stringify(polluted)));
  }
  const polluted = structuredClone(lock);
  polluted.packages[""]._authToken = "fixture-token";
  assert.throws(() => checkLockText(JSON.stringify(polluted)));
});
