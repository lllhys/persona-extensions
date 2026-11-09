import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, writeFile, mkdir, mkdtemp, rm } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { spawnSync } from "node:child_process";
import { credentialFixtures } from "./credential-fixtures.ts";
const { checkLock } = await import(pathToFileURL(resolve("scripts/check-lock.mjs")).href);

const canonical = async () => JSON.parse(await readFile("package-lock.json", "utf8"));
async function withLock(change, check) {
  await mkdir("build", { recursive: true });
  const dir = await mkdtemp(resolve("build/lock-regression-"));
  try {
    const lock = await canonical(); change(lock);
    const path = resolve(dir, "package-lock.json"); await writeFile(path, JSON.stringify(lock));
    await check(path);
  } finally { await rm(dir, { recursive: true, force: true }); }
}
test("canonical and staged locks contain only public npm sources and the approved SDK link", async () => {
  const checked = await checkLock("package-lock.json");
  assert.deepEqual(checked, { packages: 37, public_resolved: 34, local_sdk_links: 1 });
  assert.deepEqual(await checkLock("build/package/package-lock.json"), checked);
  assert.deepEqual(JSON.parse(await readFile("build/package/package-lock.json", "utf8")), await canonical());
});
test("34 unapproved resolved sources reproduce the provenance failure without embedding a private URL", async () => {
  await withLock(lock => {
    let count = 0;
    for (const pkg of Object.values(lock.packages)) if (pkg.resolved?.startsWith("https:")) {
      const url = new URL(pkg.resolved); url.hostname = "registry.example.invalid"; pkg.resolved = url.href; count++;
    }
    assert.equal(count, 34);
  }, path => assert.rejects(checkLock(path), /unapproved lock source/));
});
test("the original three synthetic credential boundaries match the legacy Python fixture set", () => {
  const check = spawnSync("python3", ["-I", "-c", `
import ast, json, pathlib, sys
from urllib.parse import urlsplit
source = pathlib.Path(sys.argv[1]).read_text()
tree = ast.parse(source)
functions = [node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == "credential_fixtures"]
assert len(functions) == 1
# Execute only the trusted, pure fixture function, never the legacy verifier's
# build/extraction/HOME-substitution steps.
namespace = {"urlsplit": urlsplit}
exec(compile(ast.Module(body=functions, type_ignores=[]), "synthetic-fixture-only", "exec"), namespace)
values = json.load(sys.stdin)
assert namespace["credential_fixtures"]() == {values["topic"]: "tests/model.test.ts", values["archive"]: "tests/lock.test.mjs", values["query"]: "tests/lock.test.mjs"}
topic, archive, query = [urlsplit(values[key]) for key in ("topic", "archive", "query")]
assert (topic.scheme, topic.hostname, topic.username, topic.password, topic.path, topic.query) == ("https", "v2ex.com", "user", None, "/t/123", "")
assert (archive.scheme, archive.hostname, archive.username, archive.password, archive.path, archive.query) == ("https", "registry.npmjs.org", "fixture", "synthetic", "/example/-/example-1.0.0.tgz", "")
assert (query.scheme, query.hostname, query.username, query.password, query.path, query.query) == ("https", "registry.npmjs.org", None, None, "/example/-/example-1.0.0.tgz", "token=synthetic")
print("synthetic credential boundaries: PASS (same three original values)")
`, resolve("scripts/verify-round3.py")], { input: JSON.stringify(credentialFixtures), encoding: "utf8", timeout: 10000 });
  assert.equal(check.status, 0, check.stderr); assert.ok(check.stdout.includes("same three original values"));
});

test("lock provenance rejects URL userinfo, query credentials and unapproved file links", async () => {
  for (const resolved of [credentialFixtures.archive, credentialFixtures.query, "file:../../other-sdk"]) {
    await withLock(lock => { const pkg = Object.values(lock.packages).find(pkg => pkg.resolved?.startsWith("https:")); pkg.resolved = resolved; }, path => assert.rejects(checkLock(path), /unapproved lock source/));
  }
});
