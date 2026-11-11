#!/usr/bin/env python3
"""Rebuild registered migration profiles on a fresh hosted runner, never a local HOME."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
LEGACY_EXAMPLES = {"hackernews", "showcase"}
MAX_BUNDLE = 8 << 20


def contained(root, name, required=True):
    if not isinstance(name, str) or not name or "\\" in name:
        raise ValueError("invalid contained path")
    path = PurePosixPath(name)
    if path.is_absolute() or any(part in {".", ".."} for part in name.split("/")):
        raise ValueError("path is not contained")
    current = root
    for part in path.parts:
        current = current / part
        if current.is_symlink():
            raise ValueError("source symlinks are forbidden")
    if required and not current.is_file():
        raise ValueError("required source file is missing: " + name)
    return current


def tracked_files(root):
    raw = subprocess.check_output(["git", "-C", str(root), "ls-files", "-z"], timeout=30)
    return {name.decode("utf-8") for name in raw.split(b"\0") if name}


def profiles(root, tracked):
    result = {}
    for name in sorted(tracked):
        path = PurePosixPath(name)
        if len(path.parts) != 3 or path.parts[0] != "extensions" or path.name != "package.json":
            continue
        extension = path.parts[1]
        package = json.loads(contained(root, name).read_text())
        scripts = package.get("scripts", {})
        descriptor = f"extensions/{extension}/source-rebuild.json"
        has_source = "build:source" in scripts or "pack:source" in scripts
        if descriptor not in tracked:
            if has_source and extension not in LEGACY_EXAMPLES:
                raise ValueError("source migration lacks a rebuild profile: " + extension)
            continue
        if not has_source:
            raise ValueError("rebuild profile has no source build: " + extension)
        config = json.loads(contained(root, descriptor).read_text())
        fields = {"version", "bundle", "build", "output", "archive", "extracted_extension", "extracted_sdk"}
        if set(config) != fields or type(config["version"]) is not int or config["version"] != 1:
            raise ValueError("invalid source rebuild profile: " + extension)
        if config["build"] not in {"build:source", "pack:source"} or config["build"] not in scripts:
            raise ValueError("source build script is missing")
        reference = f"extensions/{extension}/{config['bundle']}"
        if reference not in tracked or not config["bundle"].startswith("source-reference/"):
            raise ValueError("reference bundle must be committed separately from build output")
        data = contained(root, reference).read_bytes()
        if not data or len(data) > MAX_BUNDLE:
            raise ValueError("invalid reference bundle size")
        # The bundle embeds the manifest: one changed without regenerating the bundle (a version bump) fails here,
        # locally and before the hosted rebuild's byte comparison.
        manifest = json.loads(contained(root, f"extensions/{extension}/persona-extension.json").read_text())
        version = manifest.get("version")
        if not isinstance(version, str) or f'version: "{version}"'.encode() not in data:
            raise ValueError(f"reference bundle does not embed the manifest version {version!r}: "
                             f"regenerate {reference} (npm ci --ignore-scripts, npm run {config['build']}, Node 22 or 24)")
        for field in ("output", "archive", "extracted_extension", "extracted_sdk"):
            contained(root, config[field], required=False)
        result[extension] = config
    return result


def compare(reference, rebuilt, label):
    actual = rebuilt.read_bytes()
    if actual != reference:
        raise ValueError(label + " differs byte-for-byte from the committed bundle")
    return {"bytes": len(actual), "sha256": hashlib.sha256(actual).hexdigest()}


def extract(archive, destination):
    """Extract only bounded regular authoring files into a newly created directory."""
    seen = set()
    total = 0
    with tarfile.open(archive, "r:gz") as handle:
        for number, member in enumerate(handle, 1):
            if number > 4096:
                raise ValueError("too many authoring archive members")
            name = member.name
            while name.startswith("./"):
                name = name[2:]
            name = name.rstrip("/")
            if not name or name == ".":
                if not member.isdir():
                    raise ValueError("invalid archive root")
                continue
            target = contained(destination, name, required=False)
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
                continue
            if not member.isfile() or name in seen:
                raise ValueError("nonregular or duplicate authoring archive member")
            seen.add(name)
            total += member.size
            if member.size < 0 or total > 64 << 20:
                raise ValueError("authoring archive exceeds its byte bound")
            target.parent.mkdir(parents=True, exist_ok=True)
            source = handle.extractfile(member)
            with source, target.open("xb") as output:
                shutil.copyfileobj(source, output)


def build(extension, sdk, script, workspace):
    # No cache restore and no HOME substitution. npm installs only locked public
    # authoring dependencies; the production guest never resolves these imports.
    for directory in (sdk, extension):
        subprocess.run(["npm", "--prefix", str(directory), "ci", "--ignore-scripts",
                        "--registry=https://registry.npmjs.org", "--no-audit", "--no-fund"],
                       cwd=workspace, check=True, timeout=120)
    subprocess.run(["npm", "--prefix", str(extension), "run", script],
                   cwd=workspace, check=True, timeout=180)


def rebuild(root, extension, config, tracked, parent):
    if os.environ.get("GITHUB_ACTIONS") != "true" or os.environ.get("RUNNER_ENVIRONMENT") != "github-hosted":
        raise ValueError("standalone rebuild is restricted to a fresh GitHub-hosted runner")
    reference = contained(root, f"extensions/{extension}/{config['bundle']}").read_bytes()
    work = Path(tempfile.mkdtemp(prefix=extension + "-", dir=parent))
    seed = work / "authoring"
    seed.mkdir()
    prefix = f"extensions/{extension}/"
    inputs = sorted(name for name in tracked if name == "sdk/VERSION" or name.startswith("sdk/ts/")
                    or (name.startswith(prefix) and not name.startswith(prefix + "source-reference/")
                        and name != prefix + "source-rebuild.json"))
    for name in inputs:
        source = contained(root, name)
        target = contained(seed, name, required=False)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)
    authoring = seed / "extensions" / extension
    build(authoring, seed / "sdk/ts", config["build"], work)
    first = compare(reference, contained(authoring, config["output"]), "isolated source rebuild")
    archive = contained(authoring, config["archive"])
    unpacked = work / "unpacked"
    unpacked.mkdir()
    extract(archive, unpacked)
    extracted_extension = unpacked / config["extracted_extension"]
    extracted_sdk = unpacked / config["extracted_sdk"]
    contained(extracted_extension, "package.json")
    contained(extracted_sdk, "package.json")
    contained(extracted_extension, "package-lock.json")
    contained(extracted_sdk, "package-lock.json")
    build(extracted_extension, extracted_sdk, config["build"], work)
    second = compare(reference, contained(extracted_extension, config["output"]), "extracted authoring rebuild")
    report = {"extension": extension, "verdict": "PASS", "runner": "github-hosted", "home_substituted": False,
              "dependency_cache_restored": False, "isolated_source": first, "extracted_authoring": second}
    (parent / (extension + ".json")).write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("plan", "rebuild"))
    parser.add_argument("--extension")
    parser.add_argument("--workspace", type=Path)
    args = parser.parse_args()
    try:
        tracked = tracked_files(ROOT)
        registered = profiles(ROOT, tracked)
        if args.command == "plan":
            matrix = {"extension": sorted(registered) or ["none"]}
            print("Registered source migrations: " + ", ".join(sorted(registered)))
            print("Legacy examples are not fresh-HOME proofs unless they register a profile.")
            output = os.environ.get("GITHUB_OUTPUT")
            if output:
                with open(output, "a") as stream:
                    stream.write("matrix=" + json.dumps(matrix) + "\n")
                    stream.write("has_sources=" + str(bool(registered)).lower() + "\n")
            print("source profile plan: PASS; no rebuild executed")
            return 0
        else:
            if args.extension not in registered or args.workspace is None:
                raise ValueError("registered extension and new workspace are required")
            args.workspace.mkdir()
            rebuild(ROOT, args.extension, registered[args.extension], tracked, args.workspace)
        print("source rebuild: PASS")
        return 0
    except subprocess.TimeoutExpired as error:
        print("source rebuild: TIMEOUT: " + str(error), file=sys.stderr)
        return 1
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError, tarfile.TarError) as error:
        print("source rebuild: FAIL: " + str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
