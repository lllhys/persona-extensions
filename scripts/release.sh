#!/bin/bash
# Release an extension into this repository's market: pack it, upload the package to the GitHub release
# <id>-v<version>, and put it into both indexes (scripts/index.py). Commit and push the indexes afterwards
# (CI does all of this on main: scripts/ci.sh release).
#   ./scripts/release.sh <id>
# A hub reads the market as github:<owner>/<repo> (with a read-only token for a private repository).
set -euo pipefail
cd "$(dirname "$0")/.."
id="${1:?usage: scripts/release.sh <extension id>}"
id="${id%/}"; id="${id#extensions/}"
version=$(scripts/manifest.py version "$id")
tag="$id-v$version"
if gh release view "$tag" >/dev/null 2>&1; then
  echo "release $tag exists: a version is released once; bump the version" >&2
  exit 1
fi
./scripts/package.sh "$id"
gh release create "$tag" "dist/$id-$version.tar.gz" --title "$id $version" --notes "$id $version"
scripts/index.py add "$id" "$tag"
# Signs both indexes when $EXTENSION_MARKET_SIGNING_KEY has the market's key; prints "unsigned" otherwise.
scripts/sign-index.py
