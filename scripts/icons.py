#!/usr/bin/env python3
"""Validate source-owned icon paths, bounded image bytes and their provenance registry.

Only the Python standard library is used. SVGs are passive vector documents:
no scripts, foreign content, entities, CSS sheets or external resource references.
"""
import hashlib
import json
import math
import pathlib
import re
import struct
import xml.etree.ElementTree as ET
import zlib

MAX_BYTES = 2 * 1024 * 1024
MAX_DIMENSION = 4096
ICON_PATH = re.compile(r"assets/(?:[A-Za-z0-9_-][A-Za-z0-9._-]*/)*[A-Za-z0-9_-][A-Za-z0-9._-]*\.(?:png|svg)\Z")
SVG_TAGS = set("svg g defs path rect circle ellipse line polyline polygon use symbol clipPath mask linearGradient radialGradient stop title desc".split())
SVG_NS = "http://www.w3.org/2000/svg"


def valid_path(path):
    return isinstance(path, str) and len(path) <= 240 and ICON_PATH.fullmatch(path) is not None


def contained_file(root, path):
    """Reject symlinks at every component, even ones resolving back into the tree."""
    root = pathlib.Path(root)
    current = root
    for part in pathlib.PurePosixPath(path).parts:
        if part in ("", ".", ".."):
            raise ValueError("unclean asset path")
        current = current / part
        if current.is_symlink():
            raise ValueError("asset path must not contain symlinks")
    if not current.is_file() or not current.resolve().is_relative_to(root.resolve()):
        raise ValueError("asset must be a file inside the extension")
    return current


def png_size(data, square=True):
    if not data.startswith(b"\x89PNG\r\n\x1a\n"):
        raise ValueError("invalid PNG signature")
    offset, chunks, compressed = 8, [], bytearray()
    width = height = depth = color = interlace = None
    while offset < len(data):
        if offset + 12 > len(data):
            raise ValueError("truncated PNG chunk")
        length, kind = struct.unpack_from(">I4s", data, offset)
        end = offset + 12 + length
        if end > len(data):
            raise ValueError("truncated PNG data")
        payload = data[offset + 8:end - 4]
        if zlib.crc32(kind + payload) != struct.unpack_from(">I", data, end - 4)[0]:
            raise ValueError("invalid PNG chunk checksum")
        if not chunks and kind != b"IHDR":
            raise ValueError("PNG must start with IHDR")
        if kind == b"IHDR":
            if chunks or length != 13:
                raise ValueError("invalid PNG header")
            width, height, depth, color, compression, filtering, interlace = struct.unpack(">IIBBBBB", payload)
            depths = {0: (1, 2, 4, 8, 16), 2: (8, 16), 3: (1, 2, 4, 8), 4: (8, 16), 6: (8, 16)}
            if color not in depths or depth not in depths[color] or compression or filtering or interlace not in (0, 1):
                raise ValueError("invalid PNG encoding")
            if not 0 < width <= MAX_DIMENSION or not 0 < height <= MAX_DIMENSION:
                raise ValueError("PNG dimensions must be positive and bounded")
            if square and not 256 <= width == height:
                raise ValueError("PNG must be square and 256..4096 pixels")
        elif kind == b"IDAT":
            compressed.extend(payload)
        elif kind == b"IEND":
            if length or end != len(data) or not compressed:
                raise ValueError("invalid PNG end")
            chunks.append(kind)
            break
        elif kind[0] & 32 == 0 and kind != b"PLTE":
            raise ValueError("unsupported critical PNG chunk")
        chunks.append(kind)
        offset = end
    if not chunks or chunks[-1] != b"IEND":
        raise ValueError("PNG has no IEND")
    channels = {0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[color]
    # Adam7 has seven reduced images, each with a filter byte per scanline.
    passes = [(0, 0, 1, 1)] if not interlace else [(0, 0, 8, 8), (4, 0, 8, 8), (0, 4, 4, 8), (2, 0, 4, 4), (0, 2, 2, 4), (1, 0, 2, 2), (0, 1, 1, 2)]
    rows = []
    for x, y, dx, dy in passes:
        w, h = max(0, (width - x + dx - 1) // dx), max(0, (height - y + dy - 1) // dy)
        if w and h:
            rows.extend([(w * channels * depth + 7) // 8 + 1] * h)
    expected = sum(rows)
    decoder = zlib.decompressobj()
    try:
        raw = decoder.decompress(compressed, expected + 1)
    except zlib.error as e:
        raise ValueError("invalid PNG compressed pixels") from e
    if len(raw) != expected or not decoder.eof or decoder.unused_data or decoder.unconsumed_tail:
        raise ValueError("invalid PNG pixel length")
    pos = 0
    for row in rows:
        if raw[pos] > 4:
            raise ValueError("invalid PNG scanline filter")
        pos += row
    return width, height


def svg_size(data, square=True):
    try:
        data = data.decode("utf-8-sig")
    except UnicodeDecodeError as e:
        raise ValueError("SVG must use UTF-8") from e
    # XML declarations are harmless; declarations of entities/DTDs are not.
    if re.search(r"<!\s*(?:DOCTYPE|ENTITY)\b", data, re.I):
        raise ValueError("SVG declarations and entities are forbidden")
    if "<?" in re.sub(r"^\s*<\?xml[^?]*\?>", "", data):
        raise ValueError("SVG processing instructions are forbidden")
    try:
        root = ET.fromstring(data)
    except ET.ParseError as e:
        raise ValueError("invalid SVG XML") from e
    if root.tag != "{%s}svg" % SVG_NS:
        raise ValueError("SVG root must use the SVG namespace")
    for element in root.iter():
        if not element.tag.startswith("{%s}" % SVG_NS) or element.tag.split("}")[-1] not in SVG_TAGS:
            raise ValueError("SVG contains forbidden elements")
        for key, value in element.attrib.items():
            local = key.split("}")[-1]
            if key.startswith("{") and key != "{http://www.w3.org/1999/xlink}href":
                raise ValueError("SVG contains a forbidden attribute namespace")
            if local.lower().startswith("on") or local in ("src", "base"):
                raise ValueError("SVG contains active attributes")
            if local == "href" and not re.fullmatch(r"#[A-Za-z_][\w.-]*", value):
                raise ValueError("SVG href must reference a local fragment")
            # Reject escapes/comments that can disguise CSS URLs, imports or executable values.
            if any(s in value.lower() for s in ("\\", "/*", "@", "expression", "javascript:", "data:", "http:", "https:", "//")):
                raise ValueError("SVG contains unsafe attribute values")
            cleaned = re.sub(r"url\(\s*['\"]?#[A-Za-z_][\w.-]*['\"]?\s*\)", "", value, flags=re.I)
            if re.search(r"url\s*\(", cleaned, re.I):
                raise ValueError("SVG URLs must reference local fragments")
    def dimension(value):
        if not re.fullmatch(r"(?:\d+(?:\.\d*)?|\.\d+)(?:px)?", value):
            raise ValueError("SVG dimensions must be numeric or px")
        number = float(value.removesuffix("px"))
        if not math.isfinite(number) or not 0 < number <= MAX_DIMENSION:
            raise ValueError("SVG dimensions must be positive and bounded")
        return number
    box = root.get("viewBox")
    if box:
        try:
            coords = [float(x) for x in re.split(r"[\s,]+", box.strip())]
        except ValueError as e:
            raise ValueError("invalid SVG viewBox") from e
        if len(coords) != 4 or not all(math.isfinite(x) for x in coords) or not all(0 < x <= MAX_DIMENSION for x in coords[2:]) or (square and coords[2] != coords[3]):
            raise ValueError("SVG viewBox must be positive, bounded and square for display icons")
        size = tuple(coords[2:])
    else:
        size = None
    w, h = root.get("width"), root.get("height")
    if w is not None or h is not None:
        if w is None or h is None or (square and dimension(w) != dimension(h)):
            raise ValueError("SVG dimensions must be square for display icons")
        size = dimension(w), dimension(h)
    if size is None:
        raise ValueError("SVG needs dimensions or viewBox")
    return size


def validate_image(data, path):
    """Validate captured archive bytes without trusting or touching a filesystem."""
    if not valid_path(path):
        raise ValueError("icon must be a clean assets/ PNG or SVG path (at most 240 characters)")
    if not 0 < len(data) <= MAX_BYTES:
        raise ValueError("icon must be nonempty and at most 2 MiB")
    return png_size(data) if path.endswith(".png") else svg_size(data)


def validate_icon(root, path):
    if not valid_path(path):
        raise ValueError("icon must be a clean assets/ PNG or SVG path (at most 240 characters)")
    file = contained_file(root, path)
    if not 0 < file.stat().st_size <= MAX_BYTES:
        raise ValueError("icon must be nonempty and at most 2 MiB")
    data = file.read_bytes()
    width, height = validate_image(data, path)
    return {"path": path, "sha256": hashlib.sha256(data).hexdigest(), "format": file.suffix[1:], "width": width, "height": height}


def validate_registry(root, manifests):
    """Optional during migration; once icons are declared, every one needs provenance."""
    root = pathlib.Path(root)
    registry = root / "icon-sources.json"
    declared = {m["id"]: m["icon"] for m in manifests if "icon" in m}
    if not registry.exists():
        if declared:
            raise ValueError("source icons require icon-sources.json")
        return
    if len(declared) != len(manifests):
        raise ValueError("every official extension must declare an attributed icon")
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError("duplicate icon registry key")
            result[key] = value
        return result
    data = json.loads(registry.read_text(), object_pairs_hook=unique)
    if not isinstance(data, dict) or set(data) != {"schema_version", "sources", "icons"} or type(data["schema_version"]) is not int or data["schema_version"] != 1:
        raise ValueError("invalid icon registry schema_version or fields")
    sources, icons = data["sources"], data["icons"]
    if not isinstance(sources, dict) or not isinstance(icons, dict) or set(icons) != set(declared):
        raise ValueError("icon registry must match source manifest icons exactly")
    def fields(value, names, optional=()):
        required = set(names.split())
        if not isinstance(value, dict) or not required <= set(value) or set(value) - required - set(optional):
            raise ValueError("invalid icon registry record fields")
    def text(value):
        if not isinstance(value, str) or not value.strip() or len(value) > 4096:
            raise ValueError("icon registry text must be nonempty and bounded")
    def url(value):
        text(value)
        if not re.fullmatch(r"https://[^\s/@:]+(?:/[^\s]*)?", value):
            raise ValueError("icon provenance URLs must be public HTTPS URLs without credentials")
    for source_id, source in sources.items():
        text(source_id)
        fields(source, "repository revision license license_url license_file attribution")
        for key, value in source.items():
            text(value)
        url(source["repository"])
        url(source["license_url"])
    used = set()
    for id, record in icons.items():
        fields(record, "path source_id source_url sha256 format width height trademark_owner usage", ("source_sha256", "source_path", "transformation"))
        if "transformation" in record:
            text(record["transformation"])
        source_path = record.get("source_path", "assets/icon-source.svg")
        if not isinstance(source_path, str) or not re.fullmatch(r"assets/icon-source\.(?:png|svg)", source_path):
            raise ValueError("icon source_path must identify the original PNG or SVG asset")
        original = root / "extensions" / id / source_path
        if original.exists() or original.is_symlink() or "source_sha256" in record or "source_path" in record:
            original = contained_file(root / "extensions" / id, source_path)
            if not 0 < original.stat().st_size <= MAX_BYTES:
                raise ValueError("source image must be nonempty and at most 2 MiB")
            original_data = original.read_bytes()
            if source_path.endswith(".svg"):
                svg_size(original_data, square=False)
            else:
                png_size(original_data, square=False)
            if "source_sha256" in record and record["source_sha256"] != hashlib.sha256(original_data).hexdigest():
                raise ValueError("icon registry source_sha256 does not match source image bytes")
        if record["source_id"] not in sources:
            raise ValueError("icon registry references unknown source_id")
        used.add(record["source_id"])
        url(record["source_url"])
        text(record["trademark_owner"])
        text(record["usage"])
        if record["path"] != declared[id]:
            raise ValueError("icon registry path differs from manifest")
        actual = validate_icon(root / "extensions" / id, record["path"])
        for key, value in actual.items():
            if key in ("width", "height") and (type(record[key]) not in (int, float) or not math.isfinite(record[key])):
                raise ValueError("icon registry dimensions must be finite numbers")
            if record[key] != value:
                raise ValueError("icon registry %s does not match image bytes" % key)
        license_file = sources[record["source_id"]]["license_file"]
        if not isinstance(license_file, str) or not re.fullmatch(r"assets/(?:[A-Za-z0-9_-][A-Za-z0-9._-]*/)*LICENSE(?:\.[A-Za-z0-9_-]+)?", license_file):
            raise ValueError("icon license_file must be a clean assets/ LICENSE path")
        license_path = contained_file(root / "extensions" / id, license_file)
        if not 0 < license_path.stat().st_size <= MAX_BYTES:
            raise ValueError("icon license file must be nonempty and bounded")
    if used != set(sources):
        raise ValueError("icon registry contains unused sources")
