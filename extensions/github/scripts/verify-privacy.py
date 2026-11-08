#!/usr/bin/env python3
"""Scan draft inputs and contained outputs using an externally supplied exact PCRE list."""
import json
import pathlib
import subprocess
import sys


def main():
    root, terms = (pathlib.Path(p).resolve() for p in sys.argv[1:])
    files = []
    for name in ["src", "tests", "scripts", ".source-dist/github"]:
        files.extend(p for p in (root / name).rglob("*") if p.is_file() and not p.is_symlink())
    files.extend(root / name for name in ["package.json", "package-lock.json", "node_modules/.package-lock.json", "tsconfig.json", "README.md", "persona-extension.json"])
    files.extend((root / ".test-dist").glob("*.mjs"))
    files = sorted(set(files))
    patterns = [pattern for pattern in terms.read_text().splitlines() if pattern]
    assert patterns, "Empty PCRE vocabulary"
    for index, pattern in enumerate(patterns):
        try:
            result = subprocess.run(["grep", "-P", "-l", "--", pattern, *map(str, files)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30, check=False)
        except subprocess.TimeoutExpired:
            print("VERDICT exact-PCRE: TIMEOUT")
            raise SystemExit(1)
        if result.returncode == 0:
            print("VERDICT exact-PCRE: FAIL (matched material withheld)")
            raise SystemExit(1)
        if result.returncode != 1:
            print("VERDICT exact-PCRE: ERROR (pattern index " + str(index + 1) + ")")
            raise SystemExit(1)
    print(json.dumps({"verdict": "PASS", "exact_pcre_patterns": len(patterns), "files": len(files), "matches": 0}))


if __name__ == "__main__":
    try:
        main()
    except (AssertionError, OSError, ValueError):
        print("VERDICT exact-PCRE: ERROR (no scan completed)")
        raise SystemExit(1)
