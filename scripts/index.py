#!/usr/bin/env python3
"""Verify signatures and, on PRs, signed continuity against GitHub's actual base commit."""
import argparse
import os
import pathlib
import re
import subprocess
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import catalog

ROOT = str(catalog.ROOT)
NEW = str(catalog.CATALOG)


def git(*args):
    result = subprocess.run(["git", "-C", ROOT, *args], capture_output=True, timeout=30)
    if result.returncode:
        raise ValueError("cannot read trusted git provenance")
    return result.stdout


def full_sha(value):
    if not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{40}", value):
        raise ValueError("trusted base must be a full commit SHA")
    return value


def base_payload(data, signature=None):
    body = catalog.loads(data)
    if "payload" in body:
        return catalog.verified_data(data)[0], body["signatures"]
    # Only the initial raw-to-envelope conversion may use a detached signature from the real base tree.
    if signature is None:
        raise ValueError("raw base catalog requires its original detached signature")
    catalog.verify_signature(data, signature)
    catalog.validate_payload(body)
    if body.get("signing_key") != catalog.APPROVED_PUBLIC_KEY:
        raise ValueError("base signing key differs from approved key")
    return data, [signature]


def tree_catalog(ref):
    data = git("show", full_sha(ref) + ":persona-extensions.json")
    signature = None
    if "payload" not in catalog.loads(data):
        signature = catalog.loads(git("show", ref + ":persona-extensions.json.sig"))
    return base_payload(data, signature)


def tree_inventory(ref):
    sha = full_sha(ref)
    paths = git("ls-tree", "-r", "--name-only", sha, "--", "extensions").decode().splitlines()
    manifests, translations = {}, {}
    for path in sorted(paths):
        match = re.fullmatch(r"extensions/([a-z0-9]+(?:-[a-z0-9]+)*)/persona-extension\.json", path)
        if match:
            manifests[match[1]] = path
            continue
        match = re.fullmatch(r"extensions/([a-z0-9]+(?:-[a-z0-9]+)*)/l10n/([^/]+)\.json", path)
        if match:
            translations.setdefault(match[1], {})[match[2]] = path
    inventory = []
    for extension_id in sorted(manifests):
        manifest = catalog.loads(git("show", sha + ":" + manifests[extension_id]))
        if manifest.get("id") != extension_id:
            raise ValueError("source inventory directory/id mismatch")
        l10n = {}
        for language in sorted(translations.get(extension_id, {})):
            raw = git("show", sha + ":" + translations[extension_id][language])
            if len(raw) > 64 << 10:
                raise ValueError("extensions/%s/l10n/%s.json: larger than 64 KiB" % (extension_id, language))
            value = catalog.loads(raw)
            if not isinstance(value, dict):
                raise ValueError("extensions/%s/l10n/%s.json: not a JSON object" % (extension_id, language))
            l10n[language] = value
        inventory.append(catalog.source_entry(manifest, l10n=l10n))
    catalog.validate_entry_identities(inventory)
    return sorted(inventory, key=lambda e: e["manifest"]["id"])


def check_source(generation, base_ref, body):
    full_sha(base_ref)
    source = generation["source_commit"]
    # Commit and repository identity are not taken from a PR/artifact sidecar. The signed source must
    # be in the real base history and carry exactly its current inventory and predecessor catalog.
    git("merge-base", "--is-ancestor", source, base_ref)
    source_inventory, base_inventory = tree_inventory(source), tree_inventory(base_ref)
    if catalog.inventory_hash(source_inventory) != generation["source_inventory_hash"] or source_inventory != base_inventory:
        raise ValueError("signed source inventory is stale or does not match trusted PR base")
    if catalog.payload_hash(tree_catalog(source)[0]) != generation["base_catalog_hash"]:
        raise ValueError("signed source catalog does not match generation base")
    if generation["mode"] == "versions":
        current = sorted((catalog.entry_inventory(e) for e in body["extensions"]), key=lambda e: e["manifest"]["id"])
        if current != base_inventory:
            raise ValueError("generated catalog does not match complete source inventory")
        for entry in body["extensions"]:
            if "icon" in entry and entry.get("icon_url") != catalog.icon_url(entry["manifest"]["id"], entry["icon"], source):
                raise ValueError("generated icon_url differs from the signed source commit")


def check(path=None, *, base_catalog=None, base_signature=None, repository_id=None, base_ref=None):
    try:
        payload, body = catalog.verified(path or NEW)
        if base_catalog is None and base_ref is None:
            return True  # Offline signature check is not a PR continuity verdict.
        if str(repository_id) != str(catalog.CLEANED_REPOSITORY_ID):
            raise ValueError("PR repository is not the cleaned repository")
        if base_catalog is not None:
            prior, signatures = base_payload(pathlib.Path(base_catalog).read_bytes(), base_signature)
        else:
            prior, signatures = tree_catalog(base_ref)
        wrapper = catalog.loads(pathlib.Path(path or NEW).read_bytes())
        if payload == prior and wrapper["signatures"] == signatures:
            return True  # Unchanged catalog or byte-identical format conversion, never an older payload.
        generation = catalog.validate_generation(body.get("generation"))
        if generation["base_catalog_hash"] != catalog.payload_hash(prior):
            raise ValueError("signed catalog is stale: predecessor differs from actual PR base catalog")
        check_source(generation, base_ref, body)
        return True
    except (ValueError, OSError, TypeError, KeyError, subprocess.SubprocessError) as error:
        print("catalog: " + str(error), file=sys.stderr)
        return False


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["check"])
    parser.add_argument("--catalog", default=NEW)
    parser.add_argument("--base-ref", default=os.environ.get("CATALOG_PR_BASE_SHA") or None)
    parser.add_argument("--repository-id", default=os.environ.get("GITHUB_REPOSITORY_ID"))
    args = parser.parse_args()
    if os.environ.get("GITHUB_EVENT_NAME") == "pull_request" and not args.base_ref:
        parser.error("PR validation requires the GitHub base SHA")
    sys.exit(0 if check(args.catalog, base_ref=args.base_ref, repository_id=args.repository_id) else 1)
