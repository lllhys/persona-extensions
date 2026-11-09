import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";

/** Audit provenance separately from term-list privacy checks. Never print rejected URLs. */
export async function checkLock(path) {
  const text = await readFile(path, "utf8"), lock = JSON.parse(text);
  if (!lock.packages || /"(?:_auth(?:Token)?|password|username|access_token|token)"\s*:/i.test(text)) throw new Error("lock credentials or package metadata are invalid");
  let publicCount = 0, localCount = 0;
  for (const [name, pkg] of Object.entries(lock.packages)) {
    if (!pkg.resolved) continue;
    if (name === "node_modules/@persona/extension" && pkg.link && pkg.resolved === "../../sdk/ts") { localCount++; continue; }
    let url;
    try { url = new URL(pkg.resolved); } catch { throw new Error("unapproved lock source"); }
    if (url.protocol !== "https:" || url.hostname !== "registry.npmjs.org" || url.port || url.username || url.password || url.search || url.hash || !pkg.integrity) throw new Error("unapproved lock source or missing integrity");
    publicCount++;
  }
  return { packages: Object.keys(lock.packages).length, public_resolved: publicCount, local_sdk_links: localCount };
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const path = process.argv[2] ?? fileURLToPath(new URL("../package-lock.json", import.meta.url));
  console.log(JSON.stringify({ verdict: "PASS", ...await checkLock(path) }));
}
