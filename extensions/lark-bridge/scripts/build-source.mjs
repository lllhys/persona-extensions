import { build } from "esbuild";
import { createHash } from "node:crypto";
import { cp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { fileURLToPath, pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { checkLock } from "./check-lock.mjs";
import ts from "typescript";
import { checkSourceBundle } from "../../../sdk/ts/scripts/check-source-bundle.mjs";
/** The SDK's stdio entry is Bun-only and ends its module. Keep its public API intact
 * for ordinary programs; Deno bundles retain only the source runtime surface. */
function sourceSDK() {
  const file = resolve(root, "../../sdk/ts/src/index.ts"), text = ts.sys.readFile(file);
  const source = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  let main;
  const visit = node => {
    if (ts.isFunctionDeclaration(node) && node.name?.text === "main" && node.body) main = node;
    ts.forEachChild(node, visit);
  };
  visit(source);
  if (!main || source.statements[source.statements.length - 1] !== main || source.text.slice(main.body.end).trim()) {
    throw new Error("SDK stdio entry boundary changed; review the source build");
  }
  return text.slice(0, main.getStart(source)) + "export async function main(_prog: Program): Promise<void> { throw new Error(\"stdio transport is not part of a Deno source bundle\"); }\n";
}
const root = fileURLToPath(new URL("../", import.meta.url));
// Development source archive only; no market edits, signing, version selection or uploads.
const out = resolve(root, "build"), pkg = resolve(out, "package"), sourceEntry = resolve(root, "../../sdk/ts/src/index.ts");
await mkdir(out, { recursive: true });
const digest = bytes => createHash("sha256").update(bytes).digest("hex");
await checkLock(resolve(root, "package-lock.json"));
const built = await build({ absWorkingDir: root, entryPoints: ["src/index.ts"], bundle: true, write: false,
  format: "esm", platform: "browser", target: "es2022", treeShaking: true, metafile: true,
  plugins: [{ name: "source-sdk-entry", setup(build) {
    build.onResolve({ filter: /^@persona\/extension$/ }, () => ({ path: sourceEntry }));
    build.onLoad({ filter: /sdk\/ts\/src\/index\.ts$/ }, () => ({ contents: sourceSDK(), loader: "ts" }));
    build.onLoad({ filter: /persona-extension\.json$/ }, () => {
      const manifest = JSON.parse(ts.sys.readFile(resolve(root, "persona-extension.json")));
      // Only the outer package carries the artifact digest. Embedding it would
      // make every subsequent build hash its own predecessor.
      manifest.runtime.source.sha256 = "";
      return { contents: JSON.stringify(manifest), loader: "json" };
    });
  } }],
  jsx: "automatic", jsxImportSource: "@persona/extension", legalComments: "none", outfile: "build/lark-bridge.mjs" });
if (Object.values(built.metafile.outputs).some(output => output.imports.length)) throw new Error("runtime imports are forbidden");
const bytes = built.outputFiles[0].contents;
if (!bytes.length || bytes.length > (8 << 20) || /\b(?:require|import)\s*\(/.test(new TextDecoder().decode(bytes))) throw new Error("invalid source bundle");
await mkdir(out, { recursive: true });
const bundle = resolve(out, "lark-bridge.mjs");
const releasedText = await readFile(resolve(root, "persona-extension.json"), "utf8");
const released = JSON.parse(releasedText);
released.runtime.source.sha256 = "";
await writeFile(bundle, bytes);
const program = (await import(pathToFileURL(bundle).href + "?sha=" + digest(bytes))).default;
if (program.id !== "lark-bridge" || !program.init || !program.namespaces?.connector || !program.ui?.larkbridge || program.fetch) throw new Error("incomplete default Program");
const source = { entry: "src/index.ts", bundle: "build/lark-bridge.mjs", sha256: digest(bytes) };
const runtimeSource = program.manifest?.runtime?.source;
if (program.manifest?.runtime?.mode !== "deno" || program.manifest?.runtime?.command || runtimeSource?.entry !== source.entry || runtimeSource?.bundle !== source.bundle || runtimeSource?.sha256) throw new Error("Program manifest differs from source package");
const manifest = { ...released, runtime: { ...released.runtime, mode: "deno", source } };
delete manifest.tags;
delete manifest.replaces;
delete manifest.icon;
await rm(pkg, { recursive: true, force: true });
await mkdir(resolve(pkg, "build"), { recursive: true });
const text = JSON.stringify(manifest, null, 2) + "\n";
await writeFile(resolve(pkg, "persona-extension.json"), text);
await writeFile(resolve(pkg, "persona-plugin.json"), text);
await writeFile(resolve(pkg, source.bundle), bytes);
for (const path of ["src", "scripts", "tests", "package.json", "package-lock.json", "tsconfig.json", "README.md"]) await cp(resolve(root, path), resolve(pkg, path), { recursive: true });
const authoring = resolve(pkg, "authoring/extensions/lark-bridge"), sdk = resolve(pkg, "authoring/sdk/ts");
if (released.icon) {
  await cp(resolve(root, "assets"), resolve(pkg, "assets"), { recursive: true });
  await cp(resolve(root, "assets"), resolve(authoring, "assets"), { recursive: true });
}
for (const path of ["src", "scripts", "tests", "package.json", "package-lock.json", "tsconfig.json", "README.md", "persona-extension.json"]) await cp(resolve(root, path), resolve(authoring, path), { recursive: true });
for (const path of ["src", "scripts", "fixtures", "typechecks", "package.json", "package-lock.json", "README.md", "tsconfig.json", "tsconfig.negative.json", "tsconfig.ui.json"]) await cp(resolve(root, "../../sdk/ts", path), resolve(sdk, path), { recursive: true });
const checked = await checkSourceBundle({ root: pkg, bundle: source.bundle, manifest: "persona-extension.json", probe: true, timeoutMs: 5000 });
if (checked.status !== "PASS") throw new Error("official source authoring check failed: " + JSON.stringify(checked));
await checkLock(resolve(pkg, "package-lock.json"));
await checkLock(resolve(authoring, "package-lock.json"));
await writeFile(resolve(pkg, "SOURCE-PROFILE.txt"), "Development source archive. Native TypeScript/JSX runs on the hub; no client executes extension JavaScript. This is not a signed market release, production activation or production isolation acceptance. The immutable artifact and official SDK authoring closure are included for independent development installation and rebuilds.\n");
await writeFile(resolve(root, "persona-extension.json"), JSON.stringify({ ...released, runtime: { ...released.runtime, source } }, null, 2) + "\n");
await cp(resolve(root, "persona-extension.json"), resolve(authoring, "persona-extension.json"));
const archive = resolve(out, "lark-bridge-source.tar.gz");
const packed = spawnSync("python3", ["-I", resolve(root, "scripts/archive.py"), pkg, archive], { encoding: "utf8", timeout: 30_000 });
if (packed.error || packed.status !== 0) throw packed.error ?? new Error(packed.stderr);
const archiveBytes = await readFile(archive);
await writeFile(resolve(out, "source.json"), JSON.stringify({ source, archive_sha256: digest(archiveBytes), archive_bytes: archiveBytes.length, bundle_bytes: bytes.length }, null, 2) + "\n");
console.log(JSON.stringify({ profile: "development-source", archive, bundle_sha256: source.sha256, bundle_bytes: bytes.length, archive_sha256: digest(archiveBytes), archive_bytes: archiveBytes.length }));
