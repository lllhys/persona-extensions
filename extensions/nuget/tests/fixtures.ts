import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
import { nuspecPath, registrationPath, searchPath } from "../src/model.ts";
export const stable = {
  id: "Newtonsoft.Json", version: "13.0.4", description: "Json.NET is a popular high-performance JSON framework for .NET",
  projectUrl: "https://www.newtonsoft.com/json", licenseUrl: "https://www.nuget.org/packages/Newtonsoft.Json/13.0.4/license",
  totalDownloads: 9780413829, published: "2025-09-16T08:13:09.313Z"
};
export const other = { ...stable, id: "Newtonsoft.Json.Bson", version: "1.0.3", description: "Json.NET BSON", totalDownloads: 1469946241 };
export const searchResponse = { totalHits: 1005, data: [stable, other] };
export const leafResponse = {
  "@id": "https://api.nuget.org/v3/registration5-semver1/newtonsoft.json/13.0.4.json",
  listed: true,
  registration: "https://api.nuget.org/v3/registration5-semver1/newtonsoft.json/index.json"
};
export const catalogResponse = {
  ...stable, licenseExpression: "MIT", licenseUrl: "https://licenses.nuget.org/MIT", listed: true,
  dependencyGroups: [
    { targetFramework: ".NETFramework2.0" },
    { targetFramework: ".NETStandard1.0", dependencies: [{ id: "Microsoft.CSharp", range: "[4.3.0, )" }, { id: "NETStandard.Library", range: "[1.6.1, )" }] },
    { targetFramework: ".NETStandard1.3", dependencies: [{ id: "Microsoft.CSharp", range: "[4.3.0, )" }, { id: "NETStandard.Library", range: "[1.6.1, )" }] },
    { targetFramework: "net6.0" },
    { targetFramework: ".NETStandard2.0" }
  ]
};
export const searchPathFixture = searchPath("Newtonsoft.Json", 12);
export const search20Path = searchPath("Newtonsoft.Json", 20);
export const leafPath = registrationPath("Newtonsoft.Json", "13.0.4");
export const catalogPath = nuspecPath("Newtonsoft.Json", "13.0.4");
export const nuspecResponse = `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd">
  <metadata minClientVersion="2.12">
    <id>Newtonsoft.Json</id>
    <version>13.0.4</version>
    <title>Json.NET</title>
    <license type="expression">MIT</license>
    <licenseUrl>https://licenses.nuget.org/MIT</licenseUrl>
    <projectUrl>https://www.newtonsoft.com/json</projectUrl>
    <description>Json.NET is a popular high-performance JSON framework for .NET</description>
    <published>2025-09-16T08:13:09.313Z</published>
    <dependencies>
      <group targetFramework=".NETFramework2.0" />
      <group targetFramework=".NETFramework3.5" />
      <group targetFramework=".NETFramework4.0" />
      <group targetFramework=".NETFramework4.5" />
      <group targetFramework=".NETStandard1.0">
        <dependency id="Microsoft.CSharp" version="4.3.0" />
        <dependency id="NETStandard.Library" version="1.6.1" />
        <dependency id="System.ComponentModel.TypeConverter" version="4.3.0" />
        <dependency id="System.Runtime.Serialization.Primitives" version="4.3.0" />
      </group>
      <group targetFramework=".NETStandard1.3">
        <dependency id="Microsoft.CSharp" version="4.3.0" />
        <dependency id="NETStandard.Library" version="1.6.1" />
        <dependency id="System.ComponentModel.TypeConverter" version="4.3.0" />
        <dependency id="System.Runtime.Serialization.Formatters" version="4.3.0" />
        <dependency id="System.Runtime.Serialization.Primitives" version="4.3.0" />
        <dependency id="System.Xml.XmlDocument" version="4.3.0" />
      </group>
      <group targetFramework="net6.0" />
      <group targetFramework=".NETStandard2.0" />
    </dependencies>
  </metadata>
</package>`;
export class Fixture {
  clock = Date.UTC(2026, 10, 11);
  requests: { url: URL; init: RequestInit }[] = [];
  hits = new Map<string, number>();
  responses = new Map<string, { body: string; status?: number; delay?: number }>();
  cancelled = 0;
  constructor() {
    this.put(searchPathFixture, searchResponse);
    this.put(search20Path, searchResponse);
    this.put(searchPath("Newtonsoft.Json", 10), searchResponse);
    this.put(leafPath, leafResponse);
    this.put(catalogPath, nuspecResponse);
  }
  put(path: string, value: unknown, options: { status?: number; delay?: number } = {}) { this.responses.set(path, { body: typeof value === "string" ? value : JSON.stringify(value), ...options }); }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input), path = url.pathname + url.search;
    this.requests.push({ url, init }); this.hits.set(url.origin + path, (this.hits.get(url.origin + path) ?? 0) + 1);
    if (!["https://azuresearch-usnc.nuget.org", "https://api.nuget.org"].includes(url.origin)) throw new Error("unexpected origin");
    const answer = this.responses.get(path);
    if (!answer) return new Response("not found", { status: 404 });
    const signal = init.signal;
    if (answer.delay || signal.aborted) {
      await new Promise<void>((resolve, reject) => {
        const cancel = () => { clearTimeout(timer); this.cancelled++; reject(signal.reason); };
        const timer = setTimeout(resolve, answer.delay ?? 20);
        if (signal.aborted) cancel(); else signal.addEventListener("abort", cancel, { once: true });
      });
    }
    return new Response(answer.body, { status: answer.status ?? 200, headers: { "Content-Type": "application/json" } });
  };
}
export const until = async <T>(read: () => Promise<T>, ready: (value: T) => boolean): Promise<T> => {
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) { const value = await read(); if (ready(value)) return value; await new Promise(resolve => setTimeout(resolve, 10)); }
  throw new Error("condition did not settle");
};
export function interaction(snapshot: Snapshot, key: string, values: Record<string, string> = {}): UIEvent {
  const item = snapshot.view.sections.flatMap(section => section.items).find(item => item.key === key);
  if (!item || !("action" in item) || !item.action) throw new Error("missing action " + key);
  return { generation: snapshot.generation, version: snapshot.version, key, actionId: item.action.id, eventId: crypto.randomUUID(), values };
}
