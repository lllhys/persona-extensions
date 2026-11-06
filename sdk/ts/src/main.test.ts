import { afterAll, expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

// main() reads stdin, so it runs in a child process: a tiny program that echoes a namespace call's text.
const dir = mkdtempSync(join(tmpdir(), "persona-sdk-main-"));
const file = join(dir, "m.ts");
writeFileSync(
  file,
  `import { main } from ${JSON.stringify(join(import.meta.dir, "index.ts"))};
main({ id: "x", version: "1", namespaces: { echo: (_m, p) => ({ text: p.text }) } });
`,
);
afterAll(() => rmSync(dir, { recursive: true, force: true }));

const init = JSON.stringify({ id: "i", method: "initialize", params: { protocol: 1, hub: { build: 0 }, granted: {} } });

// Runs the program with stdin written in the given chunks (then closed) and returns its answers by id.
async function run(chunks: Uint8Array[]) {
  const proc = Bun.spawn([process.execPath, "run", file], { stdin: "pipe", stdout: "pipe", stderr: "inherit" });
  for (const c of chunks) {
    proc.stdin.write(c);
    await proc.stdin.flush();
    await Bun.sleep(50); // so the chunks reach the program as separate reads
  }
  await proc.stdin.end();
  const [out, code] = await Promise.all([new Response(proc.stdout).text(), proc.exited]);
  const answers = new Map<string, any>();
  for (const l of out.split("\n").filter(Boolean)) {
    const f = JSON.parse(l);
    answers.set(f.id, f);
  }
  return { answers, code };
}

test("main answers a last request that has no trailing newline", async () => {
  const { answers, code } = await run([new TextEncoder().encode(`${init}\n${JSON.stringify({ id: "p", method: "ping" })}`)]);
  expect(answers.get("i")?.result?.id).toBe("x");
  expect(answers.get("p")).toEqual({ id: "p", result: {} });
  expect(code).toBe(0);
});

test("main decodes a last line whose multibyte character is split across reads", async () => {
  const text = "好的";
  const bytes = new TextEncoder().encode(`${init}\n${JSON.stringify({ id: "e", method: "echo.say", params: { text } })}`);
  // Cut inside the first character's three UTF-8 bytes.
  const cut = bytes.length - new TextEncoder().encode(`${text}"}}`).length + 1;
  const { answers, code } = await run([bytes.slice(0, cut), bytes.slice(cut)]);
  expect(answers.get("i")?.result?.id).toBe("x");
  expect(answers.get("e")).toEqual({ id: "e", result: { text } });
  expect(code).toBe(0);
});
