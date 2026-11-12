import { test } from "node:test";
import assert from "node:assert/strict";
import { barcode, parseProduct, parseSearch, productPath, queryText, ReadError, searchPath } from "../src/model.ts";
import { productResponse, searchResponse } from "./fixtures.ts";
import { encodeState, readState, transition } from "../src/page.tsx";

test("product metadata retains declared allergens, ingredients and canonical links", () => {
  const result = parseSearch(searchResponse, 1000);
  assert.equal(result.products[0].name, "Nutella");
  assert.equal(result.products[0].allergens, "milk, nuts, soybeans");
  assert.equal(result.products[0].nutriscore, "e");
  assert.equal(result.products[0].nova, 3);
  assert.equal(parseProduct(productResponse, "3017620422003", 1000).code, "3017620422003");
  assert.equal(searchPath("chocolate", 12), "/cgi/search.pl?search_terms=chocolate&search_simple=1&action=process&json=1&page_size=12&fields=code%2Cproduct_name%2Cbrands%2Cquantity%2Cnutriscore_grade%2Cnova_group%2Callergens%2Cingredients_text%2Curl");
  assert.equal(productPath("3017620422003"), "/api/v2/product/3017620422003?fields=code%2Cproduct_name%2Cbrands%2Cquantity%2Cnutriscore_grade%2Cnova_group%2Callergens%2Cingredients_text%2Curl");
});

test("untrusted product URLs, wrong barcodes and malformed metadata are rejected", () => {
  const original = searchResponse.products[0];
  for (const raw of [
    { ...original, url: "https://attacker.invalid/product/3017620422003" },
    { ...original, code: "123" }, { ...original, nutriscore_grade: "z" }, { ...original, nova_group: 5 },
    { ...original, product_name: 42 },
  ]) assert.throws(() => parseSearch({ count: 1, products: [raw] }, 0), ReadError);
  assert.throws(() => parseProduct(productResponse, "8052575090230", 0), ReadError);
  assert.throws(() => parseProduct({ status: 0, status_verbose: "product not found" }, "3017620422003", 0), ReadError);
  const duplicate = parseSearch({ count: 2, products: [original, original] }, 0);
  assert.equal(duplicate.products.length, 1);
});

test("a search result without a product name is left out, not a failed search", () => {
  // The twelve results for 可口可乐 include 6901939621608, which has no product_name.
  const original = searchResponse.products[0];
  const { product_name: _, ...nameless } = { ...original, code: "6901939621608" };
  const result = parseSearch({ count: 3, products: [nameless, { ...nameless, code: "6901939621609", product_name: "  " }, original] }, 0);
  assert.deepEqual(result.products.map(product => product.code), [original.code]);
  assert.equal(result.total, 3);
  assert.throws(() => parseProduct({ ...productResponse, product: nameless }, "6901939621608", 0), ReadError);
});

test("a UPC-A barcode reads the product Open Food Facts answers as its EAN-13", () => {
  // GET /api/v2/product/737628064502 answers "code": "0737628064502".
  const upc = { status: 1, status_verbose: "product found", product: { ...productResponse.product, code: "0737628064502", url: undefined } };
  const found = parseProduct(upc, "737628064502", 0);
  assert.equal(found.code, "0737628064502");
  assert.equal(found.url, "https://world.openfoodfacts.org/product/0737628064502");
  assert.throws(() => parseProduct({ ...upc, product: { ...upc.product, code: "0737628064503" } }, "737628064502", 0), ReadError);
});

test("query and barcode validation rejects controls and unbounded input", () => {
  for (const query of ["", " ", "x".repeat(121), "bad\n"]) assert.throws(() => queryText(query), ReadError);
  assert.equal(queryText(" dark chocolate "), "dark chocolate");
  assert.equal(barcode("3017620422003"), "3017620422003");
  for (const code of ["123", "abc12345678", "3017620422003\n", "1".repeat(15)]) assert.throws(() => barcode(code), ReadError);
  assert.throws(() => searchPath("valid", 0), ReadError);
  assert.throws(() => searchPath("valid", 21), ReadError);
});

test("state and legacy callback values preserve validated product searches", () => {
  const initial = readState(undefined);
  const found = transition(initial, "search", '{"query":"chocolate"}');
  assert.equal(found.found, true);
  assert.equal(readState(encodeState(found)).q, "chocolate");
  for (const values of ["chocolate", "not-json", "null", "[]", 1, { query: ["chocolate"] }, { query: "bad\n" }]) assert.equal(transition(initial, "search", values).found, false);
  assert.equal(transition(found, "home").found, false);
});
