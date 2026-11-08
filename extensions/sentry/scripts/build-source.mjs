import { build } from "esbuild";
import { createHash } from "node:crypto";
import { readFile, writeFile, mkdir, rm, cp, lstat, readdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { resolve, relative, isAbsolute } from "node:path";
import { spawnSync } from "node:child_process";
import { checkLock } from "./check-lock.mjs";
const root = fileURLToPath(new URL("../", import.meta.url)), repo = resolve(root, "../..");
const out = resolve(root, ".source-dist"), stage = resolve(out, "sentry"), authoring = resolve(out, "sentry-authoring");
const digest = bytes => createHash("sha256").update(bytes).digest("hex");
async function regularTree(path) {
  const stat = await lstat(path);
  if (stat.isSymbolicLink() || !stat.isDirectory() && !stat.isFile()) throw new Error("non-regular authoring input");
  if (stat.isDirectory()) for (const name of await readdir(path)) await regularTree(resolve(path, name));
}
await checkLock(resolve(root, "package-lock.json"));
const provenance = JSON.parse(await readFile(resolve(root, "sdk-provenance.json"), "utf8"));
const seen = new Set();
for (const entry of provenance.files) {
  if (!entry.path.startsWith("sdk/") || isAbsolute(entry.path) || entry.path.split("/").includes("..") || seen.has(entry.path)) throw new Error("invalid SDK closure");
  seen.add(entry.path);
  await regularTree(resolve(repo, entry.path));
  if (digest(await readFile(resolve(repo, entry.path))) !== entry.sha256) throw new Error("official SDK bytes differ from provenance");
}
// Reject an incomplete inventory, including files not imported by today's bundle.
async function sdkFiles(path) {
  const files = [];
  for (const name of await readdir(path)) {
    if (["node_modules", "build", ".source-dist"].includes(name)) continue;
    const child = resolve(path, name), stat = await lstat(child);
    if (stat.isSymbolicLink()) throw new Error("SDK symlink is forbidden");
    files.push(...(stat.isDirectory() ? await sdkFiles(child) : [relative(repo, child)]));
  }
  return files;
}
const actual = ["sdk/VERSION", ...await sdkFiles(resolve(repo, "sdk/ts"))].sort();
if (JSON.stringify(actual) !== JSON.stringify([...seen].sort())) throw new Error("official SDK module closure is incomplete");
if (out !== resolve(root, ".source-dist") || out === root) throw new Error("invalid build output");
await rm(out, { recursive: true, force: true });
await mkdir(resolve(stage, "bundle"), { recursive: true });
const bundle = resolve(stage, "bundle/main.mjs");
const result = await build({
  absWorkingDir: root, entryPoints: ["src/index.ts"], outfile: bundle,
  bundle: true, platform: "neutral", format: "esm", target: "es2022", minify: true,
  metafile: true, treeShaking: true, legalComments: "none", charset: "utf8",
});
if (Object.values(result.metafile.outputs).some(output => output.imports.length)) throw new Error("offline bundle has runtime imports");
const bytes = await readFile(bundle);
if (!bytes.length || bytes.length > (16 << 20)) throw new Error("bundle exceeds host bounds");
if (/\b(?:require|import)\s*\(/.test(bytes.toString())) throw new Error("runtime dependency resolution is forbidden");
const releasedText = await readFile(resolve(root, "RELEASED-MANIFEST.reference.json"), "utf8").catch(error => {
  if (error.code !== "ENOENT") throw error;
  return readFile(resolve(root, "persona-extension.json"), "utf8");
});
const manifest = JSON.parse(releasedText);
manifest.runtime = { mode: "deno", source: { entry: "src/index.ts", bundle: "bundle/main.mjs", sha256: digest(bytes) } };
// Only the unreleased runtime profile differs. Grants/config/contributions stay exact.
const text = JSON.stringify(manifest, null, 2) + "\n";
await writeFile(resolve(stage, "persona-extension.json"), text);
await writeFile(resolve(stage, "persona-plugin.json"), text);
await writeFile(resolve(stage, "RELEASED-MANIFEST.reference.json"), releasedText);
for (const name of ["src", "tests", "scripts", "package.json", "package-lock.json", "tsconfig.json", "README.md", "sdk-provenance.json"]) {
  await regularTree(resolve(root, name));
  await cp(resolve(root, name), resolve(stage, name), { recursive: true });
}
await writeFile(resolve(stage, "SOURCE-PROFILE.txt"), "Unreleased Sentry source profile. The released Go package, version, original *.sentry.io network grant, configuration and catalog remain unchanged. The runtime bundle is contained and import-free; no extension UI or client JavaScript/HTML is added. The external manifest owns the final digest; the optional Program.manifest is absent to avoid self-reference. Runtime installation uses sentry-source.tar.gz. Standalone authoring uses sentry-authoring.tar.gz, containing extensions/sentry and the complete unchanged official SDK module closure at sdk/ts, with file:../../sdk/ts preserved. The SDK dependency is provisional, not released. Original Go inputs and the Go SDK are included for differential tests. Reports are already scrubbed by the host; this service does not sanitize report text. Native SDK callback/cold-load fixtures are tests, not shipped pages or installed-hub acceptance. Local flush/stop are not host lifecycle APIs. Notification raw lexemes, initialization duplicate fields, original wildcard broker enforcement and installed-hub acceptance remain separate gates. No production, signing, marketplace or release acceptance is claimed.\n");
await writeFile(resolve(out, "metafile.json"), JSON.stringify(result.metafile, null, 2) + "\n");
await cp(stage, resolve(authoring, "extensions/sentry"), { recursive: true });
for (const name of ["main.go", "sentry.go", "sentry_test.go", "go.mod", "go.sum"]) {
  try {
    await regularTree(resolve(root, name));
    await cp(resolve(root, name), resolve(authoring, "extensions/sentry", name));
  } catch (error) { if (name !== "go.sum" || error.code !== "ENOENT") throw error; }
}
for (const entry of provenance.files) {
  const destination = resolve(authoring, entry.path);
  await mkdir(resolve(destination, ".."), { recursive: true });
  await cp(resolve(repo, entry.path), destination);
}
// The unchanged Go SDK is the module dependency of the original Go oracle.
await regularTree(resolve(repo, "sdk/go"));
await cp(resolve(repo, "sdk/go"), resolve(authoring, "sdk/go"), { recursive: true });
const archives = {};
for (const [label, directory] of [["runtime", stage], ["authoring", authoring]]) {
  const archive = resolve(out, label === "runtime" ? "sentry-source.tar.gz" : "sentry-authoring.tar.gz");
  const packed = spawnSync("python3", ["-I", resolve(root, "scripts/package-source.py"), directory, archive], { encoding: "utf8", timeout: 30_000 });
  if (packed.error || packed.status !== 0) throw packed.error ?? new Error("source packaging failed");
  const archiveBytes = await readFile(archive);
  archives[label] = { archive, sha256: digest(archiveBytes), bytes: archiveBytes.length };
}
console.log(JSON.stringify({ profile: "unreleased-source", bundle, bundle_sha256: digest(bytes), bundle_bytes: bytes.length, sdk_files: seen.size, ...archives }));
