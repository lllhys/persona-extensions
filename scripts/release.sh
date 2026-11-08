#!/bin/bash
# Authorized OCI publication only; never creates, edits or deletes GitHub Releases or git tags.
# Explicit cutover identity and confirmation are mandatory; see docs/releases.md.
set -euo pipefail
cd "$(dirname "$0")/.."
exec python3 -I scripts/publish-oci.py push "$@"
