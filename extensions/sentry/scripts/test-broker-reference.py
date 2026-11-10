#!/usr/bin/env python3
"""Exercise the original wildcard through an explicit unchanged public broker checkout."""
import hashlib
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def regular_copy(source, destination):
    if source.is_symlink() or not source.is_file():
        raise ValueError("broker fixture requires regular official source files")
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
    if hashlib.sha256(source.read_bytes()).digest() != hashlib.sha256(destination.read_bytes()).digest():
        raise ValueError("broker fixture copy mismatch")


def main():
    root = Path(__file__).resolve().parents[1]
    upstream = Path(sys.argv[1]).resolve()
    # No Git commands, credentials, hub installation or network fallback here.
    with tempfile.TemporaryDirectory(prefix=".broker-reference-", dir=root) as folder:
        layout = Path(folder)
        for directory in ("server/internal/extension/egress", "server/internal/acceptloop"):
            for source in (upstream / directory).glob("*.go"):
                regular_copy(source, layout / source.relative_to(upstream / "server"))
        for source in (upstream / "sdk/go").rglob("*"):
            if source.is_file():
                regular_copy(source, layout / source.relative_to(upstream))
        (layout / "go.mod").write_text("module github.com/nwverse/persona/server\n\ngo 1.27.0\n\nrequire github.com/nwverse/persona/sdk/go v0.0.0\nreplace github.com/nwverse/persona/sdk/go => ./sdk/go\n")
        package = layout / "internal/extension/egress"
        regular_copy(root / "tests/broker-oracle.go.txt", package / "sentry_wildcard_test.go")
        regular_copy(root / "build/package/persona-extension.json", package / "sentry-source-manifest.json")
        try:
            result = subprocess.run(["go", "test", "-race", "-count=1", "-timeout=60s", "-run=^TestSentryOriginalWildcardBrokerFixture$", "-v", "./internal/extension/egress"], cwd=layout, timeout=120, check=False)
        except subprocess.TimeoutExpired:
            print("original wildcard broker fixture: TIMEOUT (120s)", flush=True)
            return 124
        print(f"original wildcard broker fixture: {'PASS' if result.returncode == 0 else 'FAIL'} (synthetic policy/validation only; no hub acceptance)", flush=True)
        return result.returncode


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, IndexError):
        print("original wildcard broker fixture: FAIL (explicit official checkout required)", flush=True)
        raise SystemExit(1)
