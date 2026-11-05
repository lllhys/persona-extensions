#!/usr/bin/env python3
"""Tests for what scripts/ci.sh release picks from its arguments: python3 -I scripts/test-manifest.py."""
import importlib.util
import pathlib
import subprocess
import sys
import tempfile
import unittest

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


if __name__ == '__main__':
    unittest.main()
