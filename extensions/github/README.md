# GitHub

GitHub in Persona: the inbox, the knowledge base's archive of your PRs and issues, the assistant's GitHub tools, and a page of what needs you on GitHub.

- [Extension guide and configuration](../../docs/extensions/github.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)

## Local source profile (unreleased)

The published manifest above still launches the original Go program. `npm run build:source` produces a separate local draft under `.source-dist/`: its outer manifest uses `runtime.mode: deno`, with the contained TypeScript entry and the SHA-256 of the actual import-free `bundle/main.mjs`. That bundle default-exports a `Program`; it does not run Go or start its own transport. The original manifest under `source/extensions/github` is an authoring input, not the installed runtime manifest. No digest is embedded into its own bundle.

The archive contains the complete authoring inputs and the unchanged relative SDK at `source/sdk/ts`. To rebuild, install dependencies from the public npm registry in `source/extensions/github`, then run `npm test` or `npm run build:source` there. Install the SDK's own locked development dependencies in `source/sdk/ts` before using its official authoring checker; the checker worker imports TypeScript from that module, not from the extension's sibling dependency directory. `verify:authoring` performs both clean installs and the official Program probe without changing HOME or npm configuration. Running the already-contained bundle without imports and rebuilding the extracted authoring tree are separate checks. Packaging uses Python's standard-library tar/gzip writer rather than GNU-only tar flags; it has been exercised on Linux, not on macOS.

Source-profile behavior and safety boundaries:

- Authentication switches revoke account-local inbox, seen, conditional polling, rate, avatar and API caches. In-flight collection cannot refill the next account. Old write receipts stay reserved but cannot be reused under a new account. Host-persisted historical inbox rows are not deleted.
- Device secret mutations are ordered. Logout clears in-memory authentication immediately and returns successfully only after outstanding older saves and its durable clear complete. A stale login continuation cannot restore authentication.
- Write argument types are validated before preview or HTTP dispatch. Nullable string fields retain Go's empty-string fallback; objects, arrays, booleans and string-valued numeric fields are not coerced into public writes.
- Inbox classification reads the last conversation-comment page by the issue's comment count. Multiple failed workflows share one branch item but all tracked failures must later succeed before it settles. Missing workflow evidence does not mean success.
- Timeline actors and issue/comment/review authors may be null; tools, inbox, archive and pages retain those entries with empty IDs and the existing deleted-user fallback. Optional failed-job logs retain check information with a localized diagnostic instead of failing the whole tool or conversation.
- Line and draft edits carry the head of the displayed diff through editing, preview revisions and sending. Loading checks the head around the file read, and preview rejects a changed or missing displayed head instead of silently approving newer coordinates. REST line writes submit the pinned commit ID. Draft GraphQL writes check the head before sending; that API does not provide an atomic expected-head condition.
- Snapshot redraw does not renew an old account's preview or approval. An old-account or unbound preview is rejected, and sending requires an explicit new preview under the current account.
- Discussion and commit notifications retain `subject:<repo>@<id>` identities, never legacy CI identities. Branch patterns use case-sensitive Go `path.Match` syntax, including character classes and escapes; repository filters fold case explicitly. Tab changes clear tab-local search and filter state.
- Archive cursors validate every discovery, thread and position timestamp as RFC3339, including calendar days and offset bounds, before any request or cursor advancement. `Date.parse` alone is insufficient because it normalizes impossible dates.
- Repository refresh is separate from authentication invalidation. Unrelated reads remain valid, while responses that predate a refresh cannot refill the refreshed cache. JSON syntax is validated before admission to the ETag cache; empty HTTP 200 bodies and malformed bodies are rejected, never repaired into valid JSON. HTTP 204 intentionally has no representation and is not cached.
- Go's inherited 401 retry could use a different refreshed account for an approved write. The draft's adjacent guard verifies `/user` with the exact refreshed credential and only retries a confirmed 401 for the same known principal. Different or unverifiable principals are rejected; transport errors with unknown write effects are not retried. This is a narrow retry guard, not a claim of complete account isolation across external credential changes.

- The lazy connector namespace is branded with the official `withExactParams` before initialization. Every read-tool struct field is decoded, including unused fields. Ordered duplicate and folded keys, scalar nulls, nullable integer pointers, string maps and string arrays follow Go's typed decoding. Fractional/exponent spellings, overflow and wrong types are rejected before HTTP dispatch. Legal signed-int64 numeric IDs stay as `bigint` when needed; notification and GraphQL string IDs remain strings. HTTP paths, state JSON, reply IDs, archive cursors and receipts never reconstruct large IDs from a rounded `Number`.
- The only runtime UI contribution remains `github`. It uses keyed native components and saved callbacks for snapshot and streaming transport, never client extension JavaScript or HTML. `useLoadEffect` owns finite initial/navigation loads; authentication subscriptions remain visibility-managed `useEffect` work. Cold fixtures explicitly negotiate top-level `transport: "snapshot"` and inspect the first response, not a guessed UI version or an alternate page.

### Incomplete acceptance gates

This is not a complete parity or release claim. This branch is based on provisional SDK PR #165 (`fix/158-sdk-source-authoring-delivery`); the SDK is copied byte-for-byte and not patched here. The complete SDK TypeScript module closure, including its official checker, fixtures and scripts, is contained in the source archive, preserving `file:../../sdk/ts`. SDK publication, official resynchronization and the layout fix for its Go oracle (#164) remain shared prerequisites, not source-worker fixes. The local exact-ID and cold-native-snapshot fixtures are separate from installed-hub HTTP acceptance. Unperformed integration gates are reported by `verify:acceptance`, not disguised as skipped green tests. Migration-wide HN and SIGTERM gates cannot be settled by GitHub fixtures.

The discovery cursor's equal-timestamp full-page defect is inherited from Go and intentionally unchanged in this scoped repair: with 51 issues at the same `updated_at` and a limit of 50, the first page can repeat indefinitely. A `KNOWN` fixture preserves this failure; a passing characterization test is not a successful completeness gate. Comment cursor pagination is a different path and retains its since/position repair.

Explicit UI/integration gaps still blocking full acceptance:

- Commit actions open the public commit link rather than reproducing Go's native commit-detail page.
- Checks display bounded logs but do not reproduce the original log-location selection UI. Real upstream multi-range logs require Range support and stable validators; synthetic Range/ETag fixtures are not upstream evidence.
- Source files retain the bounded preview but not Go's large-source truncation/load-more navigation. The source runtime must not expand its body or callback limits to hide this gap.
- The existing optional proxy setting is preserved but fails closed until a controlled broker proxy interface is available; no direct-network fallback is added.
- An installed isolated hub, native dark/light screenshots, restart/queue persistence, production behavior and hosted CI have not been accepted by this source writer. The coordinator must install and accept one extension instance at a time before merge.

Local checks, from this extension directory:

```sh
npm test
npm run test:bun
python3 -I scripts/verify-source.py .
npm run verify:authoring
go test -race ./...
npm run verify:acceptance # Intentionally exits 1 with the outstanding FAIL/BLOCKED gates.
```
