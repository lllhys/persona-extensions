#!/usr/bin/env python3
"""Run finite unreleased source checks against the public locked dependencies."""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    root = Path(__file__).resolve().parents[1]
    lock = root / "package-lock.json"
    before = hashlib.sha256(lock.read_bytes()).digest()
    if not shutil.which("bun"):
        print("sentry source Bun: FAIL (required runner unavailable)", flush=True)
        return 1
    with tempfile.TemporaryDirectory(prefix=".source-ci-", dir=root) as folder:
        commands = [
            ("public-lock-before", ["node", "scripts/check-lock.mjs"], 30),
            ("npm-ci", ["npm", "ci", "--cache=" + str(Path(folder) / "cache"), "--registry=https://registry.npmjs.org", "--ignore-scripts", "--no-audit", "--no-fund"], 180),
            ("public-lock-after-ci", ["node", "scripts/check-lock.mjs"], 30),
            ("typecheck", ["npm", "run", "typecheck"], 60),
            ("node-tests", ["npm", "test"], 180),
            ("bun-tests", ["npm", "run", "test:bun"], 180),
            ("build-source", ["npm", "run", "build:source"], 60),
            ("public-lock-after-build", ["node", "scripts/check-lock.mjs"], 30),
            ("original-go-parity", ["python3", "-I", "scripts/test-go-reference.py"], 300),
            ("standalone-authoring-rebuild", ["python3", "-I", "scripts/test-authoring-rebuild.py"], 360),
        ]
        for label, command, timeout in commands:
            try:
                result = subprocess.run(command, cwd=root, env=os.environ.copy(), timeout=timeout, check=False)
            except subprocess.TimeoutExpired:
                print(f"sentry source {label}: TIMEOUT ({timeout}s)", flush=True)
                return 124
            except OSError:
                print(f"sentry source {label}: FAIL (command unavailable)", flush=True)
                return 1
            print(f"sentry source {label}: {'PASS' if result.returncode == 0 else 'FAIL'} (exit {result.returncode})", flush=True)
            if result.returncode:
                return result.returncode
            if hashlib.sha256(lock.read_bytes()).digest() != before:
                print("sentry source lock stability: FAIL", flush=True)
                return 1
    print(f"sentry source CI: PASS ({len(commands)} commands, canonical lock unchanged; local regressions only)", flush=True)
    print("sentry acceptance: BLOCKED (SDK notification/init exact parameters, original wildcard broker gate, provisional SDK CI and installed-hub acceptance)", flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
