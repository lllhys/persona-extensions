import { test } from "node:test";
import assert from "node:assert/strict";
import { storageBytes } from "@persona/extension";
import { decodeHistory, DomainHistory } from "../src/store.ts";
import { ReadError } from "../src/model.ts";
class FakeHub {
  data: string | undefined;
  revision = 0;
  reads = 0;
  requests = 0;
  conflict = false;
  calls: { method: string; params: any }[] = [];
  async call<T>(method: string, params: any): Promise<T> {
    this.calls.push({ method, params });
    if (method === "storage.get") {
      this.reads++; await new Promise(resolve => setTimeout(resolve, 2));
      if (!this.data) throw Object.assign(new Error("missing"), { code: "not_found" });
      return { revision: `r${this.revision}`, data: this.data } as T;
    }
    if (method === "storage.request") return { request_id: `request-${++this.requests}` } as T;
    if (method === "storage.transaction") {
      const operation = params.operations[0];
      if (this.conflict || operation.revision !== (this.revision ? `r${this.revision}` : "")) throw Object.assign(new Error("conflict"), { code: "storage_conflict" });
      decodeHistory(operation.data); this.data = operation.data; this.revision++;
      return { items: [{ key: "history", revision: `r${this.revision}` }] } as T;
    }
    throw new Error("unexpected method");
  }
}
test("history mutations use CAS and recover from another page's write", async () => {
  const hub = new FakeHub(), first = new DomainHistory(hub), second = new DomainHistory(hub);
  await Promise.all([first.load(), first.add("EXAMPLE.com")]);
  assert.equal(hub.reads, 1);
  await second.load();
  await first.add("other.example");
  await second.add("third.example");
  assert.deepEqual(decodeHistory(hub.data!).domains, ["third.example", "other.example", "example.com"]);
  assert.equal(hub.requests, 4);
  await second.clear();
  assert.deepEqual((await second.load()).domains, []);
  assert.ok(hub.calls.every(call => ["storage.get", "storage.request", "storage.transaction"].includes(call.method)));
});
test("persistent CAS conflict does not silently discard committed history", async () => {
  const hub = new FakeHub(), store = new DomainHistory(hub);
  await store.add("example.com");
  hub.conflict = true;
  await assert.rejects(store.add("other.example"), (error: unknown) => error instanceof Error && (error as { code?: string }).code === "storage_conflict");
  assert.deepEqual(decodeHistory(hub.data!).domains, ["example.com"]);
  hub.conflict = false;
  await store.add("other.example");
  assert.equal(decodeHistory(hub.data!).domains.length, 2);
});
test("stored values are bounded UTF-8 domain metadata only", () => {
  const encoded = storageBytes(new TextEncoder().encode(JSON.stringify({ schema_version: 1, domains: ["example.com"] })));
  assert.ok(encoded.length < 1000);
  assert.throws(() => decodeHistory(encoded + "x"), ReadError);
});
