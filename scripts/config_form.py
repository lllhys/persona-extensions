#!/usr/bin/env python3
"""The configuration a hub accepts: the form subset every app draws (the hub's "Configuration: the form subset").

A hub reads a manifest's `configuration` with its own parser (nwverse/persona server/internal/extension/schema.go
ParseSchema, and checkConfigured in configured.go) and refuses the whole manifest when anything is outside the subset;
the catalog then drops that entry with a warning, so an extension published here would be missing from every
marketplace (#494). check() mirrors those rules for scripts/manifest.py check, which CI runs before any publication.

Mirrored on purpose, not imported: the hub's parser is Go in another repository. When the hub widens the subset, widen
it here in the same change that starts using it.
"""
import json, re
from urllib.parse import urlsplit

ROOT_KEYS = ("$schema", "type", "properties", "required", "title", "description")
DESCRIBING = ("title", "description", "default", "x-confirm")
KEYS_FOR = {
    "string": ("enum", "oneOf", "format", "x-os", "pattern", "minLength", "maxLength"),
    "integer": ("enum", "oneOf", "minimum", "maximum"),
    "number": ("enum", "oneOf", "minimum", "maximum"),
    "boolean": (),
    "array": ("items", "minItems", "maxItems", "uniqueItems"),
    "object": ("properties", "required"),
}
FORMATS = ("uri", "email", "hostname", "machine", "launcher")
SYSTEMS = ("darwin", "linux", "windows")
FIELD_NAME = re.compile(r"[A-Za-z_][A-Za-z0-9_-]{0,63}")
HOSTNAME = re.compile(r"(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?")
EMAIL = re.compile(r"[^@\s<>()\[\],;:\"]+@[^@\s<>()\[\],;:\"]+")
CONFIG_NETWORK = "config:"
SECRET_HINTS = ("", "last4")


class Refused(ValueError):
    """The hub refuses the configuration; the message is where and why, as the hub says it."""


def check(manifest):
    """Raise Refused when a hub would refuse manifest's configuration (or what refers to it)."""
    fields = parse(manifest.get("configuration"))
    check_configured(manifest, fields or [])


def parse(schema):
    """The top-level fields of a configuration schema ([] for none), each a dict with its name and rules."""
    if schema is None or schema == {}:
        return []
    if not isinstance(schema, dict):
        raise Refused("configuration: an object")
    for k in schema:
        if k not in ROOT_KEYS:
            raise Refused("configuration: %r is not in the subset the forms draw" % k)
    if schema.get("type") != "object":
        raise Refused('configuration: the root is {"type": "object", "properties": {…}}')
    return _properties(schema, "", 0)


def _properties(obj, at, depth):
    props = obj.get("properties")
    if not isinstance(props, dict):
        raise Refused("configuration: %sproperties: %s" % (at, "missing" if props is None else "an object"))
    required = obj.get("required", [])
    if not isinstance(required, list) or not all(isinstance(r, str) for r in required):
        raise Refused("configuration: %srequired: a list of names" % at)
    for r in required:
        if r not in props:
            raise Refused("configuration: %srequired: %r is not a property" % (at, r))
    out = []
    for name, raw in props.items():
        f = _field(name, raw, at + name + ".", depth)
        f["required"] = name in required
        out.append(f)
    return out


def _fail(at, message):
    return Refused("configuration: %s: %s" % (at.rstrip("."), message))


def _count(m, k, at):
    if k not in m:
        return None
    v = m[k]
    if isinstance(v, bool) or not isinstance(v, int) or v < 0:
        raise _fail(at, "%s: a whole number, 0 or more" % k)
    return v


def _number(m, k, at):
    if k not in m:
        return None
    v = m[k]
    if isinstance(v, bool) or not isinstance(v, (int, float)):
        raise _fail(at, "%s: a number" % k)
    return v


def _field(name, raw, at, depth):
    if not FIELD_NAME.fullmatch(name):
        raise _fail(at, "not a usable property name")
    if not isinstance(raw, dict):
        raise _fail(at, "an object")
    t = raw.get("type")
    if t not in KEYS_FOR:
        raise _fail(at, "type %r: string, integer, number, boolean, array or object" % (t if isinstance(t, str) else ""))
    for k in raw:
        if k != "type" and k not in DESCRIBING and k not in KEYS_FOR[t]:
            raise _fail(at, "%r is not in the subset the forms draw for a %s" % (k, t))
    f = {"name": name, "type": t}
    for k in ("title", "description", "format", "pattern", "x-confirm"):
        if k in raw and not isinstance(raw[k], str):
            raise _fail(at, "%s: a string" % k)
    for k in ("minLength", "maxLength", "minItems", "maxItems"):
        f[k] = _count(raw, k, at)
    for k in ("minimum", "maximum"):
        f[k] = _number(raw, k, at)
    if "uniqueItems" in raw and not isinstance(raw["uniqueItems"], bool):
        raise _fail(at, "uniqueItems: true or false")
    f["uniqueItems"] = raw.get("uniqueItems", False)
    f["format"] = raw.get("format", "")
    if f["format"] and f["format"] not in FORMATS:
        raise _fail(at, "format %r: uri, email, hostname, machine or launcher" % f["format"])
    if "x-os" in raw:
        if f["format"] != "machine":
            raise _fail(at, "x-os: only with format machine")
        os_ = raw["x-os"]
        if not isinstance(os_, list) or not os_ or any(o not in SYSTEMS for o in os_):
            raise _fail(at, "x-os: a list of darwin, linux, windows")
    f["pattern"] = None
    if raw.get("pattern"):
        try:
            f["pattern"] = re.compile(raw["pattern"])
        except re.error:
            # Go's RE2 and Python's re differ at the edges: leave a pattern only Go reads to the hub.
            f["pattern"] = None
    f["enum"] = None
    if "enum" in raw:
        if not isinstance(raw["enum"], list) or not raw["enum"]:
            raise _fail(at, "enum: a list of values")
        f["enum"] = list(raw["enum"])
    if "oneOf" in raw:
        if f["enum"] is not None:
            raise _fail(at, "enum and oneOf: one of them")
        choices = raw["oneOf"]
        if not isinstance(choices, list) or not choices:
            raise _fail(at, 'oneOf: only [{"const": …, "title": "…"}] (an enum with labels)')
        f["enum"] = []
        for c in choices:
            if not isinstance(c, dict) or "const" not in c or len(c) > 2 or (len(c) == 2 and not isinstance(c.get("title"), str)):
                raise _fail(at, 'oneOf: only [{"const": …, "title": "…"}] (an enum with labels)')
            f["enum"].append(c["const"])
    if t == "array":
        if "items" not in raw:
            raise _fail(at, "items: required")
        item = _field("items", raw["items"], at + "items.", depth + 1)
        if item["type"] != "string":
            raise _fail(at, "items: only strings (a list of objects is not in the subset)")
        f["items"] = item
    elif t == "object":
        if depth > 0:
            raise _fail(at, "an object only at the first level")
        f["fields"] = _properties(raw, at, depth + 1)
        for sub in f["fields"]:
            if sub["type"] == "object":
                raise _fail(at, "an object only at the first level")
    for v in f["enum"] or []:
        why = value_error(f, v)
        if why:
            raise _fail(at, "enum value %s: %s" % (json.dumps(v, ensure_ascii=False), why))
    if "default" in raw:
        why = value_error(f, raw["default"])
        if why:
            raise _fail(at, "default: %s" % why)
        f["default"] = raw["default"]
    return f


def value_error(f, v):
    """Why a value does not fit field f ("" when it does): the hub's Field.check."""
    t = f["type"]
    if t == "object":
        if not isinstance(v, dict):
            return "not an object"
        names = [s["name"] for s in f["fields"]]
        for k in v:
            if k not in names:
                return "%s: not a field" % k
        for s in f["fields"]:
            if s["name"] not in v or v[s["name"]] is None:
                if s["required"] and "default" not in s:
                    return "%s: required" % s["name"]
                continue
            why = value_error(s, v[s["name"]])
            if why:
                return "%s: %s" % (s["name"], why)
        return ""
    if t == "array":
        if not isinstance(v, list):
            return "not a list"
        if f["minItems"] is not None and len(v) < f["minItems"]:
            return "fewer than %d items" % f["minItems"]
        if f["maxItems"] is not None and len(v) > f["maxItems"]:
            return "more than %d items" % f["maxItems"]
        seen = []
        for i, item in enumerate(v):
            why = value_error(f["items"], item)
            if why:
                return "item %d: %s" % (i + 1, why)
            if f["uniqueItems"] and item in seen:
                return "item %d is a duplicate" % (i + 1)
            seen.append(item)
        return ""
    if t == "boolean":
        return "" if isinstance(v, bool) else "not true or false"
    if t in ("integer", "number"):
        if isinstance(v, bool) or not isinstance(v, (int, float)):
            return "not a number"
        if t == "integer" and v != int(v):
            return "not a whole number"
        if f["minimum"] is not None and v < f["minimum"]:
            return "below %s" % f["minimum"]
        if f["maximum"] is not None and v > f["maximum"]:
            return "above %s" % f["maximum"]
    if t == "string":
        if not isinstance(v, str):
            return "not a string"
        if f["minLength"] is not None and len(v) < f["minLength"]:
            return "shorter than %d characters" % f["minLength"]
        if f["maxLength"] is not None and len(v) > f["maxLength"]:
            return "longer than %d characters" % f["maxLength"]
        if f["pattern"] is not None and not f["pattern"].search(v):
            return "does not match its pattern"
        fmt = f["format"]
        if fmt == "uri":
            u = urlsplit(v)
            if not u.scheme or not u.netloc:
                return "not a URI"
        elif fmt == "email" and not EMAIL.fullmatch(v):
            return "not an email address"
        elif fmt == "hostname" and not HOSTNAME.fullmatch(v):
            return "not a host name"
        elif fmt in ("machine", "launcher") and (not v or v.strip() != v):
            return "not a %s name" % fmt
    if f.get("enum") is not None and v not in f["enum"]:
        return "not one of its choices"
    return ""


def check_configured(manifest, fields):
    """The hub's checkConfigured: a "config:<field>" network entry names a top-level string field of format uri with no
    default (only the user writes the address it reaches); a secret's hint is one the hub knows."""
    network = ((manifest.get("permissions") or {}).get("network")) or []
    by_name = {f["name"]: f for f in fields}
    for entry in network:
        if not isinstance(entry, str) or not entry.startswith(CONFIG_NETWORK):
            continue
        rest = entry[len(CONFIG_NETWORK):]
        field = rest[:-len(":http")] if rest.endswith(":http") else rest
        f = by_name.get(field)
        if not field or ":" in field:
            raise Refused("permissions.network: %r: config:<field> or config:<field>:http" % entry)
        if f is None:
            raise Refused("permissions.network: configuration has no field %r" % field)
        if f["type"] != "string" or f["format"] != "uri":
            raise Refused("permissions.network: configuration.%s: the address it reaches is a string of format uri" % field)
        if "default" in f:
            raise Refused("permissions.network: configuration.%s: the address it reaches has a default, but only the user writes it" % field)
    for s in manifest.get("secrets") or []:
        hint = s.get("hint", "") if isinstance(s, dict) else ""
        if hint not in SECRET_HINTS:
            raise Refused("secrets.%s.hint: %r (only %r)" % (s.get("name"), hint, "last4"))
