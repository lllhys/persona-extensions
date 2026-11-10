import { build } from "esbuild";
import { mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";
const root = fileURLToPath(new URL("../", import.meta.url));
await mkdir(resolve(root, "build/blockers"), { recursive: true });
await build({ absWorkingDir: root, entryPoints: ["tests/upstream-blockers.test.ts"],
  bundle: true, platform: "node", format: "esm", target: "es2022", outdir: "build/blockers", outExtension: { ".js": ".mjs" } });
const bun = process.argv.includes("--bun");
const result = spawnSync(bun ? "bun" : process.execPath, bun ? ["test", "build/blockers/upstream-blockers.test.mjs"] : ["--test", "build/blockers/upstream-blockers.test.mjs"], { cwd: root, stdio: "inherit", timeout: 90_000 });
if (result.error) {
  console.error(`Sentry acceptance counterexamples: ${result.error.code === "ETIMEDOUT" ? "TIMEOUT" : "FAIL"}`);
  process.exitCode = result.error.code === "ETIMEDOUT" ? 124 : 1;
} else {
  console.log(`Sentry acceptance counterexamples: ${result.status === 0 ? "PASS" : "FAIL/BLOCKED"} (${bun ? "Bun" : "Node"}, exit ${result.status}; no skips)`);
  process.exitCode = result.status ?? 1;
}
