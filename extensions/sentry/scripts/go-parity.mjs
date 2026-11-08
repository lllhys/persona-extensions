import { build } from "esbuild";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";
const root = fileURLToPath(new URL("../", import.meta.url)), output = resolve(root, "build/tests/go-parity.mjs");
await build({ absWorkingDir: root, entryPoints: ["tests/go-parity.ts"], bundle: true, platform: "node", format: "esm", target: "es2022", outfile: output });
const result = spawnSync(process.execPath, [output, process.argv[2]], { cwd: root, stdio: "inherit", timeout: 60_000 });
console.log(`Sentry Go differential runner: ${result.error?.code === "ETIMEDOUT" ? "TIMEOUT" : result.status === 0 ? "PASS" : "FAIL"}`);
process.exitCode = result.error?.code === "ETIMEDOUT" ? 124 : result.status ?? 1;
