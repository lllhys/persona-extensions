#!/usr/bin/env python3
"""Read an extension's manifest: extensions/<id>/persona-extension.json, the one file an extension has.

  scripts/manifest.py path <id>       the manifest's path
  scripts/manifest.py version <id>    its version
  scripts/manifest.py packaged <id>   the manifest as packages and indexes carry it: without "tags"
  scripts/manifest.py ids             every extension in extensions/
  scripts/manifest.py check           every manifest is sound (what CI runs)
  scripts/manifest.py changed <ref>   the extensions whose version is not what it was at <ref> (new ones too)
"""
import json, os, re, subprocess, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
NEW, OLD = "persona-extension.json", "persona-plugin.json"
SEMVER = re.compile(r"^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$")
# What a hub takes as a market entry's tags (nwverse/persona server/internal/extension/market.go).
TAGS_MOST, TAG_LONGEST = 16, 24


def ids():
    d = os.path.join(ROOT, "extensions")
    return sorted(e for e in os.listdir(d) if os.path.isdir(os.path.join(d, e))) if os.path.isdir(d) else []


def path(id):
    p = os.path.join(ROOT, "extensions", id, NEW)
    if not os.path.exists(p):
        sys.exit("extensions/%s has no %s" % (id, NEW))
    return p


def load(id):
    return json.load(open(path(id)))


def tags(id):
    return load(id).get("tags", [])


def packaged(id):
    """The manifest without its tags, for packages (as both persona-extension.json and persona-plugin.json) and
    indexes: hubs from before tags (nwverse/persona#2424) read manifests strictly and refuse a field they don't know.
    The index entry carries the tags instead."""
    m = load(id)
    m.pop("tags", None)
    return m


def dumps(m):
    return json.dumps(m, ensure_ascii=False, indent=2) + "\n"


def version_at(ref, id):
    """The version at a git ref, or None when the extension wasn't there (also under its pre-monorepo path)."""
    for p in ("extensions/%s/%s" % (id, NEW), "extensions/%s/%s" % (id, OLD), "%s/%s" % (id, OLD)):
        r = subprocess.run(["git", "-C", ROOT, "show", "%s:%s" % (ref, p)], capture_output=True, text=True)
        if r.returncode == 0:
            return json.loads(r.stdout).get("version")
    return None


def check():
    bad = []
    for id in ids():
        m = load(id)
        if m.get("id") != id:
            bad.append("extensions/%s: the manifest's id is %r, not the directory's name" % (id, m.get("id")))
        if not SEMVER.match(str(m.get("version", ""))):
            bad.append("extensions/%s: version %r is not MAJOR.MINOR.PATCH" % (id, m.get("version")))
        t = m.get("tags", [])
        if not isinstance(t, list) or len(t) > TAGS_MOST or any(not isinstance(x, str) or not 0 < len(x) <= TAG_LONGEST for x in t):
            bad.append("extensions/%s: tags must be at most %d strings of 1 to %d characters" % (id, TAGS_MOST, TAG_LONGEST))
        # Generated or folded into the manifest now: scripts/package.sh writes persona-plugin.json, tags are in it.
        for gone in (OLD, "market.json"):
            if os.path.exists(os.path.join(ROOT, "extensions", id, gone)):
                bad.append("extensions/%s: no %s (one manifest: %s; see docs/reference.md)" % (id, gone, NEW))
    for b in bad:
        print(b, file=sys.stderr)
    return not bad


if __name__ == "__main__":
    cmd = sys.argv[1] if len(sys.argv) > 1 else ""
    if cmd == "path":
        print(path(sys.argv[2]))
    elif cmd == "version":
        print(load(sys.argv[2])["version"])
    elif cmd == "packaged":
        sys.stdout.write(dumps(packaged(sys.argv[2])))
    elif cmd == "ids":
        print("\n".join(ids()))
    elif cmd == "changed":
        print("\n".join(id for id in ids() if version_at(sys.argv[2], id) != load(id)["version"]))
    elif cmd == "check":
        sys.exit(0 if check() else 1)
    else:
        sys.exit(__doc__)
