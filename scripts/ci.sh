#!/bin/bash
# Portable checks only. Publication is a separate gated workflow (docs/releases.md).
set -euo pipefail
[ "$#" -eq 0 ] || { printf '%s\n' 'ci: failed; publication arguments are not supported' >&2; exit 1; }
cd "$(dirname "$0")/.."
step() { printf '\n== %s\n' "$*"; }

step docs
python3 -I scripts/check-docs.py
python3 -I scripts/test-docs.py

step layout
python3 -I scripts/check-layout.py
python3 -I scripts/test-layout.py

step manifests
scripts/manifest.py check
python3 -I scripts/test-manifest.py

step gofmt
bad=$(gofmt -l . | grep -v '^dist/' || true)
[ -z "$bad" ] || { echo "not gofmt'ed:"; echo "$bad"; exit 1; }

# The copied SDK is checked as it comes from upstream; its go.mod is upstream's too, so no tidiness check.
step sdk
go -C sdk/go vet ./...
go -C sdk/go test -count=1 -timeout=90s ./...

# Every other module on its own: pluginkit, tools (the repository's own Go tests) and each extensions/<id>.
modules=$(python3 -I scripts/check-layout.py modules)

step tidy
for m in $modules; do
  go -C "$m" mod tidy -diff || { echo "$m: go.mod or go.sum is not tidy: run go -C $m mod tidy"; exit 1; }
done

step vet
for m in $modules; do go -C "$m" vet ./...; done

# -race: tests start goroutines (connection loops, watchers) next to package settings they shorten for the test.
step test
for m in $modules; do go -C "$m" test -race ./...; done

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

step publisher
python3 -I scripts/test-oci.py
python3 -I scripts/test-publish-oci.py
python3 -I scripts/test-publication-regressions.py
python3 -I scripts/test-rename.py

echo
echo "ci: ok"
