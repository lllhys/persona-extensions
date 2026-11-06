import { build } from "esbuild";
import { spawnSync } from "node:child_process";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
const dir = fileURLToPath(new URL("../", import.meta.url));
const tsc = fileURLToPath(new URL("../node_modules/typescript/bin/tsc", import.meta.url));
const run = (args) => spawnSync(process.execPath, [tsc, ...args], { cwd: dir, encoding: "utf8", timeout: 60_000 });
for (const jsx of ["react-jsx", "react-jsxdev"]) {
  const valid = run(["-p", "tsconfig.json", "--jsx", jsx]);
  if (valid.status !== 0) throw new Error(valid.stdout + valid.stderr);
}
await writeFile(new URL("../tsconfig.invalid.json", import.meta.url), JSON.stringify({ extends: "./tsconfig.json", include: ["tests/invalid.tsx"], exclude: [] }));
try {
  for (const jsx of ["react-jsx", "react-jsxdev"]) {
    const invalid = run(["-p", "tsconfig.invalid.json", "--jsx", jsx]);
    if (invalid.status === 0 || !["invalid.tsx", "TS2322", "unsupported", "number", "key"].every(token => invalid.stdout.includes(token))) throw new Error("tsc did not reject invalid native JSX: " + invalid.stdout + invalid.stderr);
  }
} finally { await rm(new URL("../tsconfig.invalid.json", import.meta.url)); }
console.log("tsc: valid JSX accepted; invalid JSX rejected");
await mkdir(new URL("../build/tests/", import.meta.url), { recursive: true });
await build({ absWorkingDir: dir, entryPoints: ["tests/port.test.tsx"], bundle: true, platform: "node", format: "esm", target: "es2022",
  jsx: "automatic", jsxImportSource: "@persona/extension", outfile: "build/tests/port.test.mjs" });
const bun = process.argv.includes("--bun");
const result = spawnSync(bun ? "bun" : process.execPath, bun ? ["test", "build/tests/port.test.mjs"] : ["--test", "build/tests/port.test.mjs"], { cwd: dir, stdio: "inherit", timeout: 90_000 });
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
