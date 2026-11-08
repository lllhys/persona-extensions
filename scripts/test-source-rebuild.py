#!/usr/bin/env python3
"""Pure guard regressions; these do not execute a local standalone rebuild."""
import importlib.util
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("source_rebuild", ROOT / "scripts/source-rebuild.py")
rebuild = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rebuild)


class SourceRebuildTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.extension = self.root / "extensions/example"
        self.extension.mkdir(parents=True)
        self.config = {"version": 1, "bundle": "source-reference/bundle.mjs", "build": "build:source",
                       "output": "build/example.mjs", "archive": "build/example.tar.gz",
                       "extracted_extension": "extensions/example", "extracted_sdk": "sdk/ts"}
        self.tracked = {"extensions/example/package.json", "extensions/example/source-rebuild.json",
                        "extensions/example/source-reference/bundle.mjs"}
        (self.extension / "package.json").write_text(json.dumps({"scripts": {"build:source": "node scripts/build.mjs"}}))
        (self.extension / "source-reference").mkdir()
        (self.extension / "source-reference/bundle.mjs").write_bytes(b"export default {};\n")
        self.write_config()

    def write_config(self):
        (self.extension / "source-rebuild.json").write_text(json.dumps(self.config))

    def test_committed_source_profile_is_selected(self):
        self.assertEqual(rebuild.profiles(self.root, self.tracked), {"example": self.config})

    def test_new_source_cannot_omit_registration(self):
        self.tracked.remove("extensions/example/source-rebuild.json")
        with self.assertRaisesRegex(ValueError, "lacks a rebuild profile"):
            rebuild.profiles(self.root, self.tracked)

    def test_reference_cannot_be_an_uncommitted_build(self):
        self.tracked.remove("extensions/example/source-reference/bundle.mjs")
        with self.assertRaisesRegex(ValueError, "must be committed"):
            rebuild.profiles(self.root, self.tracked)
        self.config["bundle"] = "build/example.mjs"
        self.write_config()
        self.tracked.add("extensions/example/build/example.mjs")
        with self.assertRaisesRegex(ValueError, "must be committed"):
            rebuild.profiles(self.root, self.tracked)

    def test_invalid_profile_shape_and_build_fail(self):
        for field, value in (("version", True), ("version", 2), ("build", "test"), ("output", "../outside.mjs")):
            original = self.config[field]
            self.config[field] = value
            self.write_config()
            with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                rebuild.profiles(self.root, self.tracked)
            self.config[field] = original
        self.config["unexpected"] = "ignored"
        self.write_config()
        with self.assertRaises(ValueError):
            rebuild.profiles(self.root, self.tracked)

    def test_reference_is_nonempty_and_bounded(self):
        bundle = self.extension / "source-reference/bundle.mjs"
        for data in (b"", b"x" * (rebuild.MAX_BUNDLE + 1)):
            bundle.write_bytes(data)
            with self.assertRaisesRegex(ValueError, "bundle size"):
                rebuild.profiles(self.root, self.tracked)

    def test_same_size_different_bytes_fail(self):
        actual = self.root / "actual.mjs"
        actual.write_bytes(b"one")
        self.assertEqual(rebuild.compare(b"one", actual, "fixture")["bytes"], 3)
        with self.assertRaisesRegex(ValueError, "byte-for-byte"):
            rebuild.compare(b"two", actual, "fixture")

    def test_paths_reject_escapes_and_symlinks(self):
        for name in ("../outside", "/absolute", "sdk/../outside", "sdk\\outside", "./sdk", ""):
            with self.subTest(name=name), self.assertRaises(ValueError):
                rebuild.contained(self.root, name, required=False)
        (self.root / "linked").symlink_to(self.extension, target_is_directory=True)
        with self.assertRaises(ValueError):
            rebuild.contained(self.root, "linked/package.json")

    def archive(self, entries):
        path = self.root / "authoring.tar.gz"
        with tarfile.open(path, "w:gz") as handle:
            for name, kind in entries:
                member = tarfile.TarInfo(name)
                member.type = kind
                if kind == tarfile.REGTYPE:
                    member.size = 3
                    handle.addfile(member, io.BytesIO(b"abc"))
                else:
                    member.linkname = "../outside"
                    handle.addfile(member)
        target = self.root / "unpacked"
        target.mkdir()
        return path, target

    def test_archive_extracts_only_regular_contained_inputs(self):
        archive, target = self.archive([("./sdk/ts/package.json", tarfile.REGTYPE)])
        rebuild.extract(archive, target)
        self.assertEqual((target / "sdk/ts/package.json").read_bytes(), b"abc")

    def test_archive_rejects_escape(self):
        archive, target = self.archive([("../outside", tarfile.REGTYPE)])
        with self.assertRaises(ValueError):
            rebuild.extract(archive, target)
        self.assertFalse((self.root.parent / "outside").exists())

    def test_archive_rejects_links_and_devices(self):
        for kind in (tarfile.SYMTYPE, tarfile.LNKTYPE, tarfile.CHRTYPE, tarfile.FIFOTYPE):
            archive, target = self.archive([("link", kind)])
            with self.subTest(kind=kind), self.assertRaises(ValueError):
                rebuild.extract(archive, target)
            target.rmdir()

    def test_archive_rejects_duplicate_files(self):
        archive, target = self.archive([("same", tarfile.REGTYPE), ("same", tarfile.REGTYPE)])
        with self.assertRaisesRegex(ValueError, "duplicate"):
            rebuild.extract(archive, target)

    def test_local_and_self_hosted_rebuilds_are_refused_before_build(self):
        for action, environment in (("false", "github-hosted"), ("true", "self-hosted")):
            with patch.dict(os.environ, {"GITHUB_ACTIONS": action, "RUNNER_ENVIRONMENT": environment}):
                with patch.object(rebuild, "build") as build:
                    with self.assertRaisesRegex(ValueError, "fresh GitHub-hosted"):
                        rebuild.rebuild(self.root, "example", self.config, self.tracked, self.root)
                    build.assert_not_called()

    def test_required_gate_includes_all_source_matrix_results(self):
        workflow = (ROOT / ".github/workflows/ci.yml").read_text()
        self.assertIn("needs: [check, source-plan, source-rebuild]", workflow)
        self.assertIn("SOURCE_REBUILD_RESULT: ${{ needs.source-rebuild.result }}", workflow)
        self.assertIn('if [ "$SOURCE_REBUILD_RESULT" != success ]; then', workflow)
        self.assertIn("fail-fast: false", workflow)
        self.assertIn("max-parallel: 4", workflow)
        block = workflow.split("  source-rebuild:\n", 1)[1].split("  ci-required:\n", 1)[0]
        self.assertIn("overwrite: true", block)
        self.assertNotIn("cache:", block)
        self.assertNotIn("HOME:", block)
        self.assertNotIn("continue-on-error", block)


if __name__ == "__main__":
    unittest.main()
