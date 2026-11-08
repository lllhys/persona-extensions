import { build } from "esbuild";
import { spawnSync } from "node:child_process";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
const dir = fileURLToPath(new URL("../", import.meta.url)), tsc = fileURLToPath(new URL("../node_modules/typescript/bin/tsc", import.meta.url));
const run = (command, args, timeout = 60_000) => { const r = spawnSync(command, args, { cwd: dir, encoding: "utf8", timeout }); if (r.error) throw r.error; return r; };
for (const jsx of ["react-jsx", "react-jsxdev"]) { const valid = run(process.execPath, [tsc, "-p", "tsconfig.json", "--jsx", jsx]); if (valid.status !== 0) throw new Error(valid.stdout + valid.stderr); }
await writeFile(new URL("../tsconfig.invalid.json", import.meta.url), JSON.stringify({ extends: "./tsconfig.json", include: ["tests/invalid.tsx"], exclude: [] }));
try { for (const jsx of ["react-jsx", "react-jsxdev"]) { const invalid = run(process.execPath, [tsc, "-p", "tsconfig.invalid.json", "--jsx", jsx]); if (invalid.status === 0 || !["invalid.tsx", "TS2322"].every(token => invalid.stdout.includes(token))) throw new Error("Invalid native JSX was not rejected: " + invalid.stdout + invalid.stderr); } } finally { await rm(new URL("../tsconfig.invalid.json", import.meta.url)); }
console.log("PASS: valid JSX accepted and invalid JSX rejected in both production and development modes");
const packageBuild = run(process.execPath, ["scripts/build-source.mjs"]); if (packageBuild.status !== 0) throw new Error(packageBuild.stdout + packageBuild.stderr); console.log(packageBuild.stdout.trim());
await mkdir(new URL("../.test-dist/", import.meta.url), { recursive: true });
const suites = ["parity", "program", "boundaries", "regressions", "round2", "round3", "exact"];
await build({ absWorkingDir: dir, entryPoints: suites.map(name => `tests/${name}.test.tsx`), bundle: true, platform: "node", format: "esm", target: "es2022", jsx: "automatic", jsxImportSource: "@persona/extension", outdir: ".test-dist", outExtension: { ".js": ".mjs" } });
const bun = process.argv.includes("--bun"), result = run(bun ? "bun" : process.execPath, [bun ? "test" : "--test", ...suites.map(name => `./.test-dist/${name}.test.mjs`)], 90_000);
console.log(result.stdout); if (result.stderr) console.error(result.stderr);
console.log(result.status === 0 ? "PASS: source tests" : "FAIL: source tests"); process.exitCode = result.status ?? 1;
