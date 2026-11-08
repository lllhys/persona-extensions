#!/usr/bin/env python3
"""Cold-rebuild the standalone authoring archive, using only its complete SDK closure."""
import hashlib
import os
from pathlib import Path, PurePosixPath
import subprocess
import tarfile
import tempfile


def main():
    root = Path(__file__).resolve().parents[1]
    archive = root / ".source-dist/sentry-authoring.tar.gz"
    expected = {name: hashlib.sha256((root / ".source-dist" / name).read_bytes()).digest()
                for name in ("sentry-source.tar.gz", "sentry-authoring.tar.gz")}
    expected_bundle = (root / ".source-dist/sentry/bundle/main.mjs").read_bytes()
    with tempfile.TemporaryDirectory(prefix=".authoring-rebuild-", dir=root) as folder:
        scratch = Path(folder)
        # Never supply or copy an external SDK into the extracted layout.
        with tarfile.open(archive, "r:gz") as tar:
            names = set()
            for member in tar.getmembers():
                path = PurePosixPath(member.name)
                if member.name in names or path.is_absolute() or ".." in path.parts or not path.parts or path.parts[0] != "sentry-authoring" or not (member.isdir() or member.isfile()):
                    raise ValueError("invalid authoring archive entry")
                names.add(member.name)
                dest = scratch.joinpath(*path.parts)
                if member.isdir():
                    dest.mkdir(parents=True, exist_ok=True)
                else:
                    dest.parent.mkdir(parents=True, exist_ok=True)
                    dest.write_bytes(tar.extractfile(member).read())
        ext = scratch / "sentry-authoring/extensions/sentry"
        env = os.environ.copy()
        commands = [
            ("archive-lock", ["node", "scripts/check-lock.mjs"], 30),
            ("cold-install", ["npm", "ci", "--cache=" + str(scratch / "cache"), "--registry=https://registry.npmjs.org", "--ignore-scripts", "--no-audit", "--no-fund"], 180),
            ("installed-lock", ["node", "scripts/check-lock.mjs"], 30),
            ("typecheck", ["npm", "run", "typecheck"], 60),
            ("rebuild-and-test", ["npm", "test"], 180),
        ]
        for label, command, timeout in commands:
            try:
                result = subprocess.run(command, cwd=ext, env=env, timeout=timeout, check=False)
            except subprocess.TimeoutExpired:
                print(f"authoring {label}: TIMEOUT ({timeout}s)", flush=True)
                return 124
            print(f"authoring {label}: {'PASS' if result.returncode == 0 else 'FAIL'} (exit {result.returncode})", flush=True)
            if result.returncode:
                return result.returncode
        if (ext / "package-lock.json").read_bytes() != (root / "package-lock.json").read_bytes():
            raise ValueError("authoring canonical lock changed")
        check = 'import {checkLock} from "./scripts/check-lock.mjs"; console.log(await checkLock("node_modules/.package-lock.json"));'
        result = subprocess.run(["node", "--input-type=module", "-e", check], cwd=ext, env=env, timeout=30, check=False)
        if result.returncode:
            return result.returncode
        if (ext / ".source-dist/sentry/bundle/main.mjs").read_bytes() != expected_bundle:
            raise ValueError("standalone authoring bundle differs")
        for name, digest in expected.items():
            if hashlib.sha256((ext / ".source-dist" / name).read_bytes()).digest() != digest:
                raise ValueError("standalone portable archive differs")
        print("standalone authoring rebuild: PASS (complete archived official SDK; cold public install; identical bundle and both archives)", flush=True)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, subprocess.TimeoutExpired):
        print("standalone authoring rebuild: FAIL (invalid input, provenance or rebuild mismatch)", flush=True)
        raise SystemExit(1)
