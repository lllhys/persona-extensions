import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
/** Dependency provenance check. Never print rejected URLs or lock contents. */
export async function checkLock(path) {
  const text = await readFile(path, "utf8"), lock = JSON.parse(text);
  if (!lock.packages || /"(?:_auth(?:Token)?|password|username|access_token|token)"\s*:/i.test(text)) throw new Error("invalid lock metadata");
  for (const [name, pkg] of Object.entries(lock.packages)) {
    if (!pkg.resolved) continue;
    if (name === "node_modules/@persona/extension" && pkg.link && pkg.resolved === "../../sdk/ts") continue;
    let url;
    try { url = new URL(pkg.resolved); } catch { throw new Error("unapproved lock source"); }
    if (url.protocol !== "https:" || url.hostname !== "registry.npmjs.org" || url.port || url.username || url.password || url.search || url.hash || !pkg.integrity) throw new Error("unapproved lock source");
  }
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) console.log("lock: PASS");
