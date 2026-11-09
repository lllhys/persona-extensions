#!/usr/bin/env python3
"""Verify local markets source provenance, archive boundaries and frozen files.

Run with python3 -I. The private PCRE terms file is a required argument, never
copied into the package. Rejected URLs, privacy patterns and matches are not logged.
"""
import argparse
import hashlib
import json
import pathlib
import re
import subprocess
import tarfile
import urllib.parse


def check(condition, message):
    if not condition:
        raise RuntimeError(message)


def verify_lock(data):
    lock = json.loads(data)
    public = 0
    for name, entry in lock["packages"].items():
        source = entry.get("resolved")
        if not source:
            continue
        if name == "node_modules/@persona/extension" and entry.get("link") is True and source == "../../sdk/ts":
            continue
        url = urllib.parse.urlsplit(source)
        check(url.scheme == "https" and url.netloc == "registry.npmjs.org" and not url.username and not url.password and not url.query and not url.fragment and entry.get("integrity", "").startswith("sha512-"), "FAIL unapproved dependency provenance (URL withheld)")
        public += 1
    return public


def synthetic_credential_fixtures():
    """Keep the existing exact refusal fixtures; never allow another origin or credential."""
    registry = urllib.parse.urlsplit("https://registry.npmjs.org/package.tgz")
    provider = urllib.parse.urlsplit("https://www.okx.com/x")
    return {
        registry._replace(netloc=f"fixture@{registry.netloc}").geturl(),
        registry._replace(query="token=fixture").geturl(),
        provider._replace(netloc=f"user@{provider.netloc}").geturl(),
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--terms", type=pathlib.Path, required=True)
    parser.add_argument("--extracted", type=pathlib.Path)
    args = parser.parse_args()
    rules = [line for line in args.terms.read_text().splitlines() if line.strip()]
    check(bool(rules), "FAIL empty privacy terms file")
    for ordinal, rule in enumerate(rules, 1):
        result = subprocess.run(["grep", "-P", "-q", "--", rule], input=b"", stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
        check(result.returncode == 1, f"FAIL invalid PCRE rule {ordinal} (pattern withheld)")
    root = pathlib.Path(__file__).resolve().parent.parent
    repository = root.parent.parent
    stage = root / "build/package"
    archive = root / "build/markets-source.tar.gz"
    source_files = [root / name for name in ["README.md", ".gitignore", "package.json", "package-lock.json", "tsconfig.json"]]
    for name in ["src", "tests", "scripts"]:
        source_files.extend(p for p in (root / name).rglob("*") if p.is_file())
    candidates = [("authoring", p.read_bytes()) for p in source_files]
    staged = {}
    for path in stage.rglob("*"):
        check(not path.is_symlink(), "FAIL symlink in stage")
        if path.is_file():
            staged[path.relative_to(stage).as_posix()] = path.read_bytes()
    candidates.extend(("stage", data) for data in staged.values())
    archived = {}
    with tarfile.open(archive, "r:gz") as package:
        members = package.getmembers()
        for member in members:
            path = pathlib.PurePosixPath(member.name)
            check(not path.is_absolute() and ".." not in path.parts and "\\" not in member.name and (member.isfile() or member.isdir()), "FAIL unsafe archive member")
            check(member.uid == member.gid == member.mtime == 0, "FAIL nondeterministic archive metadata")
            check(path.name not in [".npmrc", ".yarnrc", ".pnp.cjs"] and "node_modules" not in path.parts, "FAIL package-manager configuration in archive")
            if member.isfile():
                check(member.name not in archived, "FAIL duplicate archive member")
                archived[member.name] = package.extractfile(member).read()
    check(staged == archived, "FAIL stage/archive bytes differ")
    candidates.extend(("archive", data) for data in archived.values())
    extension = "extensions/markets/"
    canonical = (root / "package-lock.json").read_bytes()
    check(canonical == staged[extension + "package-lock.json"], "FAIL canonical/staged lock bytes differ")
    locks = {"canonical": canonical, "stage": staged[extension + "package-lock.json"], "archive": archived[extension + "package-lock.json"]}
    hidden = root / "node_modules/.package-lock.json"
    if hidden.exists():
        locks["hidden"] = hidden.read_bytes()
    if args.extracted:
        for name, data in archived.items():
            check((args.extracted / name).read_bytes() == data, "FAIL extracted/archive bytes differ")
        unpacked = args.extracted / "extensions/markets"
        check((unpacked / "package-lock.json").read_bytes() == canonical, "FAIL extracted lock changed")
        locks["extracted"] = (unpacked / "package-lock.json").read_bytes()
        locks["extracted-hidden"] = (unpacked / "node_modules/.package-lock.json").read_bytes()
        check((unpacked / "build/markets.mjs").read_bytes() == (root / "build/markets.mjs").read_bytes(), "FAIL standalone authoring rebuild differs")
    counts = {name: verify_lock(data) for name, data in locks.items()}
    # Installed hidden locks are independently privacy-scanned as well as origin-checked.
    candidates.extend((name, data) for name, data in locks.items() if "hidden" in name)
    sdk_root = repository / "sdk/ts"
    excluded = {"node_modules", "build", "dist", ".git", "__pycache__"}
    sdk_files = {"sdk/ts/" + p.relative_to(sdk_root).as_posix(): p.read_bytes()
                 for p in sdk_root.rglob("*") if p.is_file()
                 and not any(part in excluded for part in p.relative_to(sdk_root).parts)}
    copied_sdk = {name: data for name, data in staged.items() if name.startswith("sdk/ts/")}
    check(copied_sdk == sdk_files, "FAIL complete official SDK authoring closure differs")
    manifest = json.loads(staged["persona-extension.json"])
    bundle = staged[manifest["runtime"]["source"]["bundle"]]
    check(manifest["runtime"]["source"]["entry"] in staged, "FAIL missing host authoring entry")
    check(bundle == staged[extension + "build/markets.mjs"], "FAIL host/authoring bundle bytes differ")
    check(hashlib.sha256(bundle).hexdigest() == manifest["runtime"]["source"]["sha256"], "FAIL final bundle hash mismatch")
    check(staged["persona-plugin.json"] == staged["persona-extension.json"], "FAIL host manifest aliases differ")
    check(staged[extension + "persona-plugin.json"] == staged[extension + "persona-extension.json"], "FAIL authoring manifest aliases differ")
    # This comparison covers every tracked file except the authorized markets README.
    diff = subprocess.run(["git", "-C", str(repository), "diff", "--quiet", "HEAD", "--", ".", ":!extensions/markets/README.md"], timeout=30)
    check(diff.returncode == 0, "FAIL frozen tracked files differ from HEAD")
    frozen = subprocess.check_output(["git", "-C", str(repository), "ls-files", "-z"], timeout=30).split(b"\0")
    frozen_count = sum(bool(path) and path != b"extensions/markets/README.md" for path in frozen)
    payload = b"\n".join(data for _, data in candidates)
    for ordinal, rule in enumerate(rules, 1):
        result = subprocess.run(["grep", "-P", "-q", "--", rule], input=payload, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
        check(result.returncode != 2, f"FAIL invalid PCRE rule {ordinal} (pattern withheld)")
        check(result.returncode == 1, f"FAIL privacy rule {ordinal} matched (content withheld)")
    # Credentials are allowed only as these exact, synthetic refusal fixtures.
    synthetic = synthetic_credential_fixtures()
    urls = re.findall(rb"https?://[^\s\"'<>`\\)]+", payload)
    credential_fixtures = 0
    for raw in urls:
        text = raw.decode("utf-8")
        url = urllib.parse.urlsplit(text)
        if url.username or url.password or re.search(r"(?:token|password|secret|key)=", url.query, re.I):
            check(text in synthetic, "FAIL credential-shaped URL (content withheld)")
            credential_fixtures += 1
    print(json.dumps({"verdict": "PASS", "scanned_files": len(candidates), "pcre_nonempty_rules": len(rules), "privacy_matches": 0, "urls": len(urls), "synthetic_credential_fixtures": credential_fixtures, "public_npm_origins": counts, "archive_members": len(members), "frozen_tracked_files": frozen_count, "bundle_sha256": hashlib.sha256(bundle).hexdigest(), "archive_sha256": hashlib.sha256(archive.read_bytes()).hexdigest()}, sort_keys=True))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(str(error) if isinstance(error, RuntimeError) else "FAIL verification error (details withheld)")
        raise SystemExit(1)
