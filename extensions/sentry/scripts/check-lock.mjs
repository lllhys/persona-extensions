import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

/** Lock provenance is checked separately from known-secret pattern scans. */
export function checkLockText(text) {
  const lock = JSON.parse(text);
  if (lock.lockfileVersion !== 3 || !lock.packages) throw new Error("unsupported authoring lock");
  let remote = 0, links = 0;
  for (const [path, pkg] of Object.entries(lock.packages)) {
    if (path === "node_modules/@persona/extension") {
      if (pkg.resolved !== "../../sdk/ts" || pkg.link !== true) throw new Error("unapproved SDK link");
      links++;
    } else if (pkg.resolved !== undefined) {
      let url;
      try { url = new URL(pkg.resolved); } catch { throw new Error("nonpublic dependency source"); }
      if (url.protocol !== "https:" || url.host !== "registry.npmjs.org" || url.username || url.password || url.search || url.hash || pkg.link)
        throw new Error("nonpublic dependency source");
      if (typeof pkg.version !== "string" || !/^sha512-[A-Za-z0-9+/]+={0,2}$/.test(pkg.integrity ?? ""))
        throw new Error("missing dependency version or integrity");
      remote++;
    }
  }
  if (links !== 1 || !remote) throw new Error("missing public dependencies or SDK link");
  function credentials(value) {
    if (typeof value === "string") {
      for (const match of value.matchAll(/https?:\/\/[^\s"<>]+/g)) {
        let url;
        try { url = new URL(match[0]); } catch { throw new Error("invalid lock URL"); }
        if (url.username || url.password || /(?:token|password|secret|auth|api[_-]?key)=/i.test(url.search))
          throw new Error("credential in lock URL");
      }
    } else if (value && typeof value === "object") {
      for (const [key, item] of Object.entries(value)) {
        if (/^(?:_authToken|_auth|password|token|secret|api[_-]?key)$/i.test(key)) throw new Error("credential in lock metadata");
        credentials(item);
      }
    }
  }
  credentials(lock);
  return { remote, links };
}
export async function checkLock(path) { return checkLockText(await readFile(path, "utf8")); }
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const counts = await checkLock(new URL("../package-lock.json", import.meta.url));
    console.log(`public lock: PASS (${counts.remote} public npm URLs, ${counts.links} approved SDK link, no credentials)`);
  } catch {
    console.error("public lock: FAIL (unapproved provenance or credentials; values withheld)");
    process.exitCode = 1;
  }
}
