#!/usr/bin/env python3
"""The repository's layout rules: no package-manager file at the root, and every Go extension is its own module.

  scripts/check-layout.py           check, print what is wrong and exit 1 if anything is
  scripts/check-layout.py modules   the Go modules scripts/ci.sh vets and tests, one directory per line
                                    (pluginkit, tools, then extensions/<id>; sdk/go has its own step)

Dependencies belong to the extension that needs them, in extensions/<id>/ (go.mod and go.sum, package.json…), so each
extension builds, tests and packages on its own. Shared Go code lives in its own module (pluginkit/, the copied SDK in
sdk/go/), which an extension pulls in with a replace directive; repository-level Go tests live in tools/.
"""
import os
import re
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
