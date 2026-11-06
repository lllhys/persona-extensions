import { build } from "esbuild";
import { createHash } from "node:crypto";
import { readFile, writeFile, mkdir, rm, cp } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

// Draft-only, local packaging. Never signs, indexes, uploads or changes the released manifest/version.
const root = fileURLToPath(new URL("../", import.meta.url));
const out = resolve(root, ".source-dist"), stage = resolve(out, "showcase");
await rm(out, { recursive: true, force: true });
await mkdir(resolve(stage, "bundle"), { recursive: true });
const bundle = resolve(stage, "bundle/main.mjs");
const result = await build({
  absWorkingDir: root, entryPoints: ["src/main.tsx"], outfile: bundle,
  bundle: true, platform: "neutral", format: "esm", target: "es2022",
  metafile: true, treeShaking: true, legalComments: "none", charset: "utf8",
});
if (Object.values(result.metafile.outputs).some(output => output.imports.length !== 0)) {
  throw new Error("the source artifact has runtime imports; offline packaging refused");
}
const bytes = await readFile(bundle);
if (/\b(?:require|import)\s*\(/.test(bytes.toString())) throw new Error("runtime dependency resolution is forbidden");
const manifest = JSON.parse(await readFile(resolve(root, "persona-extension.json"), "utf8"));
delete manifest.tags;
manifest.runtime = { mode: "deno", source: { entry: "src/main.tsx", bundle: "bundle/main.mjs", sha256: createHash("sha256").update(bytes).digest("hex") } };
await writeFile(resolve(stage, "persona-extension.json"), JSON.stringify(manifest, null, 2) + "\n");
await cp(resolve(root, "src"), resolve(stage, "src"), { recursive: true, filter: path => !path.endsWith(".test.tsx") });
await cp(resolve(root, "package-lock.json"), resolve(stage, "package-lock.json"));
await writeFile(resolve(stage, "DRAFT.txt"), "Not release-ready: interactive manager bridge and engine/auto-upgrade compatibility gates are pending. The process package remains released.\n");
const packed = spawnSync("tar", ["--sort=name", "--mtime=@0", "--owner=0", "--group=0", "--numeric-owner", "-czf", resolve(out, "showcase-source-draft.tar.gz"), "-C", out, "showcase"], { encoding: "utf8", timeout: 30000 });
if (packed.error || packed.status !== 0) throw packed.error ?? new Error(packed.stderr);
console.log(`source draft: ${manifest.runtime.source.sha256}; self-contained bundle; no release/version change`);
