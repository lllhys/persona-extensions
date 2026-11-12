import { build } from "esbuild";
import { cp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
const dir = fileURLToPath(new URL("../", import.meta.url));
const result = await build({ absWorkingDir: dir, entryPoints: ["src/index.ts"], bundle: true, write: false,
  format: "esm", platform: "neutral", target: "es2022", treeShaking: true, metafile: true,
  jsx: "automatic", jsxImportSource: "@persona/extension", outfile: "build/hackernews.mjs" });
if (Object.values(result.metafile.outputs).some(output => output.imports.length)) throw new Error("runtime imports are forbidden");
const bytes = result.outputFiles[0].contents;
if (bytes.length > 8 * 1024 * 1024) throw new Error("source bundle exceeds 8 MiB");
const source = { entry: "src/index.ts", bundle: "build/hackernews.mjs", sha256: createHash("sha256").update(bytes).digest("hex") };
await mkdir(new URL("../build/", import.meta.url), { recursive: true });
await writeFile(new URL("../build/hackernews.mjs", import.meta.url), bytes);
await writeFile(new URL("../build/source.json", import.meta.url), JSON.stringify(source, null, 2) + "\n");
// An explicit local profile only: never overwrite the released Go manifest or market.
const manifest = JSON.parse(await readFile(new URL("../persona-extension.json", import.meta.url), "utf8"));
const sourceIcon = manifest.icon;
delete manifest.tags;
delete manifest.replaces;
delete manifest.icon;
manifest.runtime = { mode: "deno", source };
manifest.contributes.ui = [
  { id: "stories", title: "Hacker News", placement: "nav_page", icon: "list" },
  { id: "stories-snapshot", title: "Hacker News", placement: "widget", opens: "stories", families: ["small", "medium", "large", "accessory_rectangular", "accessory_circular", "accessory_inline"] },
];
const pkg = new URL("../build/package/", import.meta.url);
await rm(pkg, { recursive: true, force: true });
await mkdir(new URL("build/", pkg), { recursive: true });
if (sourceIcon) await cp(new URL("../assets/", import.meta.url), new URL("assets/", pkg), { recursive: true });
await writeFile(new URL("persona-extension.json", pkg), JSON.stringify(manifest, null, 2) + "\n");
await writeFile(new URL(source.bundle, pkg), bytes);
for (const path of ["src", "package.json", "package-lock.json", "tsconfig.json", "scripts", "tests", "README.md"]) {
  await cp(new URL("../" + path, import.meta.url), new URL(path, pkg), { recursive: true });
}
const archive = new URL("../build/hackernews-source.tar.gz", import.meta.url);
const tar = spawnSync("tar", ["-czf", fileURLToPath(archive), "-C", fileURLToPath(pkg), "."], { encoding: "utf8", timeout: 30_000 });
if (tar.error || tar.status !== 0) throw tar.error ?? new Error(tar.stderr);
const archiveDigest = createHash("sha256").update(await readFile(archive)).digest("hex");
console.log(`source bundle: ${bytes.length} bytes, no runtime imports, default Program; sha256=${source.sha256}`);
console.log(`source archive: sha256=${archiveDigest}; explicit local profile, released Go manifest unchanged`);
