import { test } from "node:test";
import assert from "node:assert/strict";
import { domainName, lookupURL, parseRegistration, rdapService, ReadError, statusURL } from "../src/model.ts";
import { bootstrap, registration } from "./fixtures.ts";
import { decodeHistory, encodeHistory, MemoryHistory } from "../src/store.ts";
import { encodeState, readState, transition } from "../src/page.tsx";

test("one domain is normalized and IDNs are kept separate from URLs", () => {
  assert.deepEqual(domainName("EXAMPLE.com."), { ascii: "example.com", unicode: "example.com" });
  assert.equal(domainName("例子.中国").ascii, "xn--fsqu00a.xn--fiqs8s");
  assert.equal(domainName("bücher.de").ascii, "xn--bcher-kva.de");
  for (const value of ["", "example", "https://example.com", "example.com/path", "user@example.com", "*.example.com", "127.0.0.1", "::1", "bad_foo.example.com", "bad-.example.com", "bad\n.example.com", "-bad.example.com", "a..com", "example.com."]) {
    if (value === "example.com.") continue;
    assert.throws(() => domainName(value), ReadError, value);
  }
});
test("IANA selects exactly one HTTPS RDAP base without arbitrary redirect", () => {
  const service = rdapService(bootstrap, "example.com");
  assert.equal(service.base, "https://rdap.verisign.com/com/v1/");
  assert.equal(lookupURL(service, "example.com"), "https://rdap.verisign.com/com/v1/domain/example.com");
  assert.throws(() => rdapService(bootstrap, "name.unsupported"), ReadError);
  assert.throws(() => rdapService({ services: [[["com"], ["http://registry.example.com/"]]] }, "example.com"), ReadError);
  assert.throws(() => rdapService({ services: [[["com"], ["https://one.example.com/", "https://two.example.com/"]]] }, "example.com"), ReadError);
  assert.equal(rdapService(bootstrap, "example.net").base, "https://rdap.verisign.com/net/v1/");
  assert.throws(() => rdapService({ services: [[["com"], ["https://user:password@registry.example.com/"]]] }, "example.com"), ReadError);
});
test("public registration records retain dates and statuses without inventing availability", () => {
  const result = parseRegistration(registration, "example.com", 1000);
  assert.equal(result.registrar, "Example Registrar");
  assert.equal(result.statuses.length, 2);
  assert.equal(result.events[1].action, "expiration");
  assert.equal(result.url, "https://rdap.verisign.com/com/v1/domain/example.com");
  assert.equal(statusURL, "https://www.iana.org/assignments/rdap-status-values/rdap-status-values.xhtml");
  assert.throws(() => parseRegistration({ ...registration, ldhName: "attacker.example" }, "example.com", 0), ReadError);
  assert.throws(() => parseRegistration({ ...registration, links: [{ rel: "self", href: "http://localhost/" }] }, "example.com", 0), ReadError);
  assert.throws(() => parseRegistration({ ...registration, events: [{ eventAction: "registration", eventDate: "not-a-date" }] }, "example.com", 0), ReadError);
  // Date.parse would roll this into March; an RDAP date must be a real calendar day.
  assert.throws(() => parseRegistration({ ...registration, events: [{ eventAction: "registration", eventDate: "2026-02-30T00:00:00Z" }] }, "example.com", 0), ReadError);
  for (const raw of [null, [], {}, { objectClassName: "entity" }]) assert.throws(() => parseRegistration(raw, "example.com", 0), ReadError);
});
test("history is bounded, normalized and independent of external accounts", async () => {
  const history = new MemoryHistory();
  await history.add("EXAMPLE.com");
  await history.add("example.com");
  assert.deepEqual((await history.load()).domains, ["example.com"]);
  const stored = decodeHistory(encodeHistory(await history.load()));
  assert.deepEqual(stored, { schema_version: 1, domains: ["example.com"] });
  await history.clear();
  assert.deepEqual((await history.load()).domains, []);
  assert.throws(() => decodeHistory("bad"), ReadError);
});
test("navigation accepts only one valid domain and keeps history reachable", () => {
  const searched = transition(readState(undefined), "search", { domain: "EXAMPLE.com" });
  assert.deepEqual(searched, { v: "result", q: "example.com" });
  assert.equal(readState(encodeState(searched)).v, "result");
  assert.equal(transition(searched, "back").v, "lookup");
  assert.equal(transition(searched, "history").v, "history");
  for (const value of ["", "example", "https://example.com", "bad\n.com", 2, null, { domain: ["example.com"] }]) assert.equal(transition(readState(undefined), "search", value).v, "lookup");
  assert.equal(transition(readState(undefined), "domain:example.com").v, "result");
  assert.equal(transition(readState(undefined), "domain:https://attacker.invalid").v, "lookup");
});
