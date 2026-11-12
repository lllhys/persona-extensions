import { test } from "node:test";
import assert from "node:assert/strict";
import { packageID, parseCatalog, parseLeaf, parseNuspec, parseSearch, queryText, ReadError, registrationPath, searchPath } from "../src/model.ts";
import { encodeState, initialState, packageTransition, readState, transition } from "../src/page.tsx";
import { catalogResponse, leafResponse, nuspecResponse, stable } from "./fixtures.ts";

test("official search and registration paths validate IDs, queries and stable versions", () => {
  const url = new URL("https://azuresearch-usnc.nuget.org" + searchPath("Newtonsoft.Json", 12));
  assert.equal(url.searchParams.get("q"), "Newtonsoft.Json");
  assert.equal(url.searchParams.get("take"), "12");
  assert.equal(registrationPath("Newtonsoft.Json", "13.0.4"), "/v3/registration5-semver1/newtonsoft.json/13.0.4.json");
  for (const value of ["", "a/b", "a b", "a..b", "../secret", "a\n", 42]) assert.throws(() => packageID(value), ReadError);
  for (const value of ["", "x\n", 42, "x".repeat(101)]) assert.throws(() => queryText(value), ReadError);
});

test("search keeps the reported stable version, not the highest prerelease", () => {
  const result = parseSearch({ data: [stable], totalHits: 1 }, 123);
  assert.equal(result.packages[0].version, "13.0.4");
  assert.equal(result.packages[0].license, "See license link");
  assert.equal(result.packages[0].totalDownloads, 9780413829);
  assert.equal(result.at, 123);
  for (const change of [{ id: "bad/id" }, { totalDownloads: -1 }, { version: "1.0.0\n" }]) {
    assert.throws(() => parseSearch({ data: [{ ...stable, ...change }], totalHits: 1 }, 0), ReadError);
  }
  assert.equal(parseSearch({ data: [{ ...stable, projectUrl: "http://www.newtonsoft.com/json" }], totalHits: 1 }, 0).packages[0].projectURL, undefined);
  assert.equal(parseSearch({ data: [{ ...stable, projectUrl: "javascript:alert(1)" }], totalHits: 1 }, 0).packages[0].projectURL, undefined);
  assert.equal(parseSearch({ data: [{ ...stable, projectUrl: "https://www.newtonsoft.com/json" }], totalHits: 1 }, 0).packages[0].projectURL, "https://www.newtonsoft.com/json");
  assert.throws(() => parseSearch({ data: [stable, stable], totalHits: 2 }, 0), ReadError);
});

test("registration leaf validates package identity and listed state", () => {
  assert.deepEqual(parseLeaf(leafResponse, "Newtonsoft.Json", "13.0.4"), { listed: true });
  assert.throws(() => parseLeaf({ ...leafResponse, registration: "https://api.nuget.org/v3/registration5-semver1/other/index.json" }, "Newtonsoft.Json", "13.0.4"), ReadError);
});

test("nuspec metadata retains license, target frameworks and dependency counts", () => {
  const result = parseNuspec(nuspecResponse, "Newtonsoft.Json", "13.0.4", true);
  assert.equal(result.install, "dotnet add package Newtonsoft.Json --version 13.0.4");
  assert.equal(result.license, "MIT");
  assert.equal(result.dependencies, 10);
  assert.deepEqual(result.frameworks, [".NETFramework2.0", ".NETFramework3.5", ".NETFramework4.0", ".NETFramework4.5", ".NETStandard1.0", ".NETStandard1.3", "net6.0", ".NETStandard2.0"]);
  assert.equal(result.listed, true);
  assert.throws(() => parseNuspec(nuspecResponse.replace("13.0.4", "14.0.1-beta2"), "Newtonsoft.Json", "13.0.4", true), ReadError);
  assert.throws(() => parseNuspec(nuspecResponse.replace("Newtonsoft.Json", "Other"), "Newtonsoft.Json", "13.0.4", true), ReadError);
  assert.equal(parseNuspec(nuspecResponse, "Newtonsoft.Json", "13.0.4", false).listed, false);
});

test("page state accepts only validated search results and HTTPS metadata", () => {
  const state = { q: "Newtonsoft.Json", found: true };
  const result = parseSearch({ data: [stable], totalHits: 1 }, 0);
  const detail = packageTransition(state, result, "package:Newtonsoft.Json");
  assert.equal(detail.package?.id, "Newtonsoft.Json");
  assert.equal(packageTransition(state, result, "package:Other"), state);
  assert.equal(transition(state, "package:Newtonsoft.Json"), state);
  assert.deepEqual(readState(encodeState(detail)), detail);
  assert.deepEqual(readState(encodeState({ ...detail, package: { ...detail.package!, projectURL: "javascript:alert(1)" } })), initialState);
  assert.equal(transition(state, "search", { query: "Newtonsoft.Json" }).found, true);
});
