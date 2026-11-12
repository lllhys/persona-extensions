"""Synthetic, ephemeral signing and archive fixtures; never use an official private key."""
import base64
import io
import os
import pathlib
import tarfile
import tempfile
from unittest import mock

import catalog
import manifest as manifests


class CatalogFixture:
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = pathlib.Path(self.temp.name)
        # Synthetic publisher fixtures mock the source ID/manifest/tags; they
        # have no on-disk source manifest or predecessor unless a test sets one.
        predecessor = mock.patch.object(manifests, "replaces", return_value="")
        predecessor.start()
        self.addCleanup(predecessor.stop)
        icon = mock.patch.object(manifests, "icon", return_value="")
        icon.start()
        self.addCleanup(icon.stop)
        self.key = self.directory / "synthetic.pem"
        catalog.openssl("genpkey", "-algorithm", "ed25519", "-out", self.key)
        der = catalog.openssl("pkey", "-in", self.key, "-pubout", "-outform", "DER")
        self.raw = der[len(catalog.SPKI_PREFIX):]
        self.public = "ed25519:" + base64.b64encode(self.raw).decode()
        for name, value in (("APPROVED_PUBLIC_KEY", self.public), ("APPROVED_KEY_ID", catalog.key_id(self.raw))):
            patch = mock.patch.object(catalog, name, value)
            patch.start()
            self.addCleanup(patch.stop)
        self.entry = {"manifest": {"manifest_version": 1, "id": "demo", "version": "1.2.3", "name": "Demo"},
                      "tags": ["synthetic"], "packages": [{"url": "release:demo-v1.2.3/demo-1.2.3.tar.gz",
                                                            "sha256": "1" * 64, "size": 3}]}
        self.body = {"market_version": 1, "name": "Synthetic market", "description": "Synthetic fixture",
                     "signing_key": self.public, "extensions": [self.entry]}
        self.catalog_path = self.directory / "catalog.json"
        self.payload_path = self.directory / "payload.json"
        self.write_signed(self.body)

    def signature(self, payload):
        self.payload_path.write_bytes(payload)
        raw = catalog.openssl("pkeyutl", "-sign", "-inkey", self.key, "-rawin", "-in", self.payload_path)
        return {"alg": "ed25519", "key_id": catalog.key_id(self.raw), "sig": base64.b64encode(raw).decode()}

    def write_signed(self, body, raw_payload=None):
        payload = raw_payload if raw_payload is not None else catalog.encode(body)
        sig = self.signature(payload)
        self.catalog_path.write_bytes(catalog.encode(catalog.envelope(payload, [sig])))
        return payload, sig


def isolated_environ(**values):
    """The environment for a test that must not see the caller's variables. PATH stays: without it Python looks up
    openssl in /bin:/usr/bin, which on macOS is LibreSSL without Ed25519, not the OpenSSL the fixtures used."""
    return {"PATH": os.environ.get("PATH", os.defpath), **values}


def archive(manifest, files=()):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:gz") as file:
        for path, data, mode in [("persona-extension.json", catalog.encode(manifest), 0o644), *files]:
            info = tarfile.TarInfo(manifest["id"] + "/" + path)
            info.size, info.mode = len(data), mode
            file.addfile(info, io.BytesIO(data))
    return output.getvalue()
