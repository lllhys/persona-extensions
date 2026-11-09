import ts from "typescript";
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

// Extra local grammar controls; packaging also runs the official SDK authoring checker.
const root = fileURLToPath(new URL("../", import.meta.url));
const bundle = resolve(root, "build/v2ex.mjs");
const parse = (text, name) => ts.createSourceFile(name, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS).parseDiagnostics;
const check = path => spawnSync(process.execPath, ["--check", path], { encoding: "utf8", timeout: 15000 });
const text = await readFile(bundle, "utf8");
assert.equal(parse(text, "v2ex.mjs").length, 0, "TypeScript parser rejected bundle syntax");
const positive = check(bundle);
assert.equal(positive.error, undefined); assert.equal(positive.status, 0, "Node rejected bundle syntax");
const scratch = await mkdtemp(resolve(root, "build/syntax-control."));
try {
  const negative = resolve(scratch, "invalid.mjs"), broken = "const broken = ;\n";
  await writeFile(negative, broken);
  assert.ok(parse(broken, "invalid.mjs").length > 0, "parseDiagnostics must not accept malformed JavaScript");
  const rejected = check(negative);
  assert.equal(rejected.error, undefined); assert.notEqual(rejected.status, 0); assert.match(rejected.stderr, /SyntaxError/);
} finally { await rm(scratch, { recursive: true, force: true }); }
console.log("local-syntax: PASS (parseDiagnostics and Node --check positive/negative controls; not runtime confinement)");
