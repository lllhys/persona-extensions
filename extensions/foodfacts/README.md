# Open Food Facts

Original native TypeScript/JSX extension using the official `@persona/extension` SDK. No Raycast code is included.

Search by product name or barcode to view brand, quantity, ingredients, declared allergens, optional Nutri-Score and NOVA metadata, and canonical product links. The assistant tools are `foodfacts_search` and `foodfacts_product`.

The UI supports English, Simplified Chinese and Japanese. Queries, barcodes, response sizes, result counts and cache usage are bounded. Requests are paced, cancellable and limited to the official public service.

## Development

From this directory, with the repository SDK dependencies installed:

```sh
npm ci --ignore-scripts
npm test
```

The test script typechecks both JSX modes, creates the Deno source bundle, runs the official SDK source authoring check and executes model, service and protocol/UI tests. `scripts/build-source.mjs` also creates a deterministic development archive with the SDK authoring closure and a committed `source-reference/bundle.mjs` for independent byte-for-byte rebuilds. Generated build files are not committed.

Network permission is limited to `world.openfoodfacts.org`. No API key is used. Empty search results do not issue a request until the user submits a query. A failed refresh may show a cached result with a stale warning.

## Data and limits

Product data comes from the public Open Food Facts API and is crowd-sourced under the Open Database License. The page retains attribution and links to the original product.

Missing ingredient or allergen metadata does **not** mean an allergen is absent. Nutri-Score and NOVA are shown only when the service supplies them. Check the actual package for allergies, dietary decisions and current ingredients; this extension provides product metadata, not a safety guarantee or dietary advice.

Development installation and isolated verification do not imply production activation or production resource-isolation acceptance.
