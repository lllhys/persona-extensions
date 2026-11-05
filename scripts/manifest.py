#!/usr/bin/env python3
"""Read an extension's manifest: extensions/<id>/persona-extension.json, the one file an extension has.

  scripts/manifest.py path <id>       the manifest's path
  scripts/manifest.py version <id>    its version
  scripts/manifest.py packaged <id>   the manifest as packages and indexes carry it: without "tags"
  scripts/manifest.py ids             every extension in extensions/
  scripts/manifest.py check           every manifest is sound (what CI runs): id, version, tags, no retired files,
                                      the files it references are in its package, its pack members are here
  scripts/manifest.py changed <ref>   the extensions whose version is not what it was at <ref> (new ones too)
  scripts/manifest.py selected <id or ref>…   what scripts/ci.sh release releases: each id (also as extensions/<id>),
                                      and what changed since each git ref; anything else fails
"""
import json, os, posixpath, re, subprocess, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
NEW, OLD = "persona-extension.json", "persona-plugin.json"
SEMVER = re.compile(r"^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$")
# What a hub takes as a market entry's tags (nwverse/persona server/internal/extension/market.go).
TAGS_MOST, TAG_LONGEST = 16, 24
# The directories scripts/package.sh copies into a package besides the manifest and the program.
PACKAGED_DIRS = ("skills", "assistant")


def ids():
    d = os.path.join(ROOT, "extensions")
    return sorted(e for e in os.listdir(d) if os.path.isdir(os.path.join(d, e))) if os.path.isdir(d) else []


def path(id):
    p = os.path.join(ROOT, "extensions", id, NEW)
    if not os.path.exists(p):
        sys.exit("extensions/%s has no %s" % (id, NEW))
    return p


def _no_duplicate_keys(pairs):
    # A repeated key is an error, not last-one-wins: a bad merge once shipped two "version" lines that parsers
    # (Python's json, Go's encoding/json, the hub) all accepted silently.
    seen = set()
    for k, _ in pairs:
        if k in seen:
            raise ValueError("manifest has a duplicate key %r" % k)
        seen.add(k)
    return dict(pairs)


def load(id):
    with open(path(id)) as f:
        return json.load(f, object_pairs_hook=_no_duplicate_keys)


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


def is_commit(ref):
    r = subprocess.run(["git", "-C", ROOT, "rev-parse", "--verify", "--quiet", "--end-of-options", ref + "^{commit}"],
                       capture_output=True)
    return r.returncode == 0


def changed(ref):
    """The extensions whose version is not what it was at ref. A ref git doesn't have fails: version_at could not tell
    it from one where no extension was there yet, and every extension would count as new."""
    if not is_commit(ref):
        sys.exit("%r is not a git commit here" % ref)
    return [id for id in ids() if version_at(ref, id) != load(id)["version"]]


def selected(args):
    """The extensions to release for scripts/ci.sh release's arguments: an id as scripts/release.sh takes it, or a git
    ref to release what changed since. A misspelled id must fail, not pass for a ref and select every extension."""
    out = []
    for a in args:
        id = a.rstrip("/")
        id = id[len("extensions/"):] if id.startswith("extensions/") else id
        if id in ids():
            out.append(id)
        elif is_commit(a):
            out += changed(a)
        else:
            sys.exit("%r is not an extension in extensions/ nor a git commit" % a)
    return out


def referenced(m):
    """The package paths a manifest references, as (what, path, kind): kind "skill" is a directory with SKILL.md,
    "file" a file. The hub refuses a package without them at install (nwverse/persona server/internal/extension)."""
    c = m.get("contributes") or {}
    out = []
    for s in c.get("skills") or []:
        out.append(("skills %s: dir" % s.get("id"), s.get("dir"), "skill"))
    for p in c.get("prompts") or []:
        if p.get("kind") == "instructions":
            out.append(("prompts %s: file" % p.get("id"), p.get("file"), "file"))
    for a in c.get("assistant") or []:
        for sk in a.get("skills") or []:
            out.append(("assistant %s: skills" % a.get("id"), sk, "skill"))
        for key in ("instructions", "gate"):
            if a.get(key):
                out.append(("assistant %s: %s" % (a.get("id"), key), a.get(key), "file"))
    return out


def inside(p):
    """A clean relative path inside the package, as the hub takes one (its checkPackagePath)."""
    return isinstance(p, str) and p not in ("", ".", "..") and not p.startswith(("/", "../")) and "\\" not in p \
        and posixpath.normpath(p) == p


def check_files(id, m):
    """Each path the manifest references is inside the extension, under a directory scripts/package.sh copies, and
    there: a path anywhere else would be left out of the package without a word."""
    bad = []
    root = os.path.join(ROOT, "extensions", id)
    for what, p, kind in referenced(m):
        where = "extensions/%s: %s %r" % (id, what, p)
        if not inside(p):
            bad.append("%s is not a path inside the package" % where)
        elif p.split("/", 1)[0] not in PACKAGED_DIRS:
            bad.append("%s is not under %s/: scripts/package.sh would leave it out" % (where, "/ or ".join(PACKAGED_DIRS)))
        elif kind == "skill" and not os.path.isfile(os.path.join(root, p, "SKILL.md")):
            bad.append("%s has no SKILL.md" % where)
        elif kind == "file" and not os.path.isfile(os.path.join(root, p)):
            bad.append("%s is not a file in the extension" % where)
    return bad


def check_pack(id, m):
    """A pack's members from its own market (no "market" of their own) are extensions of this repository."""
    bad, here = [], ids()
    for e in m.get("extension_pack") or []:
        member = e.get("id") if isinstance(e, dict) else None
        if isinstance(e, dict) and e.get("market"):
            continue
        if member == id:
            bad.append("extensions/%s: extension_pack lists the pack itself" % id)
        elif member not in here:
            bad.append("extensions/%s: extension_pack member %r is not an extension in extensions/" % (id, member))
    return bad


def check():
    bad = []
    for id in ids():
        try:
            m = load(id)
        except ValueError as e:
            bad.append("extensions/%s: %s" % (id, e))
            continue
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
        bad += check_files(id, m) + check_pack(id, m)
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
        print("\n".join(changed(sys.argv[2])))
    elif cmd == "selected":
        print("\n".join(selected(sys.argv[2:])))
    elif cmd == "check":
        sys.exit(0 if check() else 1)
    else:
        sys.exit(__doc__)
