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
rm -rf "$out" && mkdir -p "$out/bin"
# The manifest goes in under both names, without its tags (scripts/manifest.py packaged): hubs from before the rename
# to extensions (nwverse/persona#1844) read only persona-plugin.json, newer ones prefer persona-extension.json, and
# hubs from before tags (nwverse/persona#2424) refuse a manifest with them. Only persona-extension.json is committed;
# stop writing persona-plugin.json once the compatibility period ends (docs/reference.md, "Compatibility").
scripts/manifest.py packaged "$id" >"$out/persona-extension.json"
cp "$out/persona-extension.json" "$out/persona-plugin.json"
# What the manifest points at besides the program: the assistant's skills and instructions.
for extra in skills assistant; do
  [ -d "$src/$extra" ] && cp -R "$src/$extra" "$out/"
done
# A declarative extension (no runtime: skills, prompts, themes, a pack…) has no program to build.
# -I: with -c, Python would otherwise put the cwd (repo root, an untrusted PR tree in CI) first on sys.path,
# letting a planted json.py shadow the stdlib module. The snippet uses only stdlib json/sys and an explicit argv path.
python3 -I -c 'import json,sys;[print(k, v) for k, v in json.load(open(sys.argv[1])).get("runtime", {}).get("command", {}).items()]' "$manifest" |
while read -r plat path; do
  GOOS=${plat%-*} GOARCH=${plat#*-} CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$out/$path" "./$src"
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
