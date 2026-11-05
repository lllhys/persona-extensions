#!/usr/bin/env python3
"""The market's signature (nwverse/persona#1848): plain ed25519 over the index files' bytes, needs only openssl.

  scripts/sign-index.py [--key-file <pem>]   sign persona-extensions.json and persona-plugins.json
  scripts/sign-index.py verify               check every index that has a .sig against market.pub (what CI runs)

The key is the PEM in $EXTENSION_MARKET_SIGNING_KEY (the repository's Actions secret) or --key-file. With no key it
prints "unsigned" and exits 0 (local work is not blocked), but says so when a .sig left behind no longer matches.

Files (the hub reads them the same way):
  market.pub                  one line: ed25519:<the 32-byte public key, base64>
  persona-extensions.json     its top-level "signing_key" is that same line
  <index>.sig                 {"alg": "ed25519", "key_id": "<sha256 of the 32-byte key, first 16 hex>",
                               "sig": "<ed25519 signature of the index file's raw bytes, base64>"}
"""
import base64, binascii, hashlib, json, os, subprocess, sys, tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import index  # noqa: E402

ROOT = index.ROOT
PUB = os.path.join(ROOT, "market.pub")
INDEXES = ((index.NEW, "extensions"), (index.OLD, "plugins"))
# SubjectPublicKeyInfo of an ed25519 key, before its 32 bytes (RFC 8410).
SPKI_PREFIX = bytes.fromhex("302a300506032b6570032100")


def openssl(*args):
    r = subprocess.run(["openssl", *args], capture_output=True)
    if r.returncode != 0:
        raise SystemExit("openssl %s: %s" % (args[0], r.stderr.decode(errors="replace").strip()))
    return r.stdout


def market_pub():
    line = open(PUB).read().strip()
    if not line.startswith("ed25519:"):
        raise SystemExit("market.pub: not ed25519:<base64>")
    raw = base64.b64decode(line.removeprefix("ed25519:"), validate=True)
    if len(raw) != 32:
        raise SystemExit("market.pub: the key is not 32 bytes")
    return line, raw


def key_id(raw):
    return hashlib.sha256(raw).hexdigest()[:16]


def verify_one(path, raw, tmp):
    """None if path's .sig is good, else why not."""
    sig_path = path + ".sig"
    try:
        s = json.load(open(sig_path))
    except (OSError, ValueError) as e:
        return "%s: unreadable (%s)" % (os.path.basename(sig_path), e)
    if s.get("alg") != "ed25519":
        return "%s: alg is not ed25519" % os.path.basename(sig_path)
    if s.get("key_id") != key_id(raw):
        return "%s: signed by key %s, market.pub is %s" % (os.path.basename(sig_path), s.get("key_id"), key_id(raw))
    pem = os.path.join(tmp, "pub.pem")
    with open(pem, "wb") as f:
        f.write(b"-----BEGIN PUBLIC KEY-----\n" + base64.encodebytes(SPKI_PREFIX + raw) + b"-----END PUBLIC KEY-----\n")
    sig = os.path.join(tmp, "sig.raw")
    try:
        sig_bytes = base64.b64decode(s.get("sig", ""), validate=True)
    except (binascii.Error, TypeError, ValueError) as e:
        return "%s: malformed signature (%s)" % (os.path.basename(sig_path), e)
    with open(sig, "wb") as f:
        f.write(sig_bytes)
    r = subprocess.run(["openssl", "pkeyutl", "-verify", "-pubin", "-inkey", pem, "-rawin", "-in", path, "-sigfile", sig],
                       capture_output=True)
    return None if r.returncode == 0 else "%s: the signature does not match the index" % os.path.basename(sig_path)


def verify():
    line, raw = market_pub()
    bad = []
    with tempfile.TemporaryDirectory() as tmp:
        for path, key in INDEXES:
            if not os.path.exists(path + ".sig"):
                continue
            try:
                signing_key = json.load(open(path)).get("signing_key")
            except ValueError as e:
                bad.append("%s: malformed index (%s)" % (os.path.basename(path), e))
            else:
                if signing_key != line:
                    bad.append("%s: signing_key is not market.pub" % os.path.basename(path))
            why = verify_one(path, raw, tmp)
            if why:
                bad.append(why)
            else:
                print("%s: signed by %s" % (os.path.basename(path), key_id(raw)))
    for b in bad:
        print(b, file=sys.stderr)
    return not bad


def stamp_signing_key(line):
    """Both indexes carry market.pub as signing_key (written the way index.py writes them)."""
    for path, key in INDEXES:
        d = index.read(path, key)
        if d is not None and d.get("signing_key") != line:
            d["signing_key"] = line
            index.write(path, d)


def sign(key_file):
    line, raw = market_pub()
    with tempfile.TemporaryDirectory() as tmp:
        if key_file is None:
            pem = os.environ.get("EXTENSION_MARKET_SIGNING_KEY", "")
            if not pem.strip():
                print("unsigned")
                stale = [why for path, _ in INDEXES if os.path.exists(path + ".sig")
                         for why in [verify_one(path, raw, tmp)] if why]
                for why in stale:
                    print("warning: %s; sign with the market's key, or hubs that trust it refuse this index" % why,
                          file=sys.stderr)
                return True
            key_file = os.path.join(tmp, "key.pem")
            fd = os.open(key_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "w") as f:
                f.write(pem if pem.endswith("\n") else pem + "\n")
        der = openssl("pkey", "-in", key_file, "-pubout", "-outform", "DER")
        if der[:len(SPKI_PREFIX)] != SPKI_PREFIX or der[len(SPKI_PREFIX):] != raw:
            raise SystemExit("the signing key is not the one in market.pub")
        stamp_signing_key(line)
        for path, _ in INDEXES:
            if not os.path.exists(path):
                continue
            sig = openssl("pkeyutl", "-sign", "-inkey", key_file, "-rawin", "-in", path)
            with open(path + ".sig", "w") as f:
                json.dump({"alg": "ed25519", "key_id": key_id(raw), "sig": base64.b64encode(sig).decode()}, f)
                f.write("\n")
            why = verify_one(path, raw, tmp)
            if why:
                raise SystemExit(why)
            print("%s: signed by %s" % (os.path.basename(path), key_id(raw)))
    return True


if __name__ == "__main__":
    args = sys.argv[1:]
    if args == ["verify"]:
        sys.exit(0 if verify() else 1)
    if args[:1] == ["--key-file"] and len(args) == 2:
        sys.exit(0 if sign(args[1]) else 1)
    if not args:
        sys.exit(0 if sign(None) else 1)
    sys.exit(__doc__)
