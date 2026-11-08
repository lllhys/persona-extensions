import { createInterface } from "node:readline";
import { connect } from "@persona/extension";
import { createProgram } from "../src/index.ts";
import { Fixture } from "./fixtures.ts";

// Isolated manager integration fixture, never a production launcher.
const fixture = new Fixture(); fixture.delay = 200;
const program = createProgram(fixture.fetch, { gap: 0, now: () => fixture.time });
program.namespaces!.test = method => {
  if (method !== "stats") throw new Error("unknown synthetic probe");
  return { requests: fixture.hits.get("/hot.rss") ?? 0, active: fixture.active, cancelled: fixture.cancelled };
};
const protocol = connect(program, line => process.stdout.write(line + "\n"));
const reader = createInterface({ input: process.stdin });
for await (const line of reader) protocol.line(line);
protocol.close();
