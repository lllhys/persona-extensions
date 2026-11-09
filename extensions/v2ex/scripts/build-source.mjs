import { build } from "esbuild";
import { createHash } from "node:crypto";
import { cp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { fileURLToPath, pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { checkLock } from "./check-lock.mjs";
import { checkSourceBundle } from "../../../sdk/ts/scripts/check-source-bundle.mjs";

// Local source profile only. No signing, market writes, version bumps or uploads.
const root = fileURLToPath(new URL("../", import.meta.url));
const out = resolve(root, "build"), pkg = resolve(out, "package");
const digest = bytes => createHash("sha256").update(bytes).digest("hex");
await checkLock(resolve(root, "package-lock.json"));
const built = await build({ absWorkingDir: root, entryPoints: ["src/index.ts"], bundle: true, write: false,
  format: "esm", platform: "neutral", target: "es2022", treeShaking: true, metafile: true,
  jsx: "automatic", jsxImportSource: "@persona/extension", legalComments: "none", outfile: "build/v2ex.mjs" });
if (Object.values(built.metafile.outputs).some(output => output.imports.length)) throw new Error("runtime imports are forbidden");
const bytes = built.outputFiles[0].contents;
if (!bytes.length || bytes.length > (8 << 20)) throw new Error("source bundle exceeds bounds");
if (/\b(?:require|import)\s*\(/.test(new TextDecoder().decode(bytes))) throw new Error("runtime dependency resolution is forbidden");
await mkdir(out, { recursive: true });
const bundle = resolve(out, "v2ex.mjs");
await writeFile(bundle, bytes);
const program = (await import(pathToFileURL(bundle).href + "?sha=" + digest(bytes))).default;
if (program.id !== "v2ex" || !program.init || !program.namespaces?.connector || !program.ui?.topics || program.fetch) throw new Error("default export is not the extension Program");
const source = { entry: "src/index.ts", bundle: "build/v2ex.mjs", sha256: digest(bytes) };
const released = JSON.parse(await readFile(resolve(root, "persona-extension.json"), "utf8"));
const runtimeSource = program.manifest?.runtime?.source;
if (program.manifest?.runtime?.mode !== "deno" || program.manifest?.runtime?.command || runtimeSource?.entry !== source.entry || runtimeSource?.bundle !== source.bundle) throw new Error("Program manifest does not describe the packaged source profile");
const manifest = { ...released, runtime: { mode: "deno", source } };
delete manifest.tags;
await rm(pkg, { recursive: true, force: true });
await mkdir(resolve(pkg, "build"), { recursive: true });
const text = JSON.stringify(manifest, null, 2) + "\n";
await writeFile(resolve(pkg, "persona-extension.json"), text);
await writeFile(resolve(pkg, "persona-plugin.json"), text);
await writeFile(resolve(pkg, source.bundle), bytes);
if (digest(await readFile(resolve(pkg, source.bundle))) !== source.sha256) throw new Error("staged hash mismatch");
for (const path of ["src", "scripts", "tests", "package.json", "package-lock.json", "tsconfig.json", "README.md"]) {
  await cp(resolve(root, path), resolve(pkg, path), { recursive: true });
}
// Runtime entry/bundle stay at the host's package root. Complete authoring inputs
// live in a repository-shaped subtree so file:../../sdk/ts remains inside the archive.
const authoring = resolve(pkg, "authoring/extensions/v2ex"), sdk = resolve(pkg, "authoring/sdk/ts");
for (const path of ["src", "scripts", "tests", "package.json", "package-lock.json", "tsconfig.json", "README.md", "persona-extension.json"]) {
  await cp(resolve(root, path), resolve(authoring, path), { recursive: true });
}
for (const path of ["src", "scripts", "fixtures", "typechecks", "package.json", "package-lock.json", "README.md", "tsconfig.json", "tsconfig.negative.json", "tsconfig.ui.json"]) {
  await cp(resolve(root, "../../sdk/ts", path), resolve(sdk, path), { recursive: true });
}
const checked = await checkSourceBundle({ root: pkg, bundle: source.bundle, manifest: "persona-extension.json", probe: true, timeoutMs: 5000 });
if (checked.status !== "PASS") throw new Error("official source authoring check failed: " + JSON.stringify(checked));
console.log("official-authoring: PASS (offline check, not installed-hub acceptance)");
await checkLock(resolve(pkg, "package-lock.json"));
await checkLock(resolve(authoring, "package-lock.json"));
await writeFile(resolve(pkg, "SOURCE-PROFILE.txt"), "Unreleased local source profile. The released Go manifest, version, package default and both market indexes/signatures are unchanged. Unit tests and SDK simulations are not independent test-hub acceptance or screenshots. Production source confinement must pass before switching source defaults and market indexes; both indexes will then be regenerated and signed with the new market key. Existing index verification is not evidence of publishing this source artifact.\n");
const archive = resolve(out, "v2ex-source.tar.gz");
const packed = spawnSync("python3", ["-I", resolve(root, "scripts/archive.py"), pkg, archive], { encoding: "utf8", timeout: 30_000 });
if (packed.error || packed.status !== 0) throw packed.error ?? new Error(packed.stderr);
const archiveBytes = await readFile(archive);
await writeFile(resolve(out, "source.json"), JSON.stringify({ source, archive_sha256: digest(archiveBytes), archive_bytes: archiveBytes.length, bundle_bytes: bytes.length }, null, 2) + "\n");
console.log(JSON.stringify({ profile: "unreleased-local-source", archive, bundle_sha256: source.sha256, bundle_bytes: bytes.length, archive_sha256: digest(archiveBytes), archive_bytes: archiveBytes.length }));
