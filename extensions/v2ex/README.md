# V2EX

Read public V2EX topics in Persona, without an account: hot, latest and followed-node lists, an in-app topic reader and assistant tools.

- [Extension guide and configuration](../../docs/extensions/v2ex.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)

## Unreleased source profile

The TypeScript Program shares a bounded service between connector tools and the
native `topics` page. Existing settings, source lists, contribution IDs, grants,
localizations and request deadlines remain unchanged. UI versions below 3 open a
topic inline; later versions push a native topic session and retain the parent.
Stable keys, cached resumes and replayable native callback receipts are preserved.
Lists page through all 200 retained topics without increasing aggregate frame limits.

From this extension directory, run:

```sh
npm --prefix ../../sdk/ts ci --ignore-scripts --registry=https://registry.npmjs.org
npm ci --ignore-scripts --registry=https://registry.npmjs.org
npm run typecheck
npm test
npm run pack:source
```

Packaging produces `build/v2ex-source.tar.gz`. `build/source.json` records exact
bundle/archive hashes and byte counts. The import-free bundle exports the default
Program; its staged manifest describes Deno source rather than a Go executable.
Only the packaged manifest contains the final bundle hash, avoiding a self-hash
cycle. The official SDK authoring checker runs offline during packaging; it is
not an installed-hub or source-confinement test.

Rebuild the contained archive from `authoring/extensions/v2ex/`, not its reference
source files at the archive root. The `file:../../sdk/ts` dependency resolves inside
that subtree to the unmodified official SDK module, including its checker workers.
Public-registry lockfiles are checked independently. No package default, released
Go file, manifest, version, catalog, signature or publication is changed.

### Authoring boundaries

HTTP responses and raw page state retain signed-int64 values, numeric spelling,
ordered duplicate errors and RawMessage deletion flags after syntax validation.
An iterative lossless parser accepts ignored deep fields without overflowing the
JavaScript call stack. Keyword lowercase is context-free like Go; field matching
uses simple folding, and configured node names use Go's Unicode White_Space.
Extended date and folding fixtures retain their existing Go comparison evidence;
this source delivery does not regenerate the original-Go oracle.

The connector uses the official, lazily bound exact-params handler. It preserves
full signed-int64 lexical/range rules and scalar versus pointer null behavior,
then clamps counts before converting them to JavaScript numbers. It does not use
a safe-integer subset or reconstruct original tokens from rounded numbers.
Finite page loading opts into `useLoadEffect`, so snapshot host calls await the
bounded read before suspension. The original four premature direct Renderer
suspensions remain an explicit negative control: every read is cancelled and no
topics or cache are fabricated. An uninterrupted resume must then settle, advance
the callback revision and open the topic; a separate snapshot-transport test must
return topics before suspension and preserve cached resume/replay receipts. This
qualifies the old host-bypassing counterexample, not a renderer or deadline waiver.

Initialization now opts into the official `withExactInit` API delivered by the
merged official SDK synchronization. Its actual RPC config slice passes through
the same ordered parser before validation. The original
`{"nodes":1,"nodes":["go"]}` rejection assertion is unchanged and no longer a TODO.
Folded duplicates, nulls, failed-init retry, ignored deep fields and the enclosing
config RawMessage's last-occurrence rule are covered. Direct authoring probes have
no raw frame and use their supplied config object, without fabricating tokens.

The source uses the unchanged official SDK from merged main, not a provisional or
handpatched copy. Current-head hosted required CI/standalone source and archive
rebuilds, plus isolated installed-hub content, callback readings and inspected
screenshots, remain merge gates. The last actual upstream browser-check response
contained zero topic rows and is still **BLOCKED**, not content acceptance.
Synthetic fixtures and offline checks do not erase that result. No production
activation, source default/catalog/key change or release is part of this delivery.
