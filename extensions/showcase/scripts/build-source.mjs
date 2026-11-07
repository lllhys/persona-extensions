import { build } from "esbuild";
import { createHash } from "node:crypto";
import { readFile, writeFile, mkdir, rm, cp, lstat } from "node:fs/promises";
import { fileURLToPath, pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

// Extension-owned, unreleased packaging. Never signs, indexes, uploads or changes release inputs.
const root = fileURLToPath(new URL("../", import.meta.url));
const out = resolve(root, ".source-dist"), stage = resolve(out, "showcase");
const digest = bytes => createHash("sha256").update(bytes).digest("hex");
await rm(out, { recursive: true, force: true });
await mkdir(resolve(stage, "bundle"), { recursive: true });
const bundle = resolve(stage, "bundle/main.mjs");
const result = await build({
  absWorkingDir: root, entryPoints: ["src/main.tsx"], outfile: bundle,
  bundle: true, platform: "neutral", format: "esm", target: "es2022", minify: true,
  metafile: true, treeShaking: true, legalComments: "none", charset: "utf8",
});
if (Object.values(result.metafile.outputs).some(output => output.imports.length !== 0)) {
  throw new Error("the source artifact has runtime imports; offline packaging refused");
}
const bytes = await readFile(bundle);
if (!bytes.length || bytes.length > (16 << 20)) throw new Error("source bundle exceeds host bounds");
if (/\b(?:require|import)\s*\(/.test(bytes.toString())) throw new Error("runtime dependency resolution is forbidden");
const manifest = JSON.parse(await readFile(resolve(root, "persona-extension.json"), "utf8"));
delete manifest.tags;
const { sourceContributions } = await import(pathToFileURL(bundle).href);
manifest.contributes = sourceContributions;
manifest.runtime = { mode: "deno", source: { entry: "src/main.tsx", bundle: "bundle/main.mjs", sha256: digest(bytes) } };
manifest.configuration = { type: "object", properties: {
  media: { type: "object", title: "Synthetic presentation media", properties: Object.fromEntries(
    ["avatar", "square", "wide", "broken"].map(key => [key, { type: "string", format: "uri" }])) },
} };
const text = JSON.stringify(manifest, null, 2) + "\n";
await writeFile(resolve(stage, "persona-extension.json"), text);
// Older hubs must validate/reject the unsupported source runtime, not mistake it for a missing manifest.
await writeFile(resolve(stage, "persona-plugin.json"), text);
await cp(resolve(root, "src"), resolve(stage, "src"), {
  recursive: true, filter: async path => !(await lstat(path)).isSymbolicLink() && !path.endsWith(".test.tsx"),
});
for (const name of ["assets", "package.json", "package-lock.json", "README.md"]) {
  await cp(resolve(root, name), resolve(stage, name), { recursive: true });
}
await writeFile(resolve(stage, "SOURCE-PROFILE.txt"), "Unreleased source authoring profile. Not a market release or automatic upgrade. Offline runtime bundle; native presentation images use HTTPS and optional media configuration. Production source execution requires independently confined host resources. The Go package remains the released default.\n");
const archive = resolve(out, "showcase-source.tar.gz");
const packed = spawnSync("tar", ["--sort=name", "--mtime=@0", "--owner=0", "--group=0", "--numeric-owner", "-czf", archive, "-C", out, "showcase"], { encoding: "utf8", timeout: 30000 });
if (packed.error || packed.status !== 0) throw packed.error ?? new Error(packed.stderr);
console.log(JSON.stringify({ profile: "unreleased-source", bundle_sha256: digest(bytes), archive_sha256: digest(await readFile(archive)), bundle_bytes: bytes.length }));
