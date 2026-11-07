import { spawnSync } from "node:child_process";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { resolve } from "node:path";

// Real compiler diagnostics, not expect-error assertions that can accidentally suppress unrelated errors.
const dir = mkdtempSync(resolve(".negative-"));
try {
  const cases = [
    ['<Text key="x" html="not supported" />', "html"],
    ['<Text text="missing key" />', "key"],
    ['<Chart key="x" kind={42} series={[]} />', "number"],
    ['<Button key="x" title="No client JS" action={() => {}} />', "CallbackAction"],
    ['<div />', "div"],
  ];
  for (const [jsx, diagnostic] of cases) {
    writeFileSync(resolve(dir, "case.tsx"), `import { Text, Chart, Button } from "@persona/extension/ui";\nexport const invalid = ${jsx};\n`);
    writeFileSync(resolve(dir, "tsconfig.json"), JSON.stringify({ extends: "../tsconfig.json", include: ["case.tsx"], exclude: [] }));
    const result = spawnSync(process.execPath, [resolve("node_modules/typescript/bin/tsc"), "-p", resolve(dir, "tsconfig.json")], { encoding: "utf8", timeout: 30000 });
    if (result.error || result.status === 0 || !result.stdout.includes(diagnostic)) throw new Error(`invalid JSX ${jsx} did not fail as expected (status ${result.status}): ${result.error || result.stdout || result.stderr}`);
  }
  console.log("negative JSX: 5 invalid programs rejected by TypeScript");
} finally { rmSync(dir, { recursive: true, force: true }); }
