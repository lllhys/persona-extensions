import { test } from "node:test";
import assert from "node:assert/strict";
import { parseImages, plain, queryText, ReadError, searchPath } from "../src/model.ts";
import { response } from "./fixtures.ts";
import { encodeState, readState, transition } from "../src/page.tsx";

test("image search validates query and keeps only explicit license metadata", () => {
  const result = parseImages(response, 1000);
  assert.equal(result.images[0].title, "File:Tour Eiffel Wikimedia Commons.jpg");
  assert.equal(result.images[0].description, "The Eiffel Tower in Paris.");
  assert.equal(result.images[0].license, "Public domain");
  assert.equal(result.images[0].pageURL, "https://commons.wikimedia.org/wiki/File:Tour_Eiffel_Wikimedia_Commons.jpg");
  assert.equal(result.images[0].thumbnailURL, "https://upload.wikimedia.org/wikipedia/commons/thumb/a/a8/Tour_Eiffel_Wikimedia_Commons.jpg/640px-Tour_Eiffel_Wikimedia_Commons.jpg");
  assert.equal(result.images[0].width, 640);
  const original = response.query.pages[6926930].imageinfo[0];
  assert.throws(() => parseImages({ query: { pages: { 6926930: { ...response.query.pages[6926930], imageinfo: [{ ...original, thumburl: "https://attacker.invalid/image.jpg" }] } } } }, 0), ReadError);
  assert.throws(() => parseImages({ query: { pages: { 6926930: { ...response.query.pages[6926930], imageinfo: [{ ...original, descriptionurl: "https://attacker.invalid/wiki/File:x" }] } } } }, 0), ReadError);
  const officialThumb = structuredClone(response);
  officialThumb.query.pages[6926930].imageinfo[0].thumburl = "https://thumb.wikimedia.org/wikipedia/commons/thumb/a/a8/Tour_Eiffel_Wikimedia_Commons.jpg/640px-Tour_Eiffel_Wikimedia_Commons.jpg";
  assert.equal(parseImages(officialThumb, 0).images[0].thumbnailURL?.startsWith("https://thumb.wikimedia.org/"), true);
  const noLicense = structuredClone(response);
  Reflect.deleteProperty(noLicense.query.pages[6926930].imageinfo[0].extmetadata, "LicenseShortName");
  assert.equal(parseImages(noLicense, 0).images[0].license, "See file page");
});

test("a file's http or unreadable license link is upgraded or left out, not a failed search", () => {
  const page = response.query.pages[6926930], info = page.imageinfo[0];
  const withLicense = (value: string, id: number) => ({ ...page, pageid: id, title: `File:Example ${id}.jpg`,
    imageinfo: [{ ...info, descriptionurl: `https://commons.wikimedia.org/wiki/File:Example_${id}.jpg`, extmetadata: { ...info.extmetadata, LicenseShortName: { value: "GFDL" }, LicenseUrl: { value } } }] });
  const result = parseImages({ query: { pages: {
    1: withLicense("http://www.gnu.org/copyleft/fdl.html", 1), // as File:Domestic cat felis catus.jpg links it
    2: withLicense("http://creativecommons.org/licenses/by-sa/3.0/", 2),
    3: withLicense("http://example.org/license", 3),
    4: withLicense("not a link", 4),
    5: withLicense("https://user:secret@creativecommons.org/licenses/by/4.0/", 5),
  } } }, 0);
  assert.deepEqual(result.images.map(image => image.licenseURL), ["https://www.gnu.org/copyleft/fdl.html", "https://creativecommons.org/licenses/by-sa/3.0/", undefined, undefined, undefined]);
  assert.deepEqual(result.images.map(image => image.license), ["GFDL", "GFDL", "GFDL", "GFDL", "GFDL"]);
});

test("HTML metadata stays plain data and URLs cannot choose another host", () => {
  assert.equal(plain(" <b>Hello</b>&nbsp; world &unknown; &#x1F600; "), "Hello world &unknown; 😀");
  assert.equal(plain("unterminated <tag"), "unterminated");
  assert.equal(plain("&#0; &#xD800;"), "� �");
  for (const query of ["", " ", "x".repeat(161), "bad\n"]) assert.throws(() => searchPath(query), ReadError);
  assert.throws(() => searchPath("valid", 0), ReadError);
  assert.throws(() => searchPath("valid", 21), ReadError);
  assert.equal(queryText(" Eiffel Tower "), "Eiffel Tower");
});

test("state and legacy callback values preserve validated searches", () => {
  const initial = readState(undefined);
  const found = transition(initial, "search", '{"query":"Eiffel Tower"}');
  assert.equal(found.found, true);
  assert.equal(readState(encodeState(found)).q, "Eiffel Tower");
  for (const values of ["Eiffel", "not-json", "null", "[]", 1, { query: ["Eiffel"] }]) assert.equal(transition(initial, "search", values).found, false);
  assert.equal(transition(found, "home").found, false);
});
