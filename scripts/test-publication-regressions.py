#!/usr/bin/env python3
"""Regression cases for publication continuity, inventory and installable archives."""
import argparse
import copy
import importlib.util
import io
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import tarfile
import unittest
from unittest import mock

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import catalog
import index
import oci
from test_support import CatalogFixture

spec = importlib.util.spec_from_file_location("publisher_regressions", pathlib.Path(__file__).with_name("publish-oci.py"))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)


def package(m, files=(), extra=()):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:gz") as archive:
        for name, data, mode in [("persona-extension.json", catalog.encode(m), 0o644), *files]:
            member = tarfile.TarInfo(m["id"] + "/" + name)
            member.size, member.mode = len(data), mode
            archive.addfile(member, io.BytesIO(data))
        for member, data in extra:
            archive.addfile(member, io.BytesIO(data))
    return output.getvalue()


class ArchiveRegressions(unittest.TestCase):
    def setUp(self):
        self.m = {"manifest_version": 1, "id": "demo", "version": "1.2.3", "name": "Demo"}

    def test_binary_must_be_executable(self):
        self.m["runtime"] = {"command": {"linux-amd64": "bin/demo"}}
        with self.assertRaisesRegex(ValueError, "executable"):
            publisher.check_archive(package(self.m, [("bin/demo", b"binary", 0o644)]), self.m)

    def test_referenced_files_must_exist(self):
        for contribution in ({"instructions": "assistant/style.md"}, {"skills": ["skills/demo"]},
                             {"gate": "assistant/gate.json"}):
            m = dict(self.m, contributes={"assistant": [dict(id="demo", **contribution)]})
            with self.subTest(contribution=contribution), self.assertRaisesRegex(ValueError, "missing"):
                publisher.check_archive(package(m), m)

    def test_path_and_nonordinary_files_are_rejected(self):
        for name, kind in (("demo/../escape", tarfile.REGTYPE), ("demo/link", tarfile.SYMTYPE),
                           ("demo/pipe", tarfile.FIFOTYPE), ("other/file", tarfile.REGTYPE)):
            member = tarfile.TarInfo(name)
            member.type, member.linkname = kind, "persona-extension.json"
            with self.subTest(name=name), self.assertRaises(ValueError):
                publisher.check_archive(package(self.m, extra=[(member, b"")]), self.m)

    def test_file_count_limit_includes_manifest(self):
        files = [("data/" + str(i), b"", 0o644) for i in range(20000)]
        with self.assertRaisesRegex(ValueError, "files"):
            publisher.check_archive(package(self.m, files), self.m)

    def test_oversized_regular_member_rejected_before_reading_body(self):
        member = tarfile.TarInfo("demo/large")
        member.size = 201 * 1024 * 1024
        # A tiny gzip with a huge declared member, not a large resource-consuming fixture.
        import gzip
        data = gzip.compress(tarfile.TarInfo("demo/").tobuf() + member.tobuf() + b"\0" * 10240)
        with self.assertRaises(ValueError):
            publisher.check_archive(data, self.m)

    def test_small_compressed_bomb_and_header_flood_are_bounded(self):
        import gzip
        # Use a small injected scan budget; production is bounded even for directories/PAX/padding.
        with mock.patch.object(publisher, "MAX_TAR_BYTES", 2048):
            with self.assertRaisesRegex(ValueError, "scan budget"):
                publisher.check_archive(gzip.compress(b"\0" * 10000), self.m)

    def test_manifest_limit_matches_hub_not_old_one_megabyte_limit(self):
        m = dict(self.m, description="x" * (256 * 1024))
        with self.assertRaisesRegex(ValueError, "manifest"):
            publisher.check_archive(package(m), m)

    def test_archive_scanner_never_uses_unbounded_getmembers(self):
        with mock.patch.object(tarfile.TarFile, "getmembers", side_effect=AssertionError("unbounded scan")):
            publisher.check_archive(package(self.m), self.m)

    def test_explicit_platform_does_not_require_other_runtime_binary(self):
        self.m["runtime"] = {"command": {"linux-amd64": "bin/linux", "darwin-arm64": "bin/mac"}}
        data = package(self.m, [("bin/linux", b"binary", 0o755)])
        publisher.check_archive(data, self.m, platform="linux-amd64")
        with self.assertRaisesRegex(ValueError, "binary"):
            publisher.check_archive(data, self.m, platform="any")
        with self.assertRaisesRegex(ValueError, "platform"):
            publisher.check_archive(data, self.m, platform="windows-amd64")

    def test_gate_reference_does_not_require_a_json_filename_suffix(self):
        self.m["contributes"] = {"assistant": [{"id": "demo", "skills": ["skills/demo"], "gate": "assistant/gate"}]}
        files = [("skills/demo/SKILL.md", b"skill", 0o644), ("skills/demo/run.py", b"script", 0o644),
                 ("assistant/gate", catalog.encode({"command": "demo", "title": "Demo", "script": "demo/run.py"}), 0o644)]
        publisher.check_archive(package(self.m, files), self.m)

    def test_gate_script_must_be_a_safe_existing_skill_file(self):
        self.m["contributes"] = {"assistant": [{"id": "demo", "skills": ["skills/demo"], "gate": "assistant/gate.json"}]}
        files = [("skills/demo/SKILL.md", b"skill", 0o644),
                 ("assistant/gate.json", catalog.encode({"command": "demo", "title": "Demo", "script": "demo/missing.py"}), 0o644)]
        with self.assertRaisesRegex(ValueError, "missing"):
            publisher.check_archive(package(self.m, files), self.m)
        files.append(("skills/demo/missing.py", b"script", 0o644))
        publisher.check_archive(package(self.m, files), self.m)


class InventoryRegressions(CatalogFixture, unittest.TestCase):
    def test_tags_only_change_reuses_original_package(self):
        self.entry["packages"][0]["url"] = "oci:ghcr.io/" + oci.repository("demo") + "@sha256:" + "1" * 64
        with mock.patch.object(publisher.manifests, "ids", return_value=["demo"]), \
             mock.patch.object(publisher.manifests, "packaged", return_value=self.entry["manifest"]), \
             mock.patch.object(publisher.manifests, "tags", return_value=["new"]):
            result = publisher.desired_entries(self.body, "versions")
        self.assertEqual(result, [dict(self.entry, tags=["new"])])

    def test_deleted_inventory_id_is_removed_without_registry_history_writes(self):
        self.entry["packages"][0]["url"] = "oci:ghcr.io/" + oci.repository("demo") + "@sha256:" + "1" * 64
        raw, _ = self.write_signed(self.body)
        plan = {"repository_id": publisher.CLEANED_REPOSITORY_ID, "head_sha": "1" * 40, "mode": "versions",
                "base_payload_sha256": oci.digest(raw), "entries": [], "release_receipts": [],
                "removed_ids": ["demo"], "catalog_update_required": True}
        with mock.patch.object(publisher.manifests, "ids", return_value=[]):
            factory = mock.Mock()
            result = catalog.loads(publisher.generate_payload(plan, self.catalog_path, "1" * 40, factory))
        self.assertEqual(result["extensions"], [])
        factory.assert_not_called()

    def test_deletion_push_emits_explicit_catalog_update_without_package_upload(self):
        import argparse
        self.entry["packages"][0]["url"] = "oci:ghcr.io/" + oci.repository("demo") + "@sha256:" + "1" * 64
        self.write_signed(self.body)
        output = self.directory / "plan"
        args = argparse.Namespace(catalog=self.catalog_path, mode="versions", output=output, head_sha="1" * 40)
        with mock.patch.object(publisher, "cutover_gate"), mock.patch.object(publisher.manifests, "ids", return_value=[]), \
             mock.patch.object(publisher.oci, "Registry") as registry, \
             mock.patch.dict(os.environ, {"GHCR_TOKEN": "synthetic", "GITHUB_ACTOR": "fixture"}, clear=True):
            publisher.push(args)
        registry.assert_not_called()
        plan = catalog.loads((output / "plan.json").read_bytes())
        self.assertEqual(plan["removed_ids"], ["demo"])
        self.assertIs(plan["catalog_update_required"], True)
        self.assertEqual(plan["entries"], [])
        with mock.patch.object(publisher.manifests, "ids", return_value=[]):
            factory = mock.Mock()
            plan["removed_ids"] = []
            with self.assertRaisesRegex(ValueError, "deletion"):
                publisher.generate_payload(plan, self.catalog_path, "1" * 40, factory)
            factory.assert_not_called()

    def test_changed_payload_requires_signed_base_provenance(self):
        base = self.directory / "base.json"
        base.write_bytes(self.catalog_path.read_bytes())
        self.entry["manifest"]["version"] = "2.0.0"
        self.write_signed(self.body)
        self.assertFalse(index.check(self.catalog_path, base_catalog=base, repository_id=publisher.CLEANED_REPOSITORY_ID))

    def test_same_raw_payload_signature_conversion_is_allowed(self):
        base = self.directory / "base.json"
        base.write_bytes(self.payload_path.read_bytes())
        sig = catalog.loads(self.catalog_path.read_bytes())["signatures"][0]
        self.assertTrue(index.check(self.catalog_path, base_catalog=base, base_signature=sig,
                                    repository_id=publisher.CLEANED_REPOSITORY_ID))

    def test_signed_continuation_cannot_replay_after_another_merge(self):
        base = self.directory / "base.json"
        base.write_bytes(self.catalog_path.read_bytes())
        raw = catalog.verified(base)[0]
        self.body["generation"] = {"repository": publisher.FINAL_REPOSITORY, "repository_id": publisher.CLEANED_REPOSITORY_ID,
                                   "base_catalog_hash": oci.digest(raw), "source_commit": "1" * 40,
                                   "source_inventory_hash": "sha256:" + "2" * 64, "mode": "versions"}
        self.entry["manifest"]["version"] = "2.0.0"
        self.write_signed(self.body)
        with mock.patch.object(index, "check_source", return_value=None):
            self.assertTrue(index.check(self.catalog_path, base_catalog=base, repository_id=publisher.CLEANED_REPOSITORY_ID,
                                        base_ref="3" * 40))
            stale = self.catalog_path.read_bytes()
            base.write_bytes(stale)
            self.entry["manifest"]["version"] = "3.0.0"
            self.body["generation"]["base_catalog_hash"] = oci.digest(catalog.verified(base)[0])
            self.write_signed(self.body)
            base.write_bytes(self.catalog_path.read_bytes())
            self.catalog_path.write_bytes(stale)
            self.assertFalse(index.check(self.catalog_path, base_catalog=base, repository_id=publisher.CLEANED_REPOSITORY_ID,
                                         base_ref="3" * 40))


class GitContinuityRegressions(CatalogFixture, unittest.TestCase):
    def setUp(self):
        super().setUp()
        self.repo = self.directory / "repo"
        self.repo.mkdir()
        self.run_git("init", "-q")
        patch = mock.patch.object(index, "ROOT", str(self.repo))
        patch.start()
        self.addCleanup(patch.stop)
        self.source_path = self.repo / "extensions/demo/persona-extension.json"
        self.source_path.parent.mkdir(parents=True)
        self.source_path.write_bytes(catalog.encode(dict(self.entry["manifest"], tags=self.entry["tags"])))
        (self.repo / "persona-extensions.json").write_bytes(self.catalog_path.read_bytes())
        self.original = self.commit("Initial synthetic catalog")
        self.pr_base = self.original

    def run_git(self, *args):
        return subprocess.run(["git", "-C", str(self.repo), *args], check=True, capture_output=True, timeout=30).stdout

    def commit(self, message):
        self.run_git("add", ".")
        self.run_git("-c", "user.name=Fixture", "-c", "user.email=me@example.com", "commit", "-qm", message)
        return self.run_git("rev-parse", "HEAD").decode().strip()

    def candidate(self):
        base_raw = index.tree_catalog(self.pr_base)[0]
        self.body["extensions"][0]["manifest"]["version"] = "2.0.0"
        self.body["generation"] = {"repository": catalog.FINAL_REPOSITORY, "repository_id": catalog.CLEANED_REPOSITORY_ID,
                                   "base_catalog_hash": catalog.payload_hash(base_raw), "source_commit": self.pr_base,
                                   "source_inventory_hash": catalog.inventory_hash(index.tree_inventory(self.pr_base)), "mode": "versions"}
        self.write_signed(self.body)

    def advance_source(self):
        self.source_path.write_bytes(catalog.encode(dict(self.entry["manifest"], version="2.0.0", tags=self.entry["tags"])))
        self.pr_base = self.commit("Advance source version")

    def check(self, **changes):
        return index.check(self.catalog_path, base_ref=changes.get("base_ref", self.pr_base),
                           repository_id=changes.get("repository_id", catalog.CLEANED_REPOSITORY_ID))

    def test_real_pr_base_and_current_signed_catalog_pass(self):
        self.advance_source()
        self.candidate()
        self.assertTrue(self.check())
        example = os.environ.get("PERSONA_SYNTHETIC_PROVENANCE_EXAMPLE")
        if example:
            with open(example, "xb") as output:
                output.write(self.catalog_path.read_bytes())

    def test_old_valid_signed_artifact_fails_against_new_real_base(self):
        self.advance_source()
        self.candidate()
        stale = self.catalog_path.read_bytes()
        (self.repo / "persona-extensions.json").write_bytes(stale)
        self.pr_base = self.commit("Merge newer catalog")
        self.body["extensions"][0]["tags"] = ["new"]
        self.source_path.write_bytes(catalog.encode(dict(self.entry["manifest"], tags=["new"])))
        self.pr_base = self.commit("Advance source tags")
        self.body["generation"]["base_catalog_hash"] = catalog.payload_hash(index.tree_catalog(self.pr_base)[0])
        self.body["generation"]["source_commit"] = self.pr_base
        self.body["generation"]["source_inventory_hash"] = catalog.inventory_hash(index.tree_inventory(self.pr_base))
        self.write_signed(self.body)
        self.assertTrue(self.check())
        (self.repo / "persona-extensions.json").write_bytes(self.catalog_path.read_bytes())
        self.pr_base = self.commit("Merge current catalog")
        self.catalog_path.write_bytes(stale)
        self.assertFalse(self.check())

    def test_docs_only_base_advance_keeps_valid_signed_continuation(self):
        self.advance_source()
        self.candidate()
        (self.repo / "NOTES.txt").write_text("Synthetic documentation change\n")
        self.pr_base = self.commit("Advance documentation only")
        self.assertTrue(self.check())

    def test_signed_source_inventory_cannot_be_replayed_after_source_removal(self):
        self.advance_source()
        self.candidate()
        self.source_path.unlink()
        self.pr_base = self.commit("Remove source extension")
        self.assertFalse(self.check())

    def test_foreign_repo_nonancestor_or_forged_inventory_fails(self):
        self.advance_source()
        self.candidate()
        self.assertFalse(self.check(repository_id=123))
        self.body["generation"]["source_commit"] = "0" * 40
        self.write_signed(self.body)
        self.assertFalse(self.check())
        self.body["generation"]["source_commit"] = self.pr_base
        self.body["generation"]["source_inventory_hash"] = "sha256:" + "0" * 64
        self.write_signed(self.body)
        self.assertFalse(self.check())

    def test_rename_declaration_must_match_real_signed_source_inventory(self):
        source = dict(self.entry['manifest'], id='renamed', version='2.0.0',
                      tags=self.entry['tags'], replaces='demo')
        target = self.repo / 'extensions/renamed/persona-extension.json'
        target.parent.mkdir(parents=True)
        target.write_bytes(catalog.encode(source))
        self.source_path.unlink()
        self.pr_base = self.commit('Declare a synthetic replacement')
        self.body['extensions'] = [dict(self.entry, manifest={k: v for k, v in source.items()
                                                            if k not in ('tags', 'replaces')},
                                       replaces='demo')]
        self.body['generation'] = {
            'repository': catalog.FINAL_REPOSITORY, 'repository_id': catalog.CLEANED_REPOSITORY_ID,
            'base_catalog_hash': catalog.payload_hash(index.tree_catalog(self.pr_base)[0]),
            'source_commit': self.pr_base,
            'source_inventory_hash': catalog.inventory_hash(index.tree_inventory(self.pr_base)),
            'mode': 'versions'}
        self.write_signed(self.body)
        self.assertTrue(self.check())
        for predecessor in ('unrelated', None):
            with self.subTest(predecessor=predecessor):
                if predecessor is None:
                    self.body['extensions'][0].pop('replaces')
                else:
                    self.body['extensions'][0]['replaces'] = predecessor
                # A valid fresh signature cannot override reviewed source ownership.
                self.write_signed(self.body)
                self.assertFalse(self.check())

    def test_initial_conversion_against_real_raw_base_tree(self):
        raw, _ = self.write_signed(self.body)
        signature = catalog.loads(self.catalog_path.read_bytes())["signatures"][0]
        (self.repo / "persona-extensions.json").write_bytes(raw)
        (self.repo / "persona-extensions.json.sig").write_bytes(catalog.encode(signature))
        self.pr_base = self.commit("Synthetic detached catalog")
        self.assertTrue(self.check())
        self.entry["manifest"]["version"] = "9.0.0"
        self.write_signed(self.body)
        self.assertFalse(self.check())


class FrozenArchiveRegressions(unittest.TestCase):
    @unittest.skipUnless(os.environ.get("PERSONA_FROZEN_ARCHIVE_AUDIT"), "optional frozen archive evidence is not supplied")
    def test_all_fourteen_original_archives_are_still_installable_and_unchanged(self):
        audit = pathlib.Path(os.environ["PERSONA_FROZEN_ARCHIVE_AUDIT"])
        _, body = catalog.verified(catalog.CATALOG)
        self.assertEqual(len(body["extensions"]), 14)
        for entry in body["extensions"]:
            for descriptor in entry["packages"]:
                name = descriptor["url"].split("/", 1)[1]
                originals = list(audit.glob("*/" + name))
                self.assertEqual(len(originals), 1)
                # Each untrusted archive is copied to its own new empty directory, never executed.
                with tempfile.TemporaryDirectory(prefix="frozen-archive-") as directory:
                    path = pathlib.Path(directory) / name
                    shutil.copyfile(originals[0], path)
                    data = path.read_bytes()
                    publisher.archive_matches(data, descriptor)
                    publisher.check_archive(data, entry["manifest"], platform=catalog.package_platform(descriptor))
                print(entry["manifest"]["id"] + ": frozen archive checksum/size/files PASS")


class WorkflowRegressions(unittest.TestCase):
    def test_each_fixed_artifact_name_overwrites_on_rerun(self):
        text = (catalog.ROOT / ".github/workflows/ci.yml").read_text()
        publication = text.split("\n  packages:\n", 1)[1]
        uploads = publication.split("uses: actions/upload-artifact@v4")[1:]
        self.assertEqual(len(uploads), 3)
        for block in uploads:
            self.assertIn("overwrite: true", block.split("\n  anonymous:")[0].split("\n  sign:")[0])

    def test_automatic_gate_needs_no_repository_setting(self):
        text = (catalog.ROOT / ".github/workflows/ci.yml").read_text()
        self.assertNotIn("PERSONA_OCI_CUTOVER_READY", text)
        self.assertIn('"$GITHUB_REF" = refs/heads/main', text)
        self.assertNotIn("GITHUB_REF_NAME", text)
        self.assertNotIn("feat/146-oci-extension-market", text.split("workflow_dispatch:", 1)[0])
        self.assertIn('"$GITHUB_REPOSITORY_ID" = 1449704212', text)

    def test_version_domain_and_tag_length_are_explicit(self):
        for value in ("01.2.3", "1.2.3-01", "1.2.3-..", "1.2.3+build", "1.2.3_p_linux", "1.2.3-" + "a" * 91):
            with self.subTest(version=value), self.assertRaises(ValueError):
                oci.version_tag(value)
        value = "1.2.3-" + "a" * 90
        self.assertEqual(len(value), 96)
        self.assertLessEqual(len(oci.version_tag(value, "windows-arm64")), 128)
        self.assertTrue(catalog.valid_version(value))
        self.assertFalse(catalog.valid_version("1.2.3-01"))

    def test_identity_shell_checks_only_before_cutover_and_on_issue_refs(self):
        text = (catalog.ROOT / ".github/workflows/ci.yml").read_text()
        script = text.split("        run: |\n", 1)[1].split("\n  check:", 1)[0]
        script = "\n".join(line[10:] for line in script.splitlines())
        with tempfile.TemporaryDirectory(prefix="workflow-gate-test-") as directory:
            root = pathlib.Path(directory)
            executable = root / "python3"
            # Synthetic stand-in for the separate remote identity gate: no API/registry access.
            executable.write_text('#!/bin/sh\n[ "$GITHUB_REPOSITORY" = nwverse/persona-extensions ] && '
                                  '[ "$GITHUB_REPOSITORY_ID" = 1449704212 ] && [ "$GITHUB_REF" = refs/heads/main ]\n')
            executable.chmod(0o755)
            main = "refs/heads/main"
            cases = [("push", "nwverse/persona-extensions-next", "1449704212", main, "", False, 0),
                     ("push", "nwverse/persona-extensions", "123", main, "", False, 0),
                     ("push", "nwverse/persona-extensions", "1449704212", "refs/heads/feat/146-oci-extension-market", "", False, 0),
                     ("push", "nwverse/persona-extensions", "1449704212", "refs/tags/main", "", False, 0),
                     ("push", "nwverse/persona-extensions", "1449704212", main, "", True, 0),
                     ("workflow_dispatch", "nwverse/persona-extensions", "1449704212", main, "false", False, 1),
                     ("workflow_dispatch", "nwverse/persona-extensions-next", "1449704212", main, "true", False, 1),
                     ("workflow_dispatch", "nwverse/persona-extensions", "1449704212", main, "true", True, 0)]
            for number, (event, repository, repository_id, ref, confirm, publish, status) in enumerate(cases):
                output = root / ("output-" + str(number))
                env = dict(os.environ, PATH=str(root) + ":/usr/bin:/bin", GITHUB_OUTPUT=str(output),
                           GITHUB_EVENT_NAME=event, GITHUB_REPOSITORY=repository, GITHUB_REPOSITORY_ID=repository_id,
                           GITHUB_REF=ref, CONFIRM=confirm)
                result = subprocess.run(["/bin/bash", "-e", "-c", script], cwd=catalog.ROOT, env=env,
                                        capture_output=True, timeout=10)
                with self.subTest(event=event, ref=ref, repo=repository):
                    self.assertEqual(result.returncode, status)
                    self.assertEqual(output.exists() and "publish=true" in output.read_text(), publish)

    def test_hub_platforms_field_is_the_only_platform_field(self):
        # The hub reads `platforms`; it ignores a singular `platform` and would install the package anywhere.
        self.assertEqual(catalog.package_platform({}), "any")
        self.assertEqual(catalog.package_platform({"platforms": ["linux-arm64"]}), "linux-arm64")
        for package in ({"platform": "linux-amd64"}, {"platform": "any"}, {"platforms": ["any"]}, {"platforms": "linux-amd64"},
                        {"platforms": ["linux-amd64", "linux-arm64"]}, {"platforms": []}, {"platforms": ["plan9-amd64"]}):
            with self.subTest(package=package), self.assertRaises(ValueError):
                catalog.package_platform(package)

    def test_unchanged_plan_is_not_signed(self):
        with tempfile.TemporaryDirectory(prefix="noop-plan-") as directory:
            plan = pathlib.Path(directory) / "plan.json"
            plan.write_bytes(catalog.encode({"catalog_update_required": False, "entries": [], "removed_ids": []}))
            args = argparse.Namespace(plan=str(plan), catalog=catalog.CATALOG, head_sha="0" * 40,
                                      output=str(pathlib.Path(directory) / "out"))
            with mock.patch.object(publisher, "cutover_gate"), \
                    mock.patch.object(publisher, "generate_payload") as generate:
                with self.assertRaisesRegex(ValueError, "nothing to sign"):
                    publisher.catalog_phase(args)
                generate.assert_not_called()
            self.assertFalse((pathlib.Path(directory) / "out").exists())

    def test_version_platform_tuple_is_injective(self):
        self.assertNotEqual(oci.version_tag("1.2.3", "linux-amd64"), oci.version_tag("1.2.3-linux-amd64", "any"))
        self.assertNotEqual(oci.filename("demo", "1.2.3", "linux-amd64"), oci.filename("demo", "1.2.3-linux-amd64", "any"))
        self.assertEqual(oci.version_tag("1.2.3"), "1.2.3")
        self.assertEqual(oci.filename("demo", "1.2.3"), "demo-1.2.3.tar.gz")


if __name__ == "__main__":
    unittest.main()
