#!/bin/bash
# What CI runs, runnable on any machine with Go, Python 3 and (for release) gh:
#   scripts/ci.sh            the checks a pull request needs: documentation links, manifests, gofmt, vet, tests,
#                            every platform builds, indexes,
#                            and the indexes' signatures against market.pub (nwverse/persona#1848)
#   scripts/ci.sh release <base>    then release the extensions whose version changed since the git ref <base>
#                                   (a push to main: its "before"); a version merged but meant to wait is not released
#   scripts/ci.sh release <id>…     then release these (CI's manual run)
# A version is released once: one that has its release already is skipped.
set -euo pipefail
cd "$(dirname "$0")/.."
step() { printf '\n== %s\n' "$*"; }

step docs
python3 -I scripts/check-docs.py
python3 -I scripts/test-docs.py

step manifests
scripts/manifest.py check

step gofmt
bad=$(gofmt -l . | grep -v '^dist/' || true)
[ -z "$bad" ] || { echo "not gofmt'ed:"; echo "$bad"; exit 1; }

step sdk
go -C sdk/go vet ./...
go -C sdk/go test -count=1 -timeout=90s ./...

step vet
go vet ./...

step test
go test ./...

step companion
python3 -I -B extensions/larkagentx/bridge_test.py

step build
for id in $(scripts/manifest.py ids); do
  scripts/package.sh "$id" >/dev/null
  echo "$id $(scripts/manifest.py version "$id"): ok"
done

step index
scripts/index.py check

step signature
scripts/sign-index.py verify

if [ "${1:-}" = release ]; then
  shift
  step release
  ids=()
  for a in "$@"; do
    if [ -d "extensions/$a" ]; then
      ids+=("$a")
    else
      while read -r id; do [ -z "$id" ] || ids+=("$id"); done < <(scripts/manifest.py changed "$a")
    fi
  done
  [ "${#ids[@]}" -gt 0 ] || echo "nothing to release"
  for id in "${ids[@]}"; do
    tag="$id-v$(scripts/manifest.py version "$id")"
    if gh release view "$tag" >/dev/null 2>&1; then
      echo "$tag: released already"
    else
      scripts/release.sh "$id"
    fi
  done
  scripts/index.py check
fi
echo
echo "ci: ok"
