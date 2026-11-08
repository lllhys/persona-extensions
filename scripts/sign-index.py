#!/usr/bin/env python3
"""Verify/sign the embedded catalog, or convert a verified detached signature without a private key."""
import argparse
import base64
import os
import pathlib
import sys
import tempfile

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import catalog
import index


def convert_legacy(source, signature, public_key, output):
    payload = pathlib.Path(source).read_bytes()
    line = pathlib.Path(public_key).read_text().strip()
    if line != catalog.APPROVED_PUBLIC_KEY:
        raise ValueError("legacy public key differs from approved key")
    sig = catalog.loads(pathlib.Path(signature).read_bytes())
    catalog.verify_signature(payload, sig)
    body = catalog.validate_payload(catalog.loads(payload))
    if body.get("signing_key") != line:
        raise ValueError("legacy payload signing_key differs from approved key")
    wrapper = catalog.envelope(payload, [sig])
    pathlib.Path(output).write_bytes(catalog.encode(wrapper))
    catalog.verified(output)


def sign(payload_path, output, source_commit=None):
    payload = pathlib.Path(payload_path).read_bytes()
    body = catalog.validate_payload(catalog.loads(payload))
    generation = catalog.validate_generation(body.get("generation"))
    if source_commit is not None:
        if generation["source_commit"] != index.full_sha(source_commit):
            raise ValueError("payload source differs from trusted signing checkout")
        index.check_source(generation, source_commit, body)
    if body.get("signing_key") != catalog.APPROVED_PUBLIC_KEY:
        raise ValueError("payload signing_key differs from approved key")
    with tempfile.TemporaryDirectory() as directory:
        pem = os.environ.get("EXTENSION_MARKET_SIGNING_KEY", "")
        if not pem.strip():
            raise ValueError("EXTENSION_MARKET_SIGNING_KEY is required; refusing unsigned publication")
        key_file = pathlib.Path(directory) / "key.pem"
        fd = os.open(key_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as file:
            file.write(pem)
        der = catalog.openssl("pkey", "-in", key_file, "-pubout", "-outform", "DER")
        if der != catalog.SPKI_PREFIX + catalog.public_raw(catalog.APPROVED_PUBLIC_KEY):
            raise ValueError("private key does not match approved public key")
        sig = catalog.openssl("pkeyutl", "-sign", "-inkey", key_file, "-rawin", "-in", payload_path)
        signature = {"alg": "ed25519", "key_id": catalog.key_id(der[len(catalog.SPKI_PREFIX):]),
                     "sig": base64.b64encode(sig).decode("ascii")}
        catalog.verify_signature(payload, signature)
        pathlib.Path(output).write_bytes(catalog.encode(catalog.envelope(payload, [signature])))
        catalog.verified(output)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    verify = commands.add_parser("verify")
    verify.add_argument("--catalog", default=catalog.CATALOG)
    convert = commands.add_parser("convert-legacy")
    convert.add_argument("--catalog", default=catalog.CATALOG)
    convert.add_argument("--signature", default=str(catalog.CATALOG) + ".sig")
    convert.add_argument("--public-key", default=catalog.ROOT / "market.pub")
    convert.add_argument("--output", default=catalog.CATALOG)
    signing = commands.add_parser("sign")
    signing.add_argument("--payload", required=True)
    signing.add_argument("--output", required=True)
    signing.add_argument("--source-commit", required=True, help="Trusted Actions checkout SHA, not artifact metadata")
    args = parser.parse_args()
    if args.command == "verify":
        catalog.verified(args.catalog)
    elif args.command == "convert-legacy":
        convert_legacy(args.catalog, args.signature, args.public_key, args.output)
    else:
        sign(args.payload, args.output, args.source_commit)
    print("catalog: verified approved key " + catalog.APPROVED_KEY_ID)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, TypeError, KeyError) as error:
        sys.exit("catalog: failed: " + str(error))
