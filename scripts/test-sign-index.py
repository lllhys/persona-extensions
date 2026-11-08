#!/usr/bin/env python3
"""Embedded Ed25519 conversion, byte preservation and fail-closed signing tests."""
import importlib.util
import json
import os
import pathlib
import sys
import unittest
from unittest import mock

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import catalog
from test_support import CatalogFixture

spec = importlib.util.spec_from_file_location("sign_index", pathlib.Path(__file__).with_name("sign-index.py"))
sign_index = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sign_index)


class SigningTests(CatalogFixture, unittest.TestCase):
    def setUp(self):
        super().setUp()
        self.body["generation"] = {"repository": catalog.FINAL_REPOSITORY, "repository_id": catalog.CLEANED_REPOSITORY_ID,
                                   "base_catalog_hash": "sha256:" + "1" * 64, "source_commit": "1" * 40,
                                   "source_inventory_hash": "sha256:" + "2" * 64, "mode": "versions"}
        self.write_signed(self.body)

    def test_new_signature_requires_signed_provenance(self):
        self.body.pop("generation")
        self.payload_path.write_bytes(catalog.encode(self.body))
        with mock.patch.dict(os.environ, {"EXTENSION_MARKET_SIGNING_KEY": self.key.read_text()}, clear=True):
            with self.assertRaisesRegex(ValueError, "provenance"):
                sign_index.sign(self.payload_path, self.directory / "new.json")

    def test_provenance_cannot_be_changed_without_resigning(self):
        wrapper = catalog.loads(self.catalog_path.read_bytes())
        body = catalog.loads(wrapper["payload"])
        body["generation"]["base_catalog_hash"] = "sha256:" + "3" * 64
        wrapper["payload"] = catalog.encode(body).decode()
        self.catalog_path.write_bytes(catalog.encode(wrapper))
        with self.assertRaises(ValueError):
            catalog.verified(self.catalog_path)

    def test_untrusted_sidecar_provenance_is_rejected(self):
        wrapper = catalog.loads(self.catalog_path.read_bytes())
        wrapper["generation"] = self.body["generation"]
        self.catalog_path.write_bytes(catalog.encode(wrapper))
        with self.assertRaisesRegex(ValueError, "envelope"):
            catalog.verified(self.catalog_path)

    def test_raw_unicode_crlf_and_whitespace_survive_conversion(self):
        body = dict(self.body, name="Synthetic ☃")
        payload = json.dumps(body, ensure_ascii=False, indent=3).encode() + b"\r\n\n"
        signature = self.signature(payload)
        legacy = self.directory / "legacy.json"
        legacy.write_bytes(payload)
        sig_path = self.directory / "legacy.sig"
        sig_path.write_bytes(catalog.encode(signature))
        public = self.directory / "public"
        public.write_text(self.public)
        with mock.patch.dict(os.environ, {}, clear=True):
            sign_index.convert_legacy(legacy, sig_path, public, self.catalog_path)
        actual, result = catalog.verified(self.catalog_path)
        self.assertEqual(actual, payload)
        self.assertEqual(result, body)
        self.assertEqual(catalog.loads(self.catalog_path.read_bytes())["signatures"], [signature])

    def test_outer_json_format_is_not_signed(self):
        wrapper = catalog.loads(self.catalog_path.read_bytes())
        self.catalog_path.write_text(json.dumps(wrapper, indent=5, ensure_ascii=True))
        self.assertEqual(catalog.verified(self.catalog_path)[1], self.body)

    def test_payload_mutation_fails(self):
        wrapper = catalog.loads(self.catalog_path.read_bytes())
        wrapper["payload"] += " "
        self.catalog_path.write_bytes(catalog.encode(wrapper))
        with self.assertRaises(ValueError):
            catalog.verified(self.catalog_path)

    def test_missing_empty_and_wrong_signatures_fail(self):
        original = catalog.loads(self.catalog_path.read_bytes())
        for signatures in (None, [], [{}], [{"alg": "rsa"}], [original["signatures"][0]] * 2):
            wrapper = dict(original, signatures=signatures)
            self.catalog_path.write_bytes(catalog.encode(wrapper))
            with self.assertRaises((ValueError, TypeError)):
                catalog.verified(self.catalog_path)

    def test_wrong_key_and_bad_base64_fail(self):
        original = catalog.loads(self.catalog_path.read_bytes())
        for change in ({"key_id": "0" * 16}, {"sig": "bad!"}, {"sig": 42}, {"sig": "AA=="}):
            wrapper = dict(original, signatures=[dict(original["signatures"][0], **change)])
            self.catalog_path.write_bytes(catalog.encode(wrapper))
            with self.assertRaises((ValueError, TypeError)):
                catalog.verified(self.catalog_path)

    def test_without_secret_fails_without_output(self):
        output = self.directory / "new.json"
        with mock.patch.dict(os.environ, {}, clear=True):
            with self.assertRaisesRegex(ValueError, "required"):
                sign_index.sign(self.payload_path, output)
        self.assertFalse(output.exists())

    def test_wrong_private_key_fails_without_output(self):
        other = self.directory / "other.pem"
        catalog.openssl("genpkey", "-algorithm", "ed25519", "-out", other)
        output = self.directory / "new.json"
        with mock.patch.dict(os.environ, {"EXTENSION_MARKET_SIGNING_KEY": other.read_text()}, clear=True):
            with self.assertRaisesRegex(ValueError, "private key"):
                sign_index.sign(self.payload_path, output)
        self.assertFalse(output.exists())

    def test_signs_raw_payload_with_existing_synthetic_key(self):
        output = self.directory / "new.json"
        with mock.patch.dict(os.environ, {"EXTENSION_MARKET_SIGNING_KEY": self.key.read_text()}, clear=True):
            sign_index.sign(self.payload_path, output)
        self.assertEqual(catalog.verified(output)[0], self.payload_path.read_bytes())

    def test_actions_empty_secret_fails_closed(self):
        output = self.directory / "actions.json"
        for secret in ("", " \n\t"):
            with mock.patch.dict(os.environ, {"EXTENSION_MARKET_SIGNING_KEY": secret}, clear=True):
                with self.assertRaisesRegex(ValueError, "required"):
                    sign_index.sign(self.payload_path, output)
        self.assertFalse(output.exists())

    def test_legacy_bad_signature_does_not_replace_source(self):
        payload = self.payload_path.read_bytes()
        source = self.directory / "legacy.json"
        source.write_bytes(payload)
        sig_path = self.directory / "legacy.sig"
        sig_path.write_bytes(catalog.encode(dict(self.signature(payload), key_id="0" * 16)))
        public = self.directory / "public"
        public.write_text(self.public)
        with self.assertRaises(ValueError):
            sign_index.convert_legacy(source, sig_path, public, source)
        self.assertEqual(source.read_bytes(), payload)


if __name__ == "__main__":
    unittest.main()
