import { build } from "esbuild";
import { spawnSync } from "node:child_process";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
const dir = fileURLToPath(new URL("../", import.meta.url));
const tsc = fileURLToPath(new URL("../node_modules/typescript/bin/tsc", import.meta.url));
const run = args => spawnSync(process.execPath, [tsc, ...args], { cwd: dir, encoding: "utf8", timeout: 60_000 });
for (const jsx of ["react-jsx", "react-jsxdev"]) {
  const result = run(["-p", "tsconfig.json", "--jsx", jsx]);
  if (result.error || result.status !== 0) throw result.error ?? new Error(result.stdout + result.stderr);
}
await writeFile(new URL("../tsconfig.invalid.json", import.meta.url), JSON.stringify({ extends: "./tsconfig.json", include: ["tests/invalid.tsx"], exclude: [] }));
try {
  for (const jsx of ["react-jsx", "react-jsxdev"]) {
    const result = run(["-p", "tsconfig.invalid.json", "--jsx", jsx]);
    if (result.error || result.status === 0 || (result.stdout.match(/error TS\d+:/g) ?? []).length !== 3 || !["invalid.tsx", "TS2322", "unsupported", "number", "key"].every(token => result.stdout.includes(token))) throw result.error ?? new Error("negative JSX contract failed: " + result.stdout + result.stderr);
  }
} finally { await rm(new URL("../tsconfig.invalid.json", import.meta.url)); }
console.log("types: PASS (positive and negative, react-jsx and react-jsxdev)");
const source = spawnSync(process.execPath, ["scripts/build-source.mjs"], { cwd: dir, stdio: "inherit", timeout: 60_000 });
if (source.error || source.status !== 0) throw source.error ?? new Error("source build failed");
const syntax = spawnSync(process.execPath, ["scripts/check-syntax.mjs"], { cwd: dir, stdio: "inherit", timeout: 60_000 });
if (syntax.error || syntax.status !== 0) throw syntax.error ?? new Error("local syntax controls failed");
await mkdir(new URL("../build/tests/", import.meta.url), { recursive: true });
const entries = ["tests/model.test.ts", "tests/service.test.ts", "tests/page.test.tsx", "tests/program.test.tsx", "tests/regression.test.tsx", "tests/round2.test.tsx", "tests/round3.test.tsx", "tests/scoped.test.ts", "tests/authoring.test.ts", "tests/lock.test.mjs"];
await build({ absWorkingDir: dir, entryPoints: entries, bundle: true, platform: "node", format: "esm", target: "es2022",
  jsx: "automatic", jsxImportSource: "@persona/extension", outdir: "build/tests", outExtension: { ".js": ".mjs" } });
const result = spawnSync(process.execPath, ["--test", "--test-reporter=tap", ...entries.map(path => path.replace("tests/", "build/tests/").replace(/\.tsx?$/, ".mjs"))], { cwd: dir, encoding: "utf8", timeout: 90_000 });
process.stdout.write(result.stdout ?? ""); process.stderr.write(result.stderr ?? "");
if (result.error) { console.log("tests: FAIL (timeout or process error)"); throw result.error; }
const blocked = /not ok[^\n]*# TODO/.test(result.stdout ?? "");
const verdict = result.status !== 0 ? "FAIL" : blocked ? "BLOCKED" : "PASS";
console.log(`tests: ${verdict} exit=${result.status ?? 1}; failing TODOs are not acceptance`);
process.exitCode = result.status || (blocked ? 2 : 0);
