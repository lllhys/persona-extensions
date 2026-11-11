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
