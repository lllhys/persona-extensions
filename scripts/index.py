#!/usr/bin/env python3
"""The market's index (docs/releases.md, "Market and indexes"), in two files with the same entries:

  persona-extensions.json   key "extensions", read by hubs that know extensions (nwverse/persona#1844)
  persona-plugins.json      key "plugins" with the old manifest, read by hubs from before the rename

  scripts/index.py add <id> <tag>   put dist/<id>-<version>.tar.gz in as the release asset of <tag>
  scripts/index.py has <id> <tag>   exit 0 when both files list <id> with a package of release <tag>
                                    (release:<tag>/<file>, pinned by its sha256); its entry is replaced, the others stay
  scripts/index.py check            both files list the same extensions, versions and packages, and no entry's
                                    manifest has tags (what CI runs)

An entry's manifest is scripts/manifest.py packaged (the manifest without tags, as the package carries it); its tags
are the manifest's.

The signature of both files is scripts/sign-index.py (nwverse/persona#1848).
"""
import hashlib, json, os, sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import manifest  # noqa: E402

ROOT = manifest.ROOT
NEW = os.path.join(ROOT, "persona-extensions.json")
OLD = os.path.join(ROOT, "persona-plugins.json")


def read(p, key):
    if not os.path.exists(p):
        return None
    d = json.load(open(p))
    d.setdefault(key, [])
    return d


def write(p, d):
    with open(p, "w") as f:
        json.dump(d, f, ensure_ascii=False, indent=2)
        f.write("\n")


def meta(d):
    return {k: d[k] for k in ("market_version", "name", "description", "homepage", "signing_key") if k in d}


def add(id, tag):
    m = manifest.load(id)
    pkg = "%s-%s.tar.gz" % (id, m["version"])
    data = open(os.path.join(ROOT, "dist", pkg), "rb").read()
    new, old = read(NEW, "extensions"), read(OLD, "plugins")
    if new is None and old is None:
        sys.exit("no index yet: write persona-extensions.json with market_version, name, description first")
    head = meta(new or old)
    new = new or dict(head, extensions=[])
    old = old or dict(head, plugins=[])
    # The tags come out of the manifest into the entry: hubs from before tags refuse a manifest with them.
    man, tags = manifest.packaged(id), manifest.tags(id)
    packages = [{"url": "release:%s/%s" % (tag, pkg), "sha256": hashlib.sha256(data).hexdigest(), "size": len(data)}]
    for d, key in ((new, "extensions"), (old, "plugins")):
        d[key] = [e for e in d[key] if e["manifest"]["id"] != id] + [{"manifest": man, "tags": tags, "packages": packages}]
        d[key].sort(key=lambda e: e["manifest"]["id"])
    write(NEW, new)
    write(OLD, old)
    print("index: %s %s → release:%s/%s" % (id, m["version"], tag, pkg))


def has(id, tag):
    """Whether both indexes list id with a package of release tag."""
    for path, key in ((NEW, "extensions"), (OLD, "plugins")):
        d = read(path, key)
        if d is None or not any(e["manifest"]["id"] == id and any(p["url"].startswith("release:%s/" % tag) for p in e["packages"]) for e in d[key]):
            return False
    return True


def check():
    new, old = read(NEW, "extensions"), read(OLD, "plugins")
    if new is None or old is None:
        print("persona-extensions.json and persona-plugins.json must both be there", file=sys.stderr)
        return False

    def key(d, k):
        return [(e["manifest"]["id"], e["manifest"]["version"], json.dumps(e["packages"], sort_keys=True)) for e in d[k]]

    if key(new, "extensions") != key(old, "plugins"):
        print("persona-extensions.json and persona-plugins.json list different extensions or packages", file=sys.stderr)
        return False
    tagged = [e["manifest"]["id"] for d, k in ((new, "extensions"), (old, "plugins")) for e in d[k] if "tags" in e["manifest"]]
    if tagged:
        print("index entries whose manifest has tags (hubs from before tags refuse it): %s" % ", ".join(tagged), file=sys.stderr)
        return False
    if meta(new) != meta(old):
        print("persona-extensions.json and persona-plugins.json have a different name or description", file=sys.stderr)
        return False
    return True


if __name__ == "__main__":
    cmd = sys.argv[1] if len(sys.argv) > 1 else ""
    if cmd == "has" and len(sys.argv) == 4:
        sys.exit(0 if has(sys.argv[2].rstrip("/").removeprefix("extensions/"), sys.argv[3]) else 1)
    if cmd == "add" and len(sys.argv) == 4:
        add(sys.argv[2].rstrip("/").removeprefix("extensions/"), sys.argv[3])
    elif cmd == "check":
        sys.exit(0 if check() else 1)
    else:
        sys.exit(__doc__)
