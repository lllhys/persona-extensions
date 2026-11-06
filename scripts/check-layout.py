#!/usr/bin/env python3
"""The repository's layout rules: no package-manager file at the root, every Go extension is its own module, and no
compiled program is committed.

  scripts/check-layout.py           check, print what is wrong and exit 1 if anything is
  scripts/check-layout.py modules   the Go modules scripts/ci.sh vets and tests, one directory per line
                                    (pluginkit, tools, then extensions/<id>; sdk/go has its own step)

Dependencies belong to the extension that needs them, in extensions/<id>/ (go.mod and go.sum, package.json…), so each
extension builds, tests and packages on its own. Shared Go code lives in its own module (pluginkit/, the copied SDK in
sdk/go/), which an extension pulls in with a replace directive; repository-level Go tests live in tools/.
"""
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# Names a package manager or its lockfile uses, for any language. Exact names, plus the patterns below.
FORBIDDEN = {
    # Go
    "go.mod", "go.sum", "go.work", "go.work.sum", "vendor",
    # JavaScript / TypeScript
    "package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "pnpm-workspace.yaml",
    "yarn.lock", ".yarnrc.yml", ".pnp.cjs", "bun.lock", "bun.lockb", "deno.json", "deno.jsonc", "deno.lock",
    "node_modules",
    # Rust
    "Cargo.toml", "Cargo.lock",
    # Python
    "pyproject.toml", "setup.py", "setup.cfg", "Pipfile", "Pipfile.lock", "poetry.lock", "uv.lock", "pdm.lock",
    # Others
    "Gemfile", "Gemfile.lock", "composer.json", "composer.lock", "pom.xml", "build.gradle", "build.gradle.kts",
    "settings.gradle", "settings.gradle.kts", "Package.swift", "Package.resolved", "mix.exs", "mix.lock",
}
FORBIDDEN_PATTERNS = [re.compile(r"requirements.*\.txt"), re.compile(r".*\.csproj"), re.compile(r".*\.gemspec")]

SKIP_DIRS = {".git", "dist", "node_modules", "testdata"}

# The first bytes of a compiled program: ELF (linux), Mach-O 64- and 32-bit, and Mach-O universal (darwin). A
# `go build` inside an extension's directory leaves one there; the release builds every program from source
# (scripts/package.sh), so one in the repository is only weight in every clone.
PROGRAM_MAGIC = (b"\x7fELF", b"\xcf\xfa\xed\xfe", b"\xce\xfa\xed\xfe", b"\xca\xfe\xba\xbe")


def forbidden(name):
    return name in FORBIDDEN or any(p.fullmatch(name) for p in FORBIDDEN_PATTERNS)


def has_go(d):
    for top, dirs, files in os.walk(d):
        dirs[:] = [x for x in dirs if x not in SKIP_DIRS]
        if any(f.endswith(".go") for f in files):
            return True
    return False


def module_path(gomod):
    with open(gomod, encoding="utf-8") as f:
        for line in f:
            m = re.match(r"module\s+(\S+)", line)
            if m:
                return m.group(1)
    return None


def extensions(root):
    d = os.path.join(root, "extensions")
    return sorted(e for e in os.listdir(d) if os.path.isdir(os.path.join(d, e))) if os.path.isdir(d) else []


def modules(root):
    out = [m for m in ("pluginkit", "tools") if os.path.isfile(os.path.join(root, m, "go.mod"))]
    out += [f"extensions/{e}" for e in extensions(root) if os.path.isfile(os.path.join(root, "extensions", e, "go.mod"))]
    return out


def committed(root, top):
    """The files under root/top, relative to root: the tracked ones in a git checkout (a local build stays untracked or
    ignored), every file otherwise."""
    if os.path.exists(os.path.join(root, ".git")):  # a directory, or a file in a worktree
        try:
            res = subprocess.run(["git", "-C", root, "ls-files", "-z", "--", top], capture_output=True)
        except OSError:  # no git: look at every file
            res = None
        if res is not None and res.returncode == 0:
            return sorted(n.decode("utf-8", "surrogateescape") for n in res.stdout.split(b"\0") if n)
    out = []
    for d, dirs, files in os.walk(os.path.join(root, top)):
        dirs[:] = [x for x in dirs if x not in SKIP_DIRS]
        out += [os.path.relpath(os.path.join(d, f), root) for f in files]
    return sorted(out)


def compiled(path):
    try:
        with open(path, "rb") as f:
            return f.read(4) in PROGRAM_MAGIC
    except OSError:
        return False


def problems(root):
    out = []
    for name in sorted(os.listdir(root)):
        if forbidden(name):
            out.append(f"{name}: package-manager files do not go at the repository root; "
                       "put dependencies in the extension that needs them (extensions/<id>/)")
    for e in extensions(root):
        d = os.path.join(root, "extensions", e)
        if not has_go(d):
            continue
        gomod = os.path.join(d, "go.mod")
        if not os.path.isfile(gomod):
            out.append(f"extensions/{e}: has Go code but no go.mod of its own")
            continue
        path = module_path(gomod)
        if not path or not path.endswith(f"/extensions/{e}"):
            out.append(f"extensions/{e}/go.mod: module path {path!r} does not end in /extensions/{e}")
    for name in committed(root, "extensions"):
        if compiled(os.path.join(root, name)):
            out.append(f"{name}: a compiled program; the release builds it from source, "
                       "so it does not go in the repository (go build -o elsewhere, or delete it)")
    # Go code outside a module: nothing at the root resolves it any more.
    for top, dirs, files in os.walk(root):
        dirs[:] = [x for x in dirs if x not in SKIP_DIRS]
        if not any(f.endswith(".go") for f in files):
            continue
        d = top
        while d != root and not os.path.isfile(os.path.join(d, "go.mod")):
            d = os.path.dirname(d)
        if d == root:
            out.append(f"{os.path.relpath(top, root)}: Go code that belongs to no module")
            dirs[:] = []
    return out


def main(argv):
    root = os.environ.get("LAYOUT_ROOT", ROOT)
    if argv[1:] == ["modules"]:
        print("\n".join(modules(root)))
        return 0
    if argv[1:]:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    bad = problems(root)
    for p in bad:
        print(p)
    if bad:
        return 1
    print(f"layout: ok ({len(modules(root))} Go modules)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
