#!/bin/bash
# Portable checks only. Publication is a separate gated workflow (docs/releases.md).
set -euo pipefail
[ "$#" -eq 0 ] || { printf '%s\n' 'ci: failed; publication arguments are not supported' >&2; exit 1; }
cd "$(dirname "$0")/.."
step() { printf '\n== %s\n' "$*"; }

# The source profiles' bundle checks refuse every Node but 22 and 24 (hosted CI uses 24.11.1): stop here rather than
# at the build step, where the checks after it would be left unrun.
step node
node_version=$(node -p process.versions.node 2>/dev/null || echo none)
case "${node_version%%.*}" in
  22|24) echo "node $node_version" ;;
  *) echo "ci: failed; Node 22 or 24 is required (the source profiles' bundle checks refuse others), found $node_version" >&2; exit 1 ;;
esac

step workflow
python3 -I scripts/test-ci-policy.py
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/ci.yml

step docs
python3 -I scripts/check-docs.py
python3 -I scripts/test-docs.py

step layout
python3 -I scripts/check-layout.py
python3 -I scripts/test-layout.py

# Each source profile's committed reference bundle embeds its current manifest (the hosted rebuild compares it byte
# for byte; this catches a manifest changed without regenerating it).
step sources
python3 -I scripts/test-source-rebuild.py
python3 -I scripts/source-rebuild.py plan

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
