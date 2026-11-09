import { build } from "esbuild";
import assert from "node:assert/strict";
import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";
const root = fileURLToPath(new URL("../", import.meta.url));
const built = await build({ absWorkingDir: root, entryPoints: ["src/index.ts"], bundle: true, write: false,
  format: "esm", platform: "neutral", target: "es2022", treeShaking: true, metafile: true,
  jsx: "automatic", jsxImportSource: "@persona/extension", legalComments: "none", outfile: "build/v2ex.mjs" });
assert.deepEqual(Buffer.from(built.outputFiles[0].contents), await readFile(resolve(root, "build/v2ex.mjs")));
assert.equal(Object.values(built.metafile.outputs).reduce((n, output) => n + output.imports.length, 0), 0);
// This is a synthetic stdio host harness, not part of the closed extension bundle.
const entry = resolve(root, "build/stdio-entry.ts");
await writeFile(entry, 'import { main } from "../../../sdk/ts/src/index.ts";\nimport program from "./v2ex.mjs";\nawait main(program);\n');
const input = '{invalid}\n' + JSON.stringify({ id: "init", method: "initialize", params: { protocol: 1 } }) + "\n" + JSON.stringify({ id: "manifest", method: "manifest" }) + "\n" + JSON.stringify({ id: "last", method: "ping" });
const run = spawnSync("bun", [entry], { cwd: root, input, encoding: "utf8", timeout: 30000 });
assert.equal(run.error, undefined); assert.equal(run.status, 0);
const frames = run.stdout.trim().split("\n").map(line => JSON.parse(line));
assert.equal(frames.length, 3, "official SDK ignores malformed frames without dispatching them");
assert.equal(frames.find(frame => frame.id === "init").result.id, "v2ex");
assert.equal(frames.find(frame => frame.id === "manifest").result.runtime.mode, "deno");
assert.equal(frames.find(frame => frame.id === "manifest").result.runtime.source.sha256, undefined);
assert.deepEqual(frames.find(frame => frame.id === "last").result, {});
console.log("closed-bundle: PASS (exact rebuild, 0 runtime imports, default Program through official SDK main; synthetic Bun stdio, not Deno hub)");
