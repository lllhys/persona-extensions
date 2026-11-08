import { build } from "esbuild";
import { spawnSync } from "node:child_process";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
const root = fileURLToPath(new URL("../", import.meta.url));
const tsc = fileURLToPath(new URL("../node_modules/typescript/bin/tsc", import.meta.url));
const run = args => spawnSync(process.execPath, [tsc, ...args], { cwd: root, encoding: "utf8", timeout: 60_000 });
for (const jsx of ["react-jsx", "react-jsxdev"]) {
  const valid = run(["-p", "tsconfig.json", "--jsx", jsx]);
  if (valid.error || valid.status !== 0) throw valid.error ?? new Error(valid.stdout + valid.stderr);
}
await writeFile(new URL("../tsconfig.invalid.json", import.meta.url), JSON.stringify({ extends: "./tsconfig.json", include: ["tests/invalid.tsx"], exclude: [] }));
try {
  for (const jsx of ["react-jsx", "react-jsxdev"]) {
    const invalid = run(["-p", "tsconfig.invalid.json", "--jsx", jsx]);
    if (invalid.error || invalid.status === 0 || !["invalid.tsx", "TS2322", "unsupported", "number", "key"].every(token => invalid.stdout.includes(token))) throw invalid.error ?? new Error("invalid JSX was not rejected as expected: " + invalid.stdout + invalid.stderr);
  }
} finally { await rm(new URL("../tsconfig.invalid.json", import.meta.url)); }
console.log("tsc: production/development JSX pass; invalid JSX rejected in both modes");
const sourceBuild = spawnSync(process.execPath, ["scripts/build-source.mjs"], { cwd: root, stdio: "inherit", timeout: 60_000 });
if (sourceBuild.error || sourceBuild.status !== 0) throw sourceBuild.error ?? new Error("source build failed");
const provenance = spawnSync(process.execPath, ["--test", "scripts/check-lock.test.mjs"], { cwd: root, stdio: "inherit", timeout: 30_000 });
if (provenance.error || provenance.status !== 0) throw provenance.error ?? new Error("public lock regressions failed");
await mkdir(new URL("../build/tests/", import.meta.url), { recursive: true });
await build({ absWorkingDir: root, entryPoints: ["tests/parity.test.ts", "tests/program.test.ts", "tests/round2.test.ts", "tests/round3.test.ts", "tests/compatibility.test.tsx", "tests/native-callbacks.test.tsx", "tests/network.test.ts"],
  bundle: true, platform: "node", format: "esm", target: "es2022", jsx: "automatic", jsxImportSource: "@persona/extension", outdir: "build/tests", outExtension: { ".js": ".mjs" } });
const files = ["build/tests/parity.test.mjs", "build/tests/program.test.mjs", "build/tests/round2.test.mjs", "build/tests/round3.test.mjs", "build/tests/compatibility.test.mjs", "build/tests/native-callbacks.test.mjs", "build/tests/network.test.mjs"];
const bun = process.argv.includes("--bun");
const result = spawnSync(bun ? "bun" : process.execPath, bun ? ["test", ...files] : ["--test", ...files], { cwd: root, stdio: "inherit", timeout: 90_000 });
if (result.error) throw result.error;
console.log(`sentry source tests: ${result.status === 0 ? "PASS" : "FAIL"} (${bun ? "Bun" : "Node"})`);
process.exitCode = result.status ?? 1;
