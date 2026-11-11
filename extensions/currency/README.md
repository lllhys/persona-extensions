# Currency and units

Original native TypeScript/JSX extension using the official `@persona/extension` SDK. No Raycast code is included.

Convert supported currencies with dated European Central Bank reference rates provided by Frankfurter. Length, mass, temperature, volume, area, speed, duration and data conversions run locally on the hub. The assistant tools are `currency_convert` and `unit_convert`.

The UI supports English, Simplified Chinese and Japanese. Currency codes and units are validated; amounts are non-negative decimal strings with at most 18 decimal places. Local calculations use decimal integer arithmetic rather than binary floating-point multiplication.

## Development

From this directory, with the repository SDK dependencies installed:

```sh
npm ci --ignore-scripts
npm test
```

The test script typechecks both JSX modes, creates the Deno source bundle, runs the official SDK source authoring check and executes model, service and protocol/UI tests. `scripts/build-source.mjs` also creates `build/currency-source.tar.gz`, a deterministic development archive with the SDK authoring closure and a committed `source-reference/bundle.mjs` for independent byte-for-byte rebuilds. Generated build files are not committed.

Network permission is limited to `api.frankfurter.dev`. No API key is used. A failed refresh may display the previous cached rate with an explicit stale warning. Unit conversions do not make network requests.

## Data and limits

Rates come from Frankfurter's ECB-only endpoint (`/v2/providers/ecb/rates`). The page shows the publication date and links to [Frankfurter](https://frankfurter.dev/). These are reference rates, not executable market quotes or financial advice.

The supported currencies are those published by the ECB endpoint, including USD, EUR, CNY, JPY and GBP. Common aliases are accepted for local units. `KB`, `MB` and `GB` are decimal SI units; `KiB`, `MiB` and `GiB` are binary units. Temperature conversion includes Celsius, Fahrenheit and kelvin offsets. Mismatched dimensions and unsupported currencies are rejected.

Development installation and isolated verification do not imply production activation or production resource-isolation acceptance.
