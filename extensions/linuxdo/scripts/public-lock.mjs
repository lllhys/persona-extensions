import { readFile } from "node:fs/promises";

/** Verify URL provenance separately from the private terms scan. Never log URLs. */
export async function verifyPublicLock(path, sdkPath = "../../sdk/ts") {
  if (sdkPath !== null && !["../../sdk/ts", "sdk/ts"].includes(sdkPath)) throw new Error("unapproved SDK packaging boundary");
  const lock = JSON.parse(await readFile(path, "utf8"));
  if (lock.lockfileVersion !== 3 || !lock.packages) throw new Error("invalid public lock structure");
  let publicResolved = 0, localSDKLinks = 0;
  for (const [name, pkg] of Object.entries(lock.packages)) {
    if (pkg.link) {
      if (name !== "node_modules/@persona/extension" || pkg.resolved !== sdkPath) throw new Error("unapproved local dependency link");
      localSDKLinks++;
    } else if (pkg.resolved !== undefined) {
      let url;
      try { url = new URL(pkg.resolved); } catch { throw new Error("invalid dependency source URL"); }
      if (url.protocol !== "https:" || url.hostname !== "registry.npmjs.org" || url.port || url.username || url.password || url.search || url.hash) throw new Error("unapproved dependency source URL");
      if (!/^[\w@/.-]+\/-\/[\w.-]+\.tgz$/u.test(url.pathname)) throw new Error("invalid public registry tarball path");
      if (!/^sha512-[A-Za-z0-9+/]+={0,2}$/u.test(pkg.integrity ?? "")) throw new Error("missing public dependency integrity");
      publicResolved++;
    }
  }
  if (sdkPath === null) {
    if (localSDKLinks !== 0 || lock.packages[""]?.name !== "@persona/extension") throw new Error("invalid standalone SDK lock metadata");
  } else if (localSDKLinks !== 1 || lock.packages[""].dependencies?.["@persona/extension"] !== "file:" + sdkPath || lock.packages[sdkPath]?.name !== "@persona/extension") throw new Error("invalid local SDK metadata");
  const credentials = value => {
    if (typeof value === "string") {
      for (const match of value.matchAll(/https?:\/\/[^\s"<>]+/gu)) {
        let url;
        try { url = new URL(match[0]); } catch { throw new Error("invalid lock URL"); }
        if (url.username || url.password || /(?:auth|token|password|secret)/iu.test(url.search)) throw new Error("credentials in dependency metadata");
      }
    } else if (value && typeof value === "object") {
      for (const [key, child] of Object.entries(value)) {
        if (/^(?:_auth(?:Token)?|password|token|access_token|secret)$/iu.test(key)) throw new Error("credential field in dependency metadata");
        credentials(child);
      }
    }
  };
  credentials(lock);
  return { packages: Object.keys(lock.packages).length, publicResolved, localSDKLinks };
}
