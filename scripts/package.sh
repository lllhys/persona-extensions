#!/bin/bash
# Build an extension for every platform its manifest names and pack it: dist/<id>-<version>.tar.gz (+ its sha256).
#   ./scripts/package.sh <id>          the extension is extensions/<id>/
# Install on the hub's machine: personad extension install file:/path/to/<id>-<version>.tar.gz
set -euo pipefail
cd "$(dirname "$0")/.."
id="${1:?usage: scripts/package.sh <extension id>}"
id="${id%/}"; id="${id#extensions/}"
src="extensions/$id"
manifest=$(scripts/manifest.py path "$id")
version=$(scripts/manifest.py version "$id")
out="dist/$id"
rm -rf "${out:?}" && mkdir -p "$out/bin"
# The manifest goes in under both names, without source-only tags/replaces/icon (scripts/manifest.py packaged).
# Hubs from before the rename to extensions (nwverse/persona#1844) read only persona-plugin.json, newer ones
# prefer persona-extension.json, and
# hubs from before tags (nwverse/persona#2424) refuse a manifest with them. Only persona-extension.json is committed;
# stop writing persona-plugin.json once the compatibility period ends (docs/reference.md, "Compatibility").
scripts/manifest.py packaged "$id" >"$out/persona-extension.json"
cp "$out/persona-extension.json" "$out/persona-plugin.json"
# What the manifest points at besides the program: the assistant's skills and instructions, the looks' images, the
# manifest's translations the hub reads from l10n/, and source-owned assets (icons and their licenses).
for extra in skills assistant looks l10n assets; do
  [ -d "$src/$extra" ] && cp -R "$src/$extra" "$out/"
done
# Native TypeScript source packages ship the import-free bundle and its verified digest.
if python3 -I -c 'import json,sys; sys.exit(0 if json.load(open(sys.argv[1])).get("runtime", {}).get("mode") == "deno" else 1)' "$manifest"; then
  # A clean checkout has no build output. Compile through the extension's locked
  # public dependencies, not a pre-existing local bundle or an untracked SDK edit.
  if [ ! -x "$src/node_modules/.bin/esbuild" ]; then
    npm --prefix sdk/ts ci --ignore-scripts --registry=https://registry.npmjs.org --no-audit --no-fund
    npm --prefix "$src" ci --ignore-scripts --registry=https://registry.npmjs.org --no-audit --no-fund
  fi
  npm --prefix "$src" run build:source
  python3 -I - "$src" "$out" <<'PY'
import hashlib, json, pathlib, sys
src, out = map(pathlib.Path, sys.argv[1:])
m = json.loads((src / "persona-extension.json").read_text())
source = m["runtime"]["source"]
bundle = src / source["bundle"]
data = bundle.read_bytes()
if not data or hashlib.sha256(data).hexdigest() != source["sha256"]:
    raise SystemExit("source bundle digest does not match the manifest")
for name in ("persona-extension.json", "persona-plugin.json"):
    (out / name).write_bytes((out / name).read_bytes())
target = out / source["bundle"]
target.parent.mkdir(parents=True, exist_ok=True)
target.write_bytes(data)
PY
fi
# A Python extension (a requirements.txt next to its manifest) runs from its source as it is: the launcher in bin/, the
# program in src/ and the hash-locked requirements.txt it installs itself on first start. Not its tests; caches
# (__pycache__, *.pyc) and editor leftovers stay out, files are 0644 and launchers 0755.
if [ -f "$src/requirements.txt" ]; then
  cp -R "$src/bin" "$src/src" "$out/"
  cp "$src/requirements.txt" "$out/"
  python3 -I - "$out" <<'PY'
import os, shutil, sys
out = sys.argv[1]
for top in ("bin", "src", "skills", "assistant"):
    for dirpath, dirs, files in os.walk(os.path.join(out, top), topdown=True):
        for name in [n for n in dirs + files if n.startswith(".") or n.endswith(("~", ".pyc")) or n == "__pycache__"]:
            p = os.path.join(dirpath, name)
            shutil.rmtree(p) if os.path.isdir(p) else os.remove(p)
            if name in dirs:
                dirs.remove(name)
        for name in files:
            p = os.path.join(dirpath, name)
            if os.path.exists(p):
                os.chmod(p, 0o755 if top == "bin" else 0o644)
PY
fi
# A declarative extension (no runtime: skills, prompts, themes, a pack…) has no program to build. A command that is a
# string (a Python launcher) has none either: only a platform map is built with Go.
# -I: with -c, Python would otherwise put the cwd (repo root, an untrusted PR tree in CI) first on sys.path,
# letting a planted json.py shadow the stdlib module. The snippet uses only stdlib json/sys and an explicit argv path.
python3 -I -c 'import json,sys;c=json.load(open(sys.argv[1])).get("runtime", {}).get("command", {});[print(k, v) for k, v in (c.items() if isinstance(c, dict) else [])]' "$manifest" |
while read -r plat path; do
  # In the extension's own module (extensions/<id>/go.mod): there is none at the repository root.
  GOOS=${plat%-*} GOARCH=${plat#*-} CGO_ENABLED=0 go -C "$src" build -trimpath -ldflags "-s -w" -o "$PWD/$out/$path" .
done
rmdir "$out/bin" 2>/dev/null || true # nothing built: no empty bin/ in the package
# No macOS/editor junk in the archive. COPYFILE_DISABLE/--no-xattrs only suppress AppleDouble ._* entries and
# xattr records tar synthesizes itself; junk already in the source tree (._* files, .DS_Store, __pycache__/)
# would be copied verbatim, and a stray ._* at the top level is a second top-level entry, so the hub finds no
# manifest. Exclude by basename at every depth (manifest.py check rejects the same names under skills/assistant).
COPYFILE_DISABLE=1 tar --no-xattrs \
  --exclude='._*' --exclude=.DS_Store --exclude=__pycache__ \
  -C dist -czf "dist/$id-$version.tar.gz" "$id"
(cd dist && sha256sum "$id-$version.tar.gz" | tee "$id-$version.tar.gz.sha256")
