#!/usr/bin/env python3
"""Cold-install and rebuild a local source archive in an isolated authoring tree."""
import hashlib
import json
import os
import pathlib
import shutil
import signal
import subprocess
import sys
import tarfile
import tempfile


def run(label, command, cwd, env, log, timeout=180):
    with log.open("wb") as output:
        process = subprocess.Popen(command, cwd=cwd, env=env, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
        try:
            code = process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)
            print("VERDICT " + label + ": TIMEOUT", flush=True)
            raise SystemExit(1)
    print("VERDICT " + label + (": PASS" if code == 0 else ": FAIL"), flush=True)
    if code:
        raise SystemExit(1)


def verify_lock(path):
    from urllib.parse import urlsplit
    lock = json.loads(path.read_bytes())
    count = 0
    for entry in lock["packages"].values():
        if not entry.get("resolved"):
            continue
        if entry.get("link"):
            assert entry["resolved"] == "../../sdk/ts"
            continue
        url = urlsplit(entry["resolved"])
        assert url.scheme == "https" and url.netloc == "registry.npmjs.org"
        assert not url.username and not url.password and not url.query and not url.fragment
        assert entry.get("version") and entry.get("integrity")
        count += 1
    return count


def main():
    root = pathlib.Path(sys.argv[1]).resolve()
    work = pathlib.Path(tempfile.mkdtemp(prefix="authoring-", dir=root / ".test-dist"))
    extracted = work / "archive"
    extracted.mkdir()
    archive = root / ".source-dist/github-source.tar.gz"
    with tarfile.open(archive) as packed:
        members = packed.getmembers()
        assert all((m.isdir() or m.isfile()) and not m.name.startswith("/") and ".." not in pathlib.PurePosixPath(m.name).parts for m in members)
        # Write only validated regular files into the fresh empty tree; no version-specific extract filters.
        for member in members:
            destination = extracted / member.name
            if member.isdir():
                destination.mkdir(parents=True, exist_ok=True)
            else:
                destination.parent.mkdir(parents=True, exist_ok=True)
                with packed.extractfile(member) as source, destination.open("xb") as target:
                    shutil.copyfileobj(source, target)
    authored = extracted / "github/source/extensions/github"
    # Use ordinary npm configuration with an explicit public registry and fresh cache.
    # This verifier never changes HOME or a global/user config file.
    env = dict(os.environ)
    run("cold-public-npm", ["npm", "ci", "--ignore-scripts", "--no-audit", "--fund=false", "--registry=https://registry.npmjs.org/", "--cache=" + str(work / "npm-cache")], authored, env, work / "install.log")
    sdk = extracted / "github/source/sdk/ts"
    run("cold-official-sdk-npm", ["npm", "ci", "--ignore-scripts", "--no-audit", "--fund=false", "--registry=https://registry.npmjs.org/", "--cache=" + str(work / "sdk-npm-cache")], sdk, env, work / "sdk-install.log")
    counts = [verify_lock(authored / name) for name in ["package-lock.json", "node_modules/.package-lock.json"]]
    counts.extend(verify_lock(sdk / name) for name in ["package-lock.json", "node_modules/.package-lock.json"])
    run("extracted-authoring-tests-rebuild", ["npm", "test"], authored, env, work / "test.log")
    run("extracted-official-program-check", ["node", str(sdk / "scripts/check-source-bundle.mjs"), "--root", str(authored / ".source-dist/github"), "--bundle", "bundle/main.mjs", "--manifest", "persona-extension.json", "--probe-trusted"], root, env, work / "official-check.log")
    run("extracted-source-verification", ["python3", "-I", str(authored / "scripts/verify-source.py"), str(authored)], root, env, work / "verify.log")
    original_bundle = (root / ".source-dist/github/bundle/main.mjs").read_bytes()
    rebuilt = (authored / ".source-dist/github/bundle/main.mjs").read_bytes()
    assert original_bundle == rebuilt, "Extracted authoring bundle differs from original"
    assert archive.read_bytes() == (authored / ".source-dist/github-source.tar.gz").read_bytes(), "Deterministic extracted archive differs"
    print(json.dumps({"verdict": "PASS", "work": str(work), "public_lock_counts": counts, "authoring_bundle_equal": True, "archive_equal": True, "bundle_sha256": hashlib.sha256(rebuilt).hexdigest()}))


if __name__ == "__main__":
    try:
        main()
    except (AssertionError, OSError, ValueError, tarfile.TarError):
        print("VERDICT authoring-verification: FAIL")
        raise SystemExit(1)
