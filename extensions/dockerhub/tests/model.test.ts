import { test } from "node:test";
import assert from "node:assert/strict";
import { parseRepository, parseSearch, parseTags, queryText, ReadError, repositoryName, repositoryPath, searchPath, tagsPath } from "../src/model.ts";
import { imageTransition, encodeState, initialState, readState, transition } from "../src/page.tsx";
import { detailResponse, ingress, nginx, tag, tagsResponse } from "./fixtures.ts";

test("official JSON requests bound queries and repository names", () => {
  const url = new URL("https://hub.docker.com" + searchPath("nginx ingress", 12));
  assert.equal(url.searchParams.get("query"), "nginx ingress");
  assert.equal(url.searchParams.get("page_size"), "12");
  assert.equal(repositoryPath("library/nginx"), "/v2/repositories/library/nginx/");
  assert.equal(tagsPath("library/nginx"), "/v2/repositories/library/nginx/tags?page_size=10");
  for (const value of ["nginx", "a/b/c", "library/a b", "a\n/b", "../secret", 42]) assert.throws(() => repositoryName(value), ReadError);
  for (const value of ["", "x\n", 42, "x".repeat(101)]) assert.throws(() => queryText(value), ReadError);
});

test("search keeps bounded metadata and rejects duplicate or invalid repositories", () => {
  const result = parseSearch({ results: [nginx, ingress], count: 2 }, 123);
  assert.equal(result.images[0].name, "library/nginx");
  assert.equal(result.images[0].official, true);
  assert.equal(result.images[0].pulls, 13573379546);
  assert.equal(result.images[1].official, false);
  for (const change of [{ repo_name: "bad/name/extra" }, { pull_count: -1 }, { is_official: "yes" }]) assert.throws(() => parseSearch({ results: [{ ...nginx, ...change }], count: 1 }, 0), ReadError);
  assert.throws(() => parseSearch({ results: [nginx, nginx], count: 2 }, 0), ReadError);
});

test("repository identity and current metadata are verified", () => {
  const result = parseRepository(detailResponse, "library/nginx");
  assert.equal(result.pull, "docker pull library/nginx");
  assert.equal(result.official, true);
  assert.equal(result.updated, "2026-11-10T04:52:25.484949Z");
  assert.throws(() => parseRepository({ ...detailResponse, name: "other" }, "library/nginx"), ReadError);
  assert.throws(() => parseRepository({ ...detailResponse, namespace: "other" }, "library/nginx"), ReadError);
});

test("tag digest is distinct from validated platform image digests", () => {
  const tags = parseTags(tagsResponse, "library/nginx");
  assert.equal(tags[0].digest, "sha256:e2355aa95529a7999dad94ddcb45a8768abf6fb0b204c93b1a3ffce477d7b893");
  assert.deepEqual(tags[0].platforms, ["linux/amd64", "linux/arm64/v8"]);
  assert.equal(tags[0].platforms.includes("unknown/unknown"), false);
  for (const change of [{ digest: "latest" }, { name: "../latest" }]) assert.throws(() => parseTags({ results: [{ ...tag, ...change }] }, "library/nginx"), ReadError);
  // A signature, attestation or metadata artifact (as bitnami/redis lists them) or an inactive tag is left out, not a failure.
  const artifact = { name: "sha256-59078af5aec997137eb95f34fed376487b471e05c9a244c2812a13598c434950.metadata", content_type: "unrecognized", tag_status: "active", digest: "sha256:" + "1".repeat(64), images: [] };
  for (const other of [artifact, { ...artifact, name: "latest-metadata" }, { ...tag, name: "old", tag_status: "inactive" }, { ...tag, name: "fs", content_type: "filesystem" }]) {
    assert.deepEqual(parseTags({ results: [other, tag] }, "library/nginx").map(value => value.name), [tag.name]);
  }
  assert.throws(() => parseTags({ results: [{ ...tag, images: [{ ...tag.images[0], size: -1 }] }] }, "library/nginx"), ReadError);
});

test("page state accepts only validated search results", () => {
  const state = { q: "nginx", found: true };
  const result = parseSearch({ results: [nginx, ingress], count: 2 }, 0);
  const detail = imageTransition(state, result, "image:nginx/nginx-ingress");
  assert.equal(detail.image?.name, "nginx/nginx-ingress");
  assert.equal(imageTransition(state, result, "image:other/unknown"), state);
  assert.equal(transition(state, "image:library/nginx"), state);
  assert.deepEqual(readState(encodeState(detail)), detail);
  assert.deepEqual(readState(encodeState({ ...detail, image: { ...detail.image!, name: "not-a-repository" } })), initialState);
  assert.equal(transition(state, "search", { query: "nginx" }).found, true);
});
