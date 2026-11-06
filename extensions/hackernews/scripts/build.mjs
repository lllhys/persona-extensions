import { build } from "esbuild";
import { mkdir, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
const dir = fileURLToPath(new URL("../", import.meta.url));
const result = await build({ absWorkingDir: dir, entryPoints: ["src/index.ts"], bundle: true, write: false,
  format: "esm", platform: "neutral", target: "es2022", treeShaking: true, metafile: true,
  jsx: "automatic", jsxImportSource: "@persona/extension", outfile: "build/hackernews.mjs" });
if (Object.values(result.metafile.outputs).some(output => output.imports.length)) throw new Error("runtime imports are forbidden");
const bytes = result.outputFiles[0].contents;
await mkdir(new URL("../build/", import.meta.url), { recursive: true });
await writeFile(new URL("../build/hackernews.mjs", import.meta.url), bytes);
// SourceRuntime-shaped descriptor, NOT a release manifest or a runnable protocol claim.
await writeFile(new URL("../build/source.json", import.meta.url), JSON.stringify({ entry: "src/index.ts", bundle: "build/hackernews.mjs", sha256: createHash("sha256").update(bytes).digest("hex") }, null, 2) + "\n");
console.log(`source bundle: ${bytes.length} bytes, no runtime imports; bridge entry still required`);
