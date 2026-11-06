import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import { connect } from "@persona/extension";

const root = fileURLToPath(new URL("../", import.meta.url));
const artifact = fileURLToPath(new URL("../.source-dist/showcase/bundle/main.mjs", import.meta.url));
function build() {
  const result = spawnSync(process.execPath, ["scripts/build-source.mjs"], { cwd: root, encoding: "utf8", timeout: 30000 });
  assert.equal(result.status, 0, result.error?.message || result.stderr);
}
test("locked source bundle is reproducible, offline and portable without package resolution", async () => {
  build();
  const first = readFileSync(artifact), archive = readFileSync(root + ".source-dist/showcase-source-draft.tar.gz");
  build();
  assert.deepEqual(readFileSync(artifact), first);
  assert.deepEqual(readFileSync(root + ".source-dist/showcase-source-draft.tar.gz"), archive);
  const manifest = JSON.parse(readFileSync(root + ".source-dist/showcase/persona-extension.json", "utf8"));
  assert.deepEqual(manifest.runtime, { mode: "deno", source: { entry: "src/main.tsx", bundle: "bundle/main.mjs", sha256: createHash("sha256").update(first).digest("hex") } });
  assert.equal(manifest.version, "0.1.3"); assert.equal(manifest.permissions, undefined); assert.equal(manifest.tags, undefined);
  assert.equal(manifest.contributes.ui.length, 2);
  assert.ok(manifest.contributes.ui.every((ui: any) => ui.id !== "showcase" && ui.opens === undefined));
  assert.ok(manifest.contributes.shortcuts.every((shortcut: any) => shortcut.ui === undefined));
  assert.doesNotMatch(first.toString(), /\b(?:require|import)\s*\(/);
  const compiled = await import(artifact);
  assert.equal(compiled.default.id, "showcase");
  // The bundled SDK owns its element symbols; use its own system-snapshot renderer.
  const view = await compiled.default.ui["showcase-widget"].render({ size: "large", locale: "ja" });
  assert.equal(view.sections[0].items.length, 4);
  const replies: any[] = [], protocol = connect(compiled.default, line => replies.push(JSON.parse(line)));
  try {
    protocol.line(JSON.stringify({ id: "1", method: "initialize", params: { protocol: 1, hub: { build: 1, extension_points: {}, locale: "en" }, granted: {} } }));
    await protocol.drain(); assert.equal(replies[0].result.id, "showcase");
    protocol.line(JSON.stringify({ id: "2", method: "shortcut.run", params: { contribution: "showcase-count", values: { n: "3", unit: "star" }, locale: "en" } }));
    await protocol.drain(); assert.equal(replies[1].result.text, "Counted 3 stars");
  } finally { protocol.close(); }
});
