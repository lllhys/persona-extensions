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
const waitingFile = join(dir, "waiting.ts");
writeFileSync(waitingFile, `import { main } from ${JSON.stringify(join(import.meta.dir, "index.ts"))};
let hub;
// Keep the runtime alive so an unresolved drain cannot be hidden by automatic process exit.
setInterval(() => {}, 100);
main({ id: "x", version: "1", init: (_init, h) => { hub = h; },
  namespaces: { wait: () => hub.call("fixture.read") } });
`);
afterAll(() => rmSync(dir, { recursive: true, force: true }));

const init = JSON.stringify({ id: "i", method: "initialize", params: { protocol: 1, hub: { build: 0 }, granted: {} } });

// Runs the program with stdin written in the given chunks (then closed) and returns its answers by id.
async function run(chunks: Uint8Array[], entry = file) {
  const proc = Bun.spawn([process.execPath, "run", entry], { stdin: "pipe", stdout: "pipe", stderr: "inherit" });
  const timeout = setTimeout(() => proc.kill("SIGKILL"), 2000);
  try {
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
  } finally {
    proc.kill("SIGKILL");
    await proc.exited;
    clearTimeout(timeout);
  }
}

test("main rejects unreachable hub calls before draining requests at EOF", async () => {
  const request = JSON.stringify({ id: "w", method: "wait.read" });
  const { answers, code } = await run([new TextEncoder().encode(`${init}\n${request}\n`)], waitingFile);
  expect(answers.get("i")?.result?.id).toBe("x");
  expect(answers.get("p1")?.method).toBe("fixture.read");
  expect(code).toBe(0);
  expect(answers.get("w")).toEqual({ id: "w", error: { message: "the hub has gone" } });
});

test("main answers a final unterminated handler whose hub call is unreachable", async () => {
  const request = JSON.stringify({ id: "w", method: "wait.read" });
  const { answers, code } = await run([new TextEncoder().encode(`${init}\n${request}`)], waitingFile);
  expect(answers.get("i")?.result?.id).toBe("x");
  expect(answers.get("w")).toEqual({ id: "w", error: { message: "the hub has gone" } });
  expect(code).toBe(0);
});

test("main consumes a final unterminated hub reply before shutting down", async () => {
  const proc = Bun.spawn([process.execPath, "run", waitingFile], { stdin: "pipe", stdout: "pipe", stderr: "inherit" });
  const timeout = setTimeout(() => proc.kill("SIGKILL"), 2000);
  try {
    proc.stdin.write(`${init}\n${JSON.stringify({ id: "w", method: "wait.read" })}\n`);
    await proc.stdin.flush();
    const frames: any[] = [];
    const decoder = new TextDecoder();
    let buf = "";
    for await (const chunk of proc.stdout) {
      buf += decoder.decode(chunk, { stream: true });
      let nl: number;
      while ((nl = buf.indexOf("\n")) >= 0) {
        const frame = JSON.parse(buf.slice(0, nl));
        buf = buf.slice(nl + 1);
        frames.push(frame);
        if (frame.method === "fixture.read") {
          proc.stdin.write(JSON.stringify({ id: frame.id, result: { value: "reachable" } }));
          await proc.stdin.end();
        }
      }
    }
    expect(await proc.exited).toBe(0);
    expect(frames.find(frame => frame.id === "w")).toEqual({ id: "w", result: { value: "reachable" } });
  } finally {
    proc.kill("SIGKILL");
    await proc.exited;
    clearTimeout(timeout);
  }
});

test("main flushes a large final answer before EOF exit with a delayed stdout reader", async () => {
  const entry = join(dir, "large.ts");
  const size = 4 * 1024 * 1024;
  writeFileSync(entry, `import { main } from ${JSON.stringify(join(import.meta.dir, "index.ts"))};
main({ id: "x", version: "1", namespaces: { fixture: () => ({ text: "x".repeat(${size}) }) } });
`);
  const proc = Bun.spawn([process.execPath, "run", entry], { stdin: "pipe", stdout: "pipe", stderr: "inherit" });
  const timeout = setTimeout(() => proc.kill("SIGKILL"), 2000);
  try {
    proc.stdin.write(`${init}\n${JSON.stringify({ id: "large", method: "fixture.read" })}`);
    await proc.stdin.end();
    await Bun.sleep(150);
    const out = await new Response(proc.stdout).text();
    expect(await proc.exited).toBe(0);
    const frames = out.trim().split("\n").map(line => JSON.parse(line));
    expect(frames.find(frame => frame.id === "large")?.result?.text.length).toBe(size);
  } finally {
    proc.kill("SIGKILL");
    await proc.exited;
    clearTimeout(timeout);
  }
});

test("main keeps HTTP-only serving after empty stdin EOF", async () => {
  const entry = join(dir, "http.ts"), socket = join(dir, "http.sock");
  writeFileSync(entry, `import { main } from ${JSON.stringify(join(import.meta.dir, "index.ts"))};
main({ id: "x", version: "1", fetch: () => new Response("fixture") }).then(() => console.log("ready"));
`);
  const proc = Bun.spawn([process.execPath, "run", entry], { stdin: "pipe", stdout: "pipe", stderr: "inherit",
    env: { ...process.env, PERSONA_EXTENSION_SOCKET: socket } });
  const timeout = setTimeout(() => proc.kill("SIGKILL"), 2000);
  try {
    await proc.stdin.end();
    const reader = proc.stdout.getReader();
    const ready = await reader.read();
    reader.releaseLock();
    expect(new TextDecoder().decode(ready.value).trim()).toBe("ready");
    const response = await fetch("http://localhost/health", { unix: socket, signal: AbortSignal.timeout(500) });
    expect(await response.text()).toBe("ok");
    const page = await fetch("http://localhost/fixture", { unix: socket, signal: AbortSignal.timeout(500) });
    expect(await page.text()).toBe("fixture");
    expect(proc.exitCode).toBeNull();
    proc.kill("SIGTERM");
    expect(await proc.exited).toBe(0);
  } finally {
    proc.kill("SIGKILL");
    await proc.exited;
    clearTimeout(timeout);
  }
});

test("main answers a last request that has no trailing newline", async () => {
  const { answers, code } = await run([new TextEncoder().encode(`${init}\n${JSON.stringify({ id: "p", method: "ping" })}`)]);
  expect(answers.get("i")?.result?.id).toBe("x");
  expect(answers.get("p")).toEqual({ id: "p", result: {} });
  expect(code).toBe(0);
});

test("main ignores null and invalid frames and still answers later requests", async () => {
  const invalid = ["null", "[]", "true", '{"event":1}', '{"method":true}', "{"];
  const { answers, code } = await run([new TextEncoder().encode(`${invalid.join("\n")}\n${init}\n${JSON.stringify({ id: "p", method: "ping" })}`)]);
  expect(answers.size).toBe(2);
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
