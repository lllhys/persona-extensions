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
# Both names of the manifest go in: hubs from before the rename to extensions (nwverse/persona#1844) read only
# persona-plugin.json; newer ones prefer persona-extension.json.
for m in persona-extension.json persona-plugin.json; do
  [ -f "$src/$m" ] && cp "$src/$m" "$out/"
done
# What the manifest points at besides the program: the assistant's skills and instructions.
for extra in skills assistant; do
  [ -d "$src/$extra" ] && cp -R "$src/$extra" "$out/"
done
# A declarative extension (no runtime: skills, prompts, themes, a pack…) has no program to build.
python3 -c 'import json,sys;[print(k, v) for k, v in json.load(open(sys.argv[1])).get("runtime", {}).get("command", {}).items()]' "$manifest" |
while read -r plat path; do
  GOOS=${plat%-*} GOARCH=${plat#*-} CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$out/$path" "./$src"
done
rmdir "$out/bin" 2>/dev/null || true # nothing built: no empty bin/ in the package
# No macOS metadata: bsdtar would add AppleDouble ._* entries (a second top-level entry, so the hub finds no
# manifest) and xattr records; COPYFILE_DISABLE is ignored elsewhere, and GNU tar knows --no-xattrs too.
COPYFILE_DISABLE=1 tar --no-xattrs -C dist -czf "dist/$id-$version.tar.gz" "$id"
(cd dist && sha256sum "$id-$version.tar.gz" | tee "$id-$version.tar.gz.sha256")
