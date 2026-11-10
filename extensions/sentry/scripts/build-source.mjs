import { build } from "esbuild";
import { cp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import { verifySourceProfile } from "./source-profile.mjs";
import { verifyPublicLock } from "./public-lock.mjs";

const dir = fileURLToPath(new URL("../", import.meta.url));
const authoring = JSON.parse(await readFile(new URL("../package.json", import.meta.url), "utf8"));
const sdkPath = authoring.dependencies["@persona/extension"].replace(/^file:/u, "");
const publicLock = await verifyPublicLock(new URL("../package-lock.json", import.meta.url), sdkPath);
console.log(`public lock: ${publicLock.packages} entries, ${publicLock.publicResolved} public registry sources, ${publicLock.localSDKLinks} local SDK link`);
const result = await build({ absWorkingDir: dir, entryPoints: ["src/index.ts"], bundle: true, write: false,
  format: "esm", platform: "neutral", mainFields: ["module", "main"], target: "es2022", treeShaking: true, metafile: true,
  outfile: "build/sentry.mjs" });
if (Object.values(result.metafile.outputs).some(output => output.imports.length)) throw new Error("runtime imports are forbidden");
if (!Object.values(result.metafile.outputs).some(output => output.exports.includes("default"))) throw new Error("default Program export is required");
const bytes = result.outputFiles[0].contents;
if (bytes.length > 8 * 1024 * 1024) throw new Error("source bundle exceeds 8 MiB");
const source = { entry: "extensions/sentry/src/index.ts", bundle: "extensions/sentry/build/sentry.mjs", sha256: createHash("sha256").update(bytes).digest("hex") };
await mkdir(new URL("../build/", import.meta.url), { recursive: true });
await writeFile(new URL("../build/sentry.mjs", import.meta.url), bytes);
await writeFile(new URL("../build/source.json", import.meta.url), JSON.stringify(source, null, 2) + "\n");
// Local profile only: no version, released manifest, index or signature changes.
const manifest = JSON.parse(await readFile(new URL("../persona-extension.json", import.meta.url), "utf8"));
manifest.runtime = { mode: "deno", source };
const pkg = new URL("../build/package/", import.meta.url);
await rm(pkg, { recursive: true, force: true });
const extension = new URL("extensions/sentry/", pkg);
await mkdir(new URL("build/", extension), { recursive: true });
await writeFile(new URL("persona-extension.json", pkg), JSON.stringify(manifest, null, 2) + "\n");
await writeFile(new URL(source.bundle, pkg), bytes);
// The archive retains the repository layout and official relative SDK dependency.
for (const path of ["src", "package.json", "package-lock.json", "tsconfig.json", "scripts", "tests", "README.md", "persona-extension.json"])
  await cp(new URL("../" + path, import.meta.url), new URL(path, extension), { recursive: true });
const sdkRoot = new URL("../" + sdkPath + "/", import.meta.url);
const sdkLock = await verifyPublicLock(new URL("package-lock.json", sdkRoot), null);
console.log(`official SDK public lock: ${sdkLock.packages} entries, ${sdkLock.publicResolved} public registry sources, no local links`);
await mkdir(new URL("sdk/ts/", pkg), { recursive: true });
await cp(new URL("../VERSION", sdkRoot), new URL("sdk/VERSION", pkg));
for (const path of ["package.json", "package-lock.json", "README.md", "src", "scripts", "fixtures", "typechecks", "tsconfig.json", "tsconfig.ui.json", "tsconfig.negative.json"])
  await cp(new URL(path, sdkRoot), new URL("sdk/ts/" + path, pkg), { recursive: true });
// The Go oracle stays in the archive; its module uses the existing public Go SDK.
await mkdir(new URL("sdk/go/", pkg), { recursive: true });
await cp(new URL("../go/", sdkRoot), new URL("sdk/go/", pkg), { recursive: true });
await verifySourceProfile(fileURLToPath(pkg));
await verifyPublicLock(new URL("package-lock.json", extension), sdkPath);
const archive = new URL("../build/sentry-source.tar.gz", import.meta.url);
const tar = spawnSync("tar", ["-czf", fileURLToPath(archive), "-C", fileURLToPath(pkg), "."], { encoding: "utf8", timeout: 30_000 });
if (tar.error || tar.status !== 0) throw tar.error ?? new Error(tar.stderr);
const archiveBytes = await readFile(archive), archiveDigest = createHash("sha256").update(archiveBytes).digest("hex");
console.log(`source bundle: ${bytes.length} bytes, no imports, default Program; sha256=${source.sha256}`);
console.log(`source archive: ${archiveBytes.length} bytes; sha256=${archiveDigest}; local profile only`);
