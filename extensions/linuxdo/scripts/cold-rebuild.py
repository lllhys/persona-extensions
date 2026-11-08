"""Cold authoring rebuild in a new local directory; never inherit npm credentials."""
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import subprocess
import sys
import tarfile
import tempfile

root = Path(__file__).resolve().parent.parent
space = Path(tempfile.mkdtemp(prefix="round2-cold-", dir=root / "build"))
source = space / "authoring"
source.mkdir()
for name in ["home", "cache"]:
    (space / name).mkdir()
for name in ["user.npmrc", "global.npmrc"]:
    (space / name).write_text("registry=https://registry.npmjs.org\n")
config_only = sys.argv[1:] == ["--config-only"]
if sys.argv[1:] and not config_only:
    raise SystemExit("usage: cold-rebuild.py [--config-only]")
# Config-only is a separate, explicitly weaker proof: keep the caller's HOME.
# Use it where changing HOME is not authorized; never label it a fresh-HOME run.
env = {"PATH": os.environ["PATH"]}
if config_only:
    if "HOME" in os.environ:
        env["HOME"] = os.environ["HOME"]
else:
    env["HOME"] = str(space / "home")
config_flags = ["--userconfig=" + str(space / "user.npmrc"), "--globalconfig=" + str(space / "global.npmrc"),
                "--cache=" + str(space / "cache"), "--registry=https://registry.npmjs.org"]
proof = "config/cache isolation only; caller HOME unchanged" if config_only else "independent HOME/config/cache"


def check_lock(path, sdk, hidden=False):
    lock = json.loads(path.read_text())
    resolved = links = 0
    from urllib.parse import urlsplit
    for name, package in lock["packages"].items():
        if package.get("link"):
            assert name == "node_modules/@persona/extension" and package["resolved"] == sdk
            links += 1
        elif "resolved" in package:
            url = urlsplit(package["resolved"])
            assert url.scheme == "https" and url.netloc == "registry.npmjs.org"
            assert not url.username and not url.password and not url.query and not url.fragment
            assert package.get("integrity", "").startswith("sha512-")
            resolved += 1
    assert links == (0 if sdk is None else 1)
    if not hidden:
        if sdk is None:
            assert lock["packages"][""]["name"] == "@persona/extension"
        else:
            assert lock["packages"][""]["dependencies"]["@persona/extension"] == "file:" + sdk
            assert lock["packages"][sdk]["name"] == "@persona/extension"
    print(f"cold provenance: {'hidden' if hidden else 'canonical'} public={resolved} localSDK={links}")


try:
    with tarfile.open(root / "build/linuxdo-source.tar.gz", "r:gz") as archive:
        members = archive.getmembers()
        for member in members:
            name = PurePosixPath(member.name)
            assert not name.is_absolute() and ".." not in name.parts
            assert member.isdir() or member.isfile()
            assert "node_modules" not in name.parts and not any(part.startswith(".npm") for part in name.parts)
        assert sum(member.size for member in members) < 16 << 20
        # Only prevalidated regular files/directories in a newly created empty root;
        # no links or escape components. This also works before Python's filter API.
        archive.extractall(source, members=members)
    extension = source / "extensions/linuxdo"
    check_lock(extension / "package-lock.json", "../../sdk/ts")
    assert (extension / "../../sdk/ts").resolve() == source / "sdk/ts"
    check_lock(source / "sdk/ts/package-lock.json", None)
    print("extracted rebuild proof: " + proof)
    for label, args, cwd, seconds in [
        ("sdk-install", ["npm", *config_flags, "ci", "--ignore-scripts", "--no-audit", "--no-fund"], source / "sdk/ts", 120),
        ("install", ["npm", *config_flags, "ci", "--ignore-scripts", "--no-audit", "--no-fund"], extension, 120),
        ("typecheck", ["npm", *config_flags, "run", "typecheck"], extension, 60),
        ("source-build", ["npm", *config_flags, "run", "pack:source"], extension, 60),
        ("test-rebuild", ["npm", *config_flags, "test"], extension, 180),
    ]:
        with (space / (label + ".log")).open("wb") as log:
            run = subprocess.run(args, cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT, timeout=seconds)
        print(f"cold {label}: {'PASS' if run.returncode == 0 else 'FAIL'} exit={run.returncode}")
        if run.returncode:
            raise RuntimeError(label + " failed; inspect its local log")
    check_lock(extension / "node_modules/.package-lock.json", "../../sdk/ts", hidden=True)
    rebuilt = extension / "build/package"
    check_lock(rebuilt / "extensions/linuxdo/package-lock.json", "../../sdk/ts")
    manifest = json.loads((rebuilt / "persona-extension.json").read_text())
    bundle = rebuilt / manifest["runtime"]["source"]["bundle"]
    assert manifest["runtime"]["source"]["sha256"] == hashlib.sha256(bundle.read_bytes()).hexdigest()
    print("cold authoring rebuild: PASS (real generated hash; " + proof + ")")
except subprocess.TimeoutExpired:
    print("cold authoring rebuild: TIMEOUT")
    sys.exit(124)
except Exception as error:
    print("cold authoring rebuild: FAIL (" + type(error).__name__ + ")")
    sys.exit(1)
finally:
    print("cold artifacts=" + str(space))
