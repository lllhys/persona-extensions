import { expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { testFixtures } from "./test-fixtures.ts";
import type { UIStreamDelta, UIStreamEvent, UIStreamOperation, UIStreamRequest, UIStreamSnapshot, UIStreamValue } from "./wire.ts";

// These negative assignments are compile-time checks, not runtime validation/reducer tests.
function typeChecks(): void {
  // @ts-expect-error Unknown operations must never be skipped.
  const unknown: UIStreamOperation = { op: "patch", key: "node" };
  // @ts-expect-error Exactly one native replacement payload is required.
  const missing: UIStreamOperation = { op: "replace", key: "node" };
  // @ts-expect-error Two payloads cannot be combined.
  const two: UIStreamOperation = { op: "replace", key: "node", view: { sections: [] }, section: { key: "node", items: [] } };
  // @ts-expect-error Inserts cannot contain a root View.
  const rootInsert: UIStreamOperation = { op: "insert", parent: "page", view: { sections: [] } };
  // @ts-expect-error Nested containers are not interaction values.
  const nested: UIStreamValue = { nested: {} };
  // @ts-expect-error Functions do not cross the wire.
  const callback: UIStreamValue = () => 1;
  // @ts-expect-error Every frame requires protocol 1.
  const wrong: UIStreamEvent = { protocol: 2, session: "session-a", epoch: "epoch-a", version: 1, event_id: "event-a", key: "node", action: "cb.example", event: "press", value: null };
  // @ts-expect-error Multiple actions on one native node need an explicit callback identifier.
  const missingAction: UIStreamEvent = { protocol: 1, session: "session-a", epoch: "epoch-a", version: 1, event_id: "event-a", key: "node", event: "press", value: null };
  void [unknown, missing, two, rootInsert, nested, callback, wrong, missingAction];
}
void typeChecks;

const examples = testFixtures;
test("stream conformance fixtures preserve zeros, null and empty payloads", () => {
  const files = readdirSync(examples).filter((name) => name.startsWith("UIStream") && name.endsWith(".json"));
  expect(files.length).toBeGreaterThan(15);
  for (const file of files) {
    const frame: UIStreamSnapshot | UIStreamDelta | UIStreamEvent | UIStreamRequest = JSON.parse(readFileSync(new URL(file, examples), "utf8"));
    expect(JSON.parse(JSON.stringify(frame))).toEqual(frame);
  }
  const delta: UIStreamDelta = JSON.parse(readFileSync(new URL("UIStreamDelta.operations.json", examples), "utf8"));
  expect(delta.operations[0].op).toBe("replace");
  const replace = delta.operations[0];
  if (replace.op === "replace") expect(replace.view?.title).toBeUndefined();
  const empty: UIStreamDelta = { protocol: 1, session: "session-a", epoch: "epoch-a", base_version: 1, version: 1, operations: [] };
  expect(JSON.parse(JSON.stringify(empty)).operations).toEqual([]);
});
