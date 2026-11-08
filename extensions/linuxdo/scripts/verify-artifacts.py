"""Verify local provenance, archive bytes, protected files and exact private PCRE terms.
Run with Python -I; private paths/patterns are CLI inputs, never printed or stored here.
"""
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys
import tarfile
from urllib.parse import urlsplit

root = Path(__file__).resolve().parent.parent
repository = root.parent.parent
terms, protected = map(Path, sys.argv[1:3])
texts = []


def add_text(data):
    try:
        texts.append(data.decode("utf-8"))
    except UnicodeDecodeError:
        pass


def credentials(value):
    if isinstance(value, str):
        for raw in re.findall(r'https?://[^\s"<>]+', value):
            url = urlsplit(raw)
            assert not url.username and not url.password
            assert not re.search(r"auth|token|password|secret", url.query, re.I)
    elif isinstance(value, dict):
        for key, child in value.items():
            assert not re.fullmatch(r"_auth(?:Token)?|password|token|access_token|secret", key, re.I)
            credentials(child)
    elif isinstance(value, list):
        for child in value:
            credentials(child)


def check_lock(data, sdk, hidden=False):
    lock = json.loads(data)
    assert lock["lockfileVersion"] == 3
    public = links = 0
    for name, package in lock["packages"].items():
        if package.get("link"):
            assert name == "node_modules/@persona/extension" and package["resolved"] == sdk
            links += 1
        elif "resolved" in package:
            url = urlsplit(package["resolved"])
            assert url.scheme == "https" and url.netloc == "registry.npmjs.org"
            assert not url.username and not url.password and not url.query and not url.fragment
            assert re.fullmatch(r"sha512-[A-Za-z0-9+/]+={0,2}", package.get("integrity", ""))
            public += 1
    assert links == (0 if sdk is None else 1)
    if not hidden:
        if sdk is None:
            assert lock["packages"][""]["name"] == "@persona/extension"
        else:
            assert lock["packages"][""]["dependencies"]["@persona/extension"] == "file:" + sdk
            assert lock["packages"][sdk]["name"] == "@persona/extension"
    credentials(lock)
    return public, links


try:
    baseline = json.loads(protected.read_text())
    changed = sum(hashlib.sha256((repository / name).read_bytes()).hexdigest() != digest for name, digest in baseline.items())
    assert changed == 0
    print(f"protected tracked files: {len(baseline)} checked, changed={changed}")
    for label, path, sdk, hidden in [
        ("canonical", root / "package-lock.json", "../../sdk/ts", False),
        ("staged", root / "build/package/extensions/linuxdo/package-lock.json", "../../sdk/ts", False),
        ("hidden", root / "node_modules/.package-lock.json", "../../sdk/ts", True),
        ("SDK canonical", repository / "sdk/ts/package-lock.json", None, False),
        ("SDK staged", root / "build/package/sdk/ts/package-lock.json", None, False),
    ]:
        public, links = check_lock(path.read_bytes(), sdk, hidden)
        print(f"{label} lock: public={public} localSDK={links} other=0 credentials=0")
    package = root / "build/package"
    assert (package / "extensions/linuxdo/../../sdk/ts").resolve() == package / "sdk/ts"
    manifest = json.loads((package / "persona-extension.json").read_text())
    descriptor = manifest["runtime"]["source"]
    assert descriptor["entry"] == "extensions/linuxdo/src/index.ts"
    assert descriptor["bundle"] == "extensions/linuxdo/build/linuxdo.mjs"
    assert descriptor["sha256"] == hashlib.sha256((package / descriptor["bundle"]).read_bytes()).hexdigest()
    assert (package / "extensions/linuxdo/package-lock.json").read_bytes() == (root / "package-lock.json").read_bytes()
    assert (package / "extensions/linuxdo/persona-extension.json").read_bytes() == (root / "persona-extension.json").read_bytes()
    files = [p for p in package.rglob("*") if p.is_file()]
    assert not any(p.is_symlink() for p in package.rglob("*"))
    sdk_files = [p for p in (package / "sdk/ts").rglob("*") if p.is_file()]
    for p in sdk_files:
        assert p.read_bytes() == (repository / "sdk/ts" / p.relative_to(package / "sdk/ts")).read_bytes()
    print(f"packaged official SDK: {len(sdk_files)} unchanged authoring files")
    with tarfile.open(root / "build/linuxdo-source.tar.gz", "r:gz") as archive:
        members = archive.getmembers()
        seen = set()
        for member in members:
            name = PurePosixPath(member.name)
            assert not name.is_absolute() and ".." not in name.parts
            assert member.isdir() or member.isfile()
            assert not any(part in ("node_modules", ".git") or part.startswith(".npm") or part.endswith((".bak", ".old", "~")) for part in name.parts)
            if member.isfile():
                assert str(name) not in seen
                seen.add(str(name))
                data = archive.extractfile(member).read()
                assert data == (package / str(name)).read_bytes()
                add_text(data)
                if str(name) == "extensions/linuxdo/package-lock.json":
                    public, links = check_lock(data, "../../sdk/ts")
                    print(f"archive lock: public={public} localSDK={links} other=0 credentials=0")
        assert seen == {str(p.relative_to(package)) for p in files}
        print(f"archive: members={len(members)} files={len(seen)} byteMatches={len(seen)} links=0 escapes=0 backups=0")
    for path in [root / "src", root / "scripts", root / "tests", package, root / "build/tests"]:
        for p in path.rglob("*"):
            if p.is_file():
                add_text(p.read_bytes())
    for name in ["README.md", "package.json", "package-lock.json", "tsconfig.json", ".gitignore", "build/linuxdo.mjs"]:
        add_text((root / name).read_bytes())
    patterns = [line for line in terms.read_text().splitlines() if line.strip()]
    assert patterns, "private terms input must not be empty"
    blob = "\n".join(texts).encode()
    hits = errors = 0
    for pattern in patterns:
        code = subprocess.run(["grep", "-P", "-q", "-e", pattern], input=blob,
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=15).returncode
        hits += code == 0
        errors += code not in (0, 1)
    print(f"exact PCRE: patterns={len(patterns)} textObjects={len(texts)} hits={hits} errors={errors}")
    assert not hits and not errors
    for name in ["build/linuxdo.mjs", "build/linuxdo-source.tar.gz"]:
        print(f"artifact {name}: sha256={hashlib.sha256((root / name).read_bytes()).hexdigest()}")
    print("artifact/provenance/privacy verdict=PASS")
except subprocess.TimeoutExpired:
    print("artifact/provenance/privacy verdict=TIMEOUT")
    sys.exit(124)
except Exception as error:
    print("artifact/provenance/privacy verdict=FAIL (" + type(error).__name__ + ")")
    sys.exit(1)
