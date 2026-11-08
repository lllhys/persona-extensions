#!/usr/bin/env python3
"""Verify an unreleased source archive's containment, dependency provenance and credentials."""
import hashlib
import json
import pathlib
import re
import sys
import tarfile
import urllib.parse


def verify_lock(raw):
    lock = json.loads(raw)
    public = 0
    for name, entry in lock["packages"].items():
        resolved = entry.get("resolved", "")
        if not resolved:
            continue
        if entry.get("link"):
            assert resolved == "../../sdk/ts", "Unexpected local dependency"
            continue
        url = urllib.parse.urlsplit(resolved)
        assert url.scheme == "https" and url.netloc == "registry.npmjs.org", "Nonpublic registry source"
        assert not url.username and not url.password and not url.query and not url.fragment, "Credential-bearing dependency URL"
        assert entry.get("integrity") and entry.get("version"), "Missing dependency integrity or version"
        public += 1
    return public


def main():
    root = pathlib.Path(sys.argv[1]).resolve()
    archive = root / ".source-dist/github-source.tar.gz"
    with tarfile.open(archive) as packed:
        members = packed.getmembers()
        assert all(not m.issym() and not m.islnk() and not m.name.startswith("/") and ".." not in pathlib.PurePosixPath(m.name).parts for m in members), "Uncontained archive entry"
        files = {m.name: packed.extractfile(m).read() for m in members if m.isfile()}
    base = "github/source/extensions/github/"
    authored = json.loads((root / "package-lock.json").read_bytes())
    shipped = json.loads(files[base + "package-lock.json"])
    assert authored == shipped, "Package and authored lock differ"
    assert files[base + "persona-extension.json"] == (root / "persona-extension.json").read_bytes(), "Released manifest changed in source copy"
    lock_counts = []
    for path in [root / "package-lock.json", root / "node_modules/.package-lock.json", root / ".source-dist/github/source/extensions/github/package-lock.json"]:
        lock_counts.append(verify_lock(path.read_bytes()))
    for name, data in files.items():
        if name.endswith("package-lock.json"):
            lock_counts.append(verify_lock(data))
    source_files = 0
    for name, data in files.items():
        if name.startswith("github/source/"):
            local = root.parent.parent / name.removeprefix("github/source/")
            assert local.is_file() and not local.is_symlink() and local.read_bytes() == data, "Archive source differs from authored input"
            source_files += 1
    assert files["github/persona-plugin.json"] == files["github/persona-extension.json"], "Source manifest aliases differ"
    for name in ("src/index.ts", "src/root.tsx", "src/page.tsx", "src/service.ts", "tests/regressions.test.tsx", "scripts/test.mjs", "src/locales/en.json", "src/locales/zh-Hans.json", "src/locales/ja.json"):
        assert base + name in files, "Missing source input"
    assert "github/source/sdk/ts/src/index.ts" in files, "Relative SDK source missing"
    manifest = json.loads(files["github/persona-extension.json"])
    original = json.loads((root / "persona-extension.json").read_bytes())
    assert manifest["version"] == original["version"] and manifest["permissions"] == original["permissions"], "Release identity or grants changed"
    assert manifest["contributes"] == original["contributes"] and manifest["configuration"] == original["configuration"], "Contribution IDs or configuration changed"
    sdk = root.parent.parent / "sdk/ts"
    official = {path.relative_to(sdk).as_posix(): path.read_bytes() for path in sdk.rglob("*") if path.is_file() and not any(part in {"node_modules", ".test-dist", ".source-dist"} for part in path.relative_to(sdk).parts)}
    shipped_sdk = {name.removeprefix("github/source/sdk/ts/"): data for name, data in files.items() if name.startswith("github/source/sdk/ts/")}
    assert official == shipped_sdk, "Complete official SDK module closure is not byte-identical"
    assert manifest["runtime"]["source"]["entry"] == "source/extensions/github/src/index.ts"
    bundle = files["github/" + manifest["runtime"]["source"]["bundle"]]
    assert manifest["runtime"]["source"]["sha256"] == hashlib.sha256(bundle).hexdigest(), "Bundle hash mismatch"
    # These scans are independent of the private PCRE terms. Do not print matched material.
    userinfo = re.compile(r"https?://[^\s/@\"'<>]+(?::[^\s/@\"'<>]*)?@")
    query_credentials = re.compile(r"https?://[^\s\"'<>]+[?&](?:access_token|token|api_key|apikey|password|client_secret|_authToken)=[^&#\s\"'<>]+", re.I)
    # The immutable Go manifest documents an optional proxy-auth syntax with bracketed placeholders.
    # Only its exact, invalid-as-a-real-URL template prefix is permitted; actual URL userinfo still fails.
    templates = {m.group() for m in userinfo.finditer((root / "persona-extension.json").read_text()) if m.group().startswith("http://[")}
    template_matches = 0
    for data in files.values():
        text = data.decode("utf-8", errors="replace")
        assert not re.search(r"(?:gh[pousr]_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{80,}|-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----)", text), "Credential material detected"
        for match in userinfo.finditer(text):
            assert match.group() in templates, "URL userinfo detected"
            template_matches += 1
        assert not query_credentials.search(text), "URL query credentials detected"
        assert not re.search(r'"(?:_authToken|_auth|password)"\s*:\s*"[^\"]+"', text, re.I), "Package credential detected"
    print(json.dumps({"verdict": "PASS", "archive_files": len(files), "source_files_equal": source_files, "lock_checks": len(lock_counts), "public_resolved_per_lock": lock_counts, "userinfo_credentials": 0, "query_credentials": 0, "immutable_proxy_template_matches": template_matches, "relative_sdk": "contained", "archive_sha256": hashlib.sha256(archive.read_bytes()).hexdigest(), "archive_bytes": archive.stat().st_size, "bundle_sha256": hashlib.sha256(bundle).hexdigest(), "bundle_bytes": len(bundle)}))


if __name__ == "__main__":
    try:
        main()
    except (AssertionError, OSError, ValueError, KeyError, tarfile.TarError) as error:
        print("Source verification: FAIL (" + str(error) + ")")
        raise SystemExit(1)
