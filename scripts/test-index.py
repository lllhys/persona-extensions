#!/usr/bin/env python3
"""Single catalog and raw-payload validation, entirely synthetic."""
import copy
import pathlib
import sys
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import catalog
import index
from test_support import CatalogFixture


class CatalogTests(CatalogFixture, unittest.TestCase):
    def test_valid_single_catalog(self):
        self.assertTrue(index.check(self.catalog_path))

    def test_duplicate_id_fails(self):
        self.body["extensions"].append(copy.deepcopy(self.entry))
        with self.assertRaisesRegex(ValueError, "duplicate"):
            catalog.validate_payload(self.body)

    def test_tags_in_manifest_fail(self):
        self.entry["manifest"]["tags"] = ["synthetic"]
        with self.assertRaisesRegex(ValueError, "tags"):
            catalog.validate_payload(self.body)

    def test_bad_archive_descriptors_fail(self):
        for values in ({"sha256": "x"}, {"size": True}, {"size": -1}, {"platforms": ["../../demo"]}, {"platform": "linux-amd64"}, {"platforms": ["linux-amd64", "darwin-arm64"]}, {"platforms": []}):
            body = copy.deepcopy(self.body)
            body["extensions"][0]["packages"][0].update(values)
            with self.assertRaises(ValueError):
                catalog.validate_payload(body)

    def test_duplicate_json_keys_fail(self):
        with self.assertRaisesRegex(ValueError, "duplicate JSON"):
            catalog.loads('{"payload":"a", "payload":"b"}')

    def test_non_json_constants_fail(self):
        for value in ("NaN", "Infinity", "-Infinity"):
            with self.assertRaisesRegex(ValueError, "non-JSON"):
                catalog.loads('{"value":' + value + '}')

    def test_plain_unsigned_catalog_fails(self):
        self.catalog_path.write_bytes(catalog.encode(self.body))
        self.assertFalse(index.check(self.catalog_path))

    def test_old_plugins_only_catalog_fails(self):
        body = dict(self.body)
        body["plugins"] = body.pop("extensions")
        with self.assertRaisesRegex(ValueError, "extensions"):
            catalog.validate_payload(body)

    def test_multiple_platform_packages_preserve_mapping(self):
        package = self.entry["packages"][0]
        self.entry["packages"] = [dict(package, platforms=["linux-amd64"]), dict(package, platforms=["darwin-arm64"])]
        self.assertEqual(catalog.validate_payload(self.body), self.body)
        self.entry["packages"].append(dict(package, platforms=["linux-amd64"]))
        with self.assertRaisesRegex(ValueError, "duplicate package"):
            catalog.validate_payload(self.body)


if __name__ == "__main__":
    unittest.main()
