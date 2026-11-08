import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
const root = fileURLToPath(new URL("../", import.meta.url));
const go = process.env.LINUXDO_GO;
if (!go?.startsWith("/") || !process.env.GOROOT?.startsWith("/")) throw new Error("set absolute LINUXDO_GO and GOROOT for the installed Go 1.27.1 toolchain");
const version = spawnSync(go, ["version"], { encoding: "utf8", timeout: 10_000, env: { ...process.env, GOTOOLCHAIN: "local" } });
if (version.error || version.status !== 0 || !version.stdout.includes("go1.27.1")) throw new Error("original controls require installed Go 1.27.1");
const dir = await mkdtemp(resolve(root, "build/original-round4-"));
try {
  const overlay = resolve(dir, "overlay.json"), Replace = {};
  for (const round of [2, 3, 4]) Replace[resolve(root, `round${round}_overlay_test.go`)] = resolve(root, `tests/original-round${round}.go.txt`);
  await writeFile(overlay, JSON.stringify({ Replace }));
  const run = spawnSync(go, ["test", "-race", "-count=1", "-timeout=90s", "-overlay", overlay, "-run", "TestRound[234]OriginalControls", "-v", "."], {
    cwd: root, stdio: "inherit", timeout: 120_000,
    env: { ...process.env, TZ: "Asia/Tokyo", GOTOOLCHAIN: "local", GOPROXY: "off", GOSUMDB: "off" },
  });
  const timeout = run.error?.code === "ETIMEDOUT";
  console.log(`original Go differential controls: ${timeout ? "TIMEOUT" : run.status === 0 ? "PASS" : "FAIL"}`);
  process.exitCode = timeout ? 124 : run.status === 0 ? 0 : 1;
} finally { await rm(dir, { recursive: true, force: true }); }
