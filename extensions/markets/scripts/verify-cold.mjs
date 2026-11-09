import assert from "node:assert/strict";
import { readFile, writeFile, mkdtemp, mkdir } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
const root = fileURLToPath(new URL("../", import.meta.url));
const lab = await mkdtemp(resolve(root, "build/round3-cold-"));
const extracted = resolve(lab, "extracted"), home = resolve(lab, "home"), cache = resolve(lab, "npm-cache");
for (const dir of [extracted, home, cache]) await mkdir(dir);
const userconfig = resolve(lab, "user.npmrc"), globalconfig = resolve(lab, "global.npmrc");
await writeFile(userconfig, ""); await writeFile(globalconfig, "");
const env = { PATH: dirname(process.execPath) + ":" + process.env.PATH, HOME: home, LANG: "C.UTF-8", LC_ALL: "C.UTF-8", TZ: "UTC" };
const run = async (name, executable, args, cwd, timeout) => {
  const result = spawnSync(executable, args, { cwd, env, encoding: "utf8", timeout, maxBuffer: 16 * 1024 * 1024 });
  await writeFile(resolve(lab, name + ".log"), result.stdout + result.stderr);
  if (result.error) throw result.error;
  return result;
};
try {
  const extractor = `import pathlib,sys,tarfile
archive,target=sys.argv[1:]
with tarfile.open(archive,"r:gz") as package:
 for member in package.getmembers():
  path=pathlib.PurePosixPath(member.name)
  if path.is_absolute() or ".." in path.parts or "\\\\" in member.name or not (member.isfile() or member.isdir()): raise RuntimeError("Unsafe archive member")
  output=pathlib.Path(target)/member.name
  if member.isdir(): output.mkdir(parents=True,exist_ok=True)
  else:
   output.parent.mkdir(parents=True,exist_ok=True)
   output.write_bytes(package.extractfile(member).read())
`;
  assert.equal((await run("extract", "python3", ["-I", "-c", extractor, resolve(root, "build/markets-source.tar.gz"), extracted], root, 30_000)).status, 0);
  const authoring = resolve(extracted, "extensions/markets");
  const install = await run("install", "npm", ["ci", "--userconfig", userconfig, "--globalconfig", globalconfig, "--cache", cache, "--registry=https://registry.npmjs.org", "--ignore-scripts", "--no-audit", "--no-fund"], authoring, 120_000);
  assert.equal(install.status, 0, "Cold public-registry install failed; inspect local log");
  const rebuilt = await run("source-tests", process.execPath, ["scripts/test.mjs"], authoring, 180_000);
  const output = rebuilt.stdout;
  assert.ok(output.includes("typechecks: PASS"));
  const failures = output.split("\n").filter(line => /^not ok \d+ - /.test(line));
  assert.equal(rebuilt.status, 1); assert.equal(failures.length, 4); assert.ok(failures.every(line => line.includes("BLOCKED")));
  assert.match(output, /# skipped 0/); assert.match(output, /# todo 0/);
  const hash = bytes => createHash("sha256").update(bytes).digest("hex");
  for (const name of ["markets.mjs", "markets-source.tar.gz"]) {
    const cold = await readFile(resolve(authoring, "build", name)), canonical = await readFile(resolve(root, "build", name));
    assert.equal(hash(cold), hash(canonical), "Cold/canonical artifact hash mismatch");
    assert.ok(cold.equals(canonical), "Cold/canonical artifact bytes differ");
  }
  const paths = { extracted, authoring, lab, source_tests: "FAIL/BLOCKED", test_exit: rebuilt.status, tests: Number(/# tests (\d+)/.exec(output)?.[1]), pass: Number(/# pass (\d+)/.exec(output)?.[1]), fail: failures.length, standalone_rebuild: "PASS", byte_identical_bundle_archive: true };
  await writeFile(resolve(root, "build/round3-cold-paths.json"), JSON.stringify(paths));
  console.log(JSON.stringify(paths));
  console.log("VERDICT cold authoring rebuild: PASS; full suite: FAIL/BLOCKED (four genuine upstream assertions)");
  process.exitCode = 1;
} catch (error) {
  console.log("VERDICT cold authoring rebuild: " + (error.code === "ETIMEDOUT" ? "TIMEOUT" : "FAIL"));
  throw error;
}
