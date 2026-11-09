#!/usr/bin/env python3
"""Publisher identity, cleaned archive provenance, saved-plan resume and public gates (synthetic only)."""
import argparse
import copy
import importlib.util
import os
import pathlib
import sys
import unittest
from unittest import mock

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import catalog
import oci
from test_support import CatalogFixture, archive, isolated_environ


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, pathlib.Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


publisher = load("publish_oci", "publish-oci.py")
fake_oci = load("test_oci", "test-oci.py")
SHA = "1" * 40


class PublisherTests(CatalogFixture, unittest.TestCase):
    def setUp(self):
        super().setUp()
        self.candidate = copy.deepcopy(self.entry)
        self.candidate["manifest"]["version"] = "2.0.0"
        self.entry["packages"][0]["url"] = "oci:ghcr.io/" + oci.repository("demo") + "@sha256:" + "1" * 64
        self.write_signed(self.body)
        for name, value in (("ids", ["demo"]), ("packaged", self.candidate["manifest"]), ("tags", self.candidate["tags"])):
            patch = mock.patch.object(publisher.manifests, name, return_value=value)
            patch.start()
            self.addCleanup(patch.stop)
        self.server = fake_oci.FakeRegistry()
        data = archive(self.candidate["manifest"])
        obj = oci.manifest("demo", "2.0.0", oci.digest(data), len(data))
        encoded = oci.manifest_bytes(obj)
        ref = oci.digest(encoded)
        self.server.blobs.update({oci.digest(data): data, oci.digest(oci.EMPTY_CONFIG): oci.EMPTY_CONFIG})
        self.server.manifests.update({"2.0.0": encoded, ref: encoded})
        self.candidate["packages"] = [{"url": oci.package_url("demo", ref), "sha256": oci.digest(data)[7:], "size": len(data)}]
        raw, _ = catalog.verified(self.catalog_path)
        self.plan = {"repository_id": publisher.CLEANED_REPOSITORY_ID, "head_sha": SHA, "mode": "versions",
                     "base_payload_sha256": oci.digest(raw), "entries": [self.candidate], "release_receipts": [],
                     "removed_ids": [], "catalog_update_required": True}

    def args(self, **changes):
        values = dict(repository=publisher.FINAL_REPOSITORY, repository_id=publisher.CLEANED_REPOSITORY_ID,
                      ref="refs/heads/main", head_sha=SHA, confirm_cutover=True, run_id="123")
        values.update(changes)
        return argparse.Namespace(**values)

    def test_cutover_identity_gate_before_api_or_credentials(self):
        for args in (self.args(repository="nwverse/persona-extensions-next"), self.args(repository_id=123),
                     self.args(ref="untrusted"), self.args(ref="main"), self.args(ref="refs/tags/main"),
                     self.args(confirm_cutover=False)):
            with mock.patch.object(publisher, "gh") as gh:
                with self.assertRaises(ValueError):
                    publisher.cutover_gate(args)
                gh.assert_not_called()

    def test_cutover_gate_requires_actual_remote_id(self):
        with mock.patch.object(publisher, "gh", return_value=catalog.encode({"full_name": publisher.FINAL_REPOSITORY, "id": 123})):
            with self.assertRaisesRegex(ValueError, "cutover"):
                publisher.cutover_gate(self.args())
        for ref in publisher.TRUSTED_BRANCHES:
            publisher.identity(publisher.FINAL_REPOSITORY, publisher.CLEANED_REPOSITORY_ID, "refs/heads/" + ref)

    def test_cleaned_release_checksum_mismatch_fails(self):
        package = {"sha256": oci.digest(b"cleaned")[7:], "size": 7}
        publisher.archive_matches(b"cleaned", package)
        for data in (b"former repository bytes", b"cleaned "):
            with self.assertRaisesRegex(ValueError, "checksum"):
                publisher.archive_matches(data, package)

    def test_release_metadata_mismatch_never_downloads(self):
        package = dict(self.entry["packages"][0], url="release:demo-v1.2.3/demo-1.2.3.tar.gz")
        remote = {"id": 1, "tag_name": "demo-v1.2.3", "assets": [{"id": 2, "name": "demo-1.2.3.tar.gz", "digest": "sha256:" + "2" * 64, "size": 3}]}
        with mock.patch.object(publisher, "gh", return_value=catalog.encode(remote)) as gh:
            with self.assertRaisesRegex(ValueError, "metadata"):
                publisher.cleaned_archive("synthetic/source", self.entry, package, self.directory)
            self.assertEqual(gh.call_count, 1)

    def test_cleaned_descriptive_drift_is_reported_not_rebuilt(self):
        manifest = dict(self.entry["manifest"], description="Scrubbed description")
        data = archive(manifest)
        self.assertEqual(publisher.check_archive(data, self.entry["manifest"]), ["description"])
        with self.assertRaisesRegex(ValueError, "new-version"):
            publisher.check_archive(data, self.entry["manifest"], strict=True)

    def test_archive_identity_and_missing_platform_binary_fail(self):
        with self.assertRaisesRegex(ValueError, "identity"):
            publisher.check_archive(archive(dict(self.entry["manifest"], version="9.0.0")), self.entry["manifest"])
        m = dict(self.entry["manifest"], runtime={"command": {"linux-amd64": "bin/demo-linux-amd64"}})
        with self.assertRaisesRegex(ValueError, "binary"):
            publisher.check_archive(archive(m), m)

    def test_saved_plan_anonymous_gate_outputs_digest_pinned_payload(self):
        calls = []
        def factory(path):
            calls.append(path)
            return oci.Registry(path, transport=self.server)
        with mock.patch.dict(os.environ, isolated_environ(GHCR_TOKEN="synthetic-unused", GH_TOKEN="synthetic-unused"), clear=True):
            result = catalog.loads(publisher.generate_payload(self.plan, self.catalog_path, SHA, factory))
        self.assertEqual(result["extensions"], [self.candidate])
        self.assertEqual(calls, [oci.repository("demo")])
        token_call = next(c for c in self.server.calls if "/token?" in c[1])
        self.assertNotIn("Authorization", token_call[2])

    def test_tags_only_platform_catalog_update_verifies_without_reupload(self):
        m = dict(self.entry["manifest"], runtime={"command": {"linux-amd64": "bin/linux", "darwin-arm64": "bin/mac"}})
        packages = []
        for platform, path in m["runtime"]["command"].items():
            data = archive(m, [(path, b"binary", 0o755)])
            obj = oci.manifest("demo", m["version"], oci.digest(data), len(data), platform)
            encoded = oci.manifest_bytes(obj)
            ref = oci.digest(encoded)
            self.server.blobs[oci.digest(data)] = data
            self.server.manifests[ref] = encoded
            packages.append({"url": oci.package_url("demo", ref), "sha256": oci.digest(data)[7:],
                             "size": len(data), "platforms": [platform]})
        self.entry.update(manifest=m, packages=packages)
        raw, _ = self.write_signed(self.body)
        changed = dict(copy.deepcopy(self.entry), tags=["new"])
        plan = dict(self.plan, base_payload_sha256=oci.digest(raw), entries=[changed])
        with mock.patch.object(publisher.manifests, "packaged", return_value=m), \
             mock.patch.object(publisher.manifests, "tags", return_value=["new"]):
            result = catalog.loads(publisher.generate_payload(plan, self.catalog_path, SHA,
                                                             lambda p: oci.Registry(p, transport=self.server)))
        self.assertEqual(result["extensions"], [changed])
        self.assertEqual(result["extensions"][0]["packages"], packages)
        self.assertEqual(result["generation"]["base_catalog_hash"], oci.digest(raw))
        self.assertEqual(result["generation"]["source_commit"], SHA)
        self.assertFalse(any(c[0] in ("POST", "PUT", "DELETE") for c in self.server.calls))

    def test_actual_remote_gate_rejects_wrong_checkout_before_credentials(self):
        remote = {"full_name": publisher.FINAL_REPOSITORY, "id": publisher.CLEANED_REPOSITORY_ID}
        with mock.patch.object(publisher, "gh", return_value=catalog.encode(remote)), \
             mock.patch.object(publisher.index, "git", return_value=("2" * 40).encode()):
            with self.assertRaisesRegex(ValueError, "checkout"):
                publisher.cutover_gate(self.args())

    def test_private_registry_blocks_payload_generation(self):
        self.server.failure = 403
        with self.assertRaisesRegex(ValueError, "403"):
            publisher.generate_payload(self.plan, self.catalog_path, SHA, lambda p: oci.Registry(p, transport=self.server))

    def test_partial_stale_or_foreign_plan_fails_before_registry(self):
        for change in ({"entries": []}, {"head_sha": "2" * 40}, {"repository_id": 123},
                       {"base_payload_sha256": "sha256:" + "2" * 64}, {"mode": "unknown"}):
            plan = dict(self.plan, **change)
            factory = mock.Mock()
            with self.assertRaises(ValueError):
                publisher.generate_payload(plan, self.catalog_path, SHA, factory)
            factory.assert_not_called()

    def test_plan_manifest_tampering_fails(self):
        plan = copy.deepcopy(self.plan)
        plan["entries"][0]["manifest"]["name"] = "Tampered"
        raw, body = catalog.verified(self.catalog_path)
        with self.assertRaisesRegex(ValueError, "manifest"):
            publisher.validate_plan(plan, raw, body, SHA)

    def test_existing_version_resumes_without_rebuilding(self):
        registry = oci.Registry(oci.repository("demo"), "synthetic", "synthetic", transport=self.server)
        package = publisher.reuse_version(registry, self.candidate)
        self.assertEqual(package, self.candidate["packages"][0])
        self.assertFalse(any(c[0] in ("POST", "PUT") for c in self.server.calls))

    def test_same_version_is_a_noop_preventing_catalog_pr_loops(self):
        self.body["extensions"] = [copy.deepcopy(self.candidate)]
        self.assertEqual(publisher.desired_entries(self.body, "versions"), [])
        self.body["extensions"][0]["manifest"]["name"] = "Unversioned change"
        with self.assertRaisesRegex(ValueError, "same-version"):
            publisher.desired_entries(self.body, "versions")

    def test_new_versions_cannot_publish_before_bootstrap_catalog(self):
        self.entry["packages"][0]["url"] = "release:demo-v1.2.3/demo-1.2.3.tar.gz"
        with self.assertRaisesRegex(ValueError, "bootstrap"):
            publisher.desired_entries(self.body, "versions")

    def test_bootstrap_requires_all_fourteen_any_packages(self):
        with self.assertRaisesRegex(ValueError, "14"):
            publisher.desired_entries(self.body, "bootstrap")
        entries = []
        for i in range(14):
            entry = copy.deepcopy(self.entry)
            entry["manifest"]["id"] = "demo-" + str(i)
            entries.append(entry)
        body = dict(self.body, extensions=entries)
        self.assertEqual(len(publisher.desired_entries(body, "bootstrap")), 14)
        body["extensions"][0]["packages"][0]["platforms"] = ["linux-amd64"]
        with self.assertRaisesRegex(ValueError, "any"):
            publisher.desired_entries(body, "bootstrap")

    def test_artifact_run_provenance_fail_closed(self):
        run = {"repository": {"id": publisher.CLEANED_REPOSITORY_ID}, "head_sha": SHA, "head_branch": "main",
               "conclusion": "success", "path": ".github/workflows/ci.yml", "event": "push"}
        with mock.patch.object(publisher, "cutover_gate"):
            with mock.patch.object(publisher, "gh", return_value=catalog.encode(run)):
                publisher.validate_run(self.args())
            for change in ({"event": "pull_request"}, {"head_branch": "untrusted"}, {"conclusion": "failure"},
                           {"head_sha": "2" * 40}, {"repository": {"id": 123}}, {"path": "other.yml"}):
                with mock.patch.object(publisher, "gh", return_value=catalog.encode(dict(run, **change))):
                    with self.assertRaises(ValueError):
                        publisher.validate_run(self.args())

    def test_workflow_and_ci_never_mutate_releases_or_default_branch(self):
        root = pathlib.Path(__file__).resolve().parent.parent
        text = "\n".join((root / p).read_text() for p in (".github/workflows/ci.yml", "scripts/ci.sh", "scripts/release.sh", "scripts/publish-oci.py"))
        for prohibited in ("gh release create", "gh release delete", "git tag ", "git push", "git rebase", "INDEX_DEPLOY_KEY", "::warning::no EXTENSION_MARKET_SIGNING_KEY"):
            self.assertNotIn(prohibited, text)
        self.assertIn("dedicated catalog-update issue", text)
        self.assertNotIn("PERSONA_OCI_CUTOVER_READY", text)
        self.assertIn("verified-unsigned-payload", text)
        self.assertIn("signed-oci-catalog", text)


if __name__ == "__main__":
    unittest.main()
