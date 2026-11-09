"""Audit and safely unpack this local source artifact; never print rejected URLs."""
from pathlib import Path, PurePosixPath
import hashlib
import json
import re
import subprocess
import sys
import tarfile
from urllib.parse import urlsplit

root, destination, terms, checker = map(Path, sys.argv[1:])
build = root / "build"
meta = json.loads((build / "source.json").read_text())
archive = build / "v2ex-source.tar.gz"
assert hashlib.sha256(archive.read_bytes()).hexdigest() == meta["archive_sha256"]
assert not list(destination.iterdir()), "unpack destination must be empty"
files = {}
with tarfile.open(archive, "r:gz") as tar:
    names = set()
    for item in tar.getmembers():
        p = PurePosixPath(item.name)
        assert not p.is_absolute() and ".." not in p.parts and str(p) == item.name
        assert item.name not in names and (item.isdir() or item.isfile())
        assert not set(p.parts) & {"node_modules", ".npmrc", ".git", "backup"}
        names.add(item.name)
        if item.isfile():
            data = tar.extractfile(item).read()
            assert data == (build / "package" / item.name).read_bytes()
            files[item.name] = data
            target = destination / item.name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
assert files["build/v2ex.mjs"] == (build / "v2ex.mjs").read_bytes()
assert hashlib.sha256(files["build/v2ex.mjs"]).hexdigest() == meta["source"]["sha256"]
for name in files:
    author = "authoring/extensions/v2ex/"
    sdk = "authoring/sdk/ts/"
    if name.startswith(author):
        assert files[name] == (root / name[len(author):]).read_bytes()
    elif name.startswith(sdk):
        assert files[name] == (root / "../../sdk/ts" / name[len(sdk):]).read_bytes()

locks = [root / "package-lock.json", build / "package/package-lock.json", destination / "package-lock.json", destination / "authoring/extensions/v2ex/package-lock.json", root / "node_modules/.package-lock.json",
         root / "../../sdk/ts/package-lock.json", destination / "authoring/sdk/ts/package-lock.json"]
counts = []
for path in locks:
    text = path.read_text()
    assert not re.search(r'"(?:_auth(?:Token)?|password|username|access_token|token)"\s*:', text, re.I)
    public = local = 0
    for name, pkg in json.loads(text)["packages"].items():
        resolved = pkg.get("resolved")
        if not resolved:
            continue
        if name == "node_modules/@persona/extension" and pkg.get("link") and resolved == "../../sdk/ts":
            local += 1
            continue
        u = urlsplit(resolved)
        assert u.scheme == "https" and u.hostname == "registry.npmjs.org"
        assert not u.username and not u.password and not u.query and not u.fragment and not u.port and pkg.get("integrity")
        public += 1
    counts.append({"public": public, "local_sdk": local})
credentials = re.compile(r'(?:-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----|AKIA[A-Z0-9]{16}|npm_[A-Za-z0-9]{36}|//[^\s]+:_authToken\s*=)')
for data in files.values():
    assert not credentials.search(data.decode()), "credential scan failed"
paths = [str(destination / name) for name in files]
result = subprocess.run(["python3", "-I", str(checker), str(terms), *paths], capture_output=True, text=True, timeout=130)
assert result.returncode == 0, "privacy term scan failed or errored"
print(json.dumps({"verdict": "PASS", "archive_files": len(files), "staged_archive_source_equal": True, "sdk_byte_identical": True, "safe_paths": True, "locks": counts, "privacy": result.stdout.strip(), **meta}))
