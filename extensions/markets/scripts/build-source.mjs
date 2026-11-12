import { build } from "esbuild";
import { cp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { fileURLToPath, pathToFileURL } from "node:url";
import { archiveTree } from "./archive.mjs";
import { resolve } from "node:path";
const root = fileURLToPath(new URL("../", import.meta.url));
const hash = bytes => createHash("sha256").update(bytes).digest("hex");
const releasedPath = resolve(root, "persona-extension.json"), releasedBytes = await readFile(releasedPath);
const lock = JSON.parse(await readFile(resolve(root, "package-lock.json"), "utf8"));
for (const [path, entry] of Object.entries(lock.packages)) if (entry.resolved) {
  if (path === "node_modules/@persona/extension" && entry.link === true && entry.resolved === "../../sdk/ts") continue;
  let url;
  try { url = new URL(entry.resolved); } catch { throw new Error("Unapproved resolved source in package lock"); }
  if (url.protocol !== "https:" || url.host !== "registry.npmjs.org" || url.username || url.password || url.search || url.hash || !entry.integrity?.startsWith("sha512-")) throw new Error("Source packaging requires credential-free public npm provenance or the approved local SDK link");
}
const packageManifest = JSON.parse(releasedBytes);
const result = await build({ absWorkingDir: root, entryPoints: ["src/index.ts"], bundle: true, write: false,
  // Package runtime metadata belongs to the host, not to a self-referential guest export.
  plugins: [{ name: "program-identity", setup(build) { build.onLoad({ filter: /persona-extension\.json$/ }, args => args.path === releasedPath ? { loader: "json", contents: JSON.stringify({ id: packageManifest.id, version: packageManifest.version, contributes: packageManifest.contributes }) } : undefined); } }],
  format: "esm", platform: "neutral", target: "es2022", treeShaking: true, metafile: true,
  jsx: "automatic", jsxImportSource: "@persona/extension", outfile: "build/markets.mjs", legalComments: "none" });
if (Object.values(result.metafile.outputs).some(output => output.imports.length)) throw new Error("Runtime imports are forbidden");
const bytes = result.outputFiles[0].contents;
if (!bytes.length || bytes.length > 8 * 1024 * 1024 || /\b(?:require|import)\s*\(/.test(new TextDecoder().decode(bytes))) throw new Error("Source bundle exceeds offline bounds");
await mkdir(resolve(root, "build"), { recursive: true });
const bundle = resolve(root, "build/markets.mjs");
await writeFile(bundle, bytes);
const program = (await import(pathToFileURL(bundle).href + "?hash=" + hash(bytes))).default;
if (program.id !== "markets" || program.fetch || typeof program.init !== "function" || !program.ui?.watchlist?.componentPage || !program.ui?.card?.render) throw new Error("Default source Program is incomplete");
const manifest = JSON.parse(releasedBytes);
delete manifest.tags;
delete manifest.replaces;
delete manifest.icon;
manifest.runtime = { mode: "deno", source: { entry: "src/index.ts", bundle: "build/markets.mjs", sha256: hash(bytes) } };
const packageRoot = resolve(root, "build/package"), stage = resolve(packageRoot, "extensions/markets");
await rm(packageRoot, { recursive: true, force: true });
await mkdir(resolve(stage, "build"), { recursive: true });
const text = JSON.stringify(manifest, null, 2) + "\n";
await writeFile(resolve(stage, "persona-extension.json"), text);
await writeFile(resolve(stage, "persona-plugin.json"), text);
await writeFile(resolve(stage, "build/markets.mjs"), bytes);
// The official installer reads the manifest at the archive root. Authoring keeps
// its extension-relative manifest and SDK dependency separately below that root.
const hostManifest = structuredClone(manifest);
hostManifest.runtime.source.entry = "extensions/markets/src/index.ts";
const hostText = JSON.stringify(hostManifest, null, 2) + "\n";
await writeFile(resolve(packageRoot, "persona-extension.json"), hostText);
await writeFile(resolve(packageRoot, "persona-plugin.json"), hostText);
await mkdir(resolve(packageRoot, "build"), { recursive: true });
await writeFile(resolve(packageRoot, "build/markets.mjs"), bytes);
if (packageManifest.icon) {
  await cp(resolve(root, "assets"), resolve(packageRoot, "assets"), { recursive: true });
  await cp(resolve(root, "assets"), resolve(stage, "assets"), { recursive: true });
}
// Rebuilding is extension-local; execution itself resolves no package dependencies.
for (const path of ["src", "tests", "testdata", "scripts", "package.json", "package-lock.json", "tsconfig.json", "README.md"]) await cp(resolve(root, path), resolve(stage, path), { recursive: true });
// Include the entire unchanged SDK authoring closure: exports, exact/compat
// helpers, fixtures, tests, checker scripts and their Go oracle module. Generated
// dependencies and build outputs are never packaged or staged in this PR.
const sdk = resolve(root, "../../sdk/ts"), sdkStage = resolve(packageRoot, "sdk/ts");
await cp(sdk, sdkStage, { recursive: true, dereference: false,
  filter: path => !path.slice(sdk.length).split(/[\\/]/).some(part => ["node_modules", "build", "dist", ".git"].includes(part)) });
await writeFile(resolve(stage, "SOURCE-PROFILE.txt"), "Unreleased local source profile. Installable manifest and bundle are at the archive root; authoring root: extensions/markets. Authoring rebuild uses the included, unchanged complete sdk/ts package closure and file:../../sdk/ts dependency. Program.manifest is intentionally absent; the official host validates the staged package manifest and its final bundle SHA-256, not a self-referential guest manifest. The Go manifest, version, default package, market indexes and signatures remain unchanged. This archive is not a signed market release. Old state requires reviewed offline conversion and encrypted host storage. Production rollout remains gated on independent isolation acceptance.\n");
const archive = resolve(root, "build/markets-source.tar.gz");
await archiveTree(packageRoot, archive);
const archiveBytes = await readFile(archive);
if (!releasedBytes.equals(await readFile(releasedPath))) throw new Error("Released manifest changed during local packaging");
console.log(JSON.stringify({ profile: "unreleased-source", archive, archive_sha256: hash(archiveBytes), archive_bytes: archiveBytes.length, bundle, bundle_sha256: hash(bytes), bundle_bytes: bytes.length, runtime_imports: 0 }));
