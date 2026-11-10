import { createHash } from "node:crypto";
import { lstat, readFile } from "node:fs/promises";
import { isAbsolute, resolve, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

/** Local preparation guard, not a substitute for manager installation validation. */
export async function verifySourceProfile(root) {
  root = resolve(root);
  const regular = async name => {
    if (typeof name !== "string" || !name || name.includes("\\") || isAbsolute(name)) throw new Error("invalid source path");
    const path = resolve(root, name), rel = relative(root, path);
    if (!rel || rel.startsWith(".." + sep) || rel === "..") throw new Error("source path escapes package");
    let current = root;
    for (const part of rel.split(sep)) {
      current = resolve(current, part);
      if ((await lstat(current)).isSymbolicLink()) throw new Error("source symlinks are forbidden");
    }
    if (!(await lstat(path)).isFile()) throw new Error("source path is not a regular file");
    return path;
  };
  const manifest = JSON.parse(await readFile(await regular("persona-extension.json"), "utf8"));
  if (manifest.runtime?.mode !== "deno" || ["command", "args", "env", "http", "rpc"].some(key => Object.hasOwn(manifest.runtime, key))) throw new Error("not a source-only profile");
  const source = manifest.runtime.source;
  if (!source || !/^[a-f0-9]{64}$/u.test(source.sha256) || !/\.tsx?$/u.test(source.entry) || !/\.mjs$/u.test(source.bundle)) throw new Error("invalid source metadata");
  await regular(source.entry);
  const bytes = await readFile(await regular(source.bundle));
  if (!bytes.length || bytes.length > 8 << 20) throw new Error("invalid source bundle size");
  const sha256 = createHash("sha256").update(bytes).digest("hex");
  if (sha256 !== source.sha256) throw new Error("source hash mismatch");
  // Official SDK checker A owns ESM grammar/import/default validation. Its default
  // static mode constructs and links a module but never evaluates guest code.
  // Always use actual Node, including when authoring tests are driven by Bun.
  const checker = fileURLToPath(new URL("../../../sdk/ts/scripts/check-source-bundle.mjs", import.meta.url));
  const run = spawnSync("node", [checker, "--root", root, "--bundle", source.bundle, "--manifest", "persona-extension.json"], {
    encoding: "utf8", timeout: 10_000,
  });
  if (run.error) throw run.error;
  const verdict = JSON.parse(run.stdout);
  if (verdict.status !== "PASS") {
    if (verdict.code === "SYNTAX") throw new SyntaxError("source bundle has parse diagnostics");
    if (verdict.code === "IMPORT") throw new Error("runtime imports are forbidden");
    if (verdict.code === "DEFAULT_EXPORT") throw new Error("default Program export is required");
    throw new Error(`official SDK source checker: ${verdict.code}`);
  }
  if (verdict.code !== "STATIC_OK" || verdict.program !== "not-probed" || verdict.sha256 !== sha256) throw new Error("unexpected source checker verdict");
  return { manifest, sha256, bytes: bytes.length, checker: verdict };
}
