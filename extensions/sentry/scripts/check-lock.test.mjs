import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { verifyPublicLock } from "./public-lock.mjs";

const lockURL = new URL("../package-lock.json", import.meta.url);
const archivedURL = new URL("../build/package/extensions/sentry/package-lock.json", import.meta.url);

test("author and archived locks use public npm and the approved relative SDK", async () => {
  for (const path of [lockURL, archivedURL]) {
    const result = await verifyPublicLock(path, "../../sdk/ts");
    assert.deepEqual({ remote: result.publicResolved, links: result.localSDKLinks }, { remote: 34, links: 1 });
  }
  assert.equal(await readFile(lockURL, "utf8"), await readFile(archivedURL, "utf8"));
});

test("unapproved origins, links, credentials and metadata fail closed", async () => {
  const lock = JSON.parse(await readFile(lockURL, "utf8"));
  for (const source of ["https://registry.example.com/package.tgz", "http://registry.npmjs.org/package.tgz",
    "https://registry.npmjs.org:444/package.tgz", "https://<user>:<password>@registry.npmjs.org/package.tgz",
    "https://registry.npmjs.org/package.tgz?token=fixture-token"]) {
    const polluted = structuredClone(lock);
    polluted.packages["node_modules/typescript"].resolved = source;
    await assert.rejects(() => verifyPublicLockObject(polluted));
    assert.throws(() => verifyPublicLockObject(polluted));
  }
  for (const source of ["../../other-sdk", "https://registry.npmjs.org/other.tgz"]) {
    const polluted = structuredClone(lock);
    polluted.packages["node_modules/@persona/extension"].resolved = source;
    assert.throws(() => verifyPublicLockObject(polluted));
  }
  const polluted = structuredClone(lock);
  polluted.packages[""]._authToken = "fixture-token";
  assert.throws(() => verifyPublicLockObject(polluted));
});

// Run the same production validator without creating an on-disk mutated lock.
async function verifyPublicLockObject(lock) {
  const { mkdtemp, writeFile, rm } = await import("node:fs/promises");
  const { tmpdir } = await import("node:os");
  const { join } = await import("node:path");
  const dir = await mkdtemp(join(tmpdir(), "sentry-public-lock-"));
  try {
    await writeFile(join(dir, "lock.json"), JSON.stringify(lock));
    await verifyPublicLock(join(dir, "lock.json"), "../../sdk/ts");
  } finally { await rm(dir, { recursive: true, force: true }); }
}
