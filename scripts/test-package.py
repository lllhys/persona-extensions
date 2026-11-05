#!/usr/bin/env python3
"""Regression test for scripts/package.sh: macOS/editor junk already in an extension's source tree must never
land in the package tarball (issue #99). A small declarative extension (no runtime, so no Go toolchain needed)
is staged in a temp directory with copies of the packaging scripts; ._* AppleDouble files, .DS_Store and a
__pycache__ directory are planted under skills/ and assistant/, and the built tarball is inspected.

Run: python3 -I scripts/test-package.py
"""
import importlib.util
import json
import pathlib
import shutil
import subprocess
import tarfile
import tempfile
import unittest

REPO = pathlib.Path(__file__).resolve().parent.parent
_spec = importlib.util.spec_from_file_location('manifest', pathlib.Path(__file__).with_name('manifest.py'))
manifest = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(manifest)

EXT_ID = 'starter-kit'
# Junk planted in the source tree: every basename here must be filtered out by scripts/package.sh.
JUNK = [
    'skills/._SKILL.md',
    'skills/.DS_Store',
    'skills/weekly-review/._SKILL.md',
    'skills/weekly-review/__pycache__/mod.pyc',
    'assistant/.DS_Store',
    'assistant/._answer-style.md',
]
# Real files that must still ship despite the exclusions.
KEEP = [
    'skills/weekly-review/SKILL.md',
    'assistant/answer-style.md',
]
# Names that look like junk but must NOT be matched by the basename patterns (no prefix/glob over-reach).
NEAR_MISS = [
    'skills/weekly-review/my._keep',
    'skills/.DS_Store.bak',
    'skills/__pycache__old/note.txt',
]


# Independent oracle for tar members: written from the issue text, not from manifest.is_junk, so a shared
# pattern bug cannot make both the exclusion and its test green together.
def _is_junk_entry(part):
    return part == '.DS_Store' or part == '__pycache__' or part.startswith('._')


class PackageJunkTests(unittest.TestCase):
    def setUp(self):
        self.tmp = pathlib.Path(tempfile.mkdtemp())
        (self.tmp / 'scripts').mkdir()
        (self.tmp / 'extensions').mkdir()
        # package.sh invokes scripts/manifest.py relative to its own location, so stage both.
        for name in ('package.sh', 'manifest.py'):
            shutil.copy2(REPO / 'scripts' / name, self.tmp / 'scripts' / name)
        shutil.copytree(REPO / 'extensions' / EXT_ID, self.tmp / 'extensions' / EXT_ID)
        ext = self.tmp / 'extensions' / EXT_ID
        for rel in JUNK + NEAR_MISS:
            p = ext / rel
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_bytes(b'junk')
        self.addCleanup(shutil.rmtree, self.tmp)

    def test_junk_planted_in_source_never_lands_in_tarball(self):
        r = subprocess.run(['bash', 'scripts/package.sh', EXT_ID], cwd=self.tmp,
                           capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stderr + r.stdout)
        version = json.loads((self.tmp / 'extensions' / EXT_ID / manifest.NEW).read_text())['version']
        tarball = self.tmp / 'dist' / ('%s-%s.tar.gz' % (EXT_ID, version))
        self.assertTrue(tarball.is_file())
        with tarfile.open(tarball, 'r:gz') as t:
            names = t.getnames()
        # No path component is a junk name: catches files and excluded directories at every depth.
        for n in names:
            for part in n.split('/'):
                self.assertFalse(_is_junk_entry(part), 'junk entry in tarball: %s' % n)
        members = {n.split('/', 1)[1] for n in names if '/' in n}
        for rel in KEEP + NEAR_MISS + ['persona-extension.json', 'persona-plugin.json']:
            self.assertIn(rel, members)
        # The junk really was in the source tree, so the tar flags did the filtering (not an empty fixture).
        for rel in JUNK:
            self.assertTrue((self.tmp / 'extensions' / EXT_ID / rel).is_file(), rel)


if __name__ == '__main__':
    unittest.main()
