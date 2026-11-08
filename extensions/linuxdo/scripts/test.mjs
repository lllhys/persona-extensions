import { build } from "esbuild";
import { spawnSync } from "node:child_process";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
const dir = fileURLToPath(new URL("../", import.meta.url));
const tsc = fileURLToPath(new URL("../node_modules/typescript/bin/tsc", import.meta.url));
const run = args => spawnSync(process.execPath, [tsc, ...args], { cwd: dir, encoding: "utf8", timeout: 60_000 });
try {
  for (const jsx of ["react-jsx", "react-jsxdev"]) {
    const valid = run(["-p", "tsconfig.json", "--jsx", jsx]);
    if (valid.error) throw valid.error;
    if (valid.status !== 0) throw new Error(valid.stdout + valid.stderr);
  }
  await writeFile(new URL("../tsconfig.invalid.json", import.meta.url), JSON.stringify({ extends: "./tsconfig.json", include: ["tests/invalid.tsx"], exclude: [] }));
  try {
    for (const jsx of ["react-jsx", "react-jsxdev"]) {
      const invalid = run(["-p", "tsconfig.invalid.json", "--jsx", jsx]);
      if (invalid.error) throw invalid.error;
      if (invalid.status === 0 || !["invalid.tsx", "TS2322", "unsupported", "number", "key"].every(token => invalid.stdout.includes(token))) throw new Error("tsc did not reject invalid native JSX: " + invalid.stdout + invalid.stderr);
    }
  } finally { await rm(new URL("../tsconfig.invalid.json", import.meta.url), { force: true }); }
  console.log("JSX positive/negative types: PASS (production and development)");
  await mkdir(new URL("../build/tests/", import.meta.url), { recursive: true });
  const sourceBuild = spawnSync(process.execPath, ["scripts/build.mjs"], { cwd: dir, stdio: "inherit", timeout: 60_000 });
  if (sourceBuild.error || sourceBuild.status !== 0) throw sourceBuild.error ?? new Error("source build failed");
  const names = ["port", "program", "round2", "round3", "round4"];
  await build({ absWorkingDir: dir, entryPoints: names.map(name => `tests/${name}.test.tsx`), bundle: true, platform: "node", format: "esm", target: "es2022", jsx: "automatic", jsxImportSource: "@persona/extension", outdir: "build/tests", outExtension: { ".js": ".mjs" } });
  const result = spawnSync(process.execPath, [process.versions.bun ? "test" : "--test", ...names.map(name => `./build/tests/${name}.test.mjs`)], { cwd: dir, stdio: "inherit", timeout: 90_000 });
  if (result.error || result.status !== 0) throw result.error ?? new Error(`tests exited ${result.status}`);
  console.log("source tests: PASS");
} catch (error) {
  console.error(error);
  const timedOut = error?.code === "ETIMEDOUT";
  console.log(`source tests: ${timedOut ? "TIMEOUT" : "FAIL"}`);
  process.exitCode = timedOut ? 124 : 1;
}
