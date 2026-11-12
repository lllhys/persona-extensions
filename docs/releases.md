# Releases and market signing

[Documentation](README.md) · [Installation](installation.md)

Publishing packages, changing package visibility and dispatching publication workflows require authorization. Code and tests can merge after affected local verification passes **before** cutover, but publication cannot. Hosted CI is explicit-dispatch only, not a PR merge gate. The cutover owner handles repository renaming and visibility; this workflow does neither. Local CI and synthetic fixtures are not evidence of a published package or a working hub installation.

## Market and indexes

The repository has one catalog, `persona-extensions.json`, with embedded signatures:

```json
{
  "payload": "<the original UTF-8 catalog JSON string, including its whitespace>",
  "signatures": [
    {"alg": "ed25519", "key_id": "3cce0f471911a1cf", "sig": "<base64 signature>"}
  ]
}
```

The payload has `market_version: 1`, market metadata, `signing_key` and `extensions`. Newly generated payloads also have signed `generation` provenance (see below). Each entry contains its packaged `manifest` (without source-only `tags`, `replaces` and `icon`), entry `tags`, optional `replaces` and `packages`. There is no legacy catalog, detached signature or standalone public-key file. [scripts/catalog.py](../scripts/catalog.py) retains the approved **public** key as a code constant; no private key belongs in the repository.

Signatures cover `payload.encode("utf-8")`, **not** the outer JSON, parsed objects or canonicalized JSON. Outer formatting can change; the signed string cannot. Duplicate keys, absent/empty signatures, the wrong key and malformed signatures fail closed.

The initial format conversion deliberately retains the frozen signed payload's `release:<tag>/<filename>` references. It is not an OCI publication. Do not replace these with guessed digests. Only successful package publication and the anonymous gate below generate OCI references.

### Icons and download ranking

Every official source manifest declares `icon`, a square PNG (at least 256 × 256) or SVG under its own `assets/` directory. The package includes the image and `assets/LICENSE`; [icon-sources.json](../icon-sources.json) records its exact source URL, SHA256, copyright/license notice and trademark owner. This is brand identification, not a claim that a trademark is licensed under the repository's code license. Never substitute an invented service logo. Source validation checks the file, dimensions, safety and attribution inventory.

`icon` is source-only metadata, like `tags` and `replaces`: older hubs reject unknown runtime manifest fields. The signed catalog entry retains its source `icon` path for generation provenance and adds `icon_url`, an absolute HTTPS raw URL containing the full trusted source commit SHA. It never uses `main` or a mutable release tag. Anonymous archive verification checks that the packaged icon is byte-identical to that source image. Clients display `icon_url`, not `manifest.icon`; old clients can ignore the new entry fields.

On every changed publication, [scripts/downloads.py](../scripts/downloads.py) reads the **exact cumulative Total downloads** counter on each official package's public GitHub detail page. GitHub's documented Packages REST API does not expose this statistic; release asset downloads and rounded organization-list numbers are not substitutes. Requests are anonymous, bounded, retried for transient transport/rate-limit errors, and limited to four concurrent requests. Missing pages/counters, rounded or ambiguous counters and incomplete snapshots fail publication rather than recording zero. No third-party analytics service or secret is required.

The signed payload adds `downloads_updated_at` (RFC 3339 UTC), every entry adds integer `downloads`, and entries sort by downloads descending with ID ascending as the deterministic tie-breaker. Ranking does not alter source inventory order: continuity validation compares the complete source inventory in ID order. All these metadata fields are inside the signed payload. Real zero counts are allowed; fetch failures are not zero counts. These are registry download counters, including publication/verification traffic, not unique users or installations.

For a metadata-only refresh, run a versions `phase=packages` dispatch to obtain the current same-SHA plan, then a `phase=catalog` dispatch with that `package_run_id` and `refresh_metadata=true`. This reuses immutable packages, anonymously verifies them, fetches a fresh complete snapshot and produces the normal signed artifact for its own reviewed catalog PR. Package versions do not change for statistics alone. There is deliberately no daily workflow that silently pushes signed catalogs past the existing dedicated-issue, local-verification and reviewed-PR gates.

### Signed continuation and PR validation

Every new payload includes this object **inside the signed UTF-8 payload**, not in an unsigned envelope field or artifact sidecar:

```json
{
  "generation": {
    "repository": "nwverse/persona-extensions",
    "repository_id": 1449704212,
    "base_catalog_hash": "sha256:<predecessor raw payload hash>",
    "source_commit": "<full trusted source commit SHA>",
    "source_inventory_hash": "sha256:<complete source manifest and tags inventory hash>",
    "mode": "versions"
  }
}
```

`base_catalog_hash` hashes the predecessor's decoded **raw payload bytes**, not its outer envelope formatting. The inventory hash covers the full sorted inventory, including IDs, packaged manifests, tags and each non-empty `replaces` declaration; sources without that field retain their original encoding (no empty default field is added). It uses the canonical encoding in `catalog.inventory_hash`, independently of signature encoding. Mode is `bootstrap` or `versions`.

Continuity validation needs the actual fetched PR base, not an artifact's claimed predecessor. Local catalog PR checks pass that base with `scripts/index.py check --base-ref <actual-base-ref-or-SHA>`; manual CI retains GitHub repository/run identity checks and full checkout history but is not an automatic PR-context receipt. The base catalog is read directly from the chosen actual commit with `git show`. The signed predecessor hash must equal that base's verified payload hash. The signed source commit must be its ancestor, have the same source inventory as that base, and contain the predecessor catalog. Versions-mode output must match the complete inventory. Thus docs-only base advances remain valid, but a newer catalog merge or source deletion makes an older signed artifact fail. Regenerate from the new base rather than editing provenance.

The only exception is an unchanged raw payload **and the original signature reused exactly**: this allows the initial detached-to-envelope conversion without a private key, and unchanged catalogs in code-only PRs. A different old signed payload without signed provenance is never accepted as an update.

Trust boundary: GitHub supplies repository/run identity and the actual comparison base; the local reviewer must use that fetched base, not choose one from an artifact. The signer supplies signed provenance, and reviewed workflow/checker code enforces it. Artifact metadata and PR-supplied labels/branch names cannot replace those values. Workflow/checker changes need normal code review and affected local verification; this is not protection against a maintainer deliberately replacing the validation code. An offline signature-only check is not a PR continuity verdict.

## OCI contract

| Part | Contract |
| --- | --- |
| Package path | `ghcr.io/nwverse/persona-extensions/<id>:<version>` (no `latest`) |
| Catalog URL | `oci:ghcr.io/nwverse/persona-extensions/<id>@sha256:<manifest digest>` |
| Manifest | OCI image manifest, `schemaVersion: 2`, media type `application/vnd.oci.image.manifest.v1+json` |
| Artifact type | `application/vnd.persona.extension.v1` |
| Config | Empty `{}` blob, `application/vnd.oci.empty.v1+json` |
| Layer | Exactly one archive; `application/vnd.persona.extension.package.v1+gzip` |
| Title | `org.opencontainers.image.title`, safe `<id>-<version>.tar.gz` filename |
| Source | Always `https://github.com/nwverse/persona-extensions`, including before the source repository is renamed |
| Catalog `sha256` / `size` | Archive **blob** digest / bytes, not manifest digest / bytes |

Current packages are all `any`: each program archive includes its manifest's platform-to-binary map. Declarative packages need no binary. `any` keeps the historical `<version>` tag and `<id>-<version>.tar.gz` title unchanged. Explicit platform packages list exactly one value in `platforms`, the field the hub reads (a singular `platform` is refused because the hub would ignore it), use `p_<platform>_<version>` tags and `<id>-<version>__<platform>.tar.gz` titles. Underscores are outside the accepted version domain, so `1.2.3`/`linux-amd64` cannot collide with `1.2.3-linux-amd64`/`any`. The version domain is SemVer `MAJOR.MINOR.PATCH` with optional dot-separated prerelease identifiers, no leading zeros in numeric identifiers, **no build metadata**, at most **96 ASCII characters**; explicit-platform tags remain below OCI's 128-character limit. Manifests are not Docker multi-platform indexes.

[scripts/oci.py](../scripts/oci.py) implements the exact Distribution protocol with Python's standard library. It checks manifest/config/archive digests and metadata. Existing tags are verified and reused, never overwritten; an archive, metadata or source-label conflict requires a new version. Only a registry HTTP 404 means absent. Auth, rate limits, transport and server errors stop publication. Workflow writers are serialized globally, across trusted branches. Never run an independent tag writer concurrently.

## Automated releases

[CI](../.github/workflows/ci.yml) runs [scripts/ci.sh](../scripts/ci.sh) on every push to `main` and on explicit dispatch. A trusted-main push runs the normal checks, then `phase=versions` publishes or verifies existing OCI packages, runs anonymous verification and signs the resulting catalog. A manual `phase=check` dispatch is still available for validation without publication credentials, package writes or signing. `CI required` aggregates that manual checks-only run; `Publication required` fails a main push or manual catalog run if packages (on push), anonymous verification or signing did not succeed. A green publication run therefore cannot silently omit the signed catalog artifact. PRs merge after affected local verification, not after a hosted result. `scripts/ci.sh` never publishes and rejects the former `release` arguments.

```sh
gh workflow run ci.yml --repo nwverse/persona-extensions --ref <issue-branch> -f phase=check
```

Publication requires both the final name `nwverse/persona-extensions` and cleaned repository ID `1449704212`. The former repository cannot pass this gate. Only trusted `main` can publish; issue branches remain checks-only. Before cutover, a checks-only dispatch is permitted; dispatch publication refuses even when a caller checks the confirmation input. The script rechecks the actual GitHub repository identity, checkout SHA and full source inventory before reading package credentials or doing any upload.

After cutover, a manual publication dispatch still requires `phase=packages` or `phase=catalog` with `confirm_cutover=true` and the same live identity checks. A manual catalog dispatch also requires `package_run_id` for a successful package run at the same checkout SHA. The catalog phase deliberately skips its `packages` job and does not run the package writer again; the anonymous gate instead requires that job to be skipped, validates the successful prior package run and downloads its plan. On main pushes, the gate requires the current run's `packages` job to succeed, uses the actual current `github.run_id` and continues to signing. Existing tags are reused. No new repository/org variable or publication credential is introduced by this trigger policy. The package visibility step below remains manual and is not bypassed by automatic publication.

### Phase 1: packages

The initial bootstrap downloads **all 14 current archives from the cleaned repository's Releases**, compares asset metadata and downloaded bytes to the frozen signed catalog, and uploads those exact bytes. It never recompiles/replaces a current version or uses the former repository's assets. Cleaned historical archives can differ from the catalog in scrubbed descriptive fields; the audit lists those differences while enforcing identical id/version/runtime mappings and exact frozen checksums. Do not rewrite them to make their descriptions match.

Authorized versions-phase manual runs compare the **full current source inventory** to the signed catalog, including work from a replaced waiting run. A same-version packaged-manifest change fails; tags-only or source-only `replaces` changes retain the original package URL/SHA256/size and need neither a version bump nor a new upload. Removing `extensions/<id>` removes that ID from the next generated catalog without deleting any historic Release, tag or GHCR version. Selection is not just a version-bump diff. An existing registry version is verified and reused **before any build**, so interrupted runs do not rebuild or overwrite published bytes. New versions are locally packaged once if absent. There are no Release/tag creation/deletion calls. The current catalog must first complete bootstrap before new-version catalogs can be generated.

The successful job produces artifact **`oci-package-plan`**:

- `plan.json`: source SHA, frozen payload digest, complete updated entries, `removed_ids`, `catalog_update_required` (including deletion-only updates), OCI manifest references, archive descriptors and cleaned Release receipts for bootstrap. Validation recomputes changes from the complete trusted inventory; these unsigned plan signals are not signing authority.
- `package-settings.json`: all package names and GitHub package settings URLs for that run.

A failed run may retain a partial plan for diagnosis. It cannot pass the successful-run provenance check or completeness validation. Rerun packages to resume from verified registry versions. All three fixed-name artifacts use overwrite on reruns (which share a run ID), and consumers keep those exact names. Each artifact download starts in a new empty directory; Python runs with `-I` from the reviewed checkout, never inside downloaded data. Do not sign a partial plan.

### Renamed identities

An ID change is a new immutable OCI package namespace, not a moved registry tag. First merge the hub/node compatibility support. Then review the moved source, new ID/version/binary names and single `replaces` declaration through a separate extension PR. Once that version is on trusted `main` after cutover, automatic publication verifies or uploads its immutable packages and prepares the signed catalog artifact. An explicit authorized `phase=packages` dispatch can perform the same package step before a later main push; a checks-only dispatch cannot grant publication authorization.

The complete source inventory projects `replaces` into the entry and signed generation provenance, while both packaged and embedded runtime manifests omit it. Duplicate/self predecessors, cycles and predecessors still present as current IDs are refused. The generated catalog removes the old current entry and includes the successor declaration; old packages, GHCR versions and historical Releases/tags are never deleted. Even a valid new signature cannot substitute a different declaration for the reviewed source inventory.

Make the new namespace public through the authorized visibility step, then use the same-SHA anonymous gate and isolated signing job to create the dedicated catalog PR. Require its affected local verification and actual-base continuity checks. Existing users confirm the same-market identity upgrade; do not instruct them to uninstall/reinstall, move secret rows or rewrite data. See [identity changes](reference.md#identity-changes).

### Human visibility step

New GHCR packages are private by default, even when the source repository is public. An authorized, logged-in organization owner opens every URL in `package-settings.json` and changes **Package settings → Danger Zone → Change visibility → Public**. There is no documented REST endpoint to change GHCR visibility; this workflow does not pretend to have one, create credentials or log in for the owner.

Do this for the complete bootstrap batch, and for every newly introduced package later. Existing public packages' new versions normally need no visibility change.

### Phase 2: catalog

Dispatch `phase=catalog` using the successful package run id and the **same checkout SHA**. The workflow checks repository/run identity, trusted branch, workflow path and matching SHA, then independently requires a completed successful `packages` job before downloading the plan. On a main push, that job belongs to the current still-running publication: its overall conclusion is not yet available, so the completed package job is the success evidence. A prior manual package run can likewise supply a plan after a later visibility gate failed, provided its own package job succeeded. If main moved, rerun packages at the new SHA; existing tags are reused.

A separate job creates fresh **anonymous** registry clients, ignoring all GitHub/registry credential environment variables. It checks every retained and updated OCI package by digest, including config, archive, id/version and platform binaries. Private packages, wrong digests, a changed source catalog or incomplete/tampered plan block payload output. The tar scanner is streaming: compressed and unpacked regular-file bytes are capped at **200 MiB**, regular files at **20,000**, manifests at **256 KiB**, matching the hub's package limits. A separate decompressed scan budget (including headers, directories and padding) and bounded PAX/GNU metadata prevent small compressed bombs/header floods. No extraction or execution occurs. Paths must stay inside the extension root; duplicates, links, sparse files and special files are rejected. Required instructions, skills' `SKILL.md`, gates and gate script references must be safe regular files. Runtime commands must have executable bits: `any` checks all declared platforms, while an explicit platform package checks its own runtime binary; node commands still cover all declared computers. Registry token requests use the fixed GHCR HTTPS endpoint and exact pull scope. Credentials never follow blob redirects or a server-supplied realm.

Only after this gate does artifact **`verified-unsigned-payload`** contain `payload.json` for a changed catalog. The same job then signs it, without running any extension build/test code in this job. Only its signing step receives `EXTENSION_MARKET_SIGNING_KEY`. Missing secret, a secret exporting a different public key or failed verification is fatal. The fixed key remains `3cce0f471911a1cf`; embedding signatures is **not** another rotation.

When versions have not changed, the anonymous job validates the complete plan and every current OCI archive, verifies the existing approved signature and compares the exact signed envelope with the checkout. That is a successful no-op: the job skips the secret and signed-artifact steps, no empty catalog PR is needed and `Publication required` accepts only the explicit `anonymous=success`, `unchanged=true`, `sign=skipped` combination. A changed catalog must produce a nonempty `payload` output and the `verified-unsigned-payload` artifact, so the same job cannot silently treat it as unchanged. Missing anonymous verification, an inconsistent output or a skipped sign for a changed catalog still fails.

Artifact **`signed-oci-catalog`** contains `persona-extensions.json` and `PR-body.txt` with issue-creation instructions, not an automatic PR. Before each catalog-update PR, an authorized maintainer creates or reuses a **dedicated issue for that catalog update**, claims it, and references it in that PR. #146 tracks the code change only; do not reuse it to close multiple publication PRs. The initial code PR can use `Refs #146`; completed OCI publication is separate cutover-dependent work. Require each catalog PR's affected local verification and signed base continuity before merging. An unchanged catalog-only main merge writes no packages; publication is a separate explicit phase and there is no catalog-PR publication loop or bypass deploy key.

## Authorized cutover bootstrap

These are instructions for **after** the cutover owner's confirmation, not commands to run while the repository is still next. Do not change repository names or visibility here.

```sh
# At an approved trusted ref/SHA, first push exact cleaned current archives.
gh workflow run ci.yml --repo nwverse/persona-extensions --ref main \
  -f phase=packages -f mode=bootstrap -F confirm_cutover=true

# After that run succeeds and a logged-in owner makes every listed package public:
# use the same ref/SHA as the package run (or rerun packages if main moved).
gh workflow run ci.yml --repo nwverse/persona-extensions --ref main \
  -f phase=catalog -f package_run_id=<successful-package-run-id> -F confirm_cutover=true

# Inspect the signed output in a new empty directory; do not execute downloaded code.
gh run download <catalog-run-id> --repo nwverse/persona-extensions \
  -n signed-oci-catalog -D /path/to/new-empty-catalog-directory
python3 -I scripts/sign-index.py verify \
  --catalog /path/to/new-empty-catalog-directory/persona-extensions.json
```

Apply that one catalog file to a branch/PR linked to its own dedicated catalog-update issue, not a second PR closing #146. Do not copy a workflow, script, old public key or historical package from the artifact. Release/tag identities and all archive digests remain unchanged; compare the audit history before/after, not only counts.

For an independently authorized local publisher, [scripts/release.sh](../scripts/release.sh) delegates to the same identity-gated `push` command. Provide `--repository`, `--repository-id`, `--ref`, full `--head-sha`, `--confirm-cutover`, `--mode` and a new `--output` directory. GHCR credentials belong in `GHCR_TOKEN`/`GITHUB_ACTOR`, never argv or repository files. Signing still belongs in the independent Actions secret step, not local bootstrap.

## Market signing

The approved cleaned repository's current key id is `3cce0f471911a1cf`. The Persona hub's official pin and sandbox allowlist must use the corresponding public key too; hub pin migration is tracked separately in nwverse/persona#1157. A synthetic test is not proof that a deployed hub has the new pin. Preserve deliberate/unknown user pins, do not recreate deleted markets, and quarantine cached catalogs until real re-verification succeeds. Do not instruct users to bypass verification with the removed non-official TOFU/trust flow.

The private key already lives in the repository Actions secret `EXTENSION_MARKET_SIGNING_KEY`. Keep it there and do not rotate it for this format change. Its exported public key must match [scripts/catalog.py](../scripts/catalog.py). A lost/compromised key requires a separately authorized recovery decision, not an automatic key generator or silent fallback.

## Verification without a private key

```sh
python3 -I scripts/index.py check
python3 -I scripts/sign-index.py verify
```

Both require a present valid embedded signature. The initial conversion used the following tool while the original next files were present:

```sh
python3 -I scripts/sign-index.py convert-legacy
```

`convert-legacy` verifies the original raw file with its original detached signature and approved public key **before** wrapping the original UTF-8 string and reusing that same signature. It does not read a private key or re-sign. Custom `--catalog`, `--signature`, `--public-key` and `--output` paths allow independent conversion checks of a frozen original snapshot. Never recreate removed detached files on main.

## Read-only cleaned Release audit

This can run before cutover; it does not upload, sign, dispatch or change visibility:

```sh
python3 -I scripts/publish-oci.py audit \
  --repository nwverse/persona-extensions-next \
  --output /path/to/new-empty-audit-directory
```

After renaming, pass the final repository name instead; the immutable cleaned repository id is checked. Each downloaded archive gets its own fresh directory and is only read, never extracted/executed. `audit.json` lists the current exact-byte checks, platform mappings, descriptive-field differences and historical Release/asset identities. Do not include credentials in audit logs or commit downloaded archives. Keep audit evidence separate from code and do not mistake it for an anonymous OCI or real hub installation test.
