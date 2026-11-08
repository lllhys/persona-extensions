#!/usr/bin/env python3
"""Source-only predecessor metadata, signed inventory and immutable rename publication."""
import copy
import importlib.util
import pathlib
import sys
import unittest
from unittest import mock

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import catalog
import manifest as manifests
import oci
from test_support import CatalogFixture, archive


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, pathlib.Path(__file__).with_name(filename))
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


publisher = load("rename_publisher", "publish-oci.py")
fake_oci = load("rename_registry", "test-oci.py")


class RenameMetadataTests(unittest.TestCase):
    def source(self, **changes):
        return dict(manifest_version=1, id="new-name", version="2.0.0", name="Demo", tags=["synthetic"], **changes)

    def test_author_fields_are_projected_without_mutating_source(self):
        source = self.source(replaces="old-name")
        original = copy.deepcopy(source)
        projected = catalog.source_entry(source)
        with mock.patch.object(manifests, "load", return_value=source):
            packaged = manifests.packaged("new-name")
        self.assertEqual(source, original)
        self.assertEqual(projected["replaces"], "old-name")
        self.assertEqual(projected["tags"], ["synthetic"])
        self.assertEqual(packaged, projected["manifest"])
        self.assertNotIn("tags", packaged)
        self.assertNotIn("replaces", packaged)

    def test_absent_metadata_preserves_legacy_inventory_bytes(self):
        source = self.source()
        expected = {"manifest": {k: v for k, v in source.items() if k != "tags"}, "tags": source["tags"]}
        actual = catalog.source_entry(source)
        self.assertEqual(actual, expected)
        self.assertNotIn("replaces", actual)
        self.assertEqual(catalog.inventory_hash([actual]), catalog.inventory_hash([expected]))

    def test_invalid_self_and_ambiguous_predecessors_fail(self):
        for predecessor in (None, "", True, "../old", "new-name", "x" * 33):
            with self.subTest(predecessor=predecessor), self.assertRaises(ValueError):
                catalog.source_entry(self.source(replaces=predecessor))
        first = catalog.source_entry(self.source(replaces="old-name"))
        second = catalog.source_entry(dict(self.source(replaces="old-name"), id="other-name"))
        with self.assertRaisesRegex(ValueError, "ambiguous"):
            catalog.validate_entry_identities([first, second])

    def test_predecessor_cannot_remain_a_current_identity_or_form_a_cycle(self):
        new = catalog.source_entry(self.source(replaces="old-name"))
        old = catalog.source_entry(dict(self.source(), id="old-name"))
        with self.assertRaisesRegex(ValueError, "predecessor"):
            catalog.validate_entry_identities([old, new])
        old["replaces"] = "new-name"
        with self.assertRaisesRegex(ValueError, "cycle"):
            catalog.validate_entry_identities([old, new])


class RenamePublicationTests(CatalogFixture, unittest.TestCase):
    def setUp(self):
        super().setUp()
        self.entry["packages"][0]["url"] = "oci:ghcr.io/" + oci.repository("demo") + "@sha256:" + "1" * 64
        self.original, _ = self.write_signed(self.body)
        self.new = {"manifest": dict(self.entry["manifest"], id="renamed", version="2.0.0"),
                    "tags": ["synthetic"], "replaces": "demo", "packages": []}
        for name, value in (("ids", ["renamed"]), ("packaged", self.new["manifest"]),
                            ("tags", self.new["tags"]), ("replaces", "demo")):
            patch = mock.patch.object(publisher.manifests, name, return_value=value)
            patch.start()
            self.addCleanup(patch.stop)
        self.server = fake_oci.FakeRegistry()
        data = archive(self.new["manifest"])
        encoded = oci.manifest_bytes(oci.manifest("renamed", "2.0.0", oci.digest(data), len(data)))
        self.server.blobs.update({oci.digest(data): data, oci.digest(oci.EMPTY_CONFIG): oci.EMPTY_CONFIG})
        self.server.manifests[oci.digest(encoded)] = encoded
        self.new["packages"] = [{"url": oci.package_url("renamed", oci.digest(encoded)),
                                  "sha256": oci.digest(data)[7:], "size": len(data)}]
        self.plan = {"repository_id": catalog.CLEANED_REPOSITORY_ID, "head_sha": "1" * 40,
                     "mode": "versions", "base_payload_sha256": oci.digest(self.original),
                     "entries": [self.new], "removed_ids": ["demo"], "release_receipts": [],
                     "catalog_update_required": True}

    def test_new_identity_is_selected_with_signed_predecessor(self):
        expected = dict(copy.deepcopy(self.new), packages=[])
        self.assertEqual(publisher.desired_entries(self.body, "versions"), [expected])

    def test_anonymous_gate_generates_replacement_and_removes_old_catalog_entry(self):
        result = catalog.loads(publisher.generate_payload(self.plan, self.catalog_path, "1" * 40,
                                                         lambda path: oci.Registry(path, transport=self.server)))
        self.assertEqual(result["extensions"], [self.new])
        self.assertEqual(result["extensions"][0]["replaces"], "demo")
        self.assertNotIn("replaces", result["extensions"][0]["manifest"])
        self.assertEqual(result["generation"]["source_inventory_hash"],
                         catalog.inventory_hash([catalog.entry_inventory(self.new)]))
        self.assertFalse(any(call[0] in ("POST", "PUT", "DELETE") for call in self.server.calls))
        token = next(call for call in self.server.calls if "/token?" in call[1])
        self.assertNotIn("Authorization", token[2])

    def test_tampered_or_missing_predecessor_fails_before_registry(self):
        for replacement in (None, "other", ""):
            plan = copy.deepcopy(self.plan)
            if replacement is None:
                plan["entries"][0].pop("replaces")
            else:
                plan["entries"][0]["replaces"] = replacement
            factory = mock.Mock()
            with self.subTest(replacement=replacement), self.assertRaises(ValueError):
                publisher.generate_payload(plan, self.catalog_path, "1" * 40, factory)
            factory.assert_not_called()

    def test_predecessor_is_inside_the_signed_payload(self):
        body = dict(copy.deepcopy(self.body), extensions=[copy.deepcopy(self.new)])
        body["generation"] = {"repository": catalog.FINAL_REPOSITORY, "repository_id": catalog.CLEANED_REPOSITORY_ID,
                              "base_catalog_hash": oci.digest(self.original), "source_commit": "1" * 40,
                              "source_inventory_hash": catalog.inventory_hash([catalog.entry_inventory(self.new)]),
                              "mode": "versions"}
        self.write_signed(body)
        wrapper = catalog.loads(self.catalog_path.read_bytes())
        changed = catalog.loads(wrapper["payload"])
        changed["extensions"][0]["replaces"] = "another"
        wrapper["payload"] = catalog.encode(changed).decode()
        with self.assertRaises(ValueError):
            catalog.verified_data(catalog.encode(wrapper))

    def test_metadata_only_update_retains_immutable_packages(self):
        for name, value in (("ids", ["demo"]), ("packaged", self.entry["manifest"]),
                            ("tags", self.entry["tags"]), ("replaces", "legacy-name")):
            patch = mock.patch.object(publisher.manifests, name, return_value=value)
            patch.start()
            self.addCleanup(patch.stop)
        changed = publisher.desired_entries(self.body, "versions")
        self.assertEqual(changed, [dict(copy.deepcopy(self.entry), replaces="legacy-name")])
        self.assertEqual(changed[0]["packages"], self.entry["packages"])
        self.assertFalse(self.server.calls)


class CheckOnlyDispatchTests(unittest.TestCase):
    def test_check_dispatch_has_no_publication_and_reports_required_ci(self):
        text = (catalog.ROOT / ".github/workflows/ci.yml").read_text()
        self.assertIn("options: [check, packages, catalog]", text)
        self.assertIn("default: check", text)
        gate = text.split('if [ "$GITHUB_EVENT_NAME" = workflow_dispatch ] && [ "$PHASE" = check ]; then', 1)[1].split("elif ", 1)[0]
        self.assertIn("publish=false", gate)
        self.assertNotIn("--confirm-cutover", gate)
        self.assertNotIn("GHCR_TOKEN", gate)
        self.assertIn("inputs.phase == 'check'", text.split("ci-required:", 1)[1].split("packages:", 1)[0])


if __name__ == "__main__":
    unittest.main()
