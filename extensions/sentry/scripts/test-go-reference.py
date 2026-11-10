#!/usr/bin/env python3
"""Run the unchanged original Go race tests plus differential cases in a disposable layout."""
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    root = Path(__file__).resolve().parents[1]
    repo = root.parents[1]
    with tempfile.TemporaryDirectory(prefix=".go-reference-", dir=root) as folder:
        layout = Path(folder)
        ext = layout / "extensions/sentry"
        ext.mkdir(parents=True)
        shutil.copytree(repo / "sdk/go", layout / "sdk/go", symlinks=False)
        for name in ("main.go", "sentry.go", "sentry_test.go", "go.mod", "go.sum"):
            if (root / name).exists():
                shutil.copyfile(root / name, ext / name)
        released = root / "RELEASED-MANIFEST.reference.json"
        shutil.copyfile(released if released.exists() else root / "persona-extension.json", ext / "persona-extension.json")
        shutil.copyfile(root / "tests/reference.go.txt", ext / "round2_reference_test.go")
        shutil.copyfile(root / "tests/typed-oracle.go.txt", ext / "typed_oracle_test.go")
        shutil.copyfile(root / "tests/typed-cases.json", ext / "typed-cases.json")
        try:
            result = subprocess.run(["go", "test", "-race", "-count=1", "-timeout=90s", "-v", "./..."], cwd=ext, timeout=180, check=False)
        except subprocess.TimeoutExpired:
            print("original Go plus differential oracle: TIMEOUT (180s)", flush=True)
            return 124
        print(f"original Go plus differential oracle: {'PASS' if result.returncode == 0 else 'FAIL'} (exit {result.returncode})", flush=True)
        if result.returncode:
            return result.returncode
        try:
            parity = subprocess.run(["node", "scripts/go-parity.mjs", str(ext / "typed-oracle.json")], cwd=root, timeout=90, check=False)
        except subprocess.TimeoutExpired:
            print("source versus unchanged Go: TIMEOUT (90s)", flush=True)
            return 124
        print(f"source versus unchanged Go: {'PASS' if parity.returncode == 0 else 'FAIL'}", flush=True)
        return parity.returncode


if __name__ == "__main__":
    raise SystemExit(main())
