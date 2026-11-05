#!/bin/bash
# What CI runs, runnable on any machine with Go, Python 3 and (for release) gh:
#   scripts/ci.sh            the checks a pull request needs: documentation links, manifests, gofmt,
#                            the copied SDK's vet and tests, vet, tests, every platform builds, indexes,
#                            and the indexes' signatures against market.pub (nwverse/persona#1848)
#   scripts/ci.sh release <base>    then release the extensions whose version changed since the git ref <base>
#                                   (a push to main: its "before"); a version merged but meant to wait is not released
#   scripts/ci.sh release <id>…     then release these (CI's manual run; extensions/<id> too). An argument that is
#                                   neither an extension nor a git commit fails the release, not selects them all
# A version is released once: one that has its release already is skipped, unless the index does not have it yet.
set -euo pipefail
cd "$(dirname "$0")/.."
step() { printf '\n== %s\n' "$*"; }

step docs
python3 -I scripts/check-docs.py
python3 -I scripts/test-docs.py

step manifests
scripts/manifest.py check
python3 -I scripts/test-manifest.py

step gofmt
bad=$(gofmt -l . | grep -v '^dist/' || true)
[ -z "$bad" ] || { echo "not gofmt'ed:"; echo "$bad"; exit 1; }

# sdk/go is a nested module, so the root vet and tests below do not reach it.
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
python3 -I scripts/test-package.py

step index
scripts/index.py check
python3 -I scripts/test-index.py

step signature
scripts/sign-index.py verify
python3 -I scripts/test-sign-index.py

if [ "${1:-}" = release ]; then
  shift
  step release
  # Captured, not read from a process substitution: under set -e an argument that is neither an id nor a git
  # commit, or manifest.py's own git failing, stops the release here instead of an empty "nothing to release".
  ids=$(scripts/manifest.py selected "$@")
  [ -n "$ids" ] || echo "nothing to release"
  for id in $ids; do
    tag="$id-v$(scripts/manifest.py version "$id")"
    if gh release view "$tag" >/dev/null 2>&1; then
      if scripts/index.py has "$id" "$tag"; then
        echo "$tag: released already"
      else
        # Released, but the index commit after it was lost (a push racing another merge to main): put the package
        # that was uploaded into the index, not a new build of it (its bytes, so its sha256, may differ).
        echo "$tag: released, not in the index: adding it"
        gh release download "$tag" -p "$id-$(scripts/manifest.py version "$id").tar.gz" -D dist --clobber
        scripts/index.py add "$id" "$tag"
      fi
    else
      scripts/release.sh "$id"
    fi
  done
  scripts/index.py check
fi
echo
echo "ci: ok"
