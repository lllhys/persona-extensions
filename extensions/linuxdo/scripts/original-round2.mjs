import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
const root = fileURLToPath(new URL("../", import.meta.url));
const dir = await mkdtemp(resolve(root, "build/original-round2-"));
try {
  const overlay = resolve(dir, "overlay.json");
  await writeFile(overlay, JSON.stringify({ Replace: { [resolve(root, "round2_overlay_test.go")]: resolve(root, "tests/original-round2.go.txt") } }));
  const run = spawnSync("go", ["test", "-race", "-count=1", "-timeout=90s", "-overlay", overlay, "-run", "TestRound2OriginalControls", "-v", "."], { cwd: root, stdio: "inherit", timeout: 120_000, env: { ...process.env, TZ: "Asia/Tokyo", GOTOOLCHAIN: "local", GOPROXY: "off", GOSUMDB: "off" } });
  const timeout = run.error?.code === "ETIMEDOUT";
  console.log(`original Go controls: ${timeout ? "TIMEOUT" : run.status === 0 ? "PASS" : "FAIL"}`);
  process.exitCode = timeout ? 124 : run.status === 0 ? 0 : 1;
} finally { await rm(dir, { recursive: true, force: true }); }
