# Releases and market signing

[Documentation](README.md) · [Installation](installation.md)

This guide describes maintainer operations, not steps required to use an extension. Publishing a release, changing a GitHub secret, pushing an index or rotating a trust key are outward-facing actions; run them only with appropriate authorization. Local documentation checks do not require any private key or GitHub token.

## Market and indexes

The repository itself is a market. Released packages live in GitHub Releases named `<id>-v<version>`; the index pins each asset by SHA-256.

| File | Contents |
| --- | --- |
| `persona-extensions.json` | Current entries under `extensions`. Each entry's `manifest` is the packaged manifest (without `tags`); its tags are in the entry's `tags`. |
| `persona-plugins.json` | The same entries under `plugins`, for hubs from before the rename. |
| `market.pub` | One line: `ed25519:<base64 of the 32-byte public key>`. |
| Index-level `signing_key` | The same public-key line; older hubs ignore unknown fields. |
| `persona-extensions.json.sig`, `persona-plugins.json.sig` | `{"alg": "ed25519", "key_id": "<first 16 hex characters of SHA-256 of the 32-byte public key>", "sig": "<base64 Ed25519 signature of the index's raw bytes>"}`. |

Package locations use `release:<tag>/<file>` in the index. [scripts/index.py](../scripts/index.py) replaces the entry for the released extension, preserves other entries and keeps both formats consistent.

## Automated releases

[CI](../.github/workflows/ci.yml) runs [scripts/ci.sh](../scripts/ci.sh) for pull requests. On a push to `main`, it releases only extensions whose manifest version changed in that push, including new extensions.

A version is released once: CI skips an existing release; `scripts/release.sh` refuses to overwrite it. Do not rewrite published tags or historical commits to change documentation language.

After packaging and creating releases, CI updates both indexes, signs them and commits the indexes and signatures. Its index commit message is already English. A manual workflow run takes extension ids and can release a version merged earlier but deliberately held back. A push without a usable previous ref runs checks only.

If the CI environment is unavailable, maintainers can run the checks locally and describe that limitation in the PR. Do not report checks that did not run as passing.

## Authorized local release

From the repository root:

```sh
scripts/release.sh <id>
```

This packages the extension, creates the GitHub Release, uploads the archive and updates both indexes. It signs them if the market key is available. Commit and push the updated indexes and signatures afterwards through the repository's authorized publication workflow; the script does not commit or push them for you.

To inspect/check versions without publishing:

```sh
scripts/manifest.py ids
scripts/manifest.py version linuxdo
scripts/manifest.py changed <git-ref>
scripts/index.py check
```

## Market signing

Both indexes are signed with the market's Ed25519 key. When a hub registers the market it records the public key. Later it refuses installation/upgrades if verification fails, a required signature disappears or the key changes.

The signature covers **raw file bytes**, not canonicalized JSON. Do not change even whitespace in an index after signing it.

In CI, the private key is available only to the signing step, as the Actions secret `EXTENSION_MARKET_SIGNING_KEY`; it is not passed to steps that build or test extension code. For an authorized local release, the same environment variable or a protected key file can be used:

```sh
scripts/sign-index.py --key-file /path/to/market.pem
scripts/sign-index.py verify
```

Without a key, the signing command prints `unsigned` and exits successfully, warning if existing signatures are stale. This does **not** make a changed index safe to publish: `scripts/ci.sh` fails if its existing signatures no longer match. Sign after updating indexes.

The current verifier checks signatures that exist; it skips an absent `.sig` file. Therefore, success alone is not proof that both signatures are present. Confirm both files are included before publishing.

## Verification without a private key

Prefer `scripts/sign-index.py verify`, which checks existing signatures against the committed public key. It needs OpenSSL, not a private key.

For an independent OpenSSL check of the current index, run this from the repository root. It uses a fresh temporary directory and only public verification material:

```sh
verify_dir=$(mktemp -d)
python3 -I - "$verify_dir" <<'PY'
import base64
import json
import pathlib
import sys

out = pathlib.Path(sys.argv[1])
raw = base64.b64decode(pathlib.Path("market.pub").read_text().strip().removeprefix("ed25519:"))
der = bytes.fromhex("302a300506032b6570032100") + raw
(out / "market.pem").write_text("-----BEGIN PUBLIC KEY-----\n" + base64.b64encode(der).decode() + "\n-----END PUBLIC KEY-----\n")
(out / "index.sig").write_bytes(base64.b64decode(json.loads(pathlib.Path("persona-extensions.json.sig").read_text())["sig"]))
PY
openssl pkeyutl -verify -pubin -inkey "$verify_dir/market.pem" -rawin \
  -in persona-extensions.json -sigfile "$verify_dir/index.sig"
rm -r "$verify_dir"
```

Repeat with the legacy index and its signature to verify that file independently. No signing key is needed for verification.

## Private-key management

For a new market, generate an Ed25519 key into a protected, non-repository directory:

```sh
key_dir=$(mktemp -d)
(umask 077; openssl genpkey -algorithm ed25519 -out "$key_dir/market.pem")
```

The public-key line is `ed25519:` followed by Base64 of the last 32 bytes of the public DER encoding, obtainable with `openssl pkey -in "$key_dir/market.pem" -pubout -outform DER | tail -c 32 | base64`. Put only that **public** line in `market.pub`.

Store the private key in the repository's Actions secret, for example `gh secret set EXTENSION_MARKET_SIGNING_KEY < "$key_dir/market.pem"`, and keep an offline backup in the maintainer's password manager. Never commit the private key or print it in logs. Remove the temporary copy after securely recording the intended backup; do not delete the only copy.

If the key is lost or compromised, generate a new one, replace the secret and `market.pub`, re-sign both indexes and publish the change. Existing hubs refuse the changed key until their owner explicitly confirms the new trust. Do not silently replace a trusted market key to work around verification errors.
