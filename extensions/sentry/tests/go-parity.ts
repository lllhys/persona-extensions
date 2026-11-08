import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { ExactJSON } from "@persona/extension";
import { decodeDelivery, decodeExactConfig, stringifyExact } from "../src/params.ts";
import { event } from "../src/event.ts";
function ordered(value: any): any {
  if (Array.isArray(value)) return value.map(ordered);
  if (value !== null && typeof value === "object") return Object.fromEntries(Object.keys(value).sort().map(key => [key, ordered(value[key])]));
  return value;
}
const oracle = JSON.parse(await readFile(process.argv[2], "utf8")) as { kind: string; raw: string; ok: boolean; events: string[]; config?: any }[];
for (const [index, expected] of oracle.entries()) {
  let accepted = true, actual: any;
  try {
    const raw = ExactJSON.parse(expected.raw);
    if (expected.kind === "config") {
      const cfg = decodeExactConfig(raw);
      actual = { environment: cfg.environment || "production", sample: Math.min(1, Math.max(0, cfg.sample_rate ?? 1)), logs: cfg.include_log_lines ?? true, allow_http: cfg.allow_http };
    } else actual = await Promise.all(decodeDelivery(raw).map(async report => stringifyExact(ordered(await event(report, { environment: "production", client: "persona-sentry/0.1.2" })))));
  } catch { accepted = false; }
  assert.equal(accepted, expected.ok, `case ${index} ${expected.kind}: acceptance differs from unchanged Go`);
  if (accepted) assert.deepEqual(actual, expected.kind === "config" ? expected.config : expected.events, `case ${index} ${expected.kind}: conversion differs from unchanged Go`);
}
console.log(`Sentry full typed Go differential: PASS (${oracle.length} cases; exact int64, scalar/null/pointer/map/slice duplicates, case folding and original clamps)`);
