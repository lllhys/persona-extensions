#!/usr/bin/env python3
"""Tests for scripts/index.py check: python3 -I scripts/test-index.py."""
import importlib.util
import json
import pathlib
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('index', pathlib.Path(__file__).with_name('index.py'))
index = importlib.util.module_from_spec(spec)
spec.loader.exec_module(index)


class CheckTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        d = pathlib.Path(self.dir.name)
        self.new_path, self.old_path = d / 'persona-extensions.json', d / 'persona-plugins.json'
        self.head = {"market_version": 1, "name": "market", "description": "synthetic fixtures"}
        self.entry = {
            "manifest": {"id": "demo", "version": "0.1.0", "name": "Demo"},
            "tags": ["public"],
            "packages": [{"url": "release:demo-v0.1.0/demo-0.1.0.tar.gz", "sha256": "x", "size": 1}],
        }
        self.old_new, self.old_old = index.NEW, index.OLD
        index.NEW, index.OLD = str(self.new_path), str(self.old_path)
        self.addCleanup(self.dir.cleanup)
        self.addCleanup(setattr, index, "NEW", self.old_new)
        self.addCleanup(setattr, index, "OLD", self.old_old)

    def write(self, new_entries, old_entries):
        self.new_path.write_text(json.dumps(dict(self.head, extensions=new_entries)))
        self.old_path.write_text(json.dumps(dict(self.head, plugins=old_entries)))

    def copy(self, e):
        return json.loads(json.dumps(e))

    def test_identical_entries_pass(self):
        self.write([self.entry], [self.copy(self.entry)])
        self.assertTrue(index.check())

    def test_key_order_does_not_matter(self):
        # sort_keys canonicalization: the same entry with its JSON keys shuffled is not drift.
        old = self.copy(self.entry)
        self.new_path.write_text(json.dumps(dict(self.head, extensions=[old])))
        # The plugins file carries the same entry but with its keys written in a different order.
        entry_text = '{"packages": %s, "tags": %s, "manifest": {"name": %s, "version": %s, "id": %s}}' % (
            json.dumps(old["packages"]),
            json.dumps(old["tags"]),
            json.dumps(old["manifest"]["name"]),
            json.dumps(old["manifest"]["version"]),
            json.dumps(old["manifest"]["id"]),
        )
        self.old_path.write_text(
            '{"market_version": 1, "name": "market", "description": "synthetic fixtures", "plugins": [%s]}' % entry_text
        )
        self.assertTrue(index.check())

    def test_a_tag_drift_is_rejected(self):
        # The entry tags hand-edited in only one file used to pass: check compared just id/version/packages.
        old = self.copy(self.entry)
        old["tags"] = ["private"]
        self.write([self.entry], [old])
        self.assertFalse(index.check())

    def test_a_manifest_drift_is_rejected(self):
        # A name (or permissions, runtime) changed in one manifest body must be caught too.
        old = self.copy(self.entry)
        old["manifest"]["name"] = "Drifted"
        self.write([self.entry], [old])
        self.assertFalse(index.check())

    def test_a_duplicate_id_is_rejected(self):
        # The same id twice in one index, even at different versions, must not ship.
        dup = self.copy(self.entry)
        dup["manifest"]["version"] = "0.2.0"
        self.write([self.entry, dup], [self.copy(self.entry), self.copy(dup)])
        self.assertFalse(index.check())


if __name__ == '__main__':
    unittest.main()
