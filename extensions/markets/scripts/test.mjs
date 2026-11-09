import { build } from "esbuild";
import { spawnSync } from "node:child_process";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
const dir = fileURLToPath(new URL("../", import.meta.url));
const tsc = fileURLToPath(new URL("../node_modules/typescript/bin/tsc", import.meta.url));
const run = args => spawnSync(process.execPath, [tsc, ...args], { cwd: dir, encoding: "utf8", timeout: 60_000 });
for (const jsx of ["react-jsx", "react-jsxdev"]) {
  const valid = run(["-p", "tsconfig.json", "--jsx", jsx]);
  if (valid.error || valid.status !== 0) throw valid.error ?? new Error(valid.stdout + valid.stderr);
}
await writeFile(new URL("../tsconfig.invalid.json", import.meta.url), JSON.stringify({ extends: "./tsconfig.json", include: ["tests/invalid.tsx"], exclude: [] }));
try {
  for (const jsx of ["react-jsx", "react-jsxdev"]) {
    const invalid = run(["-p", "tsconfig.invalid.json", "--jsx", jsx]);
    if (invalid.error || invalid.status === 0 || !["invalid.tsx", "TS2322", "unsupported", "number", "key"].every(token => invalid.stdout.includes(token))) throw invalid.error ?? new Error("Negative native JSX validation failed: " + invalid.stdout + invalid.stderr);
  }
} finally { await rm(new URL("../tsconfig.invalid.json", import.meta.url)); }
console.log("typechecks: PASS valid and invalid JSX in production and development modes");
const source = spawnSync(process.execPath, ["scripts/build-source.mjs"], { cwd: dir, stdio: "inherit", timeout: 60_000 });
if (source.error || source.status !== 0) throw source.error ?? new Error("Source build failed");
await mkdir(new URL("../build/tests/", import.meta.url), { recursive: true });
const names = ["port", "program", "regressions", "round2", "round3", "lifecycle"];
const files = names.map(name => "tests/" + name + ".test.tsx");
await build({ absWorkingDir: dir, entryPoints: files, bundle: true, external: ["typescript"], platform: "node", format: "esm", target: "es2022", jsx: "automatic", jsxImportSource: "@persona/extension", outdir: "build/tests", outExtension: { ".js": ".mjs" } });
const result = spawnSync(process.execPath, [...(process.versions.bun ? ["test"] : ["--test", "--test-reporter=tap"]), ...names.map(name => "./build/tests/" + name + ".test.mjs")], { cwd: dir, stdio: "inherit", timeout: 90_000 });
if (result.error) throw result.error;
console.log("VERDICT source tests: " + (result.status === 0 ? "PASS" : "FAIL; inspect the test output"));
process.exitCode = result.status ?? 1;
