#!/usr/bin/env python3
"""Two-phase OCI publication: push packages, then anonymously verify a saved plan for signing.

The read-only audit can run before cutover. All publication commands require the final repository identity.
"""
import argparse
import copy
import gzip
import io
import os
import posixpath
import pathlib
import re
import subprocess
import sys
import tarfile
import tempfile

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import catalog
import index
import manifest as manifests
import oci

FINAL_REPOSITORY = catalog.FINAL_REPOSITORY
CLEANED_REPOSITORY_ID = catalog.CLEANED_REPOSITORY_ID
TRUSTED_BRANCHES = {"main"}


def gh(*args):
    result = subprocess.run(["gh", *map(str, args)], capture_output=True, timeout=180)
    if result.returncode:
        raise ValueError("GitHub read operation failed: " + args[0])
    return result.stdout


def identity(repository, repository_id, ref):
    # Name alone is insufficient: the former repository must never publish to the new package namespace.
    if repository != FINAL_REPOSITORY or str(repository_id) != str(CLEANED_REPOSITORY_ID):
        raise ValueError("publication blocked until cleaned repository cutover is complete")
    # A tag named main has the same short name: only the full branch ref is trusted.
    if not ref.startswith("refs/heads/") or ref[len("refs/heads/"):] not in TRUSTED_BRANCHES:
        raise ValueError("publication requires trusted main")


def cutover_gate(args):
    if not args.confirm_cutover:
        raise ValueError("explicit --confirm-cutover is required")
    # Check identity before credentials, downloads, builds or writes. API failures are not confirmation.
    identity(args.repository, args.repository_id, args.ref)
    remote = catalog.loads(gh("api", "repos/" + args.repository))
    identity(remote["full_name"], remote["id"], args.ref)
    if index.git("rev-parse", "HEAD").decode().strip() != index.full_sha(args.head_sha):
        raise ValueError("checkout differs from trusted Actions SHA")
    index.git("diff", "--exit-code", "HEAD", "--", "extensions", "persona-extensions.json")
    inventory = [{"manifest": manifests.packaged(k), "tags": manifests.tags(k)} for k in manifests.ids()]
    if inventory != index.tree_inventory(args.head_sha):
        raise ValueError("checkout inventory differs from trusted source commit")


def archive_matches(data, package):
    if oci.digest(data) != "sha256:" + package["sha256"] or len(data) != package["size"]:
        raise ValueError("cleaned Release archive checksum or size mismatch")


def safe_path(path):
    return manifests.inside(path) and not any(ord(c) < 32 or ord(c) == 127 for c in path)


# Match source.go's compressed/unpacked regular-file and manifest.go's manifest limits.
PACKAGE_MOST = catalog.PACKAGE_MOST
PACKAGE_FILES = 20000
MANIFEST_MOST = 256 * 1024
MAX_MEMBERS = 40000  # Also bound directories and metadata/header floods.
MAX_TAR_BYTES = PACKAGE_MOST + MAX_MEMBERS * 1024 + 10240


class ScanBudget:
    def __init__(self, stream):
        self.stream, self.left = stream, MAX_TAR_BYTES

    def read(self, size):
        data = self.stream.read(min(size, self.left + 1))
        self.left -= len(data)
        if self.left < 0:
            raise ValueError("archive exceeds decompressed scan budget")
        return data


class BoundedTarInfo(tarfile.TarInfo):
    @classmethod
    def frombuf(cls, *args):
        member = super().frombuf(*args)
        # tarfile otherwise allocates PAX/GNU metadata before yielding a member.
        if member.type in (tarfile.XHDTYPE, tarfile.XGLTYPE, tarfile.GNUTYPE_LONGNAME,
                           tarfile.GNUTYPE_LONGLINK) and member.size > MANIFEST_MOST:
            raise ValueError("archive metadata exceeds scan budget")
        return member


def scan_archive(data, extension_id, capture=()):
    if len(data) > PACKAGE_MOST:
        raise ValueError("archive exceeds compressed byte limit")
    files, saved, names = {}, {}, set()
    count = total = members = 0
    try:
        with gzip.GzipFile(fileobj=io.BytesIO(data)) as compressed:
            budget = ScanBudget(compressed)
            with tarfile.open(fileobj=budget, mode="r|", tarinfo=BoundedTarInfo) as archive:
                for member in archive:
                    members += 1
                    if members > MAX_MEMBERS:
                        raise ValueError("archive has too many members")
                    path = member.name.removeprefix("./").removesuffix("/")
                    if not safe_path(path) or not (path == extension_id or path.startswith(extension_id + "/")):
                        raise ValueError("archive has an unsafe path or wrong package root")
                    if path in names:
                        raise ValueError("archive has duplicate paths")
                    names.add(path)
                    if member.isdir():
                        if member.size != 0:
                            raise ValueError("archive directory is not empty")
                    elif member.type in (tarfile.REGTYPE, tarfile.AREGTYPE) and member.sparse is None:
                        count += 1
                        total += member.size
                        if count > PACKAGE_FILES:
                            raise ValueError("archive has more than 20000 files")
                        if member.size < 0 or total > PACKAGE_MOST:
                            raise ValueError("archive exceeds unpacked byte limit")
                        rel = path[len(extension_id) + 1:]
                        if not rel:
                            raise ValueError("archive root is not a directory")
                        files[rel] = (member.mode, member.size)
                        if rel in ("persona-extension.json", "persona-plugin.json") and member.size > MANIFEST_MOST:
                            raise ValueError("archive manifest exceeds 256 KiB")
                        # Keep bounded JSON references only, never binaries or arbitrary file contents.
                        if (rel.endswith(".json") or rel in capture) and member.size <= MANIFEST_MOST:
                            saved[rel] = archive.extractfile(member).read()
                    else:
                        raise ValueError("archive member is not an ordinary file or directory")
                    # Streaming tarfile still caches TarInfo objects on older Python versions.
                    archive.members.clear()
            # Drain padding/trailing gzip bytes under the same budget and verify gzip integrity.
            while budget.read(64 * 1024):
                pass
    except (tarfile.TarError, EOFError, OSError, RecursionError) as error:
        raise ValueError("invalid or truncated archive") from error
    if "persona-extension.json" not in saved:
        raise ValueError("archive has no regular extension manifest")
    # No file may be used as a parent directory, even if header order masks it.
    for path in names:
        parent = posixpath.dirname(path)
        while parent and parent != extension_id:
            if parent[len(extension_id) + 1:] in files:
                raise ValueError("archive file is used as a directory")
            parent = posixpath.dirname(parent)
    return catalog.loads(saved["persona-extension.json"]), files, saved


def archive_manifest(data, extension_id):
    return scan_archive(data, extension_id)[0]


def check_archive(data, expected, strict=False, platform="any"):
    actual, files, saved = scan_archive(data, expected["id"])
    gates = {a["gate"] for m in (actual, expected) for a in m.get("contributes", {}).get("assistant", []) if a.get("gate")}
    if any(path in files and path not in saved for path in gates):
        # Gates contain JSON but have no required suffix. A bounded second pass also handles gates
        # appearing before the manifest, without retaining every small binary/text file in memory.
        _, _, saved = scan_archive(data, expected["id"], capture=gates)
    if actual.get("id") != expected["id"] or actual.get("version") != expected["version"]:
        raise ValueError("archive extension identity differs from catalog")
    if strict and actual != expected:
        raise ValueError("new-version archive manifest differs from source")
    commands = expected.get("runtime", {}).get("command", {})
    if actual.get("runtime", {}).get("command", {}) != commands:
        raise ValueError("archive runtime platform mapping differs from catalog")
    oci.version_tag(expected["version"], platform)
    if commands and platform != "any" and platform not in commands:
        raise ValueError("archive runtime does not declare package platform")

    def required(path, executable=False):
        if not safe_path(path) or path not in files:
            raise ValueError("archive is missing a regular " + ("platform binary" if executable else "referenced file"))
        if executable and files[path][0] & 0o111 == 0:
            raise ValueError("archive platform binary is not executable")

    selected = commands.values() if platform == "any" else ([commands[platform]] if commands else [])
    for path in selected:
        required(path, True)
    # Node programs are shipped to other computers: hub checks all declared node commands.
    for path in actual.get("node", {}).get("command", {}).values():
        required(path, True)
    for m in (actual, expected):
        for _, path, kind in manifests.referenced(m):
            if not safe_path(path):
                raise ValueError("archive reference has an unsafe path")
            required(path + "/SKILL.md" if kind == "skill" else path)
        for assistant in m.get("contributes", {}).get("assistant", []):
            if not assistant.get("gate"):
                continue
            gate_path = assistant["gate"]
            if gate_path not in saved:
                raise ValueError("archive gate is missing or exceeds JSON limit")
            gate = catalog.loads(saved[gate_path])
            script = gate.get("script")
            if not safe_path(script):
                raise ValueError("archive gate script has an unsafe path")
            matching = [skill for skill in assistant.get("skills", [])
                        if script.startswith(posixpath.basename(skill) + "/")]
            if len(matching) != 1:
                raise ValueError("archive gate script is not in a unique declared skill")
            required(posixpath.dirname(matching[0]) + "/" + script)
    return sorted(k for k in actual.keys() | expected.keys() if actual.get(k) != expected.get(k))


def cleaned_archive(repository, entry, package, destination):
    m = entry["manifest"]
    platform = catalog.package_platform(package)
    expected_name = oci.filename(m["id"], m["version"], platform)
    expected_tag = m["id"] + "-v" + m["version"]
    if package["url"] != f"release:{expected_tag}/{expected_name}":
        raise ValueError("bootstrap requires the frozen cleaned Release reference")
    remote = catalog.loads(gh("api", f"repos/{repository}/releases/tags/{expected_tag}"))
    assets = [a for a in remote["assets"] if a["name"] == expected_name]
    if len(assets) != 1 or assets[0].get("digest") != "sha256:" + package["sha256"] or assets[0]["size"] != package["size"]:
        raise ValueError("cleaned Release asset metadata differs from frozen signed catalog")
    # One fresh empty directory per downloaded archive; gh only downloads, nothing is executed here.
    directory = pathlib.Path(tempfile.mkdtemp(prefix=m["id"] + "-", dir=destination))
    gh("release", "download", expected_tag, "--repo", repository, "--pattern", expected_name, "--dir", directory)
    data = (directory / expected_name).read_bytes()
    archive_matches(data, package)
    # Cleaned historical archives can have scrubbed descriptive fields. The approved frozen signature
    # pins their exact bytes; never rebuild or rewrite them to make descriptive metadata match.
    differences = check_archive(data, m, platform=platform)
    return data, {"release_id": remote["id"], "tag": remote["tag_name"], "asset_id": assets[0]["id"],
                  "filename": expected_name, "sha256": package["sha256"], "size": len(data),
                  "runtime_platforms": sorted(m.get("runtime", {}).get("command", {})),
                  "manifest_field_differences": differences}


def audit(args):
    remote = catalog.loads(gh("api", "repos/" + args.repository))
    if remote["id"] != CLEANED_REPOSITORY_ID:
        raise ValueError("audit source is not the approved cleaned repository")
    _, body = catalog.verified(args.catalog)
    destination = pathlib.Path(args.output)
    destination.mkdir(parents=True, exist_ok=False)
    release_lines = gh("api", "--paginate", f"repos/{args.repository}/releases?per_page=100", "--jq",
                       '.[] | {id, tag_name, assets: [.assets[] | {id, name, size, digest}]}')
    releases = [catalog.loads(line) for line in release_lines.splitlines()]
    results = []
    for entry in body["extensions"]:
        for package in entry["packages"]:
            _, receipt = cleaned_archive(remote["full_name"], entry, package, destination)
            receipt["id"] = entry["manifest"]["id"]
            receipt["platform"] = catalog.package_platform(package)
            results.append(receipt)
            print(receipt["id"] + ": cleaned Release bytes verified")
    # Preserve all historic identities for an independent before/after comparison, not just counts.
    history = [{"id": r["id"], "tag": r["tag_name"], "assets": [
        {k: a.get(k) for k in ("id", "name", "size", "digest")} for a in r["assets"]]} for r in releases]
    result = {"repository_id": remote["id"], "releases": len(releases),
              "assets": sum(len(r["assets"]) for r in releases), "current_packages": results, "history": history}
    (destination / "audit.json").write_bytes(catalog.encode(result))
    print(f"audit: ok ({len(results)} verified packages; {result['releases']} Releases; {result['assets']} assets)")


def desired_entries(body, mode):
    if mode == "bootstrap":
        # Do not derive already published versions from a new build or from the former repository.
        if len(body["extensions"]) != 14 or any(len(e["packages"]) != 1 or catalog.package_platform(e["packages"][0]) != "any"
                                                for e in body["extensions"]):
            raise ValueError("bootstrap expects the frozen 14 any-platform packages")
        return copy.deepcopy(body["extensions"])
    prior = {e["manifest"]["id"]: e for e in body["extensions"]}
    result = []
    for extension_id in manifests.ids():
        m = manifests.packaged(extension_id)
        old = prior.get(extension_id)
        if old and old["manifest"]["version"] == m["version"]:
            if old["manifest"] != m:
                raise ValueError("same-version manifest changed; bump version before publishing")
            tags = manifests.tags(extension_id)
            if old["tags"] != tags:
                result.append(dict(copy.deepcopy(old), tags=tags))
            continue
        result.append({"manifest": m, "tags": manifests.tags(extension_id), "packages": []})
    if result and any(not p["url"].startswith("oci:") for e in body["extensions"] for p in e["packages"]):
        raise ValueError("complete frozen Release bootstrap before publishing new versions")
    return result


def reuse_version(registry, entry):
    m = entry["manifest"]
    try:
        ref, data = registry.read_manifest(oci.version_tag(m["version"]))
    except oci.Missing:
        return None
    body = catalog.loads(data)
    layers = body.get("layers", [])
    if len(layers) != 1:
        raise ValueError("existing version must have exactly one archive layer")
    layer = layers[0]
    expected = oci.manifest(m["id"], m["version"], layer["digest"], layer["size"])
    if body != expected:
        raise ValueError("existing version tag metadata conflicts")
    registry.read_blob(expected["config"]["digest"], expected["config"]["size"])
    archive = registry.read_blob(layer["digest"], layer["size"])
    check_archive(archive, m, strict=True)
    return {"url": oci.package_url(m["id"], ref), "sha256": layer["digest"].removeprefix("sha256:"), "size": layer["size"]}


def push(args):
    cutover_gate(args)
    token, username = os.environ.get("GHCR_TOKEN", ""), os.environ.get("GITHUB_ACTOR", "")
    if not token or not username:
        raise ValueError("GHCR_TOKEN and GITHUB_ACTOR are required")
    original, body = catalog.verified(args.catalog)
    entries = desired_entries(body, args.mode)
    output = pathlib.Path(args.output)
    output.mkdir(parents=True, exist_ok=False)
    removed = sorted({e["manifest"]["id"] for e in body["extensions"]} - set(manifests.ids())) if args.mode == "versions" else []
    plan = {"repository_id": CLEANED_REPOSITORY_ID, "head_sha": args.head_sha, "mode": args.mode,
            "base_payload_sha256": oci.digest(original), "entries": [], "release_receipts": [],
            "removed_ids": removed, "catalog_update_required": bool(entries or removed)}
    for entry in entries:
        m = entry["manifest"]
        registry = oci.Registry(oci.repository(m["id"]), token, username)
        if args.mode == "bootstrap":
            packages = []
            for package in entry["packages"]:
                archive, receipt = cleaned_archive(args.repository, entry, package, output)
                ref = registry.push_once(m["id"], m["version"], archive, catalog.package_platform(package))
                packages.append(dict(package, url=oci.package_url(m["id"], ref)))
                plan["release_receipts"].append(receipt)
            entry["packages"] = packages
        elif entry["packages"]:
            # Catalog-only tags updates reuse all immutable package descriptors, including platform.
            pass
        else:
            package = reuse_version(registry, entry)
            if package is None:
                subprocess.run([str(catalog.ROOT / "scripts/package.sh"), m["id"]], check=True, timeout=900,
                               env={k: v for k, v in os.environ.items() if k not in ("GHCR_TOKEN", "GH_TOKEN", "EXTENSION_MARKET_SIGNING_KEY")})
                archive = (catalog.ROOT / "dist" / oci.filename(m["id"], m["version"])).read_bytes()
                check_archive(archive, m, strict=True)
                ref = registry.push_once(m["id"], m["version"], archive)
                package = {"url": oci.package_url(m["id"], ref), "sha256": oci.digest(archive)[7:], "size": len(archive)}
            entry["packages"] = [package]
        plan["entries"].append(entry)
        # Save after every version; a failed run's partial plan is evidence, never a signable catalog.
        (output / "plan.json").write_bytes(catalog.encode(plan))
    (output / "plan.json").write_bytes(catalog.encode(plan))
    settings = [{"package": oci.repository(e["manifest"]["id"]),
                 "settings_url": "https://github.com/orgs/nwverse/packages/container/" +
                                 "persona-extensions%2F" + e["manifest"]["id"] + "/settings"}
                for e in entries]
    (output / "package-settings.json").write_bytes(catalog.encode(settings))
    print("packages: ok; visibility is a separate authorized human operation")


def validate_plan(plan, original, body, head_sha):
    if plan.get("repository_id") != CLEANED_REPOSITORY_ID or plan.get("head_sha") != head_sha:
        raise ValueError("plan source identity or checkout SHA mismatch")
    if plan.get("base_payload_sha256") != oci.digest(original):
        raise ValueError("catalog moved since packages were published; generate a new plan")
    if plan.get("mode") not in ("bootstrap", "versions"):
        raise ValueError("unknown publication plan mode")
    expected = desired_entries(body, plan["mode"])
    removed = sorted({e["manifest"]["id"] for e in body["extensions"]} - set(manifests.ids())) if plan["mode"] == "versions" else []
    if plan.get("removed_ids") != removed or plan.get("catalog_update_required") is not bool(expected or removed):
        raise ValueError("plan deletion inventory or catalog-update signal differs from trusted checkout")
    entries = plan["entries"]
    if len(entries) != len(expected):
        raise ValueError("plan is incomplete")
    for actual, wanted in zip(entries, expected):
        if set(actual) != {"manifest", "tags", "packages"} or actual["manifest"] != wanted["manifest"] or actual["tags"] != wanted["tags"]:
            raise ValueError("plan manifest or tags differ from trusted checkout")
        if plan["mode"] == "bootstrap":
            if len(actual["packages"]) != len(wanted["packages"]):
                raise ValueError("bootstrap package count changed")
            for a, w in zip(actual["packages"], wanted["packages"]):
                if {k: v for k, v in a.items() if k != "url"} != {k: v for k, v in w.items() if k != "url"}:
                    raise ValueError("bootstrap archive descriptor differs from frozen signature")
        elif wanted["packages"]:
            if actual["packages"] != wanted["packages"]:
                raise ValueError("tags-only update must retain immutable package descriptors")
        elif len(actual["packages"]) != 1 or set(actual["packages"][0]) != {"url", "sha256", "size"}:
            raise ValueError("new versions must have one any-platform archive")
    catalog.validate_payload(dict(body, extensions=entries))
    return entries


def generate_payload(plan, catalog_path, head_sha, registry_factory=oci.Registry):
    original, body = catalog.verified(catalog_path)
    entries = validate_plan(plan, original, body, head_sha)
    # Construct clients with NO credentials. Do not reuse the publisher's authenticated clients/tokens.
    for entry in entries:
        m = entry["manifest"]
        registry = registry_factory(oci.repository(m["id"]))
        for package in entry["packages"]:
            strict = plan["mode"] == "versions" and not next(
                (old["manifest"]["version"] == m["version"] for old in body["extensions"] if old["manifest"]["id"] == m["id"]), False)
            oci.verify_package(registry, m["id"], m["version"], package,
                               archive_validator=lambda data: check_archive(data, m, strict, catalog.package_platform(package)))
            print(m["id"] + ": anonymous manifest/config/archive verified")
    inventory = [{"manifest": manifests.packaged(k), "tags": manifests.tags(k)} for k in manifests.ids()]
    present = {e["manifest"]["id"] for e in inventory}
    merged = {e["manifest"]["id"]: e for e in body["extensions"]
              if plan["mode"] == "bootstrap" or e["manifest"]["id"] in present}
    merged.update({e["manifest"]["id"]: e for e in entries})
    body["extensions"] = [merged[k] for k in sorted(merged)]
    body["generation"] = {"repository": FINAL_REPOSITORY, "repository_id": CLEANED_REPOSITORY_ID,
                          "base_catalog_hash": oci.digest(original), "source_commit": head_sha,
                          "source_inventory_hash": catalog.inventory_hash(inventory), "mode": plan["mode"]}
    # Verify every retained OCI reference too, not just the newly uploaded versions.
    for entry in body["extensions"]:
        if entry in entries:
            continue
        m = entry["manifest"]
        registry = registry_factory(oci.repository(m["id"]))
        for package in entry["packages"]:
            if not package["url"].startswith("oci:"):
                raise ValueError("bootstrap must finish before publishing new-version catalogs")
            oci.verify_package(registry, m["id"], m["version"], package,
                               archive_validator=lambda data: check_archive(data, m, platform=catalog.package_platform(package)))
    return catalog.encode(catalog.validate_payload(body))


def catalog_phase(args):
    cutover_gate(args)
    plan = catalog.loads(pathlib.Path(args.plan).read_bytes())
    if plan.get("catalog_update_required") is not True:
        # Signing an unchanged catalog would only rotate its generation and open an empty update.
        raise ValueError("plan has no catalog change; nothing to sign")
    payload = generate_payload(plan, args.catalog, args.head_sha)
    output = pathlib.Path(args.output)
    output.mkdir(parents=True, exist_ok=False)
    (output / "payload.json").write_bytes(payload)
    print("anonymous gate: ok; payload ready for independent secret signing step")


def validate_run(args):
    cutover_gate(args)
    if not re.fullmatch(r"[0-9]+", args.run_id):
        raise ValueError("invalid package run id")
    run = catalog.loads(gh("api", f"repos/{args.repository}/actions/runs/{args.run_id}"))
    if (run.get("repository", {}).get("id") != CLEANED_REPOSITORY_ID or run.get("head_sha") != args.head_sha or
            run.get("head_branch") not in TRUSTED_BRANCHES or run.get("conclusion") != "success" or
            run.get("path") != ".github/workflows/ci.yml" or run.get("event") not in ("push", "workflow_dispatch")):
        raise ValueError("package artifact must come from a successful trusted same-SHA publication run")
    print("package run: approved provenance")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["audit", "push", "catalog", "validate-run", "gate"])
    parser.add_argument("--repository", default=os.environ.get("GITHUB_REPOSITORY", "nwverse/persona-extensions-next"))
    parser.add_argument("--repository-id", default=os.environ.get("GITHUB_REPOSITORY_ID", ""))
    parser.add_argument("--ref", default=os.environ.get("GITHUB_REF", ""))
    parser.add_argument("--head-sha", default=os.environ.get("GITHUB_SHA", ""))
    parser.add_argument("--confirm-cutover", action="store_true")
    parser.add_argument("--catalog", default=catalog.CATALOG)
    parser.add_argument("--mode", choices=["bootstrap", "versions"], default="bootstrap")
    parser.add_argument("--output")
    parser.add_argument("--plan")
    parser.add_argument("--run-id")
    args = parser.parse_args()
    if args.command in ("audit", "push", "catalog") and not args.output:
        parser.error("--output is required")
    if args.command != "audit" and not re.fullmatch(r"[0-9a-f]{40}", args.head_sha):
        parser.error("publication requires a full trusted checkout SHA")
    if args.command == "audit":
        audit(args)
    elif args.command == "push":
        push(args)
    elif args.command == "catalog":
        if not args.plan:
            parser.error("--plan is required")
        catalog_phase(args)
    elif args.command == "validate-run":
        validate_run(args)
    else:
        cutover_gate(args)
        print("cutover identity gate: ok")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, TypeError, KeyError, oci.Missing, subprocess.SubprocessError) as error:
        # Subprocess diagnostics/registry responses must not leak credentials.
        detail = str(error) if not isinstance(error, subprocess.SubprocessError) else "external operation failed"
        sys.exit("publisher: failed: " + detail)
