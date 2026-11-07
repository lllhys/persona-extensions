import { readFileSync } from "node:fs";
import { resolve } from "node:path";

// Invoke TypeScript itself, not Bun's transpile-only loader.
const root = resolve(import.meta.dir, "..");
const source = readFileSync(resolve(root, "typechecks/negative.tsx"), "utf8").split("\n");
const expected = source.flatMap((line, index) => line.startsWith("const ") || line.startsWith("import ") ? [index + 1] : []);
const result = Bun.spawnSync([process.execPath, resolve(root, "node_modules/typescript/bin/tsc"), "-p", resolve(root, "tsconfig.negative.json"), "--pretty", "false"], { cwd: root, timeout: 30000 });
const diagnostics = new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr);
const missing = expected.filter(line => !diagnostics.includes(`negative.tsx(${line},`));
if (result.exitCode !== 2 || missing.length) {
  process.stderr.write(diagnostics);
  throw new Error(`negative TSX compilation failed verification: exit=${result.exitCode}, missing lines=${missing.join(",")}`);
}
console.log(`PASS: actual TypeScript compiler rejected all ${expected.length} negative TSX/type fixtures`);
