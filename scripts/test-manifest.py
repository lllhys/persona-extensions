#!/usr/bin/env python3
"""Tests for scripts/manifest.py: what scripts/ci.sh release picks and the check() invariants.
Run: python3 -I scripts/test-manifest.py."""
import importlib.util
import io
import pathlib
import shutil
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stderr

spec = importlib.util.spec_from_file_location('manifest', pathlib.Path(__file__).with_name('manifest.py'))
manifest = importlib.util.module_from_spec(spec)
spec.loader.exec_module(manifest)


class SelectedTests(unittest.TestCase):
    def setUp(self):
        self.id = manifest.ids()[0]

    def test_an_id_as_release_sh_takes_it(self):
        for arg in (self.id, self.id + '/', 'extensions/' + self.id, 'extensions/' + self.id + '/'):
            self.assertEqual(manifest.selected([arg]), [self.id], arg)

    def test_a_commit_is_a_base(self):
        self.assertEqual(manifest.selected(['HEAD']), manifest.changed('HEAD'))

    def test_neither_an_id_nor_a_commit_is_refused(self):
        # A misspelled id, or a word a glob expanded to, must not pass for a base where every extension is new.
        for arg in ('no-such-extension', 'README.md', '-h', ''):
            with self.assertRaises(SystemExit, msg=arg):
                manifest.selected([self.id, arg])

    def test_changed_refuses_an_unknown_ref(self):
        # Not "every extension changed": git can't tell what was there at a ref it doesn't have.
        r = subprocess.run([sys.executable, '-I', manifest.__file__, 'changed', 'no-such-ref'], capture_output=True, text=True)
        self.assertNotEqual(r.returncode, 0, r.stdout)
        self.assertEqual(r.stdout, '')

    def test_a_duplicate_key_is_rejected(self):
        # Two "version" lines once shipped silently (json last-wins); load must refuse instead.
        d = pathlib.Path(tempfile.mkdtemp())
        ext = d / 'extensions' / 'dup'
        ext.mkdir(parents=True)
        (ext / manifest.NEW).write_text('{"id": "dup", "version": "0.1.0", "version": "0.1.1"}\n')
        old_root = manifest.ROOT
        manifest.ROOT = str(d)
        try:
            with self.assertRaises(ValueError):
                manifest.load('dup')
        finally:
            manifest.ROOT = old_root


class JunkTests(unittest.TestCase):
    """macOS/editor junk (AppleDouble ._*, .DS_Store, __pycache__) under skills/assistant must fail the check:
    scripts/package.sh copies those trees verbatim, so the junk would ship (issue #99)."""

    def make_ext(self, files):
        d = pathlib.Path(tempfile.mkdtemp())
        ext = d / 'extensions' / 'junk'
        ext.mkdir(parents=True)
        (ext / manifest.NEW).write_text('{"id": "junk", "version": "0.1.0"}\n')
        for rel in files:
            p = ext / rel
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text('')
        self.addCleanup(shutil.rmtree, d)
        return d

    def with_root(self, d):
        self._old_root = manifest.ROOT
        manifest.ROOT = str(d)
        self.addCleanup(self._restore_root)

    def _restore_root(self):
        manifest.ROOT = self._old_root

    def test_junk_under_skills_and_assistant_is_rejected(self):
        planted = [
            'skills/._SKILL.md',
            'skills/.DS_Store',
            'skills/review/__pycache__/mod.pyc',
            'skills/review/._SKILL.md',
            'assistant/.DS_Store',
            'assistant/._answer-style.md',
            # A real skill next to the junk must still be there.
            'skills/review/SKILL.md',
            'assistant/answer-style.md',
        ]
        d = self.make_ext(planted)
        self.with_root(d)
        bad = manifest.check_junk('junk')
        flagged = {b.split(': ', 1)[1].split(' is packaging junk')[0] for b in bad}
        self.assertEqual(flagged, {
            'skills/._SKILL.md',
            'skills/.DS_Store',
            'skills/review/__pycache__',
            'skills/review/._SKILL.md',
            'assistant/.DS_Store',
            'assistant/._answer-style.md',
        })
        with redirect_stderr(io.StringIO()):
            self.assertFalse(manifest.check())

    def test_a_clean_tree_passes(self):
        d = self.make_ext(['skills/review/SKILL.md', 'assistant/answer-style.md'])
        self.with_root(d)
        self.assertEqual(manifest.check_junk('junk'), [])
        self.assertTrue(manifest.check())

    def test_junk_outside_packaged_dirs_is_not_scanned(self):
        # package.sh copies only skills/ and assistant/, so junk elsewhere cannot land in the tarball.
        d = self.make_ext(['.DS_Store', '._foo', 'src/.DS_Store'])
        self.with_root(d)
        self.assertEqual(manifest.check_junk('junk'), [])


if __name__ == '__main__':
    unittest.main()
