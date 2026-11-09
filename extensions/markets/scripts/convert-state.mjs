import { readFile, open, realpath } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
// Explicit offline conversion only. Never reads ambient directories, contacts a hub or replaces either input.
const args = process.argv.slice(2);
if (args.length !== 3) throw new Error("Usage: node scripts/convert-state.mjs <watchlist.json> <alerts.json> <new-output-file>");
const [watch, alerts, output] = args.map(path => resolve(path));
const input = async path => { const bytes = await readFile(path); if (bytes.length > 256 * 1024) throw new Error("Legacy state exceeds size limit"); return parseStateJSON(new TextDecoder("utf-8", { fatal: true }).decode(bytes)); };
const [watchPath, alertsPath] = await Promise.all([realpath(watch), realpath(alerts)]);
if (output === watchPath || output === alertsPath) throw new Error("Output must be a new file");
const bundle = fileURLToPath(new URL("../build/markets.mjs", import.meta.url));
const { encodeState, legacyState, parseStateJSON } = await import(pathToFileURL(bundle).href);
const state = legacyState(await input(watchPath), await input(alertsPath));
const data = encodeState(state);
const file = await open(output, "wx", 0o600);
try { await file.writeFile(JSON.stringify({ contribution: "markets", data }) + "\n", "utf8"); } finally { await file.close(); }
console.log(JSON.stringify({ watchlist_entries: state.watchlist.length, alerts: state.alerts.length, result: "Converted locally; encrypted host import still required" }));
