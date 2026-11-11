#!/usr/bin/env python3
"""Signed catalog primitives. Signatures cover the UTF-8 payload, never reserialized JSON."""
import base64
import hashlib
import json
import pathlib
import re
import subprocess
import tempfile

ROOT = pathlib.Path(__file__).resolve().parent.parent
CATALOG = ROOT / "persona-extensions.json"
# Approved cleaned repository key; embedding a signature does not rotate this pin.
APPROVED_PUBLIC_KEY = "ed25519:+J+9FsfWHflg/wLLB3FYK32UGGNBUDPwSb64LtprtZ4="
APPROVED_KEY_ID = "3cce0f471911a1cf"
SPKI_PREFIX = bytes.fromhex("302a300506032b6570032100")
FINAL_REPOSITORY = "nwverse/persona-extensions"
CLEANED_REPOSITORY_ID = 1449704212
VERSION_MOST = 96
PACKAGE_MOST = 200 * 1024 * 1024
VERSION = re.compile(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-(?:[A-Za-z0-9-]+)(?:\.[A-Za-z0-9-]+)*)?")


def valid_version(value):
    if not isinstance(value, str) or len(value) > VERSION_MOST or not VERSION.fullmatch(value):
        return False
    return "-" not in value or all(not p.isdigit() or p == "0" or not p.startswith("0")
                                   for p in value.split("-", 1)[1].split("."))


def payload_hash(payload):
    return "sha256:" + hashlib.sha256(payload).hexdigest()


def valid_extension_id(value):
    return isinstance(value, str) and len(value) <= 32 and re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", value) is not None


def source_entry(source, l10n=None):
    """Project author metadata into an inventory entry without changing runtime manifests.

    l10n is the package's translations by language (scripts/manifest.py l10n): a hub shows them in the market before
    downloading anything. Empty means none."""
    manifest = dict(source)
    tags = manifest.pop("tags", [])
    has_predecessor = "replaces" in manifest
    predecessor = manifest.pop("replaces", None)
    entry = {"manifest": manifest, "tags": tags}
    if l10n:
        entry["l10n"] = l10n
    if has_predecessor:
        if not valid_extension_id(predecessor) or predecessor == manifest.get("id"):
            raise ValueError("invalid extension replaces declaration")
        entry["replaces"] = predecessor
    return entry


def entry_inventory(entry):
    """The exact source-owned fields covered by generation provenance."""
    result = {"manifest": entry["manifest"], "tags": entry.get("tags", [])}
    if "l10n" in entry:
        result["l10n"] = entry["l10n"]
    if "replaces" in entry:
        result["replaces"] = entry["replaces"]
    return result


def validate_entry_identities(entries):
    ids, predecessors = set(), set()
    for entry in entries:
        extension_id = entry["manifest"].get("id")
        if not valid_extension_id(extension_id) or extension_id in ids:
            raise ValueError("unsafe or duplicate extension id")
        ids.add(extension_id)
        if "replaces" in entry:
            predecessor = entry["replaces"]
            if not valid_extension_id(predecessor) or predecessor == extension_id:
                raise ValueError("invalid extension replaces declaration")
            if predecessor in predecessors:
                raise ValueError("ambiguous extension replaces declaration")
            predecessors.add(predecessor)
    if predecessors & ids:
        raise ValueError("replacement predecessor is still a current extension (cycle or duplicate identity)")
    return entries


def inventory_hash(inventory):
    # Explicit canonical encoding here is for inventory provenance, never for signature verification.
    raw = json.dumps(inventory, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False).encode("utf-8")
    return payload_hash(raw)


def validate_generation(generation):
    fields = {"repository", "repository_id", "base_catalog_hash", "source_commit", "source_inventory_hash", "mode"}
    if not isinstance(generation, dict) or set(generation) != fields:
        raise ValueError("new catalog requires signed generation provenance")
    if generation["repository"] != FINAL_REPOSITORY or generation["repository_id"] != CLEANED_REPOSITORY_ID:
        raise ValueError("generation repository identity mismatch")
    if not isinstance(generation["source_commit"], str) or not re.fullmatch(r"[0-9a-f]{40}", generation["source_commit"]):
        raise ValueError("generation source_commit must be a full SHA")
    for name in ("base_catalog_hash", "source_inventory_hash"):
        if not isinstance(generation[name], str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", generation[name]):
            raise ValueError("invalid generation hash")
    if generation["mode"] not in ("bootstrap", "versions"):
        raise ValueError("invalid generation mode")
    return generation


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON key: " + key)
        result[key] = value
    return result


def invalid_constant(value):
    raise ValueError("non-JSON numeric constant: " + value)


def loads(data):
    return json.loads(data, object_pairs_hook=unique_object, parse_constant=invalid_constant)


def encode(value):
    return (json.dumps(value, ensure_ascii=False, indent=2, allow_nan=False) + "\n").encode("utf-8")


def public_raw(line):
    if not isinstance(line, str) or not line.startswith("ed25519:"):
        raise ValueError("not an Ed25519 public key")
    raw = base64.b64decode(line[8:], validate=True)
    if len(raw) != 32:
        raise ValueError("public key must be 32 bytes")
    return raw


def key_id(raw):
    return hashlib.sha256(raw).hexdigest()[:16]


def openssl(*args):
    result = subprocess.run(["openssl", *map(str, args)], capture_output=True, timeout=30)
    if result.returncode:
        # Never include OpenSSL diagnostics or key paths in logs.
        raise ValueError("OpenSSL operation failed: " + args[0])
    return result.stdout


def verify_signature(payload, signature, public_key=None):
    raw = public_raw(public_key or APPROVED_PUBLIC_KEY)
    if not isinstance(signature, dict) or signature.get("alg") != "ed25519":
        raise ValueError("signature algorithm must be ed25519")
    if signature.get("key_id") != key_id(raw):
        raise ValueError("signature key does not match approved key")
    sig = base64.b64decode(signature.get("sig", ""), validate=True)
    if len(sig) != 64:
        raise ValueError("signature must be 64 bytes")
    with tempfile.TemporaryDirectory() as directory:
        d = pathlib.Path(directory)
        (d / "public.pem").write_bytes(b"-----BEGIN PUBLIC KEY-----\n" +
                                      base64.encodebytes(SPKI_PREFIX + raw) + b"-----END PUBLIC KEY-----\n")
        (d / "payload").write_bytes(payload)
        (d / "signature").write_bytes(sig)
        openssl("pkeyutl", "-verify", "-pubin", "-inkey", d / "public.pem", "-rawin",
                "-in", d / "payload", "-sigfile", d / "signature")


def validate_payload(payload):
    if not isinstance(payload, dict) or payload.get("market_version") != 1:
        raise ValueError("unsupported catalog payload")
    entries = payload.get("extensions")
    if not isinstance(entries, list):
        raise ValueError("extensions must be an array")
    if "generation" in payload:
        validate_generation(payload["generation"])
    validate_entry_identities(entries)
    for entry in entries:
        m = entry["manifest"]
        if "tags" in m:
            raise ValueError("packaged manifests must not contain tags")
        if "replaces" in m:
            raise ValueError("packaged manifests must not contain replaces")
        if not valid_version(m["version"]):
            raise ValueError("unsafe extension version")
        packages = entry["packages"]
        if not isinstance(packages, list) or not packages:
            raise ValueError("entry must have packages")
        platforms = set()
        for package in packages:
            if not re.fullmatch(r"[0-9a-f]{64}", package["sha256"]):
                raise ValueError("archive SHA256 must be lowercase hex")
            if type(package["size"]) is not int or not 0 < package["size"] <= PACKAGE_MOST:
                raise ValueError("archive size must be positive and at most 200 MiB")
            platform = package_platform(package)
            if platform in platforms:
                raise ValueError("unsafe or duplicate package platform")
            platforms.add(platform)
            if not isinstance(package["url"], str) or not package["url"]:
                raise ValueError("package URL is missing")
    return payload


def package_platform(package):
    """The package's platform as the hub reads it: `platforms` lists GOOS-GOARCH values; absent means any.

    Publication makes one package per platform, so a list holds exactly one value. The singular `platform`
    is not a hub field: a hub would ignore it and install the package everywhere, so it is refused."""
    if "platform" in package:
        raise ValueError("unsafe or duplicate package platform: use the hub's platforms list")
    if "platforms" not in package:
        return "any"
    platforms = package["platforms"]
    if (not isinstance(platforms, list) or len(platforms) != 1 or not isinstance(platforms[0], str) or
            not re.fullmatch(r"(?:linux|darwin|windows)-(?:amd64|arm64)", platforms[0])):
        raise ValueError("unsafe or duplicate package platform")
    return platforms[0]


def envelope(payload, signatures):
    # Strict UTF-8 round trip preserves all signed bytes, including whitespace and trailing newline.
    text = payload.decode("utf-8")
    validate_payload(loads(text))
    return {"payload": text, "signatures": signatures}


def verified(path=CATALOG, public_key=None):
    return verified_data(pathlib.Path(path).read_bytes(), public_key)


def verified_data(data, public_key=None):
    wrapper = loads(data)
    if not isinstance(wrapper, dict) or set(wrapper) != {"payload", "signatures"}:
        raise ValueError("catalog must be a single signed envelope")
    if not isinstance(wrapper["payload"], str):
        raise ValueError("payload must be a raw UTF-8 JSON string")
    signatures = wrapper["signatures"]
    if not isinstance(signatures, list) or len(signatures) != 1:
        raise ValueError("exactly one approved signature is required")
    payload = wrapper["payload"].encode("utf-8")
    verify_signature(payload, signatures[0], public_key)
    result = validate_payload(loads(payload))
    if result.get("signing_key") != (public_key or APPROVED_PUBLIC_KEY):
        raise ValueError("payload signing_key differs from approved public key")
    return payload, result
